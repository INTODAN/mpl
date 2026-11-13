from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="IssuePlayerTokenBody")


@_attrs_define
class IssuePlayerTokenBody:
    """
    Attributes:
        game_key (str): The game whose requests the token is for, its aud claim, such as island-battles.
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    game_key: str
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        game_key = self.game_key

        schema = self.schema

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "game_key": game_key,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        game_key = d.pop("game_key")

        schema = d.pop("$schema", UNSET)

        issue_player_token_body = cls(
            game_key=game_key,
            schema=schema,
        )

        return issue_player_token_body
