# Live lobby acceptance

This suite belongs to the generic lobby component. It checks the public lobby API, PostgreSQL lobby schema, pushes after commit and their failure while Centrifugo is down, reconnect/history recovery, leases, restart recovery, protocol authorization, and the Python SDK and CLI. It never queries game tables or calls game binding endpoints.

From the component root, supply a Go host application:

```sh
./scripts/test-acceptance.sh --server-root /path/to/host --server-package ./cmd/server
```

The runner requires Go, Python 3.14+, uv, and Linux Docker or Podman. It builds the supplied Go package, starts disposable PostgreSQL 18-alpine and Centrifugo 6.9.6 containers, and configures the host with these environment variables:

```text
DATABASE_URL
HTTP_ADDR
CENTRIFUGO_PUBLIC_URL
CENTRIFUGO_API_URL
CENTRIFUGO_API_KEY
CONNECTION_TOKEN_HMAC_SECRET
LOBBY_PROXY_SECRET
```

`--env-prefix` prepends a host-specific configuration namespace to these names. The host must expose `/healthz`, the lobby HTTP API and trusted Centrifugo proxy routes; initialize the lobby schema; push each committed change through the lobby's `realtime.Publisher`, whose failures it logs to standard output; and register `island-battles` and `quiz-demo` as example game keys. Tests read that log through `LOBBY_TEST_BACKEND_LOG`. `LOBBY_TEST_GAME_KEY` can override the first game key. These keys carry no game-specific behavior in this suite.

`--extra-tests /path/to/tests` adds host-owned pytest suites. They can reuse the fixtures and helpers in `integration.test_live`. In the Island Battles monorepo, the root `scripts/test-lobby-acceptance.sh` supplies the backend package, its environment prefix, and `integration/island_battles/` tests.

The runner uses temporary ports, generated credentials, and unique container names. No configured developer database is used. It restarts the supplied host process to verify persisted state and cleans containers and processes on success or failure. Failure logs remain under a printed `/tmp/lobby-acceptance-*` directory. `LOBBY_CONTAINER_RUNTIME` can select a Docker-compatible executable. Infrastructure failures fail the suite instead of skipping it.

Generator reproducibility is checked separately from the component root:

```sh
uv run --project clients/lobby-python/tools --locked python clients/lobby-python/scripts/generate.py --check
```
