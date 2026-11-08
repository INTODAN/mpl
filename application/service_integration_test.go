package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"islandBattles/lobby/application"
	"islandBattles/lobby/domain"
	"islandBattles/lobby/internal/testutil"
	"islandBattles/lobby/postgres"
	"islandBattles/lobby/realtime"

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

// recordedPushes counts what the publishing store pushed after each commit,
// counting an unsubscribe only when the store's check confirms it.
type recordedPushes struct {
	mutex         sync.Mutex
	invalidations map[string]int
	lastVersion   map[string]int64
	revocations   map[string]int
	// beforeRevoke runs once, before the next push with revocations checks
	// them, like a request that lands while a push waits to retry.
	beforeRevoke func()
}

func (pushes *recordedPushes) PublishChange(ctx context.Context, room domain.Lobby, change domain.Change, stillDue domain.RevocationCheck) error {
	pushes.mutex.Lock()
	if change.PublicChanged {
		pushes.invalidations[room.ID]++
		pushes.lastVersion[room.ID] = room.Version
	}
	hook := pushes.beforeRevoke
	if len(change.Revocations) > 0 {
		pushes.beforeRevoke = nil
	}
	pushes.mutex.Unlock()
	if len(change.Revocations) == 0 {
		return nil
	}
	if hook != nil {
		hook()
	}
	for _, revocation := range change.Revocations {
		due, err := stillDue(ctx, revocation)
		if err != nil {
			return err
		}
		if due {
			pushes.mutex.Lock()
			pushes.revocations[room.ID]++
			pushes.mutex.Unlock()
		}
	}
	return nil
}

type pushKind int

const (
	allPushes pushKind = iota
	invalidationPushes
	revocationPushes
)

