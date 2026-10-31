import assert from "node:assert/strict";
import test from "node:test";
import { defaultTimeoutSeconds, maxResponseBytes, maxTimeoutSeconds, refuseRequest, relayRequest, relayURL } from "./relay.mjs";

const origin = "https://play.example";
const lobby = "4a0f1a52-7f4e-4d5c-9a8e-2f3b6c1d0e9a";
const other = "9b1e2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d";
const prefix = "/quiz/{lobby_id}/data/";
const allowed = (path) => relayURL(prefix, lobby, path, origin);

test("paths under the lobby's prefix are relayed", () => {
  assert.equal(allowed(`/quiz/${lobby}/data/logs?round=4`), `${origin}/quiz/${lobby}/data/logs?round=4`);
  assert.equal(allowed(`/quiz/${lobby}/data/`), `${origin}/quiz/${lobby}/data/`);
  // The query is not part of the path check, and may be encoded.
  assert.equal(allowed(`/quiz/${lobby}/data/logs?name=a%20b&x=..`), `${origin}/quiz/${lobby}/data/logs?name=a%20b&x=..`);
});

test("paths outside the lobby's prefix are refused", () => {
  const refused = {
    "another lobby": `/quiz/${other}/data/logs`,
    "another lobby by prefix": `/quiz/${lobby.slice(0, 8)}/data/logs`,
    "the prefix without its slash": `/quiz/${lobby}/data`,
    "a sibling of the prefix": `/quiz/${lobby}/database`,
    "the lobby's other routes": `/quiz/${lobby}/result`,
    "the lobby API": "/lobby/v1/connection-token",
  };
  for (const [name, path] of Object.entries(refused)) assert.throws(() => allowed(path), /outside/, name);
});

test("traversal and encoding tricks are refused", () => {
  const refused = [
    `/quiz/${lobby}/data/../../${other}/data/logs`,
    `/quiz/${lobby}/data/../result`,
    `/quiz/${lobby}/data/./logs`,
    `/quiz/${lobby}/data/..`,
    `/quiz/${lobby}/data/%2e%2e/%2e%2e/${other}/data/logs`,
    `/quiz/${lobby}/data/%2E%2E/result`,
    `/quiz/${lobby}/data/.%2e/result`,
    `/quiz/${lobby}/data/..%2fresult`,
    `/quiz/${lobby}/data/%2fresult`,
    `/quiz/${lobby}/data/%5c..%5cresult`,
    `/quiz/${lobby}/data/%252e%252e/result`,
    `/quiz/${lobby}/data/\\..\\result`,
    `/quiz/${lobby}/data/logs\\..\\..\\result`,
    `/quiz/${lobby}/data/\t../result`,
    `/quiz/${lobby}/data/\n../result`,
    `/quiz/${lobby}/data/ logs`,
    `/quiz/${lobby}/data/logs\u0000`,
    `/quiz/${lobby}/data/logs#fragment`,
    `/quiz/${lobby}/data/lögs`,
    "/quiz/{lobby_id}/data/logs",
  ];
  for (const path of refused) assert.throws(() => allowed(path), Error, JSON.stringify(path));
});

test("other origins are refused", () => {
  const refused = [
    `https://evil.example/quiz/${lobby}/data/logs`,
    `${origin}/quiz/${lobby}/data/logs`,
    `//evil.example/quiz/${lobby}/data/logs`,
    `/\\evil.example/quiz/${lobby}/data/logs`,
    `\\\\evil.example/quiz/${lobby}/data/logs`,
    `/\t/evil.example/quiz/${lobby}/data/logs`,
    `javascript:alert(1)//quiz/${lobby}/data/`,
    `data:text/plain,/quiz/${lobby}/data/`,
    `quiz/${lobby}/data/logs`,
    `../quiz/${lobby}/data/logs`,
    "",
  ];
  for (const path of refused) assert.throws(() => allowed(path), Error, JSON.stringify(path));
  for (const path of [undefined, null, 4, { toString: () => `/quiz/${lobby}/data/logs` }, `/quiz/${lobby}/data/${"a".repeat(2048)}`]) {
    assert.throws(() => allowed(path), Error, String(path));
  }
});

test("a game without a prefix or a page without a lobby relays nothing", () => {
  assert.throws(() => relayURL("", lobby, `/quiz/${lobby}/data/logs`, origin), /no data/);
  assert.throws(() => relayURL(undefined, lobby, `/quiz/${lobby}/data/logs`, origin), /no data/);
  assert.throws(() => relayURL(prefix, "", "/quiz//data/logs", origin), /no lobby/);
});

