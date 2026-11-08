"""Typed public errors. Credentials and server response bodies are never
included, except the game's own message for invalid_game_settings,
invalid_seat_settings and game_requirement, and the game's problem type for
game_requirement, which the game writes for clients."""


class LobbyError(Exception):
    def __init__(self, code: str, status: int | None = None, detail: str | None = None,
                 problem_type: str | None = None):
        self.code, self.status, self.detail = code, status, detail
        # For game_requirement, the game's own problem type, such as a URI that
        # means the player has yet to choose something in the game's API.
        self.problem_type = problem_type
        super().__init__(f"{code}: {detail}" if detail else code)


class AuthenticationError(LobbyError):
    pass


class MembershipError(LobbyError):
    pass


class CompatibilityError(LobbyError):
    pass


class TransientError(LobbyError):
    pass


class ClientClosedError(LobbyError):
    def __init__(self):
        super().__init__("client_closed")


def response_error(status: int, code: str, detail: str | None = None,
                   problem_type: str | None = None) -> LobbyError:
    if code in ("invalid_game_settings", "invalid_seat_settings"):
        # The game's validator wrote this message for clients, such as which
        # settings field is invalid.
        return LobbyError(code, status, detail or None)
    if code == "game_requirement":
        # The game refuses readiness, or the start, until the player does
        # something in the game's own API; its message says what.
        return LobbyError(code, status, detail or None, problem_type or None)
    cls = (AuthenticationError if status == 401 else
           MembershipError if status == 404 or code == "stale_membership" else
           TransientError if status >= 500 or status == 429 else LobbyError)
    return cls(code, status)
