"""Smoke test for a deployment: the lobby Lambda and the VM together.

Run it with `scripts/vm.sh smoke`. It goes through the lobby's URL, which
CloudFront serves from the lobby's Function URL, and checks, in order:

1. `/healthz` returns 204.
2. `/lobby/v1/jwks.json` publishes at least one player token key, which games
   verify the lobby's player tokens with.
3. `/` opens the lobby page, and its game catalog lists at least one game.
4. A guest credential works. The credential is kept in a session file, so a
   later run reuses it. After moving to a new VM, that shows the database was
   carried over. A missing or expired credential is replaced by a new guest
   session.
5. The lobby issues a connection token, so it reaches Postgres.
6. An authenticated GET reaches the lobby with its `Authorization` header.
   CloudFront strips the header from GET requests unless its origin request
   policy forwards it.
7. Centrifugo accepts the token on its public WebSocket URL, with a valid
   certificate and the lobby's URL as the browser origin.
8. Centrifugo subscribes the guest to its personal channel, so Centrifugo
   reaches its subscribe proxy: the lobby, or, until the cutover, the game
   that still runs the lobby in its own process (deploy/README.md).
"""
from __future__ import annotations

import argparse
import asyncio
import datetime as dt
import json
import os
import sys
import tempfile
import uuid
from pathlib import Path
from urllib.parse import urlsplit

import httpx
from websockets.asyncio.client import connect
from websockets.exceptions import WebSocketException

# Guest sessions are kept for 24 hours. A credential this close to expiring is
# replaced, so it can't expire between the run before a move and the one after.
MINIMUM_REMAINING_SESSION = dt.timedelta(hours=2)
# Covers a Lambda cold start, and Centrifugo's subscribe proxy call.
HTTP_TIMEOUT_SECONDS = 30
WEBSOCKET_TIMEOUT_SECONDS = 30


class SmokeTestFailure(Exception):
    """A check failed. The message says which one and what to look at."""


def report(message: str) -> None:
    print(f"ok: {message}", flush=True)


def origin_of(url: str) -> str:
    parts = urlsplit(url)
    return f"{parts.scheme}://{parts.netloc}"


def load_session(session_file: Path) -> dict[str, str] | None:
    if not session_file.exists():
        return None
    session = json.loads(session_file.read_text(encoding="utf-8"))
    expires_at = dt.datetime.fromisoformat(session["expires_at"])
    if expires_at - dt.datetime.now(dt.UTC) < MINIMUM_REMAINING_SESSION:
        print(f"note: the saved guest credential expires at {session['expires_at']}; creating a new one", flush=True)
        return None
    return session


def save_session(session_file: Path, session: dict[str, str]) -> None:
    """Writes the credential readable only by its owner, replacing the old one atomically."""
    session_file.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_path = tempfile.mkstemp(dir=session_file.parent, prefix=".smoke-session-")
    with os.fdopen(descriptor, "w", encoding="utf-8") as temporary_file:
        json.dump(session, temporary_file)
    os.replace(temporary_path, session_file)