function recordingFetch(response) {
  const calls = [];
  const fetch = async (url, options) => {
    calls.push({ url, options });
    if (response instanceof Error) throw response;
    return response;
  };
  return { calls, fetch };
}

const settings = (fetch) => ({ prefix, lobbyID: lobby, origin, credential: "secret-credential", fetch });

test("a relayed request is a GET with the credential, answered with the response alone", async () => {
  const { calls, fetch } = recordingFetch(Response.json({ lines: ["ok"] }, { status: 200 }));
  const answer = await relayRequest({ type: "game.request", id: 7, path: `/quiz/${lobby}/data/logs?round=1` }, settings(fetch));
  assert.deepEqual(answer, { type: "lobby.response", id: 7, status: 200, body: { lines: ["ok"] } });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, `${origin}/quiz/${lobby}/data/logs?round=1`);
  assert.equal(calls[0].options.method, "GET");
  assert.equal(calls[0].options.headers.Authorization, "Bearer secret-credential");
  assert.equal(calls[0].options.redirect, "error");
  assert.ok(!JSON.stringify(answer).includes("secret-credential"));
});

test("error statuses, problem JSON and text bodies are passed on", async () => {
  const problem = new Response(JSON.stringify({ status: 404, detail: "not found" }), { status: 404, headers: { "Content-Type": "application/problem+json" } });
  assert.deepEqual(await relayRequest({ type: "game.request", id: "a", path: `/quiz/${lobby}/data/x` }, settings(recordingFetch(problem).fetch)),
    { type: "lobby.response", id: "a", status: 404, body: { status: 404, detail: "not found" } });
  const text = new Response("plain", { status: 503, headers: { "Content-Type": "text/plain" } });
  assert.deepEqual(await relayRequest({ type: "game.request", id: "b", path: `/quiz/${lobby}/data/x` }, settings(recordingFetch(text).fetch)),
    { type: "lobby.response", id: "b", status: 503, body: "plain" });
  const failed = await relayRequest({ type: "game.request", id: "c", path: `/quiz/${lobby}/data/x` }, settings(recordingFetch(new TypeError("offline")).fetch));
  assert.deepEqual(failed, { type: "lobby.response", id: "c", status: 0, body: null, error: "the request failed" });
});

test("refused requests are answered without fetching", async () => {
  const { calls, fetch } = recordingFetch(Response.json({}));
  const outside = await relayRequest({ type: "game.request", id: 1, path: `/quiz/${other}/data/logs` }, settings(fetch));
  assert.equal(outside.status, 0);
  assert.match(outside.error, /outside/);
  for (const method of ["POST", "DELETE", "get", ""]) {
    const answer = await relayRequest({ type: "game.request", id: 2, method, path: `/quiz/${lobby}/data/logs` }, settings(fetch));
    assert.deepEqual(answer, { type: "lobby.response", id: 2, status: 0, body: null, error: "only GET requests are relayed" }, method);
  }
  assert.equal((await relayRequest({ type: "game.request", id: 3, method: "GET", path: `/quiz/${lobby}/data/logs` }, settings(fetch))).status, 200);
  assert.equal(calls.length, 1);
});

test("messages without a usable ID or of another type are ignored", async () => {
  const { calls, fetch } = recordingFetch(Response.json({}));
  const path = `/quiz/${lobby}/data/logs`;
  for (const data of [null, "game.request", { type: "game.request", path }, { type: "game.request", id: {}, path },
    { type: "game.request", id: Number.NaN, path }, { type: "game.request", id: "x".repeat(201), path }, { type: "game.result-size", id: 1, path }]) {
    assert.equal(await relayRequest(data, settings(fetch)), null, JSON.stringify(data));
  }
  assert.equal(calls.length, 0);
  assert.equal(refuseRequest({ type: "game.request", path }, "busy"), null);
  assert.deepEqual(refuseRequest({ type: "game.request", id: 5, path }, "busy"), { type: "lobby.response", id: 5, status: 0, body: null, error: "busy" });
});

// hangingFetch never answers until its request is aborted. With a body, it
// sends the headers at once and the body never ends.
function hangingFetch({ headers = false } = {}) {
  const aborted = (signal) => new Promise((_resolve, reject) => signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError"))));
  return (_url, { signal }) => {
    if (!headers) return aborted(signal);
    const body = new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode("{")); }, pull: () => aborted(signal) });
    return Promise.resolve(new Response(body, { headers: { "Content-Type": "application/json" } }));
  };
}

