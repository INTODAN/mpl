package lobby

import (
	"context"
	"time"
)

func (service *Service) Heartbeat(ctx context.Context, principal Principal, scope Scope, request HeartbeatRequest) (HeartbeatResult, error) {
	if err := validatePrincipal(principal); err != nil {
		return HeartbeatResult{}, err
	}
	if ValidateUUID(request.ClientInstanceID) != nil || request.MembershipGeneration < 1 {
		return HeartbeatResult{}, ErrInvalidInput
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *Aggregate, now time.Time) (Change, error) {
		member := FindMember(aggregate, principal.PlayerID)
		if member == nil {
			return Change{}, ErrNotFound
		}
		if !member.Active || member.Generation != request.MembershipGeneration {
			return Change{}, ErrStaleMembership
		}
		lease := FindLease(aggregate, principal.PlayerID, request.MembershipGeneration, request.ClientInstanceID)
		if lease == nil || lease.ReleasedAt != nil {
			return Change{}, ErrStaleMembership
		}
		lease.ExpiresAt = now.Add(service.policy.LeaseDuration)
		change := Change{PrivateChanged: true, PublicChanged: member.DisconnectedUntil != nil}
		member.DisconnectedUntil = nil
		return change, nil
	})
	if err != nil {
		return HeartbeatResult{}, err
	}
	member := FindMember(&aggregate, principal.PlayerID)
	lease := FindLease(&aggregate, principal.PlayerID, member.Generation, request.ClientInstanceID)
	return HeartbeatResult{Version: aggregate.Lobby.Version, MembershipGeneration: member.Generation, LeaseExpiresAt: lease.ExpiresAt, DisconnectedUntil: copyPointer(member.DisconnectedUntil)}, nil
}

func (service *Service) ReleaseLease(ctx context.Context, principal Principal, scope Scope, request ReleaseLeaseRequest) error {
	if err := validatePrincipal(principal); err != nil {
		return err
	}
	if ValidateUUID(request.ClientInstanceID) != nil || request.MembershipGeneration < 1 {
		return ErrInvalidInput
	}
	_, err := service.runMutation(ctx, scope, func(aggregate *Aggregate, now time.Time) (Change, error) {
		member := FindMember(aggregate, principal.PlayerID)
		if member == nil {
			return Change{}, ErrNotFound
		}
		if member.Generation != request.MembershipGeneration {
			return Change{}, ErrStaleMembership
		}
		lease := FindLease(aggregate, principal.PlayerID, request.MembershipGeneration, request.ClientInstanceID)
		if lease == nil || lease.ReleasedAt != nil {
			return Change{}, nil
		}
		lease.ReleasedAt = &now
		change, err := NormalizeExpired(aggregate, now, service.policy)
		return MergeChanges(change, Change{PrivateChanged: true}), err
	})
	return err
}
