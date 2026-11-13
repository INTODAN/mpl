// Package frontend serves the temporary browser client for the generic lobby API.
package frontend

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"islandBattles/lobby/domain"
)

//go:embed index.html app.js catalog.mjs links.mjs playertoken.mjs relay.mjs resultretry.mjs roomapi.mjs rounds.mjs style.css throttle.mjs vendor watch.mjs
var assets embed.FS

// catalogPath is the file, relative to the frontend's mount point, that lists
// the host's games as JSON.
const catalogPath = "games.json"

// StaticAssetCacheControl is the Cache-Control value of the scripts and styles
// that hosts serve beside the lobby page. A deploy reaches players within five
// minutes, so file names don't need a fingerprint.
const StaticAssetCacheControl = "public, max-age=300"

// LobbyIDPlaceholder marks where a game's result URL takes the lobby ID.
const LobbyIDPlaceholder = "{lobby_id}"

// RoundPlaceholder optionally marks where a game's result URL takes the
// lobby's round, so the game answers with the game of that round only.
const RoundPlaceholder = "{round}"

// Game is one entry in the frontend's game catalog. The host supplies every
// game-specific name, URL and line of copy, so the frontend stays generic.
//
// Each of its URLs is either a path on the lobby page's own origin, such as
// /quiz/{lobby_id}/result, or an absolute URL on the game's Origin, such as
// https://quiz.example.org/quiz/{lobby_id}/result, for a game whose site is
// not the lobby's. The page then loads the game's modules and documents and
// calls its API across origins, so the game's site must allow the lobby's
// origin with CORS.
type Game struct {
	// Key is the lobby game key. The host also registers it in its lobby policy.
	Key string `json:"key"`
	// Name is shown in the create form and above the room.
	Name string `json:"name"`
	// Origin is the game's own site, such as https://quiz.example.org, which
	// the catalog's absolute URLs must be on: https, or http on localhost for
	// local development. Without it, every URL is a path on the lobby page's
	// origin. The page passes the game's origin to its modules as gameURL.
	Origin string `json:"origin,omitempty"`
	// ResultURL is the URL of the HTML document shown once the lobby starts.
	// LobbyIDPlaceholder is replaced with the lobby ID, and RoundPlaceholder,
	// where it appears, with the lobby's round: a lobby plays one game per
	// round, and the page loads each round's document anew. The document is
	// fetched with the player's token for the game (playertoken.mjs), never
	// the guest credential.
	ResultURL string `json:"result_url"`
	// WatchResultURL optionally lets people outside the roster watch a
	// lobby of this game, which the page then offers ("Watch a lobby"). It is
	// the URL of the HTML document a watcher sees, with LobbyIDPlaceholder as
	// in ResultURL, fetched with the player's token for the game. It answers
	// 404 while the game has not started, and 403 to a watcher whom the host
	// removed. A watcher's lobby has no round to ask for, so the document is
	// of the lobby's newest game; it tells the page with a game.reload message
	// when a later round's game starts.
	WatchResultURL string `json:"watch_result_url,omitempty"`
	// WatchDataURLPrefix is DataURLPrefix for a watcher's document: the only
	// paths it may read. The page never falls back to DataURLPrefix for a
	// watcher, who may not read what a player does.
	WatchDataURLPrefix string `json:"watch_data_url_prefix,omitempty"`
	// StartedHint optionally tells the players of a started lobby how they take
	// part, for example which client submits their turns.
	StartedHint string `json:"started_hint,omitempty"`
	// ExamplePlayer optionally lets the browser follow and play the game, and
	// enables the demo lobby for it.
	ExamplePlayer *ExamplePlayer `json:"example_player,omitempty"`
	// SettingsModule is the optional URL of an ES module that adds the game's
	// own controls to the create form and returns its game_settings and
	// teams. The frontend README describes its contract.
	SettingsModule string `json:"settings_module,omitempty"`
	// DataURLPrefix optionally lets the result document read the game's data.
	// The document can't send a credential itself, so it asks the page with a
	// game.request message, and the page fetches paths under this prefix, on
	// the prefix's origin, with GET and the player's token for the game.
	// LobbyIDPlaceholder is replaced with the room's lobby ID, and the prefix
	// ends with a slash. The frontend README describes the messages.
	DataURLPrefix string `json:"data_url_prefix,omitempty"`
	// RoomModule is the optional URL of an ES module that shows the game's own
	// controls to the player in a waiting room, such as a choice the game
	// requires before the player gets ready. The frontend README describes
	// its contract.
	RoomModule string `json:"room_module,omitempty"`
	// RoomAPIPrefix is the URL prefix under which the room module may call
	// the game's API with the player's token for the game, with GET or PUT.
	// LobbyIDPlaceholder is replaced with the room's lobby ID, and the prefix
	// ends with a slash.
	RoomAPIPrefix string `json:"room_api_prefix,omitempty"`
	// Links optionally lists the game's own pages that the lobby page links
	// to, such as one that still works after a lobby is gone.
	Links []Link `json:"links,omitempty"`
}

