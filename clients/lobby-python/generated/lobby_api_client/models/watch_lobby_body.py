from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="WatchLobbyBody")


@_attrs_define
class WatchLobbyBody:
    """
    Attributes:
        game_key (str):
        join_code (str):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    game_key: str
    join_code: str
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        game_key = self.game_key

        join_code = self.join_code

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "game_key": game_key,
                "join_code": join_code,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        game_key = d.pop("game_key")

        join_code = d.pop("join_code")

        schema = d.pop("$schema", UNSET)

        watch_lobby_body = cls(
            game_key=game_key,
            join_code=join_code,
            schema=schema,
        )

        return watch_lobby_body
