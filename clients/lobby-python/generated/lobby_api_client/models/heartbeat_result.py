from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="HeartbeatResult")


@_attrs_define
class HeartbeatResult:
    """
    Attributes:
        disconnected_until (datetime.datetime | None):
        lease_expires_at (datetime.datetime):
        membership_generation (int):
        version (int):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    disconnected_until: datetime.datetime | None
    lease_expires_at: datetime.datetime
    membership_generation: int
    version: int
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        disconnected_until: None | str
        if isinstance(self.disconnected_until, datetime.datetime):
            disconnected_until = self.disconnected_until.isoformat()
        else:
            disconnected_until = self.disconnected_until

        lease_expires_at = self.lease_expires_at.isoformat()

        membership_generation = self.membership_generation

        version = self.version

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "disconnected_until": disconnected_until,
                "lease_expires_at": lease_expires_at,
                "membership_generation": membership_generation,
                "version": version,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_disconnected_until(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                disconnected_until_type_0 = datetime.datetime.fromisoformat(data)

                return disconnected_until_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        disconnected_until = _parse_disconnected_until(d.pop("disconnected_until"))

        lease_expires_at = datetime.datetime.fromisoformat(d.pop("lease_expires_at"))

        membership_generation = d.pop("membership_generation")

        version = d.pop("version")

        schema = d.pop("$schema", UNSET)

        heartbeat_result = cls(
            disconnected_until=disconnected_until,
            lease_expires_at=lease_expires_at,
            membership_generation=membership_generation,
            version=version,
            schema=schema,
        )

        return heartbeat_result
