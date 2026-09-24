from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="Team")


@_attrs_define
class Team:
    """
    Attributes:
        capacity (int):
        id (str):
        name (str):
        order (int):
    """

    capacity: int
    id: str
    name: str
    order: int

    def to_dict(self) -> dict[str, Any]:
        capacity = self.capacity

        id = self.id

        name = self.name

        order = self.order

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "capacity": capacity,
                "id": id,
                "name": name,
                "order": order,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        capacity = d.pop("capacity")

        id = d.pop("id")

        name = d.pop("name")

        order = d.pop("order")

        team = cls(
            capacity=capacity,
            id=id,
            name=name,
            order=order,
        )

        return team
