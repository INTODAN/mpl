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
	Code    domain.ErrorCode `json:"code" enum:"unauthenticated,not_found,not_host,lobby_full,team_full,phase_conflict,not_ready,empty_teams,stale_membership,idempotency_conflict,invalid_input,invalid_game_settings,unavailable,internal_error"`
}

func (problem *Problem) Error() string             { return problem.Detail }
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
	case domain.CodeLobbyFull, domain.CodeTeamFull, domain.CodePhaseConflict, domain.CodeNotReady, domain.CodeEmptyTeams, domain.CodeStaleMembership, domain.CodeIdempotencyConflict:
		status, detail = http.StatusConflict, errorValue.Error()
	case domain.CodeInvalidInput:
		status, detail = http.StatusUnprocessableEntity, "invalid input"
	case domain.CodeInvalidGameSettings:
		// Validators write their messages for the client.
		status, detail = http.StatusUnprocessableEntity, errorValue.Error()
	case domain.CodeUnavailable:
		status, detail = http.StatusServiceUnavailable, "service temporarily unavailable"
	default:
		code = "internal_error"
	}
	problem := &Problem{Title: http.StatusText(status), Status: status, Detail: detail, Code: code}
	var domainError *domain.Error
	if code == domain.CodeEmptyTeams && errors.As(errorValue, &domainError) {
		problem.TeamIDs = domainError.TeamIDs
	}
	return problem
}
