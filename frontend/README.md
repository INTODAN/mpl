# Temporary lobby frontend

Run `./scripts/dev.sh` from the repository root, then open **http://localhost:8888/lobby/ui/** (or your configured backend address). The script supports Docker Compose or native Podman on Linux; see the [backend setup](../../backend/README.md#run-locally).

For the quickest test, click **New demo lobby**. It creates Alex on Blue in the current tab, opens Sam on Red in a second tab, marks both ready, and starts automatically. Both tabs run the [example player](../../backend/demo/example-player.mjs): send one soldier from the first available owned castle to the first enemy castle, then finish the turn. Players submit immediately when a generation arrives, without an artificial client delay. Replay playback has its own viewing pace. Failed requests still use retry backoff. If the browser blocks the second tab, use **Open second player tab**. Each demo creates fresh player sessions; using it from an existing lobby leaves that lobby first.

The board follows new generations live. **Pause this player** stops that tab from submitting its next turn; the other player then waits at the turn barrier. **Resume this player** continues. The timeline lets you review previous generations; **Follow live** returns to the newest state. Keep both tabs open. A matching, fully typed Python implementation is in [`examples/player.py`](../../clients/island-battles-python/examples/player.py).

To set up players manually:

1. Enter your name, select **Island Battles**, and create a lobby.
2. Open the same page in another tab, browser, or device. Enter another name and join using the eight-character code.
3. Each player chooses a different team and marks ready. The host can then start the lobby.

New lobbies have two seats. Starting freezes the roster and the host backend launches the selected game. Manually created/joined lobbies wait for commands through the [Island Battles Python client](../../clients/island-battles-python/README.md); only demo invitations enable the browser example player. Disconnected players remain part of the turn barrier. Playback controls change the view, while the example player's pause/resume controls submission. On another device, use the host's reachable address instead of `localhost`.

Guest credentials and the current room are saved in `sessionStorage`, so independently opened tabs can act as separate players and a reload restores the current player. A browser's **Duplicate tab** action may copy that identity; open a fresh tab using the URL to test a second player. **Leave lobby** removes the player immediately. Closing a tab relies on the server's lease expiry and grace period.

The frontend uses the locally vendored official Centrifugo JavaScript SDK. It subscribes to lobby invalidations and the player's generation channel. It fetches a snapshot after subscription/reconnect, and fetches lobby state when its version changes. New generation events carry the full game state directly. There are no recurring snapshot GETs. The only periodic HTTP request is the membership lease heartbeat every fifteen seconds; command and connection failures can retry. No frontend build step or CDN access is needed. Assets are embedded in Go; restart/rebuild the host after changing them. Allow the frontend's origin in Centrifugo (`localhost:8888` is included in `.env.example`).

For another host, mount `frontend.Handler()` at `/lobby/ui/` with that prefix stripped, alongside the same-origin `/lobby/v1` API. Game choices live in `index.html`, with display names, result URLs, and optional player modules in `app.js`. Island Battles serves its example module under `/game/demo/`. The host must register game keys in its lobby policy. A game's result URL returns its HTML document, fetched with the guest bearer credential and displayed in a sandboxed iframe. Generation updates reach the frame through parent messages; simulation and rendering remain owned by the game backend.

Run the browser player's strategy, deduplication, retry and no-polling checks from the repository root with `node backend/demo/example-player.test.mjs`.
