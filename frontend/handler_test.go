package frontend_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"islandBattles/lobby/frontend"
)

func quizGame() frontend.Game {
	return frontend.Game{
		Key: "quiz-demo", Name: "Quiz", ResultURL: "/quiz/{lobby_id}/result",
		StartedHint: "Answer in the quiz app.",
		ExamplePlayer: &frontend.ExamplePlayer{
			Module: "/quiz/player.mjs", Strategy: "First answer", Summary: "Each player picks the first answer.",
		},
	}
}

// mounted serves the frontend under a prefix, as hosts mount it.
func mounted(t *testing.T, games []frontend.Game) http.Handler {
	t.Helper()
	handler, err := frontend.Handler(games)
	if err != nil {
		t.Fatal(err)
	}
	return http.StripPrefix("/lobby/ui/", handler)
}

func get(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, recorder.Code)
	}
	return recorder
}

func TestHandlerServesTheHostCatalog(t *testing.T) {
	plain := frontend.Game{Key: "chess", Name: "Chess", ResultURL: "/chess/{lobby_id}"}
	handler := mounted(t, []frontend.Game{quizGame(), plain})
	response := get(t, handler, "/lobby/ui/games.json")
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content type %q", contentType)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("catalog may be cached")
	}
	var catalog []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{
			"key": "quiz-demo", "name": "Quiz", "result_url": "/quiz/{lobby_id}/result", "started_hint": "Answer in the quiz app.",
			"example_player": map[string]any{"module": "/quiz/player.mjs", "strategy": "First answer", "summary": "Each player picks the first answer."},
		},
		{"key": "chess", "name": "Chess", "result_url": "/chess/{lobby_id}"},
	}
	if !reflect.DeepEqual(catalog, want) {
		t.Fatalf("catalog %v", catalog)
	}
	if !strings.Contains(get(t, handler, "/lobby/ui/").Body.String(), "./app.js") {
		t.Fatal("index page not served")
	}
}

func TestHandlerRejectsInvalidCatalogs(t *testing.T) {
	cases := map[string]func(*frontend.Game){
		"invalid key":            func(game *frontend.Game) { game.Key = "Quiz" },
		"missing name":           func(game *frontend.Game) { game.Name = " " },
		"no lobby placeholder":   func(game *frontend.Game) { game.ResultURL = "/quiz/result" },
		"missing player summary": func(game *frontend.Game) { game.ExamplePlayer.Summary = "" },
		"missing strategy":       func(game *frontend.Game) { game.ExamplePlayer.Strategy = "" },
		"empty result url":       func(game *frontend.Game) { game.ResultURL = "" },
		"empty module":           func(game *frontend.Game) { game.ExamplePlayer.Module = "" },
		"module //":              func(game *frontend.Game) { game.ExamplePlayer.Module = "//" },
	}
	// Hostile values for both URL fields. A browser strips tab, LF and CR
	// before parsing, so "/<ctrl>/evil.com" reads as "//evil.com".
	hostile := map[string]string{
		"cross-origin":       "https://example.com/",
		"protocol-relative":  "//evil.com/",
		"no leading slash":   "quiz/",
		"relative dots":      "../quiz/",
		"tab":                "/\t/evil.com/",
		"line feed":          "/\n/evil.com/",
		"carriage return":    "/\r/evil.com/",
		"embedded tab":       "/quiz\t/",
		"space":              "/quiz /",
		"delete":             "/quiz\x7f/",
		"leading backslash":  `\evil.com/`,
		"slash backslash":    `/\evil.com/`,
		"embedded backslash": `/quiz\evil/`,
		"javascript scheme":  "javascript:alert(1)//",
	}
	for name, value := range hostile {
		cases["result url "+name] = func(game *frontend.Game) { game.ResultURL = value + "{lobby_id}" }
		cases["module "+name] = func(game *frontend.Game) { game.ExamplePlayer.Module = value + "player.mjs" }
	}
	for name, mutate := range cases {
		game := quizGame()
		mutate(&game)
		if _, err := frontend.Handler([]frontend.Game{game}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := frontend.Handler(nil); err == nil {
		t.Error("empty catalog accepted")
	}
	if _, err := frontend.Handler([]frontend.Game{quizGame(), quizGame()}); err == nil {
		t.Error("duplicate key accepted")
	}
}

// The frontend learns every game from its host, so its own assets name none.
func TestAssetsNameNoGame(t *testing.T) {
	handler := mounted(t, []frontend.Game{quizGame()})
	// The file server answers index.html at the mount point itself.
	for _, file := range []string{"", "app.js", "style.css"} {
		path := "/lobby/ui/" + file
		body, err := io.ReadAll(get(t, handler, path).Body)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(body))
		for _, word := range []string{"island", "soldier", "castle", "python", "/game/", "from-lobby"} {
			if strings.Contains(text, word) {
				t.Errorf("%s mentions %q", path, word)
			}
		}
	}
}
