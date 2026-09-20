package postgres

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// SchemaTables is the complete generic-lobby schema. It intentionally excludes
// all Island Battles tables and task 11's data backfill.
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
	"lobby_legacy_records",
}

// InitializeSchema creates the generic lobby schema atomically and may safely
// be called more than once. It does not open a connection or run any backfill.
func InitializeSchema(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return errors.New("lobby postgres: nil database")
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.AutoMigrate(
			&PlayerRecord{},
			&SessionRecord{},
			&RoomRecord{},
			&TeamRecord{},
			&MembershipRecord{},
			&LeaseRecord{},
			&StartRosterRecord{},
			&CreateRequestRecord{},
			&OutboxRecord{},
			&LegacyRecord{},
		); err != nil {
			return err
		}

		for _, statement := range schemaStatements {
			if err := executeSchemaStatement(tx, statement); err != nil {
				return err
			}
		}
		return nil
	})
}

func executeSchemaStatement(tx *gorm.DB, statement string) error {
	// PostgreSQL has CREATE ... IF NOT EXISTS for indexes but no equivalent for
	// constraints. Looking up the fixed constraint name makes initialization
	// repeatable without interpolating application data into DDL.
	if strings.HasPrefix(statement, "ALTER TABLE ") {
		fields := strings.Fields(statement)
		if len(fields) < 6 || fields[3] != "ADD" || fields[4] != "CONSTRAINT" {
			return errors.New("lobby postgres: malformed fixed constraint DDL")
		}
		var exists bool
		if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = ?)`, fields[5]).Scan(&exists).Error; err != nil {
			return err
		}
		if exists {
			return nil
		}
	}
	return tx.Exec(statement).Error
}

// schemaStatements are fixed PostgreSQL DDL. GORM owns record mapping while
// these declarations make database invariants explicit and reviewable.
var schemaStatements = []string{
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_lobby_sessions_player ON lobby_sessions (player_id)`,
	`CREATE INDEX IF NOT EXISTS ix_lobby_sessions_expires_at ON lobby_sessions (expires_at)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_lobby_rooms_game_join_code ON lobby_rooms (game_key, join_code)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_lobby_teams_lobby_id_id ON lobby_teams (lobby_id, id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_lobby_teams_lobby_order ON lobby_teams (lobby_id, team_order)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_lobby_teams_lobby_normalized_name ON lobby_teams (lobby_id, normalized_name)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_lobby_memberships_lobby_player ON lobby_memberships (lobby_id, player_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_lobby_memberships_lobby_id_id ON lobby_memberships (lobby_id, id)`,
	`CREATE INDEX IF NOT EXISTS ix_lobby_memberships_scope ON lobby_memberships (lobby_id, active, join_sequence)`,
	`CREATE INDEX IF NOT EXISTS ix_lobby_leases_deadline ON lobby_leases (lobby_id, expires_at)`,
	`CREATE INDEX IF NOT EXISTS ix_lobby_leases_scope ON lobby_leases (lobby_id, player_id, generation)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_lobby_start_roster_lobby_player ON lobby_start_roster (lobby_id, player_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_lobby_outbox_event_id ON lobby_outbox (event_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_lobby_outbox_invalidation_version ON lobby_outbox (lobby_id, version) WHERE kind = 'invalidation'`,
	`CREATE INDEX IF NOT EXISTS ix_lobby_outbox_pending_claim ON lobby_outbox (next_attempt_at, claim_deadline) WHERE acknowledged_at IS NULL`,
	`CREATE INDEX IF NOT EXISTS ix_lobby_outbox_scope ON lobby_outbox (lobby_id, kind, acknowledged_at)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_lobby_legacy_source ON lobby_legacy_records (source_table, source_primary_key)`,

	`ALTER TABLE lobby_sessions ADD CONSTRAINT fk_lobby_sessions_player FOREIGN KEY (player_id) REFERENCES lobby_players (id)`,
	`ALTER TABLE lobby_rooms ADD CONSTRAINT fk_lobby_rooms_host FOREIGN KEY (host_player_id) REFERENCES lobby_players (id)`,
	`ALTER TABLE lobby_teams ADD CONSTRAINT fk_lobby_teams_room FOREIGN KEY (lobby_id) REFERENCES lobby_rooms (id)`,
	`ALTER TABLE lobby_memberships ADD CONSTRAINT fk_lobby_memberships_room FOREIGN KEY (lobby_id) REFERENCES lobby_rooms (id)`,
	`ALTER TABLE lobby_memberships ADD CONSTRAINT fk_lobby_memberships_player FOREIGN KEY (player_id) REFERENCES lobby_players (id)`,
	`ALTER TABLE lobby_memberships ADD CONSTRAINT fk_lobby_memberships_team FOREIGN KEY (lobby_id, team_id) REFERENCES lobby_teams (lobby_id, id)`,
	`ALTER TABLE lobby_leases ADD CONSTRAINT fk_lobby_leases_membership FOREIGN KEY (lobby_id, player_id) REFERENCES lobby_memberships (lobby_id, player_id)`,
	`ALTER TABLE lobby_start_roster ADD CONSTRAINT fk_lobby_start_roster_room FOREIGN KEY (lobby_id) REFERENCES lobby_rooms (id)`,
	`ALTER TABLE lobby_start_roster ADD CONSTRAINT fk_lobby_start_roster_player FOREIGN KEY (player_id) REFERENCES lobby_players (id)`,
	`ALTER TABLE lobby_start_roster ADD CONSTRAINT fk_lobby_start_roster_team FOREIGN KEY (lobby_id, team_id) REFERENCES lobby_teams (lobby_id, id)`,
	`ALTER TABLE lobby_start_roster ADD CONSTRAINT fk_lobby_start_roster_member FOREIGN KEY (lobby_id, member_id) REFERENCES lobby_memberships (lobby_id, id)`,
	`ALTER TABLE lobby_create_requests ADD CONSTRAINT fk_lobby_create_requests_player FOREIGN KEY (player_id) REFERENCES lobby_players (id)`,
	`ALTER TABLE lobby_outbox ADD CONSTRAINT fk_lobby_outbox_room FOREIGN KEY (lobby_id) REFERENCES lobby_rooms (id)`,
	`ALTER TABLE lobby_outbox ADD CONSTRAINT fk_lobby_outbox_player FOREIGN KEY (player_id) REFERENCES lobby_players (id)`,

	`ALTER TABLE lobby_sessions ADD CONSTRAINT ck_lobby_sessions_bearer_hash CHECK (octet_length(bearer_hash) = 32)`,
	`ALTER TABLE lobby_rooms ADD CONSTRAINT ck_lobby_rooms_capacity CHECK (capacity BETWEEN 2 AND 64)`,
	`ALTER TABLE lobby_rooms ADD CONSTRAINT ck_lobby_rooms_phase CHECK (phase IN ('waiting', 'started', 'closed'))`,
	`ALTER TABLE lobby_rooms ADD CONSTRAINT ck_lobby_rooms_version CHECK (version > 0)`,
	`ALTER TABLE lobby_rooms ADD CONSTRAINT ck_lobby_rooms_next_join_sequence CHECK (next_join_sequence > 0)`,
	`ALTER TABLE lobby_teams ADD CONSTRAINT ck_lobby_teams_capacity CHECK (capacity BETWEEN 1 AND 64)`,
	`ALTER TABLE lobby_teams ADD CONSTRAINT ck_lobby_teams_order CHECK (team_order > 0)`,
	`ALTER TABLE lobby_memberships ADD CONSTRAINT ck_lobby_memberships_generation CHECK (generation > 0)`,
	`ALTER TABLE lobby_memberships ADD CONSTRAINT ck_lobby_memberships_join_sequence CHECK (join_sequence > 0)`,
	`ALTER TABLE lobby_leases ADD CONSTRAINT ck_lobby_leases_generation CHECK (generation > 0)`,
	`ALTER TABLE lobby_start_roster ADD CONSTRAINT ck_lobby_start_roster_team_order CHECK (team_order > 0)`,
	`ALTER TABLE lobby_create_requests ADD CONSTRAINT ck_lobby_create_requests_hash CHECK (octet_length(payload_hash) = 32)`,
	`ALTER TABLE lobby_outbox ADD CONSTRAINT ck_lobby_outbox_kind CHECK (kind IN ('invalidation', 'revocation'))`,
	`ALTER TABLE lobby_outbox ADD CONSTRAINT ck_lobby_outbox_attempts CHECK (attempts >= 0)`,
	`ALTER TABLE lobby_outbox ADD CONSTRAINT ck_lobby_outbox_payload CHECK ((kind = 'invalidation' AND lobby_id IS NOT NULL AND version IS NOT NULL AND player_id IS NULL AND generation IS NULL) OR (kind = 'revocation' AND lobby_id IS NOT NULL AND player_id IS NOT NULL AND generation IS NOT NULL))`,
	`ALTER TABLE lobby_legacy_records ADD CONSTRAINT ck_lobby_legacy_records_migration_version CHECK (migration_version > 0)`,
}
