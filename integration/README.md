# Live lobby acceptance

This suite belongs to the generic lobby component. It checks the lobby's own server, its public API, PostgreSQL lobby schema, pushes after commit and their failure while Centrifugo is down, reconnect/history recovery, leases, restart recovery, protocol authorization, and the Python SDK and CLI. It never queries game tables or calls game binding endpoints.

The runner runs the lobby as its own process, the local server [`cmd/lobby`](../cmd/lobby/main.go), and, when one is given, a game's host as a second process that the lobby reaches over HTTP. From the component root, the lobby alone with the [mock game](../mockgame/):

```sh
./scripts/test-acceptance.sh --mock-game
```

and with a game's host beside it:

```sh
./scripts/test-acceptance.sh --server-root /path/to/game --server-package ./cmd/server --env-prefix GAME_ \
  --lobby-games-command "go run ./cmd/lobby-games"
```

The runner requires Go, Python 3.14+, uv, and Linux Docker or Podman. It builds the lobby, the game's host and the mock game, starts disposable PostgreSQL 18-alpine and Centrifugo 6.9.6 containers, and configures the lobby with its own settings ([Running the lobby](../README.md#configuration)): `LOBBY_DATABASE_URL`, `LOBBY_HTTP_ADDR`, `LOBBY_PUBLIC_URL` (the lobby's address), `LOBBY_CENTRIFUGO_PUBLIC_URL`, `LOBBY_CENTRIFUGO_API_URL`, `LOBBY_CENTRIFUGO_API_KEY`, `LOBBY_CONNECTION_TOKEN_HMAC_SECRET`, `LOBBY_PROXY_SECRET`, and `LOBBY_PLAYER_TOKEN_KEYS` unless the environment already sets it. Before every start it runs the lobby's migrate command (`cmd/migrate`), as a deploy does, and Centrifugo sends its proxy calls to the lobby.

A game's host gets a database of its own, `game`, on the same PostgreSQL server, as in production: the lobby's tables stay in the lobby's database. It gets these variables, each with the `--env-prefix` in front, and the lobby's `LOBBY_DATABASE_URL` too:

```text
DATABASE_URL       the game's own database
HTTP_ADDR
CENTRIFUGO_API_URL
CENTRIFUGO_API_KEY
LOBBY_URL          the lobby's address, which is also the iss of its player tokens
```

It must expose `/healthz`, serve the hooks that its `LOBBY_GAMES` entry names, read the lobby's game API and verify player tokens with the lobby's JWKS at `LOBBY_URL`, and log to standard output. `--migrate-package` names a Go package that migrates the game host's schema and exits, which the runner runs with the same environment before every start. `--lobby-games-command` names a command, run in `--server-root`, that prints the lobby's `LOBBY_GAMES` with the game added, given the game host's address as its last argument and `LOBBY_GAMES` in its environment; the game's shared secret must be in both processes' environment. The two are separate origins, as the lobby and a game are in production: `LOBBY_TEST_API_URL` is the lobby's address, and `LOBBY_TEST_GAME_URL` the game host's, `http://localhost:<port>`, which the lobby's catalog accepts for a game's screens over http. Clients send the lobby's routes to the first and the game's to the second.

The lobby pushes each committed change through its `realtime.Publisher`, whose failures it logs to standard output. The lobby, the game's host and the mock game write one log, which tests read through `LOBBY_TEST_BACKEND_LOG`. `LOBBY_TEST_GAME_KEY` sets the game key that every test uses unless it names another, the CLI test included (`mock-game` without a game's host), and `LOBBY_TEST_SECOND_GAME_KEY` the second, `mock-game`. These keys carry no game-specific behavior in this suite, so a game whose lobbies need something of their own, such as a choice in the game's API before getting ready, sets `LOBBY_TEST_GAME_KEY` to a game without it.

`--mock-game` serves such a game: the runner builds [`cmd/mock-game`](../cmd/mock-game/main.go), the mock game over HTTP, starts it without its rules (`MOCK_GAME_RULES=off`: no settings, no member settings or seats, everyone may get ready), and adds it to the lobby's `LOBBY_GAMES` as `mock-game`, with a new secret in `LOBBY_GAME_SECRET_MOCK_GAME`. It reads the lobby's game API.

`--extra-tests /path/to/tests` adds game-owned pytest suites. They can reuse the fixtures and helpers in `integration.test_live`. In the Island Battles monorepo, the root `scripts/test-lobby-acceptance.sh` supplies the backend package, its migrate command, its environment prefix, `backend/cmd/lobby-games`, and `integration/island_battles/` tests.

The runner uses temporary ports, generated credentials, and unique container names. No configured developer database is used. It restarts the lobby and the game's host to verify persisted state (`SIGUSR1` to the runner restarts both, `SIGUSR2` stops both) and cleans containers and processes on success or failure. Failure logs remain under a printed `/tmp/lobby-acceptance-*` directory. `LOBBY_CONTAINER_RUNTIME` can select a Docker-compatible executable. Infrastructure failures fail the suite instead of skipping it.

Generator reproducibility is checked separately from the component root:

```sh
uv run --project clients/lobby-python/tools --locked python clients/lobby-python/scripts/generate.py --check
```
