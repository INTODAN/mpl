package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MigrationSet is an ordered list of SQL migrations recorded in its own
// history table. The lobby has one set; a host game can version its own
// tables with another set without sharing history with the lobby.
type MigrationSet struct {
	// HistoryTable records applied versions. Its name also keys the advisory
	// lock that serializes runs of this set and prefixes the set's errors.
	HistoryTable string
	// Files holds the migrations at its root, named NNNN_description.sql and
	// numbered from 0001 without gaps. An applied file must never change.
	Files fs.FS
	// Adopt lets the set take over a database created before versioning.
	// When the history is empty and every table migration 1 creates already
	// exists with at least the columns migration 1 gives it, migration 1 is
	// recorded as adopted instead of being run. When only some of those
	// tables exist, or one lacks a column, Apply fails instead of guessing.
	Adopt bool
}

// migrationRecord is one row of a set's history table.
type migrationRecord struct {
	Version   int64     `gorm:"column:version;primaryKey"`
	Name      string    `gorm:"column:name;not null"`
	Checksum  []byte    `gorm:"column:checksum;not null"`
	Adopted   bool      `gorm:"column:adopted;not null"`
	AppliedAt time.Time `gorm:"column:applied_at;not null"`
}

type migrationFile struct {
	version  int64
	name     string
	sql      string
	checksum []byte
}

var migrationFileName = regexp.MustCompile(`^([0-9]{4})_[a-z0-9_]+\.sql$`)

// Apply runs every pending migration in one READ COMMITTED transaction that
// holds an advisory lock, so a concurrent run waits, then reads the history
// the first run committed and finds nothing to do. A failure leaves the
// database unchanged. Migrations therefore cannot use statements PostgreSQL
// refuses inside a transaction, such as CREATE INDEX CONCURRENTLY.
//
// Apply fails when an applied migration's file has changed. Versions newer
// than this build's files are left alone with a warning, so an older build
// can still start.
func (set MigrationSet) Apply(ctx context.Context, database *gorm.DB) error {
	if err := set.apply(ctx, database); err != nil {
		prefix := set.HistoryTable
		if prefix == "" {
			prefix = "migrations"
		}
		return fmt.Errorf("%s: %w", prefix, err)
	}
	return nil
}

// Current reports whether database has every migration of the set applied:
// its history table exists and records the newest version of the set's files,
// or a newer one. It changes nothing and takes no lock. A host that is about
// to rely on the set's schema, such as one that drops an older copy of it
// elsewhere, checks it first.
func (set MigrationSet) Current(ctx context.Context, database *gorm.DB) (bool, error) {
	if database == nil {
		return false, errors.New("nil database")
	}
	migrations, err := set.load()
	if err != nil {
		return false, err
	}
	var exists bool
	if err := database.WithContext(ctx).Raw(`SELECT to_regclass(?) IS NOT NULL`, set.HistoryTable).Scan(&exists).Error; err != nil {
		return false, fmt.Errorf("%s: look for the history: %w", set.HistoryTable, err)
	}
	if !exists {
		return false, nil
	}
	var newest *int64
	if err := database.WithContext(ctx).Table(set.HistoryTable).Select("max(version)").Scan(&newest).Error; err != nil {
		return false, fmt.Errorf("%s: read the history: %w", set.HistoryTable, err)
	}
	return newest != nil && *newest >= migrations[len(migrations)-1].version, nil
}

