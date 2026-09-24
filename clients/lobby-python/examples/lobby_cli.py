#!/usr/bin/env python3
"""Small terminal client for the generic lobby service.

The example deliberately keeps identity retention explicit.  A session file is
an input to this process; it is never created as a side effect of reconnecting
or of a failed lobby request.
"""
from __future__ import annotations

import argparse
import asyncio
import codecs
import datetime as dt
import io
import json
import os
import signal
import sys
import tempfile
from contextlib import suppress
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable, TextIO
from uuid import UUID, uuid4

# When this file is run directly from the repository root, make the handwritten
# package importable without requiring a prior editable install.
_PROJECT_DIR = Path(__file__).resolve().parents[1]
if str(_PROJECT_DIR) not in sys.path:
    sys.path.insert(0, str(_PROJECT_DIR))

from lobby_api_client.models import TeamDefinition  # noqa: E402
from lobby_client import AuthenticationError, LobbyClient, LobbyError, MembershipError, Scope  # noqa: E402


class CliError(Exception):
    """A user-facing argument, session-file, or terminal error."""


@dataclass(frozen=True)
class RetainedSession:
    player_id: str
    access_token: str
    expires_at: str

    def as_dict(self) -> dict[str, str]:
        return {
            "player_id": self.player_id,
            "access_token": self.access_token,
            "expires_at": self.expires_at,
        }


def _session_from_object(value: object) -> RetainedSession:
    if not isinstance(value, dict) or set(value) != {"player_id", "access_token", "expires_at"}:
        raise CliError("session file must contain only player_id, access_token, and expires_at")
    if any(not isinstance(value[key], str) or not value[key] for key in value):
        raise CliError("session file fields must be non-empty strings")
    try:
        UUID(value["player_id"])
        dt.datetime.fromisoformat(value["expires_at"].replace("Z", "+00:00"))
    except (ValueError, TypeError) as exc:
        raise CliError("session file has an invalid player_id or expires_at") from exc
    return RetainedSession(value["player_id"], value["access_token"], value["expires_at"])


def load_session(path: Path) -> RetainedSession:
    """Load and validate a retained credential without exposing its contents."""
    try:
        raw = path.read_text(encoding="utf-8")
        value = json.loads(raw)
    except FileNotFoundError as exc:
        raise CliError(f"session file does not exist: {path}") from exc
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise CliError(f"cannot read session file: {path}") from exc
    return _session_from_object(value)


def save_session(path: Path, session: object) -> None:
    """Publish a session atomically, refusing to replace an existing file."""
    if path.exists():
        raise CliError(f"session file already exists; refusing to overwrite: {path}")
    if not isinstance(session, RetainedSession):
        # Accept the generated GuestSession without importing its concrete type.
        try:
            session = RetainedSession(
                str(session.player_id), str(session.access_token), session.expires_at.isoformat()
            )
        except (AttributeError, TypeError) as exc:
            raise CliError("server returned an invalid guest session") from exc
    _session_from_object(session.as_dict())
    path.parent.mkdir(parents=True, exist_ok=True)
    temp_name: str | None = None
    fd: int | None = None
    try:
        fd, temp_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
        try:
            os.fchmod(fd, 0o600)
            payload = json.dumps(session.as_dict(), sort_keys=True, separators=(",", ":")) + "\n"
            with os.fdopen(fd, "w", encoding="utf-8") as handle:
                fd = None
                handle.write(payload)
                handle.flush()
                os.fsync(handle.fileno())
        finally:
            if fd is not None:
                os.close(fd)
                fd = None
        # A hard link is an atomic, same-filesystem no-clobber publication:
        # unlike replace/rename, a concurrent writer cannot be overwritten.
        os.link(temp_name, path)
        os.unlink(temp_name)
        temp_name = None
        os.chmod(path, 0o600)
    except CliError:
        raise
    except FileExistsError as exc:
        raise CliError(f"session file already exists; refusing to overwrite: {path}") from exc
    except (OSError, ValueError) as exc:
        raise CliError(f"cannot save session file: {path}") from exc
    finally:
        if fd is not None:
            os.close(fd)
        if temp_name:
            try:
                os.unlink(temp_name)
            except FileNotFoundError:
                pass


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Connect to a generic lobby from a POSIX terminal.")
    parser.add_argument("--base-url", required=True, help="Lobby API base URL")
    parser.add_argument("--game-key", default="island-battles")
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--create", metavar="NAME", help="create a lobby with this display name")
    mode.add_argument("--join", metavar="CODE", help="join the printed lobby code")
    parser.add_argument("--session-file", required=True, type=Path)
    parser.add_argument("--new-session", metavar="DISPLAY_NAME", help="explicitly create a guest identity")
    parser.add_argument("--capacity", type=int, default=4)
    return parser


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = build_parser()
    args = parser.parse_args(argv)
    if args.capacity < 1:
        parser.error("--capacity must be positive")
    if args.create and args.new_session is not None and not args.new_session.strip():
        parser.error("--new-session must not be empty")
    return args


