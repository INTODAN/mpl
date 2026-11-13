package main

import (
	"encoding/base64"
	"strings"
	"testing"

	"islandBattles/lobby/app"
)

func environment(extra map[string]string) func(string) (string, bool) {
	values := map[string]string{
		app.DatabaseURLEnv:            "postgres://lobby:lobby@127.0.0.1:5432/lobby?sslmode=disable",
		app.PublicURLEnv:              "http://localhost:8890",
		app.CentrifugoAPIURLEnv:       "http://127.0.0.1:8000/api",
		app.CentrifugoAPIKeyEnv:       "api-key",
		app.CentrifugoPublicURLEnv:    "ws://127.0.0.1:8000/connection/websocket",
		app.ConnectionTokenSecretEnv:  strings.Repeat("c", 32),
		app.ProxySecretEnv:            "proxy-secret",
		app.PlayerTokenKeysEnv:        "test:" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("k", 32))),
		app.GamesEnv:                  `[{"key": "mock-game", "name": "Mock game", "hooks_url": "http://127.0.0.1:9/hooks", "secret_env": "LOBBY_GAME_SECRET_MOCK_GAME"}]`,
		"LOBBY_GAME_SECRET_MOCK_GAME": strings.Repeat("g", 32),
	}
	for name, value := range extra {
		values[name] = value
	}
	return func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
}

// The local server listens on :8890 with a pool wider than Lambda's, unless
// its environment says otherwise.
func TestTheLocalServerDefaults(t *testing.T) {
	config, address, err := load(environment(nil))
	if err != nil {
		t.Fatal(err)
	}
	if address != ":8890" || config.Database.MaxOpenConnections != LocalMaxOpenConnections {
		t.Fatalf("address = %q, pool = %d", address, config.Database.MaxOpenConnections)
	}
	config, address, err = load(environment(map[string]string{app.HTTPAddrEnv: "127.0.0.1:9890", app.DatabaseMaxOpenConnectionsEnv: "3"}))
	if err != nil {
		t.Fatal(err)
	}
	if address != "127.0.0.1:9890" || config.Database.MaxOpenConnections != 3 {
		t.Fatalf("address = %q, pool = %d", address, config.Database.MaxOpenConnections)
	}
	if _, _, err := load(environment(map[string]string{app.PublicURLEnv: ""})); err == nil || !strings.Contains(err.Error(), app.PublicURLEnv) {
		t.Fatalf("load() error = %v", err)
	}
}