// settled reports whether a promise has settled by the time the microtasks
// that the mocked timers released have run.
async function settled(promise) {
  let done = false;
  promise.then(() => { done = true; }, () => { done = true; });
  for (let turn = 0; turn < 20; turn++) await Promise.resolve();
  return done;
}

test("a request that takes too long is aborted, also while its body arrives", async (context) => {
  context.mock.timers.enable({ apis: ["setTimeout"] });
  const timedOut = { type: "lobby.response", id: 1, status: 0, body: null, error: "the request timed out" };
  for (const options of [{}, { headers: true }]) {
    const answer = relayRequest({ type: "game.request", id: 1, path: `/quiz/${lobby}/data/logs` }, settings(hangingFetch(options)));
    context.mock.timers.tick(defaultTimeoutSeconds * 1000 - 1);
    assert.equal(await settled(answer), false, JSON.stringify(options));
    context.mock.timers.tick(1);
    assert.deepEqual(await answer, timedOut, JSON.stringify(options));
  }
});

test("a request may ask for a longer timeout, up to the maximum", async (context) => {
  context.mock.timers.enable({ apis: ["setTimeout"] });
  const path = `/quiz/${lobby}/data/demo`;
  const answer = relayRequest({ type: "game.request", id: 1, path, timeout_seconds: maxTimeoutSeconds }, settings(hangingFetch({ headers: true })));
  context.mock.timers.tick(maxTimeoutSeconds * 1000 - 1);
  assert.equal(await settled(answer), false);
  context.mock.timers.tick(1);
  assert.equal((await answer).error, "the request timed out");
  // A shorter time is allowed too, and a slow answer within it is passed on.
  const short = relayRequest({ type: "game.request", id: 2, path, timeout_seconds: 0.5 }, settings(hangingFetch()));
  context.mock.timers.tick(500);
  assert.equal((await short).error, "the request timed out");
  context.mock.timers.reset();
  const { calls, fetch } = recordingFetch(Response.json({ ok: true }));
  assert.deepEqual(await relayRequest({ type: "game.request", id: 3, path, timeout_seconds: 30 }, settings(fetch)), { type: "lobby.response", id: 3, status: 200, body: { ok: true } });
  // Anything else is refused without fetching.
  for (const timeout of [0, -1, maxTimeoutSeconds + 1, Number.NaN, Number.POSITIVE_INFINITY, "60", {}]) {
    const refused = await relayRequest({ type: "game.request", id: 4, path, timeout_seconds: timeout }, settings(fetch));
    assert.equal(refused.status, 0, String(timeout));
    assert.match(refused.error, /timeout_seconds/, String(timeout));
  }
  assert.equal(calls.length, 1);
});

test("a response body larger than the limit is refused, also without a length", async () => {
  const path = `/quiz/${lobby}/data/demo`;
  const limited = (response) => ({ ...settings(recordingFetch(response).fetch), maxBytes: 10 });
  const exact = await relayRequest({ type: "game.request", id: 1, path }, limited(new Response("0123456789", { headers: { "Content-Type": "text/plain" } })));
  assert.deepEqual(exact, { type: "lobby.response", id: 1, status: 200, body: "0123456789" });
  const tooLarge = { type: "lobby.response", id: 2, status: 0, body: null, error: "the response is larger than 10 bytes" };
  assert.deepEqual(await relayRequest({ type: "game.request", id: 2, path }, limited(new Response("0123456789a"))), tooLarge);
  // A streamed body without Content-Length is cut off once it passes the limit.
  let pulled = 0;
  const endless = new ReadableStream({ pull(controller) { pulled++; controller.enqueue(new TextEncoder().encode("abcd")); } });
  assert.deepEqual(await relayRequest({ type: "game.request", id: 2, path }, limited(new Response(endless))), tooLarge);
  assert.ok(pulled <= 4, `read ${pulled} chunks`);
  // Characters split across chunks are decoded whole.
  const split = new TextEncoder().encode("é".repeat(4));
  const chunked = new ReadableStream({ start(controller) { for (const byte of split) controller.enqueue(new Uint8Array([byte])); controller.close(); } });
  assert.equal((await relayRequest({ type: "game.request", id: 3, path }, limited(new Response(chunked)))).body, "éééé");
  // The default allows a game's large downloads.
  assert.equal(maxResponseBytes, 64 * 1024 * 1024);
});
