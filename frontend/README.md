# Temporary lobby frontend

A plain HTML/CSS/JavaScript playground for two players on the generic lobby API. It names no game of its own: the host application lists its games when it mounts the frontend, and the page builds its create form from that list. No frontend build step or CDN access is needed. Assets are embedded in Go; restart/rebuild the host after changing them.

In this repository, `./scripts/dev.sh` runs the game backend, which serves the frontend at **http://localhost:8888/lobby/ui/** (or your configured backend address). The script supports Docker Compose or native Podman on Linux; see the [backend setup](../../backend/README.md#run-locally) and its [browser demo](../../backend/README.md#browser-demo).

## Mount it with a game catalog

Mount `frontend.Handler(games)` with its prefix stripped, on the same origin as the `/lobby/v1` API, so guest credentials never need cross-origin configuration. Register the same game keys in the host's lobby policy.

```go
lobbyFrontend, err := frontend.Handler([]frontend.Game{{
	Key:       "quiz-demo",
	Name:      "Quiz",
	ResultURL: "/quiz/{lobby_id}/result",
	// Optional: lets the result document read /quiz/{lobby_id}/data/... through the page.
	DataURLPrefix: "/quiz/{lobby_id}/data/",
}})
if err != nil {
	return err
}
router.Handle("/lobby/ui/*", http.StripPrefix("/lobby/ui/", lobbyFrontend))
```

The handler serves the catalog as JSON at `games.json`, and `app.js` loads it at startup. Each `frontend.Game` has these fields:

| Field | Meaning |
|---|---|
| `Key` | The lobby game key. |
| `Name` | Shown in the create form and above the room. |
| `ResultURL` | Same-origin path of the game's result document once the lobby starts. `{lobby_id}` is replaced with the lobby ID. |
| `StartedHint` | Optional. Tells the players of a started lobby how they take part, for example which client submits their turns. |
| `ExamplePlayer` | Optional. A browser player `Module`, with a short `Strategy` name and a one-sentence `Summary`. It lets the page follow the game live and enables **New demo lobby** for the game. |
| `SettingsModule` | Optional. Same-origin URL of an ES module that adds the game's own controls to the create form and chooses the new lobby's `game_settings` and teams. |
| `DataURLPrefix` | Optional. Same-origin path prefix under which the result document may read the game's data through the page, with `game.request`. It contains `{lobby_id}` and ends with `/`, for example `/quiz/{lobby_id}/data/`. |

`Handler` rejects an empty catalog, invalid or duplicate keys, missing names, and module, result or data URLs that leave the frontend's origin. It also rejects a data URL prefix without `{lobby_id}`, without a final `/`, or with `.` or `..` segments, `%`, `?` or `#`. A second game needs only another entry; nothing in `lobby/` changes.

## Play

To set up players manually:

1. Enter your name, choose a game, and create a lobby.
2. Open the same page in another tab, browser, or device. Enter another name and join using the eight-character code.
3. Each player chooses a different team and marks ready. The host can then start the lobby.

New lobbies have one seat per team. Without a settings module there are two teams, Blue and Red; a game's settings module can choose others. Starting freezes the roster and the host backend launches the selected game. On another device, use the host's reachable address instead of `localhost`.

**New demo lobby** appears when the selected game has an example player. It creates Alex on Blue in the current tab, opens Sam on Red in a second tab, marks both ready, and starts automatically. Both tabs run the game's example player. If the browser blocks the second tab, use **Open second player tab**. Each demo creates fresh player sessions; using it from an existing lobby leaves that lobby first. **Pause this player** stops that tab from submitting its next turn, and **Resume this player** continues. Only demo invitations let the example player submit turns; in other lobbies it only follows the game's state.

Guest credentials and the current room are saved in `sessionStorage`, so independently opened tabs can act as separate players and a reload restores the current player. A browser's **Duplicate tab** action may copy that identity; open a fresh tab using the URL to test a second player. **Leave lobby** removes the player immediately. Closing a tab relies on the server's lease expiry and grace period.

## What a game provides

A game's result URL returns its HTML document, fetched with the guest bearer credential and displayed in an iframe with `sandbox="allow-scripts"`. The document has an opaque origin and no credential, so it can't make an authorized request itself. Simulation and rendering remain owned by the game backend. The page and the frame exchange these messages:

