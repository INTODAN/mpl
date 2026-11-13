package mockgame_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"islandBattles/lobby/application"
	"islandBattles/lobby/auth"
	"islandBattles/lobby/domain"
	"islandBattles/lobby/gamehooks"
	"islandBattles/lobby/httpapi"
	"islandBattles/lobby/internal/testutil"
	"islandBattles/lobby/mockgame"
	lobbypostgres "islandBattles/lobby/postgres"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const gameKey = "mock-game"

// hookTimeout is short, so the tests of a slow game stay quick.
const hookTimeout = 500 * time.Millisecond

var (
	gameSecret  = []byte(strings.Repeat("m", 40))
	otherSecret = []byte(strings.Repeat("o", 40))
)

// stack is the lobby's HTTP server, configured with LOBBY_GAMES, and the mock
// game's HTTP server, which the lobby calls and which reads the lobby
// through the game API. A second mock game, other-game, has its own secret.
type stack struct {
	t         *testing.T
	lobbyURL  string
	gameURL   string
	game      *mockgame.Game
	other     *mockgame.Game
	store     domain.Store
	gameAPI   *gamehooks.GameAPIClient
	otherKeys *gamehooks.GameAPIClient
}

func newStack(t *testing.T) *stack {
	t.Helper()
	database := testutil.NewPostgresFixture(t)
	game, err := mockgame.New(gameKey, gameSecret)
	if err != nil {
		t.Fatal(err)
	}
	gameServer := httptest.NewServer(game)
	t.Cleanup(gameServer.Close)
	other, err := mockgame.New("other-game", otherSecret)
	if err != nil {
		t.Fatal(err)
	}
	otherServer := httptest.NewServer(other)
	t.Cleanup(otherServer.Close)

	configuration := fmt.Sprintf(`[
		{"key": %q, "name": "Mock game", "hooks_url": %q, "secret_env": "MOCK_GAME_SECRET", "start_rules": {"every_team_needs_member": true},
		 "frontend": {"result_url": "/mock/{lobby_id}/result"}},
		{"key": "other-game", "name": "Other game", "hooks_url": %q, "secret_env": "OTHER_GAME_SECRET"}
	]`, gameKey, gameServer.URL+mockgame.HooksPath, otherServer.URL+mockgame.HooksPath)
	environment := map[string]string{gamehooks.GamesEnv: configuration, "MOCK_GAME_SECRET": string(gameSecret), "OTHER_GAME_SECRET": string(otherSecret)}
	games, err := gamehooks.LoadGames(func(name string) (string, bool) {
		value, found := environment[name]
		return value, found
	}, gamehooks.WithTimeout(hookTimeout))
	if err != nil {
		t.Fatal(err)
	}
	clock, identifiers := domain.SystemClock{}, domain.CryptoIdentifiers{}
	policy := domain.DefaultPolicy(gamehooks.Registrations(games)...)
	store, err := lobbypostgres.NewStore(database, clock)
	if err != nil {
		t.Fatal(err)
	}
	identities, err := lobbypostgres.NewIdentityStore(database)
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := auth.NewService(identities, clock, policy, identifiers, []byte(strings.Repeat("c", 32)), "ws://127.0.0.1:1/connection/websocket")
	if err != nil {
		t.Fatal(err)
	}
	lobbies, err := application.NewService(store, clock, identifiers, policy)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	httpapi.Register(newAPI(router, "Lobby"), lobbies, authentication, authentication, nil, nil)
	httpapi.RegisterGameAPI(newAPI(router, "Lobby game API"), lobbies, gamehooks.Secrets(games))
	lobbyServer := httptest.NewServer(router)
	t.Cleanup(lobbyServer.Close)

	gameAPI, err := gamehooks.NewGameAPIClient(gameKey, lobbyServer.URL, gameSecret)
	if err != nil {
		t.Fatal(err)
	}
	game.UseLobby(gameAPI)
	// A client that signs with the mock game's secret for other-game's key.
	otherKeys, err := gamehooks.NewGameAPIClient("other-game", lobbyServer.URL, gameSecret)
	if err != nil {
		t.Fatal(err)
	}
	return &stack{t: t, lobbyURL: lobbyServer.URL, gameURL: gameServer.URL, game: game, other: other, store: store, gameAPI: gameAPI, otherKeys: otherKeys}
}

