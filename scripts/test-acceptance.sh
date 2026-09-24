#!/usr/bin/env bash
set -euo pipefail
lobby_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
export GOCACHE="${GOCACHE:-/tmp/lobby-go-cache}"
export UV_CACHE_DIR="${UV_CACHE_DIR:-/tmp/lobby-uv-cache}"
exec uv run --project "$lobby_root/clients/lobby-python" --locked python "$lobby_root/integration/run.py" "$@"
