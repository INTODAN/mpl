package domain

import "errors"

type ErrorCode string

const (
	CodeUnauthenticated     ErrorCode = "unauthenticated"
	CodeNotFound            ErrorCode = "not_found"
	CodeNotHost             ErrorCode = "not_host"
	CodeLobbyFull           ErrorCode = "lobby_full"
	CodeTeamFull            ErrorCode = "team_full"
	CodePhaseConflict       ErrorCode = "phase_conflict"
	CodeNotReady            ErrorCode = "not_ready"
	CodeEmptyTeams          ErrorCode = "empty_teams"
	CodeStaleMembership     ErrorCode = "stale_membership"
	CodeIdempotencyConflict ErrorCode = "idempotency_conflict"
	CodeInvalidInput        ErrorCode = "invalid_input"
	CodeInvalidGameSettings ErrorCode = "invalid_game_settings"
	CodeUnavailable         ErrorCode = "unavailable"
	// PermissionDenied is private proxy authorization only, not a player API code.
	CodePermissionDenied ErrorCode = "permission_denied"
)

// Error exposes a safe message and stable code. Cause is for internal diagnosis;
// HTTP adapters must never serialize Cause or an arbitrary wrapped error.
type Error struct {
	Code    ErrorCode
	Message string
	// TeamIDs names the teams an error is about, such as the empty teams of
	// CodeEmptyTeams. They are safe to show the client.
	TeamIDs []string
	Cause   error
}

func (err *Error) Error() string { return err.Message }
func (err *Error) Unwrap() error { return err.Cause }
func (err *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && err.Code == other.Code
}
func NewError(code ErrorCode, message string) *Error { return &Error{Code: code, Message: message} }
func ErrorCodeOf(err error) ErrorCode {
	var domainError *Error
	if errors.As(err, &domainError) {
		return domainError.Code
	}
	return ""
}

var (
	ErrUnauthenticated     = NewError(CodeUnauthenticated, "authentication required")
	ErrNotFound            = NewError(CodeNotFound, "lobby not found")
	ErrNotHost             = NewError(CodeNotHost, "host required")
	ErrLobbyFull           = NewError(CodeLobbyFull, "lobby is full")
	ErrTeamFull            = NewError(CodeTeamFull, "team is full")
	ErrPhaseConflict       = NewError(CodePhaseConflict, "operation unavailable in this phase")
	ErrNotReady            = NewError(CodeNotReady, "members are not ready")
	ErrStaleMembership     = NewError(CodeStaleMembership, "membership or client lease is stale")
	ErrIdempotencyConflict = NewError(CodeIdempotencyConflict, "request ID was used with different input")
	ErrInvalidInput        = NewError(CodeInvalidInput, "invalid input")
	ErrUnavailable         = NewError(CodeUnavailable, "service temporarily unavailable")
	ErrPermissionDenied    = NewError(CodePermissionDenied, "permission denied")
	// Internal storage sentinel, never returned directly through player HTTP.
	ErrJoinCodeCollision = errors.New("lobby join code collision")
)
