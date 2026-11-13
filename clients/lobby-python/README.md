# Generic lobby Python client

This directory contains the generated HTTP SDK and the handwritten asyncio
wrapper, and both ship in the one `mpl-lobby-client` distribution.
`lobby_api_client/` is produced from the checked-in Huma OpenAPI document;
`lobby_client/` owns credentials, the Centrifugo connection, reconciliation,
heartbeats, and cleanup. Huma is the schema authority. Do not hand-edit files
under `lobby_api_client/`; update the schema/export and run the generator instead.

**One player per team is a breaking change; clients must upgrade.** A team holds one member, a person or a seat, so a lobby holds as many members as it has teams. The create request lost `capacity`, both the lobby's and each team's: `create_lobby` takes only `teams`, each `TeamDefinition` only a `name`, and the server refuses a request that still sends a `capacity` with 422. Snapshot teams lost `capacity` too; the lobby's `capacity` stays, read-only, as the number of teams. Selecting, or adding a seat to, a team that already has a member fails with `team_full`. What to do: regenerate or upgrade the client, drop `capacity=` from `create_lobby` and `TeamDefinition`, and treat a team as taken when an active member is on it.

**This is a breaking change; clients must upgrade.** Lobbies can hold seats that no person plays, such as a game's bot, which the host adds and removes with `add_seat` and `remove_seat`. Every snapshot member now has `kind` (`human` or `seat`) and `seat_settings`, and a seat is always ready, holds no lease and never hosts; the new error code `invalid_seat_settings` refuses settings the game doesn't take. An older client shows a seat as an ordinary member. What to do: regenerate or upgrade the client, and show a seat's `display_name` without treating it as a person.

The lobby API also gained the error code `game_requirement` and a `type` field on error bodies: a game can refuse `PUT …/ready` with `ready: true`, or the start, until a member has done something in the game's own API (Island Battles: choosing a bot). Regenerate or upgrade the client; the SDK's error then carries the game's problem type, and a client that doesn't know the code treats it as an unrecognized error. What to do: handle `game_requirement` by showing its `detail` to the player, who must act in the game's API and then retry.

People outside a lobby can watch it with its join code, without a seat: `watch(game_key=..., join_code=...)` returns the lobby ID and its watch channel (`member` is true for an active member, who gets no watcher row), and `unwatch(scope)` stops. Members list the watchers with `watchers(scope)`, and the host kicks one with `kick_watcher(scope, player_id)`. A watcher holds no lease, so the client follows nothing for them; subscribe to the watch channel with the Centrifugo SDK and use the game's watcher reads. Snapshots carry `watcher_count`, and the new error codes `watcher_kicked` (the host kicked that session) and `watchers_full` (the lobby has its maximum of watchers) refuse a watcher. What to do: regenerate or upgrade the client, which needs `watcher_count` in every snapshot.

The host can change a waiting lobby's `game_settings` and teams with `change_settings(scope, teams=[...], game_settings={...})`, which takes the same fields and rules as `create_lobby`. The teams are rebuilt by position: a kept team keeps its ID, new teams get new IDs, removed teams disappear (their person loses the team, their seat is removed), and every person's ready is cleared. So a lobby's `teams`, `capacity` and `game_settings` are no longer fixed while it waits. What to do: regenerate or upgrade the client, and read them from each snapshot instead of keeping the ones from the create or join.

**Playing again in the same lobby is a breaking change; clients must upgrade.** A lobby plays one game per round: every snapshot's lobby has `round` (from 1), and once the game has ended any active person calls `reopen(scope)`, which brings the started lobby back to `waiting` in its next round with the same members, teams, seats and settings, every person's ready cleared and an empty `start_roster`. Calling it again is safe. While the game runs it raises `LobbyError` with the new code `game_running` (409); for a closed lobby or a game that doesn't play again, `phase_conflict`. So `started` is no longer final, and the start roster can change between games. What to do: regenerate or upgrade the client, which needs `round` in every snapshot, and treat a lobby that goes from `started` back to `waiting` as the next round, not as an error.

**Member settings are a breaking change; clients must upgrade.** Each person sets their own settings for the lobby's game, a JSON object that only the game reads, with `set_member_settings(scope, {...})`, while the lobby waits. Every snapshot member now has `member_settings` (null until set, always null for a seat), and every member sees every member's. The game checks them, and the new error code `invalid_member_settings` (422) carries its message; a game that takes none refuses every value. New settings clear your ready, the same settings again change nothing, and they stay when the lobby is reopened. Island Battles' bot choice moved here: its member settings are `{"bot": {"kind": "localhost"}}` or `{"bot": {"kind": "default", "level": N}}`, and the game's own `PUT /game/lobbies/{lobby_id}/bot` and `GET /game/lobbies/{lobby_id}/bots` are gone. What to do: regenerate or upgrade the client, which needs `member_settings` in every snapshot, and set an Island Battles bot with `set_member_settings` instead of the game's endpoint.

## Installing from PyPI

Each release publishes this client to PyPI as `mpl-lobby-client`, at the release's version (the import name is `lobby_client`):

```sh
pip install mpl-lobby-client==X.Y.Z   # or: uv add mpl-lobby-client==X.Y.Z
```

It needs Python 3.14 or newer. The wheel contains both import packages: `lobby_client` and the generated HTTP SDK `lobby_api_client` (there is no separate SDK distribution). The rest of this README describes the repository checkout.

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
  --base-url http://localhost:8890 --session-file /tmp/lobby-alice.json \
  --new-session Alice --create Demo

uv run --project clients/lobby-python --locked python clients/lobby-python/examples/lobby_cli.py \
  --base-url http://localhost:8890 --session-file /tmp/lobby-bob.json \
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
immutable order as `team 1` and `team 2`. Each team holds one member, so the
lobby holds two; those labels carry no Island Battles
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
Island Battles refuses until the player has set a bot in their member settings
with `set_member_settings` (see the [backend README](../../../backend/README.md#choosing-a-bot));
a game without that rule, such as `--game-key mock-game` against the live suite's mock game, plays without it. An
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

The host can fill a team with a seat that the game plays, such as Island Battles' default bot:

```python
snapshot = await client.add_seat(scope, team_id, {"bot_level": 2})
seat = next(member for member in snapshot.members if member.kind.value == "seat")
snapshot = await client.remove_seat(scope, seat.id)
```

Only the host can, while the lobby waits; otherwise `LobbyError` with `not_host` or `phase_conflict`. Settings the game rejects raise `invalid_seat_settings` with the game's message as `detail`, and a game without seats, such as `quiz-demo`, refuses every one. `add_seat` sends a `request_id` (yours, or a fresh one), so after a transport failure you can call it again with the same `request_id`, team and settings: the lobby returns the snapshot without a second seat, and fails with `idempotency_conflict` if the team or settings differ. The client doesn't retry by itself.

To rejoin after `quit`, reuse the same session file without `--new-session`:

```sh
uv run --project clients/lobby-python --locked python clients/lobby-python/examples/lobby_cli.py \
  --base-url http://localhost:8890 --session-file /tmp/lobby-alice.json --join PRINTED_CODE
```

This process has a new client-instance UUID while retaining the guest identity.
