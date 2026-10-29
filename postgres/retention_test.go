package postgres_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/internal/testutil"
	lobbypostgres "islandBattles/lobby/postgres"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var retentionNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// retentionPolicy keeps the defaults for island-battles and gives quiz-demo
// lobbies a 10 minute idle period and a 2 hour maximum age.
func retentionPolicy() domain.Policy {
	return domain.DefaultPolicy(
		domain.GameRegistration{Key: "island-battles"},
		domain.GameRegistration{Key: "quiz-demo", Retention: domain.LobbyRetention{IdlePeriod: 10 * time.Minute, MaximumAge: 2 * time.Hour}},
	)
}

func newRetentionStore(t *testing.T, db *gorm.DB) *lobbypostgres.RetentionStore {
	t.Helper()
	store, err := lobbypostgres.NewRetentionStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func deleteExpired(t *testing.T, store *lobbypostgres.RetentionStore, cutoffs domain.RetentionCutoffs) domain.RetentionResult {
	t.Helper()
	result, err := store.DeleteExpired(context.Background(), cutoffs)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func playerCreated(t *testing.T, db *gorm.DB, age time.Duration) string {
	t.Helper()
	player := lobbypostgres.PlayerRecord{ID: uuid.NewString(), DisplayName: "Guest", CreatedAt: retentionNow.Add(-age)}
	mustCreate(t, db, &player)
	return player.ID
}

func sessionExpiring(t *testing.T, db *gorm.DB, playerID string, expiresIn time.Duration) string {
	t.Helper()
	session := sessionFor(playerID)
	session.ExpiresAt = retentionNow.Add(expiresIn)
	mustCreate(t, db, &session)
	return session.ID
}

func createRequestMade(t *testing.T, db *gorm.DB, playerID string, age time.Duration) lobbypostgres.CreateRequestRecord {
	t.Helper()
	request := lobbypostgres.CreateRequestRecord{PlayerID: playerID, RequestID: uuid.NewString(), PayloadHash: make([]byte, 32), ResponseJSON: datatypes.JSON(`{}`), CreatedAt: retentionNow.Add(-age)}
	mustCreate(t, db, &request)
	return request
}

// lobbyChildren holds the rows that belong to one room.
type lobbyChildren struct {
	teams, memberships, leases, roster int64
}

// seedLobby creates a started room with a team, a membership, a lease and a
// start roster entry for host, as a game in progress has.
func seedLobby(t *testing.T, db *gorm.DB, hostID, gameKey string, age, idle time.Duration) string {
	t.Helper()
	room := roomFor(hostID, gameKey, joinCode(t))
	room.Phase = string(domain.PhaseStarted)
	room.CreatedAt = retentionNow.Add(-age)
	room.LastConnectedAt = retentionNow.Add(-idle)
	mustCreate(t, db, &room)
	team := lobbypostgres.TeamRecord{ID: uuid.NewString(), LobbyID: room.ID, Name: "Red", NormalizedName: "red", Capacity: 2, TeamOrder: 1}
	mustCreate(t, db, &team)
	member := memberFor(room.ID, hostID)
	member.TeamID = &team.ID
	mustCreate(t, db, &member)
	lease := lobbypostgres.LeaseRecord{LobbyID: room.ID, PlayerID: hostID, Generation: 1, ClientInstanceID: uuid.NewString(), ExpiresAt: room.LastConnectedAt.Add(45 * time.Second)}
	mustCreate(t, db, &lease)
	mustCreate(t, db, &lobbypostgres.StartRosterRecord{LobbyID: room.ID, PlayerID: hostID, TeamID: team.ID, MemberID: member.ID, TeamOrder: 1})
	return room.ID
}

func joinCode(t *testing.T) string {
	t.Helper()
	code, err := (domain.CryptoIdentifiers{}).JoinCode()
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func childrenOf(t *testing.T, db *gorm.DB, lobbyID string) lobbyChildren {
	t.Helper()
	count := func(model any) int64 {
		var rows int64
		if err := db.Model(model).Where(clause.Eq{Column: clause.Column{Name: "lobby_id"}, Value: lobbyID}).Count(&rows).Error; err != nil {
			t.Fatal(err)
		}
		return rows
	}
	return lobbyChildren{
		teams:       count(&lobbypostgres.TeamRecord{}),
		memberships: count(&lobbypostgres.MembershipRecord{}),
		leases:      count(&lobbypostgres.LeaseRecord{}),
		roster:      count(&lobbypostgres.StartRosterRecord{}),
	}
}

func remainingIDs(t *testing.T, db *gorm.DB, model any) []string {
	t.Helper()
	var ids []string
	if err := db.Model(model).Pluck("id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	return ids
}

func requireIDs(t *testing.T, label string, got []string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("remaining %s = %v, want %v", label, got, want)
	}
}

func TestRetentionDeletesCreateRequestsAfterTheirWindow(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	player := playerCreated(t, db, time.Hour)
	sessionExpiring(t, db, player, time.Hour)
	expired := createRequestMade(t, db, player, 24*time.Hour+time.Second)
	atWindowEnd := createRequestMade(t, db, player, 24*time.Hour)
	recent := createRequestMade(t, db, player, time.Minute)

	result := deleteExpired(t, newRetentionStore(t, db), retentionPolicy().RetentionCutoffs(retentionNow))
	if result != (domain.RetentionResult{CreateRequests: 1}) {
		t.Fatalf("result = %+v, want one create request", result)
	}
	var requests []string
	if err := db.Model(&lobbypostgres.CreateRequestRecord{}).Pluck("request_id", &requests).Error; err != nil {
		t.Fatal(err)
	}
	slices.Sort(requests)
	want := []string{atWindowEnd.RequestID, recent.RequestID}
	slices.Sort(want)
	if !slices.Equal(requests, want) || slices.Contains(requests, expired.RequestID) {
		t.Fatalf("remaining create requests = %v, want %v", requests, want)
	}
}

func TestRetentionDeletesExpiredSessionsAndUnreferencedPlayers(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	old := 3 * 24 * time.Hour
	// Expired longer than the hour of grace: deleted with its player.
	abandoned := playerCreated(t, db, old)
	abandonedSession := sessionExpiring(t, db, abandoned, -time.Hour-time.Second)
	// Expired, but inside the grace.
	recentlyExpired := playerCreated(t, db, old)
	recentlyExpiredSession := sessionExpiring(t, db, recentlyExpired, -30*time.Minute)
	live := playerCreated(t, db, time.Hour)
	liveSession := sessionExpiring(t, db, live, time.Hour)
	// Old players without sessions, each kept by one remaining reference.
	member := playerCreated(t, db, old)
	host := playerCreated(t, db, old)
	rostered := playerCreated(t, db, old)
	requester := playerCreated(t, db, old)
	room := roomFor(host, "island-battles", joinCode(t))
	room.LastConnectedAt = retentionNow
	room.CreatedAt = retentionNow
	mustCreate(t, db, &room)
	team := lobbypostgres.TeamRecord{ID: uuid.NewString(), LobbyID: room.ID, Name: "Red", NormalizedName: "red", Capacity: 2, TeamOrder: 1}
	mustCreate(t, db, &team)
	membership := memberFor(room.ID, member)
	mustCreate(t, db, &membership)
	mustCreate(t, db, &lobbypostgres.StartRosterRecord{LobbyID: room.ID, PlayerID: rostered, TeamID: team.ID, MemberID: membership.ID, TeamOrder: 1})
	createRequestMade(t, db, requester, time.Hour)
	// Without a session but too young for its session to have been deleted,
	// so it isn't checked yet.
	young := playerCreated(t, db, time.Hour)
	// Without a session and unreferenced.
	orphan := playerCreated(t, db, old)

	result := deleteExpired(t, newRetentionStore(t, db), retentionPolicy().RetentionCutoffs(retentionNow))
	if result != (domain.RetentionResult{Sessions: 1, Players: 2}) {
		t.Fatalf("result = %+v, want one session and two players", result)
	}
	requireIDs(t, "sessions", remainingIDs(t, db, &lobbypostgres.SessionRecord{}), recentlyExpiredSession, liveSession)
	requireIDs(t, "players", remainingIDs(t, db, &lobbypostgres.PlayerRecord{}), recentlyExpired, live, member, host, rostered, requester, young)
	if slices.Contains(remainingIDs(t, db, &lobbypostgres.SessionRecord{}), abandonedSession) || slices.Contains(remainingIDs(t, db, &lobbypostgres.PlayerRecord{}), orphan) {
		t.Fatal("expired data remained")
	}
}

func TestRetentionDeletesLobbiesPastTheirGamesPeriods(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	host := playerCreated(t, db, time.Hour)
	sessionExpiring(t, db, host, time.Hour)
	day := 24 * time.Hour
	deleted := []string{
		// island-battles keeps the default hour of idleness and day of age.
		seedLobby(t, db, host, "island-battles", 2*time.Hour, time.Hour+time.Second),
		seedLobby(t, db, host, "island-battles", day+time.Second, 0),
		// quiz-demo overrides both.
		seedLobby(t, db, host, "quiz-demo", 30*time.Minute, 10*time.Minute+time.Second),
		seedLobby(t, db, host, "quiz-demo", 2*time.Hour+time.Second, time.Minute),
		// A game that is no longer registered gets the defaults.
		seedLobby(t, db, host, "retired-game", 2*time.Hour, time.Hour+time.Second),
	}
	kept := []string{
		seedLobby(t, db, host, "island-battles", 2*time.Hour, time.Hour),
		seedLobby(t, db, host, "island-battles", day, 0),
		seedLobby(t, db, host, "quiz-demo", time.Hour, 10*time.Minute),
		seedLobby(t, db, host, "quiz-demo", 2*time.Hour, 0),
		seedLobby(t, db, host, "retired-game", 2*time.Hour, 30*time.Minute),
	}

	result := deleteExpired(t, newRetentionStore(t, db), retentionPolicy().RetentionCutoffs(retentionNow))
	if result != (domain.RetentionResult{Lobbies: len(deleted)}) {
		t.Fatalf("result = %+v, want %d lobbies", result, len(deleted))
	}
	requireIDs(t, "rooms", remainingIDs(t, db, &lobbypostgres.RoomRecord{}), kept...)
	for _, lobbyID := range deleted {
		if children := childrenOf(t, db, lobbyID); children != (lobbyChildren{}) {
			t.Errorf("deleted lobby %s kept children %+v", lobbyID, children)
		}
	}
	for _, lobbyID := range kept {
		if children := childrenOf(t, db, lobbyID); children != (lobbyChildren{teams: 1, memberships: 1, leases: 1, roster: 1}) {
			t.Errorf("kept lobby %s has children %+v", lobbyID, children)
		}
	}
	requireIDs(t, "players", remainingIDs(t, db, &lobbypostgres.PlayerRecord{}), host)
}

func TestRetentionFreesPlayersOfDeletedLobbiesInTheSamePass(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	host := playerCreated(t, db, 3*24*time.Hour)
	sessionExpiring(t, db, host, -2*time.Hour)
	seedLobby(t, db, host, "island-battles", 2*time.Hour, 2*time.Hour)

	result := deleteExpired(t, newRetentionStore(t, db), retentionPolicy().RetentionCutoffs(retentionNow))
	if result != (domain.RetentionResult{Sessions: 1, Lobbies: 1, Players: 1}) {
		t.Fatalf("result = %+v", result)
	}
	requireIDs(t, "players", remainingIDs(t, db, &lobbypostgres.PlayerRecord{}))
}

func TestRetentionDeletesInBoundedBatches(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	for range 5 {
		player := playerCreated(t, db, 3*24*time.Hour)
		sessionExpiring(t, db, player, -2*time.Hour)
		createRequestMade(t, db, player, 2*24*time.Hour)
		seedLobby(t, db, player, "island-battles", 2*time.Hour, 2*time.Hour)
	}
	store := newRetentionStore(t, db)
	cutoffs := retentionPolicy().RetentionCutoffs(retentionNow)
	cutoffs.BatchSize = 2

	first := deleteExpired(t, store, cutoffs)
	if first.CreateRequests != 2 || first.Sessions != 2 || first.Lobbies != 2 || first.Players > 2 || !first.Backlog(cutoffs.BatchSize) {
		t.Fatalf("first pass = %+v, want batches of 2", first)
	}
	total := first
	passes := 1
	for result := first; result.Backlog(cutoffs.BatchSize); passes++ {
		if passes > 10 {
			t.Fatal("backlog never drained")
		}
		result = deleteExpired(t, store, cutoffs)
		if max(result.CreateRequests, result.Sessions, result.Lobbies, result.Players) > cutoffs.BatchSize {
			t.Fatalf("pass deleted more than a batch: %+v", result)
		}
		total.CreateRequests += result.CreateRequests
		total.Sessions += result.Sessions
		total.Lobbies += result.Lobbies
		total.Players += result.Players
	}
	if total != (domain.RetentionResult{CreateRequests: 5, Sessions: 5, Lobbies: 5, Players: 5}) {
		t.Fatalf("deleted in total %+v after %d passes", total, passes)
	}
	for _, model := range []any{&lobbypostgres.PlayerRecord{}, &lobbypostgres.SessionRecord{}, &lobbypostgres.RoomRecord{}, &lobbypostgres.TeamRecord{}, &lobbypostgres.MembershipRecord{}, &lobbypostgres.LeaseRecord{}, &lobbypostgres.StartRosterRecord{}, &lobbypostgres.CreateRequestRecord{}} {
		if count := storeCount(t, db, model); count != 0 {
			t.Errorf("%T rows left = %d", model, count)
		}
	}
}

func TestRetentionSkipsLobbyARequestHolds(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	host := playerCreated(t, db, time.Hour)
	sessionExpiring(t, db, host, time.Hour)
	held := seedLobby(t, db, host, "island-battles", 2*time.Hour, 2*time.Hour)
	free := seedLobby(t, db, host, "island-battles", 2*time.Hour, 2*time.Hour)
	store := newRetentionStore(t, db)
	cutoffs := retentionPolicy().RetentionCutoffs(retentionNow)

	// A request's transaction locks its room, as Transact does.
	transaction := db.Begin()
	t.Cleanup(func() { transaction.Rollback() })
	var rooms []lobbypostgres.RoomRecord
	if err := transaction.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).Where(&lobbypostgres.RoomRecord{ID: held}).Find(&rooms).Error; err != nil || len(rooms) != 1 {
		t.Fatalf("lock room: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := store.DeleteExpired(ctx, cutoffs)
	if err != nil || result.Lobbies != 1 {
		t.Fatalf("pass beside a held room = %+v, %v; want only the free room deleted", result, err)
	}
	requireIDs(t, "rooms", remainingIDs(t, db, &lobbypostgres.RoomRecord{}), held)
	if children := childrenOf(t, db, free); children != (lobbyChildren{}) {
		t.Fatalf("free room kept children %+v", children)
	}
	if err := transaction.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if result := deleteExpired(t, store, cutoffs); result.Lobbies != 1 {
		t.Fatalf("pass after release = %+v", result)
	}
	requireIDs(t, "rooms", remainingIDs(t, db, &lobbypostgres.RoomRecord{}))
}

func TestRetentionRejectsUnboundedBatches(t *testing.T) {
	store, err := lobbypostgres.NewRetentionStore(&gorm.DB{})
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{0, -1, domain.MaxRetentionBatchSize + 1} {
		cutoffs := retentionPolicy().RetentionCutoffs(retentionNow)
		cutoffs.BatchSize = size
		if _, err := store.DeleteExpired(context.Background(), cutoffs); err == nil {
			t.Errorf("batch size %d accepted", size)
		}
	}
	if _, err := lobbypostgres.NewRetentionStore(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}

// Deleting a lobby deletes the create request that made it, so a retry inside
// the window creates a new lobby instead of replaying a deleted one.
func TestRetentionDeletesTheCreateRequestOfADeletedLobby(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	host := playerCreated(t, db, time.Hour)
	sessionExpiring(t, db, host, time.Hour)
	idle := seedLobby(t, db, host, "island-battles", 2*time.Hour, 2*time.Hour)
	active := seedLobby(t, db, host, "island-battles", 2*time.Hour, 0)
	made := func(lobbyID string) lobbypostgres.CreateRequestRecord {
		request := lobbypostgres.CreateRequestRecord{PlayerID: host, RequestID: uuid.NewString(), LobbyID: &lobbyID, PayloadHash: make([]byte, 32), ResponseJSON: datatypes.JSON(`{}`), CreatedAt: retentionNow.Add(-time.Minute)}
		mustCreate(t, db, &request)
		return request
	}
	made(idle)
	kept := made(active)

	result := deleteExpired(t, newRetentionStore(t, db), retentionPolicy().RetentionCutoffs(retentionNow))
	if result != (domain.RetentionResult{Lobbies: 1}) {
		t.Fatalf("result = %+v", result)
	}
	var requests []string
	if err := db.Model(&lobbypostgres.CreateRequestRecord{}).Pluck("request_id", &requests).Error; err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0] != kept.RequestID {
		t.Fatalf("remaining create requests = %v, want only %s", requests, kept.RequestID)
	}
}

// A step that keeps failing, here because a trigger refuses to delete create
// requests, doesn't stop the later steps.
func TestRetentionStepsFailIndependently(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	if err := db.Exec(`
CREATE FUNCTION refuse_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'refused'; END $$;
CREATE TRIGGER refuse_create_request_delete BEFORE DELETE ON lobby_create_requests FOR EACH ROW EXECUTE FUNCTION refuse_delete();
`).Error; err != nil {
		t.Fatal(err)
	}
	requester := playerCreated(t, db, time.Hour)
	sessionExpiring(t, db, requester, time.Hour)
	createRequestMade(t, db, requester, 2*24*time.Hour)
	abandoned := playerCreated(t, db, 3*24*time.Hour)
	sessionExpiring(t, db, abandoned, -2*time.Hour)
	host := playerCreated(t, db, time.Hour)
	sessionExpiring(t, db, host, time.Hour)
	seedLobby(t, db, host, "island-battles", 2*time.Hour, 2*time.Hour)

	result, err := newRetentionStore(t, db).DeleteExpired(context.Background(), retentionPolicy().RetentionCutoffs(retentionNow))
	if err == nil || !strings.Contains(err.Error(), "create requests") || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("error = %v, want the failing create request step", err)
	}
	if result != (domain.RetentionResult{Sessions: 1, Lobbies: 1, Players: 1}) {
		t.Fatalf("result = %+v, want the later steps to run", result)
	}
	if count := storeCount(t, db, &lobbypostgres.CreateRequestRecord{}); count != 1 {
		t.Fatalf("create requests = %d, want the refused one kept", count)
	}
}
