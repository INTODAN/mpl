package postgres_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/internal/testutil"
	lobbypostgres "islandBattles/lobby/postgres"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const historyTable = "lobby_schema_migrations"

type historyRow struct {
	Version int64
	Name    string
	Adopted bool
}

// Current tells a migrated database from one without the set's history or
// with only part of it.
func TestCurrentReportsWhetherEveryMigrationIsApplied(t *testing.T) {
	ctx := context.Background()
	empty := testutil.NewEmptyPostgres(t)
	if current, err := lobbypostgres.Migrations().Current(ctx, empty); err != nil || current {
		t.Fatalf("an empty database: current = %t, %v", current, err)
	}
	migrated := testutil.NewPostgresFixture(t)
	if current, err := lobbypostgres.Migrations().Current(ctx, migrated); err != nil || !current {
		t.Fatalf("a migrated database: current = %t, %v", current, err)
	}
	var newest int64
	if err := migrated.Table(historyTable).Select("max(version)").Scan(&newest).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrated.Exec(`DELETE FROM lobby_schema_migrations WHERE version = ?`, newest).Error; err != nil {
		t.Fatal(err)
	}
	if current, err := lobbypostgres.Migrations().Current(ctx, migrated); err != nil || current {
		t.Fatalf("a database without the newest migration: current = %t, %v", current, err)
	}
}

func TestMigrateFreshInstallRecordsEveryVersion(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	history := migrationHistory(t, db)
	names := migrationNames(t, lobbypostgres.Migrations().Files)
	if len(history) != len(names) {
		t.Fatalf("history = %+v, want versions for %v", history, names)
	}
	for index, row := range history {
		if row.Version != int64(index+1) || row.Name != names[index] || row.Adopted {
			t.Fatalf("history[%d] = %+v, want version %d %s run, not adopted", index, row, index+1, names[index])
		}
	}
}

func TestMigrateAdoptsSchemaCreatedBeforeVersioning(t *testing.T) {
	legacy := testutil.NewEmptyPostgres(t)
	executeFile(t, legacy, "testdata/legacy_schema.sql")
	player := seedPlayer(t, legacy)
	room := seedRoom(t, legacy, player.ID, "island-battles", "ADOPT001")

	// Migration 1 has no IF NOT EXISTS clauses, so running it here would fail.
	if err := lobbypostgres.Migrate(context.Background(), legacy); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}
	history := migrationHistory(t, legacy)
	if len(history) == 0 || history[0].Version != 1 || !history[0].Adopted {
		t.Fatalf("history = %+v, want version 1 adopted", history)
	}
	for _, row := range history[1:] {
		if row.Adopted {
			t.Fatalf("history = %+v, want only version 1 adopted", history)
		}
	}
	var loaded lobbypostgres.RoomRecord
	mustFind(t, legacy, &loaded, room.ID)
	if loaded.JoinCode != room.JoinCode || loaded.HostPlayerID == nil || *loaded.HostPlayerID != player.ID {
		t.Fatalf("room after adoption = %#v, want %#v", loaded, room)
	}

	fresh := testutil.NewPostgresFixture(t)
	assertSameSchema(t, schemaSnapshot(t, legacy), schemaSnapshot(t, fresh))
}

func TestMigrateAdoptsThenAppliesLaterMigrations(t *testing.T) {
	legacy := testutil.NewEmptyPostgres(t)
	executeFile(t, legacy, "testdata/legacy_schema.sql")

	// Migration 2 drops lobby_outbox, which migration 1 created. That must not
	// stop an old database from being adopted first.
	if err := lobbypostgres.Migrate(context.Background(), legacy); err != nil {
		t.Fatalf("adopt and upgrade legacy schema: %v", err)
	}
	names := migrationNames(t, lobbypostgres.Migrations().Files)
	history := migrationHistory(t, legacy)
	if len(history) != len(names) || !history[0].Adopted || history[1].Name != "0002_drop_lobby_outbox.sql" || history[1].Adopted {
		t.Fatalf("history = %+v, want version 1 adopted and 0002_drop_lobby_outbox.sql run", history)
	}
	if legacy.Migrator().HasTable("lobby_outbox") {
		t.Fatal("lobby_outbox survived the later migration")
	}
	if err := lobbypostgres.Migrate(context.Background(), legacy); err != nil {
		t.Fatalf("repeat after adoption: %v", err)
	}
}

