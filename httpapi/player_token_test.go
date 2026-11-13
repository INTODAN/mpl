package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"islandBattles/lobby/playertoken"
)

func TestAPlayerTokenIsIssuedForARegisteredGameAndVerifiesWithThePublishedKeys(t *testing.T) {
	t.Parallel()
	_, api := newTestAPI(t)
	response := api.Post("/lobby/v1/player-tokens", "Authorization: Bearer good", map[string]string{"game_key": "island-battles"})
	if response.Code != http.StatusOK {
		t.Fatalf("issue status = %d: %s", response.Code, response.Body.String())
	}
	var issued struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if lifetime := time.Until(issued.ExpiresAt); lifetime <= 14*time.Minute || lifetime > playertoken.MaxLifetime {
		t.Fatalf("expires_at = %v, %v from now", issued.ExpiresAt, lifetime)
	}

	keys := api.Get("/lobby/v1/jwks.json")
	if keys.Code != http.StatusOK || keys.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("JWKS status = %d, Cache-Control = %q", keys.Code, keys.Header().Get("Cache-Control"))
	}
	var set playertoken.JWKS
	if err := json.Unmarshal(keys.Body.Bytes(), &set); err != nil || len(set.Keys) != 1 || set.Keys[0].KeyID != "test" {
		t.Fatalf("JWKS = %s (%v)", keys.Body.String(), err)
	}
	// A game reads the keys over HTTP, as a JWKSSource does.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(keys.Body.Bytes()) }))
	defer server.Close()
	source, err := playertoken.NewJWKSSource(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := playertoken.NewVerifier("https://lobby.example.test", source)
	if err != nil {
		t.Fatal(err)
	}
	player, err := verifier.Verify(context.Background(), issued.Token, "island-battles")
	if err != nil || player != (playertoken.Player{ID: testUUID, Name: "Bearer Owner"}) {
		t.Fatalf("Verify() = %+v, %v", player, err)
	}
	if _, err := verifier.Verify(context.Background(), issued.Token, "quiz-demo"); !errors.Is(err, playertoken.ErrInvalid) {
		t.Fatalf("Verify() for another game = %v, want ErrInvalid", err)
	}
}

func TestAPlayerTokenNeedsASessionAndAKnownGame(t *testing.T) {
	t.Parallel()
	_, api := newTestAPI(t)
	for _, test := range []struct {
		headers []any
		body    any
		status  int
		code    string
	}{
		{nil, map[string]string{"game_key": "island-battles"}, http.StatusUnauthorized, "unauthenticated"},
		{[]any{"Authorization: Bearer stale"}, map[string]string{"game_key": "island-battles"}, http.StatusUnauthorized, "unauthenticated"},
		{[]any{"Authorization: Bearer good"}, map[string]string{"game_key": "quiz-demo"}, http.StatusNotFound, "not_found"},
		{[]any{"Authorization: Bearer good"}, map[string]string{"game_key": "Not A Key"}, http.StatusUnprocessableEntity, "invalid_input"},
		{[]any{"Authorization: Bearer good"}, map[string]string{}, http.StatusUnprocessableEntity, "invalid_input"},
	} {
		response := api.Post("/lobby/v1/player-tokens", append(test.headers, test.body)...)
		var problem Problem
		_ = json.Unmarshal(response.Body.Bytes(), &problem)
		if response.Code != test.status || string(problem.Code) != test.code {
			t.Fatalf("%v %v: status = %d, code = %q: %s", test.headers, test.body, response.Code, problem.Code, response.Body.String())
		}
	}
}
