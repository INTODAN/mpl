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
		StartedHint: "Answer in the quiz app.", SettingsModule: "/quiz/settings.mjs", DataURLPrefix: "/quiz/{lobby_id}/data/",
		RoomModule: "/quiz/room.mjs", RoomAPIPrefix: "/quiz/{lobby_id}/players/",
		Links:     []frontend.Link{{Label: "Past rounds", URL: "/quiz/rounds/"}},
		SeatNames: &frontend.SeatNames{Singular: "a quiz robot", Plural: "quiz robots"},
		ExamplePlayer: &frontend.ExamplePlayer{
			Module: "/quiz/player.mjs", Strategy: "First answer", Summary: "Each player picks the first answer.", Heading: "Your buzzer",
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
			"key": "quiz-demo", "name": "Quiz", "result_url": "/quiz/{lobby_id}/result", "started_hint": "Answer in the quiz app.", "settings_module": "/quiz/settings.mjs",
			"data_url_prefix": "/quiz/{lobby_id}/data/", "room_module": "/quiz/room.mjs", "room_api_prefix": "/quiz/{lobby_id}/players/",
			"links":          []any{map[string]any{"label": "Past rounds", "url": "/quiz/rounds/"}},
			"seat_names":     map[string]any{"singular": "a quiz robot", "plural": "quiz robots"},
			"example_player": map[string]any{"module": "/quiz/player.mjs", "strategy": "First answer", "summary": "Each player picks the first answer.", "heading": "Your buzzer"},
		},
		// A game that names no seats and no heading gets no such words.
		{"key": "chess", "name": "Chess", "result_url": "/chess/{lobby_id}"},
	}
	if !reflect.DeepEqual(catalog, want) {
		t.Fatalf("catalog %v", catalog)
	}
	if !strings.Contains(get(t, handler, "/lobby/ui/").Body.String(), "./app.js") {
		t.Fatal("index page not served")
	}
	// Browsers load ES modules only with a JavaScript content type.
	if contentType := get(t, handler, "/lobby/ui/relay.mjs").Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/javascript") {
		t.Fatalf("relay module content type %q", contentType)
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
		// The page compares requested paths with the filled-in prefix, so
		// the prefix must be a whole, canonical directory of one lobby.
		"data prefix without lobby":  func(game *frontend.Game) { game.DataURLPrefix = "/quiz/data/" },
		"data prefix without slash":  func(game *frontend.Game) { game.DataURLPrefix = "/quiz/{lobby_id}" },
		"data prefix mid-segment":    func(game *frontend.Game) { game.DataURLPrefix = "/quiz/{lobby_id}/data" },
		"data prefix with dots":      func(game *frontend.Game) { game.DataURLPrefix = "/quiz/../{lobby_id}/" },
		"data prefix with dot":       func(game *frontend.Game) { game.DataURLPrefix = "/quiz/./{lobby_id}/" },
		"data prefix with escape":    func(game *frontend.Game) { game.DataURLPrefix = "/quiz/%2e%2e/{lobby_id}/" },
		"data prefix with query":     func(game *frontend.Game) { game.DataURLPrefix = "/quiz/{lobby_id}/?a=/" },
		"data prefix with fragment":  func(game *frontend.Game) { game.DataURLPrefix = "/quiz/{lobby_id}/#/" },
		"room prefix without lobby":  func(game *frontend.Game) { game.RoomAPIPrefix = "/quiz/players/" },
		"room prefix mid-segment":    func(game *frontend.Game) { game.RoomAPIPrefix = "/quiz/{lobby_id}/players" },
		"room prefix with dots":      func(game *frontend.Game) { game.RoomAPIPrefix = "/quiz/../{lobby_id}/" },
		"room prefix without module": func(game *frontend.Game) { game.RoomModule = "" },
		"link without label":         func(game *frontend.Game) { game.Links[0].Label = " " },
		"link without url":           func(game *frontend.Game) { game.Links[0].URL = "" },
		"seats without singular":     func(game *frontend.Game) { game.SeatNames.Singular = " " },
		"seats without plural":       func(game *frontend.Game) { game.SeatNames.Plural = "" },
		"seats without names":        func(game *frontend.Game) { game.SeatNames = &frontend.SeatNames{} },
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
		cases["settings module "+name] = func(game *frontend.Game) { game.SettingsModule = value + "settings.mjs" }
		cases["data prefix "+name] = func(game *frontend.Game) { game.DataURLPrefix = value + "{lobby_id}/" }
		cases["room module "+name] = func(game *frontend.Game) { game.RoomModule = value + "room.mjs" }
		cases["room prefix "+name] = func(game *frontend.Game) { game.RoomAPIPrefix = value + "{lobby_id}/" }
		cases["link "+name] = func(game *frontend.Game) { game.Links[0].URL = value + "rounds/" }
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

