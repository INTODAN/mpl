"use strict";

const $ = (id) => document.getElementById(id);
const storageKey = "lobby-playground";
const games = {
  "island-battles": { name: "Island Battles", resultURL: (lobbyID) => `/game/from-lobby/${encodeURIComponent(lobbyID)}/replay` },
};
let saved;
try { saved = JSON.parse(sessionStorage.getItem(storageKey)); } catch { /* Start a fresh tab. */ }
let state = saved && typeof saved === "object" ? saved : {};
let snapshot = null;
let busy = false;
let timer;
let roomEpoch = 0;
let lastHeartbeat = 0;
let teamOptions = "";
let resultRoom = null;
let resultRequest = null;

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
  roomEpoch++;
  if (state.room) $("join-code").value = state.room.join_code || "";
  state.room = null;
  snapshot = null;
  lastHeartbeat = 0;
  resultRequest?.abort();
  resultRequest = null;
  resultRoom = null;
  $("game-result").removeAttribute("srcdoc");
  $("game-result").hidden = true;
  $("result-status").hidden = false;
  $("result-status").textContent = "Preparing the game…";
  $("retry-game").hidden = true;
  $("demo-player-link").hidden = true;
  save();
}

function reportError(error) {
  const messages = {
    lobby_full: "This lobby is full. There’s room for two players.",
    team_full: "That team is taken. Please choose the other team.",
    not_ready: "Both players need a team and must be ready before starting.",
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
  if (busy) return;
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
}

function scheduleSync(delay = 2000) {
  clearTimeout(timer);
  if (state.room) timer = setTimeout(sync, delay);
}

async function sync() {
  if (!state.room) return;
  if (busy) { scheduleSync(); return; }
  const epoch = roomEpoch;
  const room = state.room;
  try {
    if (Date.now() - lastHeartbeat >= 15000) {
      await api(`${roomPath(room)}/heartbeat`, "POST", {
        client_instance_id: state.clientInstanceID, membership_generation: room.membership_generation,
      });
      if (epoch !== roomEpoch) return;
      lastHeartbeat = Date.now();
    }
    const next = await api(roomPath(room));
    if (epoch !== roomEpoch) return;
    acceptSnapshot(next);
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
    }
  } finally { scheduleSync(); }
}

function element(tag, className, text) {
  const node = document.createElement(tag);
  node.className = className;
  node.textContent = text;
  return node;
}

// The game owns the result document. The lobby only loads and displays it.
async function loadResult() {
  const room = snapshot.lobby;
  const resultURL = games[room.game_key]?.resultURL;
  if (!resultURL || resultRequest || resultRoom === room.id) return;
  const controller = new AbortController();
  resultRequest = controller;
  const timeout = setTimeout(() => controller.abort(), 8000);
  try {
    const response = await fetch(resultURL(room.id), {
      headers: { Authorization: `Bearer ${state.session.access_token}`, Accept: "text/html" }, signal: controller.signal,
    });
    if (!response.ok) throw new Error(response.status === 404 ? "The game is being prepared. If it doesn’t appear, the host can retry starting it." : "Could not load the game. Retrying…");
    const html = await response.text();
    if (controller !== resultRequest) return;
    resultRoom = room.id;
    $("game-result").srcdoc = html;
    $("game-result").hidden = false;
    $("result-status").hidden = true;
    $("retry-game").hidden = true;
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

function render() {
  $("entry-panel").hidden = Boolean(snapshot);
  $("room-panel").hidden = !snapshot;
  $("result-panel").hidden = !snapshot || snapshot.lobby.phase !== "started" || !games[snapshot.lobby.game_key]?.resultURL;
  $("display-name").readOnly = Boolean(state.session);
  $("change-player").hidden = !state.session;
  for (const id of ["create-button", "join-button", "change-player", "leave-button", "demo-button"]) $(id).disabled = busy;
  $("game").disabled = busy;
  if (!snapshot) return;

  const { lobby, teams, members } = snapshot;
  if (members.length === 2 && !$("demo-player-link").hidden) {
    $("demo-player-link").hidden = true;
    notice();
  }
  const self = members.find((member) => member.player_id === state.session.player_id);
  const waiting = lobby.phase === "waiting";
  const host = lobby.host_player_id === state.session.player_id;
  $("room-game").textContent = games[lobby.game_key]?.name || lobby.game_key;
  $("room-name").textContent = lobby.name;
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
  $("start-button").disabled = busy || members.length < 2 || !members.every((member) => member.ready && member.team_id && !member.disconnected_until);
  $("room-hint").textContent = !waiting ? "Island Battles waits for player commands through the Python client. The replay below shows recorded game state." : members.length < 2 ? "Share the code above to invite your second player." : host ? "Once both players choose a team and mark ready, you can start the lobby." : "Choose a team and mark ready. The host will start the lobby.";
  $("retry-game").disabled = busy;
  if (!waiting) loadResult();
}

async function createLobby(name) {
  await ensureSession();
  const payload = {
    game_key: $("game").value, name: name || `${state.session.display_name}’s lobby`,
    capacity: 2, teams: [{ name: "Blue", capacity: 1 }, { name: "Red", capacity: 1 }],
    client_instance_id: state.clientInstanceID,
  };
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
  if (busy) return;
  // Open during the click so popup blockers allow it, before any HTTP requests.
  const secondPlayer = window.open("about:blank", "_blank");
  if (secondPlayer) {
    secondPlayer.opener = null;
    secondPlayer.document.title = "Preparing second player…";
    secondPlayer.document.body.textContent = "Preparing the demo lobby…";
  }
  action(async () => {
    try {
      if (state.room) await api(`${roomPath()}/membership`, "DELETE");
      dropRoom();
      state = {};
      save();
      $("demo-player-link").hidden = true;
      $("display-name").value = "Alex";
      await createLobby("Demo lobby");
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
  action(() => createLobby($("lobby-name").value.trim()));
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

async function restore() {
  const invitation = new URLSearchParams(location.hash.slice(1));
  if (invitation.has("demo")) {
    // New windows may inherit sessionStorage from their opener. Never reuse it.
    state = {};
    save();
    history.replaceState(null, "", location.pathname + location.search);
    $("display-name").value = "Sam";
    const game = invitation.get("game");
    const joinCode = invitation.get("demo");
    $("join-code").value = joinCode;
    await action(async () => {
      if (!games[game] || !/^[A-Z0-9]{8}$/.test(joinCode)) throw new Error("This demo invitation is invalid.");
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
restore();
