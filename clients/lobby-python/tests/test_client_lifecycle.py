import asyncio
import json
from uuid import uuid4

import centrifuge
import httpx
import pytest

from test_reconcile import Harness, FakeSDK, SCOPE, OTHER, Scope, h, snapshot, until
from lobby_client import AuthenticationError, MembershipError, TransientError, LobbyError, ClientClosedError


def problem(status, code):
    return httpx.Response(status, json={"code": code, "status": status, "title": code, "detail": code})


async def test_heartbeat_connection_gate(h):
    await h.join()
    identity = h.client.client_instance_id
    await h.client._sdk.events.on_connecting(None)
    await h.time.advance(15)
    assert not any(r.url.path.endswith("/heartbeat") for r in h.calls)
    await h.client._sdk.events.on_connected(None)
    await h.time.advance(15)
    requests = [r for r in h.calls if r.url.path.endswith("/heartbeat")]
    assert len(requests) == 1
    assert json.loads(requests[0].content) == {"client_instance_id": str(identity), "membership_generation": 1}
    assert h.client.client_instance_id == identity
    assert h.client.credential == "retained-secret"


async def test_heartbeat_version_and_stale(h):
    await h.join()
    async def hook(request):
        if request.url.path.endswith("/heartbeat"):
            return httpx.Response(200, json=dict(version=7, membership_generation=1,
                lease_expires_at="2026-09-20T00:00:45Z", disconnected_until=None))
    h.version, h.hook = 7, hook
    await h.client._tick()
    await until(lambda: h.client._states[SCOPE].accepted_version == 7)
    async def stale(request):
        if request.url.path.endswith("/heartbeat"):
            return problem(409, "stale_membership")
    h.hook = stale
    await h.client._tick()
    assert h.client._states[SCOPE].stopped
    await h.client._tick()
    assert len([r for r in h.calls if r.url.path.endswith("/memberships")]) == 1
    with pytest.raises(MembershipError):
        await h.client.snapshot(SCOPE)


async def test_token_provider_terminal_denial(h):
    await h.join()
    async def denied(request):
        if request.url.path.endswith("/connection-token"):
            return problem(401, "unauthenticated")
    h.hook = denied
    with pytest.raises(centrifuge.UnauthorizedError):
        await h.client._sdk.options["get_token"]()
    assert h.client._states[SCOPE].stopped
    with pytest.raises(AuthenticationError):
        await h.client.snapshot(SCOPE)


async def test_token_refresh_transient(h):
    await h.join()
    async def unavailable(request):
        if request.url.path.endswith("/connection-token"):
            return problem(503, "unavailable")
    h.hook = unavailable
    with pytest.raises(TransientError):
        await h.client._sdk.options["get_token"]()
    assert not h.client._states[SCOPE].stopped


async def test_token_refresh_identity(h):
    await h.join()
    identity = h.client.client_instance_id
    await h.client._sdk.options["get_token"]()
    calls = [r for r in h.calls if r.url.path.endswith("/connection-token")]
    assert len(calls) == 2
    assert all(r.headers["Authorization"] == "Bearer retained-secret" for r in calls)
    assert not any(r.url.path.endswith("/sessions") for r in h.calls)
    assert identity == h.client.client_instance_id


async def test_no_subscription_jwt(h):
    await h.join()
    for options in h.client._sdk.subscription_options:
        assert "token" not in options
        assert "get_token" not in options


@pytest.mark.parametrize("status,code,error", [(401,"unauthenticated",AuthenticationError),
                                               (404,"not_found",MembershipError)])
async def test_auth_denied_vs_transient(h, status, code, error):
    await h.join()
    async def denied(request):
        if request.method == "GET":
            return problem(status, code)
    h.hook = denied
    stream = h.client.updates(SCOPE)
    await anext(stream)
    await h.sub.ack()
    with pytest.raises(error):
        await anext(stream)


async def test_transient_snapshot_backoff(h):
    await h.join()
    count = 0
    async def unavailable(request):
        nonlocal count
        if request.method == "GET":
            count += 1
            if count == 1:
                raise httpx.ConnectError("offline", request=request)
            if count == 2:
                return problem(503, "unavailable")
    h.hook, h.version = unavailable, 2
    await h.sub.ack()
    await until(lambda: count == 1)
    assert not h.client._states[SCOPE].stopped
    await h.time.advance(.1)
    assert count == 2
    await h.time.advance(.19)
    assert count == 2
    await h.time.advance(.011)
    await until(lambda: h.client._states[SCOPE].accepted_version == 2)
    assert h.client._states[SCOPE].failures == 0


