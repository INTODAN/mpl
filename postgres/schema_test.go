package postgres_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"islandBattles/lobby/internal/testutil"
	lobbypostgres "islandBattles/lobby/postgres"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// No table holds messages waiting to be sent: changes are pushed after commit.
func TestSchemaCreatesOnlyLobbyTables(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	tables, err := db.Migrator().GetTables()
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{
		"lobby_players", "lobby_sessions", "lobby_rooms", "lobby_teams",
		"lobby_memberships", "lobby_leases", "lobby_start_roster",
		"lobby_create_requests", "lobby_watchers", "lobby_schema_migrations",
	}
	slices.Sort(tables)
	slices.Sort(expected)
	if !slices.Equal(tables, expected) {
		t.Fatalf("schema tables = %v, want %v", tables, expected)
	}
}

func TestSchemaMigrationIsRepeatable(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	player := seedPlayer(t, db)
	room := seedRoom(t, db, player.ID, "island-battles", "REPEAT01")
	if err := lobbypostgres.Migrate(context.Background(), db); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	var count int64
	if err := db.Model(&lobbypostgres.RoomRecord{}).Where(&lobbypostgres.RoomRecord{ID: room.ID}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("room count after repeated migration = %d, want 1", count)
	}
	var loaded lobbypostgres.RoomRecord
	if err := db.First(&loaded, "id = ?", room.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.Name != room.Name || loaded.Version != room.Version {
		t.Fatalf("room changed after repeated migration: %#v", loaded)
	}
}

func TestSchemaRejectsDuplicateMembership(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	player := seedPlayer(t, db)
	roomA := seedRoom(t, db, player.ID, "island-battles", "MEMBER01")
	roomB := seedRoom(t, db, player.ID, "quiz-demo", "MEMBER01")
	member := memberFor(roomA.ID, player.ID)
	mustCreate(t, db, &member)
	duplicate := memberFor(roomA.ID, player.ID)
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate room/player membership succeeded")
	}
	otherRoom := memberFor(roomB.ID, player.ID)
	mustCreate(t, db, &otherRoom)
}

