from enum import Enum


class MemberKind(str, Enum):
    HUMAN = "human"
    SEAT = "seat"

    def __str__(self) -> str:
        return str(self.value)
