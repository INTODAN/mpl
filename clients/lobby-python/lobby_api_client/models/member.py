from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.member_kind import MemberKind

if TYPE_CHECKING:
    from ..models.member_member_settings_type_0 import MemberMemberSettingsType0
    from ..models.member_seat_settings_type_0 import MemberSeatSettingsType0


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
        kind (MemberKind): human: a person who joined. seat: a seat that no person plays, such as the game's bot, which
            the host added to a team; it is always ready, never disconnects or hosts, and the game plays it from
            seat_settings. Its id is the seat ID for removing it.
        member_settings (MemberMemberSettingsType0 | None): The settings this person set for the game with
            setLobbyMemberSettings, compacted with sorted keys, which only the game reads. Null until they set some, and
            always null for a seat. Every member sees every member's settings.
        player_id (str):
        ready (bool):
        seat_settings (MemberSeatSettingsType0 | None): For a seat, the settings the host added it with, compacted with
            sorted keys, which only the game reads. Null for a person.
        team_id (None | str):
    """

    active: bool
    disconnected_until: datetime.datetime | None
    display_name: str
    generation: int
    id: str
    join_sequence: int
    joined_at: datetime.datetime
    kind: MemberKind
    member_settings: MemberMemberSettingsType0 | None
    player_id: str
    ready: bool
    seat_settings: MemberSeatSettingsType0 | None
    team_id: None | str

    def to_dict(self) -> dict[str, Any]:
        from ..models.member_member_settings_type_0 import MemberMemberSettingsType0
        from ..models.member_seat_settings_type_0 import MemberSeatSettingsType0

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

        kind = self.kind.value

        member_settings: dict[str, Any] | None
        if isinstance(self.member_settings, MemberMemberSettingsType0):
            member_settings = self.member_settings.to_dict()
        else:
            member_settings = self.member_settings

        player_id = self.player_id

        ready = self.ready

        seat_settings: dict[str, Any] | None
        if isinstance(self.seat_settings, MemberSeatSettingsType0):
            seat_settings = self.seat_settings.to_dict()
        else:
            seat_settings = self.seat_settings

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
                "kind": kind,
                "member_settings": member_settings,
                "player_id": player_id,
                "ready": ready,
                "seat_settings": seat_settings,
                "team_id": team_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.member_member_settings_type_0 import MemberMemberSettingsType0
        from ..models.member_seat_settings_type_0 import MemberSeatSettingsType0

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

        kind = MemberKind(d.pop("kind"))

        def _parse_member_settings(data: object) -> MemberMemberSettingsType0 | None:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                member_settings_type_0 = MemberMemberSettingsType0.from_dict(data)

                return member_settings_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(MemberMemberSettingsType0 | None, data)

        member_settings = _parse_member_settings(d.pop("member_settings"))

        player_id = d.pop("player_id")

        ready = d.pop("ready")

        def _parse_seat_settings(data: object) -> MemberSeatSettingsType0 | None:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                seat_settings_type_0 = MemberSeatSettingsType0.from_dict(data)

                return seat_settings_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(MemberSeatSettingsType0 | None, data)

        seat_settings = _parse_seat_settings(d.pop("seat_settings"))

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
            kind=kind,
            member_settings=member_settings,
            player_id=player_id,
            ready=ready,
            seat_settings=seat_settings,
            team_id=team_id,
        )

        return member
