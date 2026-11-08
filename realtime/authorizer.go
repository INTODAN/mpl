// Package realtime adapts authenticated Centrifugo subscriptions and publications
// to the generic lobby. It does not create membership on socket connection.
package realtime

import (
	"context"
	"errors"
	"islandBattles/lobby/application"
	"islandBattles/lobby/domain"
)

type Authorizer struct {
	identities domain.IdentityStore
	store      domain.Store
	clock      domain.Clock
	policy     domain.Policy
}

func NewAuthorizer(identities domain.IdentityStore, store domain.Store, clock domain.Clock, policy domain.Policy) (*Authorizer, error) {
	if identities == nil || store == nil || clock == nil {
		return nil, domain.ErrInvalidInput
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &Authorizer{identities: identities, store: store, clock: clock, policy: policy}, nil
}

func (authorizer *Authorizer) AuthorizeSubscription(ctx context.Context, playerID, channelName string) (domain.SubscriptionAuthorization, error) {
	if domain.ValidateUUID(playerID) != nil {
		return domain.SubscriptionAuthorization{}, domain.ErrPermissionDenied
	}
	channel, err := domain.ParseChannel(channelName)
	if err != nil {
		return domain.SubscriptionAuthorization{}, domain.ErrPermissionDenied
	}
	identity, err := authorizer.identities.LookupByPlayer(ctx, playerID)
	if err != nil {
		return domain.SubscriptionAuthorization{}, unavailable(err)
	}
	now := authorizer.clock.Now().UTC()
	if identity == nil || identity.Principal.PlayerID != playerID || !now.Before(identity.ExpiresAt) {
		return domain.SubscriptionAuthorization{}, domain.ErrUnauthenticated
	}
	if channel.Namespace == domain.ChannelPlayer {
		if channel.PlayerID != playerID {
			return domain.SubscriptionAuthorization{}, domain.ErrPermissionDenied
		}
	} else {
		if authorizer.policy.ValidateGame(channel.Scope.GameKey) != nil {
			return domain.SubscriptionAuthorization{}, domain.ErrPermissionDenied
		}
		if channel.Namespace == domain.ChannelWatch {
			// The watch channel is open to active members and to watchers
			// the host hasn't kicked. It is read without the room's lock,
			// since a watcher's subscription mustn't queue behind the
			// room's writes.
			aggregate, readError := authorizer.store.Read(ctx, channel.Scope)
			if readError == nil && !domain.CanWatch(&aggregate, playerID) {
				readError = domain.ErrPermissionDenied
			}
			err = readError
		} else {
			_, err = application.TransactNormalized(ctx, authorizer.store, authorizer.clock, authorizer.policy, channel.Scope, func(aggregate *domain.Aggregate) (domain.Change, error) {
				member := domain.FindMember(aggregate, playerID)
				if member == nil || !member.Active {
					return domain.Change{}, domain.ErrPermissionDenied
				}
				return domain.Change{}, nil
			})
		}
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrPermissionDenied) {
				return domain.SubscriptionAuthorization{}, domain.ErrPermissionDenied
			}
			return domain.SubscriptionAuthorization{}, unavailable(err)
		}
	}
	allowedUntil := now.Add(authorizer.policy.SubscriptionDuration)
	if identity.ExpiresAt.Before(allowedUntil) {
		allowedUntil = identity.ExpiresAt
	}
	return domain.SubscriptionAuthorization{AllowedUntil: allowedUntil}, nil
}

// unavailable keeps the cause of an outage for the host's log. The proxy
// answers it with a bare 503.
func unavailable(cause error) error {
	return &domain.Error{Code: domain.CodeUnavailable, Message: domain.ErrUnavailable.Message, Cause: cause}
}