// remoteQuizGame is quizGame on its own site: every URL absolute on the
// game's origin.
func remoteQuizGame(origin string) frontend.Game {
	game := quizGame()
	game.Origin = origin
	for _, field := range []*string{&game.ResultURL, &game.SettingsModule, &game.DataURLPrefix, &game.RoomModule, &game.RoomAPIPrefix,
		&game.Links[0].URL, &game.ExamplePlayer.Module} {
		*field = origin + *field
	}
	return game
}

// A lobby on its own site loads a game's screens from the game's site: the
// catalog takes absolute URLs on the game's origin, https or, for local
// development, http on localhost.
func TestHandlerAcceptsAbsoluteURLsOnTheGamesOrigin(t *testing.T) {
	for _, origin := range []string{"https://quiz.example.org", "https://quiz.example.org:8443", "http://localhost:8888", "http://localhost"} {
		game := remoteQuizGame(origin)
		game.WatchResultURL, game.WatchDataURLPrefix = origin+"/quiz/{lobby_id}/watch", origin+"/quiz/{lobby_id}/watch/"
		handler := mounted(t, []frontend.Game{game})
		var catalog []map[string]any
		if err := json.Unmarshal(get(t, handler, "/lobby/ui/games.json").Body.Bytes(), &catalog); err != nil {
			t.Fatal(err)
		}
		if catalog[0]["origin"] != origin || catalog[0]["result_url"] != origin+"/quiz/{lobby_id}/result" {
			t.Fatalf("catalog %v", catalog)
		}
	}
	// A game may keep some of its URLs as paths on the lobby page's origin.
	mixed := remoteQuizGame("https://quiz.example.org")
	mixed.Links[0].URL = "/quiz/rounds/"
	if _, err := frontend.Handler([]frontend.Game{mixed}); err != nil {
		t.Fatal(err)
	}
}