@pytest.mark.parametrize("authorized", [True, False])
async def test_server_unsubscribe_reauthorize(h, authorized):
    await h.join()
    sub = h.sub
    async def check(request):
        if request.method == "GET" and not authorized:
            return problem(404, "not_found")
    h.hook = check
    await sub.unsubscribe()
    if authorized:
        await until(lambda: sub.calls == 2)
        await until(lambda: h.client._states[SCOPE].acknowledged)
    else:
        await until(lambda: h.client._states[SCOPE].stopped)
        assert sub.calls == 1
    assert len([r for r in h.calls if r.url.path.endswith("/memberships")]) == 1


async def test_explicit_retry_policy(h):
    request_id = uuid4()
    async def unavailable(request):
        if request.url.path.endswith("/lobbies"):
            return problem(503, "unavailable")
    h.hook = unavailable
    kwargs = dict(game_key="quiz-demo", name="Room", capacity=4, teams=[], request_id=request_id)
    with pytest.raises(TransientError):
        await h.client.create_lobby(**kwargs)
    assert len(h.calls) == 1
    with pytest.raises(LobbyError, match="idempotency_conflict"):
        await h.client.create_lobby(**(kwargs | {"name": "Changed"}))
    assert len(h.calls) == 1
    h.hook = None
    await h.client.create_lobby(**kwargs)
    creates = [r for r in h.calls if r.url.path.endswith("/lobbies")]
    assert creates[0].content == creates[1].content


async def test_create_sends_game_settings_in_the_fingerprint(h):
    seen = []
    async def created(request):
        if request.url.path.endswith("/lobbies"):
            seen.append(json.loads(request.content))
    h.hook = created
    kwargs = dict(game_key="quiz-demo", name="Room", capacity=4, teams=[], request_id=uuid4())
    await h.client.create_lobby(**kwargs, game_settings={"rounds": 3})
    assert seen[0]["game_settings"] == {"rounds": 3}
    with pytest.raises(LobbyError, match="idempotency_conflict"):
        await h.client.create_lobby(**kwargs, game_settings={"rounds": 4})
    await h.client.create_lobby(**(kwargs | {"request_id": uuid4()}))
    assert "game_settings" not in seen[1]
    assert len(seen) == 2
    empty = dict(kwargs, request_id=uuid4())
    await h.client.create_lobby(**empty)
    await h.client.create_lobby(**empty, game_settings={})
    assert len(seen) == 4


async def test_rejected_game_settings_carry_the_game_message(h):
    async def rejected(request):
        if request.url.path.endswith("/lobbies"):
            return httpx.Response(422, json={"code": "invalid_game_settings", "status": 422, "title": "Unprocessable Entity",
                                             "detail": "game_settings.rounds: must be between 1 and 10"})
        if request.url.path.endswith("/start"):
            return httpx.Response(409, json={"code": "not_ready", "status": 409, "title": "Conflict", "detail": "members are not ready"})
        if request.url.path.endswith("/ready"):
            return httpx.Response(409, json={"code": "game_requirement", "status": 409, "title": "Conflict",
                                             "detail": "Choose your bot first.", "type": "urn:example:problem:bot-not-chosen"})
    h.hook = rejected
    kwargs = dict(game_key="quiz-demo", name="Room", capacity=4, teams=[], request_id=uuid4())
    with pytest.raises(LobbyError) as invalid:
        await h.client.create_lobby(**kwargs, game_settings={"rounds": 0})
    assert (invalid.value.code, invalid.value.status) == ("invalid_game_settings", 422)
    assert invalid.value.detail == "game_settings.rounds: must be between 1 and 10"
    assert str(invalid.value) == "invalid_game_settings: game_settings.rounds: must be between 1 and 10"
    await h.join()
    with pytest.raises(LobbyError) as other:
        await h.client.start(SCOPE)
    assert (other.value.code, other.value.detail, str(other.value)) == ("not_ready", None, "not_ready")
    assert other.value.problem_type is None
    # A game's own refusal carries its message and problem type.
    with pytest.raises(LobbyError) as refused:
        await h.client.set_ready(SCOPE, True)
    assert (refused.value.code, refused.value.status, refused.value.detail, refused.value.problem_type) == (
        "game_requirement", 409, "Choose your bot first.", "urn:example:problem:bot-not-chosen")


