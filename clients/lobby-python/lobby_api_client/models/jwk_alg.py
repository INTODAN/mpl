from enum import Enum


class JWKAlg(str, Enum):
    EDDSA = "EdDSA"

    def __str__(self) -> str:
        return str(self.value)