// Link is one of a game's pages that the lobby page links to. It opens in a
// new tab, so the player keeps their lobby.
type Link struct {
	// Label is the link's text.
	Label string `json:"label"`
	// URL is the page's URL.
	URL string `json:"url"`
}

// ExamplePlayer describes a browser player that a game supplies as an ES module.
type ExamplePlayer struct {
	// Module is the URL of an ES module that exports an ExamplePlayer class.
	// The frontend README describes its contract.
	Module string `json:"module"`
	// Strategy names the example's strategy in a few words.
	Strategy string `json:"strategy"`
	// Summary describes in one sentence what each example player does.
	Summary string `json:"summary"`
}

// Handler serves the embedded UI assets and the host's game catalog. Mount it
// on the same origin as /lobby/v1 so guest credentials never need
// cross-origin configuration. It refuses a catalog with a URL on any origin
// other than the lobby page's and the game's own Origin.
func Handler(games []Game) (http.Handler, error) {
	if err := validateCatalog(games); err != nil {
		return nil, err
	}
	catalog, err := json.Marshal(games)
	if err != nil {
		return nil, fmt.Errorf("frontend: encode game catalog: %w", err)
	}
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		name := strings.TrimPrefix(request.URL.Path, "/")
		// The catalog and the page change with a deploy and name the other
		// files, so they are never cached. The rest may sit at the CDN and in
		// browsers for a few minutes, so one page load doesn't run the host
		// once per file.
		if name == "" || name == catalogPath || name == "index.html" {
			response.Header().Set("Cache-Control", "no-store")
		} else {
			response.Header().Set("Cache-Control", StaticAssetCacheControl)
		}
		if name == catalogPath {
			http.ServeContent(response, request, catalogPath, time.Time{}, bytes.NewReader(catalog))
			return
		}
		files.ServeHTTP(response, request)
	}), nil
}

// onItsOrigin ends the errors about URLs on other origins.
const onItsOrigin = "on the lobby page's origin (a path) or on the game's origin"

