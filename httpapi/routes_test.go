package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"islandBattles/lobby/domain"
)

const testUUID = "11111111-1111-4111-8111-111111111111"

func TestPublicLobbyOperationContract(t *testing.T) {
	t.Parallel()
	_, api := newTestAPI(t)
	expected := map[string]struct {
		method, operationID string
		status              int
	}{
		"/lobby/v1/sessions":                                                        {http.MethodPost, "createLobbySession", http.StatusCreated},
		"/lobby/v1/connection-token":                                                {http.MethodPost, "createLobbyConnectionToken", http.StatusOK},
		"/lobby/v1/lobbies":                                                         {http.MethodPost, "createLobby", http.StatusCreated},
		"/lobby/v1/memberships":                                                     {http.MethodPost, "joinLobby", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}":                             {http.MethodGet, "getLobby", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/team":                        {http.MethodPut, "selectLobbyTeam", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/ready":                       {http.MethodPut, "setLobbyReady", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/start":                       {http.MethodPost, "startLobby", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/settings":                    {http.MethodPut, "changeLobbySettings", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/reopen":                      {http.MethodPost, "reopenLobby", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/seats":                       {http.MethodPost, "addLobbySeat", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/seats/{seat_id}":             {http.MethodDelete, "removeLobbySeat", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/membership":                  {http.MethodDelete, "leaveLobby", http.StatusNoContent},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/heartbeat":                   {http.MethodPost, "heartbeatLobby", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/leases/{client_instance_id}": {http.MethodDelete, "releaseLobbyLease", http.StatusNoContent},
		"/lobby/v1/watches":                                                         {http.MethodPost, "watchLobby", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/watch":                       {http.MethodDelete, "unwatchLobby", http.StatusNoContent},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/watchers":                    {http.MethodGet, "listLobbyWatchers", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/watchers/{player_id}":        {http.MethodDelete, "kickLobbyWatcher", http.StatusNoContent},
	}
	for path, want := range expected {
		item := api.OpenAPI().Paths[path]
		if item == nil {
			t.Fatalf("missing path %s", path)
		}
		operation := item.Get
		switch want.method {
		case http.MethodPost:
			operation = item.Post
		case http.MethodPut:
			operation = item.Put
		case http.MethodDelete:
			operation = item.Delete
		}
		if operation == nil || operation.OperationID != want.operationID || operation.Responses[strconvStatus(want.status)] == nil {
			t.Fatalf("%s operation mismatch: %#v", path, operation)
		}
		if path != "/lobby/v1/sessions" && len(operation.Security) != 1 {
			t.Fatalf("%s missing bearer security", path)
		}
	}
}

func TestAnonymousSessionOnlyAndAuthenticationPrecedesInputValidation(t *testing.T) {
	t.Parallel()
	_, api := newTestAPI(t)
	if response := api.Post("/lobby/v1/sessions", map[string]string{"display_name": "Guest"}); response.Code != http.StatusCreated {
		t.Fatalf("session status = %d", response.Code)
	}
	response := api.Put("/lobby/v1/games/INVALID/lobbies/not-a-uuid/team", map[string]any{})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing bearer status = %d, want 401: %s", response.Code, response.Body.String())
	}
	response = api.Put("/lobby/v1/games/INVALID/lobbies/not-a-uuid/team", "Authorization: Bearer bad", map[string]any{})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("bad bearer status = %d, want 401", response.Code)
	}
}

func TestTeamNullVersusMissingAndReadyFalseVersusMissing(t *testing.T) {
	t.Parallel()
	application := &fakeApplication{}
	_, api := newTestAPIWithApp(t, application)
	path := "/lobby/v1/games/island-battles/lobbies/" + testUUID
	response := api.Put(path+"/team", "Authorization: Bearer good", map[string]any{"team_id": nil})
	if response.Code != http.StatusOK || application.teamRequest.TeamID != nil {
		t.Fatalf("null team = status %d request %#v", response.Code, application.teamRequest)
	}
	response = api.Put(path+"/team", "Authorization: Bearer good", map[string]any{})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing team status = %d", response.Code)
	}
	response = api.Put(path+"/ready", "Authorization: Bearer good", map[string]any{"ready": false})
	if response.Code != http.StatusOK || application.readyRequest.Ready {
		t.Fatalf("false ready = status %d request %#v", response.Code, application.readyRequest)
	}
	response = api.Put(path+"/ready", "Authorization: Bearer good", map[string]any{})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing ready status = %d", response.Code)
	}
}

func TestClaimedPlayerCannotOverrideBearer(t *testing.T) {
	t.Parallel()
	application := &fakeApplication{}
	_, api := newTestAPIWithApp(t, application)
	response := api.Post("/lobby/v1/lobbies", "Authorization: Bearer good", map[string]any{
		"request_id": testUUID, "game_key": "island-battles", "name": "Lobby",
		"teams":              []map[string]any{{"name": "One"}, {"name": "Two"}},
		"client_instance_id": testUUID, "player_id": "22222222-2222-4222-8222-222222222222",
	})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("claimed player status = %d: %s", response.Code, response.Body.String())
	}
	if application.createPrincipal.PlayerID != "" {
		t.Fatalf("claimed player reached service: %#v", application.createPrincipal)
	}
}

// A team holds one member, so a create request has no capacity to choose,
// for the lobby or for a team.
func TestCreateRefusesCapacities(t *testing.T) {
	t.Parallel()
	application := &fakeApplication{}
	_, api := newTestAPIWithApp(t, application)
	for _, body := range []string{
		`{"request_id":"` + testUUID + `","game_key":"quiz-demo","name":"Lobby","capacity":2,"teams":[{"name":"One"},{"name":"Two"}],"client_instance_id":"` + testUUID + `"}`,
		`{"request_id":"` + testUUID + `","game_key":"quiz-demo","name":"Lobby","teams":[{"name":"One","capacity":1},{"name":"Two"}],"client_instance_id":"` + testUUID + `"}`,
	} {
		response := api.Post("/lobby/v1/lobbies", "Authorization: Bearer good", strings.NewReader(body))
		if response.Code != http.StatusUnprocessableEntity || application.createRequest.RequestID != "" {
			t.Fatalf("create with a capacity = %d %s", response.Code, response.Body.String())
		}
	}
}

func TestCreatePassesGameSettingsThroughUnread(t *testing.T) {
	t.Parallel()
	application := &fakeApplication{}
	_, api := newTestAPIWithApp(t, application)
	body := func(settings string) io.Reader {
		return strings.NewReader(`{"request_id":"` + testUUID + `","game_key":"quiz-demo","name":"Lobby",` +
			`"teams":[{"name":"One"},{"name":"Two"}],"client_instance_id":"` + testUUID + `"` + settings + `}`)
	}
	const settings = `{"rounds": 3, "seed": 12345678901234567890123}`
	response := api.Post("/lobby/v1/lobbies", "Authorization: Bearer good", body(`,"game_settings":`+settings))
	if response.Code != http.StatusCreated || string(application.createRequest.GameSettings) != settings {
		t.Fatalf("create = %d %s; application received %s", response.Code, response.Body.String(), application.createRequest.GameSettings)
	}
	if !strings.Contains(response.Body.String(), `"game_settings":{"rounds":3,"seed":12345678901234567890123}`) {
		t.Fatalf("snapshot settings: %s", response.Body.String())
	}
	response = api.Post("/lobby/v1/lobbies", "Authorization: Bearer good", body(""))
	if response.Code != http.StatusCreated || application.createRequest.GameSettings != nil || !strings.Contains(response.Body.String(), `"game_settings":{}`) {
		t.Fatalf("omitted settings = %d %s; application received %q", response.Code, response.Body.String(), application.createRequest.GameSettings)
	}
	for _, invalid := range []string{`"text"`, `[1]`} {
		application.createRequest = domain.CreateRequest{}
		response = api.Post("/lobby/v1/lobbies", "Authorization: Bearer good", body(`,"game_settings":`+invalid))
		if response.Code != http.StatusUnprocessableEntity || application.createRequest.RequestID != "" {
			t.Fatalf("settings %s = %d %s", invalid, response.Code, response.Body.String())
		}
	}
	// The schema lets an explicit null through, so it must reach the domain,
	// which rejects it, rather than read as omitted settings.
	application.createRequest = domain.CreateRequest{}
	api.Post("/lobby/v1/lobbies", "Authorization: Bearer good", body(`,"game_settings":null`))
	if string(application.createRequest.GameSettings) != "null" {
		t.Fatalf("null settings reached the application as %q", application.createRequest.GameSettings)
	}
	application.createError = domain.NewError(domain.CodeInvalidGameSettings, "rounds must be between 1 and 10")
	response = api.Post("/lobby/v1/lobbies", "Authorization: Bearer good", body(`,"game_settings":{"rounds":0}`))
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || response.Code != http.StatusUnprocessableEntity || problem.Code != domain.CodeInvalidGameSettings || problem.Detail != "rounds must be between 1 and 10" {
		t.Fatalf("rejected settings = %d %s", response.Code, response.Body.String())
	}
}

func TestChangeSettingsPassesTeamsAndSettingsThrough(t *testing.T) {
	t.Parallel()
	application := &fakeApplication{}
	_, api := newTestAPIWithApp(t, application)
	path := "/lobby/v1/games/quiz-demo/lobbies/" + testUUID + "/settings"
	const settings = `{"rounds": 3, "seed": 12345678901234567890123}`
	response := api.Put(path, "Authorization: Bearer good", strings.NewReader(`{"teams":[{"name":"Blue"},{"name":"Red"},{"name":"Green"}],"game_settings":`+settings+`}`))
	if response.Code != http.StatusOK || string(application.settingsRequest.GameSettings) != settings || len(application.settingsRequest.Teams) != 3 || application.settingsRequest.Teams[2].Name != "Green" || application.settingsScope != (domain.Scope{GameKey: "quiz-demo", LobbyID: testUUID}) {
		t.Fatalf("change settings = %d %s; application received %#v in %#v", response.Code, response.Body.String(), application.settingsRequest, application.settingsScope)
	}
	if !strings.Contains(response.Body.String(), `"game_settings":{"rounds":3,"seed":12345678901234567890123}`) {
		t.Fatalf("snapshot settings: %s", response.Body.String())
	}
	application.settingsRequest = domain.ChangeSettingsRequest{}
	response = api.Put(path, "Authorization: Bearer good", map[string]any{"teams": []map[string]any{{"name": "Blue"}, {"name": "Red"}}})
	if response.Code != http.StatusOK || application.settingsRequest.GameSettings != nil || len(application.settingsRequest.Teams) != 2 {
		t.Fatalf("omitted settings = %d %s; application received %#v", response.Code, response.Body.String(), application.settingsRequest)
	}
	for _, invalid := range []map[string]any{{}, {"teams": []map[string]any{{"name": "Blue"}}}, {"teams": []map[string]any{{"name": "Blue"}, {"name": "Red"}}, "game_settings": "text"}} {
		application.settingsRequest = domain.ChangeSettingsRequest{}
		response = api.Put(path, "Authorization: Bearer good", invalid)
		if response.Code != http.StatusUnprocessableEntity || application.settingsRequest.Teams != nil {
			t.Fatalf("invalid body %v = %d %s", invalid, response.Code, response.Body.String())
		}
	}
	for code, status := range map[domain.ErrorCode]int{domain.CodeNotHost: http.StatusForbidden, domain.CodePhaseConflict: http.StatusConflict, domain.CodeLobbyFull: http.StatusConflict, domain.CodeInvalidGameSettings: http.StatusUnprocessableEntity} {
		application.settingsError = domain.NewError(code, "refused")
		response = api.Put(path, "Authorization: Bearer good", map[string]any{"teams": []map[string]any{{"name": "Blue"}, {"name": "Red"}}})
		var problem Problem
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || response.Code != status || problem.Code != code {
			t.Fatalf("%s = %d %s", code, response.Code, response.Body.String())
		}
	}
}

func TestSeatsPassSettingsThroughUnreadAndShowTheirKind(t *testing.T) {
	t.Parallel()
	application := &fakeApplication{}
	_, api := newTestAPIWithApp(t, application)
	path := "/lobby/v1/games/island-battles/lobbies/" + testUUID + "/seats"
	const settings = `{"bot_level": 2}`
	response := api.Post(path, "Authorization: Bearer good", strings.NewReader(`{"team_id":"`+testUUID+`","seat_settings":`+settings+`}`))
	if response.Code != http.StatusOK || application.seatRequest.TeamID != testUUID || string(application.seatRequest.SeatSettings) != settings {
		t.Fatalf("add seat = %d %s; application received %#v", response.Code, response.Body.String(), application.seatRequest)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"kind":"human","seat_settings":null`) || !strings.Contains(body, `"kind":"seat","seat_settings":{"bot_level":2}`) {
		t.Fatalf("snapshot members: %s", body)
	}
	application.seatRequest = domain.AddSeatRequest{}
	response = api.Post(path, "Authorization: Bearer good", map[string]any{"team_id": testUUID})
	if response.Code != http.StatusUnprocessableEntity || application.seatRequest.TeamID != "" {
		t.Fatalf("missing seat settings = %d %s", response.Code, response.Body.String())
	}
	application.seatError = domain.NewError(domain.CodeInvalidSeatSettings, "bot_level must be 1, 2 or 3")
	response = api.Post(path, "Authorization: Bearer good", map[string]any{"team_id": testUUID, "seat_settings": map[string]any{"bot_level": 9}})
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || response.Code != http.StatusUnprocessableEntity || problem.Code != domain.CodeInvalidSeatSettings || problem.Detail != "bot_level must be 1, 2 or 3" {
		t.Fatalf("rejected seat settings = %d %s", response.Code, response.Body.String())
	}
	response = api.Delete(path+"/"+testUUID, "Authorization: Bearer good")
	if response.Code != http.StatusOK || application.removedSeat != testUUID || !strings.Contains(response.Body.String(), `"join_code":"SECRETCODE"`) {
		t.Fatalf("remove seat = %d %s", response.Code, response.Body.String())
	}
	if response = api.Delete(path+"/not-a-uuid", "Authorization: Bearer good"); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("remove invalid seat ID = %d", response.Code)
	}
}

func TestWatchRoutesReturnTheWatchChannelListAndKick(t *testing.T) {
	t.Parallel()
	application := &fakeApplication{}
	_, api := newTestAPIWithApp(t, application)
	response := api.Post("/lobby/v1/watches", "Authorization: Bearer good", map[string]string{"game_key": "island-battles", "join_code": "abcd2345"})
	if response.Code != http.StatusOK || application.watchRequest.JoinCode != "abcd2345" ||
		!strings.Contains(response.Body.String(), `"channel":"watch:island-battles:`+testUUID+`"`) || !strings.Contains(response.Body.String(), `"member":false`) {
		t.Fatalf("watch = %d %s", response.Code, response.Body.String())
	}
	application.watchError = domain.ErrWatcherKicked
	response = api.Post("/lobby/v1/watches", "Authorization: Bearer good", map[string]string{"game_key": "island-battles", "join_code": "abcd2345"})
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || response.Code != http.StatusForbidden || problem.Code != domain.CodeWatcherKicked {
		t.Fatalf("kicked watch = %d %s", response.Code, response.Body.String())
	}
	path := "/lobby/v1/games/island-battles/lobbies/" + testUUID
	response = api.Get(path+"/watchers", "Authorization: Bearer good")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"watchers":[{"player_id":"44444444-4444-4444-8444-444444444444","display_name":"Robin","joined_at":"1970-01-01T00:00:00Z"}]`) {
		t.Fatalf("watchers = %d %s", response.Code, response.Body.String())
	}
	response = api.Delete(path+"/watchers/44444444-4444-4444-8444-444444444444", "Authorization: Bearer good")
	if response.Code != http.StatusNoContent || application.kickedWatcher != "44444444-4444-4444-8444-444444444444" {
		t.Fatalf("kick = %d %s", response.Code, response.Body.String())
	}
	if response = api.Delete(path+"/watch", "Authorization: Bearer good"); response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("unwatch = %d %q", response.Code, response.Body.String())
	}
}

func TestProblemStatusAndCodeMapping(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		errorValue error
		status     int
		code       string
	}{
		{domain.NewError(domain.CodeInvalidGameSettings, "rounds must be positive"), http.StatusUnprocessableEntity, "invalid_game_settings"}, {domain.ErrNotFound, http.StatusNotFound, "not_found"}, {domain.ErrNotHost, http.StatusForbidden, "not_host"}, {domain.ErrLobbyFull, http.StatusConflict, "lobby_full"}, {domain.ErrInvalidInput, http.StatusUnprocessableEntity, "invalid_input"}, {domain.ErrUnavailable, http.StatusServiceUnavailable, "unavailable"}, {errors.New("SQL password=secret"), http.StatusInternalServerError, "internal_error"},
	} {
		problem := problemFor(testCase.errorValue).(*Problem)
		if problem.Status != testCase.status || string(problem.Code) != testCase.code {
			t.Fatalf("problemFor(%v) = %#v", testCase.errorValue, problem)
		}
		if strings.Contains(problem.Detail, "secret") || problem.TeamIDs != nil {
			t.Fatalf("problem leaked internal error: %#v", problem)
		}
	}
}

