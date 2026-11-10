import assert from "node:assert/strict";
import test from "node:test";
import { reopenMessage, resultURL } from "./rounds.mjs";

test("the result URL takes the lobby ID and, when it asks for it, the round", () => {
  assert.equal(resultURL("/g/{lobby_id}/replay?round={round}", { id: "a b", round: 2 }), "/g/a%20b/replay?round=2");
  assert.equal(resultURL("/g/{lobby_id}/replay", { id: "l", round: 3 }), "/g/l/replay");
});

test("a refused reopen explains itself", () => {
  assert.match(reopenMessage({ code: "game_running" }), /still running/);
  assert.match(reopenMessage({ code: "phase_conflict" }), /can’t be opened again/);
  assert.equal(reopenMessage({ code: "unavailable" }), null);
});
