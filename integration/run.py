"""Run the real lobby stack with test-owned containers and retained failure logs.

The lobby runs as its own process, cmd/lobby. A game's host, when one is
given, runs as a second process that the lobby reaches over HTTP, and a front
gives clients one URL for both: /lobby/ and /internal/lobby/ go to the lobby,
everything else to the game.
"""
from __future__ import annotations

import argparse
import http.client
import http.server
import json
import os
import secrets
import signal
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

# The lobby's paths, which the front sends to the lobby.
LOBBY_PREFIXES = ("/lobby/", "/internal/lobby/")
# Headers that belong to one connection, which the front doesn't pass on.
HOP_BY_HOP = {"connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer",
              "transfer-encoding", "upgrade", "content-length"}


def free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def command(*args: str, **kwargs: object) -> subprocess.CompletedProcess[str]:
    return subprocess.run(args, check=True, text=True, **kwargs)


def wait_http(url: str, process: subprocess.Popen[str] | None = None) -> None:
    deadline = time.monotonic() + 45
    while time.monotonic() < deadline:
        if process is not None and process.poll() is not None:
            raise RuntimeError(f"A host exited before becoming ready: {url}")
        try:
            with urllib.request.urlopen(url, timeout=1) as response:
                if response.status < 400:
                    return
        except (urllib.error.URLError, TimeoutError, OSError):
            time.sleep(0.1)
    raise TimeoutError(f"Service did not become ready: {url}")


def wait_listening(port: int, process: subprocess.Popen[str] | None, name: str) -> None:
    deadline = time.monotonic() + 30
    while True:
        if process is not None and process.poll() is not None:
            raise RuntimeError(f"{name} exited before listening")
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=1):
                return
        except OSError:
            if time.monotonic() >= deadline:
                raise TimeoutError(f"{name} did not start")
            time.sleep(0.1)


class Front(http.server.ThreadingHTTPServer):
    """One origin for the lobby and the game, as a CDN in front of both would be."""

    daemon_threads = True

    def __init__(self, port: int, lobby_port: int, game_port: int) -> None:
        super().__init__(("127.0.0.1", port), FrontHandler)
        self.lobby_port, self.game_port = lobby_port, game_port

    def handle_error(self, request: object, client_address: object) -> None:
        # A client that hangs up before its answer, as a cancelled request
        # does, is no failure of the front.
        if not isinstance(sys.exc_info()[1], ConnectionError):
            super().handle_error(request, client_address)


