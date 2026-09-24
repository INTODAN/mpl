from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="Lobby")


@_attrs_define
class Lobby:
    """
    Attributes:
        capacity (int):
        created_at (datetime.datetime):
        game_key (str):
        host_player_id (None | str):
        id (str):
        join_code (str):
        name (str):
        phase (str):
        version (int):
    """

    capacity: int
    created_at: datetime.datetime
    game_key: str
    host_player_id: None | str
    id: str
    join_code: str
    name: str
    phase: str
    version: int

    def to_dict(self) -> dict[str, Any]:
        capacity = self.capacity

        created_at = self.created_at.isoformat()

        game_key = self.game_key

        host_player_id: None | str
        host_player_id = self.host_player_id

        id = self.id

        join_code = self.join_code

        name = self.name

        phase = self.phase

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "capacity": capacity,
                "created_at": created_at,
                "game_key": game_key,
                "host_player_id": host_player_id,
                "id": id,
                "join_code": join_code,
                "name": name,
                "phase": phase,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        capacity = d.pop("capacity")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        game_key = d.pop("game_key")

        def _parse_host_player_id(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        host_player_id = _parse_host_player_id(d.pop("host_player_id"))

        id = d.pop("id")

        join_code = d.pop("join_code")

        name = d.pop("name")

        phase = d.pop("phase")

        version = d.pop("version")

        lobby = cls(
            capacity=capacity,
            created_at=created_at,
            game_key=game_key,
            host_player_id=host_player_id,
            id=id,
            join_code=join_code,
            name=name,
            phase=phase,
            version=version,
        )

        return lobby
