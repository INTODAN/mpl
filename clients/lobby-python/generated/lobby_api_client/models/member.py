from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="Member")


@_attrs_define
class Member:
    """
    Attributes:
        active (bool):
        disconnected_until (datetime.datetime | None):
        display_name (str):
        generation (int):
        id (str):
        join_sequence (int):
        joined_at (datetime.datetime):
        player_id (str):
        ready (bool):
        team_id (None | str):
    """

    active: bool
    disconnected_until: datetime.datetime | None
    display_name: str
    generation: int
    id: str
    join_sequence: int
    joined_at: datetime.datetime
    player_id: str
    ready: bool
    team_id: None | str

    def to_dict(self) -> dict[str, Any]:
        active = self.active

        disconnected_until: None | str
        if isinstance(self.disconnected_until, datetime.datetime):
            disconnected_until = self.disconnected_until.isoformat()
        else:
            disconnected_until = self.disconnected_until

        display_name = self.display_name

        generation = self.generation

        id = self.id

        join_sequence = self.join_sequence

        joined_at = self.joined_at.isoformat()

        player_id = self.player_id

        ready = self.ready

        team_id: None | str
        team_id = self.team_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "active": active,
                "disconnected_until": disconnected_until,
                "display_name": display_name,
                "generation": generation,
                "id": id,
                "join_sequence": join_sequence,
                "joined_at": joined_at,
                "player_id": player_id,
                "ready": ready,
                "team_id": team_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        active = d.pop("active")

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

        display_name = d.pop("display_name")

        generation = d.pop("generation")

        id = d.pop("id")

        join_sequence = d.pop("join_sequence")

        joined_at = datetime.datetime.fromisoformat(d.pop("joined_at"))

        player_id = d.pop("player_id")

        ready = d.pop("ready")

        def _parse_team_id(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        team_id = _parse_team_id(d.pop("team_id"))

        member = cls(
            active=active,
            disconnected_until=disconnected_until,
            display_name=display_name,
            generation=generation,
            id=id,
            join_sequence=join_sequence,
            joined_at=joined_at,
            player_id=player_id,
            ready=ready,
            team_id=team_id,
        )

        return member
