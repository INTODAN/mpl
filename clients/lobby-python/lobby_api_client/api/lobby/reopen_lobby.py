from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.snapshot import Snapshot
from ...types import Response


def _get_kwargs(
    game_key: str,
    lobby_id: UUID,
) -> dict[str, Any]:
    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/lobby/v1/games/{game_key}/lobbies/{lobby_id}/reopen".format(
            game_key=quote(str(game_key), safe=""),
            lobby_id=quote(str(lobby_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | Snapshot | None:
    if response.status_code == 200:
        response_200 = Snapshot.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Problem | Snapshot]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    game_key: str,
    lobby_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | Snapshot]:
    """Reopen a lobby after its game

     Brings a started lobby whose game has ended back to waiting, so its members can play another game in
    it, and returns the snapshot. Any active person in the lobby may reopen it; anyone else gets
    not_found. The lobby's round goes up by one, and the game binds the next game to the new round.
    Members, teams, seats, the game settings, every member's member_settings and the host stay; every
    person's ready is cleared, seats stay ready, and the start roster is emptied until the next start.
    The change publishes lobby.changed. A lobby that already waits is returned unchanged, so two members
    reopening it at once both succeed. A lobby whose game is still running fails with game_running; a
    closed lobby, or one of a game that doesn't let its lobbies play again, fails with phase_conflict.
    While the lobby waits, people can join it and the host can change its settings as before a first
    game.

    Args:
        game_key (str):
        lobby_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | Snapshot]
    """

    kwargs = _get_kwargs(
        game_key=game_key,
        lobby_id=lobby_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    game_key: str,
    lobby_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Problem | Snapshot | None:
    """Reopen a lobby after its game

     Brings a started lobby whose game has ended back to waiting, so its members can play another game in
    it, and returns the snapshot. Any active person in the lobby may reopen it; anyone else gets
    not_found. The lobby's round goes up by one, and the game binds the next game to the new round.
    Members, teams, seats, the game settings, every member's member_settings and the host stay; every
    person's ready is cleared, seats stay ready, and the start roster is emptied until the next start.
    The change publishes lobby.changed. A lobby that already waits is returned unchanged, so two members
    reopening it at once both succeed. A lobby whose game is still running fails with game_running; a
    closed lobby, or one of a game that doesn't let its lobbies play again, fails with phase_conflict.
    While the lobby waits, people can join it and the host can change its settings as before a first
    game.

    Args:
        game_key (str):
        lobby_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | Snapshot
    """

    return sync_detailed(
        game_key=game_key,
        lobby_id=lobby_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    game_key: str,
    lobby_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | Snapshot]:
    """Reopen a lobby after its game

     Brings a started lobby whose game has ended back to waiting, so its members can play another game in
    it, and returns the snapshot. Any active person in the lobby may reopen it; anyone else gets
    not_found. The lobby's round goes up by one, and the game binds the next game to the new round.
    Members, teams, seats, the game settings, every member's member_settings and the host stay; every
    person's ready is cleared, seats stay ready, and the start roster is emptied until the next start.
    The change publishes lobby.changed. A lobby that already waits is returned unchanged, so two members
    reopening it at once both succeed. A lobby whose game is still running fails with game_running; a
    closed lobby, or one of a game that doesn't let its lobbies play again, fails with phase_conflict.
    While the lobby waits, people can join it and the host can change its settings as before a first
    game.

    Args:
        game_key (str):
        lobby_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | Snapshot]
    """

    kwargs = _get_kwargs(
        game_key=game_key,
        lobby_id=lobby_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    game_key: str,
    lobby_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Problem | Snapshot | None:
    """Reopen a lobby after its game

     Brings a started lobby whose game has ended back to waiting, so its members can play another game in
    it, and returns the snapshot. Any active person in the lobby may reopen it; anyone else gets
    not_found. The lobby's round goes up by one, and the game binds the next game to the new round.
    Members, teams, seats, the game settings, every member's member_settings and the host stay; every
    person's ready is cleared, seats stay ready, and the start roster is emptied until the next start.
    The change publishes lobby.changed. A lobby that already waits is returned unchanged, so two members
    reopening it at once both succeed. A lobby whose game is still running fails with game_running; a
    closed lobby, or one of a game that doesn't let its lobbies play again, fails with phase_conflict.
    While the lobby waits, people can join it and the host can change its settings as before a first
    game.

    Args:
        game_key (str):
        lobby_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | Snapshot
    """

    return (
        await asyncio_detailed(
            game_key=game_key,
            lobby_id=lobby_id,
            client=client,
        )
    ).parsed