func TestSchemaAllowsInactiveMembershipReuse(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	player := seedPlayer(t, db)
	room := seedRoom(t, db, player.ID, "island-battles", "REUSE001")
	member := memberFor(room.ID, player.ID)
	mustCreate(t, db, &member)
	if err := db.Model(&lobbypostgres.MembershipRecord{}).Where("id = ?", member.ID).Updates(map[string]any{"active": false, "generation": int64(2)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&lobbypostgres.MembershipRecord{}).Where("id = ?", member.ID).Updates(map[string]any{"active": true, "generation": int64(3)}).Error; err != nil {
		t.Fatal(err)
	}
	var loaded lobbypostgres.MembershipRecord
	if err := db.Where(&lobbypostgres.MembershipRecord{LobbyID: room.ID, PlayerID: player.ID}).First(&loaded).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.ID != member.ID || !loaded.Active || loaded.Generation != 3 {
		t.Fatalf("reactivated membership = %#v", loaded)
	}
}

func TestSchemaRejectsCrossLobbyTeam(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	player := seedPlayer(t, db)
	roomA := seedRoom(t, db, player.ID, "island-battles", "CROSS001")
	roomB := seedRoom(t, db, player.ID, "quiz-demo", "CROSS001")
	teamB := lobbypostgres.TeamRecord{ID: uuid.NewString(), LobbyID: roomB.ID, Name: "Blue", NormalizedName: "blue", TeamOrder: 1}
	mustCreate(t, db, &teamB)
	member := memberFor(roomA.ID, player.ID)
	member.TeamID = &teamB.ID
	if err := db.Create(&member).Error; err == nil {
		t.Fatal("cross-lobby team assignment succeeded")
	}
}

func TestSchemaRetainsOldGenerationLeases(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	player := seedPlayer(t, db)
	room := seedRoom(t, db, player.ID, "island-battles", "LEASE001")
	member := memberFor(room.ID, player.ID)
	mustCreate(t, db, &member)
	lease := lobbypostgres.LeaseRecord{LobbyID: room.ID, PlayerID: player.ID, Generation: 1, ClientInstanceID: uuid.NewString(), ExpiresAt: time.Now().UTC().Add(time.Minute)}
	mustCreate(t, db, &lease)
	if err := db.Model(&lobbypostgres.MembershipRecord{}).Where("id = ?", member.ID).Update("generation", int64(2)).Error; err != nil {
		t.Fatalf("increment membership generation with old lease: %v", err)
	}
	var count int64
	if err := db.Model(&lobbypostgres.LeaseRecord{}).Where(&lobbypostgres.LeaseRecord{LobbyID: room.ID, PlayerID: player.ID, Generation: 1}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("old lease count = %d, err = %v", count, err)
	}
}

func TestSchemaEnforcesLocalBounds(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	player := seedPlayer(t, db)
	valid := seedRoom(t, db, player.ID, "island-battles", "BOUND001")
	cases := []struct {
		name   string
		create func() error
	}{
		{"zero version", func() error {
			room := roomFor(player.ID, "quiz-demo", "BOUND001")
			room.Version = 0
			return db.Create(&room).Error
		}},
		{"zero generation", func() error {
			member := memberFor(valid.ID, player.ID)
			member.Generation = 0
			return db.Create(&member).Error
		}},
		{"capacity one", func() error {
			room := roomFor(player.ID, "quiz-demo", "BOUND002")
			room.Capacity = 1
			return db.Create(&room).Error
		}},
		{"capacity seventeen", func() error {
			room := roomFor(player.ID, "quiz-demo", "BOUND003")
			room.Capacity = 17
			return db.Create(&room).Error
		}},
		{"invalid phase", func() error {
			room := roomFor(player.ID, "quiz-demo", "BOUND004")
			room.Phase = "invalid"
			return db.Create(&room).Error
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := test.create(); err == nil {
				t.Fatal("invalid local value was accepted")
			}
		})
	}
}

func TestSchemaScopesJoinCodeUniqueness(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	player := seedPlayer(t, db)
	closed := seedRoom(t, db, player.ID, "island-battles", "CLOSED01")
	if err := db.Model(&lobbypostgres.RoomRecord{}).Where("id = ?", closed.ID).Update("phase", "closed").Error; err != nil {
		t.Fatal(err)
	}
	duplicate := roomFor(player.ID, "island-battles", "CLOSED01")
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("closed-room join code was reused in same game")
	}
	otherGame := roomFor(player.ID, "quiz-demo", "CLOSED01")
	mustCreate(t, db, &otherGame)
}

func TestSchemaAllowsOneSessionPerGuest(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	first := seedPlayer(t, db)
	second := seedPlayer(t, db)
	firstSession := sessionFor(first.ID)
	mustCreate(t, db, &firstSession)
	duplicate := sessionFor(first.ID)
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("second session for one guest succeeded")
	}
	secondSession := sessionFor(second.ID)
	mustCreate(t, db, &secondSession)
}

func TestSchemaScopesTeamUniqueness(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	player := seedPlayer(t, db)
	roomA := seedRoom(t, db, player.ID, "island-battles", "TEAM0001")
	roomB := seedRoom(t, db, player.ID, "quiz-demo", "TEAM0001")
	team := lobbypostgres.TeamRecord{ID: uuid.NewString(), LobbyID: roomA.ID, Name: "Blue", NormalizedName: "blue", TeamOrder: 1}
	mustCreate(t, db, &team)
	duplicateOrder := team
	duplicateOrder.ID = uuid.NewString()
	duplicateOrder.Name, duplicateOrder.NormalizedName = "Red", "red"
	if err := db.Create(&duplicateOrder).Error; err == nil {
		t.Fatal("duplicate team order succeeded")
	}
	duplicateName := team
	duplicateName.ID = uuid.NewString()
	duplicateName.TeamOrder = 2
	if err := db.Create(&duplicateName).Error; err == nil {
		t.Fatal("duplicate normalized team name succeeded")
	}
	reused := team
	reused.ID, reused.LobbyID = uuid.NewString(), roomB.ID
	mustCreate(t, db, &reused)
}