def _member_text(member: Any) -> str:
    status = "ready" if getattr(member, "ready", False) else "not ready"
    if not getattr(member, "active", True):
        status += ", inactive"
    return f"{getattr(member, 'display_name', getattr(member, 'player_id', 'member'))} ({status})"


def render_snapshot(snapshot: Any) -> str:
    """Render only public lobby state; credentials are not part of a snapshot."""
    lobby = snapshot.lobby
    lines = [
        f"scope: {lobby.game_key}/{lobby.id}",
        f"join code: {lobby.join_code}",
        f"phase: {lobby.phase}",
        f"host: {lobby.host_player_id or 'none'}",
        f"version: {lobby.version}",
    ]
    members = sorted(snapshot.members or [], key=lambda member: member.join_sequence)
    teams = sorted(snapshot.teams or [], key=lambda team: team.order)
    for index, team in enumerate(teams, 1):
        team_members = [member for member in members if member.team_id == team.id]
        names = ", ".join(_member_text(member) for member in team_members) or "(empty)"
        lines.append(f"team {index}: {team.name} [{names}]")
    unassigned = [member for member in members if member.team_id is None]
    if unassigned:
        lines.append("unassigned: " + ", ".join(_member_text(member) for member in unassigned))
    return "\n".join(lines)


COMMAND_HELP = "commands: team N, team none, ready, unready, start, show, leave, quit"


class AsyncLineReader:
    """Cancellable POSIX stdin adapter using the event loop's fd watcher."""

    def __init__(self, stdin: TextIO = sys.stdin) -> None:
        self.stdin = stdin
        self._queue: asyncio.Queue[str | None] = asyncio.Queue()
        self._loop = asyncio.get_running_loop()
        self._fd: int | None = None
        self._inline = isinstance(stdin, io.StringIO)
        self._blocking: bool | None = None
        self._decoder = codecs.getincrementaldecoder("utf-8")(errors="replace")
        self._line_buffer = ""
        self._eof = False
        try:
            self._fd = stdin.fileno()
        except (AttributeError, OSError, ValueError):
            self._fd = None
        if self._inline:
            return
        if self._fd is None or not hasattr(self._loop, "add_reader"):
            raise CliError("the example requires a POSIX terminal with asyncio add_reader")
        self._blocking = os.get_blocking(self._fd)
        os.set_blocking(self._fd, False)
        self._loop.add_reader(self._fd, self._read_ready)

    def _read_ready(self) -> None:
        try:
            chunk = os.read(self._fd, 4096) if self._fd is not None else b""
        except BlockingIOError:
            return
        except (OSError, ValueError):
            chunk = b""
        if chunk:
            self._line_buffer += self._decoder.decode(chunk)
            while "\n" in self._line_buffer:
                line, self._line_buffer = self._line_buffer.split("\n", 1)
                self._queue.put_nowait(line + "\n")
            return
        self._finish_eof()

    def _finish_eof(self) -> None:
        if self._eof:
            return
        self._eof = True
        self._line_buffer += self._decoder.decode(b"", final=True)
        if self._line_buffer:
            self._queue.put_nowait(self._line_buffer)
            self._line_buffer = ""
        self._queue.put_nowait(None)
        if self._fd is not None:
            self._loop.remove_reader(self._fd)
            if self._blocking is not None:
                with suppress(OSError, ValueError):
                    os.set_blocking(self._fd, self._blocking)
            self._fd = None

    async def read(self) -> str | None:
        if self._inline:
            line = self.stdin.readline()
            return line if line else None
        return await self._queue.get()

    def close(self) -> None:
        if self._fd is not None:
            self._loop.remove_reader(self._fd)
            if self._blocking is not None:
                with suppress(OSError, ValueError):
                    os.set_blocking(self._fd, self._blocking)
            self._fd = None


