package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportDeterministic(t *testing.T) {
	first, err := buildArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.openapi, second.openapi) || !bytes.Equal(first.events, second.events) {
		t.Fatal("independent exports differ")
	}
	if !bytes.HasSuffix(first.openapi, []byte("\n")) || !bytes.HasSuffix(first.events, []byte("\n")) {
		t.Fatal("artifacts must have one trailing newline")
	}
}

func TestExportPublicOperations(t *testing.T) {
	result, err := buildArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	decodeJSON(t, result.openapi, &document)
	paths := document["paths"].(map[string]any)
	want := map[string]string{
		"/lobby/v1/sessions":                                                        "createLobbySession",
		"/lobby/v1/connection-token":                                                "createLobbyConnectionToken",
		"/lobby/v1/lobbies":                                                         "createLobby",
		"/lobby/v1/memberships":                                                     "joinLobby",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}":                             "getLobby",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/team":                        "selectLobbyTeam",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/ready":                       "setLobbyReady",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/start":                       "startLobby",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/settings":                    "changeLobbySettings",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/reopen":                      "reopenLobby",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/seats":                       "addLobbySeat",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/seats/{seat_id}":             "removeLobbySeat",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/member-settings":             "setLobbyMemberSettings",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/membership":                  "leaveLobby",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/heartbeat":                   "heartbeatLobby",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/leases/{client_instance_id}": "releaseLobbyLease",
		"/lobby/v1/watches":                                                         "watchLobby",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/watch":                       "unwatchLobby",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/watchers":                    "listLobbyWatchers",
		"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/watchers/{player_id}":        "kickLobbyWatcher",
	}
	if len(paths) != len(want) {
		t.Fatalf("path count = %d, want %d", len(paths), len(want))
	}
	for path, operationID := range want {
		pathItem, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("missing path %s", path)
		}
		found := false
		for _, method := range []string{"get", "post", "put", "delete"} {
			operation, ok := pathItem[method].(map[string]any)
			if !ok {
				continue
			}
			if operation["operationId"] == operationID {
				found = true
			}
		}
		if !found {
			t.Fatalf("operation %q missing at %s", operationID, path)
		}
	}
}

func TestExportExcludesInternalAndGameplay(t *testing.T) {
	result, err := buildArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	decodeJSON(t, result.openapi, &document)
	for path := range document["paths"].(map[string]any) {
		if strings.Contains(path, "/internal/") || strings.Contains(path, "/game/") || strings.Contains(path, "map") || strings.Contains(path, "generation") {
			t.Fatalf("OpenAPI contains forbidden route %q", path)
		}
	}
}

func TestExportSecurityAndResponses(t *testing.T) {
	result, err := buildArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	decodeJSON(t, result.openapi, &document)
	paths := document["paths"].(map[string]any)
	for path, raw := range paths {
		for method, operationRaw := range raw.(map[string]any) {
			operation, ok := operationRaw.(map[string]any)
			if !ok || !map[string]bool{"get": true, "post": true, "put": true, "delete": true}[method] {
				continue
			}
			security, hasSecurity := operation["security"]
			if path == "/lobby/v1/sessions" {
				if hasSecurity {
					t.Fatalf("session unexpectedly has security metadata")
				}
			} else if !hasSecurity || len(security.([]any)) != 1 {
				t.Fatalf("%s %s missing bearer security", method, path)
			}
		}
	}
	assertResponse(t, paths, "/lobby/v1/sessions", "post", "201")
	assertResponse(t, paths, "/lobby/v1/lobbies", "post", "201")
	assertResponse(t, paths, "/lobby/v1/games/{game_key}/lobbies/{lobby_id}/membership", "delete", "204")
	assertResponse(t, paths, "/lobby/v1/games/{game_key}/lobbies/{lobby_id}/leases/{client_instance_id}", "delete", "204")
	problem := document["components"].(map[string]any)["schemas"].(map[string]any)["Problem"].(map[string]any)
	if problem["properties"].(map[string]any)["code"] == nil {
		t.Fatal("problem schema does not contain code")
	}
}

