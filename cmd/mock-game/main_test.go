package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/gamehooks"
	"islandBattles/lobby/mockgame"
)

var secret = strings.Repeat("s", 32)

func environment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// client calls the hooks of the mock game that environment configures.
func client(t *testing.T, values map[string]string) *gamehooks.Client {
	t.Helper()
	handler, err := newHandler(environment(values))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	hooks, err := gamehooks.NewClient("mock-game", server.URL+mockgame.HooksPath, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return hooks
}

func TestTheMockGameKeepsItsRulesByDefault(t *testing.T) {
	hooks := client(t, map[string]string{"MOCK_GAME_SECRET": secret})
	ctx := context.Background()
	if err := hooks.ValidateSettings(ctx, "", json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("the mock game took no rounds")
	}
	check, _ := hooks.ReadyGate(ctx, "")
	if err := check([]domain.ReadyMember{{PlayerID: "p", DisplayName: "P"}}, false); domain.ErrorCodeOf(err) != domain.CodeGameRequirement {
		t.Fatalf("ready without a color = %v", err)
	}
}

func TestTheMockGameWithoutRulesTakesAnyLobby(t *testing.T) {
	hooks := client(t, map[string]string{"MOCK_GAME_SECRET": secret, "MOCK_GAME_RULES": "off"})
	ctx := context.Background()
	if err := hooks.ValidateSettings(ctx, "", json.RawMessage(`{}`), nil); err != nil {
		t.Fatalf("empty settings = %v", err)
	}
	if err := hooks.ValidateSettings(ctx, "", json.RawMessage(`{"rounds":1}`), nil); err == nil {
		t.Fatal("the game took settings")
	}
	check, _ := hooks.ReadyGate(ctx, "")
	if err := check([]domain.ReadyMember{{PlayerID: "p", DisplayName: "P"}}, true); err != nil {
		t.Fatalf("ready = %v", err)
	}
	snapshot := domain.Snapshot{Lobby: domain.Lobby{ID: "6f1a0bf4-6c1f-4f63-9d6c-3d3c1c7c0f10", GameKey: "mock-game", Phase: domain.PhaseStarted, Round: 1}, StartRoster: domain.StartRoster{{PlayerID: "p"}}}
	first, err := hooks.Start(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := hooks.Start(ctx, snapshot); err != nil || again != first {
		t.Fatalf("second start = %q, %v; want %q", again, err, first)
	}
}

func TestTheMockGameRefusesABadConfiguration(t *testing.T) {
	for name, values := range map[string]map[string]string{
		"a short secret": {"MOCK_GAME_SECRET": "short"},
		"unknown rules":  {"MOCK_GAME_SECRET": secret, "MOCK_GAME_RULES": "maybe"},
		"a bad lobby":    {"MOCK_GAME_SECRET": secret, "MOCK_GAME_LOBBY_URL": "ftp://lobby"},
	} {
		if _, err := newHandler(environment(values)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
