"use strict";

const $ = (id) => document.getElementById(id);
const storageKey = "lobby-playground";
// The host's game catalog by key, loaded from games.json at startup.
let games = new Map();
let saved;
try { saved = JSON.parse(sessionStorage.getItem(storageKey)); } catch { /* Start a fresh tab. */ }
let state = saved && typeof saved === "object" ? saved : {};
let snapshot = null;
let busy = false;
let timer;
let roomEpoch = 0;
let heartbeatTimer;
let realtime = null;
let realtimeLoading = null;
let lobbySubscription = null;
let syncing = false;
let teamOptions = "";
// The lobby round whose game the page shows: its result document and its
// example player. A lobby plays one game per round, so they start over when the
// round changes. gameEpoch counts those starts, like roomEpoch counts rooms.
let gameRound = null;
let gameEpoch = 0;
let resultRoom = null;
let resultRequest = null;
// The pending retry of loadResult after a failed read, and the failed reads in a row.
let resultRetryTimer = null;
let resultFailures = 0;
// The result document the page loaded into the frame, from its srcdoc, or null.
let resultDocument = null;
let gamePlayer = null;
let playerLoading = false;
// True once a player outside a demo reports what its game player does, such as
// relaying a localhost bot's turns, so the page shows the status panel.
let playerReports = false;
let latestGameState = null;
// The selected game's settings form, from its catalog settings module.
let settingsForm = null;
let settingsFormGame = null;
// The room and settings that #room-settings describes, as a key.
let describedRoom = null;
// The game settings module's label for changing a waiting room's settings,
// for the room it was loaded for: {epoch, label}.
let changeSettingsLabel = null;
// The host's form for changing a waiting room's settings while it is open:
// {epoch, form}, form being the game's SettingsForm, or null while it loads.
let roomSettingsForm = null;
// The game's controls in the waiting room, from its catalog room module:
// {epoch, panel} for the room it was made for, with a null panel when the
// module failed to load. roomPanelLoading is the epoch being loaded.
let roomPanel = null;
let roomPanelLoading = null;
// The seats that the host can add to a team, from the room module's
// seatOptions: {epoch, label, choices} for the room they were loaded for.
// seatControls is the key of the controls the room shows for them.
let seatOptions = null;
let seatControls = "";
// The games' settings and room modules by URL, each imported once per page.
const gameModules = new Map();
// The page's link and watcher rules, loaded at startup. Without them the page
// still plays; it offers no invite links and no watching.
let linksLibrary = null;
let watchLibrary = null;
// What a watcher outside the roster has open, in this tab: {game_key, lobby_id,
// channel, join_code}, kept in state so a reload watches again. watchEpoch
// counts the watches, like roomEpoch counts rooms.
let watchEpoch = 0;
let watchClient = null;
// The failed reads of the watcher's document in a row, and the pending retry.
let watchFailures = 0;
let watchRetryTimer = null;
let watchRequest = null;
// What the lobby's watch channel published while no document was shown to
// take it, so that the document, loaded a moment later, gets it too.
let watchEvents = [];
// The members' list of watchers: {lobbyID, version, list, reading, dirty,
// error, kicking}. It is read again when the lobby changes.
let watchers = { lobbyID: null, version: -1, list: [], reading: false, dirty: false, error: "", kicking: new Set() };
// The invite or watch link that waits for the player's name: {action, code, game}.
let pendingLink = null;
// Lobbies without a game settings module get these teams.
const defaultTeams = ["Blue", "Red"];

