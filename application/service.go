// Package application coordinates lobby commands, game binding, and pushing
// committed changes. Every command applies due lease and grace expiry first, so
// requests (heartbeats included) and subscription proxy calls drive expiry; no
// background work runs. It depends on domain contracts; persistence and
// transports are supplied by callers.
package application

import (
	"context"
	"errors"
	"time"

	"islandBattles/lobby/domain"
)

type Service struct {
	store       domain.Store
	clock       domain.Clock
	identifiers domain.IdentifierSource
	policy      domain.Policy
}

func NewService(store domain.Store, clock domain.Clock, identifiers domain.IdentifierSource, policy domain.Policy) (*Service, error) {
	if store == nil || clock == nil || identifiers == nil {
		return nil, domain.ErrInvalidInput
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	policy.RegisteredGameKeys = append([]string{}, policy.RegisteredGameKeys...)
	return &Service{store: store, clock: clock, identifiers: identifiers, policy: policy}, nil
}

// TransactNormalized commits deadline cleanup even if the requested command is
// rejected. Commands run on a detached copy so their partial writes cannot leak.
func TransactNormalized(ctx context.Context, store domain.Store, clock domain.Clock, policy domain.Policy, scope domain.Scope, command domain.Mutation) (domain.Aggregate, error) {
	if err := ctx.Err(); err != nil {
		return domain.Aggregate{}, err
	}
	if command == nil {
		return domain.Aggregate{}, domain.ErrInvalidInput
	}
	return transactNormalizedTimed(ctx, store, clock, policy, scope, func(aggregate *domain.Aggregate, _ time.Time) (domain.Change, error) { return command(aggregate) })
}

type timedMutation func(*domain.Aggregate, time.Time) (domain.Change, error)

func transactNormalizedTimed(ctx context.Context, store domain.Store, clock domain.Clock, policy domain.Policy, scope domain.Scope, command timedMutation) (domain.Aggregate, error) {
	if store == nil || clock == nil || command == nil {
		return domain.Aggregate{}, domain.ErrInvalidInput
	}
	var commandError error
	aggregate, err := store.Transact(ctx, scope, func(loaded *domain.Aggregate) (domain.Change, error) {
		now := clock.Now().UTC()
		normalization, err := domain.NormalizeExpired(loaded, now, policy)
		if err != nil {
			return domain.Change{}, err
		}
		candidate := loaded.DeepCopy()
		mutation, err := command(&candidate, now)
		if err != nil {
			commandError = err
			return normalization, nil
		}
		*loaded = candidate
		return domain.MergeChanges(normalization, mutation), nil
	})
	if err != nil {
		return domain.Aggregate{}, err
	}
	if commandError != nil {
		return domain.Aggregate{}, commandError
	}
	return aggregate, nil
}

func (service *Service) runMutation(ctx context.Context, scope domain.Scope, command timedMutation) (domain.Aggregate, error) {
	if err := service.policy.ValidateScope(scope); err != nil {
		return domain.Aggregate{}, err
	}
	return transactNormalizedTimed(ctx, service.store, service.clock, service.policy, scope, command)
}

func validatePrincipal(principal domain.Principal) error {
	if domain.ValidateUUID(principal.PlayerID) != nil || domain.ValidateUUID(principal.SessionID) != nil {
		return domain.ErrUnauthenticated
	}
	return nil
}

func activeMember(aggregate *domain.Aggregate, playerID string) (*domain.Member, error) {
	member := domain.FindMember(aggregate, playerID)
	if member == nil || !member.Active {
		return nil, domain.ErrNotFound
	}
	return member, nil
}

func unavailable(cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return &domain.Error{Code: domain.CodeUnavailable, Message: domain.ErrUnavailable.Message, Cause: cause}
}

var _ domain.Application = (*Service)(nil)
