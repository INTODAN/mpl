# mpl-lobby-client (TypeScript)

The TypeScript client for the generic lobby's player API, published to npm as `mpl-lobby-client`. It is the TypeScript counterpart of the [Python client](../lobby-python/README.md), with the same behavior:

- a guest session, or a credential from an earlier one;
- creating and joining lobbies, and every player command: teams, ready, member settings, seats, settings, start, reopen, leave;
- following each joined lobby: it subscribes to the lobby's Centrifugo channel with the [official SDK](https://github.com/centrifugal/centrifuge-js), reads a new snapshot over HTTP when the lobby changes, heartbeats its lease every 15 seconds while connected, and reconciles while disconnected;
- watching a lobby by its join code, listing and kicking watchers;
- player tokens for a game's own API;
- typed errors, and `close()`, which releases this client's leases.

The types of the API (`Snapshot`, `Member`, `paths` and the rest) are generated from [`api/openapi.json`](../../api/openapi.json) into [`src/generated/`](src/generated/); never edit them by hand.

## Installing

```sh
npm install mpl-lobby-client@X.Y.Z   # or: bun add mpl-lobby-client@X.Y.Z
```

Each release publishes it at the release's version, the same as the Go module's tag and the Python client's version. It is an ES module for browsers, Node 20.3 or newer, Bun and Deno, and needs `fetch`, `WebSocket`, `crypto.randomUUID` and `AbortSignal.any`. In Node older than 22, which has no global `WebSocket`, pass one to the Centrifugo SDK through the `realtime` option.

## Using it

```ts
import { LobbyClient, LobbyError, scopeOf } from "mpl-lobby-client";

const client = new LobbyClient("https://lobby.example.org");
await client.createSession("Ada"); // keep client.credential to resume this player later

const joined = await client.joinLobby({ gameKey: "quiz-demo", joinCode: "ABCDEFGH" });
const scope = scopeOf(joined);

await client.setReady(scope, true);

try {
	for await (const snapshot of client.updates(scope)) {
		console.log(snapshot.lobby.phase, snapshot.members?.length);
		if (snapshot.lobby.phase === "started") break;
	}
} catch (error) {
	if (error instanceof LobbyError) console.error(error.code);
}

await client.close();
```

A host creates a lobby with `createLobby({ gameKey, name, teams, requestId, gameSettings })`. Each team holds one member, and `requestId` (a UUID) makes a retry safe. `gameSettings`, `seatSettings` and `memberSettings` are the game's own JSON objects; the lobby stores them and the game checks them.

`updates(scope)` yields the current snapshot and then each newer one; a slow reader skips to the newest. It throws the error that stopped the client from following the lobby.

To call a game's API, get a player token with `issuePlayerToken(gameKey)` and send it as `Authorization: Bearer <token>`.

## Errors

Every error is a `LobbyError`, with the API's `code` and the HTTP `status`:

| Class | When |
|---|---|
| `AuthenticationError` | The credential is missing or refused (401). The client stops following its lobbies. |
| `MembershipError` | The membership is gone or stale (404, `stale_membership`, `left_membership`, `not_joined`). Join again to follow the lobby. |
| `TransientError` | A network failure, a 5xx or a 429. The call can be tried again. |
| `CompatibilityError` | The server answered with something this client doesn't understand. Upgrade the client. |
| `ClientClosedError` | The client was closed. |

`detail` holds the game's own message only for `invalid_game_settings`, `invalid_seat_settings`, `invalid_member_settings` and `game_requirement`, and `problemType` the game's problem type for `game_requirement`. Other errors carry no server text.

## Development

Run these from this directory. They need [Bun](https://bun.sh); `bun install --frozen-lockfile` installs the tools, [Biome](https://biomejs.dev) for formatting and linting, and TypeScript.

*   **Format and lint:** `bun run format` fixes; `bunx biome ci .` checks
*   **Type check:** `bun run typecheck`
*   **Tests:** `bun test`
*   **Regenerate the API types** after the API documents change: `bun run generate`; `bun run generate --check` checks they are current
*   **Build** into `dist/`: `bun run build` (Bun bundles the module, `tsc` writes the declarations)
*   **See what would be published:** `npm pack --dry-run`

`bun run check` runs Biome, the type check and the generation check together. CI runs these on every pull request (the **TypeScript** job in [`.github/workflows/checks.yml`](../../.github/workflows/checks.yml)).

[`examples/lobby.ts`](examples/lobby.ts) creates or joins a lobby against a running lobby, like the Go example:

```sh
bun examples/lobby.ts --name Ada
bun examples/lobby.ts --name Grace --join-code ABCDEFGH
```

## Releasing

The [Release workflow](../../.github/workflows/release.yml) publishes this package to npm at the release's version, with npm's trusted publishing and provenance, after the tag and the GitHub release exist. The version in `package.json` stays `0.0.0-dev`; the workflow sets the real one in its working copy only. See the root [README](../../README.md#releases).
