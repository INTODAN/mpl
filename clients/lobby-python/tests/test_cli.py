from __future__ import annotations

import asyncio
import io
import os
import signal
from datetime import datetime, timezone
from pathlib import Path
from types import SimpleNamespace
from uuid import UUID

import pytest

from lobby_api_client.models import GuestSession
from lobby_client import LobbyError, Scope
from examples import lobby_cli


def snapshot(*, version: int = 1):
    teams = [
        SimpleNamespace(id="blue-id", order=2, name="Blue"),
        SimpleNamespace(id="red-id", order=1, name="Red"),
    ]
    members = [
        SimpleNamespace(display_name="Alice", team_id="red-id", ready=False, active=True, join_sequence=1),
        SimpleNamespace(display_name="Bob", team_id=None, ready=False, active=True, join_sequence=2),
    ]
    lobby = SimpleNamespace(
        game_key="island-battles", id="lobby-id", join_code="ABCD", phase="waiting",
        host_player_id="host-id", version=version, name="Demo", capacity=2,
    )
    return SimpleNamespace(lobby=lobby, teams=teams, members=members)


class FakeClient:
    instances: list["FakeClient"] = []

    def __init__(self, base_url: str, credential: str | None = None):
        self.base_url = base_url
        self.credential = credential
        self.calls: list[tuple] = []
        self.closed = 0
        self._snapshot = snapshot()
        self.update_items = []
        FakeClient.instances.append(self)

    async def create_session(self, display_name):
        self.calls.append(("create_session", display_name))
        return GuestSession(
            player_id=UUID("00000000-0000-0000-0000-000000000001"),
            access_token="secret-token",
            expires_at=datetime.now(timezone.utc),
        )

    async def create_lobby(self, **kwargs):
        self.calls.append(("create_lobby", kwargs))
        return self._snapshot

    async def join_lobby(self, **kwargs):
        self.calls.append(("join_lobby", kwargs))
        return self._snapshot

    async def select_team(self, scope, team_id):
        self.calls.append(("select_team", team_id))
        return self._snapshot

    async def set_ready(self, scope, ready):
        self.calls.append(("set_ready", ready))
        return self._snapshot

    async def start(self, scope):
        self.calls.append(("start",))
        return self._snapshot

    async def leave(self, scope):
        self.calls.append(("leave",))

    async def close(self):
        self.closed += 1

    async def updates(self, scope):
        for item in self.update_items:
            yield item
        await asyncio.sleep(3600)


def test_mode_validation():
    with pytest.raises(SystemExit):
        lobby_cli.parse_args(["--base-url", "http://x", "--session-file", "/tmp/x"])
    with pytest.raises(SystemExit):
        lobby_cli.parse_args([
            "--base-url", "http://x", "--session-file", "/tmp/x", "--create", "x", "--join", "y"
        ])


def test_existing_session_preserved(tmp_path: Path):
    path = tmp_path / "session.json"
    path.write_text('{"access_token":"old","expires_at":"2026-01-01T00:00:00+00:00","player_id":"00000000-0000-0000-0000-000000000001"}')
    before = path.read_bytes()
    with pytest.raises(lobby_cli.CliError):
        lobby_cli.save_session(path, lobby_cli.RetainedSession("00000000-0000-0000-0000-000000000001", "new", "2026-01-01T00:00:00+00:00"))
    assert path.read_bytes() == before


def test_session_no_clobber_race(tmp_path: Path, monkeypatch):
    path = tmp_path / "session.json"
    original_link = lobby_cli.os.link

    def competing_writer(source, destination):
        Path(destination).write_text("competitor", encoding="utf-8")
        return original_link(source, destination)

    monkeypatch.setattr(lobby_cli.os, "link", competing_writer)
    with pytest.raises(lobby_cli.CliError):
        lobby_cli.save_session(
            path,
            lobby_cli.RetainedSession(
                "00000000-0000-0000-0000-000000000001",
                "new-token",
                "2026-01-01T00:00:00+00:00",
            ),
        )
    assert path.read_text(encoding="utf-8") == "competitor"


@pytest.mark.asyncio
async def test_missing_credential_no_fallback(tmp_path: Path):
    called = False

    def factory(*args, **kwargs):
        nonlocal called
        called = True
        return FakeClient(*args, **kwargs)

    args = lobby_cli.parse_args([
        "--base-url", "http://x", "--session-file", str(tmp_path / "missing"), "--join", "ABCD"
    ])
    assert await lobby_cli.run_cli(args, client_factory=factory) == 2
    assert called is False


