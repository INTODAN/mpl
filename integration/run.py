"""Run the real lobby stack with test-owned containers and retained failure logs."""
from __future__ import annotations

import argparse
import os
import secrets
import signal
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


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
            raise RuntimeError("Backend exited before becoming ready")
        try:
            with urllib.request.urlopen(url, timeout=1) as response:
                if response.status < 400:
                    return
        except (urllib.error.URLError, TimeoutError, OSError):
            time.sleep(0.1)
    raise TimeoutError(f"Service did not become ready: {url}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--server-root", type=Path, required=True, help="Go module/workspace containing the host application")
    parser.add_argument("--server-package", required=True, help="Go package for the host executable")
    parser.add_argument("--env-prefix", default="", help="Host configuration environment-variable prefix")
    parser.add_argument("--extra-tests", type=Path, action="append", default=[], help="Additional host-owned pytest suites")
    parser.add_argument("--skip-lobby-suite", action="store_true", help="Run only --extra-tests against the started stack")
    parser.add_argument("--pytest-timeout", type=int, default=180, help="Seconds allowed for the whole pytest run")
    args = parser.parse_args()
    if args.skip_lobby_suite and not args.extra_tests:
        parser.error("--skip-lobby-suite requires --extra-tests")
    server_root = args.server_root.resolve()
    runtime = os.environ.get("LOBBY_CONTAINER_RUNTIME") or shutil.which("docker") or shutil.which("podman")
    if not runtime:
        raise RuntimeError("Docker or Podman is required; no tests were skipped")
    token = secrets.token_hex(6)
    database_name = f"lobby-acceptance-db-{token}"
    centrifugo_name = f"lobby-acceptance-events-{token}"
    database_port, http_port, socket_port = free_port(), free_port(), free_port()
    api_key, signing_key, proxy_key = (secrets.token_urlsafe(32) for _ in range(3))
    temporary = Path(tempfile.mkdtemp(prefix="lobby-acceptance-"))
    backend: subprocess.Popen[str] | None = None
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
        environment = os.environ.copy()
        environment.update({
            f"{args.env_prefix}DATABASE_URL": f"postgres://lobby:lobby@127.0.0.1:{database_port}/lobby?sslmode=disable",
            f"{args.env_prefix}HTTP_ADDR": f"127.0.0.1:{http_port}",
            f"{args.env_prefix}CENTRIFUGO_PUBLIC_URL": f"ws://127.0.0.1:{socket_port}/connection/websocket",
            f"{args.env_prefix}CENTRIFUGO_API_URL": f"http://127.0.0.1:{socket_port}/api",
            f"{args.env_prefix}CENTRIFUGO_API_KEY": api_key,
            f"{args.env_prefix}CONNECTION_TOKEN_HMAC_SECRET": signing_key,
            f"{args.env_prefix}LOBBY_PROXY_SECRET": proxy_key,
        })
        command("go", "build", "-o", str(temporary / "backend"), args.server_package, cwd=server_root, env=environment)
        backend = subprocess.Popen([str(temporary / "backend")], cwd=server_root, env=environment, stdout=log_file, stderr=subprocess.STDOUT, text=True)
        wait_http(f"http://127.0.0.1:{http_port}/healthz", backend)
        control_status = temporary / "backend-control"
        control_generation = 0

        def control_backend(signum, frame):
            nonlocal backend, control_generation
            if backend is not None and backend.poll() is None:
                backend.terminate()
                backend.wait(timeout=10)
            backend = None
            if signum == signal.SIGUSR1:
                backend = subprocess.Popen([str(temporary / "backend")], cwd=server_root, env=environment,
                                           stdout=log_file, stderr=subprocess.STDOUT, text=True)
                wait_http(f"http://127.0.0.1:{http_port}/healthz", backend)
            control_generation += 1
            control_status.write_text(str(control_generation))

        signal.signal(signal.SIGUSR1, control_backend)
        signal.signal(signal.SIGUSR2, control_backend)
        control_status.write_text("0")
        configuration = (ROOT / "centrifugo/centrifugo-config.toml").read_text().replace("http://host.docker.internal:8888", f"http://127.0.0.1:{http_port}")
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
        deadline = time.monotonic() + 30
        while True:
            try:
                with socket.create_connection(("127.0.0.1", socket_port), timeout=1):
                    break
            except OSError:
                if time.monotonic() >= deadline:
                    raise TimeoutError("Centrifugo listener did not start")
                time.sleep(0.1)
        environment.update({
            "LOBBY_TEST_API_URL": f"http://127.0.0.1:{http_port}",
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
        if backend is not None and backend.poll() is None:
            backend.terminate()
            try:
                backend.wait(timeout=10)
            except subprocess.TimeoutExpired:
                backend.kill()
                backend.wait(timeout=5)
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
