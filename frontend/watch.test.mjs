import assert from "node:assert/strict";
import test from "node:test";
import { relayURL } from "./relay.mjs";
import { canWatch, kickedMessage, relayPrefix, steadyWatchDelayMs, wasKicked, watchMessage, watchRelayPrefix, watchResultURL, watchRetryDelay, watchersChanged } from "./watch.mjs";

const origin = "https://play.example";
const lobby = "4a0f1a52-7f4e-4d5c-9a8e-2f3b6c1d0e9a";
const quiz = {
  key: "quiz", result_url: "/quiz/{lobby_id}/result", data_url_prefix: "/quiz/{lobby_id}/",
  watch_result_url: "/quiz/{lobby_id}/watch/result", watch_data_url_prefix: "/quiz/{lobby_id}/watch/",
};

test("a game offers watching when it names a watcher document", () => {
  assert.equal(canWatch(quiz), true);
  assert.equal(canWatch({ key: "chess", result_url: "/chess/{lobby_id}" }), false);
  assert.equal(canWatch(undefined), false);
});

test("a watcher's frame reads the watcher prefix, a player's the player prefix", () => {
  assert.equal(relayPrefix(quiz, true), "/quiz/{lobby_id}/watch/");
  assert.equal(relayPrefix(quiz, false), "/quiz/{lobby_id}/");
});

test("a watcher whose game names no watcher prefix reads nothing, and never the player prefix", () => {
  const game = { key: "chess", result_url: "/chess/{lobby_id}", data_url_prefix: "/chess/{lobby_id}/" };
  assert.equal(watchRelayPrefix(game), null);
  assert.equal(relayPrefix(game, true), null);
  assert.equal(relayPrefix(undefined, true), null);
  assert.throws(() => relayURL(relayPrefix(game, true), lobby, `/chess/${lobby}/logs`, origin), /no data/);
});

test("the relay lets a watcher's frame call the watcher routes only", () => {
  const prefix = relayPrefix(quiz, true);
  assert.equal(relayURL(prefix, lobby, `/quiz/${lobby}/watch/demo`, origin), `${origin}/quiz/${lobby}/watch/demo`);
  for (const path of [
    `/quiz/${lobby}/logs?round=1`, `/quiz/${lobby}/demo`, `/quiz/${lobby}/result`, `/quiz/${lobby}/watch`, `/quiz/${lobby}/watcher/demo`,
    `/quiz/${lobby}/watch/../demo`, `/quiz/${lobby}/watch/%2e%2e/demo`, "/lobby/v1/connection-token",
    "/quiz/9b1e2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d/watch/demo",
  ]) assert.throws(() => relayURL(prefix, lobby, path, origin), Error, path);
});

test("a watcher's document URL names the lobby", () => {
  assert.equal(watchResultURL(quiz.watch_result_url, lobby), `/quiz/${lobby}/watch/result`);
});

test("the wait for a watcher's document grows, then holds", () => {
  assert.deepEqual([1, 2, 3, 4, 50].map(watchRetryDelay), [1000, 2000, 4000, steadyWatchDelayMs, steadyWatchDelayMs]);
  assert.equal(watchRetryDelay(0), steadyWatchDelayMs);
  assert.equal(watchRetryDelay(NaN), steadyWatchDelayMs);
});

test("the lobby's watcher errors have sentences, and a removed watcher is known", () => {
  assert.equal(watchMessage({ code: "watcher_kicked" }), kickedMessage);
  assert.match(watchMessage({ code: "not_found" }), /No lobby found/);
  assert.match(watchMessage({ code: "watchers_full" }), /as many watchers/);
  assert.equal(watchMessage({ code: "unavailable" }), null);
  assert.equal(watchMessage(undefined), null);
  assert.equal(wasKicked({ code: "watcher_kicked", status: 403 }), true);
  assert.equal(wasKicked({ code: "not_found", status: 404 }), false);
  assert.equal(wasKicked(null), false);
});

test("the watchers are read again when the lobby changes and someone watches", () => {
  assert.equal(watchersChanged({ lastVersion: 3, version: 4, watcherCount: 1, shownCount: 0 }), true);
  assert.equal(watchersChanged({ lastVersion: 3, version: 4, watcherCount: 0, shownCount: 2 }), true, "the last one left");
  assert.equal(watchersChanged({ lastVersion: 3, version: 4, watcherCount: 0, shownCount: 0 }), false);
  assert.equal(watchersChanged({ lastVersion: 4, version: 4, watcherCount: 1, shownCount: 1 }), false, "same version");
});
