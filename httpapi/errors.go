package httpapi

import (
	"islandBattles/lobby/domain"
	"net/http"
)

type Problem struct {
	Title  string           `json:"title"`
	Status int              `json:"status"`
	Detail string           `json:"detail"`
	Code   domain.ErrorCode `json:"code" enum:"unauthenticated,not_found,not_host,lobby_full,team_full,phase_conflict,not_ready,stale_membership,idempotency_conflict,invalid_input,unavailable,internal_error"`
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
	case domain.CodeLobbyFull, domain.CodeTeamFull, domain.CodePhaseConflict, domain.CodeNotReady, domain.CodeStaleMembership, domain.CodeIdempotencyConflict:
		status, detail = http.StatusConflict, errorValue.Error()
	case domain.CodeInvalidInput:
		status, detail = http.StatusUnprocessableEntity, "invalid input"
	case domain.CodeUnavailable:
		status, detail = http.StatusServiceUnavailable, "service temporarily unavailable"
	default:
		code = "internal_error"
	}
	return &Problem{Title: http.StatusText(status), Status: status, Detail: detail, Code: code}
}
