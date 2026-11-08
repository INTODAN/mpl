from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="WatchAccess")


@_attrs_define
class WatchAccess:
    """
    Attributes:
        channel (str): The lobby's watch channel, watch:<game_key>:<lobby_id>, where its game publishes what watchers
            may see. The subscribe proxy allows it while you are a watcher the host hasn't kicked, or an active member.
        game_key (str):
        lobby_id (UUID):
        member (bool): True when you are an active member of the lobby. You get no watcher row and keep your member
            access, which includes the watch channel.
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    channel: str
    game_key: str
    lobby_id: UUID
    member: bool
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        channel = self.channel

        game_key = self.game_key

        lobby_id = str(self.lobby_id)

        member = self.member

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "channel": channel,
                "game_key": game_key,
                "lobby_id": lobby_id,
                "member": member,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        channel = d.pop("channel")

        game_key = d.pop("game_key")

        lobby_id = UUID(d.pop("lobby_id"))

        member = d.pop("member")

        schema = d.pop("$schema", UNSET)

        watch_access = cls(
            channel=channel,
            game_key=game_key,
            lobby_id=lobby_id,
            member=member,
            schema=schema,
        )

        return watch_access
