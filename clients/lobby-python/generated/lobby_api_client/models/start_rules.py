from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="StartRules")


@_attrs_define
class StartRules:
    """
    Attributes:
        every_team_needs_member (bool): Starting fails with empty_teams while a team has no member.
    """

    every_team_needs_member: bool

    def to_dict(self) -> dict[str, Any]:
        every_team_needs_member = self.every_team_needs_member

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "every_team_needs_member": every_team_needs_member,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        every_team_needs_member = d.pop("every_team_needs_member")

        start_rules = cls(
            every_team_needs_member=every_team_needs_member,
        )

        return start_rules