func TestMigrateRefusesToAdoptPartialSchema(t *testing.T) {
	legacy := testutil.NewEmptyPostgres(t)
	executeFile(t, legacy, "testdata/legacy_schema.sql")
	if err := legacy.Exec(`DROP TABLE lobby_outbox, lobby_create_requests`).Error; err != nil {
		t.Fatal(err)
	}

	err := lobbypostgres.Migrate(context.Background(), legacy)
	if err == nil || !strings.HasPrefix(err.Error(), historyTable+": ") ||
		!strings.Contains(err.Error(), "lacks lobby_create_requests, lobby_outbox") {
		t.Fatalf("migrate partial schema error = %v, want the missing tables named", err)
	}
	assertNothingRecorded(t, legacy)
	if legacy.Migrator().HasTable("lobby_outbox") {
		t.Fatal("a refused adoption created lobby_outbox")
	}
}

func TestMigrateRefusesToAdoptOutdatedSchema(t *testing.T) {
	legacy := testutil.NewEmptyPostgres(t)
	executeFile(t, legacy, "testdata/legacy_schema.sql")
	if err := legacy.Exec(`ALTER TABLE lobby_memberships DROP COLUMN disconnected_until`).Error; err != nil {
		t.Fatal(err)
	}
	// Columns that migration 1 doesn't create are allowed.
	if err := legacy.Exec(`ALTER TABLE lobby_rooms ADD COLUMN retired text`).Error; err != nil {
		t.Fatal(err)
	}

	err := lobbypostgres.Migrate(context.Background(), legacy)
	if err == nil || !strings.Contains(err.Error(), "lacks lobby_memberships.disconnected_until,") {
		t.Fatalf("migrate outdated schema error = %v, want the missing column named", err)
	}
	if strings.Contains(err.Error(), "retired") {
		t.Fatalf("migrate outdated schema error = %v, want extra columns ignored", err)
	}
	assertNothingRecorded(t, legacy)
}

func TestMigrateWarnsAboutNewerVersions(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	set := lobbypostgres.Migrations()
	newer := set
	next := fmt.Sprintf("%04d_from_a_newer_build.sql", len(migrationNames(t, set.Files))+1)
	newer.Files = withFile(t, set.Files, next, `SELECT 1;`)
	if err := newer.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	if err := set.Apply(context.Background(), db); err != nil {
		t.Fatalf("older build on a newer schema: %v", err)
	}
	if output := logs.String(); !strings.Contains(output, "level=WARN") || !strings.Contains(output, next) {
		t.Fatalf("log = %q, want a warning naming %s", output, next)
	}
}

func TestMigrateAppliesNewMigrationToExistingDatabase(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	player := seedPlayer(t, db)
	room := seedRoom(t, db, player.ID, "island-battles", "UPGRADE1")

	set := lobbypostgres.Migrations()
	next := fmt.Sprintf("%04d_widen_room_capacity.sql", len(migrationNames(t, set.Files))+1)
	set.Files = withFile(t, set.Files, next, `
ALTER TABLE lobby_rooms DROP CONSTRAINT ck_lobby_rooms_capacity;
ALTER TABLE lobby_rooms ADD CONSTRAINT ck_lobby_rooms_capacity CHECK (capacity BETWEEN 2 AND 128);
`)
	if err := set.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply new migration: %v", err)
	}

	wide := roomFor(player.ID, "quiz-demo", "UPGRADE2")
	wide.Capacity = 100
	mustCreate(t, db, &wide)
	history := migrationHistory(t, db)
	if last := history[len(history)-1]; last.Name != next || last.Adopted {
		t.Fatalf("last history row = %+v, want %s run", last, next)
	}
	var loaded lobbypostgres.RoomRecord
	mustFind(t, db, &loaded, room.ID)
	if loaded.JoinCode != room.JoinCode {
		t.Fatalf("room after upgrade = %#v, want %#v", loaded, room)
	}
	if err := set.Apply(context.Background(), db); err != nil {
		t.Fatalf("repeat upgraded migration: %v", err)
	}
}