func TestEmptyTeamsProblemNamesTheTeams(t *testing.T) {
	t.Parallel()
	err := domain.StartRules{EveryTeamNeedsMember: true}.Check([]domain.Team{{ID: "b", Name: "Blue", Order: 2}, {ID: "r", Name: "Red", Order: 1}}, domain.StartRoster{})
	problem := problemFor(err).(*Problem)
	encoded, _ := json.Marshal(problem)
	if problem.Status != http.StatusConflict || problem.Code != domain.CodeEmptyTeams || !strings.Contains(string(encoded), `"team_ids":["r","b"]`) || problem.Detail != "every team needs a player before the lobby can start; empty: Red, Blue" {
		t.Fatalf("empty teams problem = %s", encoded)
	}
	if err := (domain.StartRules{}).Check([]domain.Team{{ID: "r"}}, domain.StartRoster{}); err != nil {
		t.Fatalf("zero rules refused a start: %v", err)
	}
}

func TestGameRequirementProblemCarriesTheGamesType(t *testing.T) {
	t.Parallel()
	problem := problemFor(domain.GameRequirement("urn:example:problem:choice-missing", "Make your choice before getting ready.")).(*Problem)
	encoded, _ := json.Marshal(problem)
	if problem.Status != http.StatusConflict || problem.Code != domain.CodeGameRequirement || problem.Detail != "Make your choice before getting ready." ||
		!strings.Contains(string(encoded), `"type":"urn:example:problem:choice-missing"`) {
		t.Fatalf("game requirement problem = %s", encoded)
	}
	if encoded, _ := json.Marshal(problemFor(domain.ErrNotReady)); strings.Contains(string(encoded), `"type"`) {
		t.Fatalf("a lobby problem has a type: %s", encoded)
	}
}

