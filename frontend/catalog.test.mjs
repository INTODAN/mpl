import assert from "node:assert/strict";
import test from "node:test";
import { catalogURL, examplePlayerHeading, gameURL, locate, resolvingFetch, seatTexts } from "./catalog.mjs";

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

// Island Battles' entry names its seats; a game without bots names none.
const withSeats = { key: "island-battles", seat_names: { singular: "a bot", plural: "bots" }, example_player: { strategy: "One soldier", heading: "Your bot" } };
const withoutSeats = { key: "mock-game" };

test("a game's seat names are the page's words for its seats, as before for a game with bots", () => {
  assert.deepEqual(seatTexts(withSeats), {
    unavailable: "Choose a bot that can be added.",
    tooFewTeams: "These settings have fewer teams than the lobby has players and bots. Remove a bot, or choose settings with more teams.",
    reopen: "Once the game has ended, this opens the lobby again for the next game, with the same players, teams and bots.",
  });
});

test("a game that names no seats gets texts without any", () => {
  for (const game of [withoutSeats, undefined]) {
    const texts = seatTexts(game);
    assert.deepEqual(texts, {
      unavailable: "Choose a seat that can be added.",
      tooFewTeams: "These settings have fewer teams than the lobby has players. Choose settings with more teams.",
      reopen: "Once the game has ended, this opens the lobby again for the next game, with the same players and teams.",
    });
    for (const text of Object.values(texts)) assert.doesNotMatch(text, /\bbots?\b/i);
  }
});

test("the example player's panel is headed by the demo's strategy, or by the game's heading", () => {
  assert.equal(examplePlayerHeading(withSeats, true), "Example strategy · ");
  assert.equal(examplePlayerHeading(withSeats, false), "Your bot");
  assert.equal(examplePlayerHeading({ example_player: { strategy: "First answer" } }, false), "");
  assert.equal(examplePlayerHeading(withoutSeats, false), "");
});
