package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"islandBattles/lobby/app"
	"islandBattles/lobby/domain"
	"islandBattles/lobby/gamehooks"
	"islandBattles/lobby/internal/testutil"
	"islandBattles/lobby/playertoken"
	"islandBattles/lobby/realtime"

	"github.com/google/uuid"
)

// fakeCentrifugo answers each command of a Centrifugo API request, one JSON
// command per line, with success.
func fakeCentrifugo(response http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	commands := strings.Count(strings.TrimSpace(string(body)), "\n") + 1
	_, _ = io.WriteString(response, strings.TrimSuffix(strings.Repeat(`{"result":{}}`+"\n", commands), "\n"))
}

// readingGame is a game served over HTTP whose ready check reads the lobby
// through the game API before it answers, as Island Battles' does at the
// start, and whose start records a game.
type readingGame struct {
	lobby  *gamehooks.GameAPIClient
	reads  atomic.Int32
	starts atomic.Int32
}

func (game *readingGame) registration() domain.GameRegistration {
	return domain.GameRegistration{
		Key: "mock-game",
		ReadyGate: func(ctx context.Context, lobbyID string) (domain.ReadyCheck, error) {
			if _, err := game.lobby.Snapshot(ctx, lobbyID); err != nil {
				return nil, fmt.Errorf("%w: %w", domain.ErrUnavailable, err)
			}
			game.reads.Add(1)
			return func([]domain.ReadyMember, bool) error { return nil }, nil
		},
		StartGame: func(ctx context.Context, snapshot domain.Snapshot) (string, error) {
			game.starts.Add(1)
			return "game-" + snapshot.Lobby.ID, nil
		},
	}
}

type server struct {
	t   *testing.T
	url string
}

// newServer serves the lobby as cmd/lobby does, from its LOBBY_* settings,
// on a pool of maxOpen connections, with one game, readingGame, at a server
// of its own.
func newServer(t *testing.T, maxOpen int) (*server, *readingGame) {
	t.Helper()
	database := testutil.NewPostgresFixture(t)
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(maxOpen)
	centrifugo := httptest.NewServer(http.HandlerFunc(fakeCentrifugo))
	t.Cleanup(centrifugo.Close)
	game := &readingGame{}
	hooks, err := gamehooks.NewHandler([]byte(testGameSecret), game.registration())
	if err != nil {
		t.Fatal(err)
	}
	gameRouter := http.NewServeMux()
	gameRouter.Handle("POST /hooks/{hook}", hooks)
	gameServer := httptest.NewServer(gameRouter)
	t.Cleanup(gameServer.Close)

	environment := testEnvironment()
	environment[app.CentrifugoAPIURLEnv] = centrifugo.URL + "/api"
	environment[app.GamesEnv] = fmt.Sprintf(`[{"key": "mock-game", "name": "Mock game", "hooks_url": %q, "secret_env": "LOBBY_GAME_SECRET_MOCK_GAME",
		"frontend": {"result_url": "/mock/{lobby_id}/result"}}]`, gameServer.URL+"/hooks")
	config, err := load(environment)
	if err != nil {
		t.Fatal(err)
	}
	lobby, err := app.New(config, database, app.NewCentrifugoClient(config), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := lobby.Handler()
	if err != nil {
		t.Fatal(err)
	}
	lobbyServer := httptest.NewServer(handler)
	t.Cleanup(lobbyServer.Close)
	game.lobby, err = gamehooks.NewGameAPIClient("mock-game", lobbyServer.URL, []byte(testGameSecret))
	if err != nil {
		t.Fatal(err)
	}
	return &server{t: t, url: lobbyServer.URL}, game
}

// call sends a JSON request and decodes the answer into into, unless into is
// nil, after checking its status.
func (server *server) call(method, path, token string, body any, status int, into any) http.Header {
	server.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			server.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, server.url+path, reader)
	if err != nil {
		server.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		server.t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		server.t.Fatal(err)
	}
	if response.StatusCode != status {
		server.t.Fatalf("%s %s: status %d, want %d: %s", method, path, response.StatusCode, status, answer)
	}
	if into != nil {
		if err := json.Unmarshal(answer, into); err != nil {
			server.t.Fatalf("%s %s: %v: %s", method, path, err, answer)
		}
	}
	return response.Header
}

func (server *server) guest(name string) domain.GuestSession {
	server.t.Helper()
	var session domain.GuestSession
	server.call(http.MethodPost, "/lobby/v1/sessions", "", map[string]string{"display_name": name}, http.StatusCreated, &session)
	return session
}

