"""Generated HTTP operations plus the official Centrifugo SDK.

A supplied http_client is transferred to this wrapper and closed with it. No
identity, lease, connection, or task is created by the constructor.
"""
from __future__ import annotations

import asyncio
import json
import time
from collections.abc import AsyncIterator, Awaitable, Callable
from contextlib import suppress
from typing import Any
from uuid import UUID, uuid4

import centrifuge
import httpx
from centrifuge.exceptions import CentrifugeError, OperationTimeoutError
from lobby_api_client.client import AuthenticatedClient
from lobby_api_client.api.lobby import (
    create_lobby, create_lobby_connection_token, create_lobby_session, get_lobby,
    heartbeat_lobby, join_lobby, leave_lobby, release_lobby_lease,
    select_lobby_team, set_lobby_ready, start_lobby,
)
from lobby_api_client.models import (
    ConnectionTokenInputBody, CreateLobbyBody, CreateLobbyBodyGameSettings,
    CreateSessionBody, GuestSession, HeartbeatBody, JoinLobbyBody, ReadyBody,
    SelectTeamBody, Snapshot, StartInputBody, TeamDefinition,
)
from lobby_api_client.types import UNSET

from .errors import (
    AuthenticationError, ClientClosedError, CompatibilityError, LobbyError,
    MembershipError, TransientError, response_error,
)
from .reconcile import Scope, ScopeState


class _ConnectionEvents(centrifuge.ClientEventHandler):
    def __init__(self, owner):
        self.owner = owner

    async def on_connected(self, ctx):
        self.owner._connected = not self.owner._closed and self.owner._auth_error is None

    async def on_connecting(self, ctx):
        self.owner._connected = False

    async def on_disconnected(self, ctx):
        self.owner._connected = False


class _SubscriptionEvents(centrifuge.SubscriptionEventHandler):
    def __init__(self, owner, state):
        self.owner, self.state = owner, state

    async def on_subscribed(self, ctx):
        if self.owner._current(self.state):
            self.state.acknowledged = True
            self.state.ever_acknowledged = True
            self.state.resubscribe = False
            self.owner._signal(self.state)

    async def on_subscribing(self, ctx):
        if self.owner._current(self.state):
            self.state.acknowledged = False

    async def on_unsubscribed(self, ctx):
        if self.owner._current(self.state):
            self.state.acknowledged = False
            self.state.resubscribe = True
            self.owner._signal(self.state)

    async def on_publication(self, ctx):
        # Event handlers never await HTTP/SDK work and never throw into SDK reads.
        if not self.owner._current(self.state):
            return
        try:
            data = ctx.pub.data
            if isinstance(data, (str, bytes)):
                data = json.loads(data)
            if not isinstance(data, dict):
                raise ValueError("invalid event")
            if (data.get("game_key"), data.get("lobby_id")) != (
                self.state.scope.game_key, self.state.scope.lobby_id
            ):
                return
            if type(data.get("schema_version")) is not int or data["schema_version"] != 1:
                raise ValueError("unsupported schema")
            if (data.get("type") != "lobby.changed" or
                    type(data.get("version")) is not int or data["version"] < 1 or
                    not isinstance(data.get("event_id"), str)):
                raise ValueError("invalid event")
            self.owner._signal(self.state, data["version"])
        except Exception:
            self.owner._fail(self.state, CompatibilityError("invalidation_schema"))


