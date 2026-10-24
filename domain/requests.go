package domain

import (
	"encoding/json"
	"time"
)

type TeamDefinition struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}
type CreateRequest struct {
	RequestID        string           `json:"request_id"`
	GameKey          string           `json:"game_key"`
	Name             string           `json:"name"`
	Capacity         int              `json:"capacity"`
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
