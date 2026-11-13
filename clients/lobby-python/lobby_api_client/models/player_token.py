from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="PlayerToken")


@_attrs_define
class PlayerToken:
    """
    Attributes:
        expires_at (datetime.datetime): When the token expires, its exp: at most 15 minutes after it was issued, and
            never after your session expires.
        token (str): A JWT signed with EdDSA (Ed25519), whose header names its key with kid. Its claims: iss, the
            lobby's public URL; sub, your player ID; aud, the game key; iat and exp; and name, your display name. Send it to
            the game as Authorization: Bearer <token>.
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    expires_at: datetime.datetime
    token: str
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expires_at = self.expires_at.isoformat()

        token = self.token

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expires_at": expires_at,
                "token": token,
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

        schema = d.pop("$schema", UNSET)

        player_token = cls(
            expires_at=expires_at,
            token=token,
            schema=schema,
        )

        return player_token
