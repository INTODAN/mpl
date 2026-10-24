# Implementation status

Implemented on 2026-09-20. The task files preserve the detailed specifications; this file records delivery scope and verification notes. Cleanup verification completed after removal of retired data and API paths. Model assignments reflect complexity rather than using the most expensive model for every task.

| Task | Implementation / review | Evidence |
| --- | --- | --- |
| 01 Domain | Astra / high | Domain tests and dependency boundary passed |
| 02 Schema | Terra / high | Real PostgreSQL constraints and repeatability tests passed |
| 03 Identity | Terra / high | Credential/JWT/session expiry tests passed |
| 04 Transaction store | Astra / high | Real PostgreSQL concurrency, idempotency and claim-fencing tests passed |
| 05 Create/join | Coordinator; Astra / high review | Real PostgreSQL service tests passed |
| 06 Teams/lifecycle | Coordinator; Astra / high review | Readiness, start, immutable roster and host tests passed |
| 07 Reconnect leases | Coordinator; Astra / high review | Generation, deadline, multi-client and cleanup tests passed |
| 08 Huma API | Terra / high; coordinator validation review | Auth-before-validation, typed errors, null/false and schema tests passed |
| 09 Centrifugo adapter | Coordinator | Proxy authorization/transport tests passed; actual server exercised |
| 10 Delivery worker | Coordinator | Retry, failure isolation, cancellation and generation-fence tests passed |
| 12 Island adapter | Terra / high | Atomic bindings, reauthorized replay, concurrency and existing game regressions passed |
| 13 Bootstrap/config | Terra / high plus coordinator wiring | Full Go compilation/tests; actual server startup and restart passed |
| 14 OpenAPI exporter | Luna / high | Offline deterministic exporter tests and artifact check passed |
| 15 Generated Python | Luna / high | Locked generation, operation/model and generated HTTP tests passed |
| 16 Python runtime | Astra / high | 40 deterministic runtime tests passed; live reconnect and reconciliation passed |
| 17 Python CLI | Luna / high | 17 CLI tests and two real CLI processes passed |
| 18 Acceptance | Coordinator, Terra protocol tests | 14 live tests passed against the actual stack |

## Completed verification

- `go test ./...`: passed, including existing game suites and disposable PostgreSQL tests.
- `go vet ./...`: passed.
- `go test -race ./lobby/... ./backend/lobbyadapter/...`: passed.
- Huma artifacts: `go run ./lobby/cmd/export-lobby-openapi --output lobby/api/openapi.json --events-output lobby/api/events.json --check`: passed.
- Python generation: `uv run --project lobby/clients/lobby-python/tools --locked python lobby/clients/lobby-python/scripts/generate.py --check`: passed, with no generator warnings or output difference.
- Existing Python verification: `uv run --project lobby/clients/lobby-python --locked pytest lobby/clients/lobby-python/tests -q --disable-warnings`: **67 passed**. The pinned pytest-asyncio dependency emits Python 3.14 deprecation warnings; no tests were skipped.
- Live checks covered: two-player team/ready/start and idempotent Island binding; second generic game; real Centrifugo interruption/history loss; periodic snapshot convergence while publication is unavailable; a failed push while Centrifugo is down is retried briefly and logged (before #28, a durable outbox retry); subscription/snapshot race; multiple clients sharing a seat; leave; backend restart with persisted expired leases; rejected client publish/history/presence; invalidation payload contains exactly six fields. Fresh-schema startup and restart repeatability also passed.
- Protocol tests passed by observing SDK denial callbacks: centrifuge-python 0.6.0 `Subscription.ready()` can return after a failed subscription future, so a lack of exception alone does not imply authorization. The server's actual 403 replies and terminal invalid-JWT disconnects were verified.

- `./scripts/test-lobby-acceptance.sh`: **14 passed** in 30.30 seconds after cleanup, including fresh-database startup, restart repeatability, current-table/game-field preservation, two real CLI processes, and all protocol denials.
- Ruff checks for handwritten client/example/integration Python and `git diff --check`: passed.

## Environment

Go 1.26.4 (module declares 1.25), Python 3.14.6, uv 0.11.19, Centrifugo 6.9.6, PostgreSQL 18-alpine, openapi-python-client 0.29.0, centrifuge-python 0.6.0. Generator and runtime dependencies have separate committed uv locks.

The local environment has Podman rather than Docker. Go database tests use a temporary Docker-compatible Podman socket (`DOCKER_HOST=unix:///tmp/island-lobby-podman.sock`, `TESTCONTAINERS_RYUK_DISABLED=true`); every fixture still terminates its own container. The live runner launches unique containers directly and never connects to the developer database. Go/uv caches use writable paths under `/tmp`. Centrifugo's actual pinned image passed `checkconfig --strict`; Docker Compose itself was unavailable locally.

## Scope limits

Guest credentials expire after 24 hours and have no account recovery mechanism. Starting freezes a roster; the explicit Island binding creates the linked game and team mapping only. Lambda routing, simulation startup, round timers, C#/TypeScript SDKs and unrelated game-command authorization remain outside this extraction.
