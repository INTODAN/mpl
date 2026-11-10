package application

import (
	"context"
	"errors"
	"math"
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
		takenTeams := map[string]bool{}
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
			// A team holds one member. Only a lobby created before that
			// rule can have two members on a team, and it can't start.
			if takenTeams[*member.TeamID] {
				return domain.Change{}, domain.ErrNotReady
			}
			takenTeams[*member.TeamID] = true
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

// errRoundMoved tells Reopen that the lobby is no longer in the round whose
// game it asked about, so it asks again.
var errRoundMoved = errors.New("the lobby's round moved while reopening")

// reopenAttempts bounds how often Reopen asks the game again when the lobby
// starts or moves to another round meanwhile.
const reopenAttempts = 3

// Reopen brings a started lobby whose game ended back to waiting, in its next
// round, so its members can play another game. Any active person may reopen
// it. Members, teams, seats and the game settings stay; every person's ready
// is cleared, seats stay ready, and the start roster is emptied for the next
// start. A lobby that already waits is returned unchanged, so two members
// reopening at once both succeed. A closed lobby, or one whose game declares
// no GameOver check, fails with phase_conflict, and one whose game hasn't
// ended with game_running.
//
// The game is asked before the transaction, like the ready gate, so it never
// reads its data while the room is locked. The transaction then checks that
// the lobby is still started in the round it asked about; if it moved, it
// asks again. A game that ended stays ended, so the answer can't go stale
// within a round.
func (service *Service) Reopen(ctx context.Context, principal domain.Principal, scope domain.Scope) (domain.Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Snapshot{}, err
	}
	if err := service.policy.ValidateScope(scope); err != nil {
		return domain.Snapshot{}, err
	}
	game, err := service.policy.Game(scope.GameKey)
	if err != nil {
		return domain.Snapshot{}, err
	}
	for range reopenAttempts {
		current, err := service.store.Read(ctx, scope)
		if err != nil {
			return domain.Snapshot{}, err
		}
		// The game is asked only for a member's request about a started
		// lobby; the transaction refuses everyone else first.
		asked, over := int64(0), false
		member := domain.FindMember(&current, principal.PlayerID)
		if member != nil && member.Active && current.Lobby.Phase == domain.PhaseStarted && game.GameOver != nil {
			over, err = game.CheckGameOver(ctx, scope.LobbyID, current.Lobby.Round)
			if err != nil {
				return domain.Snapshot{}, err
			}
			asked = current.Lobby.Round
		}
		aggregate, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
			member, err := activeMember(aggregate, principal.PlayerID)
			if err != nil {
				return domain.Change{}, err
			}
			if member.IsSeat() {
				return domain.Change{}, domain.ErrNotFound
			}
			switch {
			case aggregate.Lobby.Phase == domain.PhaseWaiting:
				return domain.Change{}, nil
			case aggregate.Lobby.Phase != domain.PhaseStarted, game.GameOver == nil:
				return domain.Change{}, domain.ErrPhaseConflict
			case aggregate.Lobby.Round != asked:
				return domain.Change{}, errRoundMoved
			case !over:
				return domain.Change{}, domain.ErrGameRunning
			case aggregate.Lobby.Round == math.MaxInt64:
				return domain.Change{}, domain.ErrUnavailable
			}
			aggregate.Lobby.Phase = domain.PhaseWaiting
			aggregate.Lobby.Round++
			aggregate.StartRoster = domain.StartRoster{}
			for index := range aggregate.Members {
				if member := &aggregate.Members[index]; !member.IsSeat() {
					member.Ready = false
				}
			}
			return domain.Change{PublicChanged: true}, nil
		})
		if errors.Is(err, errRoundMoved) {
			continue
		}
		if err != nil {
			return domain.Snapshot{}, err
		}
		return service.snapshot(aggregate, principal.PlayerID)
	}
	return domain.Snapshot{}, domain.ErrUnavailable
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
