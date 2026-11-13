package gamehooks

import (
	"context"
	"errors"
	"net/http"

	"islandBattles/lobby/domain"
)

// GameAPIPath is where the lobby serves the game API, under the lobby's URL.
// A game's routes are under GameAPIPath + "/games/{game_key}".
const GameAPIPath = "/lobby/v1/game-api"

// GameAPIClient is a game's client of the lobby's game API: it reads the
// game's lobbies and checks their watchers, signed with the game's shared
// secret. It reaches only the game's own key.
type GameAPIClient struct {
	key       string
	transport *transport
}

// NewGameAPIClient returns the game API client of the game with key, for the
// lobby at lobbyURL (see ParseServiceURL), such as https://lobby.example.com.
func NewGameAPIClient(key, lobbyURL string, secret []byte, options ...Option) (*GameAPIClient, error) {
	if err := domain.ValidateGameKey(key); err != nil {
		return nil, errors.New("invalid game key")
	}
	transport, err := newTransport(lobbyURL, secret, options)
	if err != nil {
		return nil, err
	}
	return &GameAPIClient{key: key, transport: transport}, nil
}

func (client *GameAPIClient) lobbyPath(lobbyID string, rest ...string) []string {
	return append([]string{GameAPIPath, "games", client.key, "lobbies", lobbyID}, rest...)
}

// Snapshot reads a lobby of the game, as GET
// /lobby/v1/game-api/games/{game_key}/lobbies/{lobby_id}. It replaces the
// in-process domain.SnapshotReader. A lobby that is gone, or isn't the
// game's, is domain.ErrNotFound; a failed call is unavailable.
func (client *GameAPIClient) Snapshot(ctx context.Context, lobbyID string) (domain.Snapshot, error) {
	if domain.ValidateUUID(lobbyID) != nil {
		return domain.Snapshot{}, domain.ErrNotFound
	}
	answer, err := client.transport.send(ctx, http.MethodGet, client.lobbyPath(lobbyID), nil)
	if err != nil {
		return domain.Snapshot{}, err
	}
	switch answer.status {
	case http.StatusOK:
		var snapshot domain.Snapshot
		if err := decode("the game API", answer, &snapshot); err != nil {
			return domain.Snapshot{}, err
		}
		normalizeSnapshot(&snapshot)
		return snapshot, nil
	case http.StatusNotFound:
		return domain.Snapshot{}, domain.ErrNotFound
	default:
		return domain.Snapshot{}, unexpected("the game API", answer)
	}
}

// AuthorizeWatch checks that a player may watch a lobby of the game, as POST
// .../lobbies/{lobby_id}/authorize-watch. It is a domain.WatchAuthorizer that
// reads only the principal's player ID: nil for an active member or a
// watcher the host hasn't kicked, domain.ErrWatcherKicked for a kicked
// watcher, and domain.ErrNotFound for anyone else, a lobby that is gone and a
// scope of another game.
func (client *GameAPIClient) AuthorizeWatch(ctx context.Context, principal domain.Principal, scope domain.Scope) error {
	if scope.GameKey != client.key || domain.ValidateUUID(scope.LobbyID) != nil || domain.ValidateUUID(principal.PlayerID) != nil {
		return domain.ErrNotFound
	}
	answer, err := client.transport.send(ctx, http.MethodPost, client.lobbyPath(scope.LobbyID, "authorize-watch"), AuthorizeWatchRequest{PlayerID: principal.PlayerID})
	if err != nil {
		return err
	}
	switch answer.status {
	case http.StatusNoContent:
		return nil
	case http.StatusForbidden:
		if answer.problem().Code == string(domain.CodeWatcherKicked) {
			return domain.ErrWatcherKicked
		}
		return domain.ErrNotFound
	default:
		return unexpected("the game API", answer)
	}
}

var _ domain.WatchAuthorizer = (*GameAPIClient)(nil)
