package app_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"islandBattles/lobby/app"
	"islandBattles/lobby/domain"
	"islandBattles/lobby/playertoken"
)

const testGames = `[{"key": "mock-game", "name": "Mock game", "hooks_url": "http://127.0.0.1:9/hooks", "secret_env": "LOBBY_GAME_SECRET_MOCK_GAME"}]`

var testGameSecret = strings.Repeat("g", 32)

func testEnvironment() map[string]string {
	return map[string]string{
		app.DatabaseURLEnv:            "postgres://lobby:lobby@127.0.0.1:5432/lobby?sslmode=disable",
		app.PublicURLEnv:              "http://localhost:8890/",
		app.CentrifugoAPIURLEnv:       "http://127.0.0.1:8000/api",
		app.CentrifugoAPIKeyEnv:       "api-key",
		app.CentrifugoPublicURLEnv:    "ws://127.0.0.1:8000/connection/websocket",
		app.ConnectionTokenSecretEnv:  strings.Repeat("c", 32),
		app.ProxySecretEnv:            "proxy-secret",
		app.PlayerTokenKeysEnv:        "test:" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("k", 32))),
		app.GamesEnv:                  testGames,
		"LOBBY_GAME_SECRET_MOCK_GAME": testGameSecret,
	}
}

func load(environment map[string]string) (app.Config, error) {
	return app.LoadConfigFromLookup(func(name string) (string, bool) {
		value, found := environment[name]
		return value, found
	})
}

func TestTheLobbyReadsItsOwnEnvironment(t *testing.T) {
	config, err := load(testEnvironment())
	if err != nil {
		t.Fatal(err)
	}
	if config.PublicURL != "http://localhost:8890" || config.Database.URL != testEnvironment()[app.DatabaseURLEnv] ||
		config.CentrifugoAPIURL != "http://127.0.0.1:8000/api" || config.CentrifugoAPIKey != "api-key" ||
		config.CentrifugoPublicURL != "ws://127.0.0.1:8000/connection/websocket" || config.ConnectionTokenSecret != strings.Repeat("c", 32) ||
		config.ProxySecret != "proxy-secret" || config.PlayerTokenKeys == nil || config.Games != testGames {
		t.Fatalf("config = %+v", config)
	}
	if secret, found := config.LookupGameSecret("LOBBY_GAME_SECRET_MOCK_GAME"); !found || secret != testGameSecret {
		t.Fatalf("the game's secret was not read: %q %v", secret, found)
	}
	// The defaults: Lambda's pool, the longest token, and the default
	// retention.
	if config.Database.MaxOpenConnections != 2 || config.Database.MaxIdleConnections != 2 || config.Database.ConnectionMaxIdleTime != time.Minute ||
		config.PlayerTokenLifetime != playertoken.MaxLifetime || config.Retention != (domain.RetentionPolicy{}) {
		t.Fatalf("defaults = %+v", config)
	}
}

func TestEveryMissingSettingIsNamedWithoutValues(t *testing.T) {
	_, err := load(map[string]string{app.ProxySecretEnv: "proxy-secret"})
	if err == nil {
		t.Fatal("an empty environment loaded")
	}
	for _, name := range []string{app.DatabaseURLEnv, app.PublicURLEnv, app.CentrifugoAPIURLEnv, app.CentrifugoAPIKeyEnv, app.CentrifugoPublicURLEnv,
		app.ConnectionTokenSecretEnv, app.PlayerTokenKeysEnv, app.GamesEnv} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%v does not name %s", err, name)
		}
	}
	if strings.Contains(err.Error(), app.ProxySecretEnv) || strings.Contains(err.Error(), "proxy-secret") {
		t.Errorf("%v names a variable that is set", err)
	}
}

