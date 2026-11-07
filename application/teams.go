package application

import (
	"context"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/internal/optional"
)

func (service *Service) SelectTeam(ctx context.Context, principal domain.Principal, scope domain.Scope, request domain.SelectTeamRequest) (domain.Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Snapshot{}, err
	}
	if request.TeamID != nil && domain.ValidateUUID(*request.TeamID) != nil {
		return domain.Snapshot{}, domain.ErrInvalidInput
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		member, err := activeMember(aggregate, principal.PlayerID)
		if err != nil {
			return domain.Change{}, err
		}
		if aggregate.Lobby.Phase != domain.PhaseWaiting {
			return domain.Change{}, domain.ErrPhaseConflict
		}
		if optional.Equal(member.TeamID, request.TeamID) {
			return domain.Change{}, nil
		}
		if request.TeamID != nil {
			var selected *domain.Team
			for index := range aggregate.Teams {
				if aggregate.Teams[index].ID == *request.TeamID {
					selected = &aggregate.Teams[index]
					break
				}
			}
			if selected == nil {
				return domain.Change{}, domain.ErrInvalidInput
			}
			count := 0
			for _, candidate := range aggregate.Members {
				if candidate.Active && candidate.TeamID != nil && *candidate.TeamID == selected.ID {
					count++
				}
			}
			if count >= selected.Capacity {
				return domain.Change{}, domain.ErrTeamFull
			}
		}
		member.TeamID = optional.Copy(request.TeamID)
		member.Ready = false
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		return domain.Snapshot{}, err
	}
	return service.snapshot(aggregate, principal.PlayerID)
}

func (service *Service) SetReady(ctx context.Context, principal domain.Principal, scope domain.Scope, request domain.ReadyRequest) (domain.Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Snapshot{}, err
	}
	var check domain.ReadyCheck
	if request.Ready {
		var err error
		if check, err = service.readyCheck(ctx, scope); err != nil {
			return domain.Snapshot{}, err
		}
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		member, err := activeMember(aggregate, principal.PlayerID)
		if err != nil {
			return domain.Change{}, err
		}
		if aggregate.Lobby.Phase != domain.PhaseWaiting {
			return domain.Change{}, domain.ErrPhaseConflict
		}
		if request.Ready && member.TeamID == nil {
			return domain.Change{}, domain.ErrNotReady
		}
		if member.Ready == request.Ready {
			return domain.Change{}, nil
		}
		if check != nil {
			if err := check(member.PlayerID); err != nil {
				return domain.Change{}, err
			}
		}
		member.Ready = request.Ready
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		return domain.Snapshot{}, err
	}
	return service.snapshot(aggregate, principal.PlayerID)
}
