package application

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"islandBattles/lobby/domain"
)

// GameBinding authorizes each request before invoking game-specific persistence.
// Result is owned by the game; the lobby imposes no game ID or team representation.
type GameBinding[Result any] struct {
	gameKey string
	lobbies domain.SnapshotReader
	bind    func(context.Context, domain.Snapshot) (Result, error)
}

// NewGameBinding accepts a callback that atomically creates or returns the same
// game for a lobby. It receives a started snapshot with validated, ordered teams
// and a frozen roster, only after current host authorization succeeds.
func NewGameBinding[Result any](gameKey string, lobbies domain.SnapshotReader, bind func(context.Context, domain.Snapshot) (Result, error)) (*GameBinding[Result], error) {
	if err := domain.ValidateGameKey(gameKey); err != nil {
		return nil, err
	}
	if lobbies == nil || bind == nil {
		return nil, errors.New("lobby binding: missing dependency")
	}
	return &GameBinding[Result]{gameKey: gameKey, lobbies: lobbies, bind: bind}, nil
}

func (binding *GameBinding[Result]) Bind(ctx context.Context, principal domain.Principal, scope domain.Scope) (Result, error) {
	var zero Result
	if scope.GameKey != binding.gameKey || domain.ValidateUUID(scope.LobbyID) != nil {
		return zero, domain.ErrNotFound
	}
	snapshot, err := binding.lobbies.Snapshot(ctx, principal, scope)
	if err != nil {
		return zero, err
	}
	if snapshot.Lobby.Scope() != scope {
		return zero, domain.ErrNotFound
	}
	if snapshot.Lobby.Phase != domain.PhaseStarted {
		return zero, domain.ErrPhaseConflict
	}
	if snapshot.Lobby.HostPlayerID == nil || *snapshot.Lobby.HostPlayerID != principal.PlayerID {
		return zero, domain.ErrNotHost
	}
	if len(snapshot.StartRoster) == 0 || len(snapshot.Teams) == 0 {
		return zero, domain.ErrPhaseConflict
	}
	snapshot.Teams = slices.Clone(snapshot.Teams)
	slices.SortFunc(snapshot.Teams, func(left, right domain.Team) int {
		return cmp.Compare(left.Order, right.Order)
	})
	teamOrders := make(map[string]int, len(snapshot.Teams))
	for index, team := range snapshot.Teams {
		if domain.ValidateUUID(team.ID) != nil || team.Order != index+1 || teamOrders[team.ID] != 0 {
			return zero, domain.ErrUnavailable
		}
		teamOrders[team.ID] = team.Order
	}
	for _, entry := range snapshot.StartRoster {
		if entry.TeamOrder < 1 || teamOrders[entry.TeamID] != entry.TeamOrder {
			return zero, domain.ErrUnavailable
		}
	}
	return binding.bind(ctx, snapshot)
}
