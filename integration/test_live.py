"""Acceptance against real PostgreSQL, backend, Centrifugo and the Python client."""
from __future__ import annotations

import asyncio
import os
import signal
from pathlib import Path
from uuid import uuid4

import httpx
import pytest
import pytest_asyncio

from lobby_client import LobbyClient, Scope
from lobby_api_client.client import AuthenticatedClient
from lobby_api_client.api.lobby import get_lobby, set_lobby_ready, select_lobby_team
from lobby_api_client.models import ReadyBody, TeamDefinition, SelectTeamBody, Snapshot

pytestmark = pytest.mark.asyncio

BASE = os.environ["LOBBY_TEST_API_URL"]
RUNTIME = os.environ["LOBBY_TEST_CONTAINER_RUNTIME"]
EVENTS = os.environ["LOBBY_TEST_CENTRIFUGO_CONTAINER"]
DATABASE = os.environ["LOBBY_TEST_POSTGRES_CONTAINER"]


async def container(*arguments: str) -> str:
    process = await asyncio.create_subprocess_exec(
        RUNTIME, *arguments, stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    output, error = await process.communicate()
    assert process.returncode == 0, error.decode()
    return output.decode().strip()


async def sql(statement: str) -> str:
    return await container("exec", DATABASE, "psql", "-U", "lobby", "-d", "lobby", "-At", "-c", statement)


@pytest_asyncio.fixture
async def guests():
    clients = []

    async def make(name="Player", credential=None, **kwargs):
        client = LobbyClient(BASE, credential, **kwargs)
        clients.append(client)
        if credential is None:
            await client.create_session(name)
        return client

    yield make
    await asyncio.gather(*(client.close() for client in clients), return_exceptions=True)


async def create(client, game_key=None):
    game_key = game_key or os.environ.get("LOBBY_TEST_GAME_KEY", "island-battles")
    return await client.create_lobby(
        game_key=game_key, name="Acceptance", capacity=4,
        teams=[TeamDefinition(name="Red", capacity=4), TeamDefinition(name="Blue", capacity=4)],
        request_id=uuid4(),
    )


async def latest(client, scope, minimum_version, timeout=25):
    async with asyncio.timeout(timeout):
        async for snapshot in client.updates(scope):
            if snapshot.lobby.version >= minimum_version:
                return snapshot
    raise AssertionError("updates ended before the authoritative version")


async def ready_http(client, scope, ready):
    async with AuthenticatedClient(BASE, token=client.credential) as generated:
        response = await set_lobby_ready.asyncio_detailed(
            scope.game_key, scope.lobby_id, client=generated, body=ReadyBody(ready=ready),
        )
        assert response.status_code == 200
        return response.parsed


async def test_two_python_players_create_join_choose_teams(guests):
    alice, bob = await guests("Alice"), await guests("Bob")
    initial = await create(alice)
    scope = Scope.from_snapshot(initial)
    await bob.join_lobby(game_key=scope.game_key, join_code=initial.lobby.join_code)
    await alice.select_team(scope, initial.teams[0].id)
    await bob.select_team(scope, initial.teams[1].id)
    await alice.set_ready(scope, True)
    await bob.set_ready(scope, True)
    started = await alice.start(scope)
    observed = await latest(bob, scope, started.lobby.version)
    assert observed.lobby.phase == "started"
    assert len(observed.start_roster) == 2
    assert observed.start_roster == started.start_roster



async def test_generic_second_game_starts(guests):
    client, second = await guests(), await guests()
    room = await create(client, "quiz-demo")
    scope = Scope.from_snapshot(room)
    await second.join_lobby(game_key=scope.game_key, join_code=room.lobby.join_code)
    await client.select_team(scope, room.teams[0].id)
    await second.select_team(scope, room.teams[1].id)
    await client.set_ready(scope, True)
    await second.set_ready(scope, True)
    started = await client.start(scope)
    assert started.lobby.phase == "started"


async def test_socket_reconnect_history_loss_and_publish_retry(guests):
    alice, bob = await guests("Reconnect Alice"), await guests("Reconnect Bob")
    room = await create(alice)
    scope = Scope.from_snapshot(room)
    await bob.join_lobby(game_key=scope.game_key, join_code=room.lobby.join_code)
    selected = await alice.select_team(scope, room.teams[0].id)
    before = next(member for member in selected.members if member.player_id == selected.lobby.host_player_id)
    await container("stop", "--time", "1", EVENTS)
    try:
        changed = await ready_http(alice, scope, True)
        # HTTP remains authoritative while publication and both sockets are down.
        observed = await latest(bob, scope, changed.lobby.version, timeout=22)
        assert observed.lobby.version >= changed.lobby.version
        assert await sql(f"SELECT count(*) FROM lobby_outbox WHERE lobby_id='{scope.lobby_id}' AND version={changed.lobby.version} AND acknowledged_at IS NULL AND attempts>0") == "1"
    finally:
        await container("start", EVENTS)
    await asyncio.sleep(3)
    changed_again = await ready_http(alice, scope, False)
    observed = await latest(bob, scope, changed_again.lobby.version)
    after = next(member for member in observed.members if member.player_id == before.player_id)
    assert (after.id, after.generation, after.team_id) == (before.id, before.generation, before.team_id)
    assert observed.lobby.host_player_id == before.player_id
    async with asyncio.timeout(25):
        while await sql(f"SELECT count(*) FROM lobby_outbox WHERE lobby_id='{scope.lobby_id}' AND version={changed.lobby.version} AND acknowledged_at IS NOT NULL") != "1":
            await asyncio.sleep(0.2)


async def test_multiclient_release_and_explicit_leave(guests):
    alice = await guests("Shared")
    room = await create(alice)
    scope = Scope.from_snapshot(room)
    second = await guests(credential=alice.credential)
    joined = await second.join_lobby(game_key=scope.game_key, join_code=room.lobby.join_code)
    assert len(joined.members) == 1
    assert second.client_instance_id != alice.client_instance_id
    await alice.close()
    assert len((await second.snapshot(scope)).members) == 1
    await second.leave(scope)
    async with AuthenticatedClient(BASE, token=second.credential) as generated:
        response = await get_lobby.asyncio_detailed(scope.game_key, scope.lobby_id, client=generated)
    assert response.status_code == 404


async def test_wrong_identity_and_proxy_spoofing(guests):
    owner, stranger = await guests("Owner"), await guests("Stranger")
    room = await create(owner)
    scope = Scope.from_snapshot(room)
    async with AuthenticatedClient(BASE, token=stranger.credential) as generated:
        denied = await get_lobby.asyncio_detailed(scope.game_key, scope.lobby_id, client=generated)
    assert denied.status_code == 404
    async with httpx.AsyncClient(base_url=BASE) as raw:
        forged = await raw.post("/internal/lobby/centrifugo/subscribe", json={
            "user": room.lobby.host_player_id, "client": str(uuid4()), "channel": room.channel,
        })
        unauthenticated = await raw.get(f"/lobby/v1/games/{scope.game_key}/lobbies/{scope.lobby_id}")
    assert forged.status_code == 401
    assert unauthenticated.status_code == 401


async def test_subscribe_snapshot_race(guests):
    captured, release = asyncio.Event(), asyncio.Event()
    snapshots = []

    class DelayedSnapshot(httpx.AsyncBaseTransport):
        def __init__(self):
            self.transport = httpx.AsyncHTTPTransport()

        async def handle_async_request(self, request):
            response = await self.transport.handle_async_request(request)
            if request.method == "GET" and "/lobbies/" in request.url.path and not captured.is_set():
                await response.aread()
                snapshots.append(Snapshot.from_dict(response.json()))
                captured.set()
                await release.wait()
            return response

        async def aclose(self):
            await self.transport.aclose()

    client = await guests(http_client=httpx.AsyncClient(base_url=BASE, transport=DelayedSnapshot()))
    creating = asyncio.create_task(create(client))
    try:
        await asyncio.wait_for(captured.wait(), 10)
        earlier = snapshots[0]
        scope = Scope.from_snapshot(earlier)
        async with AuthenticatedClient(BASE, token=client.credential) as generated:
            response = await select_lobby_team.asyncio_detailed(
                scope.game_key, scope.lobby_id, client=generated,
                body=SelectTeamBody(team_id=earlier.teams[0].id),
            )
        assert response.status_code == 200
        # Give the real outbox/socket time to deliver while the HTTP read is held.
        await asyncio.sleep(2)
        release.set()
        synchronized = await asyncio.wait_for(creating, 10)
        observed = await latest(client, scope, response.parsed.lobby.version)
        assert observed.lobby.version > earlier.lobby.version
        assert synchronized.lobby.version >= earlier.lobby.version
    finally:
        release.set()
        creating.cancel()
        await asyncio.gather(creating, return_exceptions=True)


async def control_backend(action):
    status = Path(os.environ["LOBBY_TEST_BACKEND_CONTROL"])
    previous = status.read_text()
    os.kill(int(os.environ["LOBBY_TEST_RUNNER_PID"]), action)
    async with asyncio.timeout(15):
        while status.read_text() == previous:
            await asyncio.sleep(0.1)


async def test_persisted_lease_expiry_after_backend_restart(guests):
    alice, bob = await guests("Expiring host"), await guests("Next host")
    room = await create(alice)
    scope = Scope.from_snapshot(room)
    await bob.join_lobby(game_key=scope.game_key, join_code=room.lobby.join_code)
    former_host = room.lobby.host_player_id
    await alice.close()
    await control_backend(signal.SIGUSR2)
    try:
        # Advance only the test identity's persisted lease, with the backend
        # stopped so no sweeper or HTTP request can pre-apply cleanup.
        await sql(f"UPDATE lobby_leases SET expires_at=now()-interval '120 seconds', released_at=now()-interval '120 seconds' WHERE lobby_id='{scope.lobby_id}' AND player_id='{former_host}'")
        await sql(f"UPDATE lobby_memberships SET disconnected_until=NULL WHERE lobby_id='{scope.lobby_id}' AND player_id='{former_host}'")
    finally:
        await control_backend(signal.SIGUSR1)
    observed = await bob.snapshot(scope)
    assert len(observed.members) == 1
    assert observed.lobby.host_player_id == observed.members[0].player_id
    assert observed.lobby.host_player_id != former_host
    async with AuthenticatedClient(BASE, token=alice.credential) as generated:
        response = await get_lobby.asyncio_detailed(scope.game_key, scope.lobby_id, client=generated)
    assert response.status_code == 404


async def test_schema_initialization_is_repeatable(guests):
    client = await guests("Schema restart")
    room = await create(client)
    scope = Scope.from_snapshot(room)
    tables = await sql("SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename")
    lobby_tables = {table for table in tables.splitlines() if table.startswith("lobby_")}
    assert lobby_tables == {
        "lobby_players", "lobby_sessions", "lobby_rooms", "lobby_teams",
        "lobby_memberships", "lobby_leases", "lobby_start_roster",
        "lobby_create_requests", "lobby_outbox", "lobby_schema_migrations",
    }
    await control_backend(signal.SIGUSR1)
    assert await sql("SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename") == tables
    observed = await client.snapshot(scope)
    assert observed.lobby.id == room.lobby.id
    assert observed.lobby.host_player_id == room.lobby.host_player_id
    assert observed.membership_generation == room.membership_generation