func (set MigrationSet) apply(ctx context.Context, database *gorm.DB) error {
	if database == nil {
		return errors.New("nil database")
	}
	migrations, err := set.load()
	if err != nil {
		return err
	}
	// Under REPEATABLE READ the snapshot would predate the lock wait, so a
	// run that waited would read an empty history and apply everything again.
	readCommitted := &sql.TxOptions{Isolation: sql.LevelReadCommitted}
	return database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, set.HistoryTable).Error; err != nil {
			return fmt.Errorf("lock: %w", err)
		}
		if err := transaction.Exec(`CREATE TABLE IF NOT EXISTS ? (
			version bigint PRIMARY KEY,
			name text NOT NULL,
			checksum bytea NOT NULL,
			adopted boolean NOT NULL,
			applied_at timestamp with time zone NOT NULL
		)`, clause.Table{Name: set.HistoryTable}).Error; err != nil {
			return fmt.Errorf("create history: %w", err)
		}
		history := func() *gorm.DB { return transaction.Table(set.HistoryTable) }

		var applied []migrationRecord
		if err := history().Order(clause.OrderByColumn{Column: clause.Column{Name: "version"}}).Find(&applied).Error; err != nil {
			return fmt.Errorf("read history: %w", err)
		}
		if len(applied) == 0 && set.Adopt {
			adopt, err := set.adoptable(ctx, transaction, migrations[0])
			if err != nil {
				return err
			}
			if adopt {
				adopted := migrations[0].record(true)
				if err := history().Create(&adopted).Error; err != nil {
					return fmt.Errorf("adopt %s: %w", migrations[0].name, err)
				}
				applied = append(applied, adopted)
			}
		}

		known := min(len(applied), len(migrations))
		for index, record := range applied[:known] {
			expected := migrations[index]
			if record.Version != expected.version || !bytes.Equal(record.Checksum, expected.checksum) {
				return fmt.Errorf("applied migration %d (%s) differs from %s; add a new migration instead of editing an applied one",
					record.Version, record.Name, expected.name)
			}
		}
		if len(applied) > len(migrations) {
			newest := applied[len(applied)-1]
			slog.WarnContext(ctx, "Database schema is newer than this build; leaving the newer migrations alone",
				"history", set.HistoryTable, "applied_version", newest.Version, "applied_name", newest.Name,
				"build_version", migrations[len(migrations)-1].version)
		}
		for _, migration := range migrations[known:] {
			if err := execMigration(ctx, transaction, migration); err != nil {
				return err
			}
			record := migration.record(false)
			if err := history().Create(&record).Error; err != nil {
				return fmt.Errorf("record %s: %w", migration.name, err)
			}
		}
		return nil
	}, readCommitted)
}

// execMigration sends a file to the driver unchanged, because GORM's Exec
// parses "@name" placeholders. Without arguments, pgx uses the simple
// protocol, which allows several statements.
func execMigration(ctx context.Context, transaction *gorm.DB, migration migrationFile) error {
	if _, err := transaction.Statement.ConnPool.ExecContext(ctx, migration.sql); err != nil {
		return fmt.Errorf("apply %s: %w", migration.name, err)
	}
	return nil
}

// adoptable reports whether the current schema already holds what migration 1
// creates. It returns false when none of those tables exist, true when all of
// them exist with every column migration 1 gives them, and an error otherwise.
// Only migration 1 is compared: columns that later migrations add or drop
// don't matter, and extra columns are allowed.
func (set MigrationSet) adoptable(ctx context.Context, transaction *gorm.DB, first migrationFile) (bool, error) {
	var target string
	if err := transaction.Raw(`SELECT coalesce(current_schema(), '')`).Scan(&target).Error; err != nil {
		return false, fmt.Errorf("read current schema: %w", err)
	}
	if target == "" {
		return false, errors.New("no current schema to migrate")
	}
	expected, err := set.firstMigrationColumns(ctx, transaction, first)
	if err != nil {
		return false, err
	}
	tables := make([]string, 0, len(expected))
	for table := range expected {
		tables = append(tables, table)
	}
	slices.Sort(tables)
	existing, err := tableColumns(transaction, target, tables)
	if err != nil {
		return false, err
	}
	if len(existing) == 0 {
		return false, nil
	}

	var present, absent []string
	for _, table := range tables {
		if _, ok := existing[table]; ok {
			present = append(present, table)
		} else {
			absent = append(absent, table)
		}
	}
	if len(absent) > 0 {
		return false, fmt.Errorf("cannot adopt a schema created before versioning: it has %s but lacks %s, which %s also creates; complete or remove the old tables by hand",
			strings.Join(present, ", "), strings.Join(absent, ", "), first.name)
	}
	var missing []string
	for _, table := range tables {
		for _, column := range expected[table] {
			if !slices.Contains(existing[table], column) {
				missing = append(missing, table+"."+column)
			}
		}
	}
	if len(missing) > 0 {
		return false, fmt.Errorf("cannot adopt an outdated schema created before versioning: it lacks %s, which %s creates; add the columns by hand",
			strings.Join(missing, ", "), first.name)
	}
	return true, nil
}

