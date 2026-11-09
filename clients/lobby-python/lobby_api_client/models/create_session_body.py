from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateSessionBody")


@_attrs_define
class CreateSessionBody:
    """
    Attributes:
        display_name (str):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    display_name: str
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        display_name = self.display_name

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "display_name": display_name,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        display_name = d.pop("display_name")

        schema = d.pop("$schema", UNSET)

        create_session_body = cls(
            display_name=display_name,
            schema=schema,
        )

        return create_session_body
