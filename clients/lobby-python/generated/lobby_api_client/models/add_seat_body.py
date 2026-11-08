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
        request_id (UUID | Unset): Optional. Repeating a request ID with the same team and settings returns the current
            snapshot without a second seat; with another team or settings it fails with idempotency_conflict.
    """

    seat_settings: AddSeatBodySeatSettings
    team_id: UUID
    schema: str | Unset = UNSET
    request_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        seat_settings = self.seat_settings.to_dict()

        team_id = str(self.team_id)

        schema = self.schema

        request_id: str | Unset = UNSET
        if not isinstance(self.request_id, Unset):
            request_id = str(self.request_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "seat_settings": seat_settings,
                "team_id": team_id,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema
        if request_id is not UNSET:
            field_dict["request_id"] = request_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.add_seat_body_seat_settings import AddSeatBodySeatSettings

        d = dict(src_dict)
        seat_settings = AddSeatBodySeatSettings.from_dict(d.pop("seat_settings"))

        team_id = UUID(d.pop("team_id"))

        schema = d.pop("$schema", UNSET)

        _request_id = d.pop("request_id", UNSET)
        request_id: UUID | Unset
        if isinstance(_request_id, Unset):
            request_id = UNSET
        else:
            request_id = UUID(_request_id)

        add_seat_body = cls(
            seat_settings=seat_settings,
            team_id=team_id,
            schema=schema,
            request_id=request_id,
        )

        return add_seat_body
