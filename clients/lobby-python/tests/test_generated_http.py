from __future__ import annotations

import inspect
from http import HTTPStatus
import json
from uuid import UUID

import httpx
import pytest

from lobby_api_client import AuthenticatedClient, Client
from lobby_api_client import errors
from lobby_api_client.api.lobby import (
    create_lobby,
    create_lobby_connection_token,
    create_lobby_session,
    get_lobby,
    heartbeat_lobby,
    join_lobby,
    leave_lobby,
    release_lobby_lease,
    select_lobby_team,
    set_lobby_ready,
    start_lobby,
)
from lobby_api_client.models.create_lobby_body import CreateLobbyBody
from lobby_api_client.models.create_session_body import CreateSessionBody
from lobby_api_client.models.connection_token_input_body import ConnectionTokenInputBody
from lobby_api_client.models.join_lobby_body import JoinLobbyBody
from lobby_api_client.models.lobby_invalidation import LobbyInvalidation
from lobby_api_client.models.lobby_invalidation_schema_version import (
    LobbyInvalidationSchemaVersion,
)
from lobby_api_client.models.lobby_invalidation_type import LobbyInvalidationType
from lobby_api_client.models.ready_body import ReadyBody
from lobby_api_client.models.select_team_body import SelectTeamBody
from lobby_api_client.models.start_input_body import StartInputBody
from lobby_api_client.models.team_definition import TeamDefinition


EXPECTED_MODULES = {
    create_lobby,
    create_lobby_connection_token,
    create_lobby_session,
    get_lobby,
    heartbeat_lobby,
    join_lobby,
    leave_lobby,
    release_lobby_lease,
    select_lobby_team,
    set_lobby_ready,
    start_lobby,
}


def test_all_operations_generated() -> None:
    for module in EXPECTED_MODULES:
        assert callable(module.asyncio_detailed)
        assert inspect.iscoroutinefunction(module.asyncio_detailed)


def test_models_serialize_nullable_and_false_values() -> None:
    assert SelectTeamBody(team_id=None).to_dict() == {"team_id": None}
    assert ReadyBody(ready=False).to_dict() == {"ready": False}
    body = CreateLobbyBody(
        client_instance_id=UUID("11111111-1111-4111-8111-111111111111"),
        game_key="island-battles",
        name="Lobby",
        request_id=UUID("22222222-2222-4222-8222-222222222222"),
        teams=[
            TeamDefinition(name="One"),
            TeamDefinition(name="Two"),
        ],
    )
    assert (
        body.to_dict()["client_instance_id"] == "11111111-1111-4111-8111-111111111111"
    )
    assert body.to_dict()["request_id"] == "22222222-2222-4222-8222-222222222222"


@pytest.mark.asyncio
async def test_create_and_join_payloads() -> None:
    requests: list[httpx.Request] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        if request.url.path.endswith("memberships"):
            return httpx.Response(
                200,
                json={
                    "channel": "lobby:island-battles:room",
                    "lobby": {
                        "capacity": 2,
                        "created_at": "2030-01-01T00:00:00Z",
                        "game_key": "island-battles",
                        "game_settings": {},
                        "host_player_id": None,
                        "id": "room",
                        "join_code": "ABCDEFGH",
                        "name": "Lobby",
                        "phase": "waiting",
                        "round": 1,
                        "version": 1,
                    },
                    "members": [],
                    "membership_generation": 1,
                    "start_roster": [],
                    "start_rules": {"every_team_needs_member": False},
                    "watcher_count": 0,
                    "teams": [],
                },
            )
        return httpx.Response(
            200,
            json={
                "token": "jwt",
                "expires_at": "2030-01-01T00:00:00Z",
                "websocket_url": "ws://localhost",
            },
        )

    transport = httpx.MockTransport(handler)
    client = AuthenticatedClient(base_url="https://example.test", token="guest")
    client.set_async_httpx_client(
        httpx.AsyncClient(
            transport=transport,
            base_url="https://example.test",
            headers={"Authorization": "Bearer guest"},
        )
    )
    try:
        await create_lobby_connection_token.asyncio_detailed(
            client=client, body=ConnectionTokenInputBody()
        )
        await join_lobby.asyncio_detailed(
            client=client,
            body=JoinLobbyBody(
                client_instance_id=UUID("33333333-3333-4333-8333-333333333333"),
                game_key="island-battles",
                join_code="ABCDEFGH",
            ),
        )
    finally:
        await client.get_async_httpx_client().aclose()
    assert requests[0].method == "POST"
    assert requests[0].url.path == "/lobby/v1/connection-token"
    assert requests[0].headers["Authorization"] == "Bearer guest"
    assert requests[1].url.path == "/lobby/v1/memberships"
    assert json.loads(requests[1].content) == {
        "client_instance_id": "33333333-3333-4333-8333-333333333333",
        "game_key": "island-battles",
        "join_code": "ABCDEFGH",
    }