class LobbyClient:
    def __init__(
        self, base_url: str, credential: str | None = None, *,
        http_client: httpx.AsyncClient | None = None,
        sdk_factory=centrifuge.Client,
        sleep: Callable[[float], Awaitable[None]] = asyncio.sleep,
        clock: Callable[[], float] = time.monotonic,
        release_timeout: float = 2.0,
    ):
        self.credential = credential
        self.client_instance_id = uuid4()
        self._http = AuthenticatedClient(base_url=base_url.rstrip("/"), token=credential or "",
                                         timeout=httpx.Timeout(10))
        if http_client is not None:
            self._http.set_async_httpx_client(http_client)
        self._sdk_factory, self._sleep, self._clock = sdk_factory, sleep, clock
        self._release_timeout = release_timeout
        self._sdk = None
        self._connected = False
        self._closed = False
        self._auth_error = None
        self._states: dict[Scope, ScopeState] = {}
        self._leases: dict[Scope, int] = {}
        self._dirty: set[Scope] = set()
        self._wakeup = asyncio.Event()
        self._tasks: set[asyncio.Task] = set()
        self._close_task = None
        self._sdk_lock = asyncio.Lock()
        self._epoch = 0
        self._create_payloads: dict[UUID, str] = {}

    async def __aenter__(self):
        self._check_open()
        return self

    async def __aexit__(self, exc_type, exc, tb):
        await self.close()

    def _check_open(self):
        if self._closed:
            raise ClientClosedError()

    def _require_auth(self):
        self._check_open()
        if self._auth_error:
            raise self._auth_error
        if not self.credential:
            raise AuthenticationError("missing_credential", 401)

    def _spawn(self, awaitable):
        task = asyncio.create_task(awaitable)
        self._tasks.add(task)
        def done(completed):
            self._tasks.discard(completed)
            if not completed.cancelled():
                error = completed.exception()
                if error is not None and not self._closed:
                    failure = error if isinstance(error, LobbyError) else LobbyError("background_failure")
                    for state in self._states.values():
                        self._fail(state, failure)
        task.add_done_callback(done)
        return task

    async def _request(self, operation, *, protected=True, **kwargs):
        if protected and not self.credential:
            raise AuthenticationError("missing_credential", 401)
        # Updating the existing generated transport preserves connection pooling.
        transport = self._http.get_async_httpx_client()
        if self.credential:
            transport.headers["Authorization"] = f"Bearer {self.credential}"
        else:
            transport.headers.pop("Authorization", None)
        try:
            response = await operation.asyncio_detailed(client=self._http, **kwargs)
        except httpx.TransportError as exc:
            raise TransientError("network_error") from exc
        except (KeyError, ValueError, TypeError, AttributeError) as exc:
            raise CompatibilityError("invalid_http_response") from exc
        status = int(response.status_code)
        if status >= 400:
            code = getattr(response.parsed, "code", None)
            detail = getattr(response.parsed, "detail", None)
            problem_type = getattr(response.parsed, "type_", None)
            error = response_error(status, str(code) if code else f"http_{status}",
                                   detail if isinstance(detail, str) else None,
                                   problem_type if isinstance(problem_type, str) else None)
            if isinstance(error, AuthenticationError):
                self._auth_error, self._connected = error, False
                for state in self._states.values():
                    self._fail(state, error)
                if self._sdk is not None and not self._closed:
                    self._spawn(self._disconnect())
            raise error
        if status != 204 and response.parsed is None:
            raise CompatibilityError("invalid_http_response")
        return response.parsed

    async def create_session(self, display_name: str) -> GuestSession:
        self._check_open()
        if self.credential:
            raise LobbyError("session_already_set")
        session = await self._request(create_lobby_session, protected=False,
                                      body=CreateSessionBody(display_name=display_name))
        self.credential = session.access_token
        self._auth_error = None
        return session

    async def _connection_token(self):
        self._require_auth()
        return await self._request(create_lobby_connection_token, body=ConnectionTokenInputBody())

    async def _get_token(self) -> str:
        try:
            return (await self._connection_token()).token
        except AuthenticationError as exc:
            raise centrifuge.UnauthorizedError() from exc

    async def _ensure_sdk(self):
        async with self._sdk_lock:
            self._require_auth()
            if self._sdk is not None:
                return
            token = await self._connection_token()
            self._check_open()
            self._sdk = self._sdk_factory(token.websocket_url, token=token.token,
                                         get_token=self._get_token, events=_ConnectionEvents(self))
            self._spawn(self._worker())
            self._spawn(self._ticker())
            await self._sdk.connect()

    async def create_lobby(self, *, game_key: str, name: str, capacity: int,
                           teams: list[TeamDefinition], request_id: UUID | str,
                           game_settings: dict[str, Any] | None = None) -> Snapshot:
        """game_settings is the game's own JSON object, fixed for the lobby's lifetime."""
        self._require_auth()
        request_id = UUID(str(request_id))
        settings = UNSET if game_settings is None else CreateLobbyBodyGameSettings.from_dict(game_settings)
        body = CreateLobbyBody(game_key=game_key, name=name, capacity=capacity, teams=teams,
                               request_id=request_id, client_instance_id=self.client_instance_id,
                               game_settings=settings)
        fingerprint = body.to_dict()
        if not fingerprint.get("game_settings"):
            # The server treats omitted and empty settings as the same request.
            fingerprint.pop("game_settings", None)
        payload = json.dumps(fingerprint, sort_keys=True, separators=(",", ":"))
        if request_id in self._create_payloads and self._create_payloads[request_id] != payload:
            raise LobbyError("idempotency_conflict", 409)
        self._create_payloads[request_id] = payload
        result = await self._request(create_lobby, body=body)
        return await self._attach(result)

    async def join_lobby(self, *, game_key: str, join_code: str) -> Snapshot:
        self._require_auth()
        result = await self._request(join_lobby, body=JoinLobbyBody(
            game_key=game_key, join_code=join_code, client_instance_id=self.client_instance_id))
        return await self._attach(result)

    async def _attach(self, result: Snapshot) -> Snapshot:
        self._check_open()
        scope = Scope.from_snapshot(result)
        self._leases[scope] = result.membership_generation
        old = self._states.get(scope)
        if old:
            self._fail(old, MembershipError("membership_replaced"))
            await self._remove_subscription(old)
        self._epoch += 1
        state = ScopeState(scope, result.channel, result.membership_generation, self._epoch)
        self._states[scope] = state
        try:
            await self._ensure_sdk()
            state.subscription = self._sdk.new_subscription(
                result.channel, events=_SubscriptionEvents(self, state), recoverable=True, positioned=True)
            await state.subscription.subscribe()
            # SDK subscribe() only initiates the request. ready() waits for its acknowledgement.
            await self._await_ready(state)
            await state.initial.wait()
            if state.error:
                raise state.error
            return state.accepted
        except asyncio.CancelledError:
            self._fail(state, MembershipError("synchronization_cancelled"))
            await self._remove_subscription(state)
            raise
        except LobbyError as exc:
            self._fail(state, exc)
            raise

    async def _await_ready(self, state):
        while self._current(state):
            ready = asyncio.create_task(state.subscription.ready())
            terminal = asyncio.create_task(state.terminal.wait())
            try:
                await asyncio.wait((ready, terminal), return_when=asyncio.FIRST_COMPLETED)
                if state.error:
                    raise state.error
                try:
                    await ready
                    return
                except OperationTimeoutError:
                    continue  # SDK alone owns reconnect/resubscribe transport retries.
                except CentrifugeError:
                    # A server denial is classified by the worker's authorized read.
                    state.resubscribe = True
                    self._signal(state)
                    await self._sleep(0.1)
            finally:
                for task in (ready, terminal):
                    task.cancel()
                await asyncio.gather(ready, terminal, return_exceptions=True)
        if state.error:
            raise state.error
        raise ClientClosedError()

    def _current(self, state):
        return not self._closed and not state.stopped and self._states.get(state.scope) is state

    def _signal(self, state, version=0):
        if self._current(state):
            state.target_version = max(state.target_version, version)
            state.dirty = True
            self._dirty.add(state.scope)
            self._wakeup.set()

    def _fail(self, state, error):
        state.fail(error)
        if self._states.get(state.scope) is state:
            self._dirty.discard(state.scope)
        self._wakeup.set()

    async def _disconnect(self):
        with suppress(Exception):
            await asyncio.wait_for(self._sdk.disconnect(), self._release_timeout)

    @staticmethod
    def _path(scope):
        return {"game_key": scope.game_key, "lobby_id": UUID(scope.lobby_id)}

    def _state(self, scope):
        self._require_auth()
        state = self._states.get(scope)
        if state is None:
            raise MembershipError("not_joined")
        if state.error:
            raise state.error
        return state

    async def _worker(self):
        while not self._closed:
            self._wakeup.clear()
            now = self._clock()
            candidates = [self._states[s] for s in tuple(self._dirty)
                          if s in self._states and self._states[s].retry_at <= now]
            for state in candidates:
                if not self._current(state):
                    self._dirty.discard(state.scope)
                    continue
                if not state.ever_acknowledged and not state.resubscribe:
                    self._dirty.discard(state.scope)
                    continue
                self._dirty.discard(state.scope)
                state.dirty = False
                try:
                    result = await self._request(get_lobby, **self._path(state.scope))
                    if not self._current(state):
                        continue
                    state.install(result)
                    if state.resubscribe:
                        # A stale revocation can unsubscribe a newer lease; reauthorize first.
                        state.resubscribe = False
                        await state.subscription.subscribe()
                        if state.resubscribe:
                            self._retry(state)
                            continue
                    elif state.acknowledged and state.accepted_version >= state.target_version:
                        state.initial.set()
                    if result.lobby.version < state.target_version:
                        self._retry(state)
                    else:
                        state.failures = 0
                        state.retry_at = 0
                        if state.dirty:
                            self._signal(state)
                except (TransientError, CentrifugeError):
                    self._retry(state)
                except LobbyError as exc:
                    if self._current(state):
                        self._fail(state, exc)
            if self._closed:
                break
            delays = [max(0, self._states[s].retry_at - self._clock())
                      for s in self._dirty if s in self._states]
            if delays:
                await self._wait_wakeup(min(delays))
            else:
                await self._wakeup.wait()

    def _retry(self, state):
        if self._current(state):
            state.failures += 1
            state.retry_at = self._clock() + min(0.1 * 2 ** min(state.failures - 1, 7), 10)
            self._signal(state)

    async def _wait_wakeup(self, delay):
        timer = asyncio.create_task(self._sleep(delay))
        signal = asyncio.create_task(self._wakeup.wait())
        try:
            await asyncio.wait((timer, signal), return_when=asyncio.FIRST_COMPLETED)
        finally:
            timer.cancel()
            signal.cancel()
            await asyncio.gather(timer, signal, return_exceptions=True)

    async def _ticker(self):
        deadline = self._clock() + 15
        while not self._closed:
            await self._sleep(max(0, deadline - self._clock()))
            deadline = self._clock() + 15
            await self._tick()

    async def _tick(self):
        for state in tuple(self._states.values()):
            if not self._current(state):
                continue
            if state.ever_acknowledged:
                self._signal(state)
            if not self._connected:
                continue
            try:
                result = await self._request(heartbeat_lobby, **self._path(state.scope),
                    body=HeartbeatBody(client_instance_id=self.client_instance_id,
                                       membership_generation=state.generation))
                if self._current(state):
                    if result.membership_generation != state.generation:
                        raise MembershipError("stale_membership", 409)
                    if result.version > state.accepted_version:
                        self._signal(state, result.version)
            except TransientError:
                pass  # Next scheduled heartbeat retries this same owned lease.
            except LobbyError as exc:
                if self._current(state):
                    self._fail(state, exc)

    async def snapshot(self, scope: Scope) -> Snapshot:
        state = self._state(scope)
        try:
            result = await self._request(get_lobby, **self._path(scope))
            if self._current(state):
                state.install(result)
                return state.accepted
            raise state.error or MembershipError("membership_replaced")
        except (MembershipError, AuthenticationError, CompatibilityError) as exc:
            self._fail(state, exc)
            raise

    async def _command(self, scope, operation, body):
        state = self._state(scope)
        try:
            result = await self._request(operation, **self._path(scope), body=body)
            if not self._current(state):
                raise state.error or MembershipError("membership_replaced")
            state.install(result)
            return state.accepted
        except (MembershipError, AuthenticationError, CompatibilityError) as exc:
            self._fail(state, exc)
            raise

    async def select_team(self, scope: Scope, team_id: str | None) -> Snapshot:
        return await self._command(scope, select_lobby_team, SelectTeamBody(team_id=UUID(team_id) if team_id else None))

    async def set_ready(self, scope: Scope, ready: bool) -> Snapshot:
        return await self._command(scope, set_lobby_ready, ReadyBody(ready=ready))

    async def start(self, scope: Scope) -> Snapshot:
        return await self._command(scope, start_lobby, StartInputBody())

    async def updates(self, scope: Scope) -> AsyncIterator[Snapshot]:
        state = self._state(scope)
        queue = asyncio.Queue(maxsize=1)
        state.consumers.add(queue)
        if state.accepted:
            queue.put_nowait(state.accepted)
        try:
            while True:
                item = await queue.get()
                if isinstance(item, LobbyError):
                    raise item
                yield item
        finally:
            state.consumers.discard(queue)

    async def leave(self, scope: Scope) -> None:
        state = self._state(scope)
        self._fail(state, MembershipError("left_membership"))
        try:
            await self._request(leave_lobby, **self._path(scope))
            self._leases.pop(scope, None)
        finally:
            await self._remove_subscription(state)
            if self._states.get(scope) is state:
                self._states.pop(scope)
                self._dirty.discard(scope)

    async def _remove_subscription(self, state):
        if state.subscription is not None:
            with suppress(Exception):
                await asyncio.wait_for(state.subscription.unsubscribe(), self._release_timeout)
            with suppress(Exception):
                self._sdk.remove_subscription(state.subscription)
            state.subscription = None

    async def close(self) -> None:
        if self._close_task is None:
            self._closed, self._connected = True, False
            for state in self._states.values():
                self._fail(state, ClientClosedError())
            self._close_task = asyncio.create_task(self._cleanup())
        try:
            await asyncio.shield(self._close_task)
        except asyncio.CancelledError:
            await asyncio.shield(self._close_task)
            raise

    async def _cleanup(self):
        tasks = tuple(self._tasks)
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        async def release(scope, generation):
            with suppress(Exception):
                await asyncio.wait_for(self._request(release_lobby_lease,
                    **self._path(scope), client_instance_id=self.client_instance_id,
                    generation=generation), self._release_timeout)
        await asyncio.gather(*(release(s, g) for s, g in self._leases.items()))
        for state in self._states.values():
            await self._remove_subscription(state)
        if self._sdk is not None:
            with suppress(Exception):
                await asyncio.wait_for(self._sdk.disconnect(), self._release_timeout)
        await self._http.get_async_httpx_client().aclose()
        self._leases.clear()
        self._dirty.clear()
