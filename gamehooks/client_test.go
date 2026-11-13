package gamehooks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/gamehooks"
)

// recordingGame is a game written in Go whose hooks record what they got.
type recordingGame struct {
	settingsLobby string
	teams         []domain.TeamDefinition
	members       []domain.ReadyMember
	atStart       bool
	started       domain.Snapshot
}

func (game *recordingGame) registration() domain.GameRegistration {
	return domain.GameRegistration{
		Key: "example-game",
		ValidateSettings: func(_ context.Context, lobbyID string, settings json.RawMessage, teams []domain.TeamDefinition) error {
			game.settingsLobby, game.teams = lobbyID, teams
			switch string(settings) {
			case `{"mode":"fast"}`:
				return nil
			case `{"mode":"offline"}`:
				return &domain.Error{Code: domain.CodeUnavailable, Message: "offline"}
			}
			return errors.New("mode must be fast")
		},
		ValidateSeat: func(_ context.Context, _ string, settings json.RawMessage) (string, error) {
			if string(settings) != `{"level":1}` {
				return "", errors.New("level must be 1")
			}
			return "Robot", nil
		},
		ValidateMemberSettings: func(context.Context, string, json.RawMessage) error { return errors.New("no member settings here") },
		ReadyGate: func(context.Context, string) (domain.ReadyCheck, error) {
			return func(members []domain.ReadyMember, atStart bool) error {
				game.members, game.atStart = members, atStart
				for _, member := range members {
					if member.Settings == nil {
						return domain.GameRequirement("urn:example:problem:choice-missing", member.DisplayName+" must choose first")
					}
				}
				return nil
			}, nil
		},
		StartGame: func(_ context.Context, snapshot domain.Snapshot) (string, error) {
			game.started = snapshot
			return "game-1", nil
		},
		GameOver: func(_ context.Context, _ string, round int64) (bool, error) { return round == 1, nil },
	}
}

