// Package gamehooks lets the lobby call a game over HTTP, and a game read the
// lobby over HTTP, so that the two can run as separate services.
//
// The lobby calls a game's hooks with POST {hooks_url}/<hook>, JSON in and
// out, each signed with the game's shared secret (Sign, Verify). Client
// implements a domain.GameRegistration whose hooks make those calls, and
// NewHandler serves the hooks of a domain.GameRegistration that a game
// implements in Go. A game reads the lobby through the signed game API with
// GameAPIClient. ParseGames reads the games a lobby calls from its LOBBY_GAMES
// configuration. The lobby README ("Binding a game over HTTP") describes the
// protocol.
package gamehooks

import (
	"encoding/json"

	"islandBattles/lobby/domain"
)

// The hooks a game serves under its hooks URL.
const (
	// HookValidateSettings checks a lobby's game settings:
	// ValidateSettingsRequest, answered with 204, or 422 to refuse them.
	HookValidateSettings = "validate-settings"
	// HookValidateSeat checks a seat's settings and names the seat:
	// ValidateSeatRequest, answered with ValidateSeatResponse, or 422.
	HookValidateSeat = "validate-seat"
	// HookValidateMemberSettings checks a person's member settings:
	// ValidateMemberSettingsRequest, answered with 204, or 422.
	HookValidateMemberSettings = "validate-member-settings"
	// HookReadyCheck decides whether members may be ready:
	// ReadyCheckRequest, answered with 204, or 409 with the problem code
	// game_requirement, whose type and detail reach the player unchanged.
	HookReadyCheck = "ready-check"
	// HookStart creates the game of a started lobby's round: StartRequest,
	// answered with 200 and StartResponse. It must be idempotent by lobby
	// and round.
	HookStart = "start"
	// HookRoundEnded tells whether the game of a lobby's round has ended:
	// RoundEndedRequest, answered with RoundEndedResponse.
	HookRoundEnded = "round-ended"
)

// Hooks lists every hook, in the order the lobby's life cycle reaches them.
var Hooks = []string{HookValidateSettings, HookValidateSeat, HookValidateMemberSettings, HookReadyCheck, HookStart, HookRoundEnded}

// ValidateSettingsRequest is the body of validate-settings. LobbyID is absent
// while the lobby is being created, and Settings is `{}` when the request
// had none.
type ValidateSettingsRequest struct {
	LobbyID  string                  `json:"lobby_id,omitempty"`
	Settings json.RawMessage         `json:"settings"`
	Teams    []domain.TeamDefinition `json:"teams"`
}

// ValidateSeatRequest is the body of validate-seat.
type ValidateSeatRequest struct {
	LobbyID  string          `json:"lobby_id"`
	Settings json.RawMessage `json:"settings"`
}

// ValidateSeatResponse names the seat that validate-seat accepted, in at
// most 40 characters.
type ValidateSeatResponse struct {
	DisplayName string `json:"display_name"`
}

// ValidateMemberSettingsRequest is the body of validate-member-settings.
type ValidateMemberSettingsRequest struct {
	LobbyID  string          `json:"lobby_id"`
	Settings json.RawMessage `json:"settings"`
}

// ReadyCheckRequest is the body of ready-check: the one member getting
// ready, with AtStart false, or the start roster's people in the lobby's
// member order, with AtStart true.
type ReadyCheckRequest struct {
	LobbyID string        `json:"lobby_id"`
	Members []ReadyMember `json:"members"`
	AtStart bool          `json:"at_start"`
}

// ReadyMember is a member that ready-check decides about. Settings are the
// member's member settings, or null when they set none.
type ReadyMember struct {
	PlayerID    string          `json:"player_id"`
	DisplayName string          `json:"display_name"`
	Settings    json.RawMessage `json:"settings"`
}

// StartRequest is the body of start: the started lobby as the game reads it
// through the game API.
type StartRequest struct {
	Snapshot domain.Snapshot `json:"snapshot"`
}

// StartResponse names the game that start created, or found, for the round.
type StartResponse struct {
	GameID string `json:"game_id"`
}

// RoundEndedRequest is the body of round-ended.
type RoundEndedRequest struct {
	LobbyID string `json:"lobby_id"`
	Round   int64  `json:"round"`
}

// RoundEndedResponse says whether the game of the round has ended. A round
// whose start created no game counts as ended, so the lobby can't be stuck.
type RoundEndedResponse struct {
	Ended bool `json:"ended"`
}

// AuthorizeWatchRequest is the body of the game API's authorize-watch.
type AuthorizeWatchRequest struct {
	PlayerID string `json:"player_id" format:"uuid" doc:"The player who asks to watch, as the lobby names them."`
}

// Problem is the application/problem+json body of a refusal, from a hook or
// from the game API. Detail of a hook's 422 and 409 is shown to the player.
type Problem struct {
	Title  string `json:"title,omitempty"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code,omitempty"`
	// Type is the game's problem type of the code game_requirement.
	Type string `json:"type,omitempty"`
}

// ProblemContentType is the media type of a Problem.
const ProblemContentType = "application/problem+json"

// MaxBodyBytes bounds the body of a hook call, its answer, and a game API
// request.
const MaxBodyBytes = 1 << 20

// normalizeSnapshot turns the JSON nulls that a snapshot's raw JSON fields
// decode into back into nil, as the lobby builds them.
func normalizeSnapshot(snapshot *domain.Snapshot) {
	snapshot.Lobby.GameSettings = nilIfNull(snapshot.Lobby.GameSettings)
	for index := range snapshot.Members {
		member := &snapshot.Members[index]
		member.SeatSettings = nilIfNull(member.SeatSettings)
		member.MemberSettings = nilIfNull(member.MemberSettings)
	}
}

func nilIfNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}
