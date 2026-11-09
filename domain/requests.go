package domain

import (
	"encoding/json"
	"time"
)

// TeamDefinition names one team of a new lobby. A team holds at most one
// member: one person or one seat.
type TeamDefinition struct {
	Name string `json:"name"`
}
type CreateRequest struct {
	RequestID        string           `json:"request_id"`
	GameKey          string           `json:"game_key"`
	Name             string           `json:"name"`
	Teams            []TeamDefinition `json:"teams"`
	ClientInstanceID string           `json:"client_instance_id"`
	// GameSettings is an optional JSON object that only the game reads.
	// omitempty keeps the create fingerprint of a request without settings the
	// same as before settings existed.
	GameSettings json.RawMessage `json:"game_settings,omitempty"`
}
type JoinRequest struct {
	GameKey          string `json:"game_key"`
	JoinCode         string `json:"join_code"`
	ClientInstanceID string `json:"client_instance_id"`
}

// HTTP adapters must reject omitted team_id before constructing this request.
// TeamID=nil means an explicit request to clear the selected team.
type SelectTeamRequest struct {
	TeamID *string `json:"team_id"`
}

// HTTP adapters must reject omitted ready; false is a valid explicit value.
type ReadyRequest struct {
	Ready bool `json:"ready"`
}

// AddSeatRequest adds a seat that no person plays to a team. SeatSettings is
// a JSON object that only the game reads, such as the level of the bot that
// plays the seat. RequestID, optional, makes a retry safe: repeating it with
// the same team and settings adds no second seat, and with others fails with
// idempotency_conflict.
type AddSeatRequest struct {
	RequestID    string          `json:"request_id,omitempty"`
	TeamID       string          `json:"team_id"`
	SeatSettings json.RawMessage `json:"seat_settings"`
}

// WatchRequest asks to watch the lobby of a game that has this join code.
type WatchRequest struct {
	GameKey  string `json:"game_key"`
	JoinCode string `json:"join_code"`
}
type HeartbeatRequest struct {
	ClientInstanceID     string `json:"client_instance_id"`
	MembershipGeneration int64  `json:"membership_generation"`
}
type ReleaseLeaseRequest struct {
	ClientInstanceID     string `json:"client_instance_id"`
	MembershipGeneration int64  `json:"membership_generation"`
}
type HeartbeatResult struct {
	Version              int64      `json:"version"`
	MembershipGeneration int64      `json:"membership_generation"`
	LeaseExpiresAt       time.Time  `json:"lease_expires_at"`
	DisconnectedUntil    *time.Time `json:"disconnected_until"`
}
type CreateGuestRequest struct {
	DisplayName string `json:"display_name"`
}
