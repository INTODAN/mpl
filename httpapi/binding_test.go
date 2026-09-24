package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"islandBattles/lobby/application"
	"islandBattles/lobby/domain"
)

type gameBindingReader struct{ err error }

func (reader *gameBindingReader) Snapshot(_ context.Context, principal domain.Principal, scope domain.Scope) (domain.Snapshot, error) {
	return domain.Snapshot{
		Lobby:       domain.Lobby{ID: scope.LobbyID, GameKey: scope.GameKey, HostPlayerID: &principal.PlayerID, Phase: domain.PhaseStarted},
		Teams:       []domain.Team{{ID: testUUID, Order: 1}},
		StartRoster: domain.StartRoster{{TeamID: testUUID, TeamOrder: 1}},
	}, reader.err
}

type quizBindingResult struct {
	MatchID string `json:"match_id"`
}

func TestGameBindingHTTPUsesSharedAuthenticationAndErrors(t *testing.T) {
	_, api := humatest.New(t)
	reader := &gameBindingReader{}
	var persistenceError error
	calls := 0
	binding, err := application.NewGameBinding("quiz-demo", reader, func(context.Context, domain.Snapshot) (quizBindingResult, error) {
		calls++
		return quizBindingResult{MatchID: "quiz-match"}, persistenceError
	})
	if err != nil {
		t.Fatal(err)
	}
	RegisterGameBinding(api, huma.Operation{OperationID: "bindQuiz", Path: "/quiz/from-lobby"}, binding, fakeAuthenticator{})
	body := map[string]string{"game_key": "quiz-demo", "lobby_id": testUUID}
	response := api.Post("/quiz/from-lobby", body)
	if response.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("anonymous=%d calls=%d", response.Code, calls)
	}
	response = api.Post("/quiz/from-lobby", "Authorization: Bearer good", body)
	if response.Code != http.StatusOK || response.Body.String() != "{\"match_id\":\"quiz-match\"}\n" {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
	for _, test := range []struct {
		err    error
		status int
		code   domain.ErrorCode
	}{
		{domain.ErrNotFound, 404, domain.CodeNotFound},
		{domain.ErrNotHost, 403, domain.CodeNotHost},
		{domain.ErrPhaseConflict, 409, domain.CodePhaseConflict},
		{domain.ErrUnavailable, 503, domain.CodeUnavailable},
		{errors.New("private database details"), 500, "internal_error"},
	} {
		reader.err = test.err
		response = api.Post("/quiz/from-lobby", "Authorization: Bearer good", body)
		var problem Problem
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if response.Code != test.status || problem.Code != test.code || response.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("error response=%d %#v", response.Code, problem)
		}
		if test.status == 500 && problem.Detail != "internal server error" {
			t.Fatal("leaked private error")
		}
	}
	if calls != 1 {
		t.Fatalf("rejected requests reached persistence: %d", calls)
	}
	reader.err = nil
	persistenceError = domain.ErrUnavailable
	response = api.Post("/quiz/from-lobby", "Authorization: Bearer good", body)
	if response.Code != 503 {
		t.Fatalf("persistence error status=%d", response.Code)
	}
	if api.OpenAPI().Components.SecuritySchemes["LobbyBearer"] == nil {
		t.Fatal("missing standalone bearer scheme")
	}
	operation := api.OpenAPI().Paths["/quiz/from-lobby"].Post
	if operation.Responses["503"].Content["application/problem+json"] == nil {
		t.Fatal("missing problem schema")
	}
}

func TestGameBindingHTTPWithoutAuthenticator(t *testing.T) {
	_, api := humatest.New(t)
	binding, err := application.NewGameBinding("quiz-demo", &gameBindingReader{}, func(context.Context, domain.Snapshot) (quizBindingResult, error) {
		t.Fatal("unauthenticated persistence")
		return quizBindingResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	RegisterGameBinding(api, huma.Operation{OperationID: "bindQuiz", Path: "/quiz/from-lobby"}, binding, nil)
	response := api.Post("/quiz/from-lobby", "Authorization: Bearer good", "invalid JSON")
	if response.Code != 401 {
		t.Fatalf("status=%d", response.Code)
	}
}
