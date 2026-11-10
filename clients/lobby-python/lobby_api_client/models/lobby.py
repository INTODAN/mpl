from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.lobby_game_settings import LobbyGameSettings


T = TypeVar("T", bound="Lobby")


@_attrs_define
class Lobby:
    """
    Attributes:
        capacity (int): The number of members the lobby holds, which is its number of teams, since each team holds one
            member.
        created_at (datetime.datetime):
        game_key (str):
        game_settings (LobbyGameSettings): Settings for the game, as a JSON object that only the game reads. The game
            checks them when the lobby is created, and again when the host changes them while the lobby waits. The lobby
            returns them compacted with sorted keys, and omitted settings as an empty object.
        host_player_id (None | str):
        id (str):
        join_code (str):
        name (str):
        phase (str):
        round_ (int): The lobby's round, from 1. Reopening the started lobby after its game ended (reopenLobby) raises
            it by one and makes the lobby wait again; a game tells the lobby's games apart by lobby and round.
        version (int):
    """

    capacity: int
    created_at: datetime.datetime
    game_key: str
    game_settings: LobbyGameSettings
    host_player_id: None | str
    id: str
    join_code: str
    name: str
    phase: str
    round_: int
    version: int

    def to_dict(self) -> dict[str, Any]:
        capacity = self.capacity

        created_at = self.created_at.isoformat()

        game_key = self.game_key

        game_settings = self.game_settings.to_dict()

        host_player_id: None | str
        host_player_id = self.host_player_id

        id = self.id

        join_code = self.join_code

        name = self.name

        phase = self.phase

        round_ = self.round_

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "capacity": capacity,
                "created_at": created_at,
                "game_key": game_key,
                "game_settings": game_settings,
                "host_player_id": host_player_id,
                "id": id,
                "join_code": join_code,
                "name": name,
                "phase": phase,
                "round": round_,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.lobby_game_settings import LobbyGameSettings

        d = dict(src_dict)
        capacity = d.pop("capacity")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        game_key = d.pop("game_key")

        game_settings = LobbyGameSettings.from_dict(d.pop("game_settings"))

        def _parse_host_player_id(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        host_player_id = _parse_host_player_id(d.pop("host_player_id"))

        id = d.pop("id")

        join_code = d.pop("join_code")

        name = d.pop("name")

        phase = d.pop("phase")

        round_ = d.pop("round")

        version = d.pop("version")

        lobby = cls(
            capacity=capacity,
            created_at=created_at,
            game_key=game_key,
            game_settings=game_settings,
            host_player_id=host_player_id,
            id=id,
            join_code=join_code,
            name=name,
            phase=phase,
            round_=round_,
            version=version,
        )

        return lobby
