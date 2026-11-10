from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.change_settings_body_game_settings import ChangeSettingsBodyGameSettings
    from ..models.team_definition import TeamDefinition


T = TypeVar("T", bound="ChangeSettingsBody")


@_attrs_define
class ChangeSettingsBody:
    """
    Attributes:
        teams (list[TeamDefinition] | None): The lobby's new teams, in order, with the rules of a create request. They
            replace the lobby's teams by position: the first keeps the lobby's first team and its ID and takes this name,
            and so on; teams beyond the lobby's are added, and the lobby's teams beyond these are removed.
        schema (str | Unset): A URL to the JSON Schema for this object.
        game_settings (ChangeSettingsBodyGameSettings | Unset): Settings for the game, as a JSON object that only the
            game reads. The game checks them when the lobby is created, and again when the host changes them while the lobby
            waits. The lobby returns them compacted with sorted keys, and omitted settings as an empty object.
    """

    teams: list[TeamDefinition] | None
    schema: str | Unset = UNSET
    game_settings: ChangeSettingsBodyGameSettings | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        teams: list[dict[str, Any]] | None
        if isinstance(self.teams, list):
            teams = []
            for teams_type_0_item_data in self.teams:
                teams_type_0_item = teams_type_0_item_data.to_dict()
                teams.append(teams_type_0_item)

        else:
            teams = self.teams

        schema = self.schema

        game_settings: dict[str, Any] | Unset = UNSET
        if not isinstance(self.game_settings, Unset):
            game_settings = self.game_settings.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "teams": teams,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema
        if game_settings is not UNSET:
            field_dict["game_settings"] = game_settings

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.change_settings_body_game_settings import ChangeSettingsBodyGameSettings
        from ..models.team_definition import TeamDefinition

        d = dict(src_dict)

        def _parse_teams(data: object) -> list[TeamDefinition] | None:
            if data is None:
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                teams_type_0 = []
                _teams_type_0 = data
                for teams_type_0_item_data in _teams_type_0:
                    teams_type_0_item = TeamDefinition.from_dict(teams_type_0_item_data)

                    teams_type_0.append(teams_type_0_item)

                return teams_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[TeamDefinition] | None, data)

        teams = _parse_teams(d.pop("teams"))

        schema = d.pop("$schema", UNSET)

        _game_settings = d.pop("game_settings", UNSET)
        game_settings: ChangeSettingsBodyGameSettings | Unset
        if isinstance(_game_settings, Unset):
            game_settings = UNSET
        else:
            game_settings = ChangeSettingsBodyGameSettings.from_dict(_game_settings)

        change_settings_body = cls(
            teams=teams,
            schema=schema,
            game_settings=game_settings,
        )

        return change_settings_body
