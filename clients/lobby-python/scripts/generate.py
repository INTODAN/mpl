#!/usr/bin/env python3
"""Generate and verify the lobby HTTP SDK from the checked-in OpenAPI file."""

from __future__ import annotations

import argparse
import filecmp
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[3]
PROJECT = ROOT / "clients" / "lobby-python"
ARTIFACT = ROOT / "api" / "openapi.json"
CONFIG = PROJECT / "generator-config.yaml"
GENERATED = PROJECT / "generated"
EXPECTED_OPERATIONS = {
    "create_lobby_session",
    "create_lobby_connection_token",
    "create_lobby",
    "join_lobby",
    "get_lobby",
    "select_lobby_team",
    "set_lobby_ready",
    "start_lobby",
    "leave_lobby",
    "heartbeat_lobby",
    "release_lobby_lease",
}


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="compare generated files without changing them",
    )
    args = parser.parse_args(argv)

    try:
        with tempfile.TemporaryDirectory(prefix="lobby-sdk-") as temporary:
            temporary_path = Path(temporary)
            source_path = temporary_path / "lobby-openapi.json"
            generated_path = temporary_path / "lobby-sdk"
            export_openapi(source_path)
            assert_artifact_matches(source_path)
            run_generator(source_path, generated_path)
            remove_caches(generated_path)
            validate_generated(generated_path)
            if args.check:
                compare_tree(generated_path, GENERATED)
            else:
                replace_generated(generated_path)
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"generation failed: {error}", file=sys.stderr)
        return 1
    return 0


def export_openapi(destination: Path) -> None:
    env = os.environ.copy()
    env.setdefault("GOCACHE", "/tmp/island-lobby-go-cache")
    subprocess.run(
        [
            "go",
            "run",
            "./cmd/export-lobby-openapi",
            "--output",
            str(destination),
        ],
        cwd=ROOT,
        env=env,
        check=True,
    )


def assert_artifact_matches(exported: Path) -> None:
    if not exported.read_bytes() == ARTIFACT.read_bytes():
        raise RuntimeError(
            "exported OpenAPI differs from api/openapi.json; "
            "run task 14's regeneration command first"
        )


def run_generator(source: Path, destination: Path) -> None:
    subprocess.run(
        [
            "openapi-python-client",
            "generate",
            "--path",
            str(source),
            "--config",
            str(CONFIG),
            "--meta",
            "uv",
            "--output-path",
            str(destination),
            "--fail-on-warning",
        ],
        check=True,
    )


def validate_generated(directory: Path) -> None:
    if not directory.is_dir():
        raise RuntimeError("generator did not create an output project")
    package = directory / "lobby_api_client"
    if not package.is_dir():
        raise RuntimeError("generated lobby_api_client package is missing")
    operations = {
        path.stem
        for path in package.rglob("*.py")
        if path.parent.name == "lobby" and path.stem in EXPECTED_OPERATIONS
    }
    missing = EXPECTED_OPERATIONS - operations
    if missing:
        raise RuntimeError(
            f"generated operations missing: {', '.join(sorted(missing))}"
        )
    if not any(path.name == "lobby_invalidation.py" for path in package.rglob("*.py")):
        raise RuntimeError("generated LobbyInvalidation model is missing")


def remove_caches(directory: Path) -> None:
    for path in directory.rglob("*"):
        if path.is_dir() and path.name in {
            "__pycache__",
            ".ruff_cache",
            ".pytest_cache",
        }:
            shutil.rmtree(path)


def replace_generated(source: Path) -> None:
    destination = GENERATED
    backup: Path | None = None
    if destination.exists():
        backup = Path(tempfile.mkdtemp(prefix="lobby-generated-backup-", dir=PROJECT))
        backup.rmdir()
        destination.rename(backup)
    try:
        shutil.copytree(source, destination)
    except OSError:
        if destination.exists():
            shutil.rmtree(destination)
        if backup is not None and not destination.exists():
            backup.rename(destination)
        raise
    if backup is not None:
        shutil.rmtree(backup)


def compare_tree(expected: Path, actual: Path) -> None:
    if not actual.is_dir():
        raise RuntimeError(f"generated output is missing: {actual}")
    expected_files = relative_files(expected)
    actual_files = relative_files(actual)
    if expected_files != actual_files:
        missing = sorted(expected_files - actual_files)
        extra = sorted(actual_files - expected_files)
        details: list[str] = []
        if missing:
            details.append(f"missing: {', '.join(missing)}")
        if extra:
            details.append(f"extra: {', '.join(extra)}")
        raise RuntimeError("generated tree differs (" + "; ".join(details) + ")")
    for relative in sorted(expected_files):
        if not filecmp.cmp(expected / relative, actual / relative, shallow=False):
            raise RuntimeError(f"generated file differs: {relative}")


def relative_files(directory: Path) -> set[Path]:
    ignored = {"__pycache__", ".ruff_cache", ".pytest_cache"}
    return {
        path.relative_to(directory)
        for path in directory.rglob("*")
        if path.is_file()
        and not any(part in ignored for part in path.relative_to(directory).parts)
    }


if __name__ == "__main__":
    raise SystemExit(main())
