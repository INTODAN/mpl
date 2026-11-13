import { test } from "node:test";
import assert from "node:assert/strict";
import { relayRequest } from "./relay.mjs";
import { roomRequest } from "./roomapi.mjs";
import { fetchRetrying, isRepeatableLobbyRequest, maxRetryWaitMs, serverBusyMessage } from "./throttle.mjs";

const throttled = (headers = {}) => new Response('{"Message":"Rate Exceeded."}', { status: 429, statusText: "Too Many Requests", headers });
const ok = () => new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } });

// stub answers with the given responses in order and repeats the last one.
function stub(...responses) {
  const calls = [];
  const waits = [];
  return {
    calls, waits,
    fetch: async (url, init) => { calls.push({ url, init }); return responses[Math.min(calls.length, responses.length) - 1](); },
    sleep: async (ms) => { waits.push(ms); },
    random: () => 0,
  };
}

test("a 429 followed by a success returns the success", async () => {
  const s = stub(throttled, ok);
  const response = await fetchRetrying(s.fetch, "/x", {}, { repeatable: true, sleep: s.sleep, random: s.random });
  assert.equal(response.status, 200);
  assert.equal(s.calls.length, 2);
  assert.deepEqual(s.waits, [500]);
});

test("it stops after 3 retries and returns the 429", async () => {
  const s = stub(throttled);
  const response = await fetchRetrying(s.fetch, "/x", {}, { repeatable: true, sleep: s.sleep, random: s.random });
  assert.equal(response.status, 429);
  assert.equal(s.calls.length, 4);
  assert.deepEqual(s.waits, [500, 1000, 2000]);
});

test("jitter adds up to 25 percent", async () => {
  const s = stub(throttled);
  await fetchRetrying(s.fetch, "/x", {}, { repeatable: true, sleep: s.sleep, random: () => 0.999 });
  assert.deepEqual(s.waits.map(Math.round), [625, 1250, 2500]);
});

test("Retry-After lengthens the wait but never past 5 seconds", async () => {
  const s = stub(() => throttled({ "Retry-After": "3" }), () => throttled({ "Retry-After": "60" }), ok);
  await fetchRetrying(s.fetch, "/x", {}, { repeatable: true, sleep: s.sleep, random: s.random });
  assert.deepEqual(s.waits, [3000, maxRetryWaitMs]);
});

test("a request that is not repeatable is not retried", async () => {
  const s = stub(throttled, ok);
  const response = await fetchRetrying(s.fetch, "/x", {}, { repeatable: false, sleep: s.sleep, random: s.random });
  assert.equal(response.status, 429);
  assert.equal(s.calls.length, 1);
});

test("other errors are not retried", async () => {
  for (const status of [400, 409, 500]) {
    const s = stub(() => new Response("{}", { status }), ok);
    const response = await fetchRetrying(s.fetch, "/x", {}, { repeatable: true, sleep: s.sleep, random: s.random });
    assert.equal(response.status, status);
    assert.equal(s.calls.length, 1);
  }
});

test("lobby requests that create, join, start, change seats or remove watchers are not repeatable", () => {
  const path = "/games/g/lobbies/l";
  assert.equal(isRepeatableLobbyRequest("GET", path), true);
  assert.equal(isRepeatableLobbyRequest("PUT", `${path}/team`), true);
  assert.equal(isRepeatableLobbyRequest("PUT", `${path}/ready`), true);
  assert.equal(isRepeatableLobbyRequest("POST", `${path}/heartbeat`), true);
  assert.equal(isRepeatableLobbyRequest("PUT", `${path}/settings`), true);
  assert.equal(isRepeatableLobbyRequest("PUT", `${path}/member-settings`), true);
  assert.equal(isRepeatableLobbyRequest("POST", `${path}/reopen`), true);
  assert.equal(isRepeatableLobbyRequest("POST", "/player-tokens"), true);
  assert.equal(isRepeatableLobbyRequest("POST", "/lobbies"), false);
  assert.equal(isRepeatableLobbyRequest("POST", "/memberships"), false);
  assert.equal(isRepeatableLobbyRequest("POST", `${path}/start`), false);
  assert.equal(isRepeatableLobbyRequest("POST", `${path}/seats`), false);
  assert.equal(isRepeatableLobbyRequest("DELETE", `${path}/seats/s`), false);
  assert.equal(isRepeatableLobbyRequest("DELETE", `${path}/watchers/p`), false);
  assert.equal(isRepeatableLobbyRequest("DELETE", `${path}/membership`), false);
  // Watching creates the watcher, and leaving it removes the watcher: neither is sent again.
  assert.equal(isRepeatableLobbyRequest("POST", "/watches"), false);
  assert.equal(isRepeatableLobbyRequest("DELETE", `${path}/watch`), false);
  // Reading the watchers is a GET like any other.
  assert.equal(isRepeatableLobbyRequest("GET", `${path}/watchers`), true);
});

const lobbyID = "6f1c2c9e-7d0a-4a43-9c55-6e3f0a1b2c3d";
const origin = "https://lobby.example";

test("a relayed GET is retried and then answers with the plain message", async () => {
  const data = { type: "game.request", id: 1, path: `/quiz/${lobbyID}/data/logs` };
  const base = { prefix: "/quiz/{lobby_id}/data/", lobbyID, origin };
  const again = stub(throttled, ok);
  const done = await relayRequest(data, { ...base, ...again });
  assert.equal(done.status, 200);
  assert.equal(again.calls.length, 2);
  const stuck = stub(throttled);
  const failed = await relayRequest(data, { ...base, ...stuck });
  assert.equal(stuck.calls.length, 4);
  assert.equal(failed.status, 429);
  assert.equal(failed.body.detail, serverBusyMessage);
});

test("a room module's GET and PUT are retried and then answer with the plain message", async () => {
  const base = { prefix: "/quiz/{lobby_id}/players/", lobbyID, origin };
  const path = `/quiz/${lobbyID}/players/choice`;
  const again = stub(throttled, ok);
  const done = await roomRequest({ method: "PUT", path, body: { pick: 1 } }, { ...base, ...again });
  assert.equal(done.status, 200);
  assert.equal(again.calls.length, 2);
  assert.equal(again.calls[1].init.body, '{"pick":1}');
  const stuck = stub(throttled);
  const failed = await roomRequest({ path }, { ...base, ...stuck });
  assert.equal(stuck.calls.length, 4);
  assert.deepEqual(failed, { status: 429, body: { status: 429, detail: serverBusyMessage } });
});
