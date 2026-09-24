from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.lobby import Lobby
    from ..models.member import Member
    from ..models.start_roster_entry import StartRosterEntry
    from ..models.team import Team


T = TypeVar("T", bound="Snapshot")


@_attrs_define
class Snapshot:
    """
    Attributes:
        channel (str):
        lobby (Lobby):
        members (list[Member] | None):
        membership_generation (int):
        start_roster (list[StartRosterEntry] | None):
        teams (list[Team] | None):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    channel: str
    lobby: Lobby
    members: list[Member] | None
    membership_generation: int
    start_roster: list[StartRosterEntry] | None
    teams: list[Team] | None
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        channel = self.channel

        lobby = self.lobby.to_dict()

        members: list[dict[str, Any]] | None
        if isinstance(self.members, list):
            members = []
            for members_type_0_item_data in self.members:
                members_type_0_item = members_type_0_item_data.to_dict()
                members.append(members_type_0_item)

        else:
            members = self.members

        membership_generation = self.membership_generation

        start_roster: list[dict[str, Any]] | None
        if isinstance(self.start_roster, list):
            start_roster = []
            for start_roster_type_0_item_data in self.start_roster:
                start_roster_type_0_item = start_roster_type_0_item_data.to_dict()
                start_roster.append(start_roster_type_0_item)

        else:
            start_roster = self.start_roster

        teams: list[dict[str, Any]] | None
        if isinstance(self.teams, list):
            teams = []
            for teams_type_0_item_data in self.teams:
                teams_type_0_item = teams_type_0_item_data.to_dict()
                teams.append(teams_type_0_item)

        else:
            teams = self.teams

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "channel": channel,
                "lobby": lobby,
                "members": members,
                "membership_generation": membership_generation,
                "start_roster": start_roster,
                "teams": teams,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.lobby import Lobby
        from ..models.member import Member
        from ..models.start_roster_entry import StartRosterEntry
        from ..models.team import Team

        d = dict(src_dict)
        channel = d.pop("channel")

        lobby = Lobby.from_dict(d.pop("lobby"))

        def _parse_members(data: object) -> list[Member] | None:
            if data is None:
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                members_type_0 = []
                _members_type_0 = data
                for members_type_0_item_data in _members_type_0:
                    members_type_0_item = Member.from_dict(members_type_0_item_data)

                    members_type_0.append(members_type_0_item)

                return members_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[Member] | None, data)

        members = _parse_members(d.pop("members"))

        membership_generation = d.pop("membership_generation")

        def _parse_start_roster(data: object) -> list[StartRosterEntry] | None:
            if data is None:
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                start_roster_type_0 = []
                _start_roster_type_0 = data
                for start_roster_type_0_item_data in _start_roster_type_0:
                    start_roster_type_0_item = StartRosterEntry.from_dict(start_roster_type_0_item_data)

                    start_roster_type_0.append(start_roster_type_0_item)

                return start_roster_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[StartRosterEntry] | None, data)

        start_roster = _parse_start_roster(d.pop("start_roster"))

        def _parse_teams(data: object) -> list[Team] | None:
            if data is None:
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                teams_type_0 = []
                _teams_type_0 = data
                for teams_type_0_item_data in _teams_type_0:
                    teams_type_0_item = Team.from_dict(teams_type_0_item_data)

                    teams_type_0.append(teams_type_0_item)

                return teams_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[Team] | None, data)

        teams = _parse_teams(d.pop("teams"))

        schema = d.pop("$schema", UNSET)

        snapshot = cls(
            channel=channel,
            lobby=lobby,
            members=members,
            membership_generation=membership_generation,
            start_roster=start_roster,
            teams=teams,
            schema=schema,
        )

        return snapshot