class FrontHandler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server: Front

    def log_message(self, format: str, *args: object) -> None:
        pass

    def read_body(self) -> bytes:
        if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
            body = bytearray()
            while True:
                size = int(self.rfile.readline().split(b";")[0].strip(), 16)
                if size == 0:
                    while self.rfile.readline() not in (b"\r\n", b"\n", b""):
                        pass
                    return bytes(body)
                body += self.rfile.read(size)
                self.rfile.readline()
        return self.rfile.read(int(self.headers.get("Content-Length") or 0))

    def forward(self) -> None:
        body = self.read_body()
        path = self.path.split("?", 1)[0]
        port = self.server.lobby_port if path.startswith(LOBBY_PREFIXES) else self.server.game_port
        headers = {name: value for name, value in self.headers.items() if name.lower() not in HOP_BY_HOP}
        upstream = http.client.HTTPConnection("127.0.0.1", port, timeout=120)
        try:
            upstream.request(self.command, self.path, body=body if body or self.command not in ("GET", "HEAD") else None, headers=headers)
            response = upstream.getresponse()
            content = response.read()
            status, reason, answer_headers = response.status, response.reason, response.getheaders()
        except OSError as error:
            content = f"the front could not reach 127.0.0.1:{port}: {error}".encode()
            status, reason, answer_headers = 502, "Bad Gateway", [("Content-Type", "text/plain")]
        finally:
            upstream.close()
        self.send_response(status, reason)
        for name, value in answer_headers:
            if name.lower() not in HOP_BY_HOP:
                self.send_header(name, value)
        self.send_header("Content-Length", str(len(content)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(content)

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = do_HEAD = do_OPTIONS = forward


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--server-root", type=Path, help="Go module/workspace containing the game's host")
    parser.add_argument("--server-package", help="Go package of the game's host, which runs beside the lobby")
    parser.add_argument("--migrate-package", help="Go package that migrates the game host's schema before each start")
    parser.add_argument("--env-prefix", default="", help="The game host's configuration environment-variable prefix")
    parser.add_argument("--lobby-games-command", help="A command, run in --server-root, that prints the lobby's LOBBY_GAMES "
                        "with the game added, given the game host's URL as its last argument")
    parser.add_argument("--extra-tests", type=Path, action="append", default=[], help="Additional host-owned pytest suites")
    parser.add_argument("--skip-lobby-suite", action="store_true", help="Run only --extra-tests against the started stack")
    parser.add_argument("--pytest-timeout", type=int, default=180, help="Seconds allowed for the whole pytest run")
    parser.add_argument("--mock-game", action="store_true", help="Serve the lobby's mock game, without its rules, as an "
                        "HTTP game named mock-game, added to the lobby's LOBBY_GAMES")
    args = parser.parse_args()
    if args.skip_lobby_suite and not args.extra_tests:
        parser.error("--skip-lobby-suite requires --extra-tests")
    if (args.server_package is None) != (args.server_root is None):
        parser.error("--server-root and --server-package go together")
    if (args.migrate_package or args.lobby_games_command) and not args.server_package:
        parser.error("--migrate-package and --lobby-games-command need --server-package")
    server_root = args.server_root.resolve() if args.server_root else None
    runtime = os.environ.get("LOBBY_CONTAINER_RUNTIME") or shutil.which("docker") or shutil.which("podman")
    if not runtime:
        raise RuntimeError("Docker or Podman is required; no tests were skipped")
    token = secrets.token_hex(6)
    database_name = f"lobby-acceptance-db-{token}"
    centrifugo_name = f"lobby-acceptance-events-{token}"
    database_port, lobby_port, game_port, socket_port, front_port = (free_port() for _ in range(5))
    lobby_url, game_url = f"http://127.0.0.1:{lobby_port}", f"http://127.0.0.1:{game_port}"
    api_key, signing_key, proxy_key = (secrets.token_urlsafe(32) for _ in range(3))
    temporary = Path(tempfile.mkdtemp(prefix="lobby-acceptance-"))
    hosts: list[subprocess.Popen[str]] = []
    mock_game: subprocess.Popen[str] | None = None
    front: Front | None = None
    # The lobby, the game's host and the mock game share one log.
    log_file = (temporary / "backend.log").open("w+")
    success = False
    try:
        command(runtime, "run", "-d", "--name", database_name,
                "-e", "POSTGRES_USER=lobby", "-e", "POSTGRES_PASSWORD=lobby",
                "-e", "POSTGRES_DB=lobby", "-p", f"127.0.0.1:{database_port}:5432",
                "docker.io/library/postgres:18-alpine", stdout=subprocess.DEVNULL)
        deadline = time.monotonic() + 45
        while True:
            ready = subprocess.run([runtime, "exec", database_name, "psql", "-h", "127.0.0.1",
                                    "-U", "lobby", "-d", "lobby", "-At", "-c", "SELECT 1"], capture_output=True)
            if ready.returncode == 0:
                break
            if time.monotonic() >= deadline:
                raise TimeoutError("Disposable PostgreSQL did not become ready")
            time.sleep(0.1)
        database_url = f"postgres://lobby:lobby@127.0.0.1:{database_port}/lobby?sslmode=disable"
        environment = os.environ.copy()
        # The lobby's own settings (lobby/README.md, "Running the lobby").
        environment.update({
            "LOBBY_DATABASE_URL": database_url,
            "LOBBY_HTTP_ADDR": f"127.0.0.1:{lobby_port}",
            "LOBBY_PUBLIC_URL": lobby_url,
            "LOBBY_CENTRIFUGO_PUBLIC_URL": f"ws://127.0.0.1:{socket_port}/connection/websocket",
            "LOBBY_CENTRIFUGO_API_URL": f"http://127.0.0.1:{socket_port}/api",
            "LOBBY_CENTRIFUGO_API_KEY": api_key,
            "LOBBY_CONNECTION_TOKEN_HMAC_SECRET": signing_key,
            "LOBBY_PROXY_SECRET": proxy_key,
        })
        # One player token key, an Ed25519 seed of 32 bytes in base64url.
        environment.setdefault("LOBBY_PLAYER_TOKEN_KEYS", f"acceptance:{secrets.token_urlsafe(32)}")
        if args.server_package:
            # The game's host shares the database, pushes its own events to
            # Centrifugo, and reaches the lobby at its URL.
            environment.update({
                f"{args.env_prefix}DATABASE_URL": database_url,
                f"{args.env_prefix}HTTP_ADDR": f"127.0.0.1:{game_port}",
                f"{args.env_prefix}CENTRIFUGO_API_URL": f"http://127.0.0.1:{socket_port}/api",
                f"{args.env_prefix}CENTRIFUGO_API_KEY": api_key,
                f"{args.env_prefix}LOBBY_URL": lobby_url,
            })
        else:
            environment.setdefault("LOBBY_TEST_GAME_KEY", "mock-game")
        if args.mock_game:
            # The lobby calls the mock game over HTTP, as LOBBY_GAMES configures
            # it, and the mock game reads the lobby's game API.
            mock_port, mock_secret = free_port(), secrets.token_urlsafe(32)
            games = json.loads(environment.get("LOBBY_GAMES") or "[]")
            games.append({"key": "mock-game", "name": "Mock game", "hooks_url": f"http://127.0.0.1:{mock_port}/hooks",
                          "secret_env": "LOBBY_GAME_SECRET_MOCK_GAME",
                          # The lobby page lists every game; nothing here opens the result.
                          "frontend": {"result_url": "/mock-game/{lobby_id}/result"}})
            environment.update({"LOBBY_GAMES": json.dumps(games), "LOBBY_GAME_SECRET_MOCK_GAME": mock_secret})
            command("go", "build", "-o", str(temporary / "mock-game"), "./cmd/mock-game", cwd=ROOT, env=environment)
            mock_game = subprocess.Popen([str(temporary / "mock-game")], cwd=ROOT, stdout=log_file, stderr=subprocess.STDOUT, text=True,
                                         env={**environment, "MOCK_GAME_ADDR": f"127.0.0.1:{mock_port}", "MOCK_GAME_SECRET": mock_secret,
                                              "MOCK_GAME_LOBBY_URL": lobby_url, "MOCK_GAME_RULES": "off"})
            wait_listening(mock_port, mock_game, "The mock game")
        if args.lobby_games_command:
            environment["LOBBY_GAMES"] = subprocess.run([*args.lobby_games_command.split(), game_url], cwd=server_root, env=environment,
                                                        check=True, text=True, capture_output=True).stdout.strip()
        if not environment.get("LOBBY_GAMES"):
            raise RuntimeError("The lobby needs a game: pass --mock-game, --lobby-games-command or set LOBBY_GAMES")
        command("go", "build", "-o", str(temporary / "lobby"), "./cmd/lobby", cwd=ROOT, env=environment)
        if args.server_package:
            command("go", "build", "-o", str(temporary / "backend"), args.server_package, cwd=server_root, env=environment)
        if args.migrate_package:
            command("go", "build", "-o", str(temporary / "migrate"), args.migrate_package, cwd=server_root, env=environment)

        def start_hosts() -> list[subprocess.Popen[str]]:
            started: list[subprocess.Popen[str]] = []
            # A deploy migrates before the new build serves, so every start
            # does too. The lobby migrates its own schema as it starts.
            if args.migrate_package:
                log_file.flush()
                command(str(temporary / "migrate"), cwd=server_root, env=environment, stdout=log_file, stderr=subprocess.STDOUT)
            lobby = subprocess.Popen([str(temporary / "lobby")], cwd=ROOT, env=environment, stdout=log_file, stderr=subprocess.STDOUT, text=True)
            started.append(lobby)
            wait_http(f"{lobby_url}/healthz", lobby)
            if args.server_package:
                game = subprocess.Popen([str(temporary / "backend")], cwd=server_root, env=environment,
                                        stdout=log_file, stderr=subprocess.STDOUT, text=True)
                started.append(game)
                wait_http(f"{game_url}/healthz", game)
            return started

        def stop_hosts() -> None:
            for host in reversed(hosts):
                if host.poll() is None:
                    host.terminate()
                    try:
                        host.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        host.kill()
                        host.wait(timeout=5)
            hosts.clear()

        hosts.extend(start_hosts())
        control_status = temporary / "backend-control"
        control_generation = 0

        # SIGUSR1 restarts the hosts, the lobby and the game's; SIGUSR2 stops them.
        def control_hosts(signum, frame):
            nonlocal control_generation
            stop_hosts()
            if signum == signal.SIGUSR1:
                hosts.extend(start_hosts())
            control_generation += 1
            control_status.write_text(str(control_generation))

        signal.signal(signal.SIGUSR1, control_hosts)
        signal.signal(signal.SIGUSR2, control_hosts)
        control_status.write_text("0")
        # Centrifugo's proxy calls go to the lobby.
        configuration = (ROOT / "centrifugo/centrifugo-config.toml").read_text().replace("http://host.docker.internal:8890", lobby_url)
        configuration_path = temporary / "centrifugo.toml"
        configuration_path.write_text(configuration)
        command(runtime, "run", "-d", "--name", centrifugo_name, "--network", "host",
                "-v", f"{configuration_path}:/centrifugo/config.toml:ro,Z",
                "-e", f"CENTRIFUGO_HTTP_SERVER_PORT={socket_port}",
                "-e", f"CENTRIFUGO_CLIENT_TOKEN_HMAC_SECRET_KEY={signing_key}",
                "-e", f"CENTRIFUGO_HTTP_API_KEY={api_key}",
                "-e", f"CENTRIFUGO_VAR_LOBBY_PROXY_SECRET={proxy_key}",
                "-e", "CENTRIFUGO_ADMIN_ENABLED=false",
                "docker.io/centrifugo/centrifugo:v6.9.6", "centrifugo", "-c", "/centrifugo/config.toml",
                stdout=subprocess.DEVNULL)
        # A non-WebSocket GET is expected to reject the upgrade, but it proves
        # the listener is up. Actual authenticated connectivity is tested below.
        wait_listening(socket_port, None, "Centrifugo listener")
        api_url = lobby_url
        if args.server_package:
            front = Front(front_port, lobby_port, game_port)
            threading.Thread(target=front.serve_forever, daemon=True).start()
            api_url = f"http://127.0.0.1:{front_port}"
        environment.update({
            "LOBBY_TEST_API_URL": api_url,
            "LOBBY_TEST_WEBSOCKET_URL": f"ws://127.0.0.1:{socket_port}/connection/websocket",
            "LOBBY_TEST_CENTRIFUGO_API_URL": f"http://127.0.0.1:{socket_port}/api",
            "LOBBY_TEST_CENTRIFUGO_API_KEY": api_key,
            "LOBBY_TEST_CONTAINER_RUNTIME": runtime,
            "LOBBY_TEST_CENTRIFUGO_CONTAINER": centrifugo_name,
            "LOBBY_TEST_POSTGRES_CONTAINER": database_name,
            "LOBBY_TEST_RUNNER_PID": str(os.getpid()),
            "LOBBY_TEST_BACKEND_CONTROL": str(control_status),
            "LOBBY_TEST_BACKEND_LOG": str(temporary / "backend.log"),
        })
        suites = [] if args.skip_lobby_suite else [str(ROOT / "integration")]
        suites += [str(path.resolve()) for path in args.extra_tests]
        result = subprocess.run([sys.executable, "-m", "pytest", *suites, "-q", "--tb=short", "--disable-warnings"], cwd=ROOT, env=environment, timeout=args.pytest_timeout)
        success = result.returncode == 0
        return result.returncode
    finally:
        if front is not None:
            front.shutdown()
            front.server_close()
        if mock_game is not None and mock_game.poll() is None:
            mock_game.terminate()
            mock_game.wait(timeout=10)
        for host in reversed(hosts):
            if host.poll() is None:
                host.terminate()
                try:
                    host.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    host.kill()
                    host.wait(timeout=5)
        log_file.close()
        if not success:
            for name in (database_name, centrifugo_name):
                result = subprocess.run([runtime, "logs", name], text=True, capture_output=True)
                (temporary / f"{name}.log").write_text(result.stdout + result.stderr)
            print(f"Acceptance logs retained at {temporary}", file=sys.stderr)
        for name in (centrifugo_name, database_name):
            subprocess.run([runtime, "rm", "-f", "-v", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if success:
            shutil.rmtree(temporary)


if __name__ == "__main__":
    raise SystemExit(main())