func TestMigrateAddsGameSettingsToExistingRooms(t *testing.T) {
	db := testutil.NewEmptyPostgres(t)
	initial := lobbypostgres.Migrations()
	first := migrationNames(t, initial.Files)[0]
	data, err := fs.ReadFile(initial.Files, first)
	if err != nil {
		t.Fatal(err)
	}
	initial.Files = fstest.MapFS{first: &fstest.MapFile{Data: data}}
	if err := initial.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	player := seedPlayer(t, db)
	room := seedRoom(t, db, player.ID, "island-battles", "LEGACY01")

	if err := lobbypostgres.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate rooms without game settings: %v", err)
	}
	columns, err := db.Migrator().ColumnTypes(&lobbypostgres.RoomRecord{})
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(columns, func(column gorm.ColumnType) bool { return column.Name() == "game_settings" })
	if index < 0 {
		t.Fatal("migration did not add game_settings")
	}
	if nullable, _ := columns[index].Nullable(); columns[index].DatabaseTypeName() != "json" || nullable {
		t.Fatalf("game_settings column = %s, nullable %v", columns[index].DatabaseTypeName(), nullable)
	}
	store, err := lobbypostgres.NewStore(db, domain.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := store.Read(context.Background(), domain.Scope{GameKey: room.GameKey, LobbyID: room.ID})
	if err != nil || aggregate.Lobby.JoinCode != room.JoinCode || string(aggregate.Lobby.GameSettings) != domain.EmptyGameSettings {
		t.Fatalf("existing room = %#v, %v", aggregate.Lobby, err)
	}
}

