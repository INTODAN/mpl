from enum import Enum


class LobbyInvalidationType(str, Enum):
    LOBBY_CHANGED = "lobby.changed"

    def __str__(self) -> str:
        return str(self.value)