// Any URL on another origin than the game's configured one is refused at
// startup, as is an origin that isn't https or http on localhost.
func TestHandlerRefusesURLsOnOtherOrigins(t *testing.T) {
	origin := "https://quiz.example.org"
	cases := map[string]func(*frontend.Game){
		"no origin":          func(game *frontend.Game) { game.Origin = "" },
		"another origin":     func(game *frontend.Game) { game.Origin = "https://other.example.org" },
		"result elsewhere":   func(game *frontend.Game) { game.ResultURL = "https://other.example.org/quiz/{lobby_id}/result" },
		"module elsewhere":   func(game *frontend.Game) { game.ExamplePlayer.Module = "https://other.example.org/quiz/player.mjs" },
		"settings elsewhere": func(game *frontend.Game) { game.SettingsModule = "https://quiz.example.org.evil.example/settings.mjs" },
		"room elsewhere":     func(game *frontend.Game) { game.RoomModule = "https://quiz.example.org@evil.example/room.mjs" },
		"prefix elsewhere":   func(game *frontend.Game) { game.DataURLPrefix = "http://quiz.example.org/quiz/{lobby_id}/data/" },
		"room API elsewhere": func(game *frontend.Game) {
			game.RoomAPIPrefix = "https://quiz.example.org:8443/quiz/{lobby_id}/players/"
		},
		"link elsewhere":       func(game *frontend.Game) { game.Links[0].URL = "https://other.example.org/rounds/" },
		"protocol-relative":    func(game *frontend.Game) { game.ResultURL = "https://quiz.example.org//evil.example/{lobby_id}" },
		"prefix with dots":     func(game *frontend.Game) { game.DataURLPrefix = "https://quiz.example.org/quiz/../{lobby_id}/" },
		"bare origin":          func(game *frontend.Game) { game.SettingsModule = "https://quiz.example.org" },
		"http origin":          func(game *frontend.Game) { *game = remoteQuizGame("http://quiz.example.org") },
		"http on 127.0.0.1":    func(game *frontend.Game) { *game = remoteQuizGame("http://127.0.0.1:8888") },
		"origin with path":     func(game *frontend.Game) { *game = remoteQuizGame("https://quiz.example.org/play") },
		"origin with slash":    func(game *frontend.Game) { game.Origin = "https://quiz.example.org/" },
		"upper-case origin":    func(game *frontend.Game) { *game = remoteQuizGame("https://Quiz.example.org") },
		"default port":         func(game *frontend.Game) { *game = remoteQuizGame("https://quiz.example.org:443") },
		"origin with user":     func(game *frontend.Game) { *game = remoteQuizGame("https://user@quiz.example.org") },
		"javascript origin":    func(game *frontend.Game) { *game = remoteQuizGame("javascript://quiz.example.org") },
		"origin with a tab":    func(game *frontend.Game) { *game = remoteQuizGame("https://quiz.example.org\t") },
		"origin with a query":  func(game *frontend.Game) { *game = remoteQuizGame("https://quiz.example.org?a") },
		"origin with fragment": func(game *frontend.Game) { *game = remoteQuizGame("https://quiz.example.org#a") },
	}
	for name, mutate := range cases {
		game := remoteQuizGame(origin)
		mutate(&game)
		if _, err := frontend.Handler([]frontend.Game{game}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The frontend learns every game from its host, so its own assets name none.
func TestAssetsNameNoGame(t *testing.T) {
	handler := mounted(t, []frontend.Game{quizGame()})
	// The file server answers index.html at the mount point itself.
	for _, file := range []string{"", "app.js", "links.mjs", "relay.mjs", "resultretry.mjs", "roomapi.mjs", "rounds.mjs", "style.css", "throttle.mjs", "watch.mjs"} {
		path := "/lobby/ui/" + file
		body, err := io.ReadAll(get(t, handler, path).Body)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(body))
		// Map choice belongs to the game's settings module, not to the frontend.
		for _, word := range []string{"island", "soldier", "castle", "python", "/game/", "from-lobby", "map_template", "map file", "player_amount", "/map/"} {
			if strings.Contains(text, word) {
				t.Errorf("%s mentions %q", path, word)
			}
		}
	}
}

// A game opts into watching by naming the document a watcher sees, and the
// only data that document may read.
func TestHandlerCatalogCarriesTheWatcherURLs(t *testing.T) {
	game := quizGame()
	game.WatchResultURL = "/quiz/{lobby_id}/watch/result"
	game.WatchDataURLPrefix = "/quiz/{lobby_id}/watch/"
	handler := mounted(t, []frontend.Game{game, {Key: "chess", Name: "Chess", ResultURL: "/chess/{lobby_id}"}})
	var catalog []map[string]any
	if err := json.Unmarshal(get(t, handler, "/lobby/ui/games.json").Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog[0]["watch_result_url"] != "/quiz/{lobby_id}/watch/result" || catalog[0]["watch_data_url_prefix"] != "/quiz/{lobby_id}/watch/" {
		t.Fatalf("catalog entry %v", catalog[0])
	}
	// A game that doesn't opt in lists neither, so the page offers no watching.
	if _, listed := catalog[1]["watch_result_url"]; listed {
		t.Fatalf("catalog entry %v", catalog[1])
	}
}

func TestHandlerRejectsInvalidWatcherURLs(t *testing.T) {
	cases := map[string]func(*frontend.Game){
		"result without lobby":     func(game *frontend.Game) { game.WatchResultURL = "/quiz/watch/result" },
		"result cross-origin":      func(game *frontend.Game) { game.WatchResultURL = "https://example.com/{lobby_id}" },
		"result protocol-relative": func(game *frontend.Game) { game.WatchResultURL = "//evil.com/{lobby_id}" },
		"prefix without result":    func(game *frontend.Game) { game.WatchResultURL = "" },
		"prefix without lobby":     func(game *frontend.Game) { game.WatchDataURLPrefix = "/quiz/watch/" },
		"prefix without slash":     func(game *frontend.Game) { game.WatchDataURLPrefix = "/quiz/{lobby_id}/watch" },
		"prefix with dots":         func(game *frontend.Game) { game.WatchDataURLPrefix = "/quiz/../{lobby_id}/" },
		"prefix with escape":       func(game *frontend.Game) { game.WatchDataURLPrefix = "/quiz/%2e%2e/{lobby_id}/" },
		"prefix with backslash":    func(game *frontend.Game) { game.WatchDataURLPrefix = `/quiz\{lobby_id}/` },
		"prefix protocol-relative": func(game *frontend.Game) { game.WatchDataURLPrefix = "//evil.com/{lobby_id}/" },
	}
	for name, mutate := range cases {
		game := quizGame()
		game.WatchResultURL = "/quiz/{lobby_id}/watch/result"
		game.WatchDataURLPrefix = "/quiz/{lobby_id}/watch/"
		mutate(&game)
		if _, err := frontend.Handler([]frontend.Game{game}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The page and the catalog name the other files and change with a deploy;
// everything else may be cached for a few minutes.
func TestHandlerCachesOnlyTheStaticAssets(t *testing.T) {
	handler := mounted(t, []frontend.Game{quizGame()})
	for file, want := range map[string]string{
		"": "no-store", "games.json": "no-store", "index.html": "no-store",
		"app.js": "public, max-age=300", "relay.mjs": "public, max-age=300", "style.css": "public, max-age=300",
		// Go's file server drops Cache-Control from an error answer, so a missing
		// file's 404 is never kept.
		"missing.mjs": "",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/lobby/ui/"+file, nil))
		if got := recorder.Header().Get("Cache-Control"); got != want {
			t.Errorf("%q: Cache-Control %q, want %q", file, got, want)
		}
	}
}
