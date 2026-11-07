# Generic lobby Python client

This directory contains the generated HTTP SDK and the handwritten asyncio
wrapper. `generated/` is produced from the checked-in Huma OpenAPI document;
`lobby_client/` owns credentials, the Centrifugo connection, reconciliation,
heartbeats, and cleanup. Huma is the schema authority. Do not hand-edit files
under `generated/`; update the schema/export and run the generator instead.

**This is a breaking change; clients must upgrade.** The lobby API gained the error code `game_requirement` and a `type` field on error bodies: a game can refuse `PUT …/ready` with `ready: true`, or the start, until a member has done something in the game's own API (Island Battles: choosing a bot). Regenerate or upgrade the client; the SDK's error then carries the game's problem type, and a client that doesn't know the code treats it as an unrecognized error. What to do: handle `game_requirement` by showing its `detail` to the player, who must act in the game's API and then retry.

## Installing from PyPI

Each release publishes this client to PyPI as `mpl-lobby-client`, at the release's version (the import name is `lobby_client`):

```sh
pip install mpl-lobby-client==X.Y.Z   # or: uv add mpl-lobby-client==X.Y.Z
```

It needs Python 3.14 or newer. The HTTP SDK it depends on, `mpl-lobby-api-client`, installs with it. The rest of this README describes the repository checkout.

The client requires Python 3.14 or newer and [uv](https://docs.astral.sh/uv/).
A running host application exposing the lobby API, PostgreSQL, and Centrifugo are required for the live example. The host configures its database and transport credentials; clients need only the public API URL. See the [component guide](../../README.md) and [live acceptance setup](../../integration/README.md).

From the lobby component root (`lobby/` in the monorepo):

```sh
UV_CACHE_DIR=/tmp/island-lobby-uv-cache uv sync --project clients/lobby-python --locked
UV_CACHE_DIR=/tmp/island-lobby-uv-cache uv run --project clients/lobby-python/tools --locked \
  python clients/lobby-python/scripts/generate.py --check
```
The terminal example requires a POSIX terminal because it uses
`asyncio`'s `loop.add_reader` for cancellable stdin. Start one lobby in the
first terminal, then use the join code printed there in a second terminal:

```sh
uv run --project clients/lobby-python --locked python clients/lobby-python/examples/lobby_cli.py \
  --base-url http://localhost:8888 --session-file /tmp/lobby-alice.json \
  --new-session Alice --create Demo

uv run --project clients/lobby-python --locked python clients/lobby-python/examples/lobby_cli.py \
  --base-url http://localhost:8888 --session-file /tmp/lobby-bob.json \
  --new-session Bob --join PRINTED_CODE
```

The first invocation creates an identity and retains only `player_id`,
`access_token`, and `expires_at` in the owner-only session file. The file is
never overwritten: use a new path for a new identity. A join must use the same
`--game-key` and printed join code; the client never guesses a lobby by name.
Without `--new-session`, the existing file is loaded explicitly. A missing,
unreadable, or malformed file is an error, and an expired credential requires
an explicitly created new session. There is no automatic identity recovery
after the 24-hour guest credential expires.

The lobby starts with two generic teams named Red and Blue, displayed in
immutable order as `team 1` and `team 2`; those labels carry no Island Battles
color semantics. `team 1` or `team 2` selects that team's UUID;
`team none` clears it. `ready` and `unready` send `True` and `False` exactly.
The host can use `start` after at least two members are present and every
member has selected a team and is ready with a live lease. Starting freezes the roster; it
does not invoke Lambda, generate an Island Battles map, or run a simulation.

The other commands are `show`, `leave`, `quit`, and Ctrl-C/EOF. `quit`, EOF,
and signals close this client and release its lease. `leave` explicitly leaves
the guest identity across all of its running clients before closing. Closing a
client releases its own lease early. Leases last 45 seconds with a 60-second
grace period. Heartbeats renew while connected; a forced network interruption
is handled by the official Centrifugo SDK reconnect, while membership survives.
Heartbeat pauses while disconnected, and rejoining is explicit after
membership expires. Running one credential in two clients creates two leases
but consumes one lobby seat.

Expected conflicts such as `lobby_full`, `team_full`, `not_ready`,
`empty_teams`, `not_host`, and `phase_conflict` are printed as typed command failures.
A game can also refuse `ready`, or `start`, until a player does something in the
game's own API: the failure is `game_requirement`, printed with the game's message.
Island Battles refuses until the player has chosen a bot with
`PUT /game/lobbies/{lobby_id}/bot` (see the [backend README](../../../backend/README.md#choosing-a-bot));
`--game-key quiz-demo` plays a game without that rule. An
expired credential or lost membership explains that an explicit new session or
join is required. Create uses one UUID request id for the process lifetime;
the CLI never retries it with a new id or mints another guest after a network
failure.

If the API is unreachable, verify the base URL includes the backend's lobby
API address and that the PostgreSQL/backend/Centrifugo services are running.
The [live acceptance suite](../../integration/README.md) uses
PostgreSQL 18, Centrifugo 6.9.6 and a supplied host application, including two CLI
processes, transport interruption, history loss and snapshot reconciliation.
See [verification evidence](../../docs/tasks/generic-lobby/implementation-status.md).

The wrapper can also be used directly:

```python
from lobby_client import LobbyClient, Scope

async with LobbyClient(base_url, credential=access_token) as client:
    snapshot = await client.join_lobby(game_key="island-battles", join_code=code)
    scope = Scope.from_snapshot(snapshot)
    async for update in client.updates(scope):
        print(update.lobby.version)
```

A game's own refusal of readiness or of the start raises `LobbyError` with code
`game_requirement`, status 409, the game's message as `detail` and the game's
problem type as `problem_type`, such as
`urn:island-battles:problem:bot-not-chosen`.

`create_lobby` takes an optional `game_settings` dict, the game's own JSON object.
The game checks it when the lobby is created; rejected settings raise
`LobbyError` with code `invalid_game_settings`, and its `detail` holds the
game's message, which may name the invalid field. Every snapshot returns the
settings as `snapshot.lobby.game_settings`, and they can't change afterwards.

To rejoin after `quit`, reuse the same session file without `--new-session`:

```sh
uv run --project clients/lobby-python --locked python clients/lobby-python/examples/lobby_cli.py \
  --base-url http://localhost:8888 --session-file /tmp/lobby-alice.json --join PRINTED_CODE
```

This process has a new client-instance UUID while retaining the guest identity.
