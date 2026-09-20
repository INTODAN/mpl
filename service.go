package lobby

import (
	"context"
	"errors"
	"time"
)

type Service struct {
	store       Store
	clock       Clock
	identifiers IdentifierSource
	policy      Policy
}

func NewService(store Store, clock Clock, identifiers IdentifierSource, policy Policy) (*Service, error) {
	if store == nil || clock == nil || identifiers == nil {
		return nil, ErrInvalidInput
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	policy.RegisteredGameKeys = append([]string{}, policy.RegisteredGameKeys...)
	return &Service{store: store, clock: clock, identifiers: identifiers, policy: policy}, nil
}

// TransactNormalized commits deadline cleanup even if the requested command is
// rejected. Commands run on a detached copy so their partial writes cannot leak.
func TransactNormalized(ctx context.Context, store Store, clock Clock, policy Policy, scope Scope, command Mutation) (Aggregate, error) {
	if err := ctx.Err(); err != nil {
		return Aggregate{}, err
	}
	if command == nil {
		return Aggregate{}, ErrInvalidInput
	}
	return transactNormalizedTimed(ctx, store, clock, policy, scope, func(aggregate *Aggregate, _ time.Time) (Change, error) { return command(aggregate) })
}

type timedMutation func(*Aggregate, time.Time) (Change, error)

func transactNormalizedTimed(ctx context.Context, store Store, clock Clock, policy Policy, scope Scope, command timedMutation) (Aggregate, error) {
	if store == nil || clock == nil || command == nil {
		return Aggregate{}, ErrInvalidInput
	}
	var commandError error
	aggregate, err := store.Transact(ctx, scope, func(loaded *Aggregate) (Change, error) {
		now := clock.Now().UTC()
		normalization, err := NormalizeExpired(loaded, now, policy)
		if err != nil {
			return Change{}, err
		}
		candidate := loaded.DeepCopy()
		mutation, err := command(&candidate, now)
		if err != nil {
			commandError = err
			return normalization, nil
		}
		*loaded = candidate
		return MergeChanges(normalization, mutation), nil
	})
	if err != nil {
		return Aggregate{}, err
	}
	if commandError != nil {
		return Aggregate{}, commandError
	}
	return aggregate, nil
}

func (service *Service) runMutation(ctx context.Context, scope Scope, command timedMutation) (Aggregate, error) {
	if err := service.policy.ValidateScope(scope); err != nil {
		return Aggregate{}, err
	}
	return transactNormalizedTimed(ctx, service.store, service.clock, service.policy, scope, command)
}

func validatePrincipal(principal Principal) error {
	if ValidateUUID(principal.PlayerID) != nil || ValidateUUID(principal.SessionID) != nil {
		return ErrUnauthenticated
	}
	return nil
}

func activeMember(aggregate *Aggregate, playerID string) (*Member, error) {
	member := FindMember(aggregate, playerID)
	if member == nil || !member.Active {
		return nil, ErrNotFound
	}
	return member, nil
}

func unavailable(cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return &Error{Code: CodeUnavailable, Message: ErrUnavailable.Message, Cause: cause}
}

var _ Application = (*Service)(nil)
