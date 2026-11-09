from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.watch_access import WatchAccess
from ...models.watch_lobby_body import WatchLobbyBody
from ...types import Response


def _get_kwargs(
    *,
    body: WatchLobbyBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/lobby/v1/watches",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | WatchAccess | None:
    if response.status_code == 200:
        response_200 = WatchAccess.from_dict(response.json())

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WatchAccess]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: WatchLobbyBody,
) -> Response[Problem | WatchAccess]:
    """Watch a lobby

     Lets anyone with a guest session and the lobby's join code watch it, in any phase: waiting, started
    or closed while it still exists. Adds your watcher row, or finds the one you have, and returns the
    lobby and its watch channel, where its game publishes what watchers may see; the game documents its
    own watcher reads. A watcher takes no seat and counts toward nothing: not the lobby's capacity, the
    start roster, leases, the grace period or the lobby's retention. Starting to watch changes the
    snapshot's watcher_count. An active member of the lobby gets no watcher row and keeps their member
    access (member is true). A wrong code, or a code of another game, is not_found; a watcher the host
    kicked is refused with watcher_kicked, and a new watcher beyond the lobby's limit of watchers with
    watchers_full.

    Args:
        body (WatchLobbyBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WatchAccess]
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
    body: WatchLobbyBody,
) -> Problem | WatchAccess | None:
    """Watch a lobby

     Lets anyone with a guest session and the lobby's join code watch it, in any phase: waiting, started
    or closed while it still exists. Adds your watcher row, or finds the one you have, and returns the
    lobby and its watch channel, where its game publishes what watchers may see; the game documents its
    own watcher reads. A watcher takes no seat and counts toward nothing: not the lobby's capacity, the
    start roster, leases, the grace period or the lobby's retention. Starting to watch changes the
    snapshot's watcher_count. An active member of the lobby gets no watcher row and keeps their member
    access (member is true). A wrong code, or a code of another game, is not_found; a watcher the host
    kicked is refused with watcher_kicked, and a new watcher beyond the lobby's limit of watchers with
    watchers_full.

    Args:
        body (WatchLobbyBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WatchAccess
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: WatchLobbyBody,
) -> Response[Problem | WatchAccess]:
    """Watch a lobby

     Lets anyone with a guest session and the lobby's join code watch it, in any phase: waiting, started
    or closed while it still exists. Adds your watcher row, or finds the one you have, and returns the
    lobby and its watch channel, where its game publishes what watchers may see; the game documents its
    own watcher reads. A watcher takes no seat and counts toward nothing: not the lobby's capacity, the
    start roster, leases, the grace period or the lobby's retention. Starting to watch changes the
    snapshot's watcher_count. An active member of the lobby gets no watcher row and keeps their member
    access (member is true). A wrong code, or a code of another game, is not_found; a watcher the host
    kicked is refused with watcher_kicked, and a new watcher beyond the lobby's limit of watchers with
    watchers_full.

    Args:
        body (WatchLobbyBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WatchAccess]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: WatchLobbyBody,
) -> Problem | WatchAccess | None:
    """Watch a lobby

     Lets anyone with a guest session and the lobby's join code watch it, in any phase: waiting, started
    or closed while it still exists. Adds your watcher row, or finds the one you have, and returns the
    lobby and its watch channel, where its game publishes what watchers may see; the game documents its
    own watcher reads. A watcher takes no seat and counts toward nothing: not the lobby's capacity, the
    start roster, leases, the grace period or the lobby's retention. Starting to watch changes the
    snapshot's watcher_count. An active member of the lobby gets no watcher row and keeps their member
    access (member is true). A wrong code, or a code of another game, is not_found; a watcher the host
    kicked is refused with watcher_kicked, and a new watcher beyond the lobby's limit of watchers with
    watchers_full.

    Args:
        body (WatchLobbyBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WatchAccess
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
