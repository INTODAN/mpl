package httpapi

import (
	"islandBattles/lobby"
	"net/http"
)

type Problem struct {
	Title  string          `json:"title"`
	Status int             `json:"status"`
	Detail string          `json:"detail"`
	Code   lobby.ErrorCode `json:"code" enum:"unauthenticated,not_found,not_host,lobby_full,team_full,phase_conflict,not_ready,stale_membership,idempotency_conflict,invalid_input,unavailable,internal_error"`
}

func (problem *Problem) Error() string             { return problem.Detail }
func (problem *Problem) GetStatus() int            { return problem.Status }
func (problem *Problem) ContentType(string) string { return "application/problem+json" }

func problemFor(errorValue error) error {
	code := lobby.ErrorCodeOf(errorValue)
	status := http.StatusInternalServerError
	detail := "internal server error"
	switch code {
	case lobby.CodeUnauthenticated:
		status, detail = http.StatusUnauthorized, "authentication required"
	case lobby.CodeNotFound:
		status, detail = http.StatusNotFound, "lobby not found"
	case lobby.CodeNotHost:
		status, detail = http.StatusForbidden, "host required"
	case lobby.CodeLobbyFull, lobby.CodeTeamFull, lobby.CodePhaseConflict, lobby.CodeNotReady, lobby.CodeStaleMembership, lobby.CodeIdempotencyConflict:
		status, detail = http.StatusConflict, errorValue.Error()
	case lobby.CodeInvalidInput:
		status, detail = http.StatusUnprocessableEntity, "invalid input"
	case lobby.CodeUnavailable:
		status, detail = http.StatusServiceUnavailable, "service temporarily unavailable"
	default:
		code = "internal_error"
	}
	return &Problem{Title: http.StatusText(status), Status: status, Detail: detail, Code: code}
}