// newAPI is a Huma API on router without documentation routes, so that two
// APIs share it, as the player API and the game API do.
func newAPI(router chi.Router, title string) huma.API {
	config := huma.DefaultConfig(title, "1.0.0")
	config.OpenAPIPath, config.DocsPath, config.SchemasPath = "", "", ""
	return humachi.New(router, config)
}

type guest struct {
	playerID, token, client string
}

type problem struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Type   string `json:"type"`
	Detail string `json:"detail"`
}

// call sends a JSON request to url and decodes the answer into into, unless
// into is nil, after checking its status.
func (stack *stack) call(method, url string, who *guest, body any, status int, into any) {
	stack.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			stack.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		stack.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if who != nil {
		request.Header.Set("Authorization", "Bearer "+who.token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		stack.t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		stack.t.Fatal(err)
	}
	if response.StatusCode != status {
		stack.t.Fatalf("%s %s: status %d, want %d: %s", method, url, response.StatusCode, status, answer)
	}
	if into != nil {
		if err := json.Unmarshal(answer, into); err != nil {
			stack.t.Fatalf("%s %s: %v: %s", method, url, err, answer)
		}
	}
}

// refused expects a problem with status and code.
func (stack *stack) refused(method, url string, who *guest, body any, status int, code string) problem {
	stack.t.Helper()
	var refusal problem
	stack.call(method, url, who, body, status, &refusal)
	if refusal.Code != code {
		stack.t.Fatalf("%s %s: code %q, want %q (%+v)", method, url, refusal.Code, code, refusal)
	}
	return refusal
}

func (stack *stack) guest(name string) *guest {
	stack.t.Helper()
	var session domain.GuestSession
	stack.call(http.MethodPost, stack.lobbyURL+"/lobby/v1/sessions", nil, map[string]string{"display_name": name}, http.StatusCreated, &session)
	return &guest{playerID: session.PlayerID, token: session.AccessToken, client: uuid.NewString()}
}

func (stack *stack) lobbyPath(room domain.Snapshot, rest string) string {
	return fmt.Sprintf("%s/lobby/v1/games/%s/lobbies/%s%s", stack.lobbyURL, room.Lobby.GameKey, room.Lobby.ID, rest)
}

func createRequest(host *guest, key string, teams int, settings any) map[string]any {
	names := []map[string]any{}
	for index := range teams {
		names = append(names, map[string]any{"name": fmt.Sprintf("Team %d", index+1)})
	}
	return map[string]any{"request_id": uuid.NewString(), "game_key": key, "name": "Mock lobby", "teams": names, "client_instance_id": host.client, "game_settings": settings}
}

// readyRoom is a lobby of the mock game in which host and guest picked a
// color, chose teams 1 and 2 and got ready.
func (stack *stack) readyRoom(host, other *guest) domain.Snapshot {
	stack.t.Helper()
	var room domain.Snapshot
	stack.call(http.MethodPost, stack.lobbyURL+"/lobby/v1/lobbies", host, createRequest(host, gameKey, 2, map[string]any{"rounds": 2}), http.StatusCreated, &room)
	stack.call(http.MethodPost, stack.lobbyURL+"/lobby/v1/memberships", other, map[string]any{"game_key": gameKey, "join_code": room.Lobby.JoinCode, "client_instance_id": other.client}, http.StatusOK, nil)
	for index, member := range []*guest{host, other} {
		stack.call(http.MethodPut, stack.lobbyPath(room, "/team"), member, map[string]any{"team_id": room.Teams[index].ID}, http.StatusOK, nil)
		stack.call(http.MethodPut, stack.lobbyPath(room, "/member-settings"), member, map[string]any{"member_settings": map[string]any{"color": []string{"red", "blue"}[index]}}, http.StatusOK, nil)
		stack.call(http.MethodPut, stack.lobbyPath(room, "/ready"), member, map[string]any{"ready": true}, http.StatusOK, nil)
	}
	return room
}

// boundRound is the round the lobby recorded as bound to a game.
func (stack *stack) boundRound(room domain.Snapshot) int64 {
	stack.t.Helper()
	aggregate, err := stack.store.Read(context.Background(), room.Lobby.Scope())
	if err != nil {
		stack.t.Fatal(err)
	}
	return aggregate.Lobby.BoundRound
}

func TestTheMockGamePlaysTheWholeLobbyLifeCycleOverHTTP(t *testing.T) {
	stack := newStack(t)
	host, other, watcher, stranger := stack.guest("Ada"), stack.guest("Grace"), stack.guest("Watcher"), stack.guest("Stranger")

	// Create: the game checks the settings, with the teams.
	refusal := stack.refused(http.MethodPost, stack.lobbyURL+"/lobby/v1/lobbies", host, createRequest(host, gameKey, 3, map[string]any{"rounds": 9}), http.StatusUnprocessableEntity, "invalid_game_settings")
	if refusal.Detail != `game_settings must be {"rounds": N} with N from 1 to 5` {
		t.Fatalf("refused settings = %+v", refusal)
	}
	var room domain.Snapshot
	stack.call(http.MethodPost, stack.lobbyURL+"/lobby/v1/lobbies", host, createRequest(host, gameKey, 3, map[string]any{"rounds": 3}), http.StatusCreated, &room)
	if !room.StartRules.EveryTeamNeedsMember || string(room.Lobby.GameSettings) != `{"rounds":3}` {
		t.Fatalf("created = %+v", room.Lobby)
	}
	// Settings change: the game checks them with the lobby's ID.
	stack.refused(http.MethodPut, stack.lobbyPath(room, "/settings"), host, map[string]any{"teams": []map[string]string{{"name": "A"}, {"name": "B"}, {"name": "C"}}, "game_settings": map[string]any{"rounds": 0}}, http.StatusUnprocessableEntity, "invalid_game_settings")
	stack.call(http.MethodPut, stack.lobbyPath(room, "/settings"), host, map[string]any{"teams": []map[string]string{{"name": "A"}, {"name": "B"}, {"name": "C"}}, "game_settings": map[string]any{"rounds": 2}}, http.StatusOK, &room)

	stack.call(http.MethodPost, stack.lobbyURL+"/lobby/v1/memberships", other, map[string]any{"game_key": gameKey, "join_code": room.Lobby.JoinCode, "client_instance_id": other.client}, http.StatusOK, nil)
	for index, member := range []*guest{host, other} {
		stack.call(http.MethodPut, stack.lobbyPath(room, "/team"), member, map[string]any{"team_id": room.Teams[index].ID}, http.StatusOK, nil)
	}

	// Seats: the game checks their settings and names them.
	refusal = stack.refused(http.MethodPost, stack.lobbyPath(room, "/seats"), host, map[string]any{"team_id": room.Teams[2].ID, "seat_settings": map[string]any{"level": 2}}, http.StatusUnprocessableEntity, "invalid_seat_settings")
	if refusal.Detail != `seat_settings must be {"level": 1}` {
		t.Fatalf("refused seat = %+v", refusal)
	}
	var seated domain.Snapshot
	stack.call(http.MethodPost, stack.lobbyPath(room, "/seats"), host, map[string]any{"team_id": room.Teams[2].ID, "seat_settings": map[string]any{"level": 1}}, http.StatusOK, &seated)
	if seat := seated.Members[2]; !seat.IsSeat() || seat.DisplayName != mockgame.SeatName {
		t.Fatalf("seat = %+v", seat)
	}

	// Ready: the game requires a color in the member settings first.
	refusal = stack.refused(http.MethodPut, stack.lobbyPath(room, "/ready"), host, map[string]any{"ready": true}, http.StatusConflict, "game_requirement")
	if refusal.Type != mockgame.ColorMissingProblemType || refusal.Detail != "Choose your color before you get ready." {
		t.Fatalf("ready without a color = %+v", refusal)
	}
	refusal = stack.refused(http.MethodPut, stack.lobbyPath(room, "/member-settings"), host, map[string]any{"member_settings": map[string]any{"colour": "red"}}, http.StatusUnprocessableEntity, "invalid_member_settings")
	if refusal.Detail != `member_settings must be {"color": "<1 to 20 characters>"}` {
		t.Fatalf("refused member settings = %+v", refusal)
	}
	for index, member := range []*guest{host, other} {
		stack.call(http.MethodPut, stack.lobbyPath(room, "/member-settings"), member, map[string]any{"member_settings": map[string]any{"color": []string{"red", "blue"}[index]}}, http.StatusOK, nil)
		stack.call(http.MethodPut, stack.lobbyPath(room, "/ready"), member, map[string]any{"ready": true}, http.StatusOK, nil)
	}

	// Start: the game records the round's game, and a second start doesn't
	// ask again.
	var started domain.Snapshot
	stack.call(http.MethodPost, stack.lobbyPath(room, "/start"), host, map[string]any{}, http.StatusOK, &started)
	games := stack.game.Games()
	if started.Lobby.Phase != domain.PhaseStarted || len(games) != 1 || games[0].Round != 1 || len(games[0].Players) != 3 || games[0].Colors[host.playerID] != "red" || games[0].Colors[other.playerID] != "blue" {
		t.Fatalf("started %s; games = %+v", started.Lobby.Phase, games)
	}
	if stack.boundRound(room) != 1 {
		t.Fatalf("bound round = %d", stack.boundRound(room))
	}
	stack.call(http.MethodPost, stack.lobbyPath(room, "/start"), host, map[string]any{}, http.StatusOK, nil)
	if calls := stack.game.Calls(gamehooks.HookStart); calls != 1 {
		t.Fatalf("start hook calls = %d", calls)
	}

	// The game reads the lobby through the game API.
	snapshot, err := stack.gameAPI.Snapshot(context.Background(), room.Lobby.ID)
	if err != nil || snapshot.Lobby.ID != room.Lobby.ID || len(snapshot.StartRoster) != 3 || snapshot.MembershipGeneration != 0 || string(snapshot.Members[0].MemberSettings) != `{"color":"red"}` || snapshot.Members[2].MemberSettings != nil {
		t.Fatalf("game API snapshot = %+v, %v", snapshot, err)
	}

	// A watcher is authorized through the game API; a stranger isn't, and
	// neither is the watcher once the host kicks them.
	stack.call(http.MethodPost, stack.lobbyURL+"/lobby/v1/watches", watcher, map[string]any{"game_key": gameKey, "join_code": room.Lobby.JoinCode}, http.StatusOK, nil)
	var watched mockgame.Watch
	stack.call(http.MethodGet, stack.gameURL+"/watch/"+room.Lobby.ID+"?player_id="+watcher.playerID, nil, nil, http.StatusOK, &watched)
	if watched.GameID != games[0].ID || watched.Round != 1 || watched.Phase != "started" {
		t.Fatalf("watched = %+v", watched)
	}
	stack.call(http.MethodGet, stack.gameURL+"/watch/"+room.Lobby.ID+"?player_id="+host.playerID, nil, nil, http.StatusOK, nil)
	stack.refused(http.MethodGet, stack.gameURL+"/watch/"+room.Lobby.ID+"?player_id="+stranger.playerID, nil, nil, http.StatusForbidden, "permission_denied")
	stack.call(http.MethodDelete, stack.lobbyPath(room, "/watchers/"+watcher.playerID), host, nil, http.StatusNoContent, nil)
	stack.refused(http.MethodGet, stack.gameURL+"/watch/"+room.Lobby.ID+"?player_id="+watcher.playerID, nil, nil, http.StatusForbidden, "watcher_kicked")

	// Reopen: the game's round is running until the game finishes it.
	stack.refused(http.MethodPost, stack.lobbyPath(room, "/reopen"), other, map[string]any{}, http.StatusConflict, "game_running")
	stack.call(http.MethodPost, stack.gameURL+"/finish/"+room.Lobby.ID, nil, nil, http.StatusNoContent, nil)
	var reopened domain.Snapshot
	stack.call(http.MethodPost, stack.lobbyPath(room, "/reopen"), other, map[string]any{}, http.StatusOK, &reopened)
	if reopened.Lobby.Phase != domain.PhaseWaiting || reopened.Lobby.Round != 2 || string(reopened.Members[0].MemberSettings) != `{"color":"red"}` {
		t.Fatalf("reopened = %+v", reopened)
	}

	// The next round starts a new game.
	for _, member := range []*guest{host, other} {
		stack.call(http.MethodPut, stack.lobbyPath(room, "/ready"), member, map[string]any{"ready": true}, http.StatusOK, nil)
	}
	stack.call(http.MethodPost, stack.lobbyPath(room, "/start"), host, map[string]any{}, http.StatusOK, nil)
	games = stack.game.Games()
	if len(games) != 2 || games[1].Round != 2 || games[1].ID == games[0].ID || stack.boundRound(room) != 2 {
		t.Fatalf("games after the second start = %+v, bound round %d", games, stack.boundRound(room))
	}
}

// The ready check at the start covers the whole roster, and names who must
// act.
func TestTheStartChecksEveryPersonOfTheRoster(t *testing.T) {
	stack := newStack(t)
	host, other := stack.guest("Ada"), stack.guest("Grace")
	room := stack.readyRoom(host, other)
	// Grace's color is gone, as if she got ready before the game required
	// one, which the API can't reproduce.
	if _, err := stack.store.Transact(context.Background(), room.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		domain.FindMember(aggregate, other.playerID).MemberSettings = nil
		return domain.Change{PrivateChanged: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	refusal := stack.refused(http.MethodPost, stack.lobbyPath(room, "/start"), host, map[string]any{}, http.StatusConflict, "game_requirement")
	if refusal.Type != mockgame.ColorMissingProblemType || !strings.Contains(refusal.Detail, "Grace") || strings.Contains(refusal.Detail, "Ada") {
		t.Fatalf("start = %+v", refusal)
	}
	if len(stack.game.Games()) != 0 {
		t.Fatal("a refused start created a game")
	}
}

func TestAFailedStartKeepsTheLobbyStartedUntilTheHostRetries(t *testing.T) {
	stack := newStack(t)
	host, other := stack.guest("Ada"), stack.guest("Grace")
	room := stack.readyRoom(host, other)

	// The game fails, and then is too slow: the start is committed each time,
	// and the host gets game_unavailable.
	stack.game.Fail(gamehooks.HookStart, http.StatusInternalServerError, 1)
	stack.game.Delay(gamehooks.HookStart, 3*hookTimeout, 1)
	for range 2 {
		refusal := stack.refused(http.MethodPost, stack.lobbyPath(room, "/start"), host, map[string]any{}, http.StatusBadGateway, "game_unavailable")
		if !strings.Contains(refusal.Detail, "start it again") {
			t.Fatalf("refusal = %+v", refusal)
		}
		var current domain.Snapshot
		stack.call(http.MethodGet, stack.lobbyPath(room, ""), other, nil, http.StatusOK, &current)
		if current.Lobby.Phase != domain.PhaseStarted || len(current.StartRoster) != 2 || stack.boundRound(room) != 0 {
			t.Fatalf("after a failed start: phase %s, roster %d, bound round %d", current.Lobby.Phase, len(current.StartRoster), stack.boundRound(room))
		}
	}
	// Only the host retries; anyone else is refused without calling the game.
	stack.refused(http.MethodPost, stack.lobbyPath(room, "/start"), other, map[string]any{}, http.StatusForbidden, "not_host")
	if calls := stack.game.Calls(gamehooks.HookStart); calls != 2 {
		t.Fatalf("start hook calls = %d", calls)
	}
	// The retry calls the hook again, which creates the game. The game timed
	// out above but may have finished later, so it binds idempotently.
	var started domain.Snapshot
	stack.call(http.MethodPost, stack.lobbyPath(room, "/start"), host, map[string]any{}, http.StatusOK, &started)
	if games := stack.game.Games(); started.Lobby.Phase != domain.PhaseStarted || len(games) != 1 || stack.boundRound(room) != 1 {
		t.Fatalf("after the retry: phase %s, games %+v, bound round %d", started.Lobby.Phase, games, stack.boundRound(room))
	}
	stack.call(http.MethodPost, stack.lobbyPath(room, "/start"), host, map[string]any{}, http.StatusOK, nil)
	if calls := stack.game.Calls(gamehooks.HookStart); calls != 3 {
		t.Fatalf("start hook calls after the game confirmed = %d", calls)
	}
}

// A hook that fails, answers 5xx or is too slow makes the player's request
// unavailable (503), and changes nothing.
func TestAFailingGameMakesRequestsUnavailable(t *testing.T) {
	stack := newStack(t)
	host, other := stack.guest("Ada"), stack.guest("Grace")

	stack.game.Fail(gamehooks.HookValidateSettings, http.StatusServiceUnavailable, 1)
	stack.refused(http.MethodPost, stack.lobbyURL+"/lobby/v1/lobbies", host, createRequest(host, gameKey, 2, map[string]any{"rounds": 1}), http.StatusServiceUnavailable, "unavailable")
	var room domain.Snapshot
	stack.call(http.MethodPost, stack.lobbyURL+"/lobby/v1/lobbies", host, createRequest(host, gameKey, 2, map[string]any{"rounds": 1}), http.StatusCreated, &room)

	stack.game.Delay(gamehooks.HookValidateMemberSettings, 3*hookTimeout, 1)
	stack.refused(http.MethodPut, stack.lobbyPath(room, "/member-settings"), host, map[string]any{"member_settings": map[string]any{"color": "red"}}, http.StatusServiceUnavailable, "unavailable")
	stack.game.Fail(gamehooks.HookValidateSeat, http.StatusBadGateway, 1)
	stack.refused(http.MethodPost, stack.lobbyPath(room, "/seats"), host, map[string]any{"team_id": room.Teams[1].ID, "seat_settings": map[string]any{"level": 1}}, http.StatusServiceUnavailable, "unavailable")

	stack.call(http.MethodPut, stack.lobbyPath(room, "/team"), host, map[string]any{"team_id": room.Teams[0].ID}, http.StatusOK, nil)
	stack.call(http.MethodPut, stack.lobbyPath(room, "/member-settings"), host, map[string]any{"member_settings": map[string]any{"color": "red"}}, http.StatusOK, nil)
	stack.game.Fail(gamehooks.HookReadyCheck, http.StatusInternalServerError, 1)
	stack.refused(http.MethodPut, stack.lobbyPath(room, "/ready"), host, map[string]any{"ready": true}, http.StatusServiceUnavailable, "unavailable")
	var current domain.Snapshot
	stack.call(http.MethodGet, stack.lobbyPath(room, ""), host, nil, http.StatusOK, &current)
	if current.Members[0].Ready {
		t.Fatal("a failed ready check set the member ready")
	}

	started := stack.readyRoom(host, other)
	stack.call(http.MethodPost, stack.lobbyPath(started, "/start"), host, map[string]any{}, http.StatusOK, nil)
	stack.game.Finish(started.Lobby.ID)
	stack.game.Fail(gamehooks.HookRoundEnded, http.StatusInternalServerError, 1)
	stack.refused(http.MethodPost, stack.lobbyPath(started, "/reopen"), host, map[string]any{}, http.StatusServiceUnavailable, "unavailable")
	stack.call(http.MethodPost, stack.lobbyPath(started, "/reopen"), host, map[string]any{}, http.StatusOK, nil)
}

// Each game reaches the game API with its own secret, for its own key only,
// and the game refuses hook calls that the lobby didn't sign.
func TestRequestsAreSignedBothWays(t *testing.T) {
	stack := newStack(t)
	host, other := stack.guest("Ada"), stack.guest("Grace")
	room := stack.readyRoom(host, other)
	ctx := context.Background()

	// mock-game's secret doesn't sign for other-game.
	if _, err := stack.otherKeys.Snapshot(ctx, room.Lobby.ID); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("other-game's snapshot with mock-game's secret = %v", err)
	}
	// A lobby of other-game isn't mock-game's.
	var otherRoom domain.Snapshot
	stack.call(http.MethodPost, stack.lobbyURL+"/lobby/v1/lobbies", host, createRequest(host, "other-game", 2, map[string]any{"rounds": 1}), http.StatusCreated, &otherRoom)
	if _, err := stack.gameAPI.Snapshot(ctx, otherRoom.Lobby.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other-game's lobby through mock-game = %v", err)
	}
	if err := stack.gameAPI.AuthorizeWatch(ctx, domain.Principal{PlayerID: host.playerID}, otherRoom.Lobby.Scope()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("watching other-game's lobby through mock-game = %v", err)
	}

	// The game API refuses unsigned, stale and unknown games' requests.
	path := gamehooks.GameAPIPath + "/games/" + gameKey + "/lobbies/" + room.Lobby.ID
	for name, header := range map[string]string{
		"unsigned":       "",
		"stale":          gamehooks.Sign(gameSecret, time.Now().Add(-6*time.Minute), http.MethodGet, path, nil),
		"another secret": gamehooks.Sign(otherSecret, time.Now(), http.MethodGet, path, nil),
	} {
		request, _ := http.NewRequest(http.MethodGet, stack.lobbyURL+path, nil)
		if header != "" {
			request.Header.Set(gamehooks.SignatureHeader, header)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d", name, response.StatusCode)
		}
	}
	for _, key := range []string{"unknown-game", "Mock"} {
		unknownPath := gamehooks.GameAPIPath + "/games/" + key + "/lobbies/" + room.Lobby.ID
		request, _ := http.NewRequest(http.MethodGet, stack.lobbyURL+unknownPath, nil)
		request.Header.Set(gamehooks.SignatureHeader, gamehooks.Sign(gameSecret, time.Now(), http.MethodGet, unknownPath, nil))
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("game %s: status %d", key, response.StatusCode)
		}
	}
	// A signed request with a tampered body is refused.
	authorizePath := path + "/authorize-watch"
	request, _ := http.NewRequest(http.MethodPost, stack.lobbyURL+authorizePath, strings.NewReader(`{"player_id":"`+host.playerID+`"}`))
	request.Header.Set(gamehooks.SignatureHeader, gamehooks.Sign(gameSecret, time.Now(), http.MethodPost, authorizePath, []byte(`{"player_id":"`+other.playerID+`"}`)))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("tampered body: status %d", response.StatusCode)
	}

	// The game refuses a hook call without the lobby's signature, before
	// anything else.
	stack.refused(http.MethodPost, stack.gameURL+mockgame.HooksPath+"/"+gamehooks.HookStart, nil, map[string]any{"snapshot": room}, http.StatusUnauthorized, "unauthenticated")
	if calls := stack.game.Calls(gamehooks.HookStart); calls != 0 {
		t.Fatalf("unsigned start calls counted = %d", calls)
	}
}
