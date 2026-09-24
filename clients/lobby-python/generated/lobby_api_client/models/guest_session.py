from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="GuestSession")


@_attrs_define
class GuestSession:
    """
    Attributes:
        access_token (str):
        expires_at (datetime.datetime):
        player_id (UUID):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    access_token: str
    expires_at: datetime.datetime
    player_id: UUID
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        access_token = self.access_token

        expires_at = self.expires_at.isoformat()

        player_id = str(self.player_id)

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "access_token": access_token,
                "expires_at": expires_at,
                "player_id": player_id,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        access_token = d.pop("access_token")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        player_id = UUID(d.pop("player_id"))

        schema = d.pop("$schema", UNSET)

        guest_session = cls(
            access_token=access_token,
            expires_at=expires_at,
            player_id=player_id,
            schema=schema,
        )

        return guest_session