// firstMigrationColumns runs migration 1 in a scratch schema inside a
// savepoint, reads the tables and columns it created, and rolls back to the
// savepoint, which also restores the search path. Reading the file itself
// keeps the check from drifting from what migration 1 creates.
func (set MigrationSet) firstMigrationColumns(ctx context.Context, transaction *gorm.DB, first migrationFile) (map[string][]string, error) {
	const savepoint = "migration_adoption_probe"
	scratch := set.HistoryTable + "_probe"
	if err := transaction.SavePoint(savepoint).Error; err != nil {
		return nil, fmt.Errorf("probe %s: %w", first.name, err)
	}
	columns, probeErr := func() (map[string][]string, error) {
		if err := transaction.Exec(`CREATE SCHEMA ?`, clause.Table{Name: scratch}).Error; err != nil {
			return nil, err
		}
		if err := transaction.Exec(`SELECT set_config('search_path', quote_ident(?), true)`, scratch).Error; err != nil {
			return nil, err
		}
		if err := execMigration(ctx, transaction, first); err != nil {
			return nil, err
		}
		return tableColumns(transaction, scratch, nil)
	}()
	if err := transaction.RollbackTo(savepoint).Error; err != nil && probeErr == nil {
		probeErr = err
	}
	if probeErr != nil {
		return nil, fmt.Errorf("probe %s: %w", first.name, probeErr)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("probe %s: it creates no tables to adopt", first.name)
	}
	return columns, nil
}

// tableColumns lists the columns of a schema's tables in order, limited to
// the named tables unless tables is nil.
func tableColumns(transaction *gorm.DB, schema string, tables []string) (map[string][]string, error) {
	query := transaction.Table("information_schema.columns").Select("table_name, column_name").
		Where("table_schema = ?", schema).Order("table_name, ordinal_position")
	if tables != nil {
		query = query.Where("table_name IN ?", tables)
	}
	var rows []struct {
		TableName  string
		ColumnName string
	}
	if err := query.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("read columns of %s: %w", schema, err)
	}
	columns := map[string][]string{}
	for _, row := range rows {
		columns[row.TableName] = append(columns[row.TableName], row.ColumnName)
	}
	return columns, nil
}

func (set MigrationSet) load() ([]migrationFile, error) {
	if set.HistoryTable == "" || set.Files == nil {
		return nil, errors.New("migration set needs a history table and files")
	}
	entries, err := fs.ReadDir(set.Files, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	migrations := make([]migrationFile, 0, len(entries))
	for _, entry := range entries {
		match := migrationFileName.FindStringSubmatch(entry.Name())
		if entry.IsDir() || match == nil {
			return nil, fmt.Errorf("%q is not named NNNN_description.sql", entry.Name())
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return nil, err
		}
		if expected := int64(len(migrations) + 1); version != expected {
			return nil, fmt.Errorf("%s should be numbered %04d", entry.Name(), expected)
		}
		content, err := fs.ReadFile(set.Files, entry.Name())
		if err != nil {
			return nil, err
		}
		checksum := sha256.Sum256(content)
		migrations = append(migrations, migrationFile{version: version, name: entry.Name(), sql: string(content), checksum: checksum[:]})
	}
	if len(migrations) == 0 {
		return nil, errors.New("no migrations")
	}
	return migrations, nil
}

func (migration migrationFile) record(adopted bool) migrationRecord {
	return migrationRecord{
		Version:   migration.version,
		Name:      migration.name,
		Checksum:  migration.checksum,
		Adopted:   adopted,
		AppliedAt: time.Now().UTC(),
	}
}
