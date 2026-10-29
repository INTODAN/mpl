package application_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"islandBattles/lobby/application"
	"islandBattles/lobby/domain"
	"islandBattles/lobby/postgres"

	"gorm.io/gorm/clause"
)

func (fixture *serviceFixture) lastConnectedAt(t *testing.T) time.Time {
	t.Helper()
	var rooms []postgres.RoomRecord
	if err := fixture.db.Where(&postgres.RoomRecord{ID: fixture.scope.LobbyID}).Limit(1).Find(&rooms).Error; err != nil || len(rooms) != 1 {
		t.Fatalf("read room: %v", err)
	}
	return rooms[0].LastConnectedAt.UTC()
}

// retention runs passes for the fixture's database with policy.
func (fixture *serviceFixture) retention(t *testing.T, policy domain.Policy) *application.Retention {
	t.Helper()
	store, err := postgres.NewRetentionStore(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	retention, err := application.NewRetention(store, fixture.clock, policy, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return retention
}

func (fixture *serviceFixture) roomExists(t *testing.T, lobbyID string) bool {
	t.Helper()
	var count int64
	if err := fixture.db.Model(&postgres.RoomRecord{}).Where(&postgres.RoomRecord{ID: lobbyID}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count == 1
}

func TestClientsRecordTheLastConnection(t *testing.T) {
	fixture := newServiceFixture(t, 4)
	if got := fixture.lastConnectedAt(t); !got.Equal(fixture.origin) {
		t.Fatalf("created room last connected at %v, want %v", got, fixture.origin)
	}
	at := func(offset time.Duration) time.Time {
		now := fixture.origin.Add(offset)
		fixture.clock.Set(now)
		return now
	}
	ctx := context.Background()
	heartbeat := domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1}

	now := at(10 * time.Second)
	if _, err := fixture.service.Heartbeat(ctx, fixture.host, fixture.scope, heartbeat); err != nil {
		t.Fatal(err)
	}
	if got := fixture.lastConnectedAt(t); !got.Equal(now) {
		t.Fatalf("after heartbeat %v, want %v", got, now)
	}
	// Reading the lobby isn't a sign of a connection.
	at(20 * time.Second)
	if _, err := fixture.service.Snapshot(ctx, fixture.host, fixture.scope); err != nil {
		t.Fatal(err)
	}
	if got := fixture.lastConnectedAt(t); !got.Equal(now) {
		t.Fatalf("after snapshot %v, want %v", got, now)
	}
	now = at(30 * time.Second)
	guest := fixture.player(t, "Guest")
	_, guestClient := fixture.join(t, guest)
	if got := fixture.lastConnectedAt(t); !got.Equal(now) {
		t.Fatalf("after join %v, want %v", got, now)
	}
	now = at(40 * time.Second)
	if err := fixture.service.ReleaseLease(ctx, guest, fixture.scope, domain.ReleaseLeaseRequest{ClientInstanceID: guestClient, MembershipGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if got := fixture.lastConnectedAt(t); !got.Equal(now) {
		t.Fatalf("after lease release %v, want %v", got, now)
	}
	now = at(50 * time.Second)
	if err := fixture.service.Leave(ctx, fixture.host, fixture.scope); err != nil {
		t.Fatal(err)
	}
	if got := fixture.lastConnectedAt(t); !got.Equal(now) {
		t.Fatalf("after leave %v, want %v", got, now)
	}
}

// By default a lobby goes an hour after its last connection, in any phase.
func TestRetentionDeletesAStartedLobbyAnHourAfterItsLastConnection(t *testing.T) {
	fixture := newServiceFixture(t, 4)
	fixture.readyPair(t)
	if _, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope); err != nil {
		t.Fatal(err)
	}
	lastConnection := fixture.origin.Add(30 * time.Second)
	fixture.clock.Set(lastConnection)
	if _, err := fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	retention := fixture.retention(t, fixture.policy)

	fixture.clock.Set(lastConnection.Add(time.Hour))
	retention.RunIfDue(context.Background())
	if !fixture.roomExists(t, fixture.scope.LobbyID) {
		t.Fatal("lobby deleted before its idle hour was over")
	}
	fixture.clock.Set(lastConnection.Add(time.Hour + fixture.policy.Retention.PassInterval))
	retention.RunIfDue(context.Background())
	if fixture.roomExists(t, fixture.scope.LobbyID) {
		t.Fatal("lobby kept after an idle hour")
	}
	for _, model := range []any{&postgres.TeamRecord{}, &postgres.MembershipRecord{}, &postgres.LeaseRecord{}, &postgres.StartRosterRecord{}} {
		var count int64
		if err := fixture.db.Model(model).Where(clause.Eq{Column: clause.Column{Name: "lobby_id"}, Value: fixture.scope.LobbyID}).Count(&count).Error; err != nil || count != 0 {
			t.Errorf("%T rows left = %d, %v", model, count, err)
		}
	}
	// The players' sessions are live, so they stay.
	if count := storeRows(t, fixture, &postgres.PlayerRecord{}); count != 2 {
		t.Fatalf("players left = %d, want 2", count)
	}
	if _, err := fixture.service.Snapshot(context.Background(), fixture.host, fixture.scope); err == nil {
		t.Fatal("deleted lobby still readable")
	}
}

func TestRetentionDeletesALobbyInUseAtItsMaximumAge(t *testing.T) {
	fixture := newServiceFixture(t, 4)
	retention := fixture.retention(t, fixture.policy)
	// Heartbeats keep it connected, so only its age can delete it.
	keepConnected := func(now time.Time) {
		fixture.clock.Set(now)
		if err := fixture.db.Model(&postgres.RoomRecord{}).Where(&postgres.RoomRecord{ID: fixture.scope.LobbyID}).Update("last_connected_at", now).Error; err != nil {
			t.Fatal(err)
		}
	}
	keepConnected(fixture.origin.Add(24 * time.Hour))
	retention.RunIfDue(context.Background())
	if !fixture.roomExists(t, fixture.scope.LobbyID) {
		t.Fatal("lobby deleted at exactly its maximum age")
	}
	keepConnected(fixture.origin.Add(24*time.Hour + fixture.policy.Retention.PassInterval))
	retention.RunIfDue(context.Background())
	if fixture.roomExists(t, fixture.scope.LobbyID) {
		t.Fatal("lobby kept past its maximum age")
	}
}

func TestRetentionFollowsAGamesOverrides(t *testing.T) {
	fixture := newServiceFixture(t, 4)
	quiz, err := fixture.service.Create(context.Background(), fixture.host, domain.CreateRequest{RequestID: integrationID(t), GameKey: "quiz-demo", Name: "Quiz", Capacity: 2, Teams: []domain.TeamDefinition{{Name: "A", Capacity: 1}, {Name: "B", Capacity: 1}}, ClientInstanceID: integrationID(t)})
	if err != nil {
		t.Fatal(err)
	}
	policy := fixture.policy
	policy.Games = append([]domain.GameRegistration(nil), policy.Games...)
	for index := range policy.Games {
		if policy.Games[index].Key == "quiz-demo" {
			policy.Games[index].Retention = domain.LobbyRetention{IdlePeriod: 10 * time.Minute}
		}
	}
	retention := fixture.retention(t, policy)

	fixture.clock.Set(fixture.origin.Add(10*time.Minute + time.Second))
	retention.RunIfDue(context.Background())
	if fixture.roomExists(t, quiz.Lobby.ID) {
		t.Fatal("quiz-demo lobby kept past its game's idle period")
	}
	if !fixture.roomExists(t, fixture.scope.LobbyID) {
		t.Fatal("island-battles lobby deleted before the default idle period")
	}
}

func storeRows(t *testing.T, fixture *serviceFixture, model any) int64 {
	t.Helper()
	var count int64
	if err := fixture.db.Model(model).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}
