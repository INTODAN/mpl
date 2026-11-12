// Package domain defines game-independent membership contracts and domain logic.
// Adapters depend on this package; this package never imports an adapter or backend.
package domain

import (
	"encoding/json"
	"time"
)

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
	ID       string `json:"id"`
	GameKey  string `json:"game_key"`
	Name     string `json:"name"`
	JoinCode string `json:"join_code"`
	// Capacity is the number of teams, which is the number of members the
	// lobby holds, since a team holds at most one member.
	Capacity int   `json:"capacity"`
	Phase    Phase `json:"phase"`
	// Round numbers the lobby's games, from 1. Reopening a started lobby
	// after its game ended raises it by one and makes the lobby wait again,
	// so a game tells the lobby's games apart by lobby and round.
	Round        int64     `json:"round"`
	Version      int64     `json:"version"`
	HostPlayerID *string   `json:"host_player_id"`
	CreatedAt    time.Time `json:"created_at"`
	// GameSettings is the canonical JSON object chosen at creation. The host
	// can replace it, with the teams, while the lobby waits. The lobby stores
	// it without reading it.
	GameSettings json.RawMessage `json:"game_settings"`
	// NextJoinSequence is persisted, including when older memberships deactivate.
	NextJoinSequence int64 `json:"-"`
	// LastConnectedAt is the last time a client showed it was connected, by
	// creating, joining, heartbeating, releasing a lease or leaving. The
	// retention pass deletes the lobby once it is older than the idle period.
	LastConnectedAt time.Time `json:"-"`
	// RoundStartedAt is when the lobby's current round began: its creation
	// for round 1, and the time of the last reopen for later rounds. The
	// retention pass counts the maximum age from it. It is not part of the
	// public snapshot.
	RoundStartedAt time.Time `json:"-"`
}

func (room Lobby) Scope() Scope { return Scope{GameKey: room.GameKey, LobbyID: room.ID} }

// MarkConnected records that a client was connected at now. It never moves
// the time back, so a slower clock on another instance can't shorten it.
func (room *Lobby) MarkConnected(now time.Time) {
	if now.After(room.LastConnectedAt) {
		room.LastConnectedAt = now.UTC()
	}
}

// Team is one side of a lobby. It holds at most one active member: one
// person or one seat.
type Team struct {
	ID      string `json:"id"`
	LobbyID string `json:"lobby_id"`
	Name    string `json:"name"`
	Order   int    `json:"order"`
}

// MemberKind says who takes a member's place in the lobby.
type MemberKind string

const (
	// MemberHuman is a person with a guest session, who joined the lobby.
	MemberHuman MemberKind = "human"
	// MemberSeat is a seat that no person plays, such as a game's bot, which
	// the host added to a team. It is always ready, holds no lease and never
	// hosts, and the game plays it from its seat settings.
	MemberSeat MemberKind = "seat"
)

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
	// Kind is MemberHuman, or MemberSeat for a seat that the host added. An
	// empty kind is a human.
	Kind MemberKind `json:"kind"`
	// SeatSettings is the canonical JSON object that the game checked when
	// the host added the seat, and nil for a human. The lobby stores it
	// without reading it.
	SeatSettings json.RawMessage `json:"seat_settings"`
	// MemberSettings is the canonical JSON object that the game checked when
	// the person last set their own settings for the game, such as what plays
	// for them, or nil when they set none. It stays when the person leaves and
	// rejoins, and when the lobby is reopened for its next round. The lobby
	// stores it without reading it.
	MemberSettings json.RawMessage `json:"member_settings"`
	// SeatRequestID is the request ID that added the seat, or empty when it
	// had none. It never leaves the lobby.
	SeatRequestID string `json:"-"`
}

// IsSeat reports whether no person plays the member.
func (member Member) IsSeat() bool { return member.Kind == MemberSeat }

// Watcher is a person outside the lobby's members who watches it with the
// join code. A watcher takes no seat and counts toward nothing: not capacity,
// the start roster, leases, grace or retention. The game decides what a
// watcher may read. A watcher who leaves is removed; one the host kicked
// stays, with KickedAt set, so the code doesn't let them back in.
type Watcher struct {
	PlayerID    string     `json:"player_id"`
	DisplayName string     `json:"display_name"`
	JoinedAt    time.Time  `json:"joined_at"`
	KickedAt    *time.Time `json:"kicked_at"`
}

// MaxWatchersPerLobby caps the watchers a lobby can have at once, kicked and
// departed ones left out. A new watcher beyond it is refused with
// watchers_full; one who already watches keeps their row. Raise or lower it
// here; see the lobby README.
const MaxWatchersPerLobby = 50

// Watching reports whether the watcher may still watch.
func (watcher Watcher) Watching() bool { return watcher.KickedAt == nil }

// WatchAccess is what a person who asked to watch a lobby receives: the
// lobby, and the channel its game publishes on for watchers. Member is true
// for an active member, who keeps their member access instead of becoming a
// watcher; the watch channel is open to them too.
type WatchAccess struct {
	Scope   Scope  `json:"scope"`
	Channel string `json:"channel"`
	Member  bool   `json:"member"`
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
	// Watchers holds every watcher row, kicked ones included.
	Watchers []Watcher
}
type Snapshot struct {
	Lobby                Lobby       `json:"lobby"`
	Teams                []Team      `json:"teams"`
	Members              []Member    `json:"members"`
	StartRoster          StartRoster `json:"start_roster"`
	MembershipGeneration int64       `json:"membership_generation"`
	Channel              string      `json:"channel"`
	// StartRules are the game's declared start conditions. The service fills
	// them from the policy; snapshots built elsewhere leave them zero.
	StartRules StartRules `json:"start_rules"`
	// WatcherCount counts the watchers who may watch, kicked ones left out.
	WatcherCount int `json:"watcher_count"`
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

// RevocationIntent unsubscribes a player from the room channel once the change
// that deactivated this membership generation has committed. With Watch set,
// it unsubscribes a watcher who left or was kicked from the watch channel
// instead, and Generation is 0.
type RevocationIntent struct {
	PlayerID   string `json:"player_id"`
	Scope      Scope  `json:"scope"`
	Generation int64  `json:"generation"`
	Watch      bool   `json:"watch"`
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
