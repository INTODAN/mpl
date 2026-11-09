from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="Watcher")


@_attrs_define
class Watcher:
    """
    Attributes:
        display_name (str):
        joined_at (datetime.datetime): When they started watching.
        player_id (UUID): The watcher's player ID, for kicking them.
    """

    display_name: str
    joined_at: datetime.datetime
    player_id: UUID

    def to_dict(self) -> dict[str, Any]:
        display_name = self.display_name

        joined_at = self.joined_at.isoformat()

        player_id = str(self.player_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "display_name": display_name,
                "joined_at": joined_at,
                "player_id": player_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        display_name = d.pop("display_name")

        joined_at = datetime.datetime.fromisoformat(d.pop("joined_at"))

        player_id = UUID(d.pop("player_id"))

        watcher = cls(
            display_name=display_name,
            joined_at=joined_at,
            player_id=player_id,
        )

        return watcher