- The page forwards every state its example player reports to the frame as `{type: "lobby.game-state", game_key, state}`. The frame should accept it only from `window.parent` and only for its own `game_key`.
- The game can report its intrinsic content height with `{type: "game.result-size", height}` to fit summaries and responsive layouts without an inner scrollbar. The lobby accepts sizing messages only from its own result iframe.
- The game can ask the page to fetch its data with `{type: "game.request", id, path}`, when its catalog entry has a `DataURLPrefix`. The page answers `{type: "lobby.response", id, status, body}`.
- `lobby.response` echoes the request's `id`. `status` is the HTTP status, and `body` is the parsed value when the response is JSON (`application/json` or `application/problem+json`) and its text otherwise. The frame should accept it only from `window.parent`, and match it to its request by `id`.

The page relays a `game.request`, in [`relay.mjs`](relay.mjs), only when all of these hold:

- It comes from the page's own result iframe, while the frame shows the room's current lobby. The page answers only that frame, and drops the answer when the room changes before the fetch ends.
- `id` is a string of at most 200 characters or a finite number. A message without one is ignored, because it can't be answered.
- `method` is absent or `"GET"`. The page always fetches with GET.
- `path` is a same-origin absolute path of at most 2048 characters that starts with the game's `DataURLPrefix`, with `{lobby_id}` replaced by the room's lobby ID. It must already be in the form the browser would request: no `.` or `..` segments, no percent-encoding before the query, and no scheme, host, backslash, space, control character or fragment. The query is passed on unchanged.

The page fetches the path with GET, the guest bearer credential and `Accept: application/json`, refuses redirects, and gives up after 8 seconds. The credential never reaches the frame: the answer has only the status and the body. At most 8 requests run at once. A refused, failed or timed-out request is answered with `status: 0`, `body: null`, and an `error` that says why, so the frame never waits for an answer that won't come. For example:

```js
// In the result document.
const pending = new Map();
function request(path) {
  const id = crypto.randomUUID();
  window.parent.postMessage({ type: "game.request", id, path }, "*");
  return new Promise((resolve) => pending.set(id, resolve));
}
window.addEventListener("message", (event) => {
  if (event.source !== window.parent || event.data?.type !== "lobby.response") return;
  pending.get(event.data.id)?.(event.data);
  pending.delete(event.data.id);
});
const { status, body } = await request(`/quiz/${lobbyID}/data/scores?round=2`);
```

Run the relay's path checks from the repository root with `node lobby/frontend/relay.test.mjs`.

An example player module is an ES module that exports an `ExamplePlayer` class. The page constructs it with `{lobbyID, playerID, credential, subscription, enabled, pending, savePending, onState, onStatus}`:

- `credential` is the guest bearer credential, and `subscription` is an unsubscribed Centrifugo subscription to the player's `player:<player_id>` channel.
- `enabled` says whether the player may submit turns. The page reads `player.enabled` and calls `player.setEnabled(enabled)` for **Pause this player**.
- `pending` and `savePending(pending)` keep an unfinished request in `sessionStorage` across reloads.
- `onState(state)` passes game state on to the result frame, and `onStatus(title, detail)` updates the example player panel.
- The page calls `start()` once the lobby starts and `stop()` when the player leaves the room.

A settings module is an ES module that exports a `SettingsForm` class and, optionally, a `describeSettings` function:

- The page constructs `new SettingsForm({container, onChange})` when the game is selected. The form adds its controls to `container`, an element inside the create form.
- When the host creates a lobby, the page calls `form.choice()`. It returns `{settings, teams}`: `settings` is the `game_settings` object, sent only when it has keys, and `teams` lists the team names in order, or is null for the default teams. The lobby gets one seat per team. To stop the create request, `choice()` throws an `Error` whose message the page shows.
- `describeSettings(game_settings)` returns a short line, or a promise of one, that the room shows under the lobby name for every member, such as the chosen map's name.
- A form may open another page and listen for its `message` events, as a game's form may do for a page it opens. It must accept only messages from its own origin and from the window it opened.

The game checks the settings when the lobby is created. A rejection shows the game's message, which can name the invalid field. **New demo lobby** uses the form's choice too, and needs it to have exactly two teams.

## Realtime

The frontend uses the locally vendored official Centrifugo JavaScript SDK. It subscribes to lobby invalidations, fetches a snapshot after subscription/reconnect, and fetches lobby state when its version changes. There are no recurring snapshot GETs. The only periodic HTTP request is the membership lease heartbeat every fifteen seconds; command and connection failures can retry. Allow the frontend's origin in Centrifugo (`localhost:8888` is included in `.env.example`).
