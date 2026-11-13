import { test } from "node:test";
import assert from "node:assert/strict";
import { roomRequest } from "./roomapi.mjs";

const lobbyID = "6f1c2c9e-7d0a-4a43-9c55-6e3f0a1b2c3d";
const origin = "https://lobby.example";

function context(respond = () => new Response(JSON.stringify({ ok: true }), { headers: { "Content-Type": "application/json" } })) {
  const calls = [];
  return {
    calls,
    options: {
      prefix: "/quiz/{lobby_id}/players/", lobbyID, origin,
      fetch: async (url, init) => { calls.push({ url, init }); return respond(url, init); },
    },
  };
}

test("sends GET and PUT under the room prefix through the page's fetch", async () => {
  const { calls, options } = context();
  const read = await roomRequest({ path: `/quiz/${lobbyID}/players/choices` }, options);
  assert.deepEqual(read, { status: 200, body: { ok: true } });
  const saved = await roomRequest({ method: "PUT", path: `/quiz/${lobbyID}/players/choice`, body: { pick: 2 } }, options);
  assert.equal(saved.status, 200);
  assert.equal(calls[0].url, `${origin}/quiz/${lobbyID}/players/choices`);
  assert.equal(calls[0].init.method, "GET");
  // The page's fetch adds the player's token; the module adds no credential.
  assert.equal(calls[0].init.headers.Authorization, undefined);
  assert.equal(calls[0].init.body, undefined);
  assert.equal(calls[1].init.method, "PUT");
  assert.equal(calls[1].init.body, '{"pick":2}');
  assert.equal(calls[1].init.headers["Content-Type"], "application/json");
  assert.equal(calls[1].init.redirect, "error");
});

test("sends POST and DELETE under the room prefix through the page's fetch", async () => {
  const { calls, options } = context();
  const created = await roomRequest({ method: "POST", path: `/quiz/${lobbyID}/players/uploads`, body: { name: "x" } }, options);
  assert.equal(created.status, 200);
  const empty = await roomRequest({ method: "POST", path: `/quiz/${lobbyID}/players/uploads/1/complete` }, options);
  assert.equal(empty.status, 200);
  await roomRequest({ method: "DELETE", path: `/quiz/${lobbyID}/players/uploads/1` }, options);
  assert.deepEqual(calls.map((call) => [call.init.method, call.init.body, call.init.headers.Authorization]), [
    ["POST", '{"name":"x"}', undefined], ["POST", undefined, undefined], ["DELETE", undefined, undefined],
  ]);
});

test("sends a throttled GET, PUT or DELETE again, but never a POST", async () => {
  for (const [method, attempts] of [["GET", 4], ["PUT", 4], ["DELETE", 4], ["POST", 1]]) {
    const { calls, options } = context(() => new Response("", { status: 429 }));
    const answer = await roomRequest({ method, path: `/quiz/${lobbyID}/players/x` }, { ...options, sleep: async () => {}, random: () => 0 });
    assert.equal(answer.status, 429, method);
    assert.equal(calls.length, attempts, method);
  }
});

test("answers a problem with its status and body", async () => {
  const { options } = context(() => new Response('{"detail":"no"}', { status: 409, headers: { "Content-Type": "application/problem+json" } }));
  assert.deepEqual(await roomRequest({ method: "PUT", path: `/quiz/${lobbyID}/players/choice`, body: {} }, options), { status: 409, body: { detail: "no" } });
  const text = context(() => new Response("not json", { status: 502 }));
  assert.deepEqual(await roomRequest({ path: `/quiz/${lobbyID}/players/choice` }, text.options), { status: 502, body: null });
});

test("refuses other methods, paths and lobbies without fetching", async () => {
  const { calls, options } = context();
  const refused = [
    [{ method: "PATCH", path: `/quiz/${lobbyID}/players/choice` }, /method/],
    [{ method: "HEAD", path: `/quiz/${lobbyID}/players/choice` }, /method/],
    [{ method: "GET", path: `/quiz/${lobbyID}/players/choice`, body: {} }, /no body/],
    [{ method: "DELETE", path: `/quiz/${lobbyID}/players/choice`, body: {} }, /no body/],
    [{ path: `/quiz/${lobbyID}/data/x` }, /refused/],
    [{ path: "/quiz/another-lobby/players/choice" }, /refused/],
    [{ path: `/quiz/${lobbyID}/players/../../admin` }, /refused/],
    [{ path: "https://evil.example/quiz/" }, /refused/],
  ];
  for (const [request, message] of refused) await assert.rejects(roomRequest(request, options), message);
  await assert.rejects(roomRequest({ path: `/quiz/${lobbyID}/players/x` }, { ...options, prefix: undefined }), /refused/);
  await assert.rejects(roomRequest({ path: `/quiz/${lobbyID}/players/x` }, { ...options, fetch: undefined }), /session/);
  assert.equal(calls.length, 0);
});

test("reports a failed fetch as unreachable", async () => {
  const { options } = context(() => { throw new TypeError("network"); });
  await assert.rejects(roomRequest({ path: `/quiz/${lobbyID}/players/x` }, options), /Cannot reach/);
});
