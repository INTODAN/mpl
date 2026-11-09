from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ConnectionToken")


@_attrs_define
class ConnectionToken:
    """
    Attributes:
        expires_at (datetime.datetime):
        token (str):
        websocket_url (str):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    expires_at: datetime.datetime
    token: str
    websocket_url: str
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expires_at = self.expires_at.isoformat()

        token = self.token

        websocket_url = self.websocket_url

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expires_at": expires_at,
                "token": token,
                "websocket_url": websocket_url,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        token = d.pop("token")

        websocket_url = d.pop("websocket_url")

        schema = d.pop("$schema", UNSET)

        connection_token = cls(
            expires_at=expires_at,
            token=token,
            websocket_url=websocket_url,
            schema=schema,
        )

        return connection_token