func validateCatalog(games []Game) error {
	if len(games) == 0 {
		return errors.New("frontend: the game catalog is empty")
	}
	keys := make(map[string]bool, len(games))
	for _, game := range games {
		if err := domain.ValidateGameKey(game.Key); err != nil {
			return fmt.Errorf("frontend: game %q: %w", game.Key, err)
		}
		if keys[game.Key] {
			return fmt.Errorf("frontend: game %q is listed twice", game.Key)
		}
		keys[game.Key] = true
		if strings.TrimSpace(game.Name) == "" {
			return fmt.Errorf("frontend: game %q has no name", game.Key)
		}
		if game.Origin != "" && !ValidGameOrigin(game.Origin) {
			return fmt.Errorf("frontend: game %q needs an origin such as https://game.example.org: https, or http on localhost, without a path", game.Key)
		}
		// Every URL is a path on the lobby page's origin, or on the game's.
		path := func(value string) (string, bool) { return gamePath(game.Origin, value) }
		if result, ok := path(game.ResultURL); !ok || !strings.Contains(result, LobbyIDPlaceholder) {
			return fmt.Errorf("frontend: game %q needs a result URL %s, containing %s", game.Key, onItsOrigin, LobbyIDPlaceholder)
		}
		if game.DataURLPrefix != "" && !validLobbyPrefix(path(game.DataURLPrefix)) {
			return fmt.Errorf("frontend: game %q needs a data URL prefix %s, containing %s and ending with /", game.Key, onItsOrigin, LobbyIDPlaceholder)
		}
		if watch, ok := path(game.WatchResultURL); game.WatchResultURL != "" && (!ok || !strings.Contains(watch, LobbyIDPlaceholder)) {
			return fmt.Errorf("frontend: game %q needs a watch result URL %s, containing %s", game.Key, onItsOrigin, LobbyIDPlaceholder)
		}
		if game.WatchDataURLPrefix != "" && (game.WatchResultURL == "" || !validLobbyPrefix(path(game.WatchDataURLPrefix))) {
			return fmt.Errorf("frontend: game %q needs a watch result URL and a watch data URL prefix %s, containing %s and ending with /", game.Key, onItsOrigin, LobbyIDPlaceholder)
		}
		if _, ok := path(game.SettingsModule); game.SettingsModule != "" && !ok {
			return fmt.Errorf("frontend: game %q needs a settings module %s", game.Key, onItsOrigin)
		}
		if _, ok := path(game.RoomModule); game.RoomModule != "" && !ok {
			return fmt.Errorf("frontend: game %q needs a room module %s", game.Key, onItsOrigin)
		}
		if game.RoomAPIPrefix != "" && (game.RoomModule == "" || !validLobbyPrefix(path(game.RoomAPIPrefix))) {
			return fmt.Errorf("frontend: game %q needs a room module and a room API prefix %s, containing %s and ending with /", game.Key, onItsOrigin, LobbyIDPlaceholder)
		}
		for _, link := range game.Links {
			if strings.TrimSpace(link.Label) == "" {
				return fmt.Errorf("frontend: game %q has a link without a label", game.Key)
			}
			if _, ok := path(link.URL); !ok {
				return fmt.Errorf("frontend: game %q needs a URL %s for its link %q", game.Key, onItsOrigin, link.Label)
			}
		}
		if player := game.ExamplePlayer; player != nil {
			if _, ok := path(player.Module); !ok {
				return fmt.Errorf("frontend: game %q needs an example player module %s", game.Key, onItsOrigin)
			}
			if strings.TrimSpace(player.Strategy) == "" || strings.TrimSpace(player.Summary) == "" {
				return fmt.Errorf("frontend: game %q needs an example player strategy and summary", game.Key)
			}
		}
	}
	return nil
}

// ValidGameOrigin accepts a game's Origin: an https origin, or an http one on
// localhost for local development, written as browsers write an origin, such
// as https://game.example.org or http://localhost:8888: in lower case,
// without a path or a default port.
func ValidGameOrigin(origin string) bool {
	if strings.ContainsFunc(origin, func(char rune) bool { return char <= 0x20 || char == 0x7f || char == '\\' }) {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	switch {
	case parsed.Scheme == "https":
	case parsed.Scheme == "http" && parsed.Hostname() == "localhost":
	default:
		return false
	}
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		return false
	}
	// Anything else, such as a path, a query, a user or an upper-case
	// letter, makes the origin differ from what it is rebuilt from.
	return origin == strings.ToLower(parsed.Scheme+"://"+parsed.Host)
}

// gamePath returns the path of a catalog URL: the URL itself when it is a
// path on the lobby page's origin, or what follows origin when it is an
// absolute URL on the game's origin. It reports false for any other URL.
func gamePath(origin, value string) (string, bool) {
	if strings.HasPrefix(value, "/") {
		return value, sameOriginPath(value)
	}
	if origin == "" || !strings.HasPrefix(value, origin+"/") {
		return "", false
	}
	path := strings.TrimPrefix(value, origin)
	return path, sameOriginPath(path)
}

// validLobbyPrefix accepts the path prefix of one lobby's paths, as gamePath
// returns it. The page checks requested paths against the filled-in prefix,
// so a prefix that ends mid-segment would also admit sibling paths.
func validLobbyPrefix(prefix string, ok bool) bool {
	return ok && strings.Contains(prefix, LobbyIDPlaceholder) && strings.HasSuffix(prefix, "/") &&
		!strings.ContainsAny(prefix, "%?#") && !hasDotSegment(prefix)
}

// hasDotSegment reports whether a path has a "." or ".." segment, which a
// browser resolves away before requesting it.
func hasDotSegment(path string) bool {
	for segment := range strings.SplitSeq(path, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

// sameOriginPath accepts absolute paths only, so the player token that
// accompanies these requests never leaves the origin they are on. Browsers
// drop tab, line feed and carriage return from a URL before parsing it, so
// "/\t/host" would become "//host". Every control character and space is
// therefore rejected, along with the backslash that browsers read as a slash.
func sameOriginPath(value string) bool {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.Contains(value, `\`) {
		return false
	}
	for _, char := range value {
		if char <= 0x20 || char == 0x7f {
			return false
		}
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "" && parsed.Host == ""
}
