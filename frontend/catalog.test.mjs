import assert from "node:assert/strict";
import test from "node:test";
import { catalogURL, gameURL, locate, resolvingFetch } from "./catalog.mjs";

const page = "https://lobby.example.org";
const remote = { key: "quiz", origin: "https://quiz.example.org" };
const local = { key: "chess" };

test("a game's URL is its origin, or the lobby page's for a game of paths", () => {
  assert.equal(gameURL(remote, page), "https://quiz.example.org");
  assert.equal(gameURL(local, page), page);
  assert.equal(gameURL(undefined, page), page);
});

test("a path is on the lobby page's origin, an absolute URL on the game's", () => {
  assert.deepEqual(locate(local, "/quiz/{lobby_id}/data/", page), { origin: page, path: "/quiz/{lobby_id}/data/" });
  assert.deepEqual(locate(remote, "https://quiz.example.org/quiz/{lobby_id}/data/", page),
    { origin: "https://quiz.example.org", path: "/quiz/{lobby_id}/data/" });
  // A path stays on the page's origin even for a game with its own.
  assert.deepEqual(locate(remote, "/quiz/rounds/", page), { origin: page, path: "/quiz/rounds/" });
  assert.equal(catalogURL(remote, "https://quiz.example.org/r/{lobby_id}?round={round}", page), "https://quiz.example.org/r/{lobby_id}?round={round}");
  assert.equal(catalogURL(local, "/r/{lobby_id}", page), `${page}/r/{lobby_id}`);
});

test("a URL on any other origin is refused", () => {
  for (const value of ["https://evil.example.org/quiz/", "https://quiz.example.org.evil.example/x", "//quiz.example.org/x", "", null, 7]) {
    assert.equal(locate(remote, value, page), null, String(value));
  }
  assert.equal(locate(local, "https://quiz.example.org/x", page), null);
});

test("a resolving fetch sends paths to the game's origin and other URLs as they are", async () => {
  const sent = [];
  const fetch = resolvingFetch(async (url, init) => { sent.push([url, init]); return "answer"; }, "https://quiz.example.org");
  assert.equal(await fetch("/generation/commands", { method: "POST" }), "answer");
  await fetch("http://127.0.0.1:41414/turn");
  await fetch("//other.example/x");
  assert.deepEqual(sent, [
    ["https://quiz.example.org/generation/commands", { method: "POST" }],
    ["http://127.0.0.1:41414/turn", undefined],
    ["//other.example/x", undefined],
  ]);
});
