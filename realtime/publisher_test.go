package realtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centrifugal/gocent/v3"
	"islandBattles/lobby/domain"
	"islandBattles/lobby/realtime"
)

type apiCommand struct {
	Method string `json:"method"`
	Params struct {
		Channel        string          `json:"channel"`
		Data           json.RawMessage `json:"data"`
		User           string          `json:"user"`
		Client         string          `json:"client"`
		IdempotencyKey string          `json:"idempotency_key"`
	} `json:"params"`
}

// centrifugoAPI records each API request's commands and answers with the
// replies that reply returns for that request.
type centrifugoAPI struct {
	mutex    sync.Mutex
	requests [][]apiCommand
	reply    func(request int, commands []apiCommand) []string
}

func (api *centrifugoAPI) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	var commands []apiCommand
	decoder := json.NewDecoder(request.Body)
	for {
		var command apiCommand
		if err := decoder.Decode(&command); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		commands = append(commands, command)
	}
	api.mutex.Lock()
	api.requests = append(api.requests, commands)
	number := len(api.requests)
	api.mutex.Unlock()
	replies := api.reply(number, commands)
	if replies == nil {
		<-request.Context().Done()
		return
	}
	_, _ = io.WriteString(response, strings.Join(replies, "\n"))
}

func (api *centrifugoAPI) recorded() [][]apiCommand {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	return append([][]apiCommand{}, api.requests...)
}

func succeed(_ int, commands []apiCommand) []string {
	replies := make([]string, len(commands))
	for index := range replies {
		replies[index] = `{"result":{}}`
	}
	return replies
}

