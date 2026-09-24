package application_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"islandBattles/lobby/application"
	"islandBattles/lobby/domain"
	"islandBattles/lobby/internal/testutil"
	"islandBattles/lobby/postgres"

	"gorm.io/gorm"
)

type integrationClock struct {
	mutex sync.RWMutex
	now   time.Time
}

func (clock *integrationClock) Now() time.Time {
	clock.mutex.RLock()
	defer clock.mutex.RUnlock()
	return clock.now
}
func (clock *integrationClock) Set(now time.Time) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	clock.now = now
}

type serviceFixture struct {
	db       *gorm.DB
	store    *postgres.Store
	service  *application.Service
	clock    *integrationClock
	policy   domain.Policy
	origin   time.Time
	host     domain.Principal
	clientID string
	request  domain.CreateRequest
	initial  domain.Snapshot
	scope    domain.Scope
}

func integrationID(t *testing.T) string {
	t.Helper()
	id, err := (domain.CryptoIdentifiers{}).UUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func newServiceFixture(t *testing.T, capacity int) *serviceFixture {
	t.Helper()
	fixture := &serviceFixture{db: testutil.NewPostgresFixture(t), origin: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), policy: domain.DefaultPolicy("island-battles", "quiz-demo")}
	fixture.clock = &integrationClock{now: fixture.origin}
	var err error
	fixture.store, err = postgres.NewStore(fixture.db, fixture.clock, domain.CryptoIdentifiers{})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service, err = application.NewService(fixture.store, fixture.clock, domain.CryptoIdentifiers{}, fixture.policy)
	if err != nil {
		t.Fatal(err)
	}
	fixture.host = fixture.player(t, "Host")
	fixture.clientID = integrationID(t)
	fixture.request = domain.CreateRequest{RequestID: integrationID(t), GameKey: "island-battles", Name: "Room", Capacity: capacity, Teams: []domain.TeamDefinition{{Name: "Red", Capacity: 1}, {Name: "Blue", Capacity: capacity}}, ClientInstanceID: fixture.clientID}
	fixture.initial, err = fixture.service.Create(context.Background(), fixture.host, fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	fixture.scope = fixture.initial.Lobby.Scope()
	return fixture
}
func (fixture *serviceFixture) player(t *testing.T, name string) domain.Principal {
	t.Helper()
	principal := domain.Principal{PlayerID: integrationID(t), SessionID: integrationID(t), DisplayName: name}
	if err := fixture.db.Create(&postgres.PlayerRecord{ID: principal.PlayerID, DisplayName: name, CreatedAt: fixture.clock.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	return principal
}
func (fixture *serviceFixture) join(t *testing.T, principal domain.Principal) (domain.Snapshot, string) {
	t.Helper()
	client := integrationID(t)
	snapshot, err := fixture.service.Join(context.Background(), principal, domain.JoinRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode, ClientInstanceID: client})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, client
}
func (fixture *serviceFixture) read(t *testing.T) domain.Aggregate {
	t.Helper()
	aggregate, err := fixture.store.Read(context.Background(), fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	return aggregate
}
func (fixture *serviceFixture) jobs(t *testing.T, kind domain.JobKind) int64 {
	t.Helper()
	var count int64
	query := fixture.db.Model(&postgres.OutboxRecord{}).Where(&postgres.OutboxRecord{LobbyID: &fixture.scope.LobbyID})
	if kind != "" {
		query = query.Where(&postgres.OutboxRecord{Kind: string(kind)})
	}
	if err := query.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}
func (fixture *serviceFixture) assign(t *testing.T, principal domain.Principal, teamIndex int, ready bool) {
	t.Helper()
	team := fixture.initial.Teams[teamIndex].ID
	if _, err := fixture.service.SelectTeam(context.Background(), principal, fixture.scope, domain.SelectTeamRequest{TeamID: &team}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SetReady(context.Background(), principal, fixture.scope, domain.ReadyRequest{Ready: ready}); err != nil {
		t.Fatal(err)
	}
}
func (fixture *serviceFixture) readyPair(t *testing.T) (domain.Principal, string) {
	t.Helper()
	guest := fixture.player(t, "Guest")
	_, client := fixture.join(t, guest)
	fixture.assign(t, fixture.host, 0, true)
	fixture.assign(t, guest, 1, true)
	return guest, client
}
func memberFor(t *testing.T, aggregate domain.Aggregate, playerID string) domain.Member {
	t.Helper()
	member := domain.FindMember(&aggregate, playerID)
	if member == nil {
		t.Fatal("missing member")
	}
	return *member
}
func requireDomainError(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got %v; want %v", err, target)
	}
}
func activeCount(aggregate domain.Aggregate) int {
	count := 0
	for _, member := range aggregate.Members {
		if member.Active {
			count++
		}
	}
	return count
}

func TestCreateTwoIndependentGames(t *testing.T) {
	fixture := newServiceFixture(t, 4)
	request := fixture.request
	request.RequestID = integrationID(t)
	request.GameKey = "quiz-demo"
	request.ClientInstanceID = integrationID(t)
	second, err := fixture.service.Create(context.Background(), fixture.host, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Lobby.GameKey != "quiz-demo" || second.Lobby.ID == fixture.scope.LobbyID || len(second.Members) != 1 || second.Members[0].TeamID != nil || second.Members[0].Ready || second.Lobby.HostPlayerID == nil || *second.Lobby.HostPlayerID != fixture.host.PlayerID {
		t.Fatal("game scope or creator state", second)
	}
	if fixture.db.Migrator().HasTable("games") {
		t.Fatal("generic lobby created backend schema")
	}
}
func TestCreateRetryReturnsOriginalResponse(t *testing.T) {
	fixture := newServiceFixture(t, 4)
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	if err := fixture.service.Leave(context.Background(), fixture.host, fixture.scope); err != nil {
		t.Fatal(err)
	}
	replay, err := fixture.service.Create(context.Background(), fixture.host, fixture.request)
	if err != nil || !reflect.DeepEqual(replay, fixture.initial) {
		t.Fatal("replay revealed later roster", err, replay)
	}
	request := fixture.request
	request.ClientInstanceID = integrationID(t)
	_, err = fixture.service.Create(context.Background(), fixture.host, request)
	requireDomainError(t, err, domain.ErrIdempotencyConflict)
	var rooms int64
	if err := fixture.db.Model(&postgres.RoomRecord{}).Count(&rooms).Error; err != nil || rooms != 1 {
		t.Fatal("replay created room", err)
	}
}

type collisionIdentifiers struct {
	domain.CryptoIdentifiers
	code       string
	collisions int
	calls      int
}

func (source *collisionIdentifiers) JoinCode() (string, error) {
	source.calls++
	if source.calls <= source.collisions {
		return source.code, nil
	}
	return source.CryptoIdentifiers.JoinCode()
}
func TestCreateJoinCodeCollisionBounded(t *testing.T) {
	fixture := newServiceFixture(t, 4)
	source := &collisionIdentifiers{code: fixture.initial.Lobby.JoinCode, collisions: 2}
	service, err := application.NewService(fixture.store, fixture.clock, source, fixture.policy)
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.request
	request.RequestID = integrationID(t)
	if _, err := service.Create(context.Background(), fixture.host, request); err != nil || source.calls != 3 {
		t.Fatal("collision retry", source.calls, err)
	}
	source.calls = 0
	source.collisions = 99
	request.RequestID = integrationID(t)
	_, err = service.Create(context.Background(), fixture.host, request)
	requireDomainError(t, err, domain.ErrUnavailable)
	if source.calls != 5 {
		t.Fatal("unbounded collisions", source.calls)
	}
	var requests int64
	if err := fixture.db.Model(&postgres.CreateRequestRecord{}).Count(&requests).Error; err != nil || requests != 2 {
		t.Fatal("collision orphan", requests, err)
	}
}
func TestJoinLastSeatConcurrent(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	players := []domain.Principal{fixture.player(t, "A"), fixture.player(t, "B")}
	clients := []string{integrationID(t), integrationID(t)}
	barrier := make(chan struct{})
	results := make(chan error, 2)
	for index, player := range players {
		go func() {
			<-barrier
			_, err := fixture.service.Join(context.Background(), player, domain.JoinRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode, ClientInstanceID: clients[index]})
			results <- err
		}()
	}
	close(barrier)
	success, full := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, domain.ErrLobbyFull) {
			full++
		} else {
			t.Fatal(err)
		}
	}
	aggregate := fixture.read(t)
	if success != 1 || full != 1 || activeCount(aggregate) != 2 || aggregate.Lobby.Version != 2 || fixture.jobs(t, domain.JobInvalidation) != 2 {
		t.Fatal("last seat race violated capacity", aggregate)
	}
}
func TestJoinRetryDoesNotDuplicateMembership(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	guest := fixture.player(t, "Guest")
	clients := []string{integrationID(t), integrationID(t)}
	barrier := make(chan struct{})
	results := make(chan error, 2)
	for _, client := range clients {
		go func() {
			<-barrier
			_, err := fixture.service.Join(context.Background(), guest, domain.JoinRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode, ClientInstanceID: client})
			results <- err
		}()
	}
	close(barrier)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	aggregate := fixture.read(t)
	if len(aggregate.Members) != 2 || len(aggregate.Leases) != 3 || aggregate.Lobby.Version != 2 || memberFor(t, aggregate, guest.PlayerID).Generation != 1 {
		t.Fatal("multi-client join duplicated membership", aggregate)
	}
}
func TestJoinWrongGameOrMissingCode(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	guest := fixture.player(t, "Guest")
	for _, request := range []domain.JoinRequest{{GameKey: "quiz-demo", JoinCode: fixture.initial.Lobby.JoinCode, ClientInstanceID: integrationID(t)}, {GameKey: fixture.scope.GameKey, JoinCode: "23456789", ClientInstanceID: integrationID(t)}} {
		_, err := fixture.service.Join(context.Background(), guest, request)
		requireDomainError(t, err, domain.ErrNotFound)
	}
}
func TestLeaveRejoinReusesMembershipWithNewGeneration(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	guest := fixture.player(t, "Guest")
	_, oldClient := fixture.join(t, guest)
	fixture.assign(t, guest, 1, true)
	before := memberFor(t, fixture.read(t), guest.PlayerID)
	if err := fixture.service.Leave(context.Background(), guest, fixture.scope); err != nil {
		t.Fatal(err)
	}
	fixture.join(t, guest)
	after := memberFor(t, fixture.read(t), guest.PlayerID)
	if before.ID != after.ID || after.Generation != before.Generation+1 || after.JoinSequence <= before.JoinSequence || after.TeamID != nil || after.Ready {
		t.Fatal("rejoin corrupted identity/state", before, after)
	}
	_, err := fixture.service.Heartbeat(context.Background(), guest, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: oldClient, MembershipGeneration: before.Generation})
	requireDomainError(t, err, domain.ErrStaleMembership)
	err = fixture.service.ReleaseLease(context.Background(), guest, fixture.scope, domain.ReleaseLeaseRequest{ClientInstanceID: integrationID(t), MembershipGeneration: before.Generation})
	requireDomainError(t, err, domain.ErrStaleMembership)
	if got := memberFor(t, fixture.read(t), guest.PlayerID); !reflect.DeepEqual(got, after) {
		t.Fatal("old generation changed rejoined member")
	}
}
func TestSnapshotAuthorizationAndCommittedVersion(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	outsider := fixture.player(t, "Outsider")
	_, err := fixture.service.Snapshot(context.Background(), outsider, fixture.scope)
	requireDomainError(t, err, domain.ErrNotFound)
	joined, _ := fixture.join(t, outsider)
	aggregate := fixture.read(t)
	if joined.Lobby.Version != aggregate.Lobby.Version || joined.Lobby.Version != 2 {
		t.Fatal("response used pre-commit version")
	}
	if err := fixture.service.Leave(context.Background(), outsider, fixture.scope); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.Snapshot(context.Background(), outsider, fixture.scope)
	requireDomainError(t, err, domain.ErrNotFound)
}
func TestRejectedCommandStillCommitsDueExpiry(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	guest, guestClient := fixture.readyPair(t)
	fixture.clock.Set(fixture.origin.Add(80 * time.Second))
	if _, err := fixture.service.Heartbeat(context.Background(), guest, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: guestClient, MembershipGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	before := fixture.read(t)
	fixture.clock.Set(fixture.origin.Add(105 * time.Second))
	_, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	requireDomainError(t, err, domain.ErrNotFound)
	after := fixture.read(t)
	if memberFor(t, after, fixture.host.PlayerID).Active || after.Lobby.HostPlayerID == nil || *after.Lobby.HostPlayerID != guest.PlayerID || after.Lobby.Version != before.Lobby.Version+1 || after.Lobby.Phase != domain.PhaseWaiting || len(after.StartRoster) != 0 || fixture.jobs(t, domain.JobRevocation) != 1 {
		t.Fatal("denied start rolled back cleanup", after)
	}
}

type failingCommitStore struct{ domain.Store }

func (store failingCommitStore) Transact(ctx context.Context, scope domain.Scope, mutation domain.Mutation) (domain.Aggregate, error) {
	return store.Store.Transact(ctx, scope, func(aggregate *domain.Aggregate) (domain.Change, error) {
		change, err := mutation(aggregate)
		if err != nil {
			return change, err
		}
		return change, domain.ErrUnavailable
	})
}
func TestRunMutationStorageFailureWins(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	before := fixture.read(t)
	fixture.clock.Set(fixture.origin.Add(time.Hour))
	_, err := application.TransactNormalized(context.Background(), failingCommitStore{fixture.store}, fixture.clock, fixture.policy, fixture.scope, func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Lobby.Name = "must not persist"
		return domain.Change{PublicChanged: true}, domain.ErrNotHost
	})
	requireDomainError(t, err, domain.ErrUnavailable)
	if !reflect.DeepEqual(before, fixture.read(t)) || fixture.jobs(t, "") != 1 {
		t.Fatal("failed commit leaked cleanup")
	}
}
func TestDeniedMutationDiscardsPartialCommand(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	fixture.clock.Set(fixture.origin.Add(45 * time.Second))
	_, err := application.TransactNormalized(context.Background(), fixture.store, fixture.clock, fixture.policy, fixture.scope, func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Lobby.Name = "discard"
		aggregate.Members[0].Ready = true
		return domain.Change{PublicChanged: true}, domain.ErrNotReady
	})
	requireDomainError(t, err, domain.ErrNotReady)
	aggregate := fixture.read(t)
	if aggregate.Lobby.Name != "Room" || aggregate.Members[0].Ready || aggregate.Members[0].DisconnectedUntil == nil || aggregate.Lobby.Version != 2 {
		t.Fatal("partial command leaked or cleanup lost", aggregate)
	}
}
