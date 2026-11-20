from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_session import AccountSession
from ...models.create_google_session_body import CreateGoogleSessionBody
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreateGoogleSessionBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/lobby/v1/sessions/google",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountSession | Problem | None:
    if response.status_code == 201:
        response_201 = AccountSession.from_dict(response.json())

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountSession | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateGoogleSessionBody,
) -> Response[AccountSession | Problem]:
    """Sign in with Google

     Signs in with the ID token that Google Identity Services gave the page, and opens a session: a new
    player of the account, with a bearer credential that works on every route a guest's does and lasts
    as long. The lobby verifies the token's signature with Google's keys, its iss, its aud, which must
    be one of the lobby's Google client IDs (getLobbySignIn), and its exp; an invalid token is 401. It
    keeps Google's account ID (sub) and nothing else of Google's: no name, picture or email. An
    account's first sign-in creates it and needs display_name: without one it fails with 422
    display_name_required, and the client asks for a name and posts the same credential again. A name
    another account holds, without regard to case, is 409 display_name_taken, and a name that breaks the
    rule is 422 invalid_input. Later sign-ins ignore display_name. While sign-in is off, the route
    answers 404.

    Args:
        body (CreateGoogleSessionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountSession | Problem]
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
    client: AuthenticatedClient | Client,
    body: CreateGoogleSessionBody,
) -> AccountSession | Problem | None:
    """Sign in with Google

     Signs in with the ID token that Google Identity Services gave the page, and opens a session: a new
    player of the account, with a bearer credential that works on every route a guest's does and lasts
    as long. The lobby verifies the token's signature with Google's keys, its iss, its aud, which must
    be one of the lobby's Google client IDs (getLobbySignIn), and its exp; an invalid token is 401. It
    keeps Google's account ID (sub) and nothing else of Google's: no name, picture or email. An
    account's first sign-in creates it and needs display_name: without one it fails with 422
    display_name_required, and the client asks for a name and posts the same credential again. A name
    another account holds, without regard to case, is 409 display_name_taken, and a name that breaks the
    rule is 422 invalid_input. Later sign-ins ignore display_name. While sign-in is off, the route
    answers 404.

    Args:
        body (CreateGoogleSessionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountSession | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateGoogleSessionBody,
) -> Response[AccountSession | Problem]:
    """Sign in with Google

     Signs in with the ID token that Google Identity Services gave the page, and opens a session: a new
    player of the account, with a bearer credential that works on every route a guest's does and lasts
    as long. The lobby verifies the token's signature with Google's keys, its iss, its aud, which must
    be one of the lobby's Google client IDs (getLobbySignIn), and its exp; an invalid token is 401. It
    keeps Google's account ID (sub) and nothing else of Google's: no name, picture or email. An
    account's first sign-in creates it and needs display_name: without one it fails with 422
    display_name_required, and the client asks for a name and posts the same credential again. A name
    another account holds, without regard to case, is 409 display_name_taken, and a name that breaks the
    rule is 422 invalid_input. Later sign-ins ignore display_name. While sign-in is off, the route
    answers 404.

    Args:
        body (CreateGoogleSessionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountSession | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateGoogleSessionBody,
) -> AccountSession | Problem | None:
    """Sign in with Google

     Signs in with the ID token that Google Identity Services gave the page, and opens a session: a new
    player of the account, with a bearer credential that works on every route a guest's does and lasts
    as long. The lobby verifies the token's signature with Google's keys, its iss, its aud, which must
    be one of the lobby's Google client IDs (getLobbySignIn), and its exp; an invalid token is 401. It
    keeps Google's account ID (sub) and nothing else of Google's: no name, picture or email. An
    account's first sign-in creates it and needs display_name: without one it fails with 422
    display_name_required, and the client asks for a name and posts the same credential again. A name
    another account holds, without regard to case, is 409 display_name_taken, and a name that breaks the
    rule is 422 invalid_input. Later sign-ins ignore display_name. While sign-in is off, the route
    answers 404.

    Args:
        body (CreateGoogleSessionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountSession | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