@pytest.mark.asyncio
async def test_session_payload_and_release_generation() -> None:
    requests: list[httpx.Request] = []

    async def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        if request.url.path.endswith("sessions"):
            return httpx.Response(
                201,
                json={
                    "player_id": "11111111-1111-4111-8111-111111111111",
                    "access_token": "guest",
                    "expires_at": "2030-01-01T00:00:00Z",
                },
            )
        return httpx.Response(204)

    client = Client(base_url="https://example.test")
    client.set_async_httpx_client(
        httpx.AsyncClient(
            transport=httpx.MockTransport(handler), base_url="https://example.test"
        )
    )
    try:
        await create_lobby_session.asyncio_detailed(
            client=client, body=CreateSessionBody(display_name="Guest")
        )
    finally:
        await client.get_async_httpx_client().aclose()
    auth_client = AuthenticatedClient(base_url="https://example.test", token="guest")
    auth_client.set_async_httpx_client(
        httpx.AsyncClient(
            transport=httpx.MockTransport(handler), base_url="https://example.test"
        )
    )
    try:
        response = await release_lobby_lease.asyncio_detailed(
            "island-battles",
            UUID("44444444-4444-4444-8444-444444444444"),
            UUID("55555555-5555-4555-8555-555555555555"),
            client=auth_client,
            generation=7,
        )
        assert response.status_code == HTTPStatus.NO_CONTENT
        assert response.parsed is None
    finally:
        await auth_client.get_async_httpx_client().aclose()
    assert json.loads(requests[0].content) == {"display_name": "Guest"}
    assert str(requests[1].url).endswith(
        "/leases/55555555-5555-4555-8555-555555555555?generation=7"
    )


@pytest.mark.asyncio
async def test_problem_and_unknown_status() -> None:
    async def problem_handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            409,
            json={
                "title": "Conflict",
                "status": 409,
                "detail": "full",
                "code": "lobby_full",
            },
        )

    client = AuthenticatedClient(base_url="https://example.test", token="guest")
    client.set_async_httpx_client(
        httpx.AsyncClient(
            transport=httpx.MockTransport(problem_handler),
            base_url="https://example.test",
        )
    )
    try:
        response = await start_lobby.asyncio_detailed(
            "island-battles",
            UUID("66666666-6666-4666-8666-666666666666"),
            client=client,
            body=StartInputBody(),
        )
        assert response.status_code == HTTPStatus.CONFLICT
        assert response.parsed is not None
        assert response.parsed.code.value == "lobby_full"
    finally:
        await client.get_async_httpx_client().aclose()

    async def unknown_handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(418, text="teapot")

    strict = AuthenticatedClient(
        base_url="https://example.test", token="guest", raise_on_unexpected_status=True
    )
    strict.set_async_httpx_client(
        httpx.AsyncClient(
            transport=httpx.MockTransport(unknown_handler),
            base_url="https://example.test",
        )
    )
    try:
        with pytest.raises(errors.UnexpectedStatus) as raised:
            await start_lobby.asyncio_detailed(
                "island-battles",
                UUID("66666666-6666-4666-8666-666666666666"),
                client=strict,
                body=StartInputBody(),
            )
        assert raised.value.status_code == 418
        assert raised.value.content == b"teapot"
    finally:
        await strict.get_async_httpx_client().aclose()


def test_snapshot_and_invalidation_models() -> None:
    from lobby_api_client.models.snapshot import Snapshot

    snapshot = Snapshot.from_dict(
        {
            "channel": "lobby:island-battles:room",
            "lobby": {
                "capacity": 2,
                "created_at": "2030-01-01T00:00:00Z",
                "game_key": "island-battles",
                "game_settings": {"map": {"name": "Archipelago"}, "rounds": 3},
                "host_player_id": None,
                "id": "room",
                "join_code": "ABCDEFGH",
                "name": "Lobby",
                "phase": "waiting",
                "round": 1,
                "version": 9007199254740993,
            },
            "members": [],
            "membership_generation": 1,
            "start_roster": [],
            "start_rules": {"every_team_needs_member": False},
            "watcher_count": 0,
            "teams": [],
        }
    )
    assert snapshot.teams == []
    assert snapshot.lobby.version == 9007199254740993
    assert snapshot.lobby.game_settings.to_dict() == {"map": {"name": "Archipelago"}, "rounds": 3}
    event = LobbyInvalidation.from_dict(
        {
            "schema_version": 1,
            "event_id": "event",
            "game_key": "island-battles",
            "lobby_id": "room",
            "version": 2,
            "type": "lobby.changed",
        }
    )
    assert event.schema_version is LobbyInvalidationSchemaVersion.VALUE_1
    assert event.type_ is LobbyInvalidationType.LOBBY_CHANGED