async def _command_loop(client: LobbyClient, scope: Scope, initial: Any, stdin: TextIO = sys.stdin) -> bool:
    """Run commands and updates.  Return true only for an explicit leave."""
    current = initial
    print(render_snapshot(current), flush=True)
    print(COMMAND_HELP, flush=True)
    reader = AsyncLineReader(stdin)
    updates = client.updates(scope).__aiter__()
    command_task = asyncio.create_task(reader.read())
    update_task = asyncio.create_task(updates.__anext__())
    try:
        while True:
            done, _ = await asyncio.wait((command_task, update_task), return_when=asyncio.FIRST_COMPLETED)
            # Process both tasks when stdin and a publication become ready in
            # the same loop turn.  This keeps an injected (already-buffered)
            # stdin stream from starving realtime updates.
            if update_task in done:
                try:
                    incoming = update_task.result()
                except StopAsyncIteration:
                    return False
                except LobbyError as exc:
                    print(f"lobby monitoring stopped: {exc.code}; explicit new session/join may be required", file=sys.stderr, flush=True)
                    raise
                if incoming.lobby.version > current.lobby.version:
                    current = incoming
                    print(render_snapshot(current), flush=True)
                update_task = asyncio.create_task(updates.__anext__())
            if command_task in done:
                line = command_task.result()
                if line is None:
                    return False
                command_task = asyncio.create_task(reader.read())
                command = line.strip()
                if not command:
                    continue
                if command in {"quit", "exit"}:
                    return False
                if command == "show":
                    print(render_snapshot(current), flush=True)
                    continue
                if command == "leave":
                    await client.leave(scope)
                    print("left lobby; leave affects this guest identity across its running clients", flush=True)
                    return True
                try:
                    if command == "ready":
                        current = await client.set_ready(scope, True)
                    elif command == "unready":
                        current = await client.set_ready(scope, False)
                    elif command == "start":
                        current = await client.start(scope)
                    elif command.startswith("team "):
                        argument = command[5:].strip()
                        if argument == "none":
                            current = await client.select_team(scope, None)
                        elif argument.isdigit() and int(argument) > 0:
                            teams = sorted(current.teams or [], key=lambda team: team.order)
                            position = int(argument)
                            if position > len(teams):
                                print("command failed: unknown team number", flush=True)
                                continue
                            current = await client.select_team(scope, teams[position - 1].id)
                        else:
                            print("usage: team N | team none", flush=True)
                            continue
                    else:
                        print(COMMAND_HELP, flush=True)
                        continue
                    print(render_snapshot(current), flush=True)
                except LobbyError as exc:
                    print(f"command failed: {exc.code}", flush=True)
                continue
    finally:
        reader.close()
        for task in (command_task, update_task):
            if not task.done():
                task.cancel()
        await asyncio.gather(command_task, update_task, return_exceptions=True)
        close_updates = getattr(updates, "aclose", None)
        if close_updates is not None:
            with suppress(Exception):
                await close_updates()


async def run_cli(args: argparse.Namespace, *, client_factory: Callable[..., LobbyClient] = LobbyClient,
                  stdin: TextIO = sys.stdin) -> int:
    """Execute one CLI invocation and always close the wrapper exactly once."""
    if args.create and not args.new_session and not args.session_file.exists():
        raise CliError("session file is missing; use --new-session DISPLAY_NAME explicitly")
    session: RetainedSession | None = None
    client: LobbyClient | None = None
    loop = asyncio.get_running_loop()
    owner_task = asyncio.current_task()
    installed_signals: list[signal.Signals] = []
    if owner_task is not None and hasattr(loop, "add_signal_handler"):
        for signum in (signal.SIGINT, signal.SIGTERM):
            try:
                loop.add_signal_handler(signum, owner_task.cancel)
                installed_signals.append(signum)
            except (NotImplementedError, RuntimeError):
                break
    try:
        if args.new_session:
            if args.session_file.exists():
                raise CliError(f"session file already exists; refusing --new-session: {args.session_file}")
            client = client_factory(args.base_url)
            session_obj = await client.create_session(args.new_session)
            save_session(args.session_file, session_obj)
            session = load_session(args.session_file)
        else:
            session = load_session(args.session_file)
            client = client_factory(args.base_url, credential=session.access_token)
        if client is None:
            raise CliError("could not initialize lobby client")
        if args.create:
            request_id = uuid4()
            snapshot = await client.create_lobby(
                game_key=args.game_key,
                name=args.create,
                capacity=args.capacity,
                teams=[TeamDefinition(name="Red", capacity=args.capacity), TeamDefinition(name="Blue", capacity=args.capacity)],
                request_id=request_id,
            )
        else:
            snapshot = await client.join_lobby(game_key=args.game_key, join_code=args.join)
        scope = Scope.from_snapshot(snapshot)
        await _command_loop(client, scope, snapshot, stdin)
        return 0
    except (CliError, LobbyError) as exc:
        message = str(exc)
        if isinstance(exc, (AuthenticationError, MembershipError)):
            message += "; use an explicit --new-session or join again"
        print(f"error: {message}", file=sys.stderr, flush=True)
        return 2
    except Exception:
        # Do not echo generated HTTP/SDK exception text: it may contain a
        # response body or an authorization-bearing diagnostic.
        print("error: lobby operation failed", file=sys.stderr, flush=True)
        return 2
    finally:
        for signum in installed_signals:
            loop.remove_signal_handler(signum)
        if client is not None:
            await client.close()


def main(argv: list[str] | None = None) -> int:
    try:
        args = parse_args(argv)
        return asyncio.run(run_cli(args))
    except asyncio.CancelledError:
        return 130
    except KeyboardInterrupt:
        return 130
    except CliError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
