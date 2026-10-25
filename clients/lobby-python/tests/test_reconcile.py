import asyncio
import json
import sys
from pathlib import Path
from types import SimpleNamespace
from uuid import UUID

import httpx
import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from lobby_client import LobbyClient, Scope, CompatibilityError, MembershipError

LOBBY = "11111111-1111-4111-8111-111111111111"
OTHER = "22222222-2222-4222-8222-222222222222"
SCOPE = Scope("quiz-demo", LOBBY)


def snapshot(version=1, generation=1, lobby_id=LOBBY):
    return dict(channel=f"lobby:quiz-demo:{lobby_id}", membership_generation=generation,
                members=[], teams=[], start_roster=[], start_rules=dict(every_team_needs_member=False), lobby=dict(
                    id=lobby_id, game_key="quiz-demo", version=version, capacity=4,
                    created_at="2026-09-20T00:00:00Z", host_player_id=None,
                    name="Room", join_code="ABCD", phase="waiting", game_settings={}))


async def until(predicate):
    for _ in range(200):
        if predicate():
            return
        await asyncio.sleep(0)
    assert predicate(), "condition did not become true"


class FakeTime:
    def __init__(self):
        self.now = 0
        self.waiters = []

    def __call__(self):
        return self.now

    async def sleep(self, delay):
        if delay <= 0:
            await asyncio.sleep(0)
            return
        future = asyncio.get_running_loop().create_future()
        self.waiters.append((self.now + delay, future))
        try:
            await future
        finally:
            self.waiters = [(at, f) for at, f in self.waiters if f is not future]

    async def advance(self, seconds):
        self.now += seconds
        for at, future in list(self.waiters):
            if at <= self.now and not future.done():
                future.set_result(None)
        for _ in range(20):
            await asyncio.sleep(0)


class FakeSubscription:
    def __init__(self, sdk, channel, events):
        self.sdk, self.channel, self.events = sdk, channel, events
        self.acknowledged = asyncio.Event()
        self.calls = 0

    async def subscribe(self):
        self.calls += 1
        if self.sdk.auto_ack:
            await self.ack()

    async def ack(self, recovered=False):
        self.acknowledged.set()
        await self.events.on_subscribed(SimpleNamespace(recovered=recovered))

    async def ready(self):
        await self.acknowledged.wait()

    async def unsubscribe(self):
        self.acknowledged.clear()
        await self.events.on_unsubscribed(SimpleNamespace())

    async def publish(self, version=1, **changes):
        event = dict(schema_version=1, type="lobby.changed", event_id="event",
                     game_key="quiz-demo", lobby_id=self.channel.split(":")[-1], version=version)
        event.update(changes)
        await self.events.on_publication(SimpleNamespace(pub=SimpleNamespace(data=event)))


class FakeSDK:
    auto_ack = True

    def __init__(self, address, **kwargs):
        self.address, self.options = address, kwargs
        self.events = kwargs["events"]
        self.subscriptions = []
        self.subscription_options = []
        self.disconnected = False

    async def connect(self):
        await self.events.on_connected(None)

    def new_subscription(self, channel, **kwargs):
        self.subscription_options.append(kwargs)
        sub = FakeSubscription(self, channel, kwargs["events"])
        self.subscriptions.append(sub)
        return sub

    def remove_subscription(self, sub):
        self.subscriptions.remove(sub)

    async def disconnect(self):
        self.disconnected = True
        await self.events.on_disconnected(None)


class Harness:
    def __init__(self):
        self.time = FakeTime()
        self.calls = []
        self.version = 1
        self.generation = 1
        self.hook = None
        self.gets = 0
        self.client = LobbyClient("http://test", "retained-secret",
            http_client=httpx.AsyncClient(base_url="http://test", transport=httpx.MockTransport(self.handle)),
            sdk_factory=FakeSDK, sleep=self.time.sleep, clock=self.time)

    @property
    def sub(self):
        return self.client._sdk.subscriptions[0]

    async def handle(self, request):
        self.calls.append(request)
        if self.hook:
            response = await self.hook(request)
            if response is not None:
                return response
        path = request.url.path
        if path.endswith("/connection-token"):
            return httpx.Response(200, json=dict(token="connection-jwt", websocket_url="ws://test/ws",
                                                expires_at="2026-09-20T00:05:00Z"))
        if path.endswith("/sessions"):
            return httpx.Response(201, json=dict(player_id=LOBBY, access_token="new-secret",
                                                expires_at="2026-09-21T00:00:00Z"))
        if request.method == "DELETE":
            return httpx.Response(204)
        if path.endswith("/heartbeat"):
            return httpx.Response(200, json=dict(version=self.version, membership_generation=self.generation,
                lease_expires_at="2026-09-20T00:00:45Z", disconnected_until=None))
        if request.method == "GET":
            self.gets += 1
        lobby_id = OTHER if OTHER in path else LOBBY
        return httpx.Response(201 if path.endswith("/lobbies") else 200,
                              json=snapshot(self.version, self.generation, lobby_id))

    async def join(self):
        return await self.client.join_lobby(game_key="quiz-demo", join_code="ABCD")


@pytest.fixture
async def h():
    harness = Harness()
    yield harness
    await harness.client.close()


async def test_initial_ack_before_snapshot(h):
    FakeSDK.auto_ack = False
    task = asyncio.create_task(h.join())
    try:
        await until(lambda: h.client._sdk and h.client._sdk.subscriptions)
        assert h.gets == 0
        assert not task.done()
        await h.sub.ack()
        assert (await task).lobby.version == 1
    finally:
        FakeSDK.auto_ack = True
        if not task.done():
            task.cancel()
            await asyncio.gather(task, return_exceptions=True)


