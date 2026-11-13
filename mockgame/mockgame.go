// Package mockgame is a tiny game that the lobby calls over HTTP, for the
// lobby's own tests. It serves the hooks of the gamehooks protocol from Go
// functions, keeps its games in memory, reads the lobby through the game API,
// and can be told to fail or delay a hook.
//
//   - Its game settings are {"rounds": N}, N from 1 to 5.
//   - Its seats are {"level": 1}, named "Mock bot (level 1)".
//   - Its member settings are {"color": "<1 to 20 characters>"}, and a person
//     needs them to get ready: ready-check refuses a member without them with
//     the problem type ColorMissingProblemType.
//   - start records a game for the lobby's round, once: a second call for the
//     same round returns the same game.
//   - POST /finish/{lobby_id} ends the game of the lobby's newest round, so
//     round-ended answers true and the lobby can be reopened.
//   - GET /watch/{lobby_id}?player_id=… is a watcher's read: it asks the
//     lobby's game API whether the player may watch, and answers with the
//     lobby's game.
//
// A Game is an http.Handler; serve it with httptest.NewServer and configure
// the lobby with hooks_url set to the server's URL + HooksPath.
package mockgame

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/gamehooks"
)

// HooksPath is where a Game serves its hooks.
const HooksPath = "/hooks"

// ColorMissingProblemType is the ready-check's refusal of a member who has
// chosen no color.
const ColorMissingProblemType = "urn:mockgame:problem:color-missing"

// MaxRounds bounds the rounds setting.
const MaxRounds = 5

// SeatName names the mock game's only seat.
const SeatName = "Mock bot (level 1)"

// StartedGame is a game that start recorded for a lobby's round.
type StartedGame struct {
	ID      string
	LobbyID string
	Round   int64
	// Players are the start roster's player IDs, in roster order.
	Players []string
	// Colors are the people's colors by player ID, from their member
	// settings.
	Colors map[string]string
	Ended  bool
}

// Watch is the answer of GET /watch/{lobby_id}.
type Watch struct {
	GameID  string `json:"game_id"`
	LobbyID string `json:"lobby_id"`
	Round   int64  `json:"round"`
	Phase   string `json:"phase"`
}

type roundKey struct {
	lobbyID string
	round   int64
}

type fault struct {
	status    int
	delay     time.Duration
	remaining int
}

// Game is the mock game.
type Game struct {
	key   string
	mux   *http.ServeMux
	mutex sync.Mutex
	games map[roundKey]*StartedGame
	// newest is each lobby's newest round with a game.
	newest map[string]int64
	faults map[string][]*fault
	calls  map[string]int
	lobby  *gamehooks.GameAPIClient
	secret []byte
}

// New returns a mock game with key, whose hooks verify signatures with
// secret.
func New(key string, secret []byte) (*Game, error) {
	if err := domain.ValidateGameKey(key); err != nil {
		return nil, errors.New("invalid game key")
	}
	game := &Game{key: key, secret: bytes.Clone(secret), mux: http.NewServeMux(), games: map[roundKey]*StartedGame{}, newest: map[string]int64{}, faults: map[string][]*fault{}, calls: map[string]int{}}
	hooks, err := gamehooks.NewHandler(secret, game.Registration())
	if err != nil {
		return nil, err
	}
	game.mux.Handle("POST "+HooksPath+"/{hook}", game.injectFaults(hooks))
	game.mux.HandleFunc("POST /finish/{lobby_id}", game.finish)
	game.mux.HandleFunc("GET /watch/{lobby_id}", game.watch)
	return game, nil
}

func (game *Game) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	game.mux.ServeHTTP(response, request)
}

// UseLobby connects the game to the lobby's game API, for GET /watch.
func (game *Game) UseLobby(client *gamehooks.GameAPIClient) {
	game.mutex.Lock()
	defer game.mutex.Unlock()
	game.lobby = client
}

// Fail makes the next times calls of hook answer status, with a problem,
// without running the hook.
func (game *Game) Fail(hook string, status, times int) {
	game.addFault(hook, &fault{status: status, remaining: times})
}

// Delay makes the next times calls of hook wait for delay first, or until
// the call is canceled.
func (game *Game) Delay(hook string, delay time.Duration, times int) {
	game.addFault(hook, &fault{delay: delay, remaining: times})
}

func (game *Game) addFault(hook string, added *fault) {
	game.mutex.Lock()
	defer game.mutex.Unlock()
	game.faults[hook] = append(game.faults[hook], added)
}

// Calls counts the signed calls of hook that reached the game, failed ones
// included.
func (game *Game) Calls(hook string) int {
	game.mutex.Lock()
	defer game.mutex.Unlock()
	return game.calls[hook]
}

