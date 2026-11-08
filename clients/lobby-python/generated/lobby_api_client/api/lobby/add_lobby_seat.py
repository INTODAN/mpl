from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.add_seat_body import AddSeatBody
from ...models.problem import Problem
from ...models.snapshot import Snapshot
from ...types import Response


def _get_kwargs(
    game_key: str,
    lobby_id: UUID,
    *,
    body: AddSeatBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/lobby/v1/games/{game_key}/lobbies/{lobby_id}/seats".format(
            game_key=quote(str(game_key), safe=""),
            lobby_id=quote(str(lobby_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
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
    body: AddSeatBody,
) -> Response[Problem | Snapshot]:
    """Add a seat

     Adds a seat that no person plays, such as the game's bot, to a team, and returns the snapshot, where
    the seat is a member of kind seat. Only the host can add one (otherwise not_host), and only while
    the lobby waits (otherwise phase_conflict). The game checks seat_settings and names the seat;
    settings it doesn't understand fail with invalid_seat_settings, and a game without seats refuses
    every one. The seat counts toward the lobby's and the team's capacity (lobby_full, team_full) and
    toward the members a start needs, is always ready, never disconnects, and joins the start roster
    with its own player_id. A start still needs the host, a person. A team_id that isn't one of the
    lobby's teams is invalid_input. Each call adds a seat, so a retry after a lost response can add a
    second one; the host removes it.

    Args:
        game_key (str):
        lobby_id (UUID):
        body (AddSeatBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | Snapshot]
    """

    kwargs = _get_kwargs(
        game_key=game_key,
        lobby_id=lobby_id,
        body=body,
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
    body: AddSeatBody,
) -> Problem | Snapshot | None:
    """Add a seat

     Adds a seat that no person plays, such as the game's bot, to a team, and returns the snapshot, where
    the seat is a member of kind seat. Only the host can add one (otherwise not_host), and only while
    the lobby waits (otherwise phase_conflict). The game checks seat_settings and names the seat;
    settings it doesn't understand fail with invalid_seat_settings, and a game without seats refuses
    every one. The seat counts toward the lobby's and the team's capacity (lobby_full, team_full) and
    toward the members a start needs, is always ready, never disconnects, and joins the start roster
    with its own player_id. A start still needs the host, a person. A team_id that isn't one of the
    lobby's teams is invalid_input. Each call adds a seat, so a retry after a lost response can add a
    second one; the host removes it.

    Args:
        game_key (str):
        lobby_id (UUID):
        body (AddSeatBody):

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
        body=body,
    ).parsed


async def asyncio_detailed(
    game_key: str,
    lobby_id: UUID,
    *,
    client: AuthenticatedClient,
    body: AddSeatBody,
) -> Response[Problem | Snapshot]:
    """Add a seat

     Adds a seat that no person plays, such as the game's bot, to a team, and returns the snapshot, where
    the seat is a member of kind seat. Only the host can add one (otherwise not_host), and only while
    the lobby waits (otherwise phase_conflict). The game checks seat_settings and names the seat;
    settings it doesn't understand fail with invalid_seat_settings, and a game without seats refuses
    every one. The seat counts toward the lobby's and the team's capacity (lobby_full, team_full) and
    toward the members a start needs, is always ready, never disconnects, and joins the start roster
    with its own player_id. A start still needs the host, a person. A team_id that isn't one of the
    lobby's teams is invalid_input. Each call adds a seat, so a retry after a lost response can add a
    second one; the host removes it.

    Args:
        game_key (str):
        lobby_id (UUID):
        body (AddSeatBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | Snapshot]
    """

    kwargs = _get_kwargs(
        game_key=game_key,
        lobby_id=lobby_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    game_key: str,
    lobby_id: UUID,
    *,
    client: AuthenticatedClient,
    body: AddSeatBody,
) -> Problem | Snapshot | None:
    """Add a seat

     Adds a seat that no person plays, such as the game's bot, to a team, and returns the snapshot, where
    the seat is a member of kind seat. Only the host can add one (otherwise not_host), and only while
    the lobby waits (otherwise phase_conflict). The game checks seat_settings and names the seat;
    settings it doesn't understand fail with invalid_seat_settings, and a game without seats refuses
    every one. The seat counts toward the lobby's and the team's capacity (lobby_full, team_full) and
    toward the members a start needs, is always ready, never disconnects, and joins the start roster
    with its own player_id. A start still needs the host, a person. A team_id that isn't one of the
    lobby's teams is invalid_input. Each call adds a seat, so a retry after a lost response can add a
    second one; the host removes it.

    Args:
        game_key (str):
        lobby_id (UUID):
        body (AddSeatBody):

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
            body=body,
        )
    ).parsed
