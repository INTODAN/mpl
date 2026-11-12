package application

import (
	"bytes"
	"context"
	"time"

	"islandBattles/lobby/domain"
)

// SetMemberSettings sets the caller's own member settings, a JSON object that
// only the game reads, such as what plays for them, while the lobby waits.
// The game checks the settings before the transaction, so it never runs while
// the room is locked. New settings clear the caller's ready, so a start always
// sees the settings that the person confirmed by getting ready again. Settings
// equal to the stored ones change nothing: the version stays and the caller
// stays ready.
func (service *Service) SetMemberSettings(ctx context.Context, principal domain.Principal, scope domain.Scope, request domain.MemberSettingsRequest) (domain.Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Snapshot{}, err
	}
	if err := service.policy.ValidateScope(scope); err != nil {
		return domain.Snapshot{}, err
	}
	settings, err := service.policy.ValidateMemberSettings(ctx, scope.GameKey, scope.LobbyID, request.MemberSettings)
	if err != nil {
		return domain.Snapshot{}, err
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, _ time.Time) (domain.Change, error) {
		member, err := activeMember(aggregate, principal.PlayerID)
		if err != nil {
			return domain.Change{}, err
		}
		if aggregate.Lobby.Phase != domain.PhaseWaiting {
			return domain.Change{}, domain.ErrPhaseConflict
		}
		if bytes.Equal(member.MemberSettings, settings) {
			return domain.Change{}, nil
		}
		member.MemberSettings = settings
		member.Ready = false
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		return domain.Snapshot{}, err
	}
	return service.snapshot(aggregate, principal.PlayerID)
}