// Games returns the recorded games, by lobby and round.
func (game *Game) Games() []StartedGame {
	game.mutex.Lock()
	defer game.mutex.Unlock()
	games := make([]StartedGame, 0, len(game.games))
	for _, started := range game.games {
		copied := *started
		copied.Players = slices.Clone(started.Players)
		games = append(games, copied)
	}
	slices.SortFunc(games, func(first, second StartedGame) int {
		return cmp.Or(strings.Compare(first.LobbyID, second.LobbyID), cmp.Compare(first.Round, second.Round))
	})
	return games
}

// Finish ends the game of the lobby's newest round, and reports whether there
// was one.
func (game *Game) Finish(lobbyID string) bool {
	game.mutex.Lock()
	defer game.mutex.Unlock()
	round, found := game.newest[lobbyID]
	if !found {
		return false
	}
	game.games[roundKey{lobbyID, round}].Ended = true
	return true
}

// injectFaults counts each signed call and applies the hook's next fault.
// The signature is checked first, so an unsigned call reaches no fault.
func (game *Game) injectFaults(hooks http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := gamehooks.ReadSigned(request, game.secret, time.Now())
		if errors.Is(err, gamehooks.ErrBadSignature) {
			gamehooks.WriteProblem(response, gamehooks.Problem{Status: http.StatusUnauthorized, Detail: err.Error(), Code: string(domain.CodeUnauthenticated)})
			return
		}
		if err != nil {
			gamehooks.WriteProblem(response, gamehooks.Problem{Status: http.StatusBadRequest, Detail: "the request body can't be read"})
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		hook := request.PathValue("hook")
		game.mutex.Lock()
		game.calls[hook]++
		var next *fault
		if queued := game.faults[hook]; len(queued) > 0 {
			next = queued[0]
			next.remaining--
			if next.remaining <= 0 {
				game.faults[hook] = queued[1:]
			}
		}
		game.mutex.Unlock()
		if next != nil && next.delay > 0 {
			select {
			case <-time.After(next.delay):
			case <-request.Context().Done():
				return
			}
		}
		if next != nil && next.status != 0 {
			gamehooks.WriteProblem(response, gamehooks.Problem{Status: next.status, Detail: fmt.Sprintf("the mock game failed %s on purpose", hook)})
			return
		}
		hooks.ServeHTTP(response, request)
	})
}

// Registration is the mock game's hooks as Go functions, which NewHandler
// serves. The key is the game's; its start rules and retention are the
// lobby configuration's.
func (game *Game) Registration() domain.GameRegistration {
	return domain.GameRegistration{
		Key:                    game.key,
		ValidateSettings:       validateSettings,
		ValidateSeat:           validateSeat,
		ValidateMemberSettings: validateMemberSettings,
		ReadyGate: func(context.Context, string) (domain.ReadyCheck, error) {
			return readyCheck, nil
		},
		StartGame: game.start,
		GameOver:  game.roundEnded,
	}
}

// decodeExactly decodes settings into a struct with no unknown keys.
func decodeExactly(settings json.RawMessage, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(settings))
	decoder.DisallowUnknownFields()
	return decoder.Decode(into)
}

func validateSettings(_ context.Context, _ string, settings json.RawMessage, _ []domain.TeamDefinition) error {
	var parsed struct {
		Rounds *int `json:"rounds"`
	}
	if err := decodeExactly(settings, &parsed); err != nil || parsed.Rounds == nil || *parsed.Rounds < 1 || *parsed.Rounds > MaxRounds {
		return fmt.Errorf(`game_settings must be {"rounds": N} with N from 1 to %d`, MaxRounds)
	}
	return nil
}

func validateSeat(_ context.Context, _ string, settings json.RawMessage) (string, error) {
	var parsed struct {
		Level *int `json:"level"`
	}
	if err := decodeExactly(settings, &parsed); err != nil || parsed.Level == nil || *parsed.Level != 1 {
		return "", errors.New(`seat_settings must be {"level": 1}`)
	}
	return SeatName, nil
}

// color returns the color in member settings, or false when they don't hold
// one.
func color(settings json.RawMessage) (string, bool) {
	var parsed struct {
		Color *string `json:"color"`
	}
	if len(settings) == 0 || decodeExactly(settings, &parsed) != nil || parsed.Color == nil {
		return "", false
	}
	length := utf8.RuneCountInString(*parsed.Color)
	return *parsed.Color, length >= 1 && length <= 20
}

func validateMemberSettings(_ context.Context, _ string, settings json.RawMessage) error {
	if _, valid := color(settings); !valid {
		return errors.New(`member_settings must be {"color": "<1 to 20 characters>"}`)
	}
	return nil
}

