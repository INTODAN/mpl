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

// RoomRecord.GameSettings is written once at creation into the json column
// that migration 0003 adds; rooms created before it hold an empty object.
// LastConnectedAt is migration 0004's; it decides when an idle room is deleted.
// Capacity is the room's number of teams, which migration 0007 set for rooms
// created before a team held one member. Round is migration 0008's: the
// lobby's round, from 1, which goes up each time the lobby is reopened after
// a game; rooms created before it are in round 1, and a record created
// without a round gets that default. RoundStartedAt is migration 0009's: when
// the current round began, which the maximum age of retention counts from;
// rooms created before it have their creation time.
type RoomRecord struct {
	ID               string    `gorm:"column:id;type:uuid;primaryKey"`
	GameKey          string    `gorm:"column:game_key;not null"`
	Name             string    `gorm:"column:name;not null"`
	JoinCode         string    `gorm:"column:join_code;not null"`
	Capacity         int       `gorm:"column:capacity;not null"`
	Phase            string    `gorm:"column:phase;not null"`
	Round            int64     `gorm:"column:round;not null;default:1"`
	Version          int64     `gorm:"column:version;not null"`
	HostPlayerID     *string   `gorm:"column:host_player_id;type:uuid"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
	NextJoinSequence int64     `gorm:"column:next_join_sequence;not null"`
	GameSettings     []byte    `gorm:"column:game_settings;type:json"`
	LastConnectedAt  time.Time `gorm:"column:last_connected_at;not null"`
	RoundStartedAt   time.Time `gorm:"column:round_started_at;not null"`
}

func (RoomRecord) TableName() string { return "lobby_rooms" }

type TeamRecord struct {
	ID             string `gorm:"column:id;type:uuid;primaryKey"`
	LobbyID        string `gorm:"column:lobby_id;type:uuid;not null"`
	Name           string `gorm:"column:name;not null"`
	NormalizedName string `gorm:"column:normalized_name;not null"`
	TeamOrder      int    `gorm:"column:team_order;not null"`
}

func (TeamRecord) TableName() string { return "lobby_teams" }

// MembershipRecord.Kind and SeatSettings are migration 0005's: a seat that no
// person plays is kind "seat" with the game's settings, and a person is
// "human" with null settings. SeatRequestID is the request ID that added the
// seat, unique within its lobby, or null. All are written once, when the row is created.
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
	Kind              string     `gorm:"column:kind;not null;default:human"`
	SeatSettings      []byte     `gorm:"column:seat_settings;type:json"`
	SeatRequestID     *string    `gorm:"column:seat_request_id;type:uuid"`
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

// StartRosterRecord is the roster a start froze. It never changes while the
// lobby is started; reopening the lobby after the game deletes its rows, and
// the next start writes new ones. Its stable key is the room and source
// membership identity rather than a synthetic ID.
type StartRosterRecord struct {
	LobbyID   string `gorm:"column:lobby_id;type:uuid;primaryKey"`
	PlayerID  string `gorm:"column:player_id;type:uuid;not null"`
	TeamID    string `gorm:"column:team_id;type:uuid;not null"`
	MemberID  string `gorm:"column:member_id;type:uuid;primaryKey"`
	TeamOrder int    `gorm:"column:team_order;not null"`
}

func (StartRosterRecord) TableName() string { return "lobby_start_roster" }

// WatcherRecord is migration 0006's: a person who watches the lobby with its
// join code, outside its members. A watcher who leaves is deleted; one the
// host kicked keeps the row with KickedAt set. DisplayName is copied when they
// start watching, as a membership's is.
type WatcherRecord struct {
	LobbyID     string     `gorm:"column:lobby_id;type:uuid;primaryKey"`
	PlayerID    string     `gorm:"column:player_id;type:uuid;primaryKey"`
	DisplayName string     `gorm:"column:display_name;not null"`
	JoinedAt    time.Time  `gorm:"column:joined_at;not null"`
	KickedAt    *time.Time `gorm:"column:kicked_at"`
}

func (WatcherRecord) TableName() string { return "lobby_watchers" }

// CreateRequestRecord retains the original representation used to answer an
// idempotent create retry. LobbyID, from migration 0004, names the lobby it
// created, so the request is deleted with that lobby; it is null only for
// requests that an older build stored.
type CreateRequestRecord struct {
	PlayerID     string         `gorm:"column:player_id;type:uuid;primaryKey"`
	RequestID    string         `gorm:"column:request_id;type:uuid;primaryKey"`
	LobbyID      *string        `gorm:"column:lobby_id;type:uuid"`
	PayloadHash  []byte         `gorm:"column:payload_hash;type:bytea;not null"`
	ResponseJSON datatypes.JSON `gorm:"column:response_json;type:jsonb;not null"`
	CreatedAt    time.Time      `gorm:"column:created_at;not null"`
}

func (CreateRequestRecord) TableName() string { return "lobby_create_requests" }
