package application

import (
	"context"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/internal/optional"
)

func (service *Service) Heartbeat(ctx context.Context, principal domain.Principal, scope domain.Scope, request domain.HeartbeatRequest) (domain.HeartbeatResult, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.HeartbeatResult{}, err
	}
	if domain.ValidateUUID(request.ClientInstanceID) != nil || request.MembershipGeneration < 1 {
		return domain.HeartbeatResult{}, domain.ErrInvalidInput
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		member := domain.FindMember(aggregate, principal.PlayerID)
		if member == nil {
			return domain.Change{}, domain.ErrNotFound
		}
		if !member.Active || member.Generation != request.MembershipGeneration {
			return domain.Change{}, domain.ErrStaleMembership
		}
		lease := domain.FindLease(aggregate, principal.PlayerID, request.MembershipGeneration, request.ClientInstanceID)
		if lease == nil || lease.ReleasedAt != nil {
			return domain.Change{}, domain.ErrStaleMembership
		}
		lease.ExpiresAt = now.Add(service.policy.LeaseDuration)
		change := domain.Change{PrivateChanged: true, PublicChanged: member.DisconnectedUntil != nil}
		member.DisconnectedUntil = nil
		aggregate.Lobby.MarkConnected(now)
		return change, nil
	})
	if err != nil {
		return domain.HeartbeatResult{}, err
	}
	member := domain.FindMember(&aggregate, principal.PlayerID)
	lease := domain.FindLease(&aggregate, principal.PlayerID, member.Generation, request.ClientInstanceID)
	return domain.HeartbeatResult{Version: aggregate.Lobby.Version, MembershipGeneration: member.Generation, LeaseExpiresAt: lease.ExpiresAt, DisconnectedUntil: optional.Copy(member.DisconnectedUntil)}, nil
}

func (service *Service) ReleaseLease(ctx context.Context, principal domain.Principal, scope domain.Scope, request domain.ReleaseLeaseRequest) error {
	if err := validatePrincipal(principal); err != nil {
		return err
	}
	if domain.ValidateUUID(request.ClientInstanceID) != nil || request.MembershipGeneration < 1 {
		return domain.ErrInvalidInput
	}
	_, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		member := domain.FindMember(aggregate, principal.PlayerID)
		if member == nil {
			return domain.Change{}, domain.ErrNotFound
		}
		if member.Generation != request.MembershipGeneration {
			return domain.Change{}, domain.ErrStaleMembership
		}
		lease := domain.FindLease(aggregate, principal.PlayerID, request.MembershipGeneration, request.ClientInstanceID)
		if lease == nil || lease.ReleasedAt != nil {
			return domain.Change{}, nil
		}
		lease.ReleasedAt = &now
		aggregate.Lobby.MarkConnected(now)
		change, err := domain.NormalizeExpired(aggregate, now, service.policy)
		return domain.MergeChanges(change, domain.Change{PrivateChanged: true}), err
	})
	return err
}
