package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/internal/testutil"
	"islandBattles/lobby/postgres"

	"gorm.io/gorm"
)

type storeClock struct{ now time.Time }

func (clock storeClock) Now() time.Time { return clock.now }

type storeFixture struct {
	db        *gorm.DB
	store     *postgres.Store
	clock     storeClock
	principal domain.Principal
	request   domain.CreateRequest
	aggregate domain.Aggregate
	snapshot  domain.Snapshot
}

func storeUUID(t *testing.T) string {
	t.Helper()
	value, err := (domain.CryptoIdentifiers{}).UUID()
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func storePointer[T any](value T) *T { return &value }
func newStoreFixture(t *testing.T) *storeFixture {
	t.Helper()
	db := testutil.NewPostgresFixture(t)
	clock := storeClock{now: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)}
	store, err := postgres.NewStore(db, clock, domain.CryptoIdentifiers{})
	if err != nil {
		t.Fatal(err)
	}
	principal := domain.Principal{PlayerID: storeUUID(t), SessionID: storeUUID(t), DisplayName: "Host"}
	if err := db.Create(&postgres.PlayerRecord{ID: principal.PlayerID, DisplayName: principal.DisplayName, CreatedAt: clock.now}).Error; err != nil {
		t.Fatal(err)
	}
	fixture := &storeFixture{db: db, store: store, clock: clock, principal: principal}
	fixture.request, fixture.aggregate = storeCandidate(t, fixture)
	fixture.snapshot, err = store.Create(context.Background(), principal, fixture.request, fixture.aggregate)
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}
func storeCandidate(t *testing.T, fixture *storeFixture) (domain.CreateRequest, domain.Aggregate) {
	t.Helper()
	id := storeUUID(t)
	code, err := (domain.CryptoIdentifiers{}).JoinCode()
	if err != nil {
		t.Fatal(err)
	}
	request := domain.CreateRequest{RequestID: storeUUID(t), GameKey: "quiz-demo", Name: "A room", Capacity: 4, Teams: []domain.TeamDefinition{{Name: "Red", Capacity: 2}, {Name: "Blue", Capacity: 2}}, ClientInstanceID: storeUUID(t)}
	aggregate := domain.Aggregate{Lobby: domain.Lobby{ID: id, GameKey: request.GameKey, Name: request.Name, JoinCode: code, Capacity: request.Capacity, Phase: domain.PhaseWaiting, Version: 1, HostPlayerID: storePointer(fixture.principal.PlayerID), CreatedAt: fixture.clock.now, NextJoinSequence: 2}, Teams: []domain.Team{{ID: storeUUID(t), LobbyID: id, Name: "Red", Capacity: 2, Order: 1}, {ID: storeUUID(t), LobbyID: id, Name: "Blue", Capacity: 2, Order: 2}}, Members: []domain.Member{{ID: storeUUID(t), PlayerID: fixture.principal.PlayerID, DisplayName: fixture.principal.DisplayName, Generation: 1, Active: true, JoinedAt: fixture.clock.now, JoinSequence: 1}}, Leases: []domain.Lease{{LobbyID: id, PlayerID: fixture.principal.PlayerID, Generation: 1, ClientInstanceID: request.ClientInstanceID, ExpiresAt: fixture.clock.now.Add(45 * time.Second)}}, StartRoster: domain.StartRoster{}}
	return request, aggregate
}
func storeCount(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var count int64
	if err := db.Model(model).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}
func readStore(t *testing.T, fixture *storeFixture) domain.Aggregate {
	t.Helper()
	aggregate, err := fixture.store.Read(context.Background(), fixture.aggregate.Lobby.Scope())
	if err != nil {
		t.Fatal(err)
	}
	return aggregate
}

type readBarrierKey struct{}