// serve mounts handler under /game/lobby-hooks/ with http.StripPrefix, as a
// game might, and returns a client of it.
func serve(t *testing.T, handler http.Handler, options ...gamehooks.Option) *gamehooks.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/game/lobby-hooks/", http.StripPrefix("/game/lobby-hooks", handler))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := gamehooks.NewClient("example-game", server.URL+"/game/lobby-hooks/", secret, options...)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestTheClientCallsAGoGameThroughTheHandler(t *testing.T) {
	game := &recordingGame{}
	handler, err := gamehooks.NewHandler(secret, game.registration())
	if err != nil {
		t.Fatal(err)
	}
	client := serve(t, handler)
	registration := client.Registration(domain.StartRules{EveryTeamNeedsMember: true}, domain.LobbyRetention{IdlePeriod: 2 * time.Hour})
	policy := domain.DefaultPolicy(registration)
	ctx := context.Background()
	teams := []domain.TeamDefinition{{Name: "Red"}, {Name: "Blue"}}

	// Settings: accepted, refused with the game's message, and a check that
	// failed.
	if _, err := policy.ValidateGameSettings(ctx, "example-game", "", json.RawMessage(`{"mode":"fast"}`), teams); err != nil {
		t.Fatal(err)
	}
	if game.settingsLobby != "" || len(game.teams) != 2 || game.teams[1].Name != "Blue" {
		t.Fatalf("the game got lobby %q and teams %+v", game.settingsLobby, game.teams)
	}
	_, err = policy.ValidateGameSettings(ctx, "example-game", "00000000-0000-4000-8000-000000000001", json.RawMessage(`{"mode":"slow"}`), teams)
	if domain.ErrorCodeOf(err) != domain.CodeInvalidGameSettings || err.Error() != "mode must be fast" || game.settingsLobby != "00000000-0000-4000-8000-000000000001" {
		t.Fatalf("refused settings = %v (%s)", err, domain.ErrorCodeOf(err))
	}
	if _, err := policy.ValidateGameSettings(ctx, "example-game", "", json.RawMessage(`{"mode":"offline"}`), teams); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("offline settings check = %v", err)
	}

	// Seats and member settings.
	if _, name, err := policy.ValidateSeatSettings(ctx, "example-game", "", json.RawMessage(`{"level":1}`)); err != nil || name != "Robot" {
		t.Fatalf("seat = %q, %v", name, err)
	}
	if _, _, err := policy.ValidateSeatSettings(ctx, "example-game", "", json.RawMessage(`{"level":2}`)); domain.ErrorCodeOf(err) != domain.CodeInvalidSeatSettings || err.Error() != "level must be 1" {
		t.Fatalf("refused seat = %v", err)
	}
	if _, err := policy.ValidateMemberSettings(ctx, "example-game", "", json.RawMessage(`{}`)); domain.ErrorCodeOf(err) != domain.CodeInvalidMemberSettings || err.Error() != "no member settings here" {
		t.Fatalf("refused member settings = %v", err)
	}

	// The ready check passes the game's refusal through unchanged.
	check, err := registration.CheckReady(ctx, "lobby")
	if err != nil {
		t.Fatal(err)
	}
	if err := check([]domain.ReadyMember{{PlayerID: "p1", DisplayName: "Ada", Settings: json.RawMessage(`{"x":1}`)}}, false); err != nil || game.atStart || string(game.members[0].Settings) != `{"x":1}` {
		t.Fatalf("ready = %v, members %+v", err, game.members)
	}
	err = check([]domain.ReadyMember{{PlayerID: "p1", DisplayName: "Ada", Settings: json.RawMessage(`{"x":1}`)}, {PlayerID: "p2", DisplayName: "Grace"}}, true)
	var requirement *domain.Error
	if !errors.As(err, &requirement) || requirement.Code != domain.CodeGameRequirement || requirement.Type != "urn:example:problem:choice-missing" || requirement.Message != "Grace must choose first" || !game.atStart {
		t.Fatalf("refused start = %+v", err)
	}
	if game.members[1].Settings != nil {
		t.Fatalf("absent settings reached the game as %q", game.members[1].Settings)
	}

	// Start and round end.
	snapshot := domain.Snapshot{Lobby: domain.Lobby{ID: "lobby", GameKey: "example-game", Phase: domain.PhaseStarted, Round: 2}, Members: []domain.Member{{PlayerID: "p1"}}}
	if gameID, err := registration.StartGame(ctx, snapshot); err != nil || gameID != "game-1" || game.started.Lobby.Round != 2 || game.started.Members[0].MemberSettings != nil {
		t.Fatalf("start = %q, %v, %+v", gameID, err, game.started)
	}
	if ended, err := registration.CheckGameOver(ctx, "lobby", 1); err != nil || !ended {
		t.Fatalf("round 1 ended = %v, %v", ended, err)
	}
	if ended, err := registration.CheckGameOver(ctx, "lobby", 2); err != nil || ended {
		t.Fatalf("round 2 ended = %v, %v", ended, err)
	}
}

// A game whose registration leaves hooks nil answers as the lobby treats nil
// fields in its own process.
func TestNilHooksAnswerAsTheLobbyTreatsThem(t *testing.T) {
	handler, err := gamehooks.NewHandler(secret, domain.GameRegistration{Key: "example-game"})
	if err != nil {
		t.Fatal(err)
	}
	client := serve(t, handler)
	ctx := context.Background()
	if err := client.ValidateSettings(ctx, "", json.RawMessage(`{}`), nil); err != nil {
		t.Fatalf("empty settings = %v", err)
	}
	if err := client.ValidateSettings(ctx, "", json.RawMessage(`{"a":1}`), nil); err == nil || errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("settings = %v", err)
	}
	if _, err := client.ValidateSeat(ctx, "", json.RawMessage(`{}`)); err == nil || errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("seat = %v", err)
	}
	if err := client.ValidateMemberSettings(ctx, "", json.RawMessage(`{}`)); err == nil || errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("member settings = %v", err)
	}
	if err := client.ReadyCheck(ctx, "", []domain.ReadyMember{{PlayerID: "p"}}, true); err != nil {
		t.Fatalf("ready = %v", err)
	}
	if ended, err := client.RoundEnded(ctx, "", 1); err != nil || ended {
		t.Fatalf("round ended = %v, %v", ended, err)
	}
	if _, err := client.Start(ctx, domain.Snapshot{}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("start = %v", err)
	}
}

