"""Run both real terminal clients with pipe-backed POSIX stdin."""
from __future__ import annotations

import asyncio
import json
import sys
from pathlib import Path

import pytest

from .test_live import BASE, GAME_KEY


pytestmark = pytest.mark.asyncio
CLI = Path(__file__).resolve().parents[1] / "clients/lobby-python/examples/lobby_cli.py"


async def test_two_real_cli_processes_choose_teams_and_start(tmp_path):
    processes = []
    outputs = []

    async def launch(name, mode, argument):
        session_file = tmp_path / f"{name}.json"
        process = await asyncio.create_subprocess_exec(
            sys.executable, str(CLI), "--base-url", BASE, "--game-key", GAME_KEY,
            "--session-file", str(session_file), "--new-session", name, mode, argument,
            stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        processes.append(process)
        return process, session_file

    async def until(process, predicate):
        async with asyncio.timeout(12):
            while True:
                line = (await process.stdout.readline()).decode()
                assert line, "CLI ended before expected lobby output"
                outputs.append(line)
                if predicate(line):
                    return line

    async def command(process, value):
        process.stdin.write((value + "\n").encode())
        await process.stdin.drain()

    try:
        alice, alice_file = await launch("CLI Alice", "--create", "CLI acceptance")
        code = (await until(alice, lambda line: line.startswith("join code:"))).split(":", 1)[1].strip()
        bob, bob_file = await launch("CLI Bob", "--join", code)
        await until(bob, lambda line: line.startswith("commands:"))
        await command(alice, "team 1")
        await command(alice, "ready")
        await command(bob, "team 2")
        await command(bob, "ready")
        await until(alice, lambda line: "CLI Bob (ready)" in line)
        await command(alice, "start")
        await until(alice, lambda line: line.strip() == "phase: started")
        await until(bob, lambda line: line.strip() == "phase: started")
        for process in processes:
            await command(process, "quit")
        for process in processes:
            remaining, error = await asyncio.wait_for(process.communicate(), 10)
            outputs.append(remaining.decode())
            assert process.returncode == 0, error.decode()
        for session_file in (alice_file, bob_file):
            assert session_file.stat().st_mode & 0o777 == 0o600
            credential = json.loads(session_file.read_text())["access_token"]
            assert credential not in "".join(outputs)
    finally:
        for process in processes:
            if process.returncode is None:
                process.terminate()
                try:
                    await asyncio.wait_for(process.wait(), 5)
                except TimeoutError:
                    process.kill()
                    await process.wait()
