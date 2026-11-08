from enum import Enum


class ProblemCode(str, Enum):
    EMPTY_TEAMS = "empty_teams"
    GAME_REQUIREMENT = "game_requirement"
    IDEMPOTENCY_CONFLICT = "idempotency_conflict"
    INTERNAL_ERROR = "internal_error"
    INVALID_GAME_SETTINGS = "invalid_game_settings"
    INVALID_INPUT = "invalid_input"
    INVALID_SEAT_SETTINGS = "invalid_seat_settings"
    LOBBY_FULL = "lobby_full"
    NOT_FOUND = "not_found"
    NOT_HOST = "not_host"
    NOT_READY = "not_ready"
    PHASE_CONFLICT = "phase_conflict"
    STALE_MEMBERSHIP = "stale_membership"
    TEAM_FULL = "team_full"
    UNAUTHENTICATED = "unauthenticated"
    UNAVAILABLE = "unavailable"
    WATCHER_KICKED = "watcher_kicked"

    def __str__(self) -> str:
        return str(self.value)
