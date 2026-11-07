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
      prefix: "/quiz/{lobby_id}/players/", lobbyID, origin, credential: "secret",
      fetch: async (url, init) => { calls.push({ url, init }); return respond(url, init); },
    },
  };
}

test("sends GET and PUT under the room prefix with the credential", async () => {
  const { calls, options } = context();
  const read = await roomRequest({ path: `/quiz/${lobbyID}/players/choices` }, options);
  assert.deepEqual(read, { status: 200, body: { ok: true } });
  const saved = await roomRequest({ method: "PUT", path: `/quiz/${lobbyID}/players/choice`, body: { pick: 2 } }, options);
  assert.equal(saved.status, 200);
  assert.equal(calls[0].url, `${origin}/quiz/${lobbyID}/players/choices`);
  assert.equal(calls[0].init.method, "GET");
  assert.equal(calls[0].init.headers.Authorization, "Bearer secret");
  assert.equal(calls[0].init.body, undefined);
  assert.equal(calls[1].init.method, "PUT");
  assert.equal(calls[1].init.body, '{"pick":2}');
  assert.equal(calls[1].init.headers["Content-Type"], "application/json");
  assert.equal(calls[1].init.redirect, "error");
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
    [{ method: "POST", path: `/quiz/${lobbyID}/players/choice` }, /method/],
    [{ method: "DELETE", path: `/quiz/${lobbyID}/players/choice` }, /method/],
    [{ method: "GET", path: `/quiz/${lobbyID}/players/choice`, body: {} }, /no body/],
    [{ path: `/quiz/${lobbyID}/data/x` }, /refused/],
    [{ path: "/quiz/another-lobby/players/choice" }, /refused/],
    [{ path: `/quiz/${lobbyID}/players/../../admin` }, /refused/],
    [{ path: "https://evil.example/quiz/" }, /refused/],
  ];
  for (const [request, message] of refused) await assert.rejects(roomRequest(request, options), message);
  await assert.rejects(roomRequest({ path: `/quiz/${lobbyID}/players/x` }, { ...options, prefix: undefined }), /refused/);
  await assert.rejects(roomRequest({ path: `/quiz/${lobbyID}/players/x` }, { ...options, credential: null }), /session/);
  assert.equal(calls.length, 0);
});

test("reports a failed fetch as unreachable", async () => {
  const { options } = context(() => { throw new TypeError("network"); });
  await assert.rejects(roomRequest({ path: `/quiz/${lobbyID}/players/x` }, options), /Cannot reach/);
});
