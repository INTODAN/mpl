# Temporary lobby frontend

Run `./scripts/dev.sh` from the repository root, then open **http://localhost:8888/lobby/ui/** (or your configured backend address). The script supports Docker Compose or native Podman on Linux; see the [backend setup](../../backend/README.md#run-locally).

For the quickest test, click **New demo lobby**. It creates Alex on Blue in the current tab, opens Sam on Red in a second tab, and marks both ready. Click **Start lobby** in Alex's tab to run the game. If the browser blocks the second tab, use the displayed **Open second player tab** link. Each demo creates fresh player sessions; using it from an existing lobby leaves that lobby first.

To set up players manually:

1. Enter your name, select **Island Battles**, and create a lobby.
2. Open the same page in another tab, browser, or device. Enter another name and join using the eight-character code.
3. Each player chooses a different team and marks ready. The host can then start the lobby.

New lobbies have two seats. Starting freezes the roster and the host backend launches the selected game. For Island Battles, both players see an automatic battle replay with play/pause, a turn slider, and a final result. These controls only change playback; players cannot command the troops. On another device, use the host's reachable address instead of `localhost`.

Guest credentials and the current room are saved in `sessionStorage`, so independently opened tabs can act as separate players and a reload restores the current player. A browser's **Duplicate tab** action may copy that identity; open a fresh tab using the URL to test a second player. **Leave lobby** removes the player immediately. Closing a tab relies on the server's lease expiry and grace period.

This temporary client polls the HTTP API every two seconds and sends a heartbeat every fifteen seconds. It uses no JavaScript dependencies or build step. `index.html`, `style.css`, and `app.js` are embedded by `handler.go`; restart/rebuild the Go host after changing them.

For another host, mount `frontend.Handler()` at `/lobby/ui/` with that prefix stripped, alongside the same-origin `/lobby/v1` API. Game choices live in `index.html`, with display names and optional result URLs in `app.js`; the host must also register any added game keys in its lobby policy. All lobby operations use the generic API. A game's result URL returns its own HTML document, fetched with the guest bearer credential and displayed in a sandboxed iframe. Simulation and rendering remain owned by the game backend. Result fetches retry while the game is being prepared; the host can retry a failed start without creating another game.
