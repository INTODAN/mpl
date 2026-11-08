package realtime_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// A 503 leaves its cause out of the answer, so the host gets it to log.
// Denials are answers, not failures, and report nothing.
func TestProxyReportsTheCauseOfEach503(t *testing.T) {
	for _, test := range []struct {
		failure  error
		reported bool
	}{{errors.Join(domain.ErrUnavailable, errors.New("connection refused")), true}, {domain.ErrPermissionDenied, false}, {nil, false}} {
		var reported []error
		proxy, err := realtime.NewProxy(&fakeAuthorizer{err: test.failure, until: time.Unix(12345, 0)}, "secret",
			realtime.WithErrorReporter(func(_ context.Context, err error) { reported = append(reported, err) }))
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, realtime.SubscribePath, strings.NewReader(`{"client":"client","user":"user","channel":"channel"}`))
		request.Header.Set(realtime.ProxySecretHeader, "secret")
		proxy.ServeHTTP(httptest.NewRecorder(), request)
		if test.reported != (len(reported) == 1) || test.reported && !strings.Contains(reported[0].Error(), "connection refused") {
			t.Fatalf("failure %v reported %v", test.failure, reported)
		}
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

func (store *aggregateStore) Read(_ context.Context, scope domain.Scope) (domain.Aggregate, error) {
	if store.err != nil {
		return domain.Aggregate{}, store.err
	}
	if scope != store.aggregate.Lobby.Scope() {
		return domain.Aggregate{}, domain.ErrNotFound
	}
	return store.aggregate.DeepCopy(), nil
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
	authorizer, _ := realtime.NewAuthorizer(identityStore{identity: identity}, store, clock, domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"}, domain.GameRegistration{Key: "quiz-demo"}))
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

func TestWatchChannelIsForMembersAndWatchersTheHostHasNotKicked(t *testing.T) {
	const watcher = "dddddddd-dddd-4ddd-dddd-dddddddddddd"
	const kicked = "eeeeeeee-eeee-4eee-eeee-eeeeeeeeeeee"
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	kickedAt := now.Add(-time.Minute)
	store := &aggregateStore{aggregate: domain.Aggregate{
		Lobby:    domain.Lobby{ID: room, GameKey: "island-battles", Phase: domain.PhaseStarted, HostPlayerID: ptr(actor), Version: 1},
		Members:  []domain.Member{{ID: actor, PlayerID: actor, Active: true, Generation: 1, JoinSequence: 1}},
		Leases:   []domain.Lease{{PlayerID: actor, Generation: 1, ExpiresAt: now.Add(time.Minute)}},
		Watchers: []domain.Watcher{{PlayerID: watcher, JoinedAt: now}, {PlayerID: kicked, JoinedAt: now, KickedAt: &kickedAt}},
	}}
	identity := &domain.SessionVerification{ExpiresAt: now.Add(time.Hour)}
	authorizer, _ := realtime.NewAuthorizer(identityStore{identity: identity}, store, &fixedClock{now}, domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"}, domain.GameRegistration{Key: "quiz-demo"}))
	watch := "watch:island-battles:" + room
	authorize := func(playerID, name string) error {
		identity.Principal.PlayerID = playerID
		_, err := authorizer.AuthorizeSubscription(context.Background(), playerID, name)
		return err
	}
	for _, playerID := range []string{actor, watcher} {
		if err := authorize(playerID, watch); err != nil {
			t.Fatalf("watch channel refused %s: %v", playerID, err)
		}
	}
	for _, playerID := range []string{kicked, other} {
		if err := authorize(playerID, watch); !errors.Is(err, domain.ErrPermissionDenied) {
			t.Fatalf("watch channel allowed %s: %v", playerID, err)
		}
	}
	// A watcher never gets the room channel, nor another game's watch channel.
	for _, name := range []string{channel, "watch:quiz-demo:" + room, watch + ":extra", "watch:island-battles:" + strings.ToUpper(room)} {
		if err := authorize(watcher, name); err == nil {
			t.Fatalf("authorized %s for a watcher", name)
		}
	}
}
