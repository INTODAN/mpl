package postgres

import (
	"context"
	"embed"
	"io/fs"

	"gorm.io/gorm"
)

// SchemaTables is the complete generic-lobby schema, independent of game tables.
var SchemaTables = []string{
	"lobby_players",
	"lobby_sessions",
	"lobby_rooms",
	"lobby_teams",
	"lobby_memberships",
	"lobby_leases",
	"lobby_start_roster",
	"lobby_create_requests",
	"lobby_outbox",
}

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations returns the versioned lobby schema, recorded in
// lobby_schema_migrations. Change the schema by adding the next numbered file
// to lobby/postgres/migrations; never edit an applied one.
func Migrations() MigrationSet {
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err) // The directory name is a valid constant.
	}
	return MigrationSet{HistoryTable: "lobby_schema_migrations", Files: files, AdoptTable: "lobby_rooms"}
}

// Migrate brings the generic lobby schema to the latest version and may safely
// be called more than once, including concurrently. It does not open a
// connection.
func Migrate(ctx context.Context, db *gorm.DB) error {
	return Migrations().Apply(ctx, db)
}
