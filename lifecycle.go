package lobby

import (
	"context"
	"time"
)

func (service *Service) Start(ctx context.Context, principal Principal, scope Scope) (Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return Snapshot{}, err
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *Aggregate, now time.Time) (Change, error) {
		if _, err := activeMember(aggregate, principal.PlayerID); err != nil {
			return Change{}, err
		}
		if aggregate.Lobby.HostPlayerID == nil || *aggregate.Lobby.HostPlayerID != principal.PlayerID {
			return Change{}, ErrNotHost
		}
		if aggregate.Lobby.Phase == PhaseStarted {
			return Change{}, nil
		}
		if aggregate.Lobby.Phase != PhaseWaiting {
			return Change{}, ErrPhaseConflict
		}
		roster := StartRoster{}
		for _, member := range aggregate.Members {
			if !member.Active {
				continue
			}
			if !member.Ready || member.TeamID == nil || !HasLiveLease(aggregate.Leases, member.PlayerID, member.Generation, now) {
				return Change{}, ErrNotReady
			}
			order := 0
			for _, team := range aggregate.Teams {
				if team.ID == *member.TeamID {
					order = team.Order
					break
				}
			}
			if order == 0 {
				return Change{}, ErrInvalidInput
			}
			roster = append(roster, StartRosterEntry{PlayerID: member.PlayerID, TeamID: *member.TeamID, MemberID: member.ID, TeamOrder: order})
		}
		if len(roster) < 2 {
			return Change{}, ErrNotReady
		}
		aggregate.StartRoster = roster
		aggregate.Lobby.Phase = PhaseStarted
		return Change{PublicChanged: true}, nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return BuildSnapshot(aggregate, principal.PlayerID)
}

func (service *Service) Leave(ctx context.Context, principal Principal, scope Scope) error {
	if err := validatePrincipal(principal); err != nil {
		return err
	}
	_, err := service.runMutation(ctx, scope, func(aggregate *Aggregate, now time.Time) (Change, error) {
		member := FindMember(aggregate, principal.PlayerID)
		if member == nil {
			return Change{}, ErrNotFound
		}
		if !member.Active {
			return Change{}, nil
		}
		member.Active = false
		member.TeamID = nil
		member.Ready = false
		member.DisconnectedUntil = nil
		for index := range aggregate.Leases {
			lease := &aggregate.Leases[index]
			if lease.PlayerID == principal.PlayerID && lease.Generation == member.Generation && lease.ReleasedAt == nil {
				released := now
				lease.ReleasedAt = &released
			}
		}
		if aggregate.Lobby.HostPlayerID != nil && *aggregate.Lobby.HostPlayerID == principal.PlayerID {
			aggregate.Lobby.HostPlayerID = ElectHost(aggregate.Members)
		}
		if aggregate.Lobby.HostPlayerID == nil && aggregate.Lobby.Phase == PhaseWaiting {
			aggregate.Lobby.Phase = PhaseClosed
		}
		return Change{PublicChanged: true, Revocations: []RevocationIntent{{PlayerID: principal.PlayerID, Scope: scope, Generation: member.Generation}}}, nil
	})
	return err
}