// Failures of the call are unavailable: a 5xx, a timeout, an answer the
// protocol doesn't allow, an unreachable game, and the game's refusal of the
// lobby's signature.
func TestFailedCallsAreUnavailable(t *testing.T) {
	answers := map[string]func(http.ResponseWriter){
		"5xx":         func(response http.ResponseWriter) { response.WriteHeader(http.StatusBadGateway) },
		"unknown 4xx": func(response http.ResponseWriter) { response.WriteHeader(http.StatusTeapot) },
		"wrong 2xx":   func(response http.ResponseWriter) { response.WriteHeader(http.StatusAccepted) },
		"409 no code": func(response http.ResponseWriter) {
			gamehooks.WriteProblem(response, gamehooks.Problem{Status: 409, Detail: "x"})
		},
		"bad body": func(response http.ResponseWriter) {
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write([]byte("{"))
		},
		"no game id": func(response http.ResponseWriter) {
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write([]byte(`{}`))
		},
		"slow": func(http.ResponseWriter) { time.Sleep(300 * time.Millisecond) },
		"huge": func(response http.ResponseWriter) {
			_, _ = response.Write(bytes.Repeat([]byte(" "), gamehooks.MaxBodyBytes+1))
		},
		"unauthorized": func(response http.ResponseWriter) { response.WriteHeader(http.StatusUnauthorized) },
	}
	for name, answer := range answers {
		t.Run(name, func(t *testing.T) {
			client := serve(t, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { answer(response) }), gamehooks.WithTimeout(100*time.Millisecond))
			ctx := context.Background()
			if _, err := client.Start(ctx, domain.Snapshot{}); !errors.Is(err, domain.ErrUnavailable) || errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("start = %v", err)
			}
			if err := client.ReadyCheck(ctx, "", nil, false); !errors.Is(err, domain.ErrUnavailable) || errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("ready check = %v", err)
			}
		})
	}
	unreachable, err := gamehooks.NewClient("example-game", "http://127.0.0.1:1/hooks", secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := unreachable.ValidateMemberSettings(context.Background(), "", json.RawMessage(`{}`)); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("unreachable = %v", err)
	}
	// The caller's own cancellation stays the caller's.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := unreachable.ValidateMemberSettings(canceled, "", json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled = %v", err)
	}
}

func TestTheHandlerRefusesRequestsThatAreNotSigned(t *testing.T) {
	handler, err := gamehooks.NewHandler(secret, (&recordingGame{}).registration())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	body := []byte(`{"snapshot":{}}`)
	for name, header := range map[string]string{
		"unsigned":        "",
		"another secret":  gamehooks.Sign([]byte(strings.Repeat("o", 32)), time.Now(), "POST", "/start", body),
		"another hook":    gamehooks.Sign(secret, time.Now(), "POST", "/round-ended", body),
		"ten minutes old": gamehooks.Sign(secret, time.Now().Add(-10*time.Minute), "POST", "/start", body),
	} {
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/start", bytes.NewReader(body))
		if header != "" {
			request.Header.Set(gamehooks.SignatureHeader, header)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d", name, response.StatusCode)
		}
	}
	// A client with another secret gets the 401 as unavailable.
	client, err := gamehooks.NewClient("example-game", server.URL, []byte(strings.Repeat("o", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(context.Background(), domain.Snapshot{}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("start with another secret = %v", err)
	}
}

func TestServiceURLs(t *testing.T) {
	for _, valid := range []string{"https://game.example.com/hooks", "https://game.example.com", "http://127.0.0.1:8080/hooks/", "http://localhost/hooks", "http://[::1]:9/x"} {
		if _, err := gamehooks.ParseServiceURL(valid); err != nil {
			t.Errorf("%s: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "/hooks", "http://game.example.com/hooks", "ftp://game.example.com", "https://game.example.com/hooks?x=1", "https://user@game.example.com", "https://game.example.com/#x"} {
		if _, err := gamehooks.ParseServiceURL(invalid); err == nil {
			t.Errorf("%q was accepted", invalid)
		}
	}
	if _, err := gamehooks.NewClient("example-game", "https://game.example.com", []byte("short")); err == nil {
		t.Error("a short secret was accepted")
	}
	if _, err := gamehooks.NewClient("Example Game", "https://game.example.com", secret); err == nil {
		t.Error("an invalid key was accepted")
	}
}
