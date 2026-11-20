from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.account import Account


T = TypeVar("T", bound="AccountSession")


@_attrs_define
class AccountSession:
    """
    Attributes:
        access_token (str):
        account (Account):
        expires_at (datetime.datetime):
        player_id (UUID):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    access_token: str
    account: Account
    expires_at: datetime.datetime
    player_id: UUID
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        access_token = self.access_token

        account = self.account.to_dict()

        expires_at = self.expires_at.isoformat()

        player_id = str(self.player_id)

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "access_token": access_token,
                "account": account,
                "expires_at": expires_at,
                "player_id": player_id,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.account import Account

        d = dict(src_dict)
        access_token = d.pop("access_token")

        account = Account.from_dict(d.pop("account"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        player_id = UUID(d.pop("player_id"))

        schema = d.pop("$schema", UNSET)

        account_session = cls(
            access_token=access_token,
            account=account,
            expires_at=expires_at,
            player_id=player_id,
            schema=schema,
        )

        return account_session
