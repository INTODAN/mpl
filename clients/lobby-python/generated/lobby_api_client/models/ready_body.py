from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ReadyBody")


@_attrs_define
class ReadyBody:
    """
    Attributes:
        ready (bool):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    ready: bool
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        ready = self.ready

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "ready": ready,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ready = d.pop("ready")

        schema = d.pop("$schema", UNSET)

        ready_body = cls(
            ready=ready,
            schema=schema,
        )

        return ready_body
