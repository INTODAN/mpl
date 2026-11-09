from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="SelectTeamBody")


@_attrs_define
class SelectTeamBody:
    """
    Attributes:
        team_id (None | UUID):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    team_id: None | UUID
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        team_id: None | str
        if isinstance(self.team_id, UUID):
            team_id = str(self.team_id)
        else:
            team_id = self.team_id

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "team_id": team_id,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_team_id(data: object) -> None | UUID:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                team_id_type_0 = UUID(data)

                return team_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | UUID, data)

        team_id = _parse_team_id(d.pop("team_id"))

        schema = d.pop("$schema", UNSET)

        select_team_body = cls(
            team_id=team_id,
            schema=schema,
        )

        return select_team_body