function uuid() {
  if (crypto.randomUUID) return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  const hex = [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function save() {
  try { sessionStorage.setItem(storageKey, JSON.stringify(state)); } catch { /* The page also works without storage. */ }
}

function notice(message = "") {
  $("notice").textContent = message;
  $("notice").hidden = !message;
}

function roomPath(room = state.room) {
  return `/games/${encodeURIComponent(room.game_key)}/lobbies/${encodeURIComponent(room.lobby_id)}`;
}

let throttleModule = null;
function loadThrottle() {
  throttleModule ??= import("./throttle.mjs").catch((error) => {
    throttleModule = null;
    throw error;
  });
  return throttleModule;
}

async function api(path, method = "GET", body) {
  const headers = { Accept: "application/json" };
  if (state.session) headers.Authorization = `Bearer ${state.session.access_token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const controller = new AbortController();
  let timeout;
  try {
    const { fetchRetrying, isRepeatableLobbyRequest, retryWaitBudgetMs, serverBusyMessage } = await loadThrottle();
    const repeatable = isRepeatableLobbyRequest(method, path);
    // The waits between retries come on top of the 8 seconds for the answer.
    timeout = setTimeout(() => controller.abort(), 8000 + (repeatable ? retryWaitBudgetMs : 0));
    const response = await fetchRetrying(fetch, `/lobby/v1${path}`, {
      method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal: controller.signal,
    }, { repeatable });
    if (response.status === 429) {
      const error = new Error(serverBusyMessage);
      error.status = 429;
      throw error;
    }
    if (response.status === 204) return null;
    const data = await response.json();
    if (!response.ok) {
      const error = new Error(data.detail || "The request could not be completed.");
      error.code = data.code;
      error.status = response.status;
      throw error;
    }
    return data;
  } catch (error) {
    if (error.status) throw error;
    throw new Error("Cannot reach the lobby server. Check your connection and try again.");
  } finally {
    clearTimeout(timeout);
  }
}

// A game's API takes a player token for that game, which the lobby issues to
// the session (playertoken.mjs), never the guest credential. gameFetch
// resolves to a fetch for gameKey's API that sends the token, renews it before
// it expires and once more when the game answers 401.
let playerTokenModule = null;
let playerTokens = null;
async function gameFetch(gameKey) {
  if (!state.session) throw new Error("The page has no player session.");
  const credential = state.session.access_token;
  playerTokenModule ??= import("./playertoken.mjs").catch((error) => {
    playerTokenModule = null;
    throw error;
  });
  const { createPlayerTokens, playerTokenFetch } = await playerTokenModule;
  playerTokens ??= createPlayerTokens({
    issue: async (session, key) => {
      if (state.session?.access_token !== session) throw new Error("The session changed.");
      return api("/player-tokens", "POST", { game_key: key });
    },
  });
  return playerTokenFetch({ tokens: playerTokens, credential, gameKey });
}

// The current round's game: its example player, its latest state and its
// result document. A lobby plays one game per round, so the page drops them
// when the round changes, and with the room.
function dropGame() {
  gameEpoch++;
  gameRound = null;
  gamePlayer?.stop();
  gamePlayer = null;
  playerReports = false;
  playerLoading = false;
  latestGameState = null;
  resultRequest?.abort();
  resultRequest = null;
  clearTimeout(resultRetryTimer);
  resultRetryTimer = null;
  resultFailures = 0;
  resultRoom = null;
  resultDocument = null;
  $("game-result").removeAttribute("srcdoc");
  $("game-result").style.removeProperty("height");
  $("game-result").hidden = true;
  $("result-status").hidden = false;
  $("result-status").textContent = "Preparing the game…";
  $("retry-game").hidden = true;
}

function dropRoom() {
  clearTimeout(timer);
  clearTimeout(heartbeatTimer);
  roomEpoch++;
  if (state.room) $("join-code").value = state.room.join_code || "";
  state.room = null;
  delete state.demoPlayer;
  dropGame();
  realtime?.disconnect();
  realtime = null;
  realtimeLoading = null;
  lobbySubscription = null;
  describedRoom = null;
  changeSettingsLabel = null;
  closeRoomSettings();
  roomPanel = null;
  roomPanelLoading = null;
  seatOptions = null;
  seatControls = "";
  $("seat-controls").replaceChildren();
  $("watchers-panel").hidden = true;
  $("seat-controls").hidden = true;
  $("game-room-panel").replaceChildren();
  $("game-room-panel").hidden = true;
  $("room-settings").hidden = true;
  snapshot = null;
  watchers = { lobbyID: null, version: -1, list: [], reading: false, dirty: false, error: "", kicking: new Set() };
  $("demo-player-link").hidden = true;
  save();
}

// The lobby page's start screen shows the watcher's own panel instead.
function selectedGameWatchable() {
  return Boolean(watchLibrary?.canWatch(selectedGame()));
}

// dropWatch ends this tab's watch: the realtime connection, the document and
// the saved watch. It makes no request.
function dropWatch() {
  watchEpoch++;
  clearTimeout(watchRetryTimer);
  watchRetryTimer = null;
  watchRequest?.abort();
  watchRequest = null;
  watchFailures = 0;
  watchEvents = [];
  watchClient?.disconnect();
  watchClient = null;
  if (state.watch) {
    state.watch = null;
    dropGame();
  }
  save();
}

function reportError(error) {
  const messages = {
    lobby_full: "This lobby is full. Every seat is taken.",
    team_full: "That team is taken. Please choose another team.",
    not_ready: "Every player needs a team and must be ready before starting.",
    empty_teams: "Every team needs a player before the game can start.",
    not_host: "Only the host can start the lobby.",
    phase_conflict: "The lobby has already started or closed.",
    unavailable: "The lobby server is temporarily unavailable. Please try again.",
  };
  if (error.status === 401) {
    dropRoom();
    dropWatch();
    state.session = null;
    state.pendingCreate = null;
    save();
    notice("Your session expired. Connect again to continue.");
  } else if (state.room && (error.code === "not_found" || error.code === "stale_membership")) {
    dropRoom();
    notice("You’re no longer connected to that lobby. Use its code to join again.");
  } else {
    notice(messages[error.code] || watchLibrary?.watchMessage(error) || (error.code === "not_found" ? "No lobby found for that code and game." : error.message));
  }
}

async function action(task) {
  if (busy || !games.size) return;
  busy = true;
  notice();
  render();
  try { await task(); } catch (error) { reportError(error); }
  finally { busy = false; render(); }
}

async function ensureSession() {
  const name = $("display-name").value.trim();
  if (!name) {
    $("display-name").focus();
    throw new Error("Enter your name first.");
  }
  if (!state.session) {
    const session = await api("/sessions", "POST", { display_name: name });
    state.session = { ...session, display_name: name };
    state.clientInstanceID = uuid();
    save();
  }
}

function acceptSnapshot(next) {
  if (snapshot && next.lobby.id === snapshot.lobby.id && next.lobby.version < snapshot.lobby.version) return;
  if (!snapshot || next.lobby.id !== snapshot.lobby.id) roomEpoch++;
  // The lobby was reopened, or its next game started: the game the page
  // showed is over, and the next one gets its own result and player.
  if (gameRound !== null && gameRound !== next.lobby.round) dropGame();
  snapshot = next;
  state.room = {
    game_key: next.lobby.game_key, lobby_id: next.lobby.id,
    join_code: next.lobby.join_code, membership_generation: next.membership_generation,
  };
  state.pendingCreate = null;
  save();
  render();
  void ensureRealtime();
  void updateWatchers(next);
  if (!heartbeatTimer) heartbeatTimer = setTimeout(heartbeat, 15000);
}

// The members' list of watchers is read again when the lobby changes, since a
// watcher arriving, leaving or removed is a lobby change. Reads never overlap:
// a change that comes during one asks for another once it ends.
async function updateWatchers(next) {
  if (!watchLibrary || !watchLibrary.canWatch(games.get(next.lobby.game_key))) return;
  if (watchers.lobbyID !== next.lobby.id) {
    watchers = { lobbyID: next.lobby.id, version: -1, list: [], reading: false, dirty: false, error: "", kicking: new Set() };
  }
  const changed = watchLibrary.watchersChanged({
    lastVersion: watchers.version, version: next.lobby.version, watcherCount: next.watcher_count, shownCount: watchers.list.length,
  });
  watchers.version = next.lobby.version;
  if (changed) await readWatchers();
}

async function readWatchers() {
  const current = watchers;
  if (current.reading) {
    current.dirty = true;
    return;
  }
  const room = state.room;
  current.reading = true;
  try {
    current.list = (await api(`${roomPath(room)}/watchers`)).watchers;
    current.error = "";
  } catch (error) {
    if (watchers !== current) return;
    if (error.status === 401) {
      reportError(error);
      return;
    }
    current.error = error.message;
  } finally {
    current.reading = false;
  }
  if (watchers !== current) return;
  render();
  if (current.dirty) {
    current.dirty = false;
    void readWatchers();
  }
}

// The host removes a watcher, who stops receiving the game at once and can't
// watch again with the code. A watcher who left meanwhile is gone either way.
async function kickWatcher(watcher) {
  const current = watchers;
  current.kicking.add(watcher.player_id);
  render();
  try {
    await api(`${roomPath()}/watchers/${encodeURIComponent(watcher.player_id)}`, "DELETE");
    current.list = current.list.filter((item) => item.player_id !== watcher.player_id);
    current.error = "";
  } catch (error) {
    if (error.status === 401) {
      reportError(error);
      return;
    }
    if (error.status !== 404) current.error = error.message;
  } finally {
    current.kicking.delete(watcher.player_id);
  }
  if (watchers !== current) return;
  render();
  void readWatchers();
}

// showWatchers draws the members' list of watchers in the room, with a Remove
// button for the host.
function showWatchers(host) {
  const game = games.get(snapshot.lobby.game_key);
  const shown = Boolean(watchLibrary?.canWatch(game));
  $("watchers-panel").hidden = !shown;
  if (!shown) return;
  const own = watchers.lobbyID === snapshot.lobby.id ? watchers : null;
  const list = own?.list || [];
  $("watcher-count").textContent = String(snapshot.watcher_count);
  $("watchers-empty").hidden = snapshot.watcher_count > 0;
  $("watchers-error").textContent = own?.error || "";
  $("watchers-error").hidden = !own?.error;
  $("watchers-list").replaceChildren(...list.map((watcher) => {
    const item = document.createElement("li");
    item.dataset.playerId = watcher.player_id;
    item.append(element("span", "watcher-name", watcher.display_name));
    if (host) {
      const remove = element("button", "text-button", "Remove");
      remove.type = "button";
      remove.disabled = busy || own.kicking.has(watcher.player_id);
      remove.setAttribute("aria-label", `Remove ${watcher.display_name}`);
      remove.addEventListener("click", () => void kickWatcher(watcher));
      item.append(remove);
    }
    return item;
  }));
}

// occupiedTeams lists the IDs of the teams that a member other than
// exceptPlayerID, a person or a seat, is on. A team holds one member, so
// nobody else can join an occupied team.
function occupiedTeams(room, exceptPlayerID = null) {
  return new Set(room.members.filter((member) => member.team_id && member.player_id !== exceptPlayerID).map((member) => member.team_id));
}

// emptyTeams lists the teams that block the start under the game's declared
// start rules: with every_team_needs_member, the teams that have no member.
function emptyTeams(room) {
  if (!room.start_rules?.every_team_needs_member) return [];
  return room.teams.filter((team) => !room.members.some((member) => member.team_id === team.id));
}

function scheduleSync(delay = 0) {
  clearTimeout(timer);
  if (state.room) timer = setTimeout(sync, delay);
}

async function sync() {
  if (!state.room) return;
  if (busy || syncing) { scheduleSync(100); return; }
  syncing = true;
  const epoch = roomEpoch;
  const room = state.room;
  try {
    const next = await api(roomPath(room));
    if (epoch !== roomEpoch) return;
    acceptSnapshot(next);
    if (state.demoPlayer && next.lobby.phase === "waiting" && next.lobby.host_player_id === state.session.player_id &&
        next.members.length === 2 && next.members.every((member) => member.ready && member.team_id && !member.disconnected_until) &&
        !emptyTeams(next).length) {
      const started = await api(`${roomPath(room)}/start`, "POST", {});
      if (epoch !== roomEpoch) return;
      acceptSnapshot(started);
    }
    $("connection").textContent = "Connected";
    $("connection").classList.remove("reconnecting");
  } catch (error) {
    if (epoch !== roomEpoch) return;
    if (error.status === 401 || error.code === "not_found" || error.code === "stale_membership") {
      reportError(error);
      render();
    } else {
      $("connection").textContent = "Reconnecting…";
      $("connection").classList.add("reconnecting");
      // Retry failed reconciliation; successful idle subscriptions make no GETs.
      scheduleSync(2000);
    }
  } finally { syncing = false; }
}

async function heartbeat() {
  heartbeatTimer = null;
  if (!state.room) return;
  const epoch = roomEpoch;
  try {
    await api(`${roomPath()}/heartbeat`, "POST", {
      client_instance_id: state.clientInstanceID, membership_generation: state.room.membership_generation,
    });
  } catch (error) {
    if (epoch === roomEpoch && (error.status === 401 || error.code === "not_found" || error.code === "stale_membership")) {
      reportError(error);
      render();
    }
  } finally {
    if (epoch === roomEpoch && state.room) heartbeatTimer = setTimeout(heartbeat, 15000);
  }
}

function ensureRealtime() {
  if (realtime || realtimeLoading || !snapshot) return realtimeLoading || Promise.resolve(realtime);
  const epoch = roomEpoch;
  const channel = snapshot.channel;
  realtimeLoading = (async () => {
    const token = await api("/connection-token", "POST", {});
    if (epoch !== roomEpoch) return null;
    const client = new Centrifuge(token.websocket_url, {
      token: token.token,
      getToken: async () => (await api("/connection-token", "POST", {})).token,
    });
    realtime = client;
    const connection = (connected) => {
      if (epoch !== roomEpoch) return;
      $("connection").textContent = connected ? "Live updates connected" : "Reconnecting…";
      $("connection").classList.toggle("reconnecting", !connected);
    };
    client.on("connected", () => connection(true));
    client.on("connecting", () => connection(false));
    client.on("disconnected", () => connection(false));
    lobbySubscription = client.newSubscription(channel, { recoverable: true, positioned: true });
    lobbySubscription.on("subscribed", () => { if (epoch === roomEpoch) scheduleSync(); });
    lobbySubscription.on("publication", ({ data }) => {
      if (epoch === roomEpoch && data?.type === "lobby.changed" && data.schema_version === 1 &&
          data.lobby_id === snapshot.lobby.id && data.game_key === snapshot.lobby.game_key && data.version > snapshot.lobby.version) scheduleSync();
    });
    lobbySubscription.subscribe();
    client.connect();
    return client;
  })().catch((error) => {
    if (epoch === roomEpoch) {
      if (error.status === 401) { reportError(error); render(); }
      else setTimeout(() => { if (epoch === roomEpoch) void ensureRealtime(); }, 2000);
    }
    return null;
  }).finally(() => { if (epoch === roomEpoch) realtimeLoading = null; });
  return realtimeLoading;
}

function element(tag, className, text) {
  const node = document.createElement(tag);
  node.className = className;
  node.textContent = text;
  return node;
}

function selectedGame() {
  return games.get($("game").value);
}

// A game's own pages, from its catalog links. The host checked that each URL
// is a same-origin path. They open in a new tab, so this tab keeps its lobby.
function showGameLinks(container, game) {
  const links = (game?.links || []).map((link) => {
    const anchor = document.createElement("a");
    anchor.href = link.url;
    anchor.target = "_blank";
    anchor.rel = "noopener";
    anchor.textContent = link.label;
    return anchor;
  });
  container.replaceChildren(...links);
  container.hidden = !links.length;
}

// Each srcdoc assignment loads a new result document. A sandboxed frame with
// allow-scripts can still navigate or reload itself, and its contentWindow
// stays the same object, so the page counts the frame's loads: it trusts the
// frame until the second load after the srcdoc, and then neither relays its
// requests nor forwards states to it until the next srcdoc.
function showResultDocument(html) {
  resultDocument = { loads: 0, relayed: 0 };
  $("game-result").srcdoc = html;
}
function resultDocumentShown() {
  return resultDocument !== null && resultDocument.loads <= 1;
}

// Forward the example player's latest state to the game's own result document.
function displayGameState() {
  if (latestGameState && resultRoom === state.room?.lobby_id && resultDocumentShown()) {
    $("game-result").contentWindow?.postMessage({ type: "lobby.game-state", game_key: state.room.game_key, state: latestGameState }, "*");
  }
}
$("game-result").addEventListener("load", () => {
  if (!resultDocument) return;
  resultDocument.loads++;
  displayGameState();
  flushWatchEvents();
});
window.addEventListener("message", (event) => {
  const frame = $("game-result");
  // The sandboxed game has an opaque origin; accept messages from this exact
  // frame only, while it shows the document the page loaded.
  if (event.source !== frame.contentWindow || !resultDocumentShown()) return;
  if (event.data?.type === "game.request") {
    void relayGameRequest(event.data);
    return;
  }
  // A watcher's document asks to be loaded anew when the lobby's next game
  // starts, since the page learns nothing of the lobby's rounds as a watcher.
  if (event.data?.type === "game.reload") {
    if (state.watch) reloadWatchResult();
    return;
  }
  if (event.data?.type !== "game.result-size") return;
  const height = event.data.height;
  if (typeof height !== "number" || !Number.isFinite(height) || height <= 0) return;
  frame.style.height = `${Math.ceil(height)}px`;
});

// The result frame can't send the credential, so the page fetches the game's
// data for it. relay.mjs decides which paths it may read.
let relayModule = null;

function loadRelay() {
  relayModule ??= import("./relay.mjs").catch((error) => {
    relayModule = null;
    throw error;
  });
  return relayModule;
}

// Every request with a usable ID is answered, also when the page can't relay
// it, so the frame never waits for an answer that won't come.
async function relayGameRequest(data) {
  const shown = resultDocument;
  const lobbyID = resultRoom;
  const epoch = roomEpoch;
  const watch = watchEpoch;
  const round = gameEpoch;
  // The lobby whose document is shown: a watcher's, or the room's.
  const target = state.watch
    ? { lobbyID: state.watch.lobby_id, gameKey: state.watch.game_key, game: games.get(state.watch.game_key), watching: true }
    : state.room ? { lobbyID: state.room.lobby_id, gameKey: state.room.game_key, game: games.get(state.room.game_key), watching: false } : null;
  let response;
  try {
    const { maxRelayedRequests, refuseRequest, relayRequest } = await loadRelay();
    if (!state.session || !target || lobbyID !== target.lobbyID) {
      response = refuseRequest(data, "the page shows no lobby for this game");
    } else if (shown.relayed >= maxRelayedRequests) {
      response = refuseRequest(data, "too many requests at once; wait for an answer first");
    } else {
      // Each document has its own count, so an earlier document's requests
      // never hold up the next one's.
      shown.relayed++;
      try {
        // A watcher's document reads the game's watcher prefix only, never a
        // player's, and nothing when its game names none.
        response = await relayRequest(data, {
          prefix: target.watching ? watchLibrary?.watchRelayPrefix(target.game) ?? null : target.game?.data_url_prefix,
          lobbyID, origin: location.origin, fetch: await gameFetch(target.gameKey),
        });
      } finally {
        shown.relayed--;
      }
    }
  } catch {
    // relay.mjs could not be loaded. Its message checks aren't available,
    // so answer only the IDs a frame normally sends.
    const id = data?.id;
    if (data?.type !== "game.request" || !((typeof id === "string" && id.length <= 200) || (typeof id === "number" && Number.isFinite(id)))) return;
    response = { type: "lobby.response", id, status: 0, body: null, error: "the page could not load its request relay; try again" };
  }
  // Answer only the document that asked, while it still shows its lobby.
  if (!response || epoch !== roomEpoch || watch !== watchEpoch || round !== gameEpoch || resultRoom !== lobbyID || resultDocument !== shown || !resultDocumentShown()) return;
  $("game-result").contentWindow?.postMessage(response, "*");
}

async function ensureGamePlayer() {
  const example = games.get(snapshot?.lobby.game_key)?.example_player;
  if (!example || gamePlayer || playerLoading) return;
  playerLoading = true;
  const epoch = roomEpoch;
  const round = gameEpoch;
  const current = () => epoch === roomEpoch && round === gameEpoch;
  try {
    const { ExamplePlayer } = await import(example.module);
    const client = await ensureRealtime();
    if (!current()) return;
    if (!client) return;
    gameRound = snapshot.lobby.round;
    // A previous round's player stopped, but its subscription to the player
    // channel stays registered with the client, which allows one per channel.
    const channel = `player:${state.session.player_id}`;
    const previous = client.getSubscription(channel);
    if (previous) client.removeSubscription(previous);
    // The player follows the game of this lobby round only.
    gamePlayer = new ExamplePlayer({
      lobbyID: snapshot.lobby.id, lobbyRound: snapshot.lobby.round, playerID: state.session.player_id,
      fetcher: await gameFetch(snapshot.lobby.game_key), subscription: client.newSubscription(channel),
      // Outside a demo the player submits only the turns of a localhost bot,
      // which it relays; the demo's player can be paused.
      demo: Boolean(state.demoPlayer), enabled: state.demoPlayer ? Boolean(state.demoPlayer.enabled) : true, pending: state.demoPlayer?.pending || null,
      savePending: (pending) => {
        if (current() && state.demoPlayer) { state.demoPlayer.pending = pending; save(); }
      },
      onState: (gameState) => {
        if (!current()) return;
        latestGameState = gameState;
        displayGameState();
        // The game reports state only once it exists, so a document that isn't
        // loaded yet can be read now instead of at its next retry.
        if (snapshot?.lobby.phase === "started" && resultRoom !== snapshot.lobby.id) void loadResult();
      },
      onStatus: (title, detail) => {
        if (!current()) return;
        if (!state.demoPlayer && !playerReports) {
          playerReports = true;
          render();
        }
        $("example-status").textContent = title;
        $("example-detail").textContent = detail;
        $("example-toggle").textContent = gamePlayer.enabled ? "Pause this player" : "Resume this player";
      },
    });
    gamePlayer.start();
  } catch {
    if (current()) $("example-detail").textContent = "Could not load the example player. Retrying…";
  } finally {
    if (current()) playerLoading = false;
  }
}

$("example-toggle").addEventListener("click", () => {
  if (!state.demoPlayer || !gamePlayer) return;
  state.demoPlayer.enabled = !gamePlayer.enabled;
  gamePlayer.setEnabled(state.demoPlayer.enabled);
  $("example-toggle").textContent = gamePlayer.enabled ? "Pause this player" : "Resume this player";
  $("example-status").textContent = gamePlayer.enabled ? "Example player running" : "Paused";
  save();
});

// The game owns the result document. The lobby only loads and displays it. The
// result URL can ask for the round, so that the game answers only with the game
// of the round the page shows.
let roundsModule = null;
async function loadResult() {
  const room = snapshot.lobby;
  const template = games.get(room.game_key)?.result_url;
  if (!template || resultRequest || resultRoom === room.id) return;
  clearTimeout(resultRetryTimer);
  resultRetryTimer = null;
  const epoch = roomEpoch;
  gameRound = room.round;
  const controller = new AbortController();
  resultRequest = controller;
  const timeout = setTimeout(() => controller.abort(), 8000);
  try {
    roundsModule ??= import("./rounds.mjs").catch((error) => {
      roundsModule = null;
      throw error;
    });
    const { resultURL } = await roundsModule;
    const fetchGame = await gameFetch(room.game_key);
    const response = await fetchGame(resultURL(template, room), { headers: { Accept: "text/html" }, signal: controller.signal });
    if (!response.ok) throw new Error(response.status === 404 ? "The game is being prepared. If it doesn’t appear, the host can retry starting it." : "Could not load the game. Retrying…");
    const html = await response.text();
    if (controller !== resultRequest) return;
    resultRoom = room.id;
    resultFailures = 0;
    showResultDocument(html);
    $("game-result").hidden = false;
    $("result-status").hidden = true;
    $("retry-game").hidden = true;
    if (state.demoPlayer) $("result-panel").scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (error) {
    if (controller !== resultRequest) return;
    $("result-status").hidden = false;
    $("result-status").textContent = error.name === "AbortError" ? "The game is taking longer to load. Retrying…" : error.message;
    $("retry-game").hidden = room.host_player_id !== state.session.player_id;
    void retryResult(epoch, room.id);
  } finally {
    clearTimeout(timeout);
    if (controller === resultRequest) resultRequest = null;
  }
}

// The result can be missing for a moment after the lobby starts, and no other
// lobby change follows to ask again, so a failed read is retried with backoff
// for as long as the lobby is started and the document isn't loaded. The retry
// ends when the room changes or the document loads. resultretry.mjs decides
// the delay; if it can't be loaded, the page retries at the slowest pace.
let retryModule = null;
async function retryResult(epoch, lobbyID) {
  const failures = ++resultFailures;
  retryModule ??= import("./resultretry.mjs").catch(() => {
    retryModule = null;
    return { resultRetryDelay: ({ phase, loaded }) => (phase === "started" && !loaded ? 10000 : null) };
  });
  const { resultRetryDelay } = await retryModule;
  const delay = resultRetryDelay({ phase: snapshot?.lobby.phase, loaded: resultRoom === lobbyID, failures });
  // A newer read, such as one the example player's first state starts, makes
  // its own retry decision.
  if (delay === null || epoch !== roomEpoch || snapshot?.lobby.id !== lobbyID || resultRequest) return;
  clearTimeout(resultRetryTimer);
  resultRetryTimer = setTimeout(() => {
    resultRetryTimer = null;
    if (epoch === roomEpoch && snapshot?.lobby.id === lobbyID && snapshot.lobby.phase === "started") void loadResult();
  }, delay);
}

// ----- Watching a lobby from outside its roster -----
//
// A watcher has no room: the lobby shows its members to its members only. The
// page shows the game's own watcher document once the game has started, reads
// it with the guest credential, and follows what the lobby's watch channel
// publishes by passing it on to the document. The host can remove a watcher.

function watchStatus(text) {
  $("result-status").hidden = false;
  $("result-status").textContent = text;
}

// Starts watching the lobby of a join code, and shows its game. A member of
// that lobby gets their room instead.
async function startWatching(joinCode, gameKey) {
  await ensureSession();
  const access = await api("/watches", "POST", { game_key: gameKey, join_code: joinCode });
  if (access.member) {
    acceptSnapshot(await api(roomPath({ game_key: access.game_key, lobby_id: access.lobby_id })));
    scheduleSync(0);
    return;
  }
  if (state.room) {
    await api(`${roomPath()}/membership`, "DELETE").catch(() => {});
    dropRoom();
  }
  dropWatch();
  state.watch = { game_key: access.game_key, lobby_id: access.lobby_id, channel: access.channel, join_code: joinCode };
  save();
  watchStatus("Waiting for the host to start the game…");
  $("retry-game").hidden = true;
  void connectWatch();
  void loadWatchResult();
}

// Stops watching and returns to the start screen, saying why when there is
// a reason. Leaving asks the lobby to forget the watcher; a removed watcher
// has already been forgotten.
async function stopWatching({ message = "", leave = true } = {}) {
  const watch = state.watch;
  if (!watch) return;
  dropWatch();
  notice(message);
  render();
  if (!leave) return;
  try { await api(`${roomPath(watch)}/watch`, "DELETE"); } catch { /* The lobby forgets a watcher that goes silent. */ }
}

// A watcher's subscription ended. The host may have removed them, which only
// the lobby's answer tells; anything else is a connection that the client
// restores by itself.
async function confirmWatching(epoch) {
  const watch = state.watch;
  if (!watch || epoch !== watchEpoch) return;
  try {
    await api("/watches", "POST", { game_key: watch.game_key, join_code: watch.join_code });
  } catch (error) {
    if (epoch !== watchEpoch) return;
    if (watchLibrary?.wasKicked(error)) {
      await stopWatching({ message: watchLibrary.kickedMessage, leave: false });
    } else if (error.code === "not_found") {
      await stopWatching({ message: "That lobby is gone.", leave: false });
    } else if (error.status === 401) {
      reportError(error);
      render();
    }
  }
}

async function connectWatch() {
  const epoch = watchEpoch;
  const watch = state.watch;
  try {
    const token = await api("/connection-token", "POST", {});
    if (epoch !== watchEpoch) return;
    const client = new Centrifuge(token.websocket_url, {
      token: token.token,
      getToken: async () => (await api("/connection-token", "POST", {})).token,
    });
    watchClient = client;
    const connection = (connected) => {
      if (epoch !== watchEpoch) return;
      $("watch-connection").textContent = connected ? "Live updates connected" : "Reconnecting…";
      $("watch-connection").classList.toggle("reconnecting", !connected);
    };
    client.on("connected", () => connection(true));
    client.on("connecting", () => connection(false));
    client.on("disconnected", () => connection(false));
    const subscription = client.newSubscription(watch.channel, { recoverable: true, positioned: true });
    let subscribed = false;
    subscription.on("subscribed", (context) => {
      if (epoch !== watchEpoch) return;
      // After a reconnect that could not recover every publication, the
      // document is read again, so it misses no generation.
      if (subscribed && !context.recovered) reloadWatchResult();
      subscribed = true;
    });
    subscription.on("publication", ({ data }) => {
      if (epoch !== watchEpoch) return;
      watchEvents.push(data);
      if (watchEvents.length > 100) watchEvents.shift();
      flushWatchEvents();
    });
    subscription.on("unsubscribed", () => void confirmWatching(epoch));
    subscription.on("error", () => void confirmWatching(epoch));
    subscription.subscribe();
    client.connect();
  } catch (error) {
    if (epoch !== watchEpoch) return;
    if (error.status === 401) {
      reportError(error);
      render();
    } else {
      setTimeout(() => { if (epoch === watchEpoch) void connectWatch(); }, 2000);
    }
  }
}

// Passes the lobby's watch events to the shown document, which ignores
// those it already has. Events that arrive before a document is shown wait.
function flushWatchEvents() {
  const watch = state.watch;
  if (!watch || resultRoom !== watch.lobby_id || !resultDocumentShown()) return;
  const frame = $("game-result").contentWindow;
  if (!frame) return;
  for (const event of watchEvents) frame.postMessage({ type: "lobby.watch-event", game_key: watch.game_key, event }, "*");
  watchEvents = [];
}

// The watcher's document is loaded anew, for the lobby's next game or after a
// connection that missed publications.
function reloadWatchResult() {
  if (!state.watch) return;
  resultRoom = null;
  void loadWatchResult();
}

async function loadWatchResult() {
  const watch = state.watch;
  const template = watch && games.get(watch.game_key)?.watch_result_url;
  if (!template || watchRequest || resultRoom === watch.lobby_id) return;
  clearTimeout(watchRetryTimer);
  watchRetryTimer = null;
  const epoch = watchEpoch;
  const controller = new AbortController();
  watchRequest = controller;
  const timeout = setTimeout(() => controller.abort(), 8000);
  try {
    const fetchGame = await gameFetch(watch.game_key);
    const response = await fetchGame(watchLibrary.watchResultURL(template, watch.lobby_id), { headers: { Accept: "text/html" }, signal: controller.signal });
    if (epoch !== watchEpoch) return;
    if (response.status === 403) {
      await stopWatching({ message: watchLibrary.kickedMessage, leave: false });
      return;
    }
    if (response.status === 401) {
      reportError({ status: 401 });
      render();
      return;
    }
    // The document is missing until the host has started the game.
    if (!response.ok) throw Object.assign(new Error("The document is not available."), { shown: response.status === 404 ? "Waiting for the host to start the game…" : "Could not load the game. Retrying…" });
    const html = await response.text();
    if (controller !== watchRequest || epoch !== watchEpoch) return;
    watchFailures = 0;
    resultRoom = watch.lobby_id;
    resultFailures = 0;
    showResultDocument(html);
    $("game-result").hidden = false;
    $("result-status").hidden = true;
    render();
  } catch (error) {
    if (controller !== watchRequest || epoch !== watchEpoch) return;
    watchStatus(error.shown ?? (error.name === "AbortError" ? "The game is taking longer to load. Retrying…" : "Could not load the game. Retrying…"));
    const delay = watchLibrary.watchRetryDelay(++watchFailures);
    watchRetryTimer = setTimeout(() => {
      watchRetryTimer = null;
      if (epoch === watchEpoch) void loadWatchResult();
    }, delay);
  } finally {
    clearTimeout(timeout);
    if (controller === watchRequest) watchRequest = null;
  }
}

// A game's settings and room modules are imported once per page; a module
// that failed to load is imported again next time.
function gameModule(url) {
  if (!gameModules.has(url)) {
    gameModules.set(url, import(url).catch((error) => {
      gameModules.delete(url);
      throw error;
    }));
  }
  return gameModules.get(url);
}

// A game's settings module adds its own controls to the create form.
function settingsModule(game) {
  return gameModule(game.settings_module);
}

// A game's room module calls the game's API through the page, which adds the
// player's token for the game. roomapi.mjs decides which requests it may send.
let roomAPIModule = null;
async function requestForRoom(lobbyID, request) {
  if (!state.session || state.room?.lobby_id !== lobbyID) throw new Error("The page no longer shows this lobby.");
  roomAPIModule ??= import("./roomapi.mjs").catch((error) => {
    roomAPIModule = null;
    throw error;
  });
  const { roomRequest } = await roomAPIModule;
  return roomRequest(request, {
    prefix: games.get(state.room.game_key)?.room_api_prefix, lobbyID, origin: location.origin, fetch: await gameFetch(state.room.game_key),
  });
}
function roomRequester(lobbyID) {
  return (method, path, body) => requestForRoom(lobbyID, { method, path, body });
}

// A game's room module saves the player's own member settings for the game
// through the page, with the lobby's PUT …/member-settings. The page shows the
// snapshot it answers with, and the module gets it back. A refusal rejects
// with an Error that carries the lobby's detail, code and status.
function memberSettingsSaver(lobbyID) {
  return async (settings) => {
    if (!state.session || state.room?.lobby_id !== lobbyID) throw new Error("The page no longer shows this lobby.");
    const next = await api(`${roomPath()}/member-settings`, "PUT", { member_settings: settings });
    if (state.room?.lobby_id === lobbyID) acceptSnapshot(next);
    return next;
  };
}

// While the lobby waits, the game's room module shows the player the game's
// own controls, such as a choice the game requires before getting ready, and
// sees every new snapshot.
function ensureRoomPanel() {
  const game = games.get(snapshot.lobby.game_key);
  const shown = Boolean(game?.room_module) && snapshot.lobby.phase === "waiting";
  $("game-room-panel").hidden = !shown;
  if (!shown) return;
  if (roomPanel?.epoch === roomEpoch) {
    roomPanel.panel?.update?.(snapshot);
    return;
  }
  if (roomPanelLoading !== roomEpoch) void loadRoomPanel(game);
}
async function loadRoomPanel(game) {
  const epoch = roomEpoch;
  const lobbyID = snapshot.lobby.id;
  roomPanelLoading = epoch;
  let panel = null;
  try {
    const module = await gameModule(game.room_module);
    if (epoch !== roomEpoch) return;
    $("game-room-panel").replaceChildren();
    panel = new module.RoomPanel({ container: $("game-room-panel"), lobbyID, playerID: state.session.player_id, request: roomRequester(lobbyID), saveMemberSettings: memberSettingsSaver(lobbyID) });
    if (module.seatOptions) void loadSeatOptions(module.seatOptions, epoch);
  } catch {
    if (epoch === roomEpoch) $("game-room-panel").replaceChildren(element("p", "hint", "Could not load this game’s controls. Reload the page to try again."));
  } finally {
    if (epoch === roomEpoch) {
      roomPanelLoading = null;
      roomPanel = { epoch, panel };
      if (snapshot) panel?.update?.(snapshot);
    }
  }
}

// The room module's seatOptions lists the seats that the host can add to a
// team, such as the game's bot levels: {label, choices: [{label, settings}]}.
async function loadSeatOptions(load, epoch) {
  try {
    const options = await load();
    if (epoch !== roomEpoch || !Array.isArray(options?.choices) || !options.choices.length) return;
    seatOptions = { epoch, label: String(options.label || "Add"), choices: options.choices };
    render();
  } catch {
    // The room works without seats; the host can still invite players.
  }
}

// For the host of a waiting room, one control per team that adds a seat the
// game plays, with the seat_settings of the choice the host selects.
function showSeatControls(shown) {
  const container = $("seat-controls");
  const options = seatOptions?.epoch === roomEpoch ? seatOptions : null;
  container.hidden = !shown || !options;
  if (container.hidden) {
    seatControls = "";
    container.replaceChildren();
    return;
  }
  const key = JSON.stringify([snapshot.lobby.id, snapshot.teams.map((team) => [team.id, team.name])]);
  if (key !== seatControls) {
    seatControls = key;
    container.replaceChildren(...snapshot.teams.map((team) => {
      const row = element("div", "seat-row", "");
      row.dataset.teamId = team.id;
      const select = document.createElement("select");
      select.append(...options.choices.map((choice, index) => new Option(choice.label, String(index))));
      const label = element("label", "", team.name);
      label.append(select);
      const add = element("button", "secondary", `${options.label} to ${team.name}`);
      add.type = "button";
      add.addEventListener("click", () => action(async () => acceptSnapshot(await api(`${roomPath()}/seats`, "POST", {
        request_id: uuid(), team_id: team.id, seat_settings: options.choices[Number(select.value)].settings,
      }))));
      row.append(label, add);
      return row;
    }));
  }
  // A seat takes a whole team, so only an empty team offers one.
  const occupied = occupiedTeams(snapshot);
  for (const row of container.children) row.hidden = occupied.has(row.dataset.teamId);
  container.hidden = [...container.children].every((row) => row.hidden);
  for (const control of container.querySelectorAll("button, select")) control.disabled = busy;
}

// Before a demo player gets ready, the game's room module can make the
// choices the game requires, ones that suit its example player.
async function prepareDemoPlayer() {
  const game = games.get(snapshot.lobby.game_key);
  if (!game?.room_module) return;
  const { prepareDemo } = await gameModule(game.room_module);
  if (prepareDemo) await prepareDemo({ lobbyID: snapshot.lobby.id, request: roomRequester(snapshot.lobby.id), saveMemberSettings: memberSettingsSaver(snapshot.lobby.id) });
}

async function mountSettingsForm() {
  const game = selectedGame();
  if (!game || settingsFormGame === game.key) return;
  settingsFormGame = game.key;
  settingsForm = null;
  $("game-settings").replaceChildren();
  if (!game.settings_module) return;
  try {
    const { SettingsForm } = await settingsModule(game);
    if (settingsFormGame !== game.key) return;
    settingsForm = new SettingsForm({ container: $("game-settings"), onChange: () => {} });
  } catch {
    if (settingsFormGame !== game.key) return;
    settingsFormGame = null;
    $("game-settings").replaceChildren(element("p", "hint", "Could not load this game’s settings. Choose the game again to retry."));
  }
}

// The selected game's settings and teams for a new lobby.
function lobbyChoice() {
  const game = selectedGame();
  if (game?.settings_module && (!settingsForm || settingsFormGame !== game.key)) throw new Error("This game’s settings are still loading. Try again in a moment.");
  const choice = settingsForm && game?.settings_module ? settingsForm.choice() : { settings: {}, teams: null };
  const teams = choice.teams || defaultTeams;
  if (teams.length < 2) throw new Error("A lobby needs at least two teams. Choose other settings.");
  return { settings: choice.settings || {}, teams };
}

// Show the room's settings as the game's settings module describes them. The
// host can change them while the room waits, so the description follows the
// snapshot's settings.
async function describeRoomSettings() {
  const game = games.get(snapshot.lobby.game_key);
  const key = JSON.stringify([snapshot.lobby.id, snapshot.lobby.game_settings]);
  if (describedRoom === key) return;
  describedRoom = key;
  if (!game?.settings_module) return;
  const epoch = roomEpoch;
  try {
    const module = await settingsModule(game);
    const text = module.describeSettings ? await module.describeSettings(snapshot.lobby.game_settings) : "";
    if (epoch !== roomEpoch || describedRoom !== key) return;
    changeSettingsLabel = { epoch, label: String(module.changeSettingsLabel || "Change settings") };
    $("room-settings").textContent = text || "";
    $("room-settings").hidden = !text;
    render();
  } catch {
    if (epoch === roomEpoch && describedRoom === key) describedRoom = null;
  }
}

// For the host of a waiting room whose game has a settings module, a control
// that opens the game's settings form, filled with the room's settings.
// Saving replaces the room's settings and teams.
function showChangeSettings(shown) {
  if (!shown) closeRoomSettings();
  const label = changeSettingsLabel?.epoch === roomEpoch ? changeSettingsLabel.label : null;
  $("change-settings").hidden = !shown || !label || roomSettingsForm !== null;
  if (label) $("change-settings").textContent = label;
  $("change-settings").disabled = busy;
  $("save-settings").disabled = busy || !roomSettingsForm?.form;
  $("cancel-settings").disabled = busy;
}
async function openRoomSettings() {
  const game = games.get(snapshot.lobby.game_key);
  if (roomSettingsForm || !game?.settings_module) return;
  const epoch = roomEpoch;
  roomSettingsForm = { epoch, form: null };
  $("room-settings-fields").replaceChildren();
  $("room-settings-form").hidden = false;
  render();
  try {
    const { SettingsForm } = await settingsModule(game);
    if (roomSettingsForm?.epoch !== epoch || roomSettingsForm.form) return;
    roomSettingsForm.form = new SettingsForm({ container: $("room-settings-fields"), onChange: () => {}, current: snapshot.lobby.game_settings, teamCount: snapshot.teams.length });
  } catch {
    if (roomSettingsForm?.epoch !== epoch) return;
    closeRoomSettings();
    notice("Could not load this game’s settings. Try again in a moment.");
  }
  render();
}
function closeRoomSettings() {
  roomSettingsForm?.form?.destroy?.();
  roomSettingsForm = null;
  $("room-settings-fields").replaceChildren();
  $("room-settings-form").hidden = true;
}

// What the host reads when a change of the room's settings is refused.
function settingsChangeError(error) {
  const messages = {
    not_host: "Only the host can change the settings.",
    phase_conflict: "The game has already started, so its settings can’t change any more.",
    lobby_full: "These settings have fewer teams than the lobby has players and bots. Remove a bot, or choose settings with more teams.",
    invalid_game_settings: `These settings can’t be used: ${error.message}`,
  };
  if (!messages[error.code]) return error;
  const explained = new Error(messages[error.code]);
  explained.status = error.status;
  return explained;
}
async function saveRoomSettings() {
  const form = roomSettingsForm?.form;
  if (!form) return;
  const choice = form.choice();
  const teams = choice.teams || defaultTeams;
  if (teams.length < 2) throw new Error("A lobby needs at least two teams. Choose other settings.");
  let changed;
  try {
    changed = await api(`${roomPath()}/settings`, "PUT", { teams: teams.map((team) => ({ name: team })), game_settings: choice.settings || {} });
  } catch (error) {
    throw settingsChangeError(error);
  }
  closeRoomSettings();
  acceptSnapshot(changed);
}

function render() {
  void mountSettingsForm();
  const demoExample = selectedGame()?.example_player;
  $("demo-setup").hidden = !demoExample || Boolean(state.watch);
  $("demo-summary").textContent = demoExample?.summary || "";
  showGameLinks($("game-links"), selectedGame());
  const watching = Boolean(state.watch);
  $("entry-panel").hidden = Boolean(snapshot) || watching;
  $("room-panel").hidden = !snapshot;
  $("watch-panel").hidden = !watching;
  $("watch-form").hidden = !selectedGameWatchable();
  $("result-panel").hidden = watching ? false : !snapshot || snapshot.lobby.phase !== "started" || !games.has(snapshot.lobby.game_key);
  // A watcher has no lobby to go back to and no player of their own.
  $("result-actions").hidden = watching;
  $("example-controls").hidden = watching || !(state.demoPlayer || playerReports) || snapshot?.lobby.phase !== "started";
  $("copy-invite").hidden = !linksLibrary;
  if (watching) {
    $("watch-game").textContent = games.get(state.watch.game_key)?.name || state.watch.game_key;
    $("watching-as").textContent = `Watching as ${state.session?.display_name || ""}`;
    $("watch-status").textContent = resultRoom === state.watch.lobby_id ? "The game is shown below." : "The game appears below once the host starts it.";
  }
  $("stop-watching").disabled = busy;
  $("watch-button").disabled = busy || !games.size;
  $("link-invite-button").disabled = busy || !games.size;
  $("example-label").textContent = state.demoPlayer ? "Example strategy · " : "Your localhost bot";
  $("example-strategy").hidden = !state.demoPlayer;
  $("example-toggle").hidden = !state.demoPlayer;
  $("display-name").readOnly = Boolean(state.session);
  $("change-player").hidden = !state.session;
  // Nothing can be created or joined until the host's games are known.
  for (const id of ["create-button", "join-button", "change-player", "leave-button", "demo-button", "game", "copy-invite"]) $(id).disabled = busy || !games.size;
  if (!snapshot) return;

  const { lobby, teams, members } = snapshot;
  if (members.length === 2 && !$("demo-player-link").hidden) {
    $("demo-player-link").hidden = true;
    notice();
  }
  const self = members.find((member) => member.player_id === state.session.player_id);
  const waiting = lobby.phase === "waiting";
  const host = lobby.host_player_id === state.session.player_id;
  const game = games.get(lobby.game_key);
  $("room-game").textContent = game?.name || lobby.game_key;
  showGameLinks($("room-links"), game);
  $("example-strategy").textContent = game?.example_player?.strategy || "";
  $("room-name").textContent = lobby.name;
  void describeRoomSettings();
  $("room-code").value = lobby.join_code;
  $("player-count").textContent = `${members.length} / ${lobby.capacity}`;
  $("phase").textContent = waiting ? "Waiting" : "Started";
  $("playing-as").textContent = `Playing as ${state.session.display_name}`;
  $("players").replaceChildren();
  for (const member of members) {
    const player = element("div", "player", "");
    const details = element("div", "", "");
    const isSelf = member.player_id === state.session.player_id;
    const isSeat = member.kind === "seat";
    details.append(element("div", "player-name", `${member.display_name}${isSelf ? " (you)" : ""}`));
    const team = teams.find((team) => team.id === member.team_id);
    // A seat that the host added is always ready, and the game plays it.
    const labels = [team ? team.name : "No team yet", isSeat ? "Played by the game" : member.disconnected_until ? "Reconnecting" : member.ready ? "Ready" : "Not ready"];
    if (member.player_id === lobby.host_player_id) labels.push("Host");
    details.append(element("div", "player-detail", labels.join(" · ")));
    player.append(element("div", "avatar", member.display_name.slice(0, 1).toUpperCase()), details);
    if (isSeat && host && waiting) {
      const remove = element("button", "text-button seat-remove", "Remove");
      remove.type = "button";
      remove.disabled = busy;
      remove.setAttribute("aria-label", `Remove ${member.display_name}`);
      remove.addEventListener("click", () => action(async () => acceptSnapshot(await api(`${roomPath()}/seats/${encodeURIComponent(member.id)}`, "DELETE"))));
      player.append(remove);
    }
    $("players").append(player);
  }
  for (let index = members.length; index < lobby.capacity; index++) {
    $("players").append(element("div", "player empty", "＋ Waiting for another player…"));
  }
  const optionsKey = JSON.stringify(teams.map((team) => [team.id, team.name]));
  if (optionsKey !== teamOptions) {
    teamOptions = optionsKey;
    $("team-select").replaceChildren(new Option("Choose a team", ""), ...teams.map((team) => new Option(team.name, team.id)));
  }
  // A team holds one member: one that someone else is on can't be chosen.
  const takenByOthers = occupiedTeams(snapshot, state.session.player_id);
  teams.forEach((team, index) => {
    const option = $("team-select").options[index + 1];
    option.disabled = takenByOthers.has(team.id);
    option.textContent = option.disabled ? `${team.name} (taken)` : team.name;
  });
  $("team-select").value = self?.team_id || "";
  $("team-select").disabled = busy || !waiting;
  $("waiting-controls").hidden = !waiting;
  ensureRoomPanel();
  showWatchers(host);
  showSeatControls(host && waiting);
  showChangeSettings(host && waiting && !state.demoPlayer);
  $("ready-button").textContent = self?.ready ? "Not ready" : "Mark ready";
  $("ready-button").disabled = busy || !self?.team_id;
  $("start-button").hidden = !host;
  const allReady = members.length >= 2 && members.every((member) => member.ready && member.team_id && !member.disconnected_until);
  const empty = emptyTeams(snapshot);
  $("start-button").disabled = busy || !allReady || empty.length > 0;
  const emptyHint = empty.length
    ? `Every team needs a player before the game can start. Still empty: ${empty.map((team) => team.name).join(", ")}.`
    : "";
  const startedHint = state.demoPlayer && game?.example_player
    ? `${game.example_player.summary} Keep both tabs open; pause either player to hold the next turn.`
    : game?.started_hint || "The game has started.";
  $("room-hint").textContent = !waiting ? startedHint : state.demoPlayer ? "The demo starts automatically when both players are ready." : members.length < 2 ? "Share the code above to invite the other players." : allReady && emptyHint ? emptyHint : host ? "Once every player chooses a team and marks ready, you can start the lobby." : "Choose a team and mark ready. The host will start the lobby.";
  $("retry-game").disabled = busy;
  $("back-to-lobby").disabled = busy;
  if (!waiting) { loadResult(); ensureGamePlayer(); }
}

// A lobby holds one member per team. The teams come from the game's settings
// form when it has one.
async function createLobby(name, choice) {
  await ensureSession();
  const payload = {
    game_key: $("game").value, name: name || `${state.session.display_name}’s lobby`,
    teams: choice.teams.map((team) => ({ name: team })),
    client_instance_id: state.clientInstanceID,
  };
  if (Object.keys(choice.settings).length) payload.game_settings = choice.settings;
  // Retain the idempotency key if a request succeeds but its response is lost.
  if (JSON.stringify(state.pendingCreate?.payload) !== JSON.stringify(payload)) state.pendingCreate = { payload, requestID: uuid() };
  save();
  acceptSnapshot(await api("/lobbies", "POST", { ...payload, request_id: state.pendingCreate.requestID }));
  scheduleSync(0);
}

async function readyOnTeam(index) {
  acceptSnapshot(await api(`${roomPath()}/team`, "PUT", { team_id: snapshot.teams[index].id }));
  await prepareDemoPlayer();
  acceptSnapshot(await api(`${roomPath()}/ready`, "PUT", { ready: true }));
}

$("demo-button").addEventListener("click", () => {
  if (busy || !selectedGame()?.example_player) return;
  // Open during the click so popup blockers allow it, before any HTTP requests.
  const secondPlayer = window.open("about:blank", "_blank");
  if (secondPlayer) {
    secondPlayer.opener = null;
    secondPlayer.document.title = "Preparing second player…";
    secondPlayer.document.body.textContent = "Preparing the demo lobby…";
  }
  action(async () => {
    try {
      const choice = lobbyChoice();
      if (choice.teams.length !== 2) throw new Error("The demo has two players, so it needs settings with exactly two teams.");
      if (state.room) await api(`${roomPath()}/membership`, "DELETE");
      dropRoom();
      state = { demoPlayer: { enabled: true } };
      save();
      $("demo-player-link").hidden = true;
      $("display-name").value = "Alex";
      await createLobby("Demo lobby", choice);
      await readyOnTeam(0);
      const url = new URL(location.pathname, location.origin);
      // Only an invitation is transferred. Sam creates an independent session.
      url.hash = new URLSearchParams({ demo: snapshot.lobby.join_code, game: snapshot.lobby.game_key }).toString();
      if (secondPlayer && !secondPlayer.closed) {
        secondPlayer.location.replace(url.href);
      } else {
        $("demo-player-link").href = url.href;
        $("demo-player-link").hidden = false;
        notice("The second tab was blocked. Click “Open second player tab” to finish setup.");
      }
    } catch (error) {
      secondPlayer?.close();
      throw error;
    }
  });
});

$("create-form").addEventListener("submit", (event) => {
  event.preventDefault();
  action(() => createLobby($("lobby-name").value.trim(), lobbyChoice()));
});
$("join-form").addEventListener("submit", (event) => {
  event.preventDefault();
  action(async () => {
    await ensureSession();
    acceptSnapshot(await api("/memberships", "POST", {
      game_key: $("game").value, join_code: $("join-code").value.trim().toUpperCase(), client_instance_id: state.clientInstanceID,
    }));
    scheduleSync(0);
  });
});
$("team-select").addEventListener("change", (event) => {
  const teamID = event.target.value || null;
  action(async () => acceptSnapshot(await api(`${roomPath()}/team`, "PUT", { team_id: teamID })));
});
$("ready-button").addEventListener("click", () => action(async () => {
  const self = snapshot.members.find((member) => member.player_id === state.session.player_id);
  acceptSnapshot(await api(`${roomPath()}/ready`, "PUT", { ready: !self.ready }));
}));
$("start-button").addEventListener("click", () => action(async () => acceptSnapshot(await api(`${roomPath()}/start`, "POST", {}))));
$("change-settings").addEventListener("click", () => void openRoomSettings());
$("cancel-settings").addEventListener("click", () => {
  closeRoomSettings();
  render();
});
$("room-settings-form").addEventListener("submit", (event) => {
  event.preventDefault();
  action(saveRoomSettings);
});
$("retry-game").addEventListener("click", () => action(async () => acceptSnapshot(await api(`${roomPath()}/start`, "POST", {}))));
// Back to lobby: once the game has ended, any player brings the lobby back to
// waiting, with its players, teams and seats, for the next game. A second
// press, or one after another player's, changes nothing.
$("back-to-lobby").addEventListener("click", () => action(async () => {
  try {
    acceptSnapshot(await api(`${roomPath()}/reopen`, "POST"));
  } catch (error) {
    roundsModule ??= import("./rounds.mjs").catch(() => {
      roundsModule = null;
      return { reopenMessage: () => null };
    });
    const message = (await roundsModule).reopenMessage(error);
    if (!message) throw error;
    const explained = new Error(message);
    explained.code = error.code;
    explained.status = error.status;
    throw explained;
  }
}));
$("leave-button").addEventListener("click", () => action(async () => {
  await api(`${roomPath()}/membership`, "DELETE");
  dropRoom();
}));
$("change-player").addEventListener("click", () => {
  state = {};
  save();
  $("display-name").value = "";
  render();
  $("display-name").focus();
});
$("copy-code").addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(snapshot.lobby.join_code);
    $("copy-code").textContent = "Copied!";
    setTimeout(() => { $("copy-code").textContent = "Copy code"; }, 1800);
  } catch { $("room-code").select(); }
});
// The invite link joins the lobby for whoever opens it, once they have a name.
$("copy-invite").addEventListener("click", async () => {
  if (!snapshot || !linksLibrary) return;
  const link = linksLibrary.inviteLink(location.origin, location.pathname, snapshot.lobby.join_code, snapshot.lobby.game_key);
  try {
    await navigator.clipboard.writeText(link);
    $("copy-invite").textContent = "Copied!";
    setTimeout(() => { $("copy-invite").textContent = "Copy invite link"; }, 1800);
  } catch {
    // Without clipboard access, show the link to copy by hand.
    notice(`Copy this invite link: ${link}`);
  }
});
$("watch-form").addEventListener("submit", (event) => {
  event.preventDefault();
  action(() => startWatching($("watch-code").value.trim().toUpperCase(), $("game").value));
});
$("stop-watching").addEventListener("click", () => action(() => stopWatching()));
$("link-invite-button").addEventListener("click", () => {
  const link = pendingLink;
  if (link) action(() => followLink(link));
});
$("room-code").addEventListener("click", () => $("room-code").select());
$("game").addEventListener("change", render);

// followLink joins or watches the lobby of an invite or watch link, which needs
// the player's name first. A code that is wrong or expired is reported like a
// wrong code typed into the form, and stays in its field to correct.
async function followLink(link) {
  const field = link.action === "join" ? "join-code" : "watch-code";
  $(field).value = link.code;
  try {
    await ensureSession();
  } catch (error) {
    // Without a name the link waits, with its banner, for the person to enter one.
    showPendingLink(link);
    throw error;
  }
  pendingLink = null;
  $("link-invite").hidden = true;
  if (state.watch) await stopWatching();
  if (state.room) {
    // The link leads to another lobby than the one this tab was in.
    await api(`${roomPath()}/membership`, "DELETE").catch(() => {});
    dropRoom();
  }
  if (link.action === "watch") {
    await startWatching(link.code, link.game || $("game").value);
    return;
  }
  acceptSnapshot(await api("/memberships", "POST", {
    game_key: link.game || $("game").value, join_code: link.code, client_instance_id: state.clientInstanceID,
  }));
  scheduleSync(0);
}

function showPendingLink(link) {
  pendingLink = link;
  const lobby = `lobby ${link.code}`;
  $("link-invite-text").textContent = link.action === "join"
    ? `You were invited to join ${lobby}. Enter your name, then continue.`
    : `You were invited to watch ${lobby}. Enter your name, then continue.`;
  $("link-invite-button").textContent = link.action === "join" ? "Join lobby" : "Watch lobby";
  $("link-invite").hidden = false;
}

async function restore() {
  const invitation = new URLSearchParams(location.hash.slice(1));
  if (invitation.has("demo")) {
    // New windows may inherit sessionStorage from their opener. Never reuse it.
    state = { demoPlayer: { enabled: true } };
    save();
    history.replaceState(null, "", location.pathname + location.search);
    $("display-name").value = "Sam";
    const game = invitation.get("game");
    const joinCode = invitation.get("demo");
    $("join-code").value = joinCode;
    await action(async () => {
      if (!games.get(game)?.example_player || !/^[A-Z0-9]{8}$/.test(joinCode)) throw new Error("This demo invitation is invalid.");
      $("game").value = game;
      await ensureSession();
      acceptSnapshot(await api("/memberships", "POST", { game_key: game, join_code: joinCode, client_instance_id: state.clientInstanceID }));
      scheduleSync(0);
      await readyOnTeam(1);
    });
    return;
  }
  // An invite or watch link is acted on once, after the player has a name, and
  // then leaves the address bar so a reload doesn't repeat it.
  const link = linksLibrary ? linksLibrary.readLinkParams(location.search) : { action: null, problem: null };
  if (link.action || link.problem) history.replaceState(null, "", linksLibrary.addressWithout(location.pathname, link.rest, location.hash));
  if (!state.session?.access_token || !(Date.parse(state.session.expires_at) > Date.now())) state = {};
  if (state.session) $("display-name").value = state.session.display_name;
  save();
  render();
  if (link.problem) {
    notice(link.problem);
    if (!state.room && !state.watch) return;
  }
  if (link.action) {
    if (link.game && !games.has(link.game)) link.game = null;
    if (link.game) $("game").value = link.game;
    render();
    // With a name already, the link acts at once; otherwise it waits for one.
    if (state.session) await action(() => followLink(link));
    else showPendingLink(link);
    return;
  }
  if (state.watch) {
    // A reload while watching watches again: the code finds the watcher row, or
    // says that the host removed this watcher or the lobby is gone.
    const watch = state.watch;
    state.watch = null;
    $("game").value = watch.game_key;
    await action(async () => {
      try {
        await startWatching(watch.join_code, watch.game_key);
      } catch (error) {
        save();
        throw error;
      }
    });
    return;
  }
  if (!state.room) return;
  await action(async () => {
    // Reuse this tab's lease on reload; an expired membership needs explicit rejoin.
    const room = state.room;
    try { acceptSnapshot(await api(roomPath(room))); }
    catch (error) { dropRoom(); throw error; }
    scheduleSync(0);
  });
}
// The host supplies its games; this page names none of its own.
async function loadGames() {
  for (;;) {
    try {
      const response = await fetch("./games.json", { headers: { Accept: "application/json" } });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const catalog = await response.json();
      $("game").replaceChildren(...catalog.map((game) => new Option(game.name, game.key)));
      games = new Map(catalog.map((game) => [game.key, game]));
      notice();
      return;
    } catch {
      notice("Cannot load the list of games. Retrying…");
      await new Promise((resolve) => setTimeout(resolve, 2000));
    }
  }
}

// The link and watcher rules load beside the catalog; a page that can't load
// them still plays, without invite links and watching.
async function loadLibraries() {
  const [links, watch] = await Promise.allSettled([gameModule("./links.mjs"), gameModule("./watch.mjs")]);
  linksLibrary = links.status === "fulfilled" ? links.value : null;
  watchLibrary = watch.status === "fulfilled" ? watch.value : null;
}

render();
Promise.all([loadGames(), loadLibraries()]).then(restore);
