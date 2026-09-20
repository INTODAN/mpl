// Package postgres contains PostgreSQL adapters for the generic lobby domain.
// Importing it has no connection or migration side effects.
package postgres

import (
	"time"

	"gorm.io/datatypes"
)

// PlayerRecord is the durable identity label used by lobby sessions and members.
type PlayerRecord struct {
	ID          string    `gorm:"column:id;type:uuid;primaryKey"`
	DisplayName string    `gorm:"column:display_name;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
}

func (PlayerRecord) TableName() string { return "lobby_players" }

// SessionRecord deliberately stores only a digest of the bearer credential.
type SessionRecord struct {
	ID         string    `gorm:"column:id;type:uuid;primaryKey"`
	PlayerID   string    `gorm:"column:player_id;type:uuid;not null"`
	BearerHash []byte    `gorm:"column:bearer_hash;type:bytea;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;not null"`
	ExpiresAt  time.Time `gorm:"column:expires_at;not null"`
}

func (SessionRecord) TableName() string { return "lobby_sessions" }

type RoomRecord struct {
	ID               string    `gorm:"column:id;type:uuid;primaryKey"`
	GameKey          string    `gorm:"column:game_key;not null"`
	Name             string    `gorm:"column:name;not null"`
	JoinCode         string    `gorm:"column:join_code;not null"`
	Capacity         int       `gorm:"column:capacity;not null"`
	Phase            string    `gorm:"column:phase;not null"`
	Version          int64     `gorm:"column:version;not null"`
	HostPlayerID     *string   `gorm:"column:host_player_id;type:uuid"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
	NextJoinSequence int64     `gorm:"column:next_join_sequence;not null"`
}

func (RoomRecord) TableName() string { return "lobby_rooms" }

type TeamRecord struct {
	ID             string `gorm:"column:id;type:uuid;primaryKey"`
	LobbyID        string `gorm:"column:lobby_id;type:uuid;not null"`
	Name           string `gorm:"column:name;not null"`
	NormalizedName string `gorm:"column:normalized_name;not null"`
	Capacity       int    `gorm:"column:capacity;not null"`
	TeamOrder      int    `gorm:"column:team_order;not null"`
}

func (TeamRecord) TableName() string { return "lobby_teams" }

type MembershipRecord struct {
	ID                string     `gorm:"column:id;type:uuid;primaryKey"`
	LobbyID           string     `gorm:"column:lobby_id;type:uuid;not null"`
	PlayerID          string     `gorm:"column:player_id;type:uuid;not null"`
	DisplayName       string     `gorm:"column:display_name;not null"`
	Generation        int64      `gorm:"column:generation;not null"`
	Active            bool       `gorm:"column:active;not null"`
	TeamID            *string    `gorm:"column:team_id;type:uuid"`
	Ready             bool       `gorm:"column:ready;not null"`
	JoinedAt          time.Time  `gorm:"column:joined_at;not null"`
	JoinSequence      int64      `gorm:"column:join_sequence;not null"`
	DisconnectedUntil *time.Time `gorm:"column:disconnected_until"`
}

func (MembershipRecord) TableName() string { return "lobby_memberships" }

// LeaseRecord's composite identity keeps old generations available for grace
// calculation and audit after a membership is reactivated.
type LeaseRecord struct {
	LobbyID          string     `gorm:"column:lobby_id;type:uuid;primaryKey"`
	PlayerID         string     `gorm:"column:player_id;type:uuid;primaryKey"`
	Generation       int64      `gorm:"column:generation;primaryKey"`
	ClientInstanceID string     `gorm:"column:client_instance_id;type:uuid;primaryKey"`
	ExpiresAt        time.Time  `gorm:"column:expires_at;not null"`
	ReleasedAt       *time.Time `gorm:"column:released_at"`
}

func (LeaseRecord) TableName() string { return "lobby_leases" }

// StartRosterRecord is immutable after the lobby starts. Its stable key is the
// room and source membership identity rather than a synthetic ID.
type StartRosterRecord struct {
	LobbyID   string `gorm:"column:lobby_id;type:uuid;primaryKey"`
	PlayerID  string `gorm:"column:player_id;type:uuid;not null"`
	TeamID    string `gorm:"column:team_id;type:uuid;not null"`
	MemberID  string `gorm:"column:member_id;type:uuid;primaryKey"`
	TeamOrder int    `gorm:"column:team_order;not null"`
}

func (StartRosterRecord) TableName() string { return "lobby_start_roster" }

// CreateRequestRecord retains the original representation used to answer an
// idempotent create retry.
type CreateRequestRecord struct {
	PlayerID     string         `gorm:"column:player_id;type:uuid;primaryKey"`
	RequestID    string         `gorm:"column:request_id;type:uuid;primaryKey"`
	PayloadHash  []byte         `gorm:"column:payload_hash;type:bytea;not null"`
	ResponseJSON datatypes.JSON `gorm:"column:response_json;type:jsonb;not null"`
	CreatedAt    time.Time      `gorm:"column:created_at;not null"`
}

func (CreateRequestRecord) TableName() string { return "lobby_create_requests" }

// OutboxRecord stores both invalidation and revocation delivery work. Kind is
// constrained by the schema; payload fields are selected by that discriminator.
type OutboxRecord struct {
	ID             string     `gorm:"column:id;type:uuid;primaryKey"`
	Kind           string     `gorm:"column:kind;not null"`
	EventID        string     `gorm:"column:event_id;type:uuid;not null"`
	GameKey        string     `gorm:"column:game_key;not null"`
	LobbyID        *string    `gorm:"column:lobby_id;type:uuid"`
	PlayerID       *string    `gorm:"column:player_id;type:uuid"`
	Generation     *int64     `gorm:"column:generation"`
	Version        *int64     `gorm:"column:version"`
	Attempts       int        `gorm:"column:attempts;not null"`
	NextAttemptAt  time.Time  `gorm:"column:next_attempt_at;not null"`
	ClaimToken     *string    `gorm:"column:claim_token;type:uuid"`
	ClaimDeadline  *time.Time `gorm:"column:claim_deadline"`
	AcknowledgedAt *time.Time `gorm:"column:acknowledged_at"`
	LastError      string     `gorm:"column:last_error;not null"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
}

func (OutboxRecord) TableName() string { return "lobby_outbox" }

// LegacyRecord is an archive boundary. It retains source JSON exactly enough
// for the task 11 backfill without claiming legacy identities as lobby users.
type LegacyRecord struct {
	ID               string         `gorm:"column:id;type:uuid;primaryKey"`
	SourceTable      string         `gorm:"column:source_table;not null"`
	SourcePrimaryKey string         `gorm:"column:source_primary_key;not null"`
	OriginalRowJSON  datatypes.JSON `gorm:"column:original_row_json;type:jsonb;not null"`
	Checksum         string         `gorm:"column:checksum;not null"`
	SourceCreatedAt  *time.Time     `gorm:"column:source_created_at"`
	SourceUpdatedAt  *time.Time     `gorm:"column:source_updated_at"`
	SourceDeletedAt  *time.Time     `gorm:"column:source_deleted_at"`
	ArchivedAt       time.Time      `gorm:"column:archived_at;not null"`
	MigrationVersion int64          `gorm:"column:migration_version;not null"`
}

func (LegacyRecord) TableName() string { return "lobby_legacy_records" }