func readyCheck(members []domain.ReadyMember, atStart bool) error {
	var missing []string
	for _, member := range members {
		if _, valid := color(member.Settings); !valid {
			missing = append(missing, member.DisplayName)
		}
	}
	switch {
	case len(missing) == 0:
		return nil
	case atStart:
		return domain.GameRequirement(ColorMissingProblemType, "Every player chooses a color before the start; still missing: "+strings.Join(missing, ", "))
	default:
		return domain.GameRequirement(ColorMissingProblemType, "Choose your color before you get ready.")
	}
}

func (game *Game) start(_ context.Context, snapshot domain.Snapshot) (string, error) {
	if snapshot.Lobby.Phase != domain.PhaseStarted || len(snapshot.StartRoster) == 0 || snapshot.Lobby.GameKey != game.key {
		return "", errors.New("the lobby has not started")
	}
	key := roundKey{snapshot.Lobby.ID, snapshot.Lobby.Round}
	game.mutex.Lock()
	defer game.mutex.Unlock()
	if existing := game.games[key]; existing != nil {
		return existing.ID, nil
	}
	started := &StartedGame{ID: fmt.Sprintf("mock-%s-%d", snapshot.Lobby.ID, snapshot.Lobby.Round), LobbyID: snapshot.Lobby.ID, Round: snapshot.Lobby.Round, Colors: map[string]string{}}
	for _, entry := range snapshot.StartRoster {
		started.Players = append(started.Players, entry.PlayerID)
	}
	for _, member := range snapshot.Members {
		if chosen, valid := color(member.MemberSettings); valid {
			started.Colors[member.PlayerID] = chosen
		}
	}
	game.games[key] = started
	if snapshot.Lobby.Round > game.newest[snapshot.Lobby.ID] {
		game.newest[snapshot.Lobby.ID] = snapshot.Lobby.Round
	}
	return started.ID, nil
}

// roundEnded is true for a finished game, and for a round whose start
// created no game, so the lobby is never stuck.
func (game *Game) roundEnded(_ context.Context, lobbyID string, round int64) (bool, error) {
	game.mutex.Lock()
	defer game.mutex.Unlock()
	started := game.games[roundKey{lobbyID, round}]
	return started == nil || started.Ended, nil
}

func (game *Game) finish(response http.ResponseWriter, request *http.Request) {
	if !game.Finish(request.PathValue("lobby_id")) {
		gamehooks.WriteProblem(response, gamehooks.Problem{Status: http.StatusNotFound, Detail: "the lobby has no game"})
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (game *Game) watch(response http.ResponseWriter, request *http.Request) {
	lobbyID := request.PathValue("lobby_id")
	game.mutex.Lock()
	lobby := game.lobby
	game.mutex.Unlock()
	if lobby == nil {
		gamehooks.WriteProblem(response, gamehooks.Problem{Status: http.StatusServiceUnavailable, Detail: "the game is not connected to a lobby"})
		return
	}
	scope := domain.Scope{GameKey: game.key, LobbyID: lobbyID}
	err := lobby.AuthorizeWatch(request.Context(), domain.Principal{PlayerID: request.URL.Query().Get("player_id")}, scope)
	switch {
	case errors.Is(err, domain.ErrWatcherKicked):
		gamehooks.WriteProblem(response, gamehooks.Problem{Status: http.StatusForbidden, Detail: domain.ErrWatcherKicked.Message, Code: string(domain.CodeWatcherKicked)})
		return
	case errors.Is(err, domain.ErrNotFound):
		gamehooks.WriteProblem(response, gamehooks.Problem{Status: http.StatusForbidden, Detail: "you may not watch this lobby", Code: string(domain.CodePermissionDenied)})
		return
	case err != nil:
		gamehooks.WriteProblem(response, gamehooks.Problem{Status: http.StatusServiceUnavailable, Detail: err.Error(), Code: string(domain.CodeUnavailable)})
		return
	}
	snapshot, err := lobby.Snapshot(request.Context(), lobbyID)
	if err != nil {
		gamehooks.WriteProblem(response, gamehooks.Problem{Status: http.StatusServiceUnavailable, Detail: err.Error(), Code: string(domain.CodeUnavailable)})
		return
	}
	game.mutex.Lock()
	watched := Watch{LobbyID: lobbyID, Round: snapshot.Lobby.Round, Phase: string(snapshot.Lobby.Phase)}
	if round, found := game.newest[lobbyID]; found {
		watched.GameID = game.games[roundKey{lobbyID, round}].ID
	}
	game.mutex.Unlock()
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(watched)
}
