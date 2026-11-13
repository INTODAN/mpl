from enum import Enum


class JWKKty(str, Enum):
    OKP = "OKP"

    def __str__(self) -> str:
        return str(self.value)
