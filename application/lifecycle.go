package application

import (
	"context"
	"time"

	"islandBattles/lobby/domain"
)

func (service *Service) Start(ctx context.Context, principal domain.Principal, scope domain.Scope) (domain.Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Snapshot{}, err
	}
	game, err := service.policy.Game(scope.GameKey)
	if err != nil {
		return domain.Snapshot{}, err
	}
	check, err := service.readyCheck(ctx, scope)
	if err != nil {
		return domain.Snapshot{}, err
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		if _, err := activeMember(aggregate, principal.PlayerID); err != nil {
			return domain.Change{}, err
		}
		if aggregate.Lobby.HostPlayerID == nil || *aggregate.Lobby.HostPlayerID != principal.PlayerID {
			return domain.Change{}, domain.ErrNotHost
		}
		if aggregate.Lobby.Phase == domain.PhaseStarted {
			return domain.Change{}, nil
		}
		if aggregate.Lobby.Phase != domain.PhaseWaiting {
			return domain.Change{}, domain.ErrPhaseConflict
		}
		roster := domain.StartRoster{}
		humans := 0
		for _, member := range aggregate.Members {
			if !member.Active {
				continue
			}
			// A seat is always ready on its team and holds no lease.
			if !member.IsSeat() {
				if !member.Ready || !domain.HasLiveLease(aggregate.Leases, member.PlayerID, member.Generation, now) {
					return domain.Change{}, domain.ErrNotReady
				}
				humans++
			}
			if member.TeamID == nil {
				return domain.Change{}, domain.ErrNotReady
			}
			order := 0
			for _, team := range aggregate.Teams {
				if team.ID == *member.TeamID {
					order = team.Order
					break
				}
			}
			if order == 0 {
				return domain.Change{}, domain.ErrInvalidInput
			}
			roster = append(roster, domain.StartRosterEntry{PlayerID: member.PlayerID, TeamID: *member.TeamID, MemberID: member.ID, TeamOrder: order})
		}
		// The host who starts is a person, so a lobby of seats alone never
		// starts; the count keeps that rule explicit.
		if len(roster) < 2 || humans < 1 {
			return domain.Change{}, domain.ErrNotReady
		}
		if err := game.StartRules.Check(aggregate.Teams, roster); err != nil {
			return domain.Change{}, err
		}
		// The game checks the roster's people again, which also covers members
		// who got ready before the game declared its gate. Seats never got
		// ready through the gate: the game checked their settings instead.
		if check != nil {
			members := make([]domain.ReadyMember, 0, len(roster))
			for _, entry := range roster {
				member := domain.FindMember(aggregate, entry.PlayerID)
				if member == nil || member.IsSeat() {
					continue
				}
				members = append(members, domain.ReadyMember{PlayerID: entry.PlayerID, DisplayName: member.DisplayName})
			}
			if err := check(members, true); err != nil {
				return domain.Change{}, err
			}
		}
		aggregate.StartRoster = roster
		aggregate.Lobby.Phase = domain.PhaseStarted
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		return domain.Snapshot{}, err
	}
	return service.snapshot(aggregate, principal.PlayerID)
}

func (service *Service) Leave(ctx context.Context, principal domain.Principal, scope domain.Scope) error {
	if err := validatePrincipal(principal); err != nil {
		return err
	}
	_, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		member := domain.FindMember(aggregate, principal.PlayerID)
		if member == nil {
			return domain.Change{}, domain.ErrNotFound
		}
		if !member.Active {
			return domain.Change{}, nil
		}
		member.Active = false
		member.TeamID = nil
		member.Ready = false
		member.DisconnectedUntil = nil
		aggregate.Lobby.MarkConnected(now)
		for index := range aggregate.Leases {
			lease := &aggregate.Leases[index]
			if lease.PlayerID == principal.PlayerID && lease.Generation == member.Generation && lease.ReleasedAt == nil {
				released := now
				lease.ReleasedAt = &released
			}
		}
		if aggregate.Lobby.HostPlayerID != nil && *aggregate.Lobby.HostPlayerID == principal.PlayerID {
			aggregate.Lobby.HostPlayerID = domain.ElectHost(aggregate.Members)
		}
		if aggregate.Lobby.HostPlayerID == nil && aggregate.Lobby.Phase == domain.PhaseWaiting {
			domain.CloseAbandoned(aggregate)
		}
		return domain.Change{PublicChanged: true, Revocations: []domain.RevocationIntent{{PlayerID: principal.PlayerID, Scope: scope, Generation: member.Generation}}}, nil
	})
	return err
}
