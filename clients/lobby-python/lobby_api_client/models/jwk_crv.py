from enum import Enum


class JWKCrv(str, Enum):
    ED25519 = "Ed25519"

    def __str__(self) -> str:
        return str(self.value)
