"""Bounded per-scope reconciliation state, independent of the socket transport."""
from __future__ import annotations

import asyncio
from dataclasses import dataclass, field
from typing import Any

from lobby_api_client.models import Snapshot

from .errors import CompatibilityError, LobbyError


@dataclass(frozen=True)
class Scope:
    game_key: str
    lobby_id: str

    @classmethod
    def from_snapshot(cls, snapshot: Snapshot) -> Scope:
        return cls(snapshot.lobby.game_key, snapshot.lobby.id)


@dataclass(eq=False)
class ScopeState:
    scope: Scope
    channel: str
    generation: int
    epoch: int
    subscription: Any = None
    accepted: Snapshot | None = None
    target_version: int = 0
    dirty: bool = False
    acknowledged: bool = False
    ever_acknowledged: bool = False
    resubscribe: bool = False
    stopped: bool = False
    error: LobbyError | None = None
    retry_at: float = 0
    failures: int = 0
    initial: asyncio.Event = field(default_factory=asyncio.Event)
    terminal: asyncio.Event = field(default_factory=asyncio.Event)
    consumers: set[asyncio.Queue] = field(default_factory=set)

    @property
    def accepted_version(self) -> int:
        return self.accepted.lobby.version if self.accepted else 0

    def install(self, snapshot: Snapshot) -> bool:
        if Scope.from_snapshot(snapshot) != self.scope or snapshot.channel != self.channel:
            raise CompatibilityError("snapshot_scope_mismatch")
        if snapshot.membership_generation != self.generation:
            # Only an explicit join acquires a newer generation's lease.
            from .errors import MembershipError
            raise MembershipError("stale_membership", 409)
        if snapshot.lobby.version <= self.accepted_version:
            return False
        self.accepted = snapshot
        for queue in self.consumers:
            if queue.full():
                queue.get_nowait()
            queue.put_nowait(snapshot)
        return True

    def fail(self, error: LobbyError) -> None:
        if self.stopped:
            return
        self.stopped, self.error, self.dirty = True, error, False
        self.initial.set()
        self.terminal.set()
        for queue in self.consumers:
            if queue.full():
                queue.get_nowait()
            queue.put_nowait(error)
