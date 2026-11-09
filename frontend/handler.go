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

//go:embed index.html app.js relay.mjs resultretry.mjs roomapi.mjs style.css throttle.mjs vendor
var assets embed.FS

// catalogPath is the file, relative to the frontend's mount point, that lists
// the host's games as JSON.
const catalogPath = "games.json"

// LobbyIDPlaceholder marks where a game's result URL takes the lobby ID.
const LobbyIDPlaceholder = "{lobby_id}"

// Game is one entry in the frontend's game catalog. The host supplies every
// game-specific name, URL and line of copy, so the frontend stays generic.
type Game struct {
	// Key is the lobby game key. The host also registers it in its lobby policy.
	Key string `json:"key"`
	// Name is shown in the create form and above the room.
	Name string `json:"name"`
	// ResultURL is the same-origin path of the HTML document shown once the
	// lobby starts. LobbyIDPlaceholder is replaced with the lobby ID. The
	// document is fetched with the guest bearer credential.
	ResultURL string `json:"result_url"`
	// StartedHint optionally tells the players of a started lobby how they take
	// part, for example which client submits their turns.
	StartedHint string `json:"started_hint,omitempty"`
	// ExamplePlayer optionally lets the browser follow and play the game, and
	// enables the demo lobby for it.
	ExamplePlayer *ExamplePlayer `json:"example_player,omitempty"`
	// SettingsModule is the optional same-origin URL of an ES module that adds
	// the game's own controls to the create form and returns its
	// game_settings and teams. The frontend README describes its contract.
	SettingsModule string `json:"settings_module,omitempty"`
	// DataURLPrefix optionally lets the result document read the game's data.
	// The document can't send the guest credential itself, so it asks the
	// page with a game.request message, and the page fetches same-origin
	// paths under this prefix with GET and the credential.
	// LobbyIDPlaceholder is replaced with the room's lobby ID, and the prefix
	// ends with a slash. The frontend README describes the messages.
	DataURLPrefix string `json:"data_url_prefix,omitempty"`
	// RoomModule is the optional same-origin URL of an ES module that shows
	// the game's own controls to the player in a waiting room, such as a
	// choice the game requires before the player gets ready. The frontend
	// README describes its contract.
	RoomModule string `json:"room_module,omitempty"`
	// RoomAPIPrefix is the same-origin path prefix under which the room
	// module may call the game's API with the player's credential, with GET
	// or PUT. LobbyIDPlaceholder is replaced with the room's lobby ID, and
	// the prefix ends with a slash.
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
	// URL is the same-origin path of the page.
	URL string `json:"url"`
}

// ExamplePlayer describes a browser player that a game supplies as an ES module.
type ExamplePlayer struct {
	// Module is the same-origin URL of an ES module that exports an
	// ExamplePlayer class. The frontend README describes its contract.
	Module string `json:"module"`
	// Strategy names the example's strategy in a few words.
	Strategy string `json:"strategy"`
	// Summary describes in one sentence what each example player does.
	Summary string `json:"summary"`
}

// Handler serves the embedded UI assets and the host's game catalog. Mount it
// on the same origin as /lobby/v1 so guest credentials never need
// cross-origin configuration.
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
		response.Header().Set("Cache-Control", "no-store")
		if strings.TrimPrefix(request.URL.Path, "/") == catalogPath {
			http.ServeContent(response, request, catalogPath, time.Time{}, bytes.NewReader(catalog))
			return
		}
		files.ServeHTTP(response, request)
	}), nil
}

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
		if !sameOriginPath(game.ResultURL) || !strings.Contains(game.ResultURL, LobbyIDPlaceholder) {
			return fmt.Errorf("frontend: game %q needs a same-origin result URL containing %s", game.Key, LobbyIDPlaceholder)
		}
		if game.DataURLPrefix != "" && !validLobbyPrefix(game.DataURLPrefix) {
			return fmt.Errorf("frontend: game %q needs a same-origin data URL prefix containing %s and ending with /", game.Key, LobbyIDPlaceholder)
		}
		if game.SettingsModule != "" && !sameOriginPath(game.SettingsModule) {
			return fmt.Errorf("frontend: game %q needs a same-origin settings module", game.Key)
		}
		if game.RoomModule != "" && !sameOriginPath(game.RoomModule) {
			return fmt.Errorf("frontend: game %q needs a same-origin room module", game.Key)
		}
		if game.RoomAPIPrefix != "" && (game.RoomModule == "" || !validLobbyPrefix(game.RoomAPIPrefix)) {
			return fmt.Errorf("frontend: game %q needs a room module and a same-origin room API prefix containing %s and ending with /", game.Key, LobbyIDPlaceholder)
		}
		for _, link := range game.Links {
			if strings.TrimSpace(link.Label) == "" {
				return fmt.Errorf("frontend: game %q has a link without a label", game.Key)
			}
			if !sameOriginPath(link.URL) {
				return fmt.Errorf("frontend: game %q needs a same-origin URL for its link %q", game.Key, link.Label)
			}
		}
		if player := game.ExamplePlayer; player != nil {
			if !sameOriginPath(player.Module) {
				return fmt.Errorf("frontend: game %q needs a same-origin example player module", game.Key)
			}
			if strings.TrimSpace(player.Strategy) == "" || strings.TrimSpace(player.Summary) == "" {
				return fmt.Errorf("frontend: game %q needs an example player strategy and summary", game.Key)
			}
		}
	}
	return nil
}

// validLobbyPrefix accepts a same-origin path prefix for one lobby's paths.
// The page checks requested paths against the filled-in prefix, so a prefix
// that ends mid-segment would also admit sibling paths.
func validLobbyPrefix(prefix string) bool {
	return sameOriginPath(prefix) && strings.Contains(prefix, LobbyIDPlaceholder) && strings.HasSuffix(prefix, "/") &&
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

// sameOriginPath accepts absolute paths only, so the guest credential that
// accompanies these requests never leaves the frontend's origin. Browsers drop
// tab, line feed and carriage return from a URL before parsing it, so "/\t/host"
// would become "//host". Every control character and space is therefore
// rejected, along with the backslash that browsers read as a slash.
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