def test_session_permissions_and_redaction(tmp_path: Path, capsys):
    path = tmp_path / "session.json"
    lobby_cli.save_session(path, lobby_cli.RetainedSession("00000000-0000-0000-0000-000000000001", "secret-token", "2026-01-01T00:00:00+00:00"))
    assert path.stat().st_mode & 0o777 == 0o600
    rendered = lobby_cli.render_snapshot(snapshot())
    assert "secret-token" not in rendered
    assert "access_token" not in rendered


@pytest.mark.asyncio
async def test_team_order_and_null_and_ready_false():
    client = FakeClient("http://x", "token")
    await lobby_cli._command_loop(client, Scope("island-battles", "lobby-id"), snapshot(), io.StringIO("team 1\nteam none\nunready\n"))
    assert ("select_team", "red-id") in client.calls
    assert ("select_team", None) in client.calls
    assert ("set_ready", False) in client.calls


def _retained_path(tmp_path: Path) -> Path:
    path = tmp_path / "s.json"
    lobby_cli.save_session(
        path,
        lobby_cli.RetainedSession(
            "00000000-0000-0000-0000-000000000001",
            "token",
            "2026-01-01T00:00:00+00:00",
        ),
    )
    return path


@pytest.mark.asyncio
async def test_leave_explicit(tmp_path: Path):
    FakeClient.instances.clear()
    path = _retained_path(tmp_path)
    args = lobby_cli.parse_args(["--base-url", "http://x", "--session-file", str(path), "--join", "ABCD"])
    assert await lobby_cli.run_cli(args, client_factory=FakeClient, stdin=io.StringIO("leave\n")) == 0
    assert ("leave",) in FakeClient.instances[-1].calls
    assert FakeClient.instances[-1].closed == 1


@pytest.mark.asyncio
async def test_eof_close_once_without_leave(tmp_path: Path):
    FakeClient.instances.clear()
    path = _retained_path(tmp_path)
    args = lobby_cli.parse_args(["--base-url", "http://x", "--session-file", str(path), "--join", "ABCD"])
    assert await lobby_cli.run_cli(args, client_factory=FakeClient, stdin=io.StringIO("")) == 0
    assert FakeClient.instances[-1].closed == 1
    assert ("leave",) not in FakeClient.instances[-1].calls


@pytest.mark.asyncio
async def test_cancelled_run_closes_once_without_leave(tmp_path: Path, monkeypatch):
    FakeClient.instances.clear()
    path = _retained_path(tmp_path)
    args = lobby_cli.parse_args(["--base-url", "http://x", "--session-file", str(path), "--join", "ABCD"])
    wait = asyncio.Event()

    class BlockingReader:
        def __init__(self, stdin):
            pass

        async def read(self):
            await wait.wait()

        def close(self):
            pass

    monkeypatch.setattr(lobby_cli, "AsyncLineReader", BlockingReader)
    task = asyncio.create_task(lobby_cli.run_cli(args, client_factory=FakeClient, stdin=io.StringIO()))
    await asyncio.sleep(0)
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert FakeClient.instances[-1].closed == 1
    assert ("leave",) not in FakeClient.instances[-1].calls


@pytest.mark.asyncio
async def test_signal_run_closes_once_without_leave(tmp_path: Path, monkeypatch):
    FakeClient.instances.clear()
    path = _retained_path(tmp_path)
    args = lobby_cli.parse_args(["--base-url", "http://x", "--session-file", str(path), "--join", "ABCD"])
    callbacks = {}
    loop = asyncio.get_running_loop()
    monkeypatch.setattr(loop, "add_signal_handler", lambda signum, callback: callbacks.__setitem__(signum, callback))
    monkeypatch.setattr(loop, "remove_signal_handler", lambda signum: True)
    wait = asyncio.Event()

    class BlockingReader:
        def __init__(self, stdin):
            pass

        async def read(self):
            await wait.wait()

        def close(self):
            pass

    monkeypatch.setattr(lobby_cli, "AsyncLineReader", BlockingReader)
    task = asyncio.create_task(lobby_cli.run_cli(args, client_factory=FakeClient, stdin=io.StringIO()))
    await asyncio.sleep(0)
    callbacks[signal.SIGTERM]()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert FakeClient.instances[-1].closed == 1
    assert ("leave",) not in FakeClient.instances[-1].calls


@pytest.mark.asyncio
async def test_terminal_update_failure_cleanup(tmp_path: Path):
    class FailedUpdatesClient(FakeClient):
        async def updates(self, scope):
            raise LobbyError("expired")
            yield  # pragma: no cover

    path = _retained_path(tmp_path)
    args = lobby_cli.parse_args(["--base-url", "http://x", "--session-file", str(path), "--join", "ABCD"])
    assert await lobby_cli.run_cli(args, client_factory=FailedUpdatesClient, stdin=io.StringIO("")) == 2
    assert FailedUpdatesClient.instances[-1].closed == 1


