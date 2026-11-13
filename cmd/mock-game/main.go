// Command mock-game serves the lobby's mock game, lobby/mockgame, over HTTP,
// so that live tests can configure a lobby host with a game that it calls
// over HTTP, as integration/run.py --mock-game does. It reads:
//
//	MOCK_GAME_ADDR       host:port to listen on
//	MOCK_GAME_KEY        the game's key, mock-game when unset
//	MOCK_GAME_SECRET     the hooks' shared secret, at least 32 bytes
//	MOCK_GAME_LOBBY_URL  optional: the lobby whose game API GET /watch reads
//	MOCK_GAME_RULES      on (the default) or off
//
// The hooks are served under mockgame.HooksPath. With MOCK_GAME_RULES=off the
// game keeps its start and round end, but takes no game settings and no member
// settings, has no seats, and lets everyone get ready, as a game without
// rules of its own does, so the lobby's generic live suite plays it.
package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"islandBattles/lobby/gamehooks"
	"islandBattles/lobby/mockgame"
)

func main() {
	handler, err := newHandler(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	address := os.Getenv("MOCK_GAME_ADDR")
	if address == "" {
		log.Fatal("MOCK_GAME_ADDR is not set")
	}
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}

// newHandler builds the mock game's handler from the environment.
func newHandler(getenv func(string) string) (http.Handler, error) {
	key := getenv("MOCK_GAME_KEY")
	if key == "" {
		key = "mock-game"
	}
	secret := []byte(getenv("MOCK_GAME_SECRET"))
	game, err := mockgame.New(key, secret)
	if err != nil {
		return nil, err
	}
	if lobbyURL := getenv("MOCK_GAME_LOBBY_URL"); lobbyURL != "" {
		client, err := gamehooks.NewGameAPIClient(key, lobbyURL, secret)
		if err != nil {
			return nil, err
		}
		game.UseLobby(client)
	}
	switch getenv("MOCK_GAME_RULES") {
	case "", "on":
		return game, nil
	case "off":
	default:
		return nil, errors.New("MOCK_GAME_RULES must be on or off")
	}
	registration := game.Registration()
	// Nil hooks answer as the lobby's defaults: only `{}` as settings, no
	// member settings or seats, and everyone may get ready.
	registration.ValidateSettings, registration.ValidateMemberSettings, registration.ValidateSeat, registration.ReadyGate = nil, nil, nil, nil
	hooks, err := gamehooks.NewHandler(secret, registration)
	if err != nil {
		return nil, err
	}
	router := http.NewServeMux()
	router.Handle(fmt.Sprintf("POST %s/{hook}", mockgame.HooksPath), hooks)
	router.Handle("/", game)
	return router, nil
}
