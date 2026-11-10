from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_lobby_body import CreateLobbyBody
from ...models.problem import Problem
from ...models.snapshot import Snapshot
from ...types import Response


def _get_kwargs(
    *,
    body: CreateLobbyBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/lobby/v1/lobbies",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | Snapshot | None:
    if response.status_code == 201:
        response_201 = Snapshot.from_dict(response.json())

        return response_201

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
    *,
    client: AuthenticatedClient,
    body: CreateLobbyBody,
) -> Response[Problem | Snapshot]:
    """Create a lobby

     Creates a lobby with its teams, from 2 to 16 with unique names. A team holds one member, a person or
    a seat, so the lobby holds as many members as it has teams; its capacity in the snapshot is that
    number. The optional game_settings object is checked by the game now; invalid settings fail with
    invalid_game_settings. The lobby stores the settings without reading them and returns them in every
    snapshot, including the started one. The host can change them, with the teams, while the lobby waits
    (PUT …/settings). Repeating the same request_id and payload within the idempotency window, 24 hours
    by default, returns the committed snapshot. The lobby is deleted, in any phase, once nobody has been
    connected to it for an hour, and in any case a day after creation, unless its game sets other times.

    Args:
        body (CreateLobbyBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | Snapshot]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: CreateLobbyBody,
) -> Problem | Snapshot | None:
    """Create a lobby

     Creates a lobby with its teams, from 2 to 16 with unique names. A team holds one member, a person or
    a seat, so the lobby holds as many members as it has teams; its capacity in the snapshot is that
    number. The optional game_settings object is checked by the game now; invalid settings fail with
    invalid_game_settings. The lobby stores the settings without reading them and returns them in every
    snapshot, including the started one. The host can change them, with the teams, while the lobby waits
    (PUT …/settings). Repeating the same request_id and payload within the idempotency window, 24 hours
    by default, returns the committed snapshot. The lobby is deleted, in any phase, once nobody has been
    connected to it for an hour, and in any case a day after creation, unless its game sets other times.

    Args:
        body (CreateLobbyBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | Snapshot
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: CreateLobbyBody,
) -> Response[Problem | Snapshot]:
    """Create a lobby

     Creates a lobby with its teams, from 2 to 16 with unique names. A team holds one member, a person or
    a seat, so the lobby holds as many members as it has teams; its capacity in the snapshot is that
    number. The optional game_settings object is checked by the game now; invalid settings fail with
    invalid_game_settings. The lobby stores the settings without reading them and returns them in every
    snapshot, including the started one. The host can change them, with the teams, while the lobby waits
    (PUT …/settings). Repeating the same request_id and payload within the idempotency window, 24 hours
    by default, returns the committed snapshot. The lobby is deleted, in any phase, once nobody has been
    connected to it for an hour, and in any case a day after creation, unless its game sets other times.

    Args:
        body (CreateLobbyBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | Snapshot]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: CreateLobbyBody,
) -> Problem | Snapshot | None:
    """Create a lobby

     Creates a lobby with its teams, from 2 to 16 with unique names. A team holds one member, a person or
    a seat, so the lobby holds as many members as it has teams; its capacity in the snapshot is that
    number. The optional game_settings object is checked by the game now; invalid settings fail with
    invalid_game_settings. The lobby stores the settings without reading them and returns them in every
    snapshot, including the started one. The host can change them, with the teams, while the lobby waits
    (PUT …/settings). Repeating the same request_id and payload within the idempotency window, 24 hours
    by default, returns the committed snapshot. The lobby is deleted, in any phase, once nobody has been
    connected to it for an hour, and in any case a day after creation, unless its game sets other times.

    Args:
        body (CreateLobbyBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | Snapshot
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