@pytest.mark.asyncio
async def test_unknown_command(tmp_path: Path, capsys):
    FakeClient.instances.clear()
    path = _retained_path(tmp_path)
    args = lobby_cli.parse_args(["--base-url", "http://x", "--session-file", str(path), "--join", "ABCD"])
    assert await lobby_cli.run_cli(args, client_factory=FakeClient, stdin=io.StringIO("what\nquit\n")) == 0
    calls = FakeClient.instances[-1].calls
    assert not any(call[0] in {"select_team", "set_ready", "start", "leave"} for call in calls)
    assert "commands:" in capsys.readouterr().out


@pytest.mark.asyncio
async def test_commands_and_updates_both_progress(tmp_path: Path, capsys):
    FakeClient.instances.clear()
    path = _retained_path(tmp_path)

    def factory(*args, **kwargs):
        client = FakeClient(*args, **kwargs)
        client.update_items = [snapshot(version=2)]
        return client

    args = lobby_cli.parse_args(["--base-url", "http://x", "--session-file", str(path), "--join", "ABCD"])
    assert await lobby_cli.run_cli(args, client_factory=factory, stdin=io.StringIO("ready\nquit\n")) == 0
    client = FakeClient.instances[-1]
    assert ("set_ready", True) in client.calls
    assert "version: 2" in capsys.readouterr().out


@pytest.mark.asyncio
async def test_pipe_reader_preserves_multiple_lines_and_partial_input():
    read_fd, write_fd = os.pipe()
    stdin = os.fdopen(read_fd, "r", encoding="utf-8", closefd=True)
    reader = lobby_cli.AsyncLineReader(stdin)
    try:
        os.write(write_fd, b"team 1\nready\n")
        assert await asyncio.wait_for(reader.read(), 1) == "team 1\n"
        assert await asyncio.wait_for(reader.read(), 1) == "ready\n"
        os.close(write_fd)
        write_fd = -1
        assert await asyncio.wait_for(reader.read(), 1) is None
    finally:
        reader.close()
        stdin.close()
        if write_fd >= 0:
            os.close(write_fd)


@pytest.mark.asyncio
async def test_pipe_partial_line_does_not_block_other_tasks():
    read_fd, write_fd = os.pipe()
    stdin = os.fdopen(read_fd, "r", encoding="utf-8", closefd=True)
    reader = lobby_cli.AsyncLineReader(stdin)
    marker = asyncio.Event()

    async def unrelated_work():
        await asyncio.sleep(0)
        marker.set()

    try:
        os.write(write_fd, b"team")
        task = asyncio.create_task(unrelated_work())
        await asyncio.wait_for(marker.wait(), 1)
        os.write(write_fd, b" 1\n")
        assert await asyncio.wait_for(reader.read(), 1) == "team 1\n"
        await task
    finally:
        reader.close()
        stdin.close()
        os.close(write_fd)


@pytest.mark.asyncio
async def test_create_request_id_stable(tmp_path: Path):
    FakeClient.instances.clear()
    args = lobby_cli.parse_args([
        "--base-url", "http://x", "--session-file", str(tmp_path / "s.json"),
        "--new-session", "Alice", "--create", "Demo",
    ])
    # The request id is generated once per invocation and is a UUID passed to create.
    assert await lobby_cli.run_cli(args, client_factory=FakeClient, stdin=io.StringIO("quit\n")) == 0
    create_calls = [call for call in FakeClient.instances[-1].calls if call[0] == "create_lobby"]
    assert len(create_calls) == 1
    assert isinstance(create_calls[0][1]["request_id"], UUID)
    assert create_calls[0][1]["name"] == "Demo"


@pytest.mark.asyncio
async def test_quit_and_leave_cleanup(tmp_path: Path):
    FakeClient.instances.clear()
    path = tmp_path / "s.json"
    lobby_cli.save_session(path, lobby_cli.RetainedSession("00000000-0000-0000-0000-000000000001", "token", "2026-01-01T00:00:00+00:00"))
    args = lobby_cli.parse_args(["--base-url", "http://x", "--session-file", str(path), "--join", "ABCD"])
    assert await lobby_cli.run_cli(args, client_factory=FakeClient, stdin=io.StringIO("quit\n")) == 0
    assert FakeClient.instances[-1].closed == 1
    assert ("leave",) not in FakeClient.instances[-1].calls
