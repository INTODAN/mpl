from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.lobby_invalidation_schema_version import LobbyInvalidationSchemaVersion
from ..models.lobby_invalidation_type import LobbyInvalidationType

T = TypeVar("T", bound="LobbyInvalidation")


@_attrs_define
class LobbyInvalidation:
    """
    Attributes:
        event_id (str):
        game_key (str):
        lobby_id (str):
        schema_version (LobbyInvalidationSchemaVersion):
        type_ (LobbyInvalidationType):
        version (int):
    """

    event_id: str
    game_key: str
    lobby_id: str
    schema_version: LobbyInvalidationSchemaVersion
    type_: LobbyInvalidationType
    version: int

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        game_key = self.game_key

        lobby_id = self.lobby_id

        schema_version = self.schema_version.value

        type_ = self.type_.value

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_id": event_id,
                "game_key": game_key,
                "lobby_id": lobby_id,
                "schema_version": schema_version,
                "type": type_,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = d.pop("event_id")

        game_key = d.pop("game_key")

        lobby_id = d.pop("lobby_id")

        schema_version = LobbyInvalidationSchemaVersion(d.pop("schema_version"))

        type_ = LobbyInvalidationType(d.pop("type"))

        version = d.pop("version")

        lobby_invalidation = cls(
            event_id=event_id,
            game_key=game_key,
            lobby_id=lobby_id,
            schema_version=schema_version,
            type_=type_,
            version=version,
        )

        return lobby_invalidation