func TestOptionalSettingsOverrideTheDefaults(t *testing.T) {
	environment := testEnvironment()
	for name, value := range map[string]string{
		app.DatabaseMaxOpenConnectionsEnv:    "10",
		app.DatabaseMaxIdleConnectionsEnv:    "4",
		app.DatabaseConnectionMaxIdleTimeEnv: "30s",
		app.PlayerTokenLifetimeEnv:           "2m",
		app.RetentionIdlePeriodEnv:           "3h",
		app.RetentionMaximumAgeEnv:           "48h",
		app.RetentionCreateRequestsEnv:       "12h",
		app.RetentionExpiredSessionsEnv:      "2h",
		app.RetentionBatchSizeEnv:            "100",
		app.RetentionPassIntervalEnv:         "30s",
		app.AllowedOriginsEnv:                " https://game.example.org, http://localhost:3000 ",
	} {
		environment[name] = value
	}
	config, err := load(environment)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.RetentionPolicy{CreateRequests: 12 * time.Hour, ExpiredSessions: 2 * time.Hour,
		Lobbies: domain.LobbyRetention{IdlePeriod: 3 * time.Hour, MaximumAge: 48 * time.Hour}, BatchSize: 100, PassInterval: 30 * time.Second}
	if config.Database.MaxOpenConnections != 10 || config.Database.MaxIdleConnections != 4 || config.Database.ConnectionMaxIdleTime != 30*time.Second ||
		config.PlayerTokenLifetime != 2*time.Minute || config.Retention != want ||
		strings.Join(config.AllowedOrigins, " ") != "https://game.example.org http://localhost:3000" {
		t.Fatalf("config = %+v", config)
	}
}

func TestInvalidSettingsAreRefused(t *testing.T) {
	for name, values := range map[string][]string{
		app.PublicURLEnv:                  {"lobby.example.org", "https://lobby.example.org/lobby", "ftp://lobby.example.org"},
		app.DatabaseURLEnv:                {"mysql://lobby@localhost/lobby"},
		app.CentrifugoAPIURLEnv:           {"ws://127.0.0.1:8000/api"},
		app.CentrifugoPublicURLEnv:        {"http://127.0.0.1:8000/connection/websocket"},
		app.PlayerTokenKeysEnv:            {"no-seed"},
		app.PlayerTokenLifetimeEnv:        {"30s", "16m", "soon"},
		app.GamesEnv:                      {"[]", "{}", `[{"key": "mock-game"}]`},
		app.DatabaseMaxOpenConnectionsEnv: {"0", "many"},
		app.DatabaseMaxIdleConnectionsEnv: {"3"},
		app.RetentionIdlePeriodEnv:        {"-1h", "1m"},
		app.RetentionBatchSizeEnv:         {"0", "1001"},
		"LOBBY_GAME_SECRET_MOCK_GAME":     {"short"},
		app.AllowedOriginsEnv: {"*", "game.example.org", "https://game.example.org/", "https://game.example.org/play", "https://Game.example.org",
			"ftp://game.example.org", "https://game.example.org:443", "https://game.example.org, null"},
	} {
		for _, value := range values {
			environment := testEnvironment()
			environment[name] = value
			if _, err := load(environment); err == nil {
				t.Errorf("%s=%q loaded", name, value)
			} else if strings.Contains(err.Error(), "k"+strings.Repeat("k", 31)) {
				t.Errorf("%v names a secret", err)
			}
		}
	}
}

func TestTheLocalServerListensOn8890ByDefault(t *testing.T) {
	address, err := app.LoadHTTPAddr(func(string) (string, bool) { return "", false })
	if err != nil || address != ":8890" {
		t.Fatalf("address = %q, error = %v", address, err)
	}
	address, err = app.LoadHTTPAddr(func(string) (string, bool) { return "127.0.0.1:9000", true })
	if err != nil || address != "127.0.0.1:9000" {
		t.Fatalf("address = %q, error = %v", address, err)
	}
	if _, err := app.LoadHTTPAddr(func(string) (string, bool) { return "9000", true }); err == nil {
		t.Fatal("an address without a host:port loaded")
	}
}
