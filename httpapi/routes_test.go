package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/membership":                  {http.MethodDelete, "leaveLobby", http.StatusNoContent},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/heartbeat":                   {http.MethodPost, "heartbeatLobby", http.StatusOK},
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/leases/{client_instance_id}": {http.MethodDelete, "releaseLobbyLease", http.StatusNoContent},
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
		"request_id": testUUID, "game_key": "island-battles", "name": "Lobby", "capacity": 2,
		"teams":              []map[string]any{{"name": "One", "capacity": 1}, {"name": "Two", "capacity": 1}},
		"client_instance_id": testUUID, "player_id": "22222222-2222-4222-8222-222222222222",
	})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("claimed player status = %d: %s", response.Code, response.Body.String())
	}
	if application.createPrincipal.PlayerID != "" {
		t.Fatalf("claimed player reached service: %#v", application.createPrincipal)
	}
}

func TestProblemStatusAndCodeMapping(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		errorValue error
		status     int
		code       string
	}{
		{domain.ErrNotFound, http.StatusNotFound, "not_found"}, {domain.ErrNotHost, http.StatusForbidden, "not_host"}, {domain.ErrLobbyFull, http.StatusConflict, "lobby_full"}, {domain.ErrInvalidInput, http.StatusUnprocessableEntity, "invalid_input"}, {domain.ErrUnavailable, http.StatusServiceUnavailable, "unavailable"}, {errors.New("SQL password=secret"), http.StatusInternalServerError, "internal_error"},
	} {
		problem := problemFor(testCase.errorValue).(*Problem)
		if problem.Status != testCase.status || string(problem.Code) != testCase.code {
			t.Fatalf("problemFor(%v) = %#v", testCase.errorValue, problem)
		}
		if strings.Contains(problem.Detail, "secret") {
			t.Fatalf("problem leaked internal error: %#v", problem)
		}
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
	if !strings.Contains(response.Body.String(), `"teams":[]`) || !strings.Contains(response.Body.String(), `"version":9007199254740993`) {
		t.Fatalf("snapshot does not preserve arrays/int64: %s", response.Body.String())
	}
	for _, path := range []string{"/lobby/v1/games/island-battles/lobbies/" + testUUID + "/membership", "/lobby/v1/games/island-battles/lobbies/" + testUUID + "/leases/" + testUUID + "?generation=1"} {
		response = api.Delete(path, "Authorization: Bearer good")
		if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
			t.Fatalf("delete %s = %d %q", path, response.Code, response.Body.String())
		}
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
	Register(api, application, fakeAuthenticator{}, fakeIssuer{})
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
}

func fakeSnapshot() domain.Snapshot {
	return domain.Snapshot{Lobby: domain.Lobby{ID: testUUID, GameKey: "island-battles", Name: "Lobby", JoinCode: "SECRETCODE", Capacity: 2, Phase: domain.PhaseWaiting, Version: 9007199254740993, CreatedAt: time.Unix(0, 0).UTC()}, Teams: []domain.Team{}, Members: []domain.Member{}, StartRoster: domain.StartRoster{}, MembershipGeneration: 1, Channel: "lobby:island-battles:" + testUUID}
}
func (application *fakeApplication) Create(_ context.Context, principal domain.Principal, _ domain.CreateRequest) (domain.Snapshot, error) {
	application.createPrincipal = principal
	return fakeSnapshot(), nil
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
func (*fakeApplication) Leave(context.Context, domain.Principal, domain.Scope) error { return nil }
func (*fakeApplication) Heartbeat(context.Context, domain.Principal, domain.Scope, domain.HeartbeatRequest) (domain.HeartbeatResult, error) {
	return domain.HeartbeatResult{}, nil
}
func (*fakeApplication) ReleaseLease(context.Context, domain.Principal, domain.Scope, domain.ReleaseLeaseRequest) error {
	return nil
}
