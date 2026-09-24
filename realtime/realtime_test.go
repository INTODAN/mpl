package realtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/centrifugal/gocent/v3"
	"islandBattles/lobby/domain"
	"islandBattles/lobby/realtime"
)

const actor = "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"
const other = "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb"
const room = "cccccccc-cccc-4ccc-cccc-cccccccccccc"
const channel = "lobby:island-battles:" + room

type fixedClock struct{ now time.Time }

func (clock *fixedClock) Now() time.Time { return clock.now }

type fakeAuthorizer struct {
	calls int
	err   error
	until time.Time
}

func (authorizer *fakeAuthorizer) AuthorizeSubscription(context.Context, string, string) (domain.SubscriptionAuthorization, error) {
	authorizer.calls++
	return domain.SubscriptionAuthorization{AllowedUntil: authorizer.until}, authorizer.err
}

func TestProxyWireEnvelopesAndTrustedSecret(t *testing.T) {
	for _, test := range []struct {
		name, path, secret string
		failure            error
		status             int
		body               string
	}{
		{"subscribe allow", realtime.SubscribePath, "secret", nil, 200, `{"result":{"expire_at":12345}}`},
		{"refresh allow", realtime.RefreshPath, "secret", nil, 200, `{"result":{"expire_at":12345}}`},
		{"subscribe deny", realtime.SubscribePath, "secret", domain.ErrPermissionDenied, 200, `{"error":{"code":403,"message":"permission denied"}}`},
		{"refresh deny", realtime.RefreshPath, "secret", domain.ErrUnauthenticated, 200, `{"result":{"expired":true}}`},
		{"transient", realtime.RefreshPath, "secret", domain.ErrUnavailable, 503, "temporarily unavailable"},
		{"forged", realtime.SubscribePath, "wrong", nil, 401, "unauthorized"},
	} {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &fakeAuthorizer{err: test.failure, until: time.Unix(12345, 0)}
			proxy, err := realtime.NewProxy(authorizer, "secret")
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(`{"client":"client","user":"`+actor+`","channel":"`+channel+`","data":{"user":"forged"}}`))
			request.Header.Set(realtime.ProxySecretHeader, test.secret)
			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, request)
			if response.Code != test.status || strings.TrimSpace(response.Body.String()) != test.body {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
			if test.secret != "secret" && authorizer.calls != 0 {
				t.Fatal("untrusted identity reached authorizer")
			}
		})
	}
}

func TestProxyRejectsMalformedAndOversizedBody(t *testing.T) {
	for _, body := range []string{`{}`, `{"user":"x"}`, `{"client":"c","user":"u","channel":"x"} {}`, strings.Repeat("a", 70<<10)} {
		authorizer := &fakeAuthorizer{}
		proxy, _ := realtime.NewProxy(authorizer, "secret")
		request := httptest.NewRequest(http.MethodPost, realtime.SubscribePath, strings.NewReader(body))
		request.Header.Set(realtime.ProxySecretHeader, "secret")
		response := httptest.NewRecorder()
		proxy.ServeHTTP(response, request)
		if response.Code != 400 || authorizer.calls != 0 {
			t.Fatal("malformed proxy input authorized")
		}
	}
}

type identityStore struct {
	domain.IdentityStore
	identity *domain.SessionVerification
	err      error
}

func (store identityStore) LookupByPlayer(context.Context, string) (*domain.SessionVerification, error) {
	return store.identity, store.err
}

type aggregateStore struct {
	domain.Store
	aggregate domain.Aggregate
	err       error
}

func (store *aggregateStore) Transact(_ context.Context, scope domain.Scope, mutation domain.Mutation) (domain.Aggregate, error) {
	if store.err != nil {
		return domain.Aggregate{}, store.err
	}
	if scope != store.aggregate.Lobby.Scope() {
		return domain.Aggregate{}, domain.ErrNotFound
	}
	candidate := store.aggregate.DeepCopy()
	change, err := mutation(&candidate)
	if err != nil {
		return domain.Aggregate{}, err
	}
	if change.PublicChanged {
		candidate.Lobby.Version++
	}
	store.aggregate = candidate
	return candidate.DeepCopy(), nil
}

