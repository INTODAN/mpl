from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.add_seat_body_seat_settings import AddSeatBodySeatSettings


T = TypeVar("T", bound="AddSeatBody")


@_attrs_define
class AddSeatBody:
    """
    Attributes:
        seat_settings (AddSeatBodySeatSettings): Settings for a seat that no person plays, as a JSON object that only
            the game reads, such as the level of the game's bot that plays the seat. The game checks them when the host adds
            the seat; invalid settings fail with invalid_seat_settings.
        team_id (UUID): The team the seat joins.
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    seat_settings: AddSeatBodySeatSettings
    team_id: UUID
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        seat_settings = self.seat_settings.to_dict()

        team_id = str(self.team_id)

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "seat_settings": seat_settings,
                "team_id": team_id,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.add_seat_body_seat_settings import AddSeatBodySeatSettings

        d = dict(src_dict)
        seat_settings = AddSeatBodySeatSettings.from_dict(d.pop("seat_settings"))

        team_id = UUID(d.pop("team_id"))

        schema = d.pop("$schema", UNSET)

        add_seat_body = cls(
            seat_settings=seat_settings,
            team_id=team_id,
            schema=schema,
        )

        return add_seat_body