func TestStoreReadsConsistentSnapshot(t *testing.T) {
	fixture := newStoreFixture(t)
	loaded := make(chan struct{})
	resume := make(chan struct{})
	var paused atomic.Bool
	callback := "test:pause-room-read"
	if err := fixture.db.Callback().Query().After("gorm:query").Register(callback, func(db *gorm.DB) {
		if db.Statement.Table == "lobby_rooms" && db.Statement.Context.Value(readBarrierKey{}) == true && paused.CompareAndSwap(false, true) {
			close(loaded)
			<-resume
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer fixture.db.Callback().Query().Remove(callback)
	type readResult struct {
		aggregate domain.Aggregate
		err       error
	}
	done := make(chan readResult, 1)
	go func() {
		aggregate, err := fixture.store.Read(context.WithValue(context.Background(), readBarrierKey{}, true), fixture.aggregate.Lobby.Scope())
		done <- readResult{aggregate, err}
	}()
	<-loaded
	_, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Members[0].Ready = true
		return domain.Change{PublicChanged: true}, nil
	})
	close(resume)
	result := <-done
	if err != nil || result.err != nil {
		t.Fatal(err, result.err)
	}
	if result.aggregate.Lobby.Version != 1 || result.aggregate.Members[0].Ready {
		t.Fatal("torn parent/child snapshot", result.aggregate)
	}
	current := readStore(t, fixture)
	if current.Lobby.Version != 2 || !current.Members[0].Ready {
		t.Fatal("concurrent mutation did not commit")
	}
}
func TestStoreScopeMismatchIsNotFound(t *testing.T) {
	fixture := newStoreFixture(t)
	scope := fixture.aggregate.Lobby.Scope()
	scope.GameKey = "other-game"
	if _, err := fixture.store.Read(context.Background(), scope); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	called := false
	if _, err := fixture.store.Transact(context.Background(), scope, func(*domain.Aggregate) (domain.Change, error) { called = true; return domain.Change{}, nil }); !errors.Is(err, domain.ErrNotFound) || called {
		t.Fatal("wrong scope callback", err)
	}
	if _, err := fixture.store.ResolveCode(context.Background(), "other-game", fixture.aggregate.Lobby.JoinCode); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestStoreSerializesConcurrentMutations(t *testing.T) {
	fixture := newStoreFixture(t)
	barrier := make(chan struct{})
	results := make(chan int64, 2)
	failures := make(chan error, 2)
	for range 2 {
		go func() {
			<-barrier
			result, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
				aggregate.Lobby.NextJoinSequence++
				return domain.Change{PublicChanged: true}, nil
			})
			results <- result.Lobby.Version
			failures <- err
		}()
	}
	close(barrier)
	versions := map[int64]bool{}
	for range 2 {
		versions[<-results] = true
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	if !versions[2] || !versions[3] || readStore(t, fixture).Lobby.NextJoinSequence != 4 {
		t.Fatal("lost serialized update", versions)
	}
}
func TestStoreInvokesCallbackOnce(t *testing.T) {
	fixture := newStoreFixture(t)
	calls := 0
	_, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		calls++
		aggregate.Lobby.Name = "discarded"
		return domain.Change{PublicChanged: true}, domain.ErrNotReady
	})
	if calls != 1 || !errors.Is(err, domain.ErrNotReady) || readStore(t, fixture).Lobby.Name != "A room" {
		t.Fatal("callback repeated or committed", calls, err)
	}
}

type failingStoreIDs struct{ domain.CryptoIdentifiers }

