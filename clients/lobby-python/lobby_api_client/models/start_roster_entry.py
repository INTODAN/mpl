from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="StartRosterEntry")


@_attrs_define
class StartRosterEntry:
    """
    Attributes:
        member_id (str):
        player_id (str):
        team_id (str):
        team_order (int):
    """

    member_id: str
    player_id: str
    team_id: str
    team_order: int

    def to_dict(self) -> dict[str, Any]:
        member_id = self.member_id

        player_id = self.player_id

        team_id = self.team_id

        team_order = self.team_order

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "member_id": member_id,
                "player_id": player_id,
                "team_id": team_id,
                "team_order": team_order,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        member_id = d.pop("member_id")

        player_id = d.pop("player_id")

        team_id = d.pop("team_id")

        team_order = d.pop("team_order")

        start_roster_entry = cls(
            member_id=member_id,
            player_id=player_id,
            team_id=team_id,
            team_order=team_order,
        )

        return start_roster_entry