// Rooms that existed before migration 0004 must not look idle while they are
// in use, so each gets the latest end of its leases, or its creation time.
func TestMigrateRecordsLastConnectionOfExistingRooms(t *testing.T) {
	db := testutil.NewEmptyPostgres(t)
	before := lobbypostgres.Migrations()
	files := fstest.MapFS{}
	for _, name := range migrationNames(t, before.Files)[:3] {
		data, err := fs.ReadFile(before.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	before.Files = files
	if err := before.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	player := seedPlayer(t, db)
	created := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	seed := func(code string, leases ...lobbypostgres.LeaseRecord) string {
		room := seedRoom(t, db, player.ID, "island-battles", code)
		if err := db.Model(&lobbypostgres.RoomRecord{}).Where(&lobbypostgres.RoomRecord{ID: room.ID}).Update("created_at", created).Error; err != nil {
			t.Fatal(err)
		}
		if len(leases) > 0 {
			if err := db.Omit("Kind", "SeatSettings", "SeatRequestID", "MemberSettings").Create(&[]lobbypostgres.MembershipRecord{memberFor(room.ID, player.ID)}).Error; err != nil {
				t.Fatal(err)
			}
		}
		for _, lease := range leases {
			lease.LobbyID, lease.PlayerID, lease.Generation = room.ID, player.ID, 1
			lease.ClientInstanceID = uuid.NewString()
			mustCreate(t, db, &lease)
		}
		return room.ID
	}
	released := created.Add(20 * time.Minute)
	connected := seed("LASTCON1",
		lobbypostgres.LeaseRecord{ExpiresAt: created.Add(30 * time.Minute)},
		lobbypostgres.LeaseRecord{ExpiresAt: created.Add(2 * time.Hour)},
	)
	releasedEarly := seed("LASTCON2", lobbypostgres.LeaseRecord{ExpiresAt: created.Add(time.Hour), ReleasedAt: &released})
	withoutLeases := seed("LASTCON3")

	if err := lobbypostgres.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate existing rooms: %v", err)
	}
	for id, want := range map[string]time.Time{connected: created.Add(2 * time.Hour), releasedEarly: released, withoutLeases: created} {
		var room lobbypostgres.RoomRecord
		mustFind(t, db, &room, id)
		if !room.LastConnectedAt.Equal(want) {
			t.Errorf("room %s last connected at %v, want %v", id, room.LastConnectedAt, want)
		}
	}
	var nullable string
	if err := db.Raw(`SELECT is_nullable FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'lobby_rooms' AND column_name = 'last_connected_at'`).Scan(&nullable).Error; err != nil || nullable != "NO" {
		t.Fatalf("last_connected_at nullable = %q, %v", nullable, err)
	}
}

// Memberships from before migration 0005 are people, and a seat can't exist
// without its settings.
func TestMigrateMakesExistingMembershipsHuman(t *testing.T) {
	db := testutil.NewEmptyPostgres(t)
	before := lobbypostgres.Migrations()
	files := fstest.MapFS{}
	for _, name := range migrationNames(t, before.Files)[:4] {
		data, err := fs.ReadFile(before.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	before.Files = files
	if err := before.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	player := seedPlayer(t, db)
	room := seedRoom(t, db, player.ID, "island-battles", "HUMANS01")
	member := memberFor(room.ID, player.ID)
	if err := db.Omit("Kind", "SeatSettings", "SeatRequestID", "MemberSettings").Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	if err := lobbypostgres.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate existing memberships: %v", err)
	}
	var loaded lobbypostgres.MembershipRecord
	mustFind(t, db, &loaded, member.ID)
	if loaded.Kind != "human" || loaded.SeatSettings != nil {
		t.Fatalf("existing membership = kind %q, settings %s", loaded.Kind, loaded.SeatSettings)
	}
	seatPlayer := seedPlayer(t, db)
	seat := memberFor(room.ID, seatPlayer.ID)
	seat.Kind = "seat"
	if err := db.Create(&seat).Error; err == nil {
		t.Fatal("a seat without settings was stored")
	}
	seat.SeatSettings = []byte(`{"bot_level":2}`)
	if err := db.Create(&seat).Error; err != nil {
		t.Fatal(err)
	}
	// A request ID names one seat of a lobby.
	requestID := uuid.NewString()
	seat.ID, seat.PlayerID, seat.SeatRequestID = uuid.NewString(), seedPlayer(t, db).ID, &requestID
	if err := db.Create(&seat).Error; err != nil {
		t.Fatal(err)
	}
	seat.ID, seat.PlayerID = uuid.NewString(), seedPlayer(t, db).ID
	if err := db.Create(&seat).Error; err == nil {
		t.Fatal("two seats of one lobby shared a request ID")
	}
	member = memberFor(room.ID, seedPlayer(t, db).ID)
	member.SeatSettings = []byte(`{}`)
	if err := db.Create(&member).Error; err == nil {
		t.Fatal("a person with seat settings was stored")
	}
}

// legacyTeamRecord is a team row from before migration 0007, which dropped
// its capacity.
type legacyTeamRecord struct {
	ID             string `gorm:"column:id;type:uuid;primaryKey"`
	LobbyID        string `gorm:"column:lobby_id;type:uuid;not null"`
	Name           string `gorm:"column:name;not null"`
	NormalizedName string `gorm:"column:normalized_name;not null"`
	Capacity       int    `gorm:"column:capacity;not null"`
	TeamOrder      int    `gorm:"column:team_order;not null"`
}

func (legacyTeamRecord) TableName() string { return "lobby_teams" }

// Rooms from before migration 0007 chose their own capacity and their teams'.
// Each keeps its teams and members, and its capacity becomes its team count.
func TestMigrateGivesExistingRoomsOneMemberPerTeam(t *testing.T) {
	db := testutil.NewEmptyPostgres(t)
	before := lobbypostgres.Migrations()
	files := fstest.MapFS{}
	for _, name := range migrationNames(t, before.Files)[:6] {
		data, err := fs.ReadFile(before.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	before.Files = files
	if err := before.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	player := seedPlayer(t, db)
	seed := func(code string, roomCapacity int, teamNames ...string) (lobbypostgres.RoomRecord, []legacyTeamRecord) {
		room := roomFor(player.ID, "island-battles", code)
		room.Capacity = roomCapacity
		if err := db.Omit("Round", "RoundStartedAt", "BoundRound").Create(&room).Error; err != nil {
			t.Fatal(err)
		}
		teams := make([]legacyTeamRecord, len(teamNames))
		for index, name := range teamNames {
			teams[index] = legacyTeamRecord{ID: uuid.NewString(), LobbyID: room.ID, Name: name, NormalizedName: strings.ToLower(name), Capacity: 8, TeamOrder: index + 1}
			mustCreate(t, db, &teams[index])
		}
		return room, teams
	}
	wide, _ := seed("WIDEROOM", 64, "Red", "Blue", "Green")
	shared, sharedTeams := seed("SHARED01", 4, "Red", "Blue")
	// A room without teams, which only a test can make, must not stop the
	// migration with a capacity over the narrower check.
	teamless, _ := seed("NOTEAMS1", 64)
	// Two people on one team, which a team's capacity allowed before.
	for sequence := range 2 {
		member := memberFor(shared.ID, seedPlayer(t, db).ID)
		member.TeamID, member.JoinSequence = &sharedTeams[0].ID, int64(sequence+1)
		if err := db.Omit("MemberSettings").Create(&member).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := lobbypostgres.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate rooms with team capacities: %v", err)
	}
	if db.Migrator().HasColumn(&lobbypostgres.TeamRecord{}, "capacity") {
		t.Fatal("lobby_teams.capacity survived the migration")
	}
	for id, want := range map[string]int{wide.ID: 3, shared.ID: 2, teamless.ID: 16} {
		var room lobbypostgres.RoomRecord
		mustFind(t, db, &room, id)
		if room.Capacity != want {
			t.Errorf("room %s capacity = %d, want %d", room.JoinCode, room.Capacity, want)
		}
	}
	store, err := lobbypostgres.NewStore(db, domain.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := store.Read(context.Background(), domain.Scope{GameKey: shared.GameKey, LobbyID: shared.ID})
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Lobby.Capacity != 2 || len(aggregate.Teams) != 2 || len(aggregate.Members) != 2 {
		t.Fatalf("room after migration = capacity %d, %d teams, %d members", aggregate.Lobby.Capacity, len(aggregate.Teams), len(aggregate.Members))
	}
	for _, member := range aggregate.Members {
		if member.TeamID == nil || *member.TeamID != sharedTeams[0].ID {
			t.Fatalf("member %s moved to team %v, want %s", member.ID, member.TeamID, sharedTeams[0].ID)
		}
	}
	tooWide := roomFor(player.ID, "island-battles", "TOOWIDE1")
	tooWide.Capacity = 17
	if err := db.Create(&tooWide).Error; err == nil {
		t.Fatal("a room with more than 16 teams' capacity was stored")
	}
}

// Rooms from before migration 0008 are in their first round, and the round
// counts from 1.
func TestMigrateStartsExistingRoomsAtRoundOne(t *testing.T) {
	db := testutil.NewEmptyPostgres(t)
	before := lobbypostgres.Migrations()
	files := fstest.MapFS{}
	for _, name := range migrationNames(t, before.Files)[:7] {
		data, err := fs.ReadFile(before.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	before.Files = files
	if err := before.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	player := seedPlayer(t, db)
	room := seedRoom(t, db, player.ID, "island-battles", "ROUNDONE")
	if err := db.Model(&lobbypostgres.RoomRecord{}).Where(&lobbypostgres.RoomRecord{ID: room.ID}).Update("phase", "started").Error; err != nil {
		t.Fatal(err)
	}

	if err := lobbypostgres.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate existing rooms: %v", err)
	}
	store, err := lobbypostgres.NewStore(db, domain.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := store.Read(context.Background(), domain.Scope{GameKey: room.GameKey, LobbyID: room.ID})
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Lobby.Round != 1 || aggregate.Lobby.Phase != domain.PhaseStarted {
		t.Fatalf("existing room = round %d, phase %s; want round 1, started", aggregate.Lobby.Round, aggregate.Lobby.Phase)
	}
	if err := db.Model(&lobbypostgres.RoomRecord{}).Where(&lobbypostgres.RoomRecord{ID: room.ID}).Update("round", 0).Error; err == nil {
		t.Fatal("a room in round 0 was stored")
	}
}

func TestConcurrentMigrationsOfEmptyDatabaseAllSucceed(t *testing.T) {
	t.Run("server default isolation", func(t *testing.T) {
		assertConcurrentMigrationsSucceed(t, testutil.NewEmptyPostgres(t))
	})
	t.Run("server default repeatable read", func(t *testing.T) {
		// A run that waited on the lock must still see the history the first
		// run committed.
		db := testutil.NewEmptyPostgres(t)
		if err := db.Exec(`DO $$ BEGIN EXECUTE format('ALTER DATABASE %I SET default_transaction_isolation = ''repeatable read''', current_database()); END $$`).Error; err != nil {
			t.Fatal(err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		sqlDB.SetMaxIdleConns(0) // New connections pick up the default.
		var isolation string
		if err := db.Raw(`SHOW default_transaction_isolation`).Scan(&isolation).Error; err != nil || isolation != "repeatable read" {
			t.Fatalf("default isolation = %q, %v", isolation, err)
		}
		assertConcurrentMigrationsSucceed(t, db)
	})
}

func assertConcurrentMigrationsSucceed(t *testing.T, db *gorm.DB) {
	t.Helper()
	const runs = 4
	start := make(chan struct{})
	errs := make(chan error, runs)
	var group sync.WaitGroup
	for range runs {
		group.Go(func() {
			<-start
			errs <- lobbypostgres.Migrate(context.Background(), db)
		})
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent migration: %v", err)
		}
	}
	if history, names := migrationHistory(t, db), migrationNames(t, lobbypostgres.Migrations().Files); len(history) != len(names) {
		t.Fatalf("history = %+v, want one row for each of %v", history, names)
	}
}

func TestMigrateRejectsEditedMigration(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	set := lobbypostgres.Migrations()
	first := migrationNames(t, set.Files)[0]
	content, err := fs.ReadFile(set.Files, first)
	if err != nil {
		t.Fatal(err)
	}
	set.Files = withFile(t, set.Files, first, string(content)+"\n-- edited after it was applied\n")
	if err := set.Apply(context.Background(), db); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("apply edited migration error = %v, want a differing-migration error", err)
	}
}

func TestRecordsMatchMigratedColumns(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	records := []any{
		&lobbypostgres.PlayerRecord{}, &lobbypostgres.SessionRecord{}, &lobbypostgres.RoomRecord{},
		&lobbypostgres.TeamRecord{}, &lobbypostgres.MembershipRecord{}, &lobbypostgres.LeaseRecord{},
		&lobbypostgres.StartRosterRecord{}, &lobbypostgres.CreateRequestRecord{},
	}
	for _, record := range records {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(record); err != nil {
			t.Fatal(err)
		}
		var fields []string
		for _, field := range statement.Schema.Fields {
			if field.DBName != "" {
				fields = append(fields, field.DBName)
			}
		}
		columnTypes, err := db.Migrator().ColumnTypes(record)
		if err != nil {
			t.Fatal(err)
		}
		var columns []string
		for _, columnType := range columnTypes {
			columns = append(columns, columnType.Name())
		}
		slices.Sort(fields)
		slices.Sort(columns)
		if !slices.Equal(fields, columns) {
			t.Errorf("%s columns = %v, record fields = %v; add a migration", statement.Schema.Table, columns, fields)
		}
	}
}

func migrationHistory(t *testing.T, db *gorm.DB) []historyRow {
	t.Helper()
	var rows []historyRow
	if err := db.Table(historyTable).Order(clause.OrderByColumn{Column: clause.Column{Name: "version"}}).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

// assertNothingRecorded checks that a failed run left no history behind.
func assertNothingRecorded(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db.Migrator().HasTable(historyTable) {
		t.Fatalf("a failed migration left %s behind", historyTable)
	}
}

func migrationNames(t *testing.T, files fs.FS) []string {
	t.Helper()
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// withFile copies files and adds or replaces one migration.
func withFile(t *testing.T, files fs.FS, name, content string) fs.FS {
	t.Helper()
	copied := fstest.MapFS{}
	for _, existing := range migrationNames(t, files) {
		data, err := fs.ReadFile(files, existing)
		if err != nil {
			t.Fatal(err)
		}
		copied[existing] = &fstest.MapFile{Data: data}
	}
	copied[name] = &fstest.MapFile{Data: []byte(content)}
	return copied
}

func executeFile(t *testing.T, db *gorm.DB, path string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(content)).Error; err != nil {
		t.Fatalf("execute %s: %v", path, err)
	}
}

// schemaSnapshot lists the lobby tables' columns, constraints and indexes,
// excluding the migration history. Column order is ignored.
func schemaSnapshot(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var lines []string
	err := db.Raw(`
SELECT format('column %s.%s %s nullable=%s default=%s', table_name, column_name, data_type, is_nullable, coalesce(column_default, ''))
FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name LIKE 'lobby\_%' AND table_name <> ?
UNION ALL
SELECT format('constraint %s.%s %s', conrelid::regclass, conname, pg_get_constraintdef(oid))
FROM pg_constraint
WHERE connamespace = current_schema()::regnamespace AND conrelid::regclass::text LIKE 'lobby\_%' AND conrelid::regclass::text <> ?
UNION ALL
SELECT format('index %s.%s %s', tablename, indexname, indexdef)
FROM pg_indexes
WHERE schemaname = current_schema() AND tablename LIKE 'lobby\_%' AND tablename <> ?
ORDER BY 1`, historyTable, historyTable, historyTable).Scan(&lines).Error
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func assertSameSchema(t *testing.T, got, want []string) {
	t.Helper()
	for _, line := range got {
		if !slices.Contains(want, line) {
			t.Errorf("unexpected: %s", line)
		}
	}
	for _, line := range want {
		if !slices.Contains(got, line) {
			t.Errorf("missing: %s", line)
		}
	}
}

// Rooms from before migration 0009 have their creation as the start of their
// round.
func TestMigrateStartsExistingRoomsRoundAtCreation(t *testing.T) {
	db := testutil.NewEmptyPostgres(t)
	before := lobbypostgres.Migrations()
	files := fstest.MapFS{}
	for _, name := range migrationNames(t, before.Files)[:8] {
		data, err := fs.ReadFile(before.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	before.Files = files
	if err := before.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	player := seedPlayer(t, db)
	room := seedRoom(t, db, player.ID, "island-battles", "STARTED1")
	createdAt := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)
	if err := db.Model(&lobbypostgres.RoomRecord{}).Where(&lobbypostgres.RoomRecord{ID: room.ID}).Update("created_at", createdAt).Error; err != nil {
		t.Fatal(err)
	}

	if err := lobbypostgres.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate existing rooms: %v", err)
	}
	var migrated lobbypostgres.RoomRecord
	if err := db.Where(&lobbypostgres.RoomRecord{ID: room.ID}).Take(&migrated).Error; err != nil {
		t.Fatal(err)
	}
	if !migrated.RoundStartedAt.Equal(createdAt) {
		t.Fatalf("existing room's round started at %v, want its creation %v", migrated.RoundStartedAt, createdAt)
	}
}