func (failingStoreIDs) UUID() (string, error) { return "", errors.New("injected event ID failure") }
func TestStoreRollsBackAggregateAndOutbox(t *testing.T) {
	fixture := newStoreFixture(t)
	store, err := postgres.NewStore(fixture.db, fixture.clock, failingStoreIDs{})
	if err != nil {
		t.Fatal(err)
	}
	before := readStore(t, fixture)
	_, err = store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Lobby.Name = "discarded"
		aggregate.Members[0].Ready = true
		aggregate.Leases[0].ExpiresAt = fixture.clock.now.Add(time.Hour)
		return domain.Change{PublicChanged: true}, nil
	})
	if !errors.Is(err, domain.ErrUnavailable) || !reflect.DeepEqual(before, readStore(t, fixture)) || storeCount(t, fixture.db, &postgres.OutboxRecord{}) != 1 {
		t.Fatal("failed event did not roll back", err)
	}
}
func TestStorePersistsFalseZeroNull(t *testing.T) {
	fixture := newStoreFixture(t)
	_, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Members[0].Ready = true
		aggregate.Members[0].TeamID = &aggregate.Teams[0].ID
		aggregate.Members[0].DisconnectedUntil = storePointer(fixture.clock.now)
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Members[0].Ready = false
		aggregate.Members[0].TeamID = nil
		aggregate.Members[0].DisconnectedUntil = nil
		aggregate.Lobby.HostPlayerID = nil
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	current := readStore(t, fixture)
	if current.Members[0].Ready || current.Members[0].TeamID != nil || current.Members[0].DisconnectedUntil != nil || current.Lobby.HostPlayerID != nil {
		t.Fatal("zero fields omitted")
	}
}
func TestStoreLeaseOnlyMutationDoesNotInvalidate(t *testing.T) {
	fixture := newStoreFixture(t)
	deadline := fixture.clock.now.Add(time.Minute)
	current, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Leases[0].ExpiresAt = deadline
		return domain.Change{PrivateChanged: true}, nil
	})
	if err != nil || current.Lobby.Version != 1 || !readStore(t, fixture).Leases[0].ExpiresAt.Equal(deadline) || storeCount(t, fixture.db, &postgres.OutboxRecord{}) != 1 {
		t.Fatal("private mutation invalidated", err)
	}
}
func TestStorePublicMutationWritesOneInvalidation(t *testing.T) {
	fixture := newStoreFixture(t)
	current, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Lobby.Name = "Changed"
		aggregate.Members[0].Ready = true
		return domain.Change{PublicChanged: true, PrivateChanged: true}, nil
	})
	if err != nil || current.Lobby.Version != 2 || storeCount(t, fixture.db, &postgres.OutboxRecord{}) != 2 {
		t.Fatal("wrong version/events", err)
	}
	jobs, err := fixture.store.Claim(context.Background(), domain.ClaimRequest{Now: fixture.clock.now, Limit: 10, ClaimDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		payload, err := json.Marshal(job.Invalidation)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(payload, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 6 || fields["type"] != "lobby.changed" || fields["schema_version"] != float64(1) {
			t.Fatal("invalid event schema", string(payload))
		}
	}
}
func TestStoreMultipleRevocationsAreAtomic(t *testing.T) {
	fixture := newStoreFixture(t)
	player := storeUUID(t)
	if err := fixture.db.Create(&postgres.PlayerRecord{ID: player, DisplayName: "Guest", CreatedAt: fixture.clock.now}).Error; err != nil {
		t.Fatal(err)
	}
	_, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Members = append(aggregate.Members, domain.Member{ID: storeUUID(t), PlayerID: player, DisplayName: "Guest", Generation: 1, Active: true, JoinedAt: fixture.clock.now, JoinSequence: 2})
		aggregate.Lobby.NextJoinSequence = 3
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		change := domain.Change{PublicChanged: true}
		for index := range aggregate.Members {
			member := &aggregate.Members[index]
			member.Active = false
			change.Revocations = append(change.Revocations, domain.RevocationIntent{PlayerID: member.PlayerID, Scope: aggregate.Lobby.Scope(), Generation: member.Generation})
		}
		aggregate.Lobby.HostPlayerID = nil
		return change, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var records []postgres.OutboxRecord
	if err := fixture.db.Where(&postgres.OutboxRecord{Kind: string(domain.JobRevocation)}).Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || storeCount(t, fixture.db, &postgres.OutboxRecord{}) != 5 {
		t.Fatal("missing atomic revocations")
	}
	for _, member := range readStore(t, fixture).Members {
		if member.Active {
			t.Fatal("member not deactivated")
		}
	}
}
func TestCreateRequestConcurrentReplay(t *testing.T) {
	fixture := newStoreFixture(t)
	request, aggregate := storeCandidate(t, fixture)
	barrier := make(chan struct{})
	results := make(chan domain.Snapshot, 2)
	failures := make(chan error, 2)
	for range 2 {
		go func() {
			<-barrier
			response, err := fixture.store.Create(context.Background(), fixture.principal, request, aggregate)
			results <- response
			failures <- err
		}()
	}
	close(barrier)
	first := <-results
	second := <-results
	for range 2 {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(first, second) || storeCount(t, fixture.db, &postgres.RoomRecord{}) != 2 || storeCount(t, fixture.db, &postgres.OutboxRecord{}) != 2 {
		t.Fatal("replay duplicated creation or response differs", first, second)
	}
}
func TestCreateRequestPayloadConflict(t *testing.T) {
	fixture := newStoreFixture(t)
	for _, change := range []func(*domain.CreateRequest){func(request *domain.CreateRequest) { request.ClientInstanceID = storeUUID(t) }, func(request *domain.CreateRequest) {
		request.Teams = append([]domain.TeamDefinition{}, request.Teams...)
		request.Teams[0].Name = "Changed"
	}} {
		request := fixture.request
		change(&request)
		if _, err := fixture.store.Create(context.Background(), fixture.principal, request, fixture.aggregate); !errors.Is(err, domain.ErrIdempotencyConflict) {
			t.Fatal(err)
		}
	}
	if storeCount(t, fixture.db, &postgres.RoomRecord{}) != 1 {
		t.Fatal("conflict created room")
	}
}
func TestCreateReplayUsesOriginalResponse(t *testing.T) {
	fixture := newStoreFixture(t)
	_, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Lobby.Name = "Changed"
		aggregate.Members[0].Active = false
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := fixture.store.Create(context.Background(), fixture.principal, fixture.request, fixture.aggregate)
	if err != nil || !reflect.DeepEqual(replay, fixture.snapshot) || replay.Lobby.Version != 1 {
		t.Fatal("replay leaked current room", err, replay, fixture.snapshot)
	}
}
func TestCreateCodeCollisionRollsBack(t *testing.T) {
	fixture := newStoreFixture(t)
	request, aggregate := storeCandidate(t, fixture)
	aggregate.Lobby.JoinCode = fixture.aggregate.Lobby.JoinCode
	if _, err := fixture.store.Create(context.Background(), fixture.principal, request, aggregate); !errors.Is(err, domain.ErrJoinCodeCollision) {
		t.Fatal(err)
	}
	if storeCount(t, fixture.db, &postgres.RoomRecord{}) != 1 || storeCount(t, fixture.db, &postgres.TeamRecord{}) != 2 || storeCount(t, fixture.db, &postgres.CreateRequestRecord{}) != 1 {
		t.Fatal("collision left orphan records")
	}
	aggregate.Lobby.JoinCode = "23456789"
	if _, err := fixture.store.Create(context.Background(), fixture.principal, request, aggregate); err != nil {
		t.Fatal("fresh attempt failed", err)
	}
}
func TestDueScopesUsesCurrentGenerationEffectiveExpiry(t *testing.T) {
	fixture := newStoreFixture(t)
	now := fixture.clock.now
	_, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Members[0].Generation = 2
		aggregate.Leases[0].ExpiresAt = now.Add(time.Hour)
		aggregate.Leases = append(aggregate.Leases, domain.Lease{LobbyID: aggregate.Lobby.ID, PlayerID: fixture.principal.PlayerID, Generation: 2, ClientInstanceID: storeUUID(t), ExpiresAt: now.Add(-time.Minute), ReleasedAt: storePointer(now.Add(time.Hour))})
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	scopes, err := fixture.store.DueScopes(context.Background(), now, 1)
	if err != nil || len(scopes) != 1 || scopes[0] != fixture.aggregate.Lobby.Scope() {
		t.Fatal("old generation or late release concealed due room", scopes, err)
	}
	_, err = fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Members[0].DisconnectedUntil = storePointer(now.Add(time.Minute))
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	scopes, err = fixture.store.DueScopes(context.Background(), now, 1)
	if err != nil || len(scopes) != 0 {
		t.Fatal("grace room returned before deadline", scopes, err)
	}
	scopes, err = fixture.store.DueScopes(context.Background(), now.Add(time.Minute), 1)
	if err != nil || len(scopes) != 1 {
		t.Fatal("exact grace deadline missing", scopes, err)
	}
}
func TestDueScopesRotatesFailedCandidates(t *testing.T) {
	fixture := newStoreFixture(t)
	request, aggregate := storeCandidate(t, fixture)
	if _, err := fixture.store.Create(context.Background(), fixture.principal, request, aggregate); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for range 2 {
		scopes, err := fixture.store.DueScopes(context.Background(), fixture.clock.now.Add(time.Hour), 1)
		if err != nil || len(scopes) != 1 {
			t.Fatal(scopes, err)
		}
		seen[scopes[0].LobbyID] = true
	}
	if len(seen) != 2 {
		t.Fatal("failed first scope starved second")
	}
}
