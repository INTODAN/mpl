# Temporary lobby frontend

A plain HTML/CSS/JavaScript playground for two players on the generic lobby API. It names no game of its own: the host application lists its games when it mounts the frontend, and the page builds its create form from that list. No frontend build step or CDN access is needed. Assets are embedded in Go; restart/rebuild the host after changing them.

The lobby serves it at `/lobby/ui/`: its own server, [`cmd/lobby`](../README.md#running-the-lobby), or a game's host that runs the lobby in its own process. In this repository, `./scripts/dev.sh` runs the lobby on :8890 and the game backend, which serves Island Battles' modules under `/game/demo/`, on :8888; open the page on one origin through the 3D client's dev server (`bun run dev` in `clients/island-battles-web`), at **http://localhost:3000/lobby/ui/**. The script supports Docker Compose or native Podman on Linux; see the [backend setup](../../backend/README.md#run-locally) and its [browser demo](../../backend/README.md#browser-demo).

## Mount it with a game catalog

Mount `frontend.Handler(games)` with its prefix stripped, on the same origin as the `/lobby/v1` API, so guest credentials never need cross-origin configuration. Register the same game keys in the host's lobby policy.

```go
lobbyFrontend, err := frontend.Handler([]frontend.Game{{
	Key:       "quiz-demo",
	Name:      "Quiz",
	ResultURL: "/quiz/{lobby_id}/result",
	// Optional: lets the result document read /quiz/{lobby_id}/data/... through the page.
	DataURLPrefix: "/quiz/{lobby_id}/data/",
	// Optional: pages of the game that the lobby page links to.
	Links: []frontend.Link{{Label: "Past rounds", URL: "/quiz/rounds/"}},
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
| `ResultURL` | Same-origin path of the game's result document once the lobby starts. `{lobby_id}` is replaced with the lobby ID, and `{round}`, where it appears, with the lobby's round: a lobby plays one game per round. |
| `StartedHint` | Optional. Tells the players of a started lobby how they take part, for example which client submits their turns. |
| `ExamplePlayer` | Optional. A browser player `Module`, with a short `Strategy` name and a one-sentence `Summary`. It lets the page follow the game live and enables **New demo lobby** for the game. |
| `SettingsModule` | Optional. Same-origin URL of an ES module that adds the game's own controls to the create form and chooses the new lobby's `game_settings` and teams. |
| `DataURLPrefix` | Optional. Same-origin path prefix under which the result document may read the game's data through the page, with `game.request`. It contains `{lobby_id}` and ends with `/`, for example `/quiz/{lobby_id}/data/`. |
| `WatchResultURL` | Optional. Lets people outside the roster watch a lobby of this game: the same-origin path of the document a watcher sees, with `{lobby_id}` as in `ResultURL` (and no round, since a watcher can't read the lobby). It answers 404 while the game hasn't started and 403 to a watcher the host removed. Without it the page offers no watching for the game. See [Watching](#watching-a-lobby). |
| `WatchDataURLPrefix` | Optional, with `WatchResultURL`. `DataURLPrefix` for a watcher's document: the only paths it may read through `game.request`. The page never falls back to `DataURLPrefix` for a watcher, so a game that names none lets its watchers read nothing. Island Battles: `/game/from-lobby/{lobby_id}/watch/`. |
| `RoomModule` | Optional. Same-origin URL of an ES module that shows the game's own controls to the player in a waiting room, such as a choice the game requires before the player gets ready. See [Room module](#room-module). |
| `RoomAPIPrefix` | Optional, with `RoomModule`. Same-origin path prefix under which the room module may call the game's API with the player's token for the game, with `GET`, `PUT`, `POST` or `DELETE`. Like `DataURLPrefix`, it contains `{lobby_id}` and ends with `/`, for example `/quiz/{lobby_id}/players/`. |
| `Links` | Optional. The game's own pages that the lobby page links to, each a `Link` with a `Label` and a same-origin `URL`, such as a page that still works after a lobby is gone. The page lists the selected game's links below the create and join forms, and the room's game's links under the room's name. Each opens in a new tab, so the player keeps their lobby. |

`Handler` rejects an empty catalog, invalid or duplicate keys, missing names, links without a label, and module, result, data or link URLs that leave the frontend's origin. It also rejects a watch result URL without `{lobby_id}`, a watch data prefix without a watch result URL, and a data URL, watch data prefix or room API prefix without `{lobby_id}`, without a final `/`, or with `.` or `..` segments, `%`, `?` or `#`, and a room API prefix without a room module. A second game needs only another entry; nothing in `lobby/` changes.

## Room module

A game whose players must choose something before they get ready, such as what plays for them, shows its controls in the room with a room module. While the lobby waits, the page imports the module once and shows an element above the team select for it; once the lobby starts, the element is hidden. The module exports:

- `class RoomPanel`, constructed with `{container, lobbyID, playerID, saveMemberSettings, request, refreshSeatOptions}`. It renders into `container`. `saveMemberSettings(settings)` saves the player's own [member settings](../README.md#member-settings) for the game with the lobby's `PUT …/member-settings`, shows the snapshot it answers with, and resolves to that snapshot; it rejects with an `Error` that carries the lobby's `detail` (such as the game's message for `invalid_member_settings`), `code` and `status`. Every snapshot carries every member's `member_settings`, so the panel reads the choices from `update(snapshot)`. `request(method, path, body)` calls the game's API through the page, which adds the player's bearer credential: only `GET`, `PUT`, `POST` or `DELETE` (a `GET` or `DELETE` without a body), only for a same-origin path under the catalog's `RoomAPIPrefix` with the room's lobby ID, checked like a [relayed](#what-a-game-provides) `game.request` path (a guard that keeps an honest, host-configured module from mistakes such as a wrong method or path, not a sandbox or a security boundary against a hostile module), and only while the page shows that lobby. It resolves to `{status, body}`, with the parsed body of a JSON or problem response and `null` otherwise, and rejects with an `Error` that says why when the page refuses the request or gets no answer within 8 seconds. [`roomapi.mjs`](roomapi.mjs) holds the checks.
- The room module reaches only the game's room API through `request`. A game's page code that must call other endpoints, such as an example player reading the turn deliveries it passes to a player's bot, does so itself through the `fetcher` that the page passes to the example player, which sends the player's token for the game.
- An optional `update(snapshot)` method, which the page calls with every snapshot it shows, so the panel can read the game's data again when the lobby changes, for example when a member joins or gets ready.
- An optional `seatOptions({lobbyID, request})` function, for a game that has [seats that no person plays](../README.md#seats-that-no-person-plays), such as its bot. `request` is the room's `request`, as the panel gets it. It returns, or resolves to, `{label, choices}`: `label` names the action, such as `"Add bot"`, and each choice has a `label` for the select and the `settings` that the page sends as `seat_settings`, such as `{label: "Default bot, level 2", settings: {bot_level: 2}}`. A choice may also have an `input`, `{name, label, placeholder}`: while it is selected, the page shows a text field with that label next to the select, and sends its trimmed text as `settings[name]`, refusing to add the seat while it is empty. A choice with `disabled: true` is shown greyed out and can't be chosen, such as a bot that the game can't start with for now. The page calls `seatOptions` when it shows the room, and again whenever the panel calls `refreshSeatOptions()`, such as when the choices it would return have changed; it keeps each team's selected choice, by its label, and its text. The host of a waiting room then gets one select and one button per team, which adds a seat to that team with `POST …/seats` (a fresh `request_id` per click), and a **Remove** button on each seat, which calls `DELETE …/seats/{seat_id}`. The page shows a seat like a member, with **Played by the game** in place of its readiness. Without `seatOptions`, or for anyone but the host, the room shows no seat controls.
- An optional `prepareDemo({lobbyID, saveMemberSettings, request})` function, which **New demo lobby** awaits for each demo player after choosing their team and before marking them ready, so the game can make the choices it requires, ones that suit its example player.

Island Battles' room module, the bot choice, saves the player's bot with `saveMemberSettings`, and uploads, lists and deletes bot packages through `request` under its `RoomAPIPrefix`, `/game/lobbies/{lobby_id}/`. The zip itself goes from the module straight to storage with a presigned POST, which needs no credential. The lobby page knows nothing of what the module shows. When the game refuses readiness until its choice is made, the lobby answers the ready request with `game_requirement`, and the page shows the game's message.

## Play

To set up players manually:

1. Enter your name, choose a game, and create a lobby.
2. Open the same page in another tab, browser, or device. Enter another name and join using the eight-character code.
3. Each player chooses a different team, makes any choice that the game's room module shows, and marks ready. The host can then start the lobby.

**Copy invite link**, next to **Copy code** in the room, copies a link such as `/lobby/ui/?join=ABCD2345&game=quiz`. Whoever opens it gets a banner, enters a name and presses **Join lobby**; with a name already saved in the tab, the link joins at once. The page removes `join` and `game` from the address bar, so a reload doesn't join again. A link with a code that can't be one (not eight letters and digits) says so, and a wrong or expired code gives the same message as the join form (no lobby found for that code and game). `game` is optional and names the catalog key; without it the selected game is used. [`links.mjs`](links.mjs) reads and builds the links; run its tests with `node lobby/frontend/links.test.mjs`.

To play alone in a game whose room module offers seats, create a lobby, choose your team, add a seat to each other team with the controls under the players, get ready and start.

New lobbies have one seat per team. Without a settings module there are two teams, Blue and Red; a game's settings module can choose others. Starting freezes the roster and the host backend launches the selected game. On another device, use the host's reachable address instead of `localhost`.

**New demo lobby** appears when the selected game has an example player. It creates Alex on Blue in the current tab, opens Sam on Red in a second tab, lets the game's room module prepare each player, marks both ready, and starts automatically. Both tabs run the game's example player. If the browser blocks the second tab, use **Open second player tab**. Each demo creates fresh player sessions; using it from an existing lobby leaves that lobby first. **Pause this player** stops that tab from submitting its next turn, and **Resume this player** continues. In other lobbies the example player is constructed with `demo: false`: it follows the game's state, asks the server to apply a turn's deadline when others are slow, and plays only for a player whose own bot the game leaves to the page. Island Battles' example player delivers that player's turns to the bot on their machine (a **localhost bot** chosen in the room module), which submits them itself, and shows the bot's status, or why it refused a turn, in the example player panel, under **Your bot**. For a bot that the server plays without the page, a hosted or an uploaded one, the panel says **Played by the server**.

### Watching a lobby

For a game whose entry has a `WatchResultURL`, the start screen has **Watch a lobby**, and a link `?watch=ABCD2345` (with an optional `&game=KEY`) watches after the name, like an invite link joins. Watching takes no seat (`POST /lobby/v1/watches`), and the lobby shares its members with its members only, so a watcher sees no room: a **Watching a lobby** panel with a **Stop watching** button (`DELETE …/watch`), no team, ready or bot controls, and, once the game has started, the game's watcher document in the result frame. Until then the page asks for the document again with backoff of 1, 2 and 4 seconds, then every 5 seconds. The document is fetched with the guest credential from `WatchResultURL`, and its `game.request` messages may read only `WatchDataURLPrefix` ([`watch.mjs`](watch.mjs) holds this rule and its tests: `node lobby/frontend/watch.test.mjs`).

The page subscribes to the watch channel that `POST /lobby/v1/watches` returns, as the 3D client does, and passes every publication to the document as `{type: "lobby.watch-event", game_key, event}`; publications that arrive before the document is loaded wait for it. After a reconnect that couldn't recover every publication, the page loads the document again. The document tells the page to load it anew with `{type: "game.reload"}`, which a watcher's document sends when it sees a game of a later lobby round, since a watcher can't read the lobby's round. A reload of the tab watches again with the saved code.

The room of every member has a **Watchers** list (`GET …/watchers`, read again when the lobby changes and someone watches), and the host has a **Remove** button on each (`DELETE …/watchers/{player_id}`). A removed watcher loses the watch channel at once; the page asks the lobby once more, learns that the host removed them (`watcher_kicked`), and returns to the start screen saying so. Like the 3D client, the page shows no bot logs to a watcher. Starting to watch, leaving and removing are never retried after a 429; reading the list is.

Guest credentials and the current room are saved in `sessionStorage`, so independently opened tabs can act as separate players and a reload restores the current player. A browser's **Duplicate tab** action may copy that identity; open a fresh tab using the URL to test a second player. **Leave lobby** removes the player immediately. Closing a tab relies on the server's lease expiry and grace period.

## What a game provides

The page calls a game's API with a [player token](../README.md#player-tokens) for that game, never with the guest credential: [`playertoken.mjs`](playertoken.mjs) gets one from `POST /lobby/v1/player-tokens` with the room's or watched lobby's `game_key`, sends it as `Authorization: Bearer <token>`, gets a new one once 80 % of its life has passed, and sends a request that the game answers with 401 once more with a new token. This applies to the result and watch documents, the relayed reads, the room module's requests and the example player's.

A game's result URL returns its HTML document, fetched with the player's token and displayed in an iframe with `sandbox="allow-scripts allow-downloads"`. The document has an opaque origin and no credential, so it can't make an authorized request itself. `allow-downloads` lets it save a file that the player asks for, such as a download it read with `game.request` and saves through a `Blob` URL and an `<a download>` link; the browser shows its download as for any page. The sandbox still blocks everything else: same-origin access, forms, pop-ups and top-level navigation. Simulation and rendering remain owned by the game backend. The page and the frame exchange these messages:

- The page talks only to the document it loaded into the frame. The sandbox still lets the document navigate or reload the frame, so the page counts the frame's loads: after the second load since it set the document, it ignores the frame's messages and sends it nothing, neither states nor answers, until it loads the next result document. A result document should not navigate itself; links that leave it should open elsewhere or not at all.

- The page forwards every state its example player reports to the frame as `{type: "lobby.game-state", game_key, state}`. The frame should accept it only from `window.parent` and only for its own `game_key`.
- The game can report its intrinsic content height with `{type: "game.result-size", height}` to fit summaries and responsive layouts without an inner scrollbar. The lobby accepts sizing messages only from its own result iframe.
- The game can ask the page to fetch its data with `{type: "game.request", id, path}`, when its catalog entry has a `DataURLPrefix`. The page answers `{type: "lobby.response", id, status, body}`. An optional `timeout_seconds` asks the page to wait longer than its default for a large or slow response.
- `lobby.response` echoes the request's `id`. `status` is the HTTP status, and `body` is the parsed value when the response is JSON (`application/json` or `application/problem+json`) and its text otherwise. The frame should accept it only from `window.parent`, and match it to its request by `id`.

The page relays a `game.request`, in [`relay.mjs`](relay.mjs), only when all of these hold:

- It comes from the page's own result iframe, while the frame shows the document the page loaded for the room's current lobby. The page answers only that document, and drops the answer when the room or the document changes before the fetch ends.
- `id` is a string of at most 200 characters or a finite number. A message without one is ignored, because it can't be answered.
- `method` is absent or `"GET"`. The page always fetches with GET.
- `timeout_seconds` is absent or a number of seconds, more than 0 and at most 120. `null` is refused like any other value that isn't such a number.
- `path` is a same-origin absolute path of at most 2048 characters that starts with the game's `DataURLPrefix`, with `{lobby_id}` replaced by the room's lobby ID. It must already be in the form the browser would request: no `.` or `..` segments, no percent-encoding before the query, and no scheme, host, backslash, space, control character or fragment. The query is passed on unchanged.

The page fetches the path with GET, the player's token for the game and `Accept: application/json`, and refuses redirects. It gives up when the whole response, headers and body, hasn't arrived within the request's `timeout_seconds`, or 8 seconds without one. Small reads should keep the default, so a stalled one frees its place among the 8 soon. A large download asks for more: a body of tens of megabytes can take longer than 8 seconds to arrive, for example from a host that streams it at a few megabytes a second. The page allows at most 120 seconds, so a request can't hold its place for long. It reads at most 64 MiB of a response body, enough for a game's large downloads such as a saved demo. A host may pack a large body with a `Content-Encoding` such as gzip, which the browser unpacks as it reads; the 64 MiB counts the unpacked body, which is what the frame receives. The token never reaches the frame: the answer has only the status and the body. At most 8 requests of one result document run at once; a new document starts with none. The page answers every `game.request` with a usable `id` that the loaded document sends. One it refuses or can't relay, such as one sent while the page has no lobby or before it could load `relay.mjs`, and one that fails, times out or has a larger body, is answered with `status: 0`, `body: null`, and an `error` that says why, so the frame never waits for an answer that won't come. For example:

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

When the server throttles a request with 429, the page sends it again up to 3 times, after about 0.5, 1 and 2 seconds (each with up to 25% random jitter, at least the `Retry-After` header, at most 5 seconds). It retries every GET, a room module's PUT and DELETE (never its POST), and the lobby's team, ready, settings, member settings and heartbeat requests; it never retries creating a lobby, joining, starting, adding or removing a seat, or removing a watcher. A request that stays throttled shows "The server is busy. Try again in a moment."; a relayed or room request then answers with `status: 429` and a body `{status, detail}` carrying that message. [`throttle.mjs`](throttle.mjs) holds the retry; run its tests with `node lobby/frontend/throttle.test.mjs`.

If the page can't load the result document, for example because the host binds the game just after the lobby starts, it retries with backoff of 1, 2 and 4 seconds, then every 10 seconds, while the lobby is started and the document isn't loaded. [`resultretry.mjs`](resultretry.mjs) decides the delay; run its tests with `node lobby/frontend/resultretry.test.mjs`.

A lobby plays one game per round (`lobby.round` in the snapshot). Once the game has ended, **Back to lobby** in the result area reopens the lobby (`POST …/reopen`), which goes back to waiting with the same players, teams and seats, so everyone gets ready again and the host starts the next game. While the game runs, the page explains that it opens once the game has ended. The page keeps one result document and one example player per round: when the round changes it drops them, and the next start loads the next round's. A result URL with `{round}` gets the lobby's round in its place, so the game can answer only with that round's game; Island Battles' result URL asks for `?lobby_round={round}`. [`rounds.mjs`](rounds.mjs) fills the URL and words a refused reopen.

An example player module is an ES module that exports an `ExamplePlayer` class. The page constructs it with `{lobbyID, lobbyRound, playerID, fetcher, subscription, enabled, demo, pending, savePending, onState, onStatus}`:

- `lobbyRound` is the lobby round that the player plays; the page makes a new player for each round. The player should take only the states of that round's game.
- `fetcher(url, init)` is a `fetch` for the game's API that sends the player's token for the game and renews it (the player sends no credential of its own), and `subscription` is an unsubscribed Centrifugo subscription to the player's `player:<player_id>` channel.
- `demo` is true for a demo lobby's player, which plays its example strategy while `enabled`, and false for any other lobby, where the player does what its game assigns the page, such as relaying a bot's turns, and `enabled` stays true.
- `enabled` says whether the player may submit turns. The page reads `player.enabled` and calls `player.setEnabled(enabled)` for **Pause this player**.
- `pending` and `savePending(pending)` keep an unfinished request in `sessionStorage` across reloads.
- `onState(state)` passes game state on to the result frame, and `onStatus(title, detail)` updates the example player panel.
- The page calls `start()` once the lobby starts and `stop()` when the player leaves the room.

A settings module is an ES module that exports a `SettingsForm` class and, optionally, a `describeSettings` function:

- The page constructs `new SettingsForm({container, onChange})` when the game is selected. The form adds its controls to `container`, an element inside the create form.
- When the host creates a lobby, the page calls `form.choice()`. It returns `{settings, teams}`: `settings` is the `game_settings` object, sent only when it has keys, and `teams` lists the team names in order, or is null for the default teams. The lobby gets one seat per team. To stop the create request, `choice()` throws an `Error` whose message the page shows.
- `describeSettings(game_settings)` returns a short line, or a promise of one, that the room shows under the lobby name for every member, such as the chosen map's name. The page describes the settings again whenever a snapshot carries other settings, so every member sees a change without reloading.
- A form may open another page and listen for its `message` events, as a game's form may do for a page it opens. It must accept only messages from its own origin and from the window it opened. It may have a `destroy()` method, which the page calls when it removes the form, to stop listening.
- With a settings module, the host of a waiting room gets a button under the room's settings, labelled with the module's optional `changeSettingsLabel` export (Island Battles: **Change map**) or **Change settings**. It opens the same form in the room, constructed as `new SettingsForm({container, onChange, current, teamCount})` with `current` the lobby's `game_settings` and `teamCount` the number of teams the lobby has, which the form shows as chosen (a settings module that has no use for the count ignores it). **Save** sends the form's `choice()` to `PUT …/settings` as `{teams, game_settings}` ([changing a lobby's settings](../README.md#changing-a-lobbys-settings)); **Cancel** closes it. Saving clears every player's ready; a team beyond the new count loses its player or its seat. The page explains `not_host`, `phase_conflict`, `lobby_full` (more players and bots than the new teams) and `invalid_game_settings` (with the game's message).

The game checks the settings when the lobby is created and when the host changes them. A rejection shows the game's message, which can name the invalid field. **New demo lobby** uses the form's choice too, and needs it to have exactly two teams; a demo room offers no settings change.

## Realtime

The frontend uses the locally vendored official Centrifugo JavaScript SDK. It subscribes to lobby invalidations, fetches a snapshot after subscription/reconnect, and fetches lobby state when its version changes. There are no recurring snapshot GETs. The only periodic HTTP request is the membership lease heartbeat every fifteen seconds; command and connection failures can retry. Allow the frontend's origin in Centrifugo (`localhost:8888` is included in `.env.example`).
