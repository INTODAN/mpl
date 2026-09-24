from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.team_definition import TeamDefinition


T = TypeVar("T", bound="CreateLobbyBody")


@_attrs_define
class CreateLobbyBody:
    """
    Attributes:
        capacity (int):
        client_instance_id (UUID):
        game_key (str):
        name (str):
        request_id (UUID):
        teams (list[TeamDefinition] | None):
        schema (str | Unset): A URL to the JSON Schema for this object.
    """

    capacity: int
    client_instance_id: UUID
    game_key: str
    name: str
    request_id: UUID
    teams: list[TeamDefinition] | None
    schema: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        capacity = self.capacity

        client_instance_id = str(self.client_instance_id)

        game_key = self.game_key

        name = self.name

        request_id = str(self.request_id)

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
                "capacity": capacity,
                "client_instance_id": client_instance_id,
                "game_key": game_key,
                "name": name,
                "request_id": request_id,
                "teams": teams,
            }
        )
        if schema is not UNSET:
            field_dict["$schema"] = schema

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.team_definition import TeamDefinition

        d = dict(src_dict)
        capacity = d.pop("capacity")

        client_instance_id = UUID(d.pop("client_instance_id"))

        game_key = d.pop("game_key")

        name = d.pop("name")

        request_id = UUID(d.pop("request_id"))

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

        create_lobby_body = cls(
            capacity=capacity,
            client_instance_id=client_instance_id,
            game_key=game_key,
            name=name,
            request_id=request_id,
            teams=teams,
            schema=schema,
        )

        return create_lobby_body
