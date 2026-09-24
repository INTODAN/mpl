"""Real Centrifugo authorization checks against the live lobby stack."""
from __future__ import annotations

import asyncio
from contextlib import suppress

import pytest
import centrifuge
from centrifuge.exceptions import ReplyError
from lobby_api_client.api.lobby import create_lobby_connection_token, get_lobby
from lobby_api_client.client import AuthenticatedClient
from lobby_api_client.models import ConnectionTokenInputBody
from lobby_client import Scope

from .test_live import BASE, create


pytestmark = pytest.mark.asyncio

async def connection_client(credential: str) -> centrifuge.Client:
    async with AuthenticatedClient(BASE, token=credential, timeout=5) as generated:
        response = await create_lobby_connection_token.asyncio_detailed(
            client=generated, body=ConnectionTokenInputBody(),
        )
    assert response.status_code == 200
    token = response.parsed
    client = centrifuge.Client(token.websocket_url, token=token.token, timeout=5)
    await client.connect()
    await client.ready(timeout=5)
    return client


async def disconnect(client: centrifuge.Client | None) -> None:
    if client is not None:
        with suppress(Exception):
            await asyncio.wait_for(client.disconnect(), 5)


async def denied_subscription(client: centrifuge.Client, channel: str) -> None:
    events = SubscriptionEvents()
    subscription = client.new_subscription(channel, events=events)
    await subscription.subscribe()
    denied = await asyncio.wait_for(events.unsubscribed.get(), 5)

    assert denied.code == 403
    assert not events.subscribed.is_set()
    assert subscription.state == centrifuge.SubscriptionState.UNSUBSCRIBED


async def test_stranger_cannot_subscribe_room_or_private_channel(guests):
    owner, stranger = await guests("Owner"), await guests("Stranger")
    room = await create(owner)
    client = await connection_client(stranger.credential)
    try:
        await denied_subscription(client, room.channel)
        await denied_subscription(client, f"player:{room.lobby.host_player_id}")
    finally:
        await disconnect(client)


async def test_tampered_connection_jwt_is_rejected(guests):
    owner = await guests("Owner")
    async with AuthenticatedClient(BASE, token=owner.credential, timeout=5) as generated:
        response = await create_lobby_connection_token.asyncio_detailed(
            client=generated, body=ConnectionTokenInputBody(),
        )
    assert response.status_code == 200
    token = response.parsed
    prefix, separator, signature = token.token.rpartition(".")
    assert separator and signature
    tampered_signature = ("A" if signature[0] != "A" else "B") + signature[1:]
    disconnects = ClientEvents()
    client = centrifuge.Client(
        token.websocket_url,
        events=disconnects,
        token=f"{prefix}.{tampered_signature}",
        timeout=5,
    )
    try:
        await client.connect()
        disconnected = await asyncio.wait_for(disconnects.disconnected.get(), 5)
        assert disconnected.code >= 3500
        assert disconnected.reason
        assert client.state == centrifuge.ClientState.DISCONNECTED
    finally:
        await disconnect(client)


async def test_authorized_member_cannot_publish_history_or_presence(guests):
    owner = await guests("Owner")
    room = await create(owner)
    client = await connection_client(owner.credential)
    try:
        subscription = client.new_subscription(room.channel, recoverable=True, positioned=True)
        await subscription.subscribe()
        await subscription.ready(timeout=5)
        for operation in (
            subscription.publish({"forbidden": True}, timeout=5),
            subscription.history(timeout=5),
            subscription.presence(timeout=5),
        ):
            with pytest.raises(ReplyError):
                await operation
    finally:
        await disconnect(client)


async def test_leave_revokes_snapshot_and_future_subscriptions(guests):
    owner = await guests("Owner")
    room = await create(owner)
    scope = Scope.from_snapshot(room)
    client = await connection_client(owner.credential)
    try:
        await owner.leave(scope)
        async with AuthenticatedClient(BASE, token=owner.credential, timeout=5) as generated:
            response = await get_lobby.asyncio_detailed(scope.game_key, scope.lobby_id, client=generated)
        assert response.status_code == 404
        await denied_subscription(client, room.channel)
    finally:
        await disconnect(client)


class ClientEvents(centrifuge.ClientEventHandler):
    def __init__(self):
        self.disconnected: asyncio.Queue = asyncio.Queue()

    async def on_disconnected(self, ctx):
        self.disconnected.put_nowait(ctx)


class SubscriptionEvents(centrifuge.SubscriptionEventHandler):
    def __init__(self):
        self.subscribed = asyncio.Event()
        self.unsubscribed: asyncio.Queue = asyncio.Queue()

    async def on_subscribed(self, ctx):
        self.subscribed.set()

    async def on_unsubscribed(self, ctx):
        self.unsubscribed.put_nowait(ctx)


class Publications(centrifuge.SubscriptionEventHandler):
    def __init__(self):
        self.items: asyncio.Queue[dict] = asyncio.Queue()

    async def on_publication(self, ctx):
        self.items.put_nowait(ctx.pub.data)


async def test_wire_invalidation_has_only_the_frozen_fields(guests):
    owner = await guests("Owner")
    room = await create(owner)
    scope = Scope.from_snapshot(room)
    client = await connection_client(owner.credential)
    try:
        publications = Publications()
        subscription = client.new_subscription(room.channel, events=publications, recoverable=True, positioned=True)
        await subscription.subscribe()
        await subscription.ready(timeout=5)
        await owner.select_team(scope, room.teams[0].id)
        event = await asyncio.wait_for(publications.items.get(), 5)
        assert set(event) == {"schema_version", "event_id", "game_key", "lobby_id", "version", "type"}
        assert event["schema_version"] == 1
        assert event["type"] == "lobby.changed"
        assert (event["game_key"], event["lobby_id"]) == (scope.game_key, scope.lobby_id)
    finally:
        await disconnect(client)