func TestSchemaPreservesNullAndFalse(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	player := seedPlayer(t, db)
	room := roomFor("", "island-battles", "NULL0001")
	room.HostPlayerID = nil
	mustCreate(t, db, &room)
	member := memberFor(room.ID, player.ID)
	member.Active, member.Ready, member.TeamID, member.DisconnectedUntil = false, false, nil, nil
	mustCreate(t, db, &member)
	var loadedRoom lobbypostgres.RoomRecord
	var loadedMember lobbypostgres.MembershipRecord
	mustFind(t, db, &loadedRoom, room.ID)
	mustFind(t, db, &loadedMember, member.ID)
	if loadedRoom.HostPlayerID != nil || loadedMember.TeamID != nil || loadedMember.DisconnectedUntil != nil || loadedMember.Active || loadedMember.Ready {
		t.Fatalf("NULL/false fields changed: room=%#v member=%#v", loadedRoom, loadedMember)
	}
}

func seedPlayer(t *testing.T, db *gorm.DB) lobbypostgres.PlayerRecord {
	t.Helper()
	player := lobbypostgres.PlayerRecord{ID: uuid.NewString(), DisplayName: "Guest", CreatedAt: time.Now().UTC()}
	mustCreate(t, db, &player)
	return player
}

func seedRoom(t *testing.T, db *gorm.DB, playerID, gameKey, joinCode string) lobbypostgres.RoomRecord {
	t.Helper()
	room := roomFor(playerID, gameKey, joinCode)
	// Omitting game_settings lets the helper seed schemas from before
	// migration 0003 added it, and last_connected_at those before 0004.
	omitted := []string{"GameSettings"}
	if !db.Migrator().HasColumn(&lobbypostgres.RoomRecord{}, "last_connected_at") {
		omitted = append(omitted, "LastConnectedAt")
	}
	// And round, which migration 0008 added.
	if !db.Migrator().HasColumn(&lobbypostgres.RoomRecord{}, "round") {
		omitted = append(omitted, "Round")
	}
	// And round_started_at, which migration 0009 added.
	if !db.Migrator().HasColumn(&lobbypostgres.RoomRecord{}, "round_started_at") {
		omitted = append(omitted, "RoundStartedAt")
	}
	// And bound_round, which migration 0011 added.
	if !db.Migrator().HasColumn(&lobbypostgres.RoomRecord{}, "bound_round") {
		omitted = append(omitted, "BoundRound")
	}
	if err := db.Omit(omitted...).Create(&room).Error; err != nil {
		t.Fatal(err)
	}
	return room
}

func roomFor(playerID, gameKey, joinCode string) lobbypostgres.RoomRecord {
	var host *string
	if playerID != "" {
		host = &playerID
	}
	return lobbypostgres.RoomRecord{ID: uuid.NewString(), GameKey: gameKey, Name: "Room", JoinCode: joinCode, Capacity: 4, Phase: "waiting", Round: 1, Version: 1, HostPlayerID: host, CreatedAt: time.Now().UTC(), RoundStartedAt: time.Now().UTC(), NextJoinSequence: 2, GameSettings: []byte(`{}`), LastConnectedAt: time.Now().UTC()}
}

func memberFor(lobbyID, playerID string) lobbypostgres.MembershipRecord {
	return lobbypostgres.MembershipRecord{ID: uuid.NewString(), LobbyID: lobbyID, PlayerID: playerID, DisplayName: "Guest", Generation: 1, Active: true, Ready: false, JoinedAt: time.Now().UTC(), JoinSequence: 1}
}

func sessionFor(playerID string) lobbypostgres.SessionRecord {
	return lobbypostgres.SessionRecord{ID: uuid.NewString(), PlayerID: playerID, BearerHash: make([]byte, 32), CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
}

func mustCreate(t *testing.T, db *gorm.DB, value any) {
	t.Helper()
	if err := db.Create(value).Error; err != nil {
		t.Fatal(err)
	}
}
func mustFind(t *testing.T, db *gorm.DB, value any, id string) {
	t.Helper()
	if err := db.First(value, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
}
