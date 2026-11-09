from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="TeamDefinition")


@_attrs_define
class TeamDefinition:
    """
    Attributes:
        capacity (int):
        name (str):
    """

    capacity: int
    name: str

    def to_dict(self) -> dict[str, Any]:
        capacity = self.capacity

        name = self.name

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "capacity": capacity,
                "name": name,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        capacity = d.pop("capacity")

        name = d.pop("name")

        team_definition = cls(
            capacity=capacity,
            name=name,
        )

        return team_definition
