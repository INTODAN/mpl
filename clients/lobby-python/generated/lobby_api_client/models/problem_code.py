from enum import Enum


class ProblemCode(str, Enum):
    IDEMPOTENCY_CONFLICT = "idempotency_conflict"
    INTERNAL_ERROR = "internal_error"
    INVALID_INPUT = "invalid_input"
    LOBBY_FULL = "lobby_full"
    NOT_FOUND = "not_found"
    NOT_HOST = "not_host"
    NOT_READY = "not_ready"
    PHASE_CONFLICT = "phase_conflict"
    STALE_MEMBERSHIP = "stale_membership"
    TEAM_FULL = "team_full"
    UNAUTHENTICATED = "unauthenticated"
    UNAVAILABLE = "unavailable"

    def __str__(self) -> str:
        return str(self.value)
