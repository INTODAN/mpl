package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MigrationSet is an ordered list of SQL migrations recorded in its own
// history table. The lobby has one set; a host game can version its own
// tables with another set without sharing history with the lobby.
type MigrationSet struct {
	// HistoryTable records applied versions. Its name also keys the advisory
	// lock that serializes runs of this set.
	HistoryTable string
	// Files holds the migrations at its root, named NNNN_description.sql and
	// numbered from 0001 without gaps. An applied file must never change.
	Files fs.FS
	// AdoptTable, when set, is a table that migration 1 creates. If it exists
	// while the history is empty, the database was created before versioning:
	// migration 1 is recorded as adopted instead of being run.
	AdoptTable string
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

// Apply runs every pending migration in one transaction that holds an
// advisory lock, so a concurrent run waits and then finds nothing to do, and a
// failure leaves the database unchanged. Migrations therefore cannot use
// statements PostgreSQL refuses inside a transaction, such as CREATE INDEX
// CONCURRENTLY.
//
// Apply fails when an applied migration's file has changed. Versions newer
// than this build's files are left alone, so an older build can still start.
func (set MigrationSet) Apply(ctx context.Context, database *gorm.DB) error {
	if database == nil {
		return errors.New("lobby postgres: nil database")
	}
	migrations, err := set.load()
	if err != nil {
		return err
	}
	return database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, set.HistoryTable).Error; err != nil {
			return fmt.Errorf("lock %s: %w", set.HistoryTable, err)
		}
		if err := transaction.Exec(`CREATE TABLE IF NOT EXISTS ? (
			version bigint PRIMARY KEY,
			name text NOT NULL,
			checksum bytea NOT NULL,
			adopted boolean NOT NULL,
			applied_at timestamp with time zone NOT NULL
		)`, clause.Table{Name: set.HistoryTable}).Error; err != nil {
			return fmt.Errorf("create %s: %w", set.HistoryTable, err)
		}
		history := func() *gorm.DB { return transaction.Table(set.HistoryTable) }

		var applied []migrationRecord
		if err := history().Order(clause.OrderByColumn{Column: clause.Column{Name: "version"}}).Find(&applied).Error; err != nil {
			return fmt.Errorf("read %s: %w", set.HistoryTable, err)
		}
		if len(applied) == 0 && set.AdoptTable != "" && transaction.Migrator().HasTable(set.AdoptTable) {
			adopted := migrations[0].record(true)
			if err := history().Create(&adopted).Error; err != nil {
				return fmt.Errorf("adopt %s: %w", migrations[0].name, err)
			}
			applied = append(applied, adopted)
		}

		known := min(len(applied), len(migrations))
		for index, record := range applied[:known] {
			expected := migrations[index]
			if record.Version != expected.version || !bytes.Equal(record.Checksum, expected.checksum) {
				return fmt.Errorf("%s: applied migration %d (%s) differs from %s; add a new migration instead of editing an applied one",
					set.HistoryTable, record.Version, record.Name, expected.name)
			}
		}
		for _, migration := range migrations[known:] {
			// The file goes to the driver unchanged, because GORM's Exec
			// parses "@name" placeholders. Without arguments, pgx uses the
			// simple protocol, which allows several statements.
			if _, err := transaction.Statement.ConnPool.ExecContext(ctx, migration.sql); err != nil {
				return fmt.Errorf("apply %s: %w", migration.name, err)
			}
			record := migration.record(false)
			if err := history().Create(&record).Error; err != nil {
				return fmt.Errorf("record %s: %w", migration.name, err)
			}
		}
		return nil
	})
}

func (set MigrationSet) load() ([]migrationFile, error) {
	if set.HistoryTable == "" || set.Files == nil {
		return nil, errors.New("lobby postgres: migration set needs a history table and files")
	}
	entries, err := fs.ReadDir(set.Files, ".")
	if err != nil {
		return nil, fmt.Errorf("%s: read migrations: %w", set.HistoryTable, err)
	}
	migrations := make([]migrationFile, 0, len(entries))
	for _, entry := range entries {
		match := migrationFileName.FindStringSubmatch(entry.Name())
		if entry.IsDir() || match == nil {
			return nil, fmt.Errorf("%s: %q is not named NNNN_description.sql", set.HistoryTable, entry.Name())
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return nil, err
		}
		if expected := int64(len(migrations) + 1); version != expected {
			return nil, fmt.Errorf("%s: %s should be numbered %04d", set.HistoryTable, entry.Name(), expected)
		}
		content, err := fs.ReadFile(set.Files, entry.Name())
		if err != nil {
			return nil, err
		}
		checksum := sha256.Sum256(content)
		migrations = append(migrations, migrationFile{version: version, name: entry.Name(), sql: string(content), checksum: checksum[:]})
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("%s: no migrations", set.HistoryTable)
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
