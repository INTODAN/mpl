// Package frontend serves the temporary browser client for the generic lobby API.
package frontend

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"islandBattles/lobby/domain"
)

//go:embed index.html app.js style.css vendor
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

// sameOriginPath accepts absolute paths only, so the guest credential that
// accompanies these requests never leaves the frontend's origin.
func sameOriginPath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.Contains(value, `\`)
}
