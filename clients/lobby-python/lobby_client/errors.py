"""Typed public errors; credentials and server response bodies are never included."""


class LobbyError(Exception):
    def __init__(self, code: str, status: int | None = None):
        self.code, self.status = code, status
        super().__init__(code)


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


def response_error(status: int, code: str) -> LobbyError:
    cls = (AuthenticationError if status == 401 else
           MembershipError if status == 404 or code == "stale_membership" else
           TransientError if status >= 500 or status == 429 else LobbyError)
    return cls(code, status)
