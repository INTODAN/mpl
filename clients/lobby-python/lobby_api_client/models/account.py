from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="Account")


@_attrs_define
class Account:
    """
    Attributes:
        created_at (datetime.datetime):
        display_name (str):
        id (UUID):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    created_at: datetime.datetime
    display_name: str
    id: UUID
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        created_at = self.created_at.isoformat()

        display_name = self.display_name

        id = str(self.id)

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "created_at": created_at,
                "display_name": display_name,
                "id": id,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        display_name = d.pop("display_name")

        id = UUID(d.pop("id"))

        schema = d.pop("$schema", UNSET)

        account = cls(
            created_at=created_at,
            display_name=display_name,
            id=id,
            schema=schema,
        )

        return account