func TestInvalidationComponentAndEventSchemaSelfContained(t *testing.T) {
	result, err := buildArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	var document, events map[string]any
	decodeJSON(t, result.openapi, &document)
	decodeJSON(t, result.events, &events)
	component := document["components"].(map[string]any)["schemas"].(map[string]any)["LobbyInvalidation"].(map[string]any)
	properties := component["properties"].(map[string]any)
	for _, name := range []string{"schema_version", "event_id", "game_key", "lobby_id", "version", "type"} {
		if properties[name] == nil {
			t.Fatalf("invalidation property %q missing", name)
		}
	}
	refs := map[string]bool{}
	collectRefs(events, refs)
	defs, _ := events["$defs"].(map[string]any)
	for ref := range refs {
		if !strings.HasPrefix(ref, "#/$defs/") {
			t.Fatalf("event schema has non-local ref %q", ref)
		}
		if defs[strings.TrimPrefix(ref, "#/$defs/")] == nil {
			t.Fatalf("event schema ref %q is unresolved", ref)
		}
	}
}

func TestCheckModeAndWriteFailure(t *testing.T) {
	result, err := buildArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	openapiPath := filepath.Join(directory, "openapi.json")
	eventsPath := filepath.Join(directory, "events.json")
	if err := os.WriteFile(openapiPath, result.openapi, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(eventsPath, result.events, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--check", "--output", openapiPath, "--events-output", eventsPath}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("matching check failed: %v", err)
	}
	if err := os.WriteFile(eventsPath, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--check", "--output", openapiPath, "--events-output", eventsPath}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("changed check unexpectedly passed")
	}
	before, err := os.ReadFile(openapiPath)
	if err != nil {
		t.Fatal(err)
	}
	badEvents := filepath.Join(directory, "missing", "events.json")
	if err := run([]string{"--output", openapiPath, "--events-output", badEvents}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid destination unexpectedly succeeded")
	}
	after, err := os.ReadFile(openapiPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed write changed the existing OpenAPI artifact")
	}
}

// The game API is a document of its own, signed rather than bearer
// authenticated, and checked like the others.
func TestExportTheGameAPIOnItsOwn(t *testing.T) {
	result, err := buildArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	decodeJSON(t, result.gameAPI, &document)
	paths := document["paths"].(map[string]any)
	want := map[string]string{
		"/lobby/v1/game-api/games/{game_key}/lobbies/{lobby_id}":                 "get getGameLobby",
		"/lobby/v1/game-api/games/{game_key}/lobbies/{lobby_id}/authorize-watch": "post authorizeGameWatch",
	}
	if len(paths) != len(want) {
		t.Fatalf("game API paths = %v", paths)
	}
	for path, operation := range want {
		method, identifier, _ := strings.Cut(operation, " ")
		found, ok := paths[path].(map[string]any)[method].(map[string]any)
		if !ok || found["operationId"] != identifier {
			t.Fatalf("%s %s is missing", method, path)
		}
		security, _ := json.Marshal(found["security"])
		if string(security) != `[{"LobbySignature":[]}]` {
			t.Fatalf("%s security = %s", identifier, security)
		}
		assertResponse(t, paths, path, method, "401")
	}
	schemes := document["components"].(map[string]any)["securitySchemes"].(map[string]any)
	if _, ok := schemes["LobbyBearer"]; ok || schemes["LobbySignature"] == nil {
		t.Fatalf("security schemes = %v", schemes)
	}
	var public map[string]any
	decodeJSON(t, result.openapi, &public)
	for path := range public["paths"].(map[string]any) {
		if strings.Contains(path, "game-api") {
			t.Fatalf("the player document has %s", path)
		}
	}

	directory := t.TempDir()
	gameAPIPath := filepath.Join(directory, "game-api.openapi.json")
	if err := run([]string{"--game-api-output", gameAPIPath}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--check", "--game-api-output", gameAPIPath}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("matching check failed: %v", err)
	}
	if err := os.WriteFile(gameAPIPath, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--check", "--game-api-output", gameAPIPath}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("changed check unexpectedly passed")
	}
	if err := run([]string{"--output", gameAPIPath, "--game-api-output", gameAPIPath}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("one file for two documents was accepted")
	}
}

func assertResponse(t *testing.T, paths map[string]any, path, method, status string) {
	t.Helper()
	operation := paths[path].(map[string]any)[method].(map[string]any)
	responses := operation["responses"].(map[string]any)
	if responses[status] == nil {
		t.Fatalf("%s %s missing response %s", method, path, status)
	}
}

func decodeJSON(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func collectRefs(value any, refs map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		if ref, ok := typed["$ref"].(string); ok {
			refs[ref] = true
		}
		for _, child := range typed {
			collectRefs(child, refs)
		}
	case []any:
		for _, child := range typed {
			collectRefs(child, refs)
		}
	}
}
