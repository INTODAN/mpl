package application

import (
	"context"
	"sort"
	"time"

	"islandBattles/lobby/domain"
)

// Watch lets anyone with a guest session and the lobby's join code watch the
// lobby, in any phase: waiting, started or closed. It adds the caller's
// watcher row, or finds the one they have, and returns the watch channel. A
// watcher takes no seat and counts toward nothing, so watching never changes
// capacity, the start roster, leases, grace or retention, and doesn't mark
// the lobby connected. An active member keeps their member access and gets
// no watcher row. A code of another game finds no lobby, a watcher the host
// kicked is refused with watcher_kicked, and a new watcher beyond
// domain.MaxWatchersPerLobby with watchers_full.
func (service *Service) Watch(ctx context.Context, principal domain.Principal, request domain.WatchRequest) (domain.WatchAccess, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.WatchAccess{}, err
	}
	if err := service.policy.ValidateGame(request.GameKey); err != nil {
		return domain.WatchAccess{}, err
	}
	code, err := domain.NormalizeJoinCode(request.JoinCode)
	if err != nil {
		return domain.WatchAccess{}, err
	}
	displayName, err := domain.NormalizeName(principal.DisplayName, 40)
	if err != nil {
		return domain.WatchAccess{}, domain.ErrUnauthenticated
	}
	scope, err := service.store.ResolveCode(ctx, request.GameKey, code)
	if err != nil {
		return domain.WatchAccess{}, err
	}
	channel, err := domain.WatchChannel(scope)
	if err != nil {
		return domain.WatchAccess{}, err
	}
	member := false
	_, err = service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		if current := domain.FindMember(aggregate, principal.PlayerID); current != nil && current.Active {
			member = true
			return domain.Change{}, nil
		}
		if watcher := domain.FindWatcher(aggregate, principal.PlayerID); watcher != nil {
			if !watcher.Watching() {
				return domain.Change{}, domain.ErrWatcherKicked
			}
			return domain.Change{}, nil
		}
		if domain.ActiveWatchers(aggregate) >= domain.MaxWatchersPerLobby {
			return domain.Change{}, domain.ErrWatchersFull
		}
		aggregate.Watchers = append(aggregate.Watchers, domain.Watcher{PlayerID: principal.PlayerID, DisplayName: displayName, JoinedAt: now})
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		return domain.WatchAccess{}, err
	}
	return domain.WatchAccess{Scope: scope, Channel: channel, Member: member}, nil
}

// Unwatch removes the caller's watcher row and unsubscribes them from the
// watch channel. A kicked watcher's row stays, so leaving never clears a kick.
func (service *Service) Unwatch(ctx context.Context, principal domain.Principal, scope domain.Scope) error {
	if err := validatePrincipal(principal); err != nil {
		return err
	}
	_, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		watcher := domain.FindWatcher(aggregate, principal.PlayerID)
		if watcher == nil {
			return domain.Change{}, domain.ErrNotFound
		}
		if !watcher.Watching() {
			return domain.Change{}, nil
		}
		kept := aggregate.Watchers[:0]
		for _, candidate := range aggregate.Watchers {
			if candidate.PlayerID != principal.PlayerID {
				kept = append(kept, candidate)
			}
		}
		aggregate.Watchers = kept
		return domain.Change{PublicChanged: true, Revocations: []domain.RevocationIntent{{PlayerID: principal.PlayerID, Scope: scope, Watch: true}}}, nil
	})
	return err
}

// Watchers lists the lobby's watchers, kicked ones left out, in the order
// they started watching, for its active members.
func (service *Service) Watchers(ctx context.Context, principal domain.Principal, scope domain.Scope) ([]domain.Watcher, error) {
	if err := validatePrincipal(principal); err != nil {
		return nil, err
	}
	if err := service.policy.ValidateScope(scope); err != nil {
		return nil, err
	}
	// A read, without the room's lock: watchers are not worth serializing
	// behind heartbeats, and an expired member's grace is applied by the
	// next mutation anyway.
	aggregate, err := service.store.Read(ctx, scope)
	if err != nil {
		return nil, err
	}
	if _, err := activeMember(&aggregate, principal.PlayerID); err != nil {
		return nil, err
	}
	watchers := make([]domain.Watcher, 0, len(aggregate.Watchers))
	for _, watcher := range aggregate.Watchers {
		if watcher.Watching() {
			watchers = append(watchers, watcher)
		}
	}
	sort.SliceStable(watchers, func(first, second int) bool {
		if watchers[first].JoinedAt.Equal(watchers[second].JoinedAt) {
			return watchers[first].PlayerID < watchers[second].PlayerID
		}
		return watchers[first].JoinedAt.Before(watchers[second].JoinedAt)
	})
	return watchers, nil
}

// KickWatcher lets the host remove a watcher, in any phase. The watcher is
// unsubscribed from the watch channel at once, as a departing member is from
// the room channel, and the join code no longer lets them watch. Kicking a
// kicked watcher again changes nothing; a player who isn't a watcher of the
// lobby is not_found.
func (service *Service) KickWatcher(ctx context.Context, principal domain.Principal, scope domain.Scope, playerID string) error {
	if err := validatePrincipal(principal); err != nil {
		return err
	}
	if domain.ValidateUUID(playerID) != nil {
		return domain.ErrInvalidInput
	}
	_, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		if _, err := activeMember(aggregate, principal.PlayerID); err != nil {
			return domain.Change{}, err
		}
		if aggregate.Lobby.HostPlayerID == nil || *aggregate.Lobby.HostPlayerID != principal.PlayerID {
			return domain.Change{}, domain.ErrNotHost
		}
		watcher := domain.FindWatcher(aggregate, playerID)
		if watcher == nil {
			return domain.Change{}, domain.ErrNotFound
		}
		if !watcher.Watching() {
			return domain.Change{}, nil
		}
		kickedAt := now
		watcher.KickedAt = &kickedAt
		return domain.Change{PublicChanged: true, Revocations: []domain.RevocationIntent{{PlayerID: playerID, Scope: scope, Watch: true}}}, nil
	})
	return err
}

// AuthorizeWatch lets a game open its watcher reads to an active member or a
// watcher the host hasn't kicked. It reads without locking the room. A
// kicked watcher gets watcher_kicked; anyone else, and a lobby that is gone,
// not_found.
func (service *Service) AuthorizeWatch(ctx context.Context, principal domain.Principal, scope domain.Scope) error {
	if err := validatePrincipal(principal); err != nil {
		return err
	}
	return service.AuthorizeGameWatch(ctx, scope, principal.PlayerID)
}

var _ domain.WatchAuthorizer = (*Service)(nil)
