from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    game_key: str,
    lobby_id: UUID,
    player_id: UUID,
) -> dict[str, Any]:
    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/lobby/v1/games/{game_key}/lobbies/{lobby_id}/watchers/{player_id}".format(
            game_key=quote(str(game_key), safe=""),
            lobby_id=quote(str(lobby_id), safe=""),
            player_id=quote(str(player_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    game_key: str,
    lobby_id: UUID,
    player_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Any | Problem]:
    """Kick a watcher

     Lets the host remove a watcher, in any phase. The watcher is unsubscribed from the watch channel at
    once and can't watch again with the join code (watcher_kicked). Kicking them again changes nothing;
    a player who isn't a watcher of the lobby is not_found.

    Args:
        game_key (str):
        lobby_id (UUID):
        player_id (UUID): The watcher's player ID.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        game_key=game_key,
        lobby_id=lobby_id,
        player_id=player_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    game_key: str,
    lobby_id: UUID,
    player_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Any | Problem | None:
    """Kick a watcher

     Lets the host remove a watcher, in any phase. The watcher is unsubscribed from the watch channel at
    once and can't watch again with the join code (watcher_kicked). Kicking them again changes nothing;
    a player who isn't a watcher of the lobby is not_found.

    Args:
        game_key (str):
        lobby_id (UUID):
        player_id (UUID): The watcher's player ID.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        game_key=game_key,
        lobby_id=lobby_id,
        player_id=player_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    game_key: str,
    lobby_id: UUID,
    player_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Any | Problem]:
    """Kick a watcher

     Lets the host remove a watcher, in any phase. The watcher is unsubscribed from the watch channel at
    once and can't watch again with the join code (watcher_kicked). Kicking them again changes nothing;
    a player who isn't a watcher of the lobby is not_found.

    Args:
        game_key (str):
        lobby_id (UUID):
        player_id (UUID): The watcher's player ID.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        game_key=game_key,
        lobby_id=lobby_id,
        player_id=player_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    game_key: str,
    lobby_id: UUID,
    player_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Any | Problem | None:
    """Kick a watcher

     Lets the host remove a watcher, in any phase. The watcher is unsubscribed from the watch channel at
    once and can't watch again with the join code (watcher_kicked). Kicking them again changes nothing;
    a player who isn't a watcher of the lobby is not_found.

    Args:
        game_key (str):
        lobby_id (UUID):
        player_id (UUID): The watcher's player ID.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            game_key=game_key,
            lobby_id=lobby_id,
            player_id=player_id,
            client=client,
        )
    ).parsed
