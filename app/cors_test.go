package app

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func corsRequest(handler http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// The player API answers the games' sites with CORS, so a game's own lobby
// UI, such as its web client, calls it from another origin.
func TestThePlayerAPIAllowsTheGamesOrigins(t *testing.T) {
	served := 0
	lobby := allowOrigins([]string{"https://game.example.org", "http://localhost:3000"}, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		served++
		response.WriteHeader(http.StatusTeapot)
	}))
	for _, path := range []string{"/lobby/v1/sessions", "/lobby/v1/player-tokens", "/lobby/v1/games/g/lobbies/l"} {
		response := corsRequest(lobby, http.MethodPost, path, map[string]string{"Origin": "https://game.example.org"})
		headers := response.Header()
		if response.Code != http.StatusTeapot || headers.Get("Access-Control-Allow-Origin") != "https://game.example.org" ||
			headers.Get("Access-Control-Expose-Headers") != "Retry-After" || !slices.Contains(headers.Values("Vary"), "Origin") {
			t.Fatalf("POST %s: status %d, headers %v", path, response.Code, headers)
		}
	}
	before := served
	response := corsRequest(lobby, http.MethodOptions, "/lobby/v1/connection-token", map[string]string{
		"Origin": "http://localhost:3000", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "authorization",
	})
	headers := response.Header()
	if response.Code != http.StatusNoContent || served != before || headers.Get("Access-Control-Allow-Origin") != "http://localhost:3000" ||
		headers.Get("Access-Control-Allow-Methods") != "GET, POST, PUT, DELETE" || headers.Get("Access-Control-Allow-Headers") != "Authorization, Content-Type" ||
		headers.Get("Access-Control-Max-Age") != "600" {
		t.Fatalf("preflight: status %d, headers %v", response.Code, headers)
	}
	// Other origins, and the paths that browsers of other sites have no
	// business with, get no CORS headers: the game API, which games call
	// from their servers, the Centrifugo proxy and the lobby page.
	for _, test := range []struct{ origin, path string }{
		{"https://evil.example.org", "/lobby/v1/sessions"}, {"https://game.example.org.evil.example", "/lobby/v1/sessions"},
		{"https://game.example.org", "/lobby/v1/game-api/games/g/lobbies/l"}, {"https://game.example.org", "/internal/lobby/centrifugo/subscribe"},
		{"https://game.example.org", "/lobby/ui/games.json"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodOptions} {
			response := corsRequest(lobby, method, test.path, map[string]string{"Origin": test.origin, "Access-Control-Request-Method": "GET"})
			if response.Code != http.StatusTeapot || response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("%s %s from %s: status %d, headers %v", method, test.path, test.origin, response.Code, response.Header())
			}
		}
	}
}

func TestWithoutAllowedOriginsNothingChanges(t *testing.T) {
	lobby := allowOrigins(nil, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusTeapot) }))
	response := corsRequest(lobby, http.MethodOptions, "/lobby/v1/sessions", map[string]string{"Origin": "https://game.example.org", "Access-Control-Request-Method": "POST"})
	if response.Code != http.StatusTeapot || len(response.Header()) != 0 {
		t.Fatalf("status %d, headers %v", response.Code, response.Header())
	}
}
