from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="JoinLobbyBody")


@_attrs_define
class JoinLobbyBody:
    """
    Attributes:
        client_instance_id (UUID):
        game_key (str):
        join_code (str):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    client_instance_id: UUID
    game_key: str
    join_code: str
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        client_instance_id = str(self.client_instance_id)

        game_key = self.game_key

        join_code = self.join_code

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "client_instance_id": client_instance_id,
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
        client_instance_id = UUID(d.pop("client_instance_id"))

        game_key = d.pop("game_key")

        join_code = d.pop("join_code")

        schema = d.pop("$schema", UNSET)

        join_lobby_body = cls(
            client_instance_id=client_instance_id,
            game_key=game_key,
            join_code=join_code,
            schema=schema,
        )

        return join_lobby_body