async def check_lobby(http: httpx.AsyncClient, session_file: Path) -> tuple[dict[str, str], dict[str, str]]:
    """Returns the guest session and a connection token for it."""
    health = await http.get("/healthz")
    if health.status_code != 204:
        raise SmokeTestFailure(f"/healthz returned {health.status_code}, expected 204")
    report("the lobby answers /healthz")

    keys = await http.get("/lobby/v1/jwks.json")
    if keys.status_code != 200 or not keys.json().get("keys"):
        raise SmokeTestFailure(f"/lobby/v1/jwks.json returned {keys.status_code} without keys: {keys.text}")
    report(f"/lobby/v1/jwks.json publishes {len(keys.json()['keys'])} player token key(s)")

    page = await http.get("/", follow_redirects=True)
    if page.status_code != 200 or page.url.path != "/lobby/ui/":
        raise SmokeTestFailure(f"/ led to {page.url.path} with {page.status_code}, expected the lobby page at "
                               "/lobby/ui/ with 200")
    catalog = await http.get("/lobby/ui/games.json")
    games = catalog.json() if catalog.status_code == 200 else []
    if not games:
        raise SmokeTestFailure(f"the lobby page's game catalog returned {catalog.status_code} with no game: "
                               "check LOBBY_GAMES, which the VM deploy writes from the tenants")
    report(f"/ opens the lobby page, which lists {', '.join(game['key'] for game in games)}")

    session = load_session(session_file)
    reused_session = session is not None
    if session is None:
        response = await http.post("/lobby/v1/sessions", json={"display_name": "Smoke test"})
        if response.status_code != 201:
            raise SmokeTestFailure(f"creating a guest session returned {response.status_code}: {response.text}")
        session = {key: response.json()[key] for key in ("player_id", "access_token", "expires_at")}
        save_session(session_file, session)
        report(f"created guest {session['player_id']} and saved its credential in {session_file}")

    response = await http.post("/lobby/v1/connection-token", json={},
                               headers={"Authorization": f"Bearer {session['access_token']}"})
    if response.status_code == 401 and reused_session:
        raise SmokeTestFailure(
            f"the lobby rejected the saved credential of guest {session['player_id']}. After moving to a new "
            "VM, that means the database wasn't carried over. Otherwise delete the session file and run again.")
    if response.status_code != 200:
        raise SmokeTestFailure(f"getting a connection token returned {response.status_code}: {response.text}")
    if reused_session:
        report(f"the saved credential of guest {session['player_id']} still works")
    report("the lobby issued a connection token, so it reaches Postgres")
    connection_token = response.json()

    # A lobby that doesn't exist: the lobby answers 404 once it has
    # authenticated the guest, and 401 without the header.
    response = await http.get(f"/lobby/v1/games/{games[0]['key']}/lobbies/{uuid.uuid4()}",
                              headers={"Authorization": f"Bearer {session['access_token']}"})
    if response.status_code == 401:
        raise SmokeTestFailure("an authenticated GET answered 401, so its Authorization header didn't reach the "
                               "lobby. Check the distribution's origin request policy.")
    if response.status_code != 404:
        raise SmokeTestFailure(f"getting a missing lobby returned {response.status_code}, expected 404: {response.text}")
    report("an authenticated GET reaches the lobby with its Authorization header")
    return session, connection_token


async def check_centrifugo(lobby_origin: str, session: dict[str, str], connection_token: dict[str, str]) -> None:
    websocket_url = connection_token["websocket_url"]
    if lobby_origin.startswith("https://") and not websocket_url.startswith("wss://"):
        raise SmokeTestFailure(f"browsers on {lobby_origin} can't open {websocket_url}; it must use wss://")
    channel = f"player:{session['player_id']}"
    # Centrifugo's JSON protocol: one command per message, and each reply
    # carries its command's id. An empty message is a ping, answered with one.
    async with connect(websocket_url, origin=lobby_origin, open_timeout=WEBSOCKET_TIMEOUT_SECONDS) as websocket:
        async def call(command_id: int, command: dict[str, object]) -> dict[str, object]:
            await websocket.send(json.dumps({"id": command_id, **command}))
            while True:
                message = await websocket.recv()
                for line in str(message).splitlines():
                    reply = json.loads(line)
                    if not reply:
                        await websocket.send("{}")
                    elif reply.get("id") == command_id:
                        return reply

        reply = await asyncio.wait_for(call(1, {"connect": {"token": connection_token["token"], "name": "smoke-test"}}),
                                       WEBSOCKET_TIMEOUT_SECONDS)
        if "error" in reply:
            raise SmokeTestFailure(f"Centrifugo refused the connection token: {reply['error']}. Check that the Lambda "
                                   "and the VM use the same connection token secret.")
        report(f"connected to {websocket_url} from origin {lobby_origin}")

        reply = await asyncio.wait_for(call(2, {"subscribe": {"channel": channel}}), WEBSOCKET_TIMEOUT_SECONDS)
        if "error" in reply:
            raise SmokeTestFailure(f"Centrifugo refused the subscription to {channel}: {reply['error']}. Check its "
                                   "proxy endpoints (centrifugo_proxy_url) and that the proxy's target and the VM "
                                   "use the same lobby proxy secret.")
        report(f"subscribed to {channel} through Centrifugo's subscribe proxy")


async def run(lobby_url: str, session_file: Path) -> None:
    lobby_origin = origin_of(lobby_url)
    async with httpx.AsyncClient(base_url=lobby_origin, timeout=HTTP_TIMEOUT_SECONDS) as http:
        session, connection_token = await check_lobby(http, session_file)
    await check_centrifugo(lobby_origin, session, connection_token)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--lobby-url", required=True, help="where players and games reach the lobby, through CloudFront")
    parser.add_argument("--session-file", type=Path, required=True,
                        help="where the guest credential is kept between runs")
    arguments = parser.parse_args()
    try:
        asyncio.run(run(arguments.lobby_url, arguments.session_file))
    except SmokeTestFailure as failure:
        print(f"FAILED: {failure}", file=sys.stderr)
        return 1
    except (httpx.HTTPError, WebSocketException, OSError, TimeoutError, ValueError) as error:
        print(f"FAILED: {type(error).__name__}: {error}", file=sys.stderr)
        return 1
    print("Smoke test passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
