package httpapi

import (
	"errors"
	"net/http"

	"islandBattles/lobby/domain"
)

type Problem struct {
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	// TeamIDs lists the teams the error is about.
	TeamIDs []string         `json:"team_ids,omitempty" doc:"The teams the error is about. For empty_teams, the teams that still need a player, in team order."`
	Code    domain.ErrorCode `json:"code" enum:"unauthenticated,not_found,not_host,lobby_full,team_full,phase_conflict,not_ready,empty_teams,stale_membership,idempotency_conflict,invalid_input,invalid_game_settings,invalid_seat_settings,game_requirement,game_running,watcher_kicked,watchers_full,unavailable,internal_error"`
	// Type is the game's problem type of game_requirement.
	Type string `json:"type,omitempty" doc:"For game_requirement, the game's own problem type, a URI that the game documents, such as one meaning a player has yet to choose something in the game's API. Absent for every other code."`
	// cause is the error the problem answers. Responses leave it out, but the
	// host's request log reads it through Unwrap.
	cause error
}

func (problem *Problem) Error() string             { return problem.Detail }
func (problem *Problem) Unwrap() error             { return problem.cause }
func (problem *Problem) GetStatus() int            { return problem.Status }
func (problem *Problem) ContentType(string) string { return "application/problem+json" }

func problemFor(errorValue error) error {
	code := domain.ErrorCodeOf(errorValue)
	status := http.StatusInternalServerError
	detail := "internal server error"
	switch code {
	case domain.CodeUnauthenticated:
		status, detail = http.StatusUnauthorized, "authentication required"
	case domain.CodeNotFound:
		status, detail = http.StatusNotFound, "lobby not found"
	case domain.CodeNotHost:
		status, detail = http.StatusForbidden, "host required"
	case domain.CodeWatcherKicked:
		status, detail = http.StatusForbidden, errorValue.Error()
	case domain.CodeLobbyFull, domain.CodeWatchersFull, domain.CodeTeamFull, domain.CodePhaseConflict, domain.CodeNotReady, domain.CodeEmptyTeams, domain.CodeStaleMembership, domain.CodeIdempotencyConflict, domain.CodeGameRunning:
		status, detail = http.StatusConflict, errorValue.Error()
	case domain.CodeInvalidInput:
		status, detail = http.StatusUnprocessableEntity, "invalid input"
	case domain.CodeInvalidGameSettings, domain.CodeInvalidSeatSettings:
		// Validators write their messages for the client.
		status, detail = http.StatusUnprocessableEntity, errorValue.Error()
	case domain.CodeGameRequirement:
		// The game wrote this message for the client.
		status, detail = http.StatusConflict, errorValue.Error()
	case domain.CodeUnavailable:
		status, detail = http.StatusServiceUnavailable, "service temporarily unavailable"
	default:
		code = "internal_error"
	}
	problem := &Problem{Title: http.StatusText(status), Status: status, Detail: detail, Code: code, cause: errorValue}
	var domainError *domain.Error
	if errors.As(errorValue, &domainError) {
		switch code {
		case domain.CodeEmptyTeams:
			problem.TeamIDs = domainError.TeamIDs
		case domain.CodeGameRequirement:
			problem.Type = domainError.Type
		}
	}
	return problem
}
