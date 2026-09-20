package lobby

import (
	"context"
	"time"
)

func (service *Service) Snapshot(ctx context.Context, principal Principal, scope Scope) (Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return Snapshot{}, err
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *Aggregate, now time.Time) (Change, error) {
		_, err := activeMember(aggregate, principal.PlayerID)
		return Change{}, err
	})
	if err != nil {
		return Snapshot{}, err
	}
	return BuildSnapshot(aggregate, principal.PlayerID)
}
