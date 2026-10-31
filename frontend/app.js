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
let resultRoom = null;
let resultRequest = null;
// The result document the page loaded into the frame, from its srcdoc, or null.
let resultDocument = null;
let gamePlayer = null;
let playerLoading = false;
let latestGameState = null;
// The selected game's settings form, from its catalog settings module.
let settingsForm = null;
let settingsFormGame = null;
let describedRoom = null;
const settingsModules = new Map();
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

async function api(path, method = "GET", body) {
  const headers = { Accept: "application/json" };
  if (state.session) headers.Authorization = `Bearer ${state.session.access_token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 8000);
  try {
    const response = await fetch(`/lobby/v1${path}`, {
      method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal: controller.signal,
    });
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

function dropRoom() {
  clearTimeout(timer);
  clearTimeout(heartbeatTimer);
  roomEpoch++;
  if (state.room) $("join-code").value = state.room.join_code || "";
  state.room = null;
  delete state.demoPlayer;
  gamePlayer?.stop();
  gamePlayer = null;
  realtime?.disconnect();
  realtime = null;
  realtimeLoading = null;
  lobbySubscription = null;
  playerLoading = false;
  latestGameState = null;
  describedRoom = null;
  $("room-settings").hidden = true;
  snapshot = null;
  resultRequest?.abort();
  resultRequest = null;
  resultRoom = null;
  resultDocument = null;
  $("game-result").removeAttribute("srcdoc");
  $("game-result").style.removeProperty("height");
  $("game-result").hidden = true;
  $("result-status").hidden = false;
  $("result-status").textContent = "Preparing the game…";
  $("retry-game").hidden = true;
  $("demo-player-link").hidden = true;
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
    state.session = null;
    state.pendingCreate = null;
    save();
    notice("Your session expired. Connect again to continue.");
  } else if (state.room && (error.code === "not_found" || error.code === "stale_membership")) {
    dropRoom();
    notice("You’re no longer connected to that lobby. Use its code to join again.");
  } else {
    notice(messages[error.code] || (error.code === "not_found" ? "No lobby found for that code and game." : error.message));
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
  snapshot = next;
  state.room = {
    game_key: next.lobby.game_key, lobby_id: next.lobby.id,
    join_code: next.lobby.join_code, membership_generation: next.membership_generation,
  };
  state.pendingCreate = null;
  save();
  render();
  void ensureRealtime();
  if (!heartbeatTimer) heartbeatTimer = setTimeout(heartbeat, 15000);
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
  let response;
  try {
    const { maxRelayedRequests, refuseRequest, relayRequest } = await loadRelay();
    if (!state.session || !state.room || lobbyID !== state.room.lobby_id) {
      response = refuseRequest(data, "the page shows no lobby for this game");
    } else if (shown.relayed >= maxRelayedRequests) {
      response = refuseRequest(data, "too many requests at once; wait for an answer first");
    } else {
      // Each document has its own count, so an earlier document's requests
      // never hold up the next one's.
      shown.relayed++;
      try {
        response = await relayRequest(data, {
          prefix: games.get(state.room.game_key)?.data_url_prefix, lobbyID, origin: location.origin, credential: state.session.access_token,
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
  if (!response || epoch !== roomEpoch || resultRoom !== lobbyID || resultDocument !== shown || !resultDocumentShown()) return;
  $("game-result").contentWindow?.postMessage(response, "*");
}

async function ensureGamePlayer() {
  const example = games.get(snapshot?.lobby.game_key)?.example_player;
  if (!example || gamePlayer || playerLoading) return;
  playerLoading = true;
  const epoch = roomEpoch;
  try {
    const { ExamplePlayer } = await import(example.module);
    const client = await ensureRealtime();
    if (epoch !== roomEpoch) return;
    if (!client) return;
    gamePlayer = new ExamplePlayer({
      lobbyID: snapshot.lobby.id, playerID: state.session.player_id, credential: state.session.access_token,
      subscription: client.newSubscription(`player:${state.session.player_id}`),
      enabled: Boolean(state.demoPlayer?.enabled), pending: state.demoPlayer?.pending || null,
      savePending: (pending) => {
        if (epoch === roomEpoch && state.demoPlayer) { state.demoPlayer.pending = pending; save(); }
      },
      onState: (gameState) => {
        if (epoch !== roomEpoch) return;
        latestGameState = gameState;
        displayGameState();
      },
      onStatus: (title, detail) => {
        if (epoch !== roomEpoch) return;
        $("example-status").textContent = title;
        $("example-detail").textContent = detail;
        $("example-toggle").textContent = gamePlayer.enabled ? "Pause this player" : "Resume this player";
      },
    });
    gamePlayer.start();
  } catch {
    if (epoch === roomEpoch) $("example-detail").textContent = "Could not load the example player. Retrying…";
  } finally {
    if (epoch === roomEpoch) playerLoading = false;
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

// The game owns the result document. The lobby only loads and displays it.
async function loadResult() {
  const room = snapshot.lobby;
  const resultURL = games.get(room.game_key)?.result_url;
  if (!resultURL || resultRequest || resultRoom === room.id) return;
  const controller = new AbortController();
  resultRequest = controller;
  const timeout = setTimeout(() => controller.abort(), 8000);
  try {
    const response = await fetch(resultURL.replaceAll("{lobby_id}", encodeURIComponent(room.id)), {
      headers: { Authorization: `Bearer ${state.session.access_token}`, Accept: "text/html" }, signal: controller.signal,
    });
    if (!response.ok) throw new Error(response.status === 404 ? "The game is being prepared. If it doesn’t appear, the host can retry starting it." : "Could not load the game. Retrying…");
    const html = await response.text();
    if (controller !== resultRequest) return;
    resultRoom = room.id;
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
  } finally {
    clearTimeout(timeout);
    if (controller === resultRequest) resultRequest = null;
  }
}

// A game's settings module adds its own controls to the create form. Each
// module is imported once per page.
function settingsModule(game) {
  if (!settingsModules.has(game.settings_module)) {
    settingsModules.set(game.settings_module, import(game.settings_module).catch((error) => {
      settingsModules.delete(game.settings_module);
      throw error;
    }));
  }
  return settingsModules.get(game.settings_module);
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

// Show the room's settings as the game's settings module describes them.
async function describeRoomSettings() {
  const game = games.get(snapshot.lobby.game_key);
  if (describedRoom === snapshot.lobby.id) return;
  describedRoom = snapshot.lobby.id;
  if (!game?.settings_module) return;
  const epoch = roomEpoch;
  try {
    const { describeSettings } = await settingsModule(game);
    const text = describeSettings ? await describeSettings(snapshot.lobby.game_settings) : "";
    if (epoch !== roomEpoch) return;
    $("room-settings").textContent = text || "";
    $("room-settings").hidden = !text;
  } catch {
    if (epoch === roomEpoch) describedRoom = null;
  }
}

function render() {
  void mountSettingsForm();
  const demoExample = selectedGame()?.example_player;
  $("demo-setup").hidden = !demoExample;
  $("demo-summary").textContent = demoExample?.summary || "";
  $("entry-panel").hidden = Boolean(snapshot);
  $("room-panel").hidden = !snapshot;
  $("result-panel").hidden = !snapshot || snapshot.lobby.phase !== "started" || !games.has(snapshot.lobby.game_key);
  $("example-controls").hidden = !state.demoPlayer || snapshot?.lobby.phase !== "started";
  $("display-name").readOnly = Boolean(state.session);
  $("change-player").hidden = !state.session;
  // Nothing can be created or joined until the host's games are known.
  for (const id of ["create-button", "join-button", "change-player", "leave-button", "demo-button", "game"]) $(id).disabled = busy || !games.size;
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
    details.append(element("div", "player-name", `${member.display_name}${isSelf ? " (you)" : ""}`));
    const team = teams.find((team) => team.id === member.team_id);
    const labels = [team ? team.name : "No team yet", member.disconnected_until ? "Reconnecting" : member.ready ? "Ready" : "Not ready"];
    if (member.player_id === lobby.host_player_id) labels.push("Host");
    details.append(element("div", "player-detail", labels.join(" · ")));
    player.append(element("div", "avatar", member.display_name.slice(0, 1).toUpperCase()), details);
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
  $("team-select").value = self?.team_id || "";
  $("team-select").disabled = busy || !waiting;
  $("waiting-controls").hidden = !waiting;
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
  if (!waiting) { loadResult(); ensureGamePlayer(); }
}

// A playground lobby has one seat per team. The teams come from the game's
// settings form when it has one.
async function createLobby(name, choice) {
  await ensureSession();
  const payload = {
    game_key: $("game").value, name: name || `${state.session.display_name}’s lobby`,
    capacity: choice.teams.length, teams: choice.teams.map((team) => ({ name: team, capacity: 1 })),
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
$("retry-game").addEventListener("click", () => action(async () => acceptSnapshot(await api(`${roomPath()}/start`, "POST", {}))));
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
$("room-code").addEventListener("click", () => $("room-code").select());
$("game").addEventListener("change", render);

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
  if (!state.session?.access_token || !(Date.parse(state.session.expires_at) > Date.now())) state = {};
  if (state.session) $("display-name").value = state.session.display_name;
  save();
  render();
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

render();
loadGames().then(restore);
