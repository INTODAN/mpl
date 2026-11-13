from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.jwks import JWKS
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:
    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/lobby/v1/jwks.json",
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> JWKS | Problem | None:
    if response.status_code == 200:
        response_200 = JWKS.from_dict(response.json())

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[JWKS | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[JWKS | Problem]:
    """Read the lobby's public keys

     Returns the JWKS (RFC 7517) of the Ed25519 keys that verify player tokens (issuePlayerToken): the
    key that signs new tokens, and any previous key that still verifies tokens issued before a rotation.
    A key is published before it signs and kept for 15 minutes after it stops, the longest a token
    lasts. A game caches the keys for the response's max-age, 300 seconds, and fetches them again, at
    most once a minute, when a token names a kid it doesn't have.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JWKS | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> JWKS | Problem | None:
    """Read the lobby's public keys

     Returns the JWKS (RFC 7517) of the Ed25519 keys that verify player tokens (issuePlayerToken): the
    key that signs new tokens, and any previous key that still verifies tokens issued before a rotation.
    A key is published before it signs and kept for 15 minutes after it stops, the longest a token
    lasts. A game caches the keys for the response's max-age, 300 seconds, and fetches them again, at
    most once a minute, when a token names a kid it doesn't have.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JWKS | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[JWKS | Problem]:
    """Read the lobby's public keys

     Returns the JWKS (RFC 7517) of the Ed25519 keys that verify player tokens (issuePlayerToken): the
    key that signs new tokens, and any previous key that still verifies tokens issued before a rotation.
    A key is published before it signs and kept for 15 minutes after it stops, the longest a token
    lasts. A game caches the keys for the response's max-age, 300 seconds, and fetches them again, at
    most once a minute, when a token names a kid it doesn't have.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JWKS | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> JWKS | Problem | None:
    """Read the lobby's public keys

     Returns the JWKS (RFC 7517) of the Ed25519 keys that verify player tokens (issuePlayerToken): the
    key that signs new tokens, and any previous key that still verifies tokens issued before a rotation.
    A key is published before it signs and kept for 15 minutes after it stops, the longest a token
    lasts. A game caches the keys for the response's max-age, 300 seconds, and fetches them again, at
    most once a minute, when a token names a kid it doesn't have.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JWKS | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
