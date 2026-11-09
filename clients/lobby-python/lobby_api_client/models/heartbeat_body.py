from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="HeartbeatBody")


@_attrs_define
class HeartbeatBody:
    """
    Attributes:
        client_instance_id (UUID):
        membership_generation (int):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    client_instance_id: UUID
    membership_generation: int
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        client_instance_id = str(self.client_instance_id)

        membership_generation = self.membership_generation

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "client_instance_id": client_instance_id,
                "membership_generation": membership_generation,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        client_instance_id = UUID(d.pop("client_instance_id"))

        membership_generation = d.pop("membership_generation")

        schema = d.pop("$schema", UNSET)

        heartbeat_body = cls(
            client_instance_id=client_instance_id,
            membership_generation=membership_generation,
            schema=schema,
        )

        return heartbeat_body
