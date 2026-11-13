package application

import (
	"context"

	"islandBattles/lobby/domain"
)

// GameSnapshot returns a lobby as its game reads it through the game API: the
// view of every member, with no player behind it (domain.BuildGameSnapshot),
// and the game's start rules. It reads without locking the room, so a game
// can read the lobby while one of its hooks runs, and applies due lease and
// grace expiry to what it returns without storing it. A lobby that is gone,
// or one of another game, is not_found.
func (service *Service) GameSnapshot(ctx context.Context, scope domain.Scope) (domain.Snapshot, error) {
	if err := service.policy.ValidateScope(scope); err != nil {
		return domain.Snapshot{}, domain.ErrNotFound
	}
	aggregate, err := service.store.Read(ctx, scope)
	if err != nil {
		return domain.Snapshot{}, err
	}
	if _, err := domain.NormalizeExpired(&aggregate, service.clock.Now().UTC(), service.policy); err != nil {
		return domain.Snapshot{}, err
	}
	snapshot, err := domain.BuildGameSnapshot(aggregate)
	if err != nil {
		return domain.Snapshot{}, err
	}
	return service.withStartRules(snapshot)
}

// AuthorizeGameWatch is AuthorizeWatch for a game that names the player
// itself, through the game API, rather than through the player's session: it
// succeeds for an active member or a watcher the host hasn't kicked. A kicked
// watcher gets watcher_kicked; anyone else, and a lobby that is gone,
// not_found.
func (service *Service) AuthorizeGameWatch(ctx context.Context, scope domain.Scope, playerID string) error {
	if domain.ValidateUUID(playerID) != nil {
		return domain.ErrNotFound
	}
	if err := service.policy.ValidateScope(scope); err != nil {
		return domain.ErrNotFound
	}
	aggregate, err := service.store.Read(ctx, scope)
	if err != nil {
		return err
	}
	if domain.CanWatch(&aggregate, playerID) {
		return nil
	}
	if watcher := domain.FindWatcher(&aggregate, playerID); watcher != nil {
		return domain.ErrWatcherKicked
	}
	return domain.ErrNotFound
}

var _ domain.GameAPI = (*Service)(nil)
