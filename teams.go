package lobby

import (
	"context"
	"time"
)

func (service *Service) SelectTeam(ctx context.Context, principal Principal, scope Scope, request SelectTeamRequest) (Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return Snapshot{}, err
	}
	if request.TeamID != nil && ValidateUUID(*request.TeamID) != nil {
		return Snapshot{}, ErrInvalidInput
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *Aggregate, now time.Time) (Change, error) {
		member, err := activeMember(aggregate, principal.PlayerID)
		if err != nil {
			return Change{}, err
		}
		if aggregate.Lobby.Phase != PhaseWaiting {
			return Change{}, ErrPhaseConflict
		}
		if sameOptionalString(member.TeamID, request.TeamID) {
			return Change{}, nil
		}
		if request.TeamID != nil {
			var selected *Team
			for index := range aggregate.Teams {
				if aggregate.Teams[index].ID == *request.TeamID {
					selected = &aggregate.Teams[index]
					break
				}
			}
			if selected == nil {
				return Change{}, ErrInvalidInput
			}
			count := 0
			for _, candidate := range aggregate.Members {
				if candidate.Active && candidate.TeamID != nil && *candidate.TeamID == selected.ID {
					count++
				}
			}
			if count >= selected.Capacity {
				return Change{}, ErrTeamFull
			}
		}
		member.TeamID = copyPointer(request.TeamID)
		member.Ready = false
		return Change{PublicChanged: true}, nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return BuildSnapshot(aggregate, principal.PlayerID)
}

func (service *Service) SetReady(ctx context.Context, principal Principal, scope Scope, request ReadyRequest) (Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return Snapshot{}, err
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *Aggregate, now time.Time) (Change, error) {
		member, err := activeMember(aggregate, principal.PlayerID)
		if err != nil {
			return Change{}, err
		}
		if aggregate.Lobby.Phase != PhaseWaiting {
			return Change{}, ErrPhaseConflict
		}
		if request.Ready && member.TeamID == nil {
			return Change{}, ErrNotReady
		}
		if member.Ready == request.Ready {
			return Change{}, nil
		}
		member.Ready = request.Ready
		return Change{PublicChanged: true}, nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return BuildSnapshot(aggregate, principal.PlayerID)
}
