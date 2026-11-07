from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.problem_code import ProblemCode
from ..types import UNSET, Unset

T = TypeVar("T", bound="Problem")


@_attrs_define
class Problem:
    """
    Attributes:
        code (ProblemCode):
        detail (str):
        status (int):
        title (str):
        team_ids (list[str] | None | Unset): The teams the error is about. For empty_teams, the teams that still need a
            player, in team order.
        type_ (str | Unset): For game_requirement, the game's own problem type, a URI that the game documents, such as
            one meaning a player has yet to choose something in the game's API. Absent for every other code.
    """

    code: ProblemCode
    detail: str
    status: int
    title: str
    team_ids: list[str] | None | Unset = UNSET
    type_: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        code = self.code.value

        detail = self.detail

        status = self.status

        title = self.title

        team_ids: list[str] | None | Unset
        if isinstance(self.team_ids, Unset):
            team_ids = UNSET
        elif isinstance(self.team_ids, list):
            team_ids = self.team_ids

        else:
            team_ids = self.team_ids

        type_ = self.type_

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "detail": detail,
                "status": status,
                "title": title,
            }
        )
        if team_ids is not UNSET:
            field_dict["team_ids"] = team_ids
        if type_ is not UNSET:
            field_dict["type"] = type_

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = ProblemCode(d.pop("code"))

        detail = d.pop("detail")

        status = d.pop("status")

        title = d.pop("title")

        def _parse_team_ids(data: object) -> list[str] | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                team_ids_type_0 = cast(list[str], data)

                return team_ids_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[str] | None | Unset, data)

        team_ids = _parse_team_ids(d.pop("team_ids", UNSET))

        type_ = d.pop("type", UNSET)

        problem = cls(
            code=code,
            detail=detail,
            status=status,
            title=title,
            team_ids=team_ids,
            type_=type_,
        )

        return problem