async def test_seats_send_the_games_settings_and_show_their_kind(h):
    team, seat = str(uuid4()), str(uuid4())
    seen = []

    def member(kind, settings):
        return dict(id=seat, player_id=str(uuid4()), display_name="Bot (level 2)", generation=1, active=True,
                    team_id=team, ready=True, joined_at="2026-09-20T00:00:00Z", join_sequence=2,
                    disconnected_until=None, kind=kind, seat_settings=settings)

    async def seats(request):
        if "/seats" not in request.url.path:
            return None
        seen.append((request.method, request.url.path, request.content and json.loads(request.content)))
        if request.method == "POST" and json.loads(request.content)["seat_settings"].get("bot_level") == 9:
            return httpx.Response(422, json={"code": "invalid_seat_settings", "status": 422, "title": "Unprocessable Entity",
                                             "detail": "bot_level must be 1, 2 or 3"})
        body = snapshot(3 if request.method == "POST" else 4)
        if request.method == "POST":
            body["members"] = [member("seat", {"bot_level": 2})]
        return httpx.Response(200, json=body)
    h.hook = seats
    await h.join()
    added = await h.client.add_seat(SCOPE, team, {"bot_level": 2})
    assert (added.members[0].kind.value, added.members[0].seat_settings.to_dict()) == ("seat", {"bot_level": 2})
    assert seen[0] == ("POST", f"/lobby/v1/games/quiz-demo/lobbies/{SCOPE.lobby_id}/seats",
                       {"team_id": team, "seat_settings": {"bot_level": 2}})
    with pytest.raises(LobbyError) as invalid:
        await h.client.add_seat(SCOPE, team, {"bot_level": 9})
    assert (invalid.value.code, invalid.value.status, invalid.value.detail) == (
        "invalid_seat_settings", 422, "bot_level must be 1, 2 or 3")
    removed = await h.client.remove_seat(SCOPE, seat)
    assert removed.members == [] and seen[-1][:2] == ("DELETE", f"/lobby/v1/games/quiz-demo/lobbies/{SCOPE.lobby_id}/seats/{seat}")


async def test_explicit_identity(h):
    h.client.credential = None
    assert h.calls == []
    with pytest.raises(AuthenticationError):
        await h.join()
    assert h.calls == []
    session = await h.client.create_session("Guest")
    assert h.client.credential == session.access_token == "new-secret"
    assert len(h.calls) == 1


async def test_close_ownership(h):
    await h.join()
    unrelated = asyncio.create_task(asyncio.Event().wait())
    sdk = h.client._sdk
    try:
        await h.client.close()
        assert not unrelated.done()
        assert sdk.disconnected
        assert not h.client._tasks
        releases = [r for r in h.calls if "/leases/" in r.url.path]
        assert len(releases) == 1
        assert releases[0].url.path.endswith(str(h.client.client_instance_id))
        assert releases[0].url.params["generation"] == "1"
        assert not any(r.url.path.endswith("/membership") for r in h.calls)
        assert h.client._http.get_async_httpx_client().is_closed
    finally:
        unrelated.cancel()
        await asyncio.gather(unrelated, return_exceptions=True)


async def test_close_twice_and_release_failure(h):
    await h.join()
    async def unavailable(request):
        if "/leases/" in request.url.path:
            return problem(503, "unavailable")
    h.hook = unavailable
    await asyncio.gather(h.client.close(), h.client.close())
    assert len([r for r in h.calls if "/leases/" in r.url.path]) == 1
    assert not h.client._tasks


@pytest.mark.parametrize("phase", ["ready", "snapshot", "refresh", "backoff"])
async def test_cancel_each_wait(phase):
    h = Harness()
    entered = asyncio.Event()
    blocked = asyncio.Event()
    if phase == "ready":
        FakeSDK.auto_ack = False
    if phase == "snapshot":
        async def block(request):
            if request.method == "GET":
                entered.set()
                await blocked.wait()
        h.hook = block
    task = None
    try:
        if phase in ("ready", "snapshot"):
            task = asyncio.create_task(h.join())
            if phase == "snapshot":
                await entered.wait()
            else:
                await until(lambda: h.client._sdk and h.client._sdk.subscriptions)
        else:
            await h.join()
            async def block(request):
                if phase == "refresh" and request.url.path.endswith("/connection-token"):
                    entered.set()
                    await blocked.wait()
                if phase == "backoff" and request.method == "GET":
                    entered.set()
                    return problem(503, "unavailable")
            h.hook = block
            if phase == "refresh":
                task = asyncio.create_task(h.client._get_token())
            else:
                await h.sub.ack()
            await entered.wait()
        if task:
            task.cancel()
            with pytest.raises(asyncio.CancelledError):
                await task
        await h.client.close()
        assert not h.client._tasks
        assert not h.time.waiters
        assert h.client._http.get_async_httpx_client().is_closed
    finally:
        FakeSDK.auto_ack = True
        await h.client.close()


