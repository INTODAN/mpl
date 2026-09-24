from __future__ import annotations

import hashlib
from pathlib import Path
import subprocess

import pytest


ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "clients" / "lobby-python" / "scripts" / "generate.py"
TOOLS = ROOT / "clients" / "lobby-python" / "tools"
GENERATED = ROOT / "clients" / "lobby-python" / "generated"


def run_generation(*arguments: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [
            "uv",
            "run",
            "--project",
            str(TOOLS),
            "--locked",
            "python",
            str(SCRIPT),
            *arguments,
        ],
        cwd=ROOT,
        check=False,
        text=True,
        capture_output=True,
    )


def tree_digest(directory: Path) -> str:
    digest = hashlib.sha256()
    for path in sorted(directory.rglob("*")):
        if path.is_file() and not any(
            part in {"__pycache__", ".ruff_cache", ".pytest_cache"}
            for part in path.parts
        ):
            digest.update(str(path.relative_to(directory)).encode())
            digest.update(path.read_bytes())
    return digest.hexdigest()


def test_generation_check() -> None:
    result = run_generation("--check")
    assert result.returncode == 0, result.stderr


def test_generation_repeatable() -> None:
    first = tree_digest(GENERATED)
    result = run_generation("--check")
    assert result.returncode == 0, result.stderr
    assert tree_digest(GENERATED) == first


@pytest.mark.parametrize(
    "relative", [Path("README.md"), Path("lobby_api_client/models/lobby.py")]
)
def test_generation_output_contains_metadata(relative: Path) -> None:
    assert (GENERATED / relative).is_file()
