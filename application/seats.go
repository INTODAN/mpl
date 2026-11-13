package application

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"islandBattles/lobby/domain"
)

// AddSeat adds a seat that no person plays, such as a game's bot, to a team of
// a waiting lobby. Only the host can add one. The game checks the seat's
// settings and names the seat before the transaction, so it never runs while
// the room is locked. The seat takes its team, which holds no other member,
// and counts toward the lobby's capacity. It is always ready, holds no lease,
// and joins the start roster with its own player ID, which the game plays
// from the seat's settings. A request
// ID makes a retry safe: the same ID with the same team and settings returns
// the current snapshot without a second seat, and with others fails with
// idempotency_conflict.
func (service *Service) AddSeat(ctx context.Context, principal domain.Principal, scope domain.Scope, request domain.AddSeatRequest) (domain.Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Snapshot{}, err
	}
	if err := service.policy.ValidateScope(scope); err != nil {
		return domain.Snapshot{}, err
	}
	if domain.ValidateUUID(request.TeamID) != nil || (request.RequestID != "" && domain.ValidateUUID(request.RequestID) != nil) {
		return domain.Snapshot{}, domain.ErrInvalidInput
	}
	settings, name, err := service.policy.ValidateSeatSettings(ctx, scope.GameKey, scope.LobbyID, request.SeatSettings)
	if err != nil {
		return domain.Snapshot{}, err
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		if err := requireWaitingHost(aggregate, principal); err != nil {
			return domain.Change{}, err
		}
		if request.RequestID != "" {
			for _, seat := range aggregate.Members {
				if seat.SeatRequestID != request.RequestID {
					continue
				}
				// A seat the host removed since keeps no team to compare.
				if !bytes.Equal(seat.SeatSettings, settings) || (seat.Active && (seat.TeamID == nil || *seat.TeamID != request.TeamID)) {
					return domain.Change{}, domain.ErrIdempotencyConflict
				}
				return domain.Change{}, nil
			}
		}
		var team *domain.Team
		for index := range aggregate.Teams {
			if aggregate.Teams[index].ID == request.TeamID {
				team = &aggregate.Teams[index]
				break
			}
		}
		if team == nil {
			return domain.Change{}, domain.ErrInvalidInput
		}
		if domain.LobbyFull(aggregate) {
			return domain.Change{}, domain.ErrLobbyFull
		}
		if domain.TeamTaken(aggregate, team.ID) {
			return domain.Change{}, domain.ErrTeamFull
		}
		seatID, err := service.identifiers.UUID()
		if err != nil {
			return domain.Change{}, unavailable(err)
		}
		playerID, err := service.identifiers.UUID()
		if err != nil {
			return domain.Change{}, unavailable(err)
		}
		teamID := team.ID
		aggregate.Members = append(aggregate.Members, domain.Member{
			ID: seatID, PlayerID: playerID, DisplayName: distinctName(aggregate.Members, name), Generation: 1, Active: true,
			TeamID: &teamID, Ready: true, JoinedAt: now, JoinSequence: aggregate.Lobby.NextJoinSequence,
			Kind: domain.MemberSeat, SeatSettings: settings, SeatRequestID: request.RequestID,
		})
		aggregate.Lobby.NextJoinSequence++
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		return domain.Snapshot{}, err
	}
	return service.snapshot(aggregate, principal.PlayerID)
}

// RemoveSeat removes a seat that the host added, while the lobby waits. Only
// the host can remove one. Removing a seat that is already gone changes
// nothing; an ID that names no seat of the lobby is invalid input.
func (service *Service) RemoveSeat(ctx context.Context, principal domain.Principal, scope domain.Scope, seatID string) (domain.Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Snapshot{}, err
	}
	if domain.ValidateUUID(seatID) != nil {
		return domain.Snapshot{}, domain.ErrInvalidInput
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		if err := requireWaitingHost(aggregate, principal); err != nil {
			return domain.Change{}, err
		}
		for index := range aggregate.Members {
			seat := &aggregate.Members[index]
			if seat.ID != seatID {
				continue
			}
			if !seat.IsSeat() {
				return domain.Change{}, domain.ErrInvalidInput
			}
			if !seat.Active {
				return domain.Change{}, nil
			}
			removeSeat(seat)
			return domain.Change{PublicChanged: true}, nil
		}
		return domain.Change{}, domain.ErrInvalidInput
	})
	if err != nil {
		return domain.Snapshot{}, err
	}
	return service.snapshot(aggregate, principal.PlayerID)
}

// removeSeat removes an active seat from the lobby. Like a member who left,
// the seat stays in the lobby's history.
func removeSeat(seat *domain.Member) {
	seat.Active = false
	seat.TeamID = nil
	seat.Ready = false
}

// requireWaitingHost lets only the host of a waiting lobby change its seats
// and its settings.
func requireWaitingHost(aggregate *domain.Aggregate, principal domain.Principal) error {
	if _, err := activeMember(aggregate, principal.PlayerID); err != nil {
		return err
	}
	if aggregate.Lobby.HostPlayerID == nil || *aggregate.Lobby.HostPlayerID != principal.PlayerID {
		return domain.ErrNotHost
	}
	if aggregate.Lobby.Phase != domain.PhaseWaiting {
		return domain.ErrPhaseConflict
	}
	return nil
}

// maxSeatNameRunes is the longest display name a seat can have, the game's
// name and the number that tells two seats apart included.
const maxSeatNameRunes = 40

// distinctName numbers a seat's name when an active member already has it,
// so two bots of one level can be told apart: "Bot (level 2)", then
// "Bot (level 2) #2".
func distinctName(members []domain.Member, name string) string {
	taken := map[string]bool{}
	for _, member := range members {
		if member.Active {
			taken[member.DisplayName] = true
		}
	}
	candidate := name
	for number := 2; taken[candidate]; number++ {
		suffix := fmt.Sprintf(" #%d", number)
		base := []rune(name)
		if limit := maxSeatNameRunes - utf8.RuneCountInString(suffix); len(base) > limit {
			base = base[:limit]
		}
		candidate = strings.TrimSpace(string(base)) + suffix
	}
	return candidate
}
