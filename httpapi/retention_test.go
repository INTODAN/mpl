package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
)

// countingRetention counts the passes it was asked for, and those that came
// after the request's own work.
type countingRetention struct {
	application *fakeApplication
	runs        int
	afterWork   int
}

func (retention *countingRetention) RunIfDue(context.Context) {
	retention.runs++
	if retention.application.readyRequest.Ready {
		retention.afterWork++
	}
}

func TestLobbyRequestsEndWithARetentionPass(t *testing.T) {
	application := &fakeApplication{}
	retention := &countingRetention{application: application}
	_, api := humatest.New(t, huma.DefaultConfig("Lobby", "1.0.0"))
	Register(api, application, fakeAuthenticator{}, fakeIssuer{}, retention)

	path := "/lobby/v1/games/island-battles/lobbies/" + testUUID
	if response := api.Put(path+"/ready", "Authorization: Bearer good", map[string]any{"ready": true}); response.Code != http.StatusOK {
		t.Fatalf("ready status = %d: %s", response.Code, response.Body.String())
	}
	if retention.runs != 1 || retention.afterWork != 1 {
		t.Fatalf("runs = %d, after the request's work = %d; want one pass after it", retention.runs, retention.afterWork)
	}
	// Failed and unauthenticated requests end with a pass too.
	if response := api.Get(path); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	if response := api.Post("/lobby/v1/sessions", map[string]string{"display_name": "Guest"}); response.Code != http.StatusCreated {
		t.Fatalf("session status = %d", response.Code)
	}
	if retention.runs != 3 {
		t.Fatalf("runs = %d, want 3", retention.runs)
	}
}
