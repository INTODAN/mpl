// Package domain defines game-independent membership contracts and domain logic.
// Adapters depend on this package; this package never imports an adapter or backend.
package domain

import "time"

type Phase string

const (
	PhaseWaiting Phase = "waiting"
	PhaseStarted Phase = "started"
	PhaseClosed  Phase = "closed"
)

type Scope struct {
	GameKey string `json:"game_key"`
	LobbyID string `json:"lobby_id"`
}
type Principal struct {
	PlayerID    string `json:"player_id"`
	SessionID   string `json:"-"`
	DisplayName string `json:"display_name"`
}
type Lobby struct {
	ID           string    `json:"id"`
	GameKey      string    `json:"game_key"`
	Name         string    `json:"name"`
	JoinCode     string    `json:"join_code"`
	Capacity     int       `json:"capacity"`
	Phase        Phase     `json:"phase"`
	Version      int64     `json:"version"`
	HostPlayerID *string   `json:"host_player_id"`
	CreatedAt    time.Time `json:"created_at"`
	// NextJoinSequence is persisted, including when older memberships deactivate.
	NextJoinSequence int64 `json:"-"`
}

func (room Lobby) Scope() Scope { return Scope{GameKey: room.GameKey, LobbyID: room.ID} }

type Team struct {
	ID       string `json:"id"`
	LobbyID  string `json:"lobby_id"`
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
	Order    int    `json:"order"`
}
type Member struct {
	ID                string     `json:"id"`
	PlayerID          string     `json:"player_id"`
	DisplayName       string     `json:"display_name"`
	Generation        int64      `json:"generation"`
	Active            bool       `json:"active"`
	TeamID            *string    `json:"team_id"`
	Ready             bool       `json:"ready"`
	JoinedAt          time.Time  `json:"joined_at"`
	JoinSequence      int64      `json:"join_sequence"`
	DisconnectedUntil *time.Time `json:"disconnected_until"`
}
type Lease struct {
	LobbyID          string     `json:"lobby_id"`
	PlayerID         string     `json:"player_id"`
	Generation       int64      `json:"generation"`
	ClientInstanceID string     `json:"client_instance_id"`
	ExpiresAt        time.Time  `json:"expires_at"`
	ReleasedAt       *time.Time `json:"released_at"`
}
type StartRosterEntry struct {
	PlayerID  string `json:"player_id"`
	TeamID    string `json:"team_id"`
	MemberID  string `json:"member_id"`
	TeamOrder int    `json:"team_order"`
}
type StartRoster []StartRosterEntry

type Aggregate struct {
	Lobby       Lobby
	Teams       []Team
	Members     []Member
	Leases      []Lease
	StartRoster StartRoster
}
type Snapshot struct {
	Lobby                Lobby       `json:"lobby"`
	Teams                []Team      `json:"teams"`
	Members              []Member    `json:"members"`
	StartRoster          StartRoster `json:"start_roster"`
	MembershipGeneration int64       `json:"membership_generation"`
	Channel              string      `json:"channel"`
}

const InvalidationSchemaVersion = 1
const InvalidationType = "lobby.changed"

type Invalidation struct {
	SchemaVersion int    `json:"schema_version"`
	EventID       string `json:"event_id"`
	GameKey       string `json:"game_key"`
	LobbyID       string `json:"lobby_id"`
	Version       int64  `json:"version"`
	Type          string `json:"type"`
}

// RevocationIntent has no random identifier: the store assigns EventID atomically.
type RevocationIntent struct {
	PlayerID   string `json:"player_id"`
	Scope      Scope  `json:"scope"`
	Generation int64  `json:"generation"`
}
type Revocation struct {
	EventID    string `json:"event_id"`
	PlayerID   string `json:"player_id"`
	Scope      Scope  `json:"scope"`
	Generation int64  `json:"generation"`
}

// Change classifies persistence. Either flag or an intent requires persistence;
// only PublicChanged increments the room version and emits an invalidation.
type Change struct {
	PublicChanged  bool
	PrivateChanged bool
	Revocations    []RevocationIntent
}

func (change Change) HasWrites() bool {
	return change.PublicChanged || change.PrivateChanged || len(change.Revocations) > 0
}

type Player struct {
	ID          string
	DisplayName string
	CreatedAt   time.Time
}

// CredentialHash is a SHA-256 bearer digest, never the raw bearer.
type CredentialHash [32]byte
type Session struct {
	ID         string
	PlayerID   string
	BearerHash CredentialHash
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// SessionVerification carries stored identity and expiry. Callers must verify
// now < ExpiresAt; a repository lookup alone is not authentication.
type SessionVerification struct {
	Principal Principal
	ExpiresAt time.Time
}
type GuestSession struct {
	PlayerID    string    `json:"player_id"`
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}
type ConnectionToken struct {
	Token        string    `json:"token"`
	ExpiresAt    time.Time `json:"expires_at"`
	WebsocketURL string    `json:"websocket_url"`
}
type SubscriptionAuthorization struct{ AllowedUntil time.Time }
type JobKind string

const (
	JobInvalidation JobKind = "invalidation"
	JobRevocation   JobKind = "revocation"
)

// DeliveryJob is a discriminated union: exactly the payload matching Kind is set.
// ID and payload EventID are stable across attempts. Claim tokens are not event IDs.
type DeliveryJob struct {
	ID            string
	Kind          JobKind
	Invalidation  *Invalidation
	Revocation    *Revocation
	ClaimToken    string
	ClaimDeadline time.Time
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
}
type ClaimRequest struct {
	Now           time.Time
	Limit         int
	ClaimDuration time.Duration
}
type RetryRequest struct {
	JobID         string
	ClaimToken    string
	Now           time.Time
	NextAttemptAt time.Time
	// ErrorSummary must be sanitized; never persist credentials or raw SQL errors.
	ErrorSummary string
}
type MembershipState struct {
	Generation int64
	Active     bool
}