async def test_close_pending_initial_and_updates(h):
    FakeSDK.auto_ack = False
    task = asyncio.create_task(h.join())
    try:
        await until(lambda: h.client._sdk and h.client._sdk.subscriptions)
        await h.client.close()
        with pytest.raises(ClientClosedError):
            await task
    finally:
        FakeSDK.auto_ack = True


async def test_404_is_scoped(h):
    await h.join()
    from lobby_api_client.models import Snapshot
    other = Scope("quiz-demo", OTHER)
    await h.client._attach(Snapshot.from_dict(snapshot(lobby_id=OTHER)))
    async def denied(request):
        if request.method == "GET" and SCOPE.lobby_id in request.url.path:
            return problem(404, "not_found")
    h.hook = denied
    await h.sub.ack()
    await until(lambda: h.client._states[SCOPE].stopped)
    assert not h.client._states[other].stopped
    assert (await h.client.snapshot(other)).lobby.id == OTHER


async def test_session_has_no_empty_bearer_header():
    from lobby_client import LobbyClient
    seen = []
    async def handler(request):
        seen.append(request)
        return httpx.Response(201, json=dict(player_id=SCOPE.lobby_id, access_token="guest",
                                            expires_at="2026-09-21T00:00:00Z"))
    client = LobbyClient("http://test")
    # Exercise the default generated AuthenticatedClient's header construction.
    client._http.get_async_httpx_client()._transport = httpx.MockTransport(handler)
    try:
        await client.create_session("Guest")
        assert "Authorization" not in seen[0].headers
    finally:
        await client.close()


async def test_terminal_token_denial_releases_initial_wait(h):
    FakeSDK.auto_ack = False
    task = asyncio.create_task(h.join())
    try:
        await until(lambda: h.client._sdk and h.client._sdk.subscriptions)
        async def denied(request):
            if request.url.path.endswith("/connection-token"):
                return problem(401, "unauthenticated")
        h.hook = denied
        with pytest.raises(centrifuge.UnauthorizedError):
            await h.client._get_token()
        with pytest.raises(AuthenticationError):
            await task
        await until(lambda: h.client._sdk.disconnected)
    finally:
        FakeSDK.auto_ack = True


async def test_disconnected_periodic_reconcile(h):
    await h.join()
    h.version = 2
    await h.client._sdk.events.on_connecting(None)
    await h.sub.events.on_subscribing(None)
    await h.time.advance(15)
    await until(lambda: h.client._states[SCOPE].accepted_version == 2)
    assert not any(r.url.path.endswith("/heartbeat") for r in h.calls)


async def test_close_cancellation_finishes_cleanup(h):
    await h.join()
    entered, release = asyncio.Event(), asyncio.Event()
    async def block(request):
        if "/leases/" in request.url.path:
            entered.set()
            await release.wait()
    h.hook = block
    closing = asyncio.create_task(h.client.close())
    await entered.wait()
    closing.cancel()
    release.set()
    with pytest.raises(asyncio.CancelledError):
        await closing
    assert h.client._sdk.disconnected
    assert h.client._http.get_async_httpx_client().is_closed
    assert not h.client._tasks


async def test_repeated_server_unsubscribe_backs_off(h):
    await h.join()
    sub = h.sub
    async def denied_subscription():
        sub.calls += 1
        await sub.events.on_unsubscribed(None)
    sub.subscribe = denied_subscription
    await sub.unsubscribe()
    await until(lambda: sub.calls == 2)
    for _ in range(20):
        await asyncio.sleep(0)
    assert sub.calls == 2
    await h.time.advance(.1)
    assert sub.calls == 3
    await h.time.advance(.19)
    assert sub.calls == 3
    await h.time.advance(.011)
    assert sub.calls == 4
