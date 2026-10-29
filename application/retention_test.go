package application_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"islandBattles/lobby/application"
	"islandBattles/lobby/domain"
)

// fakeRetentionStore records each pass and returns the queued results.
type fakeRetentionStore struct {
	mutex   sync.Mutex
	passes  []domain.RetentionCutoffs
	results []domain.RetentionResult
	err     error
	// block, when set, holds a pass until it is closed.
	block   chan struct{}
	started chan struct{}
}

func (store *fakeRetentionStore) DeleteExpired(ctx context.Context, cutoffs domain.RetentionCutoffs) (domain.RetentionResult, error) {
	store.mutex.Lock()
	store.passes = append(store.passes, cutoffs)
	var result domain.RetentionResult
	if len(store.results) > 0 {
		result, store.results = store.results[0], store.results[1:]
	}
	block, started, err := store.block, store.started, store.err
	store.mutex.Unlock()
	if started != nil {
		close(started)
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return domain.RetentionResult{}, ctx.Err()
		}
	}
	return result, err
}

func (store *fakeRetentionStore) passCount() int {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return len(store.passes)
}

func newTestRetention(t *testing.T, store domain.RetentionStore, clock domain.Clock) (*application.Retention, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	retention, err := application.NewRetention(store, clock, domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"}), slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return retention, logs
}

func TestRetentionRunsAtMostOncePerInterval(t *testing.T) {
	origin := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	clock := &integrationClock{now: origin}
	store := &fakeRetentionStore{}
	retention, logs := newTestRetention(t, store, clock)

	retention.RunIfDue(context.Background())
	if store.passCount() != 1 || !store.passes[0].CreateRequestsBefore.Equal(origin.Add(-24*time.Hour)) {
		t.Fatalf("first request ran passes %+v", store.passes)
	}
	clock.Set(origin.Add(time.Minute - time.Second))
	retention.RunIfDue(context.Background())
	if store.passCount() != 1 {
		t.Fatal("a pass ran again inside the interval")
	}
	clock.Set(origin.Add(time.Minute))
	retention.RunIfDue(context.Background())
	if store.passCount() != 2 {
		t.Fatal("no pass after the interval")
	}
	if logs.Len() != 0 {
		t.Fatalf("passes that deleted nothing logged %q", logs.String())
	}
}

func TestRetentionRunsAgainWhileABacklogRemains(t *testing.T) {
	origin := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	clock := &integrationClock{now: origin}
	store := &fakeRetentionStore{results: []domain.RetentionResult{{Lobbies: 50}, {Lobbies: 3, Players: 7}}}
	retention, logs := newTestRetention(t, store, clock)

	retention.RunIfDue(context.Background())
	retention.RunIfDue(context.Background())
	retention.RunIfDue(context.Background())
	if store.passCount() != 2 {
		t.Fatalf("passes = %d, want a second pass for the backlog and then a wait", store.passCount())
	}
	if !strings.Contains(logs.String(), "lobbies=50") || !strings.Contains(logs.String(), "players=7") {
		t.Fatalf("log = %q, want what each pass deleted", logs.String())
	}
}

func TestRetentionFailureWaitsForTheIntervalAndIsLogged(t *testing.T) {
	origin := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	clock := &integrationClock{now: origin}
	store := &fakeRetentionStore{err: errors.New("database down")}
	retention, logs := newTestRetention(t, store, clock)

	retention.RunIfDue(context.Background())
	retention.RunIfDue(context.Background())
	if store.passCount() != 1 {
		t.Fatalf("passes = %d, want a failed pass to wait for the interval", store.passCount())
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "database down") {
		t.Fatalf("log = %q, want the failure", logs.String())
	}
}

func TestRetentionRunsOnePassAtATimeAndOutlivesCanceledRequests(t *testing.T) {
	clock := &integrationClock{now: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	store := &fakeRetentionStore{block: make(chan struct{}), started: make(chan struct{}), results: []domain.RetentionResult{{Sessions: 1}}}
	retention, logs := newTestRetention(t, store, clock)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		retention.RunIfDue(canceled)
	}()
	<-store.started
	// Another request skips while the pass runs, instead of waiting for it.
	retention.RunIfDue(context.Background())
	close(store.block)
	<-done
	if store.passCount() != 1 {
		t.Fatalf("passes = %d, want 1", store.passCount())
	}
	if !strings.Contains(logs.String(), "sessions=1") {
		t.Fatalf("log = %q; the canceled request's pass should have finished", logs.String())
	}
}

func TestNewRetentionRequiresDependenciesAndAValidPolicy(t *testing.T) {
	clock := &integrationClock{}
	logger := slog.New(slog.DiscardHandler)
	policy := domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"})
	if _, err := application.NewRetention(nil, clock, policy, logger); err == nil {
		t.Error("nil store accepted")
	}
	if _, err := application.NewRetention(&fakeRetentionStore{}, nil, policy, logger); err == nil {
		t.Error("nil clock accepted")
	}
	if _, err := application.NewRetention(&fakeRetentionStore{}, clock, policy, nil); err == nil {
		t.Error("nil logger accepted")
	}
	policy.Retention.BatchSize = 0
	if _, err := application.NewRetention(&fakeRetentionStore{}, clock, policy, logger); err == nil {
		t.Error("invalid retention accepted")
	}
}
