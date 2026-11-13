from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.issue_player_token_body import IssuePlayerTokenBody
from ...models.player_token import PlayerToken
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: IssuePlayerTokenBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/lobby/v1/player-tokens",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> PlayerToken | Problem | None:
    if response.status_code == 200:
        response_200 = PlayerToken.from_dict(response.json())

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
) -> Response[PlayerToken | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: IssuePlayerTokenBody,
) -> Response[PlayerToken | Problem]:
    """Issue a player token for a game

     Issues a short-lived player token for one game: a JWT signed with EdDSA (Ed25519), which the game
    verifies with the lobby's public keys (getLobbyPublicKeys), without the lobby's database or a call
    to the lobby. Its claims are iss, the lobby's public URL; sub, your player ID; aud, game_key; iat
    and exp; and name, your display name. Send it to the game's API as Authorization: Bearer <token>,
    where the game would otherwise need your guest credential; the lobby's own API takes only the guest
    credential. A token lasts at most 15 minutes, and never past your session's expiry; expires_at says
    when it expires. Clients get a new one before it expires, at about 80 % of its life, and once more
    when the game answers 401. A game_key that no game of this lobby has is not_found.

    Args:
        body (IssuePlayerTokenBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlayerToken | Problem]
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
    body: IssuePlayerTokenBody,
) -> PlayerToken | Problem | None:
    """Issue a player token for a game

     Issues a short-lived player token for one game: a JWT signed with EdDSA (Ed25519), which the game
    verifies with the lobby's public keys (getLobbyPublicKeys), without the lobby's database or a call
    to the lobby. Its claims are iss, the lobby's public URL; sub, your player ID; aud, game_key; iat
    and exp; and name, your display name. Send it to the game's API as Authorization: Bearer <token>,
    where the game would otherwise need your guest credential; the lobby's own API takes only the guest
    credential. A token lasts at most 15 minutes, and never past your session's expiry; expires_at says
    when it expires. Clients get a new one before it expires, at about 80 % of its life, and once more
    when the game answers 401. A game_key that no game of this lobby has is not_found.

    Args:
        body (IssuePlayerTokenBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlayerToken | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: IssuePlayerTokenBody,
) -> Response[PlayerToken | Problem]:
    """Issue a player token for a game

     Issues a short-lived player token for one game: a JWT signed with EdDSA (Ed25519), which the game
    verifies with the lobby's public keys (getLobbyPublicKeys), without the lobby's database or a call
    to the lobby. Its claims are iss, the lobby's public URL; sub, your player ID; aud, game_key; iat
    and exp; and name, your display name. Send it to the game's API as Authorization: Bearer <token>,
    where the game would otherwise need your guest credential; the lobby's own API takes only the guest
    credential. A token lasts at most 15 minutes, and never past your session's expiry; expires_at says
    when it expires. Clients get a new one before it expires, at about 80 % of its life, and once more
    when the game answers 401. A game_key that no game of this lobby has is not_found.

    Args:
        body (IssuePlayerTokenBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlayerToken | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: IssuePlayerTokenBody,
) -> PlayerToken | Problem | None:
    """Issue a player token for a game

     Issues a short-lived player token for one game: a JWT signed with EdDSA (Ed25519), which the game
    verifies with the lobby's public keys (getLobbyPublicKeys), without the lobby's database or a call
    to the lobby. Its claims are iss, the lobby's public URL; sub, your player ID; aud, game_key; iat
    and exp; and name, your display name. Send it to the game's API as Authorization: Bearer <token>,
    where the game would otherwise need your guest credential; the lobby's own API takes only the guest
    credential. A token lasts at most 15 minutes, and never past your session's expiry; expires_at says
    when it expires. Clients get a new one before it expires, at about 80 % of its life, and once more
    when the game answers 401. A game_key that no game of this lobby has is not_found.

    Args:
        body (IssuePlayerTokenBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlayerToken | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
