package gamehooks_test

import (
	"strings"
	"testing"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/frontend"
	"islandBattles/lobby/gamehooks"
)

func environment(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
}

func TestLobbyGamesConfiguresEachGame(t *testing.T) {
	lookup := environment(map[string]string{
		gamehooks.GamesEnv: `[
			{"key": "example-game", "name": "Example Game", "hooks_url": "https://game.example.com/game/lobby-hooks",
			 "secret_env": "LOBBY_GAME_SECRET_EXAMPLE_GAME", "start_rules": {"every_team_needs_member": true},
			 "retention": {"idle_period": "2h", "maximum_age": "48h"},
			 "frontend": {"result_url": "/game/{lobby_id}/result", "room_module": "/game/room.mjs"}},
			{"key": "other-game", "name": "Other", "hooks_url": "http://127.0.0.1:9000/hooks", "secret_env": "OTHER_SECRET"}
		]`,
		"LOBBY_GAME_SECRET_EXAMPLE_GAME": string(secret),
		"OTHER_SECRET":                   strings.Repeat("o", 40),
	})
	games, err := gamehooks.LoadGames(lookup)
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 2 {
		t.Fatalf("games = %+v", games)
	}
	example := games[0]
	registration := example.Registration
	if registration.Key != "example-game" || !registration.StartRules.EveryTeamNeedsMember || registration.Retention != (domain.LobbyRetention{IdlePeriod: 2 * time.Hour, MaximumAge: 48 * time.Hour}) {
		t.Fatalf("registration = %+v", registration)
	}
	if registration.ValidateSettings == nil || registration.ValidateSeat == nil || registration.ValidateMemberSettings == nil || registration.ReadyGate == nil || registration.StartGame == nil || registration.GameOver == nil {
		t.Fatal("a hook is missing from the registration")
	}
	if example.Frontend.Key != "example-game" || example.Frontend.Name != "Example Game" || example.Frontend.ResultURL != "/game/{lobby_id}/result" || example.Frontend.RoomModule != "/game/room.mjs" {
		t.Fatalf("frontend = %+v", example.Frontend)
	}
	if _, err := frontend.Handler([]frontend.Game{example.Frontend}); err != nil {
		t.Fatalf("the catalog entry is invalid: %v", err)
	}
	if games[1].Registration.Retention != (domain.LobbyRetention{}) || games[1].Registration.StartRules.EveryTeamNeedsMember {
		t.Fatalf("defaults = %+v", games[1].Registration)
	}
	if err := domain.DefaultPolicy(gamehooks.Registrations(games)...).Validate(); err != nil {
		t.Fatalf("policy: %v", err)
	}
	secrets := gamehooks.Secrets(games)
	if found, ok := secrets("example-game"); !ok || string(found) != string(secret) {
		t.Fatal("example-game's secret is missing")
	}
	if _, ok := secrets("unknown"); ok {
		t.Fatal("an unknown game has a secret")
	}
	if games, err := gamehooks.LoadGames(environment(nil)); err != nil || len(games) != 0 {
		t.Fatalf("without LOBBY_GAMES = %+v, %v", games, err)
	}
}

func TestLobbyGamesRefusesInvalidConfiguration(t *testing.T) {
	valid := `{"key": "example-game", "name": "Example", "hooks_url": "https://game.example.com/hooks", "secret_env": "SECRET"}`
	for name, raw := range map[string]string{
		"not JSON":         `[`,
		"an object":        valid,
		"two values":       `[` + valid + `] []`,
		"unknown field":    `[{"key": "example-game", "name": "Example", "hooks_url": "https://game.example.com/hooks", "secret_env": "SECRET", "secret": "x"}]`,
		"unknown frontend": `[{"key": "example-game", "name": "Example", "hooks_url": "https://game.example.com/hooks", "secret_env": "SECRET", "frontend": {"colour": "red"}}]`,
		"invalid key":      `[{"key": "Example Game", "name": "Example", "hooks_url": "https://game.example.com/hooks", "secret_env": "SECRET"}]`,
		"no name":          `[{"key": "example-game", "hooks_url": "https://game.example.com/hooks", "secret_env": "SECRET"}]`,
		"no secret_env":    `[{"key": "example-game", "name": "Example", "hooks_url": "https://game.example.com/hooks"}]`,
		"unset secret":     `[{"key": "example-game", "name": "Example", "hooks_url": "https://game.example.com/hooks", "secret_env": "UNSET"}]`,
		"short secret":     `[{"key": "example-game", "name": "Example", "hooks_url": "https://game.example.com/hooks", "secret_env": "SHORT"}]`,
		"plain http":       `[{"key": "example-game", "name": "Example", "hooks_url": "http://game.example.com/hooks", "secret_env": "SECRET"}]`,
		"bad retention":    `[{"key": "example-game", "name": "Example", "hooks_url": "https://game.example.com/hooks", "secret_env": "SECRET", "retention": {"idle_period": "soon"}}]`,
		"other name":       `[{"key": "example-game", "name": "Example", "hooks_url": "https://game.example.com/hooks", "secret_env": "SECRET", "frontend": {"name": "Other"}}]`,
		"duplicate key":    `[` + valid + `,` + valid + `]`,
	} {
		lookup := environment(map[string]string{"SECRET": string(secret), "SHORT": "short"})
		if games, err := gamehooks.ParseGames(raw, lookup); err == nil {
			t.Errorf("%s: accepted %+v", name, games)
		}
	}
}