func TestRoomAndPrivateAuthorizationIsolation(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	clock := &fixedClock{now}
	store := &aggregateStore{aggregate: domain.Aggregate{Lobby: domain.Lobby{ID: room, GameKey: "island-battles", Phase: domain.PhaseWaiting, HostPlayerID: ptr(actor), Version: 1}, Members: []domain.Member{{ID: actor, PlayerID: actor, Active: true, Generation: 1, JoinSequence: 1}}, Leases: []domain.Lease{{PlayerID: actor, Generation: 1, ExpiresAt: now.Add(time.Minute)}}}}
	identity := &domain.SessionVerification{Principal: domain.Principal{PlayerID: actor}, ExpiresAt: now.Add(30 * time.Second)}
	authorizer, _ := realtime.NewAuthorizer(identityStore{identity: identity}, store, clock, domain.DefaultPolicy("island-battles", "quiz-demo"))
	for _, name := range []string{channel, "player:" + actor} {
		allowed, err := authorizer.AuthorizeSubscription(context.Background(), actor, name)
		if err != nil || !allowed.AllowedUntil.Equal(identity.ExpiresAt) {
			t.Fatalf("allow/clamp: %+v %v", allowed, err)
		}
	}
	for _, name := range []string{"player:" + other, "lobby:quiz-demo:" + room, channel + ":extra", "lobby:island-battles:" + strings.ToUpper(room), "ping"} {
		if _, err := authorizer.AuthorizeSubscription(context.Background(), actor, name); err == nil {
			t.Fatalf("authorized wrong channel %s", name)
		}
	}
	identity.ExpiresAt = now.Add(time.Hour)
	clock.now = now.Add(2 * time.Minute)
	if _, err := authorizer.AuthorizeSubscription(context.Background(), actor, channel); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("overdue member allowed: %v", err)
	}
	if store.aggregate.Members[0].Active || store.aggregate.Lobby.Version != 2 {
		t.Fatal("denied authorization rolled back cleanup")
	}
	clock.now = identity.ExpiresAt
	if _, err := authorizer.AuthorizeSubscription(context.Background(), actor, "player:"+actor); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatal("expired identity allowed")
	}
}

func ptr(value string) *string { return &value }

func TestTransportPayloadAndUnsubscribeScope(t *testing.T) {
	var command map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(response, `{"result":{}}`)
	}))
	defer server.Close()
	transport, _ := realtime.NewTransport(gocent.New(gocent.Config{Addr: server.URL, Key: "server-only"}))
	event := domain.Invalidation{SchemaVersion: 1, EventID: other, GameKey: "island-battles", LobbyID: room, Version: 42, Type: "lobby.changed"}
	if err := transport.PublishInvalidation(context.Background(), channel, event); err != nil {
		t.Fatal(err)
	}
	var parameters struct {
		Channel string         `json:"channel"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(command["params"], &parameters); err != nil {
		t.Fatal(err)
	}
	if parameters.Channel != channel || len(parameters.Data) != 6 || parameters.Data["version"] != float64(42) {
		t.Fatalf("wrong publication: %+v", parameters)
	}
	if err := transport.Unsubscribe(context.Background(), actor, channel); err != nil {
		t.Fatal(err)
	}
	var revoke map[string]any
	if err := json.Unmarshal(command["params"], &revoke); err != nil {
		t.Fatal(err)
	}
	if string(command["method"]) != `"unsubscribe"` || revoke["user"] != actor || revoke["channel"] != channel {
		t.Fatalf("incorrect revoke: %s", command)
	}
	if _, present := revoke["client"]; present {
		t.Fatal("socket-specific revocation")
	}
	if err := transport.PublishInvalidation(context.Background(), "player:"+actor, event); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("wrong publication scope accepted")
	}
}

func TestTransportProtocolErrorsAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(response, `{"error":{"code":100,"message":"temporary"}}`)
	}))
	defer server.Close()
	transport, _ := realtime.NewTransport(gocent.New(gocent.Config{Addr: server.URL}))
	if err := transport.Unsubscribe(context.Background(), actor, channel); err == nil {
		t.Fatal("HTTP200 protocol error swallowed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := transport.Unsubscribe(ctx, actor, channel); err == nil {
		t.Fatal("cancellation swallowed")
	}
}
