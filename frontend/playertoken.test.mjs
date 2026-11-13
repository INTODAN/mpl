import { test } from "node:test";
import assert from "node:assert/strict";
import { createPlayerTokens, playerTokenFetch } from "./playertoken.mjs";

const start = Date.parse("2026-11-12T10:00:00Z");

// A fake lobby and game: the lobby issues numbered tokens that last 15
// minutes, and the game accepts the tokens in valid, unless refuseAll.
function fake() {
  const world = { now: start, issued: [], valid: new Set(), refuseAll: false, requests: [] };
  world.tokens = createPlayerTokens({
    now: () => world.now,
    issue: async (credential, gameKey) => {
      const token = `${gameKey}-${credential}-${world.issued.length + 1}`;
      world.issued.push({ credential, gameKey });
      world.valid.add(token);
      return { token, expires_at: new Date(world.now + 15 * 60_000).toISOString() };
    },
  });
  world.fetch = async (url, init) => {
    const authorization = init.headers.get("Authorization");
    world.requests.push({ url, authorization, body: init.body, accept: init.headers.get("Accept") });
    const valid = !world.refuseAll && world.valid.has(authorization.replace("Bearer ", ""));
    return new Response(valid ? "{}" : '{"status":401}', { status: valid ? 200 : 401 });
  };
  world.gameFetch = (credential = "guest", gameKey = "quiz") => playerTokenFetch({ tokens: world.tokens, credential, gameKey, fetch: world.fetch });
  return world;
}

test("a game request carries the game's player token, which is reused while fresh", async () => {
  const world = fake();
  const fetchGame = world.gameFetch();
  assert.equal((await fetchGame("/quiz/1/state", { headers: { Accept: "text/html" } })).status, 200);
  assert.equal((await fetchGame("/quiz/1/state")).status, 200);
  assert.deepEqual(world.issued, [{ credential: "guest", gameKey: "quiz" }]);
  assert.deepEqual(world.requests.map((request) => request.authorization), ["Bearer quiz-guest-1", "Bearer quiz-guest-1"]);
  // The caller's own headers stay.
  assert.equal(world.requests[0].accept, "text/html");
});

test("a token is renewed once 80 % of its life has passed", async () => {
  const world = fake();
  const fetchGame = world.gameFetch();
  await fetchGame("/quiz/1/state");
  world.now = start + 12 * 60_000 - 1;
  await fetchGame("/quiz/1/state");
  assert.equal(world.issued.length, 1);
  world.now = start + 12 * 60_000;
  await fetchGame("/quiz/1/state");
  await fetchGame("/quiz/1/state");
  assert.equal(world.issued.length, 2);
  assert.deepEqual(world.requests.map((request) => request.authorization).slice(2), ["Bearer quiz-guest-2", "Bearer quiz-guest-2"]);
});

test("a refused token is renewed once, and the request sent again with its body", async () => {
  const world = fake();
  const fetchGame = world.gameFetch();
  await fetchGame("/quiz/1/state");
  // The game stops accepting the token early, as after a key rotation.
  world.valid.clear();
  const response = await fetchGame("/quiz/1/answer", { method: "PUT", body: '{"pick":2}' });
  assert.equal(response.status, 200);
  assert.deepEqual(world.requests.slice(1).map((request) => [request.authorization, request.body]),
    [["Bearer quiz-guest-1", '{"pick":2}'], ["Bearer quiz-guest-2", '{"pick":2}']]);
  // A token refused again is the caller's 401, after one more token.
  world.refuseAll = true;
  assert.equal((await fetchGame("/quiz/1/state")).status, 401);
  assert.equal(world.issued.length, 3);
});

test("each game and session has its own token, and callers at once share one request", async () => {
  const world = fake();
  await Promise.all([world.gameFetch()("/a"), world.gameFetch()("/b"), world.gameFetch()("/c")]);
  assert.equal(world.issued.length, 1);
  await world.gameFetch("guest", "chess")("/chess/1");
  await world.gameFetch("other-guest")("/quiz/1");
  assert.deepEqual(world.issued.map((issued) => `${issued.gameKey} ${issued.credential}`), ["quiz guest", "chess guest", "quiz other-guest"]);
});

test("a failure to get a token rejects the request, and the next one asks again", async () => {
  let calls = 0;
  const tokens = createPlayerTokens({
    issue: async () => {
      calls++;
      if (calls === 1) throw Object.assign(new Error("authentication required"), { status: 401 });
      return { token: "t", expires_at: new Date(Date.now() + 60_000).toISOString() };
    },
  });
  const fetchGame = playerTokenFetch({ tokens, credential: "guest", gameKey: "quiz", fetch: async () => new Response("{}") });
  await assert.rejects(fetchGame("/quiz/1"), /authentication required/);
  assert.equal((await fetchGame("/quiz/1")).status, 200);
  assert.equal(calls, 2);
});