func TestSnapshotDoesNotExposeInternalFieldsAndDeleteResponsesAreEmpty(t *testing.T) {
	t.Parallel()
	application := &fakeApplication{}
	_, api := newTestAPIWithApp(t, application)
	response := api.Get("/lobby/v1/games/island-battles/lobbies/"+testUUID, "Authorization: Bearer good")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"join_code":"SECRETCODE"`) || strings.Contains(response.Body.String(), "next_join_sequence") {
		t.Fatalf("snapshot leaked internals: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"teams":[]`) || !strings.Contains(response.Body.String(), `"version":9007199254740993`) || !strings.Contains(response.Body.String(), `"game_settings":{}`) {
		t.Fatalf("snapshot does not preserve arrays/int64: %s", response.Body.String())
	}
	for _, path := range []string{"/lobby/v1/games/island-battles/lobbies/" + testUUID + "/membership", "/lobby/v1/games/island-battles/lobbies/" + testUUID + "/leases/" + testUUID + "?generation=1"} {
		response = api.Delete(path, "Authorization: Bearer good")
		if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
			t.Fatalf("delete %s = %d %q", path, response.Code, response.Body.String())
		}
	}
}

// Reopening returns the snapshot with its round, and a running game is a
// conflict with its own code.
func TestReopenRoute(t *testing.T) {
	t.Parallel()
	application := &fakeApplication{}
	_, api := newTestAPIWithApp(t, application)
	path := "/lobby/v1/games/quiz-demo/lobbies/" + testUUID + "/reopen"
	response := api.Post(path, "Authorization: Bearer good")
	if response.Code != http.StatusOK || application.reopenScope != (domain.Scope{GameKey: "quiz-demo", LobbyID: testUUID}) {
		t.Fatalf("reopen = %d %s; scope %#v", response.Code, response.Body.String(), application.reopenScope)
	}
	if !strings.Contains(response.Body.String(), `"round":2`) {
		t.Fatalf("reopen snapshot has no round: %s", response.Body.String())
	}
	application.reopenError = domain.ErrGameRunning
	response = api.Post(path, "Authorization: Bearer good")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"game_running"`) {
		t.Fatalf("reopen of a running game = %d %s", response.Code, response.Body.String())
	}
}

func TestRegistrationIsOfflineAndInvalidationSchemaRegistered(t *testing.T) {
	t.Parallel()
	_, api := newTestAPI(t)
	schema := api.OpenAPI().Components.Schemas.Map()["LobbyInvalidation"]
	if schema == nil || len(schema.Properties) != 6 || schema.Properties["data"] != nil {
		t.Fatalf("invalidation schema = %#v", schema)
	}
	if got := schema.Properties["schema_version"].Enum; len(got) != 1 || fmt.Sprint(got[0]) != "1" {
		t.Fatalf("schema_version enum = %#v", got)
	}
	if got := schema.Properties["type"].Enum; len(got) != 1 || got[0] != "lobby.changed" {
		t.Fatalf("type enum = %#v", got)
	}
	if schema.Properties["version"].Format != "int64" || schema.Properties["version"].Minimum == nil {
		t.Fatalf("version schema = %#v", schema.Properties["version"])
	}
	problem := api.OpenAPI().Components.Schemas.Map()["Problem"]
	if problem == nil || len(problem.Properties["code"].Enum) == 0 {
		t.Fatalf("problem schema = %#v", problem)
	}
	if _, err := json.Marshal(api.OpenAPI()); err != nil {
		t.Fatalf("OpenAPI MarshalJSON error = %v", err)
	}
}

func newTestAPI(t *testing.T) (http.Handler, humatest.TestAPI) {
	return newTestAPIWithApp(t, &fakeApplication{})
}
func newTestAPIWithApp(t *testing.T, application *fakeApplication) (http.Handler, humatest.TestAPI) {
	handler, api := humatest.New(t, huma.DefaultConfig("Lobby", "1.0.0"))
	Register(api, application, fakeAuthenticator{}, fakeIssuer{}, nil)
	return handler, api
}
func strconvStatus(status int) string { return fmt.Sprintf("%d", status) }

type fakeAuthenticator struct{}

func (fakeAuthenticator) CreateGuest(context.Context, domain.CreateGuestRequest) (domain.GuestSession, error) {
	return domain.GuestSession{PlayerID: testUUID, AccessToken: "guest", ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
}
func (fakeAuthenticator) Authenticate(_ context.Context, header string) (domain.SessionVerification, error) {
	if header != "Bearer good" {
		return domain.SessionVerification{}, domain.ErrUnauthenticated
	}
	return domain.SessionVerification{Principal: domain.Principal{PlayerID: testUUID, SessionID: testUUID, DisplayName: "Bearer Owner"}, ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
}

type fakeIssuer struct{}

func (fakeIssuer) IssueConnectionToken(_ context.Context, verification domain.SessionVerification) (domain.ConnectionToken, error) {
	return domain.ConnectionToken{Token: verification.Principal.PlayerID, ExpiresAt: verification.ExpiresAt, WebsocketURL: "ws://localhost:8000/connection/websocket"}, nil
}

type fakeApplication struct {
	teamRequest     domain.SelectTeamRequest
	readyRequest    domain.ReadyRequest
	createPrincipal domain.Principal
	createRequest   domain.CreateRequest
	createError     error
	seatRequest     domain.AddSeatRequest
	settingsRequest domain.ChangeSettingsRequest
	settingsScope   domain.Scope
	settingsError   error
	reopenScope     domain.Scope
	reopenError     error
	seatError       error
	removedSeat     string
	watchRequest    domain.WatchRequest
	watchError      error
	kickedWatcher   string
}

func fakeSnapshot() domain.Snapshot {
	return domain.Snapshot{Lobby: domain.Lobby{ID: testUUID, GameKey: "island-battles", Name: "Lobby", JoinCode: "SECRETCODE", Capacity: 2, Phase: domain.PhaseWaiting, Version: 9007199254740993, CreatedAt: time.Unix(0, 0).UTC()}, Teams: []domain.Team{}, Members: []domain.Member{}, StartRoster: domain.StartRoster{}, MembershipGeneration: 1, Channel: "lobby:island-battles:" + testUUID}
}
func (application *fakeApplication) Create(_ context.Context, principal domain.Principal, request domain.CreateRequest) (domain.Snapshot, error) {
	application.createPrincipal = principal
	application.createRequest = request
	if application.createError != nil {
		return domain.Snapshot{}, application.createError
	}
	snapshot := fakeSnapshot()
	snapshot.Lobby.GameSettings = request.GameSettings
	return snapshot, nil
}
func (*fakeApplication) Join(context.Context, domain.Principal, domain.JoinRequest) (domain.Snapshot, error) {
	return fakeSnapshot(), nil
}
func (*fakeApplication) Snapshot(context.Context, domain.Principal, domain.Scope) (domain.Snapshot, error) {
	return fakeSnapshot(), nil
}
func (application *fakeApplication) SelectTeam(_ context.Context, _ domain.Principal, _ domain.Scope, request domain.SelectTeamRequest) (domain.Snapshot, error) {
	application.teamRequest = request
	return fakeSnapshot(), nil
}
func (application *fakeApplication) SetReady(_ context.Context, _ domain.Principal, _ domain.Scope, request domain.ReadyRequest) (domain.Snapshot, error) {
	application.readyRequest = request
	return fakeSnapshot(), nil
}
func (*fakeApplication) Start(context.Context, domain.Principal, domain.Scope) (domain.Snapshot, error) {
	return fakeSnapshot(), nil
}
func (application *fakeApplication) Reopen(_ context.Context, _ domain.Principal, scope domain.Scope) (domain.Snapshot, error) {
	application.reopenScope = scope
	if application.reopenError != nil {
		return domain.Snapshot{}, application.reopenError
	}
	snapshot := fakeSnapshot()
	snapshot.Lobby.Round = 2
	return snapshot, nil
}
func (application *fakeApplication) ChangeSettings(_ context.Context, _ domain.Principal, scope domain.Scope, request domain.ChangeSettingsRequest) (domain.Snapshot, error) {
	application.settingsRequest = request
	application.settingsScope = scope
	if application.settingsError != nil {
		return domain.Snapshot{}, application.settingsError
	}
	snapshot := fakeSnapshot()
	snapshot.Lobby.GameSettings = request.GameSettings
	return snapshot, nil
}
func (application *fakeApplication) AddSeat(_ context.Context, _ domain.Principal, _ domain.Scope, request domain.AddSeatRequest) (domain.Snapshot, error) {
	application.seatRequest = request
	if application.seatError != nil {
		return domain.Snapshot{}, application.seatError
	}
	snapshot := fakeSnapshot()
	teamID := request.TeamID
	snapshot.Members = []domain.Member{
		{ID: testUUID, PlayerID: testUUID, DisplayName: "Host", Generation: 1, Active: true},
		{ID: "33333333-3333-4333-8333-333333333333", PlayerID: "44444444-4444-4444-8444-444444444444", DisplayName: "Bot (level 2)", Generation: 1, Active: true, TeamID: &teamID, Ready: true, Kind: domain.MemberSeat, SeatSettings: request.SeatSettings},
	}
	return snapshot, nil
}
func (application *fakeApplication) RemoveSeat(_ context.Context, _ domain.Principal, _ domain.Scope, seatID string) (domain.Snapshot, error) {
	application.removedSeat = seatID
	return fakeSnapshot(), nil
}
func (*fakeApplication) Leave(context.Context, domain.Principal, domain.Scope) error { return nil }
func (application *fakeApplication) Watch(_ context.Context, _ domain.Principal, request domain.WatchRequest) (domain.WatchAccess, error) {
	application.watchRequest = request
	if application.watchError != nil {
		return domain.WatchAccess{}, application.watchError
	}
	return domain.WatchAccess{Scope: domain.Scope{GameKey: request.GameKey, LobbyID: testUUID}, Channel: "watch:" + request.GameKey + ":" + testUUID}, nil
}
func (*fakeApplication) Unwatch(context.Context, domain.Principal, domain.Scope) error { return nil }
func (*fakeApplication) Watchers(context.Context, domain.Principal, domain.Scope) ([]domain.Watcher, error) {
	return []domain.Watcher{{PlayerID: "44444444-4444-4444-8444-444444444444", DisplayName: "Robin", JoinedAt: time.Unix(0, 0).UTC()}}, nil
}
func (application *fakeApplication) KickWatcher(_ context.Context, _ domain.Principal, _ domain.Scope, playerID string) error {
	application.kickedWatcher = playerID
	return nil
}
func (*fakeApplication) Heartbeat(context.Context, domain.Principal, domain.Scope, domain.HeartbeatRequest) (domain.HeartbeatResult, error) {
	return domain.HeartbeatResult{}, nil
}
func (*fakeApplication) ReleaseLease(context.Context, domain.Principal, domain.Scope, domain.ReleaseLeaseRequest) error {
	return nil
}
