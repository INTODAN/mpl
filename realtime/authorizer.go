// Package realtime adapts authenticated Centrifugo subscriptions and publications
// to the generic lobby. It does not create membership on socket connection.
package realtime

import (
	"context"
	"errors"
	"islandBattles/lobby"
)

type Authorizer struct {
	identities lobby.IdentityStore
	store      lobby.Store
	clock      lobby.Clock
	policy     lobby.Policy
}

func NewAuthorizer(identities lobby.IdentityStore, store lobby.Store, clock lobby.Clock, policy lobby.Policy) (*Authorizer, error) {
	if identities == nil || store == nil || clock == nil {
		return nil, lobby.ErrInvalidInput
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &Authorizer{identities: identities, store: store, clock: clock, policy: policy}, nil
}

func (authorizer *Authorizer) AuthorizeSubscription(ctx context.Context, playerID, channelName string) (lobby.SubscriptionAuthorization, error) {
	if lobby.ValidateUUID(playerID) != nil {
		return lobby.SubscriptionAuthorization{}, lobby.ErrPermissionDenied
	}
	channel, err := lobby.ParseChannel(channelName)
	if err != nil {
		return lobby.SubscriptionAuthorization{}, lobby.ErrPermissionDenied
	}
	identity, err := authorizer.identities.LookupByPlayer(ctx, playerID)
	if err != nil {
		return lobby.SubscriptionAuthorization{}, lobby.ErrUnavailable
	}
	now := authorizer.clock.Now().UTC()
	if identity == nil || identity.Principal.PlayerID != playerID || !now.Before(identity.ExpiresAt) {
		return lobby.SubscriptionAuthorization{}, lobby.ErrUnauthenticated
	}
	if channel.Namespace == lobby.ChannelPlayer {
		if channel.PlayerID != playerID {
			return lobby.SubscriptionAuthorization{}, lobby.ErrPermissionDenied
		}
	} else {
		if authorizer.policy.ValidateGame(channel.Scope.GameKey) != nil {
			return lobby.SubscriptionAuthorization{}, lobby.ErrPermissionDenied
		}
		_, err = lobby.TransactNormalized(ctx, authorizer.store, authorizer.clock, authorizer.policy, channel.Scope, func(aggregate *lobby.Aggregate) (lobby.Change, error) {
			member := lobby.FindMember(aggregate, playerID)
			if member == nil || !member.Active {
				return lobby.Change{}, lobby.ErrPermissionDenied
			}
			return lobby.Change{}, nil
		})
		if err != nil {
			if errors.Is(err, lobby.ErrNotFound) || errors.Is(err, lobby.ErrPermissionDenied) {
				return lobby.SubscriptionAuthorization{}, lobby.ErrPermissionDenied
			}
			return lobby.SubscriptionAuthorization{}, lobby.ErrUnavailable
		}
	}
	allowedUntil := now.Add(authorizer.policy.SubscriptionDuration)
	if identity.ExpiresAt.Before(allowedUntil) {
		allowedUntil = identity.ExpiresAt
	}
	return lobby.SubscriptionAuthorization{AllowedUntil: allowedUntil}, nil
}
