package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"islandBattles/lobby/app"
	"islandBattles/lobby/internal/testutil"

	"gorm.io/driver/postgres"
)

// Every deploy runs the command, so it must create the lobby's schema on an
// empty database and then find nothing left to do.
func TestMigrateCreatesTheLobbySchemaAndRepeats(t *testing.T) {
	database := testutil.NewEmptyPostgres(t)
	dialector, ok := database.Dialector.(*postgres.Dialector)
	if !ok {
		t.Fatalf("unexpected dialector %T", database.Dialector)
	}
	config, err := app.LoadDatabaseConfigFromLookup(func(name string) (string, bool) {
		if name == app.DatabaseURLEnv {
			return dialector.DSN, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for range 2 {
		if err := run(context.Background(), config, logger); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"lobby_schema_migrations", "lobby_rooms", "lobby_sessions", "lobby_memberships"} {
		if !database.Migrator().HasTable(table) {
			t.Fatalf("missing table %s", table)
		}
	}
	// The lobby's migrations create only the lobby's tables.
	var others []string
	if err := database.Raw(`SELECT tablename FROM pg_tables WHERE schemaname = current_schema() AND tablename NOT LIKE 'lobby\_%'`).Scan(&others).Error; err != nil {
		t.Fatal(err)
	}
	if len(others) != 0 {
		t.Fatalf("tables besides the lobby's: %v", others)
	}
}

// Only the database's URL is required.
func TestMigrateNeedsTheDatabaseURL(t *testing.T) {
	_, err := app.LoadDatabaseConfigFromLookup(func(string) (string, bool) { return "", false })
	if err == nil || err.Error() != "missing required environment variables: "+app.DatabaseURLEnv {
		t.Fatalf("got %v", err)
	}
}
