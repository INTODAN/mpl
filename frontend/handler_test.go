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
		"cross-origin result":    func(game *frontend.Game) { game.ResultURL = "https://example.com/{lobby_id}" },
		"protocol-relative":      func(game *frontend.Game) { game.ResultURL = "//example.com/{lobby_id}" },
		"cross-origin module":    func(game *frontend.Game) { game.ExamplePlayer.Module = "https://example.com/player.mjs" },
		"missing player summary": func(game *frontend.Game) { game.ExamplePlayer.Summary = "" },
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
		for _, word := range []string{"island", "soldier", "castle", "python"} {
			if strings.Contains(text, word) {
				t.Errorf("%s mentions %q", path, word)
			}
		}
	}
}
