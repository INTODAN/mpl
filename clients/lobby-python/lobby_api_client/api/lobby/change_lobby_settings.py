from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.change_settings_body import ChangeSettingsBody
from ...models.problem import Problem
from ...models.snapshot import Snapshot
from ...types import Response


def _get_kwargs(
    game_key: str,
    lobby_id: UUID,
    *,
    body: ChangeSettingsBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/lobby/v1/games/{game_key}/lobbies/{lobby_id}/settings".format(
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
    body: ChangeSettingsBody,
) -> Response[Problem | Snapshot]:
    """Change a lobby's settings

     Replaces the lobby's game_settings and teams, and returns the snapshot. Only the host can change
    them (otherwise not_host), and only while the lobby waits (otherwise phase_conflict). The body has
    the rules of a create request: from 2 to 16 teams with unique names (otherwise invalid_input), and
    an optional game_settings object that the game checks against the new teams (otherwise
    invalid_game_settings). The teams are rebuilt by position: the lobby's first team keeps its ID and
    takes the first name, and so on. New teams are added at the end. Teams beyond the new count are
    removed: a person on one stays in the lobby without a team, and a seat on one is removed, as
    removeLobbySeat removes it. Every person's ready is cleared, seats stay ready, and the lobby's
    capacity becomes the new number of teams. A change that would leave more members than teams, people
    and the seats that keep their team, fails with lobby_full and changes nothing. Settings and teams
    equal to the lobby's change nothing: the snapshot comes back with the same version and nobody's
    ready is cleared. Otherwise the change publishes lobby.changed.

    Args:
        game_key (str):
        lobby_id (UUID):
        body (ChangeSettingsBody):

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
    body: ChangeSettingsBody,
) -> Problem | Snapshot | None:
    """Change a lobby's settings

     Replaces the lobby's game_settings and teams, and returns the snapshot. Only the host can change
    them (otherwise not_host), and only while the lobby waits (otherwise phase_conflict). The body has
    the rules of a create request: from 2 to 16 teams with unique names (otherwise invalid_input), and
    an optional game_settings object that the game checks against the new teams (otherwise
    invalid_game_settings). The teams are rebuilt by position: the lobby's first team keeps its ID and
    takes the first name, and so on. New teams are added at the end. Teams beyond the new count are
    removed: a person on one stays in the lobby without a team, and a seat on one is removed, as
    removeLobbySeat removes it. Every person's ready is cleared, seats stay ready, and the lobby's
    capacity becomes the new number of teams. A change that would leave more members than teams, people
    and the seats that keep their team, fails with lobby_full and changes nothing. Settings and teams
    equal to the lobby's change nothing: the snapshot comes back with the same version and nobody's
    ready is cleared. Otherwise the change publishes lobby.changed.

    Args:
        game_key (str):
        lobby_id (UUID):
        body (ChangeSettingsBody):

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
    body: ChangeSettingsBody,
) -> Response[Problem | Snapshot]:
    """Change a lobby's settings

     Replaces the lobby's game_settings and teams, and returns the snapshot. Only the host can change
    them (otherwise not_host), and only while the lobby waits (otherwise phase_conflict). The body has
    the rules of a create request: from 2 to 16 teams with unique names (otherwise invalid_input), and
    an optional game_settings object that the game checks against the new teams (otherwise
    invalid_game_settings). The teams are rebuilt by position: the lobby's first team keeps its ID and
    takes the first name, and so on. New teams are added at the end. Teams beyond the new count are
    removed: a person on one stays in the lobby without a team, and a seat on one is removed, as
    removeLobbySeat removes it. Every person's ready is cleared, seats stay ready, and the lobby's
    capacity becomes the new number of teams. A change that would leave more members than teams, people
    and the seats that keep their team, fails with lobby_full and changes nothing. Settings and teams
    equal to the lobby's change nothing: the snapshot comes back with the same version and nobody's
    ready is cleared. Otherwise the change publishes lobby.changed.

    Args:
        game_key (str):
        lobby_id (UUID):
        body (ChangeSettingsBody):

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
    body: ChangeSettingsBody,
) -> Problem | Snapshot | None:
    """Change a lobby's settings

     Replaces the lobby's game_settings and teams, and returns the snapshot. Only the host can change
    them (otherwise not_host), and only while the lobby waits (otherwise phase_conflict). The body has
    the rules of a create request: from 2 to 16 teams with unique names (otherwise invalid_input), and
    an optional game_settings object that the game checks against the new teams (otherwise
    invalid_game_settings). The teams are rebuilt by position: the lobby's first team keeps its ID and
    takes the first name, and so on. New teams are added at the end. Teams beyond the new count are
    removed: a person on one stays in the lobby without a team, and a seat on one is removed, as
    removeLobbySeat removes it. Every person's ready is cleared, seats stay ready, and the lobby's
    capacity becomes the new number of teams. A change that would leave more members than teams, people
    and the seats that keep their team, fails with lobby_full and changes nothing. Settings and teams
    equal to the lobby's change nothing: the snapshot comes back with the same version and nobody's
    ready is cleared. Otherwise the change publishes lobby.changed.

    Args:
        game_key (str):
        lobby_id (UUID):
        body (ChangeSettingsBody):

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
