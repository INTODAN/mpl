package postgres_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"islandBattles/lobby/internal/testutil"
	lobbypostgres "islandBattles/lobby/postgres"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const historyTable = "lobby_schema_migrations"

type historyRow struct {
	Version int64
	Name    string
	Adopted bool
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

func TestConcurrentMigrationsOfEmptyDatabaseAllSucceed(t *testing.T) {
	db := testutil.NewEmptyPostgres(t)
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
		&lobbypostgres.StartRosterRecord{}, &lobbypostgres.CreateRequestRecord{}, &lobbypostgres.OutboxRecord{},
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