func newPublisher(t *testing.T, api *centrifugoAPI) (*realtime.Publisher, *bytes.Buffer) {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	var logs bytes.Buffer
	publisher, err := realtime.NewPublisher(gocent.New(gocent.Config{Addr: server.URL, Key: "server-only"}), slog.New(slog.NewTextHandler(&logs, nil)), realtime.PushRetry{Attempts: 3, AttemptTimeout: 200 * time.Millisecond, Pause: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return publisher, &logs
}

func TestPublishChangeSendsInvalidationAndRevocationsInOneRequest(t *testing.T) {
	api := &centrifugoAPI{reply: succeed}
	publisher, logs := newPublisher(t, api)
	lobby := domain.Lobby{ID: room, GameKey: "island-battles", Version: 42}
	change := domain.Change{PublicChanged: true, Revocations: []domain.RevocationIntent{{PlayerID: actor, Scope: lobby.Scope(), Generation: 1}}}
	if err := publisher.PublishChange(context.Background(), lobby, change, alwaysDue); err != nil {
		t.Fatal(err)
	}
	requests := api.recorded()
	if len(requests) != 1 || len(requests[0]) != 2 {
		t.Fatalf("want one request with two commands, got %+v", requests)
	}
	publication, revocation := requests[0][0], requests[0][1]
	var event map[string]any
	if err := json.Unmarshal(publication.Params.Data, &event); err != nil {
		t.Fatal(err)
	}
	if publication.Method != "publish" || publication.Params.Channel != channel || len(event) != 6 || event["type"] != "lobby.changed" || event["schema_version"] != float64(1) || event["version"] != float64(42) || event["lobby_id"] != room || event["event_id"] != publication.Params.IdempotencyKey {
		t.Fatalf("wrong invalidation: %+v %s", publication, publication.Params.Data)
	}
	if revocation.Method != "unsubscribe" || revocation.Params.User != actor || revocation.Params.Channel != channel || revocation.Params.Client != "" {
		t.Fatalf("revocation must unsubscribe every device of the user: %+v", revocation)
	}
	if logs.Len() != 0 {
		t.Fatalf("successful push logged: %s", logs)
	}
}

func TestPublishChangeRejectsRevocationForAnotherRoom(t *testing.T) {
	api := &centrifugoAPI{reply: succeed}
	publisher, logs := newPublisher(t, api)
	lobby := domain.Lobby{ID: room, GameKey: "island-battles", Version: 2}
	elsewhere := domain.Scope{GameKey: "quiz-demo", LobbyID: room}
	err := publisher.PublishChange(context.Background(), lobby, domain.Change{Revocations: []domain.RevocationIntent{{PlayerID: actor, Scope: elsewhere, Generation: 1}}}, alwaysDue)
	if !errors.Is(err, domain.ErrInvalidInput) || len(api.recorded()) != 0 || !strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("wrong-scope revocation was sent or not logged: %v", err)
	}
}

func alwaysDue(context.Context, domain.RevocationIntent) (bool, error) { return true, nil }

func TestPublishChangeRechecksAnUnsubscribeBeforeEachAttempt(t *testing.T) {
	api := &centrifugoAPI{reply: func(request int, commands []apiCommand) []string {
		if request == 1 {
			return []string{`{"error":{"code":100,"message":"internal server error"}}`, `{"error":{"code":100,"message":"internal server error"}}`}
		}
		return succeed(request, commands)
	}}
	publisher, logs := newPublisher(t, api)
	lobby := domain.Lobby{ID: room, GameKey: "island-battles", Version: 3}
	change := domain.Change{PublicChanged: true, Revocations: []domain.RevocationIntent{{PlayerID: actor, Scope: lobby.Scope(), Generation: 1}}}
	// The player rejoins while the first attempt fails, so the retry must
	// leave their new subscription alone.
	checks := 0
	rejoined := func(context.Context, domain.RevocationIntent) (bool, error) {
		checks++
		return checks == 1, nil
	}
	if err := publisher.PublishChange(context.Background(), lobby, change, rejoined); err != nil {
		t.Fatal(err)
	}
	requests := api.recorded()
	if checks != 2 || len(requests) != 2 || len(requests[0]) != 2 || len(requests[1]) != 1 || requests[1][0].Method != "publish" {
		t.Fatalf("want the unsubscribe rechecked and dropped, got %d checks and %+v", checks, requests)
	}
	if logs.Len() != 0 {
		t.Fatalf("recovered push logged: %s", logs)
	}
}

func TestPublishChangeRetriesAFailedRecheck(t *testing.T) {
	api := &centrifugoAPI{reply: succeed}
	publisher, _ := newPublisher(t, api)
	lobby := domain.Lobby{ID: room, GameKey: "island-battles", Version: 3}
	change := domain.Change{Revocations: []domain.RevocationIntent{{PlayerID: actor, Scope: lobby.Scope(), Generation: 1}}}
	checks := 0
	unavailable := func(context.Context, domain.RevocationIntent) (bool, error) {
		checks++
		if checks == 1 {
			return false, domain.ErrUnavailable
		}
		return true, nil
	}
	if err := publisher.PublishChange(context.Background(), lobby, change, unavailable); err != nil {
		t.Fatal(err)
	}
	if requests := api.recorded(); checks != 2 || len(requests) != 1 || requests[0][0].Method != "unsubscribe" {
		t.Fatalf("want the unsubscribe sent after a failed recheck, got %d checks and %+v", checks, requests)
	}
	if err := publisher.PublishChange(context.Background(), lobby, change, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("missing recheck accepted: %v", err)
	}
}

func TestPublishRetriesOnlyFailedCommandsWithTheSameKey(t *testing.T) {
	api := &centrifugoAPI{reply: func(request int, commands []apiCommand) []string {
		if request == 1 {
			return []string{`{"result":{}}`, `{"error":{"code":100,"message":"internal server error"}}`}
		}
		return succeed(request, commands)
	}}
	publisher, logs := newPublisher(t, api)
	first := realtime.Publication{Channel: "player:" + actor, Data: []byte(`{"n":1}`)}
	second := realtime.Publication{Channel: "player:" + other, Data: []byte(`{"n":2}`)}
	if err := publisher.Publish(context.Background(), first, second); err != nil {
		t.Fatal(err)
	}
	requests := api.recorded()
	if len(requests) != 2 || len(requests[0]) != 2 || len(requests[1]) != 1 {
		t.Fatalf("want the failed publication retried alone, got %+v", requests)
	}
	retried := requests[1][0]
	if retried.Params.Channel != second.Channel || string(retried.Params.Data) != string(second.Data) || retried.Params.IdempotencyKey == "" || retried.Params.IdempotencyKey != requests[0][1].Params.IdempotencyKey || requests[0][0].Params.IdempotencyKey == retried.Params.IdempotencyKey {
		t.Fatalf("retry changed the publication or its key: %+v", requests)
	}
	if logs.Len() != 0 {
		t.Fatalf("recovered push logged: %s", logs)
	}
}

func TestPublishGivesUpQuicklyAndLogs(t *testing.T) {
	for _, test := range []struct {
		name  string
		reply func(int, []apiCommand) []string
	}{
		{"protocol error", func(int, []apiCommand) []string {
			return []string{`{"error":{"code":100,"message":"internal server error"}}`}
		}},
		{"no reply", func(int, []apiCommand) []string { return nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := &centrifugoAPI{reply: test.reply}
			publisher, logs := newPublisher(t, api)
			// The caller hanging up must not stop a push for a committed change.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			started := time.Now()
			err := publisher.Publish(ctx, realtime.Publication{Channel: "player:" + actor, Data: []byte(`{}`)})
			if err == nil || len(api.recorded()) != 3 || time.Since(started) > 2*time.Second {
				t.Fatalf("want three bounded attempts and an error, got %d attempts, %v, %v", len(api.recorded()), time.Since(started), err)
			}
			if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "player:"+actor) || strings.Contains(logs.String(), "server-only") {
				t.Fatalf("failure not logged safely: %s", logs)
			}
		})
	}
}

func TestPublisherRequiresBoundedRetry(t *testing.T) {
	client := gocent.New(gocent.Config{Addr: "http://127.0.0.1:1/api"})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, retry := range []realtime.PushRetry{{}, {Attempts: 1}, {Attempts: 1, AttemptTimeout: time.Second, Pause: -time.Second}} {
		if _, err := realtime.NewPublisher(client, logger, retry); err == nil {
			t.Fatalf("accepted %+v", retry)
		}
	}
	if _, err := realtime.NewPublisher(client, logger, realtime.DefaultPushRetry()); err != nil {
		t.Fatal(err)
	}
}