async def test_event_during_initial_fetch(h):
    entered, release = asyncio.Event(), asyncio.Event()
    first = True
    async def hook(request):
        nonlocal first
        if request.method == "GET" and first:
            first = False
            entered.set()
            await release.wait()
            return httpx.Response(200, json=snapshot(5))
    h.version, h.hook = 6, hook
    task = asyncio.create_task(h.join())
    await entered.wait()
    await h.sub.publish(6)
    release.set()
    await until(lambda: h.client._states[SCOPE].accepted_version == 5)
    await h.time.advance(.1)
    assert (await task).lobby.version == 6


async def test_duplicate_out_of_order_signals(h):
    await h.join()
    h.version = 7
    for v in [7, 6, 7]:
        await h.sub.publish(v)
    await until(lambda: h.client._states[SCOPE].accepted_version == 7)
    h.version = 5
    assert (await h.client.snapshot(SCOPE)).lobby.version == 7


async def test_late_command_response(h):
    await h.join()
    entered, release = asyncio.Event(), asyncio.Event()
    async def hook(request):
        if request.url.path.endswith("/ready"):
            entered.set()
            await release.wait()
            return httpx.Response(200, json=snapshot(5))
    h.hook = hook
    command = asyncio.create_task(h.client.set_ready(SCOPE, True))
    await entered.wait()
    h.version = 7
    await h.sub.publish(7)
    await until(lambda: h.client._states[SCOPE].accepted_version == 7)
    release.set()
    assert (await command).lobby.version == 7


async def test_ack_during_fetch(h):
    await h.join()
    entered, release = asyncio.Event(), asyncio.Event()
    first = True
    async def hook(request):
        nonlocal first
        if request.method == "GET" and first:
            first = False
            entered.set()
            await release.wait()
            return httpx.Response(200, json=snapshot(1))
    h.hook = hook
    await h.sub.ack()
    await entered.wait()
    await h.sub.ack()
    release.set()
    await until(lambda: h.gets == 2)


async def test_leave_rejoin_epoch(h):
    await h.join()
    entered, release = asyncio.Event(), asyncio.Event()
    first = True
    async def hook(request):
        nonlocal first
        if request.method == "GET" and first:
            first = False
            entered.set()
            await release.wait()
            return httpx.Response(200, json=snapshot(99))
    h.hook = hook
    await h.sub.ack()
    await entered.wait()
    await h.client.leave(SCOPE)
    h.generation, h.version = 2, 4
    joined = asyncio.create_task(h.join())
    await until(lambda: SCOPE in h.client._states and h.client._states[SCOPE].generation == 2)
    release.set()
    assert (await joined).lobby.version == 4
    assert h.client._states[SCOPE].accepted.membership_generation == 2


async def test_signal_flood_bounded(h):
    await h.join()
    for i in range(10000):
        await h.sub.publish(7 if i % 2 else 6)
    state = h.client._states[SCOPE]
    assert state.target_version == 7
    assert len(h.client._dirty) == 1
    assert len(h.client._tasks) == 2


@pytest.mark.parametrize("recovered", [True, False])
async def test_recovered_and_failed_recovery(h, recovered):
    await h.join()
    h.version = 2
    await h.sub.ack(recovered)
    await until(lambda: h.client._states[SCOPE].accepted_version == 2)


async def test_periodic_reconcile(h):
    await h.join()
    h.version = 2
    await h.time.advance(14.9)
    assert h.gets == 1
    await h.time.advance(.1)
    await until(lambda: h.client._states[SCOPE].accepted_version == 2)


async def test_unknown_schema_and_wrong_scope(h):
    await h.join()
    await h.sub.publish(100, lobby_id=OTHER, schema_version=99)
    assert h.client._states[SCOPE].accepted_version == 1
    stream = h.client.updates(SCOPE)
    await anext(stream)
    await h.sub.publish(2, schema_version=99)
    with pytest.raises(CompatibilityError):
        await anext(stream)


async def test_two_scopes_and_slow_consumer(h):
    await h.join()
    from lobby_api_client.models import Snapshot
    other = Scope("quiz-demo", OTHER)
    await h.client._attach(Snapshot.from_dict(snapshot(lobby_id=OTHER)))
    stream = h.client.updates(SCOPE)
    await anext(stream)
    for version in range(2, 10):
        h.version = version
        await h.sub.publish(version)
        await until(lambda: h.client._states[SCOPE].accepted_version == version)
    assert (await anext(stream)).lobby.version == 9
    assert h.client._states[other].accepted_version == 1
    assert len(h.client._states[SCOPE].consumers) == 1
    await stream.aclose()


async def test_old_command_error_cannot_drop_new_generation_signal(h):
    await h.join()
    entered, release = asyncio.Event(), asyncio.Event()
    async def hook(request):
        if request.url.path.endswith("/ready"):
            entered.set()
            await release.wait()
            return httpx.Response(200, json=snapshot(2))
    h.hook = hook
    command = asyncio.create_task(h.client.set_ready(SCOPE, True))
    await entered.wait()
    await h.client.leave(SCOPE)
    assert SCOPE not in h.client._states
    h.generation, h.version = 2, 3
    await h.join()
    h.version = 4
    await h.sub.publish(4)
    release.set()
    with pytest.raises(MembershipError):
        await command
    await until(lambda: h.client._states[SCOPE].accepted_version == 4)


async def test_unknown_schema_releases_initial_sync(h):
    FakeSDK.auto_ack = False
    task = asyncio.create_task(h.join())
    try:
        await until(lambda: h.client._sdk and h.client._sdk.subscriptions)
        await h.sub.publish(2, schema_version=2)
        with pytest.raises(CompatibilityError):
            await task
        assert h.gets == 0
    finally:
        FakeSDK.auto_ack = True