type serviceFixture struct {
	db *gorm.DB
	// store pushes each commit to pushes, as the backend's store pushes to Centrifugo.
	store    domain.Store
	pushes   *recordedPushes
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

// quizSettings stands in for a game's settings validator. It accepts an
// optional rounds count from 1 to 10 and an optional teams count that must
// match the lobby's teams, and nothing else.
func quizSettings(_ context.Context, settings json.RawMessage, teams []domain.TeamDefinition) error {
	var decoded struct {
		Rounds *int `json:"rounds"`
		Teams  *int `json:"teams"`
	}
	decoder := json.NewDecoder(bytes.NewReader(settings))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if decoded.Rounds != nil && (*decoded.Rounds < 1 || *decoded.Rounds > 10) {
		return errors.New("rounds must be between 1 and 10")
	}
	if decoded.Teams != nil && *decoded.Teams != len(teams) {
		return fmt.Errorf("teams must be %d, the lobby's team count", len(teams))
	}
	return nil
}

// choiceGame stands in for a game whose players make a choice in the game's
// own API before they can get ready. It records the choices by lobby and
// player, and can fail to read them for a lobby.
type choiceGame struct {
	mutex   sync.Mutex
	chosen  map[[2]string]bool
	failing map[string]bool
}

var gatedGame = &choiceGame{chosen: map[[2]string]bool{}, failing: map[string]bool{}}

func (game *choiceGame) choose(lobbyID, playerID string, chosen bool) {
	game.mutex.Lock()
	defer game.mutex.Unlock()
	game.chosen[[2]string{lobbyID, playerID}] = chosen
}

func (game *choiceGame) fail(lobbyID string) {
	game.mutex.Lock()
	defer game.mutex.Unlock()
	game.failing[lobbyID] = true
}

func (game *choiceGame) gate(_ context.Context, lobbyID string) (domain.ReadyCheck, error) {
	game.mutex.Lock()
	defer game.mutex.Unlock()
	if game.failing[lobbyID] {
		return nil, errors.New("choices are unreadable")
	}
	chosen := map[string]bool{}
	for key, value := range game.chosen {
		if key[0] == lobbyID && value {
			chosen[key[1]] = true
		}
	}
	return func(members []domain.ReadyMember, _ bool) error {
		for _, member := range members {
			if !chosen[member.PlayerID] {
				return domain.GameRequirement("urn:example:problem:choice-missing", "Make your choice before getting ready.")
			}
		}
		return nil
	}, nil
}

// newServiceFixture creates an island-battles lobby; configure can change the
// create request, for example to another game or with game settings.
func newServiceFixture(t *testing.T, capacity int, configure ...func(*domain.CreateRequest)) *serviceFixture {
	t.Helper()
	fixture := &serviceFixture{db: testutil.NewPostgresFixture(t), origin: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), policy: domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"}, domain.GameRegistration{Key: "quiz-demo", ValidateSettings: quizSettings}, domain.GameRegistration{Key: "team-game", StartRules: domain.StartRules{EveryTeamNeedsMember: true}}, domain.GameRegistration{Key: "gated-game", ReadyGate: gatedGame.gate}, domain.GameRegistration{Key: "seat-game", ReadyGate: gatedGame.gate, ValidateSeat: botSeat, StartRules: domain.StartRules{EveryTeamNeedsMember: true}})}
	fixture.clock = &integrationClock{now: fixture.origin}
	fixture.pushes = &recordedPushes{invalidations: map[string]int{}, lastVersion: map[string]int64{}, revocations: map[string]int{}}
	fixture.store = fixture.newStore(t)
	var err error
	fixture.service, err = application.NewService(fixture.store, fixture.clock, domain.CryptoIdentifiers{}, fixture.policy)
	if err != nil {
		t.Fatal(err)
	}
	fixture.host = fixture.player(t, "Host")
	fixture.clientID = integrationID(t)
	fixture.request = domain.CreateRequest{RequestID: integrationID(t), GameKey: "island-battles", Name: "Room", Capacity: capacity, Teams: []domain.TeamDefinition{{Name: "Red", Capacity: 1}, {Name: "Blue", Capacity: capacity}}, ClientInstanceID: fixture.clientID}
	for _, change := range configure {
		change(&fixture.request)
	}
	fixture.initial, err = fixture.service.Create(context.Background(), fixture.host, fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	fixture.scope = fixture.initial.Lobby.Scope()
	return fixture
}

// newStore is a separate PostgreSQL store, as another backend instance or a
// restarted one would have, that pushes to the fixture's recorder.
func (fixture *serviceFixture) newStore(t *testing.T) domain.Store {
	t.Helper()
	database, err := postgres.NewStore(fixture.db, fixture.clock)
	if err != nil {
		t.Fatal(err)
	}
	store, err := application.NewPublishingStore(database, fixture.pushes)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
func (fixture *serviceFixture) player(t *testing.T, name string) domain.Principal {
	t.Helper()
	principal := domain.Principal{PlayerID: integrationID(t), SessionID: integrationID(t), DisplayName: name}
	if err := fixture.db.Create(&postgres.PlayerRecord{ID: principal.PlayerID, DisplayName: name, CreatedAt: fixture.clock.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	bearerHash, err := (domain.CryptoIdentifiers{}).Bytes(32)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&postgres.SessionRecord{ID: principal.SessionID, PlayerID: principal.PlayerID, BearerHash: bearerHash, CreatedAt: fixture.clock.Now(), ExpiresAt: fixture.clock.Now().Add(fixture.policy.SessionDuration)}).Error; err != nil {
		t.Fatal(err)
	}
	return principal
}

// proxy returns Centrifugo's subscribe and sub-refresh proxy call for the room
// channel, which applies due expiry like any lobby request.
func (fixture *serviceFixture) proxy(t *testing.T, store domain.Store) func(domain.Principal) error {
	t.Helper()
	identities, err := postgres.NewIdentityStore(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := realtime.NewAuthorizer(identities, store, fixture.clock, fixture.policy)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := domain.RoomChannel(fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	return func(principal domain.Principal) error {
		_, err := authorizer.AuthorizeSubscription(context.Background(), principal.PlayerID, channel)
		return err
	}
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

// pushed counts this room's pushes so far. Pushes happen before each command
// returns, so a count read right after a call includes that call's push.
func (fixture *serviceFixture) pushed(kind pushKind) int {
	fixture.pushes.mutex.Lock()
	defer fixture.pushes.mutex.Unlock()
	invalidations, revocations := fixture.pushes.invalidations[fixture.scope.LobbyID], fixture.pushes.revocations[fixture.scope.LobbyID]
	switch kind {
	case invalidationPushes:
		return invalidations
	case revocationPushes:
		return revocations
	default:
		return invalidations + revocations
	}
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

func quizLobbyWith(settings string) func(*domain.CreateRequest) {
	return func(request *domain.CreateRequest) {
		request.GameKey = "quiz-demo"
		request.GameSettings = json.RawMessage(settings)
	}
}
func TestGameSettingsReachTheStartedSnapshot(t *testing.T) {
	fixture := newServiceFixture(t, 3, quizLobbyWith(`{ "rounds" : 3 }`))
	const canonical = `{"rounds":3}`
	if string(fixture.initial.Lobby.GameSettings) != canonical {
		t.Fatalf("created settings = %s", fixture.initial.Lobby.GameSettings)
	}
	guest := fixture.player(t, "Guest")
	joined, _ := fixture.join(t, guest)
	if string(joined.Lobby.GameSettings) != canonical {
		t.Fatalf("joined settings = %s", joined.Lobby.GameSettings)
	}
	fixture.assign(t, fixture.host, 0, true)
	fixture.assign(t, guest, 1, true)
	started, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil || started.Lobby.Phase != domain.PhaseStarted || string(started.Lobby.GameSettings) != canonical {
		t.Fatalf("started snapshot = %#v, %v", started.Lobby, err)
	}
	binding, err := application.NewGameBinding("quiz-demo", fixture.service, func(_ context.Context, snapshot domain.Snapshot) (string, error) {
		return string(snapshot.Lobby.GameSettings), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	received, err := binding.Bind(context.Background(), fixture.host, fixture.scope)
	if err != nil || received != canonical {
		t.Fatalf("game received settings %q, %v", received, err)
	}
}
func TestCreateRejectsInvalidGameSettings(t *testing.T) {
	fixture := newServiceFixture(t, 4)
	for _, testCase := range []struct {
		gameKey, settings, message string
	}{
		{"island-battles", `{"rounds":3}`, "this game takes no settings"},
		{"quiz-demo", `{"rounds":0}`, "rounds must be between 1 and 10"},
		{"quiz-demo", `{"teams":3}`, "teams must be 2, the lobby's team count"},
		{"quiz-demo", `{"theme":"space"}`, `json: unknown field "theme"`},
		{"quiz-demo", `[1]`, "game settings must be a JSON object"},
		{"quiz-demo", `{"note":"a\u0000b"}`, "game settings must not contain NUL characters"},
	} {
		request := fixture.request
		request.RequestID = integrationID(t)
		request.GameKey = testCase.gameKey
		request.GameSettings = json.RawMessage(testCase.settings)
		_, err := fixture.service.Create(context.Background(), fixture.host, request)
		if domain.ErrorCodeOf(err) != domain.CodeInvalidGameSettings || err.Error() != testCase.message {
			t.Errorf("%s %s: error = %v", testCase.gameKey, testCase.settings, err)
		}
	}
	var rooms, requests int64
	if err := fixture.db.Model(&postgres.RoomRecord{}).Count(&rooms).Error; err != nil || rooms != 1 {
		t.Fatal("rejected settings created a room", rooms, err)
	}
	if err := fixture.db.Model(&postgres.CreateRequestRecord{}).Count(&requests).Error; err != nil || requests != 1 {
		t.Fatal("rejected settings kept a create request", requests, err)
	}
}
func TestCreateRetryComparesCanonicalGameSettings(t *testing.T) {
	fixture := newServiceFixture(t, 4, quizLobbyWith(`{"rounds":3}`))
	retry := fixture.request
	retry.GameSettings = json.RawMessage(` {"rounds": 3} `)
	replay, err := fixture.service.Create(context.Background(), fixture.host, retry)
	if err != nil || !reflect.DeepEqual(replay, fixture.initial) {
		t.Fatal("reformatted settings did not replay the original response", err, replay)
	}
	for _, settings := range []string{`{"rounds":4}`, ``} {
		changed := fixture.request
		changed.GameSettings = json.RawMessage(settings)
		_, err = fixture.service.Create(context.Background(), fixture.host, changed)
		requireDomainError(t, err, domain.ErrIdempotencyConflict)
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
	if success != 1 || full != 1 || activeCount(aggregate) != 2 || aggregate.Lobby.Version != 2 || fixture.pushed(invalidationPushes) != 2 {
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
	if memberFor(t, after, fixture.host.PlayerID).Active || after.Lobby.HostPlayerID == nil || *after.Lobby.HostPlayerID != guest.PlayerID || after.Lobby.Version != before.Lobby.Version+1 || after.Lobby.Phase != domain.PhaseWaiting || len(after.StartRoster) != 0 || fixture.pushed(revocationPushes) != 1 {
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
	if !reflect.DeepEqual(before, fixture.read(t)) || fixture.pushed(allPushes) != 1 {
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
	if fixture.pushed(invalidationPushes) != 2 || fixture.pushes.lastVersion[fixture.scope.LobbyID] != 2 {
		t.Fatal("committed cleanup was not pushed with its version")
	}
}