func TestTheLobbyServesEverythingOnItsOwn(t *testing.T) {
	lobby, _ := newServer(t, 2)
	if header := lobby.call(http.MethodGet, "/", "", nil, http.StatusTemporaryRedirect, nil); header.Get("Location") != app.UIPath {
		t.Fatalf("/ redirects to %q", header.Get("Location"))
	}
	lobby.call(http.MethodGet, "/healthz", "", nil, http.StatusNoContent, nil)
	var catalog []map[string]any
	lobby.call(http.MethodGet, app.UIPath+"games.json", "", nil, http.StatusOK, &catalog)
	if len(catalog) != 1 || catalog[0]["key"] != "mock-game" {
		t.Fatalf("catalog = %v", catalog)
	}
	var document struct {
		Info  struct{ Title string } `json:"info"`
		Paths map[string]any         `json:"paths"`
	}
	lobby.call(http.MethodGet, "/openapi.json", "", nil, http.StatusOK, &document)
	if document.Info.Title != "Generic Lobby API" || document.Paths["/lobby/v1/lobbies"] == nil {
		t.Fatalf("the OpenAPI document is %+v", document.Info)
	}

	alice := lobby.guest("Alice")
	var token domain.PlayerToken
	lobby.call(http.MethodPost, "/lobby/v1/player-tokens", alice.AccessToken, map[string]string{"game_key": "mock-game"}, http.StatusOK, &token)
	// A game verifies the token with the lobby's JWKS, and the lobby's
	// public URL is its issuer.
	keys, err := playertoken.NewJWKSSource(lobby.url + playertoken.JWKSPath)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := playertoken.NewVerifier("http://localhost:8890", keys)
	if err != nil {
		t.Fatal(err)
	}
	if player, err := verifier.Verify(context.Background(), token.Token, "mock-game"); err != nil || player.ID != alice.PlayerID || player.Name != "Alice" {
		t.Fatalf("player = %+v, error = %v", player, err)
	}
	var connection domain.ConnectionToken
	lobby.call(http.MethodPost, "/lobby/v1/connection-token", alice.AccessToken, map[string]any{}, http.StatusOK, &connection)
	if connection.WebsocketURL != "ws://127.0.0.1:8000/connection/websocket" {
		t.Fatalf("connection token = %+v", connection)
	}
	// Centrifugo's proxy calls need the proxy secret.
	lobby.call(http.MethodPost, realtime.SubscribePath, "", map[string]string{"user": alice.PlayerID, "channel": "player:" + alice.PlayerID}, http.StatusUnauthorized, nil)
	// The game API needs the game's signature.
	lobby.call(http.MethodGet, gamehooks.GameAPIPath+"/games/mock-game/lobbies/"+uuid.NewString(), "", nil, http.StatusUnauthorized, nil)
}

// The lobby holds the room's lock while it calls a game's ready-check hook,
// and the game may read the same lobby through the game API before it
// answers. That read takes no lock, so neither a ready request nor the start
// waits on itself, even on Lambda's pool of two connections: the request's
// transaction holds one, and the game's read takes the other.
func TestAReadyCheckThatReadsTheLobbyDoesNotWaitOnItsOwnLock(t *testing.T) {
	lobby, game := newServer(t, 2)
	host, other := lobby.guest("Host"), lobby.guest("Other")
	var room domain.Snapshot
	lobby.call(http.MethodPost, "/lobby/v1/lobbies", host.AccessToken, map[string]any{"request_id": uuid.NewString(), "game_key": "mock-game", "name": "Reading",
		"teams": []map[string]string{{"name": "Red"}, {"name": "Blue"}}, "client_instance_id": uuid.NewString()}, http.StatusCreated, &room)
	lobby.call(http.MethodPost, "/lobby/v1/memberships", other.AccessToken, map[string]any{"game_key": "mock-game", "join_code": room.Lobby.JoinCode,
		"client_instance_id": uuid.NewString()}, http.StatusOK, nil)
	path := fmt.Sprintf("/lobby/v1/games/mock-game/lobbies/%s", room.Lobby.ID)
	started := time.Now()
	for index, member := range []domain.GuestSession{host, other} {
		lobby.call(http.MethodPut, path+"/team", member.AccessToken, map[string]any{"team_id": room.Teams[index].ID}, http.StatusOK, nil)
		lobby.call(http.MethodPut, path+"/ready", member.AccessToken, map[string]any{"ready": true}, http.StatusOK, nil)
	}
	var snapshot domain.Snapshot
	lobby.call(http.MethodPost, path+"/start", host.AccessToken, map[string]any{}, http.StatusOK, &snapshot)
	if snapshot.Lobby.Phase != domain.PhaseStarted || game.reads.Load() != 3 || game.starts.Load() != 1 {
		t.Fatalf("phase = %s after %d reads and %d starts", snapshot.Lobby.Phase, game.reads.Load(), game.starts.Load())
	}
	// A wait for the lock would last until the hook's timeout.
	if elapsed := time.Since(started); elapsed >= gamehooks.DefaultTimeout {
		t.Fatalf("the ready requests and the start took %v", elapsed)
	}
}
