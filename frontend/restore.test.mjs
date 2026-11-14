import assert from "node:assert/strict";
import test from "node:test";
import { lapsedMessage, membershipLapsed, notFoundMessage, restoreRoom } from "./restore.mjs";

const lobbyID = "4a0f1a52-7f4e-4d5c-9a8e-2f3b6c1d0e9a";
const joinCode = "ABCD2345";

function savedRoom(phase) {
  const room = { game_key: "example-game", lobby_id: lobbyID, join_code: joinCode, membership_generation: 1 };
  return phase === undefined ? room : { ...room, phase };
}

function snapshot(phase, id = lobbyID) {
  return { lobby: { id, game_key: "example-game", join_code: joinCode, phase }, members: [], teams: [] };
}

function apiError(code, status) {
  return Object.assign(new Error(`the lobby answered ${code}`), { code, status });
}

// The lobby as the page reaches it on a reload: read answers the saved room's
// snapshot with the tab's membership, join joins with the saved code, and
// leave leaves the lobby of a snapshot. Each records its calls.
function lobby({ read, join = () => snapshot("started"), leave = () => null }) {
  const calls = { read: 0, join: 0, leave: [] };
  return {
    calls,
    api: {
      read: async () => { calls.read++; return read(); },
      join: async () => { calls.join++; return join(); },
      leave: async (joined) => { calls.leave.push(joined); return leave(joined); },
    },
  };
}

const lapsed = () => { throw apiError("not_found", 404); };

test("a saved room whose read answers not_found says how to join again, with the code, not that no lobby was found", async () => {
  const { calls, api } = lobby({ read: lapsed });
  const restored = await restoreRoom(savedRoom("waiting"), api);
  assert.deepEqual(restored, { message: lapsedMessage, joinCode });
  assert.match(restored.message, /no longer connected to that lobby/);
  assert.match(restored.message, /Use its code to join again/);
  assert.doesNotMatch(restored.message, /No lobby found/);
  assert.deepEqual(calls, { read: 1, join: 0, leave: [] });
});

test("a stale membership reads like a lapsed one", async () => {
  const { calls, api } = lobby({ read: () => { throw apiError("stale_membership", 409); } });
  assert.deepEqual(await restoreRoom(savedRoom("waiting"), api), { message: lapsedMessage, joinCode });
  assert.equal(calls.join, 0);
});

test("a room that still has the tab's membership is shown without joining", async () => {
  const current = snapshot("waiting");
  const { calls, api } = lobby({ read: () => current });
  assert.deepEqual(await restoreRoom(savedRoom("waiting"), api), { snapshot: current });
  assert.deepEqual(calls, { read: 1, join: 0, leave: [] });
});

test("a started lobby is joined again on its own after the membership lapsed", async () => {
  const rejoined = snapshot("started");
  const { calls, api } = lobby({ read: lapsed, join: () => rejoined });
  const restored = await restoreRoom(savedRoom("started"), api);
  assert.deepEqual(restored, { snapshot: rejoined });
  assert.deepEqual(calls, { read: 1, join: 1, leave: [] });
});

test("a waiting lobby isn't joined again: the player does that", async () => {
  for (const phase of ["waiting", undefined]) {
    const { calls, api } = lobby({ read: lapsed });
    assert.deepEqual(await restoreRoom(savedRoom(phase), api), { message: lapsedMessage, joinCode }, String(phase));
    assert.equal(calls.join, 0, `a room saved with phase ${phase} is joined`);
  }
});

test("a started lobby that was reopened since is left again, and says how to join", async () => {
  const reopened = snapshot("waiting");
  for (const leave of [() => null, () => { throw apiError("unavailable", 503); }]) {
    const { calls, api } = lobby({ read: lapsed, join: () => reopened, leave });
    assert.deepEqual(await restoreRoom(savedRoom("started"), api), { message: lapsedMessage, joinCode });
    assert.deepEqual(calls.leave, [reopened]);
  }
});

test("a code that now names another lobby leaves that lobby, and finds none of the saved one", async () => {
  const other = snapshot("waiting", "9b1e2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d");
  const { calls, api } = lobby({ read: lapsed, join: () => other });
  assert.deepEqual(await restoreRoom(savedRoom("started"), api), { message: notFoundMessage, joinCode });
  assert.deepEqual(calls.leave, [other]);
});

test("a started lobby that doesn't let the player back in says that no lobby was found", async () => {
  const { calls, api } = lobby({ read: lapsed, join: () => { throw apiError("not_found", 404); } });
  assert.deepEqual(await restoreRoom(savedRoom("started"), api), { message: notFoundMessage, joinCode });
  assert.deepEqual(calls.leave, []);
});

test("a join that fails otherwise says how to join again", async () => {
  for (const failure of [apiError("unavailable", 503), apiError("lobby_full", 409), new Error("Cannot reach the lobby server.")]) {
    const { api } = lobby({ read: lapsed, join: () => { throw failure; } });
    assert.deepEqual(await restoreRoom(savedRoom("started"), api), { message: lapsedMessage, joinCode }, failure.message);
  }
});

test("an expired session and the read's other errors are the page's to report", async () => {
  const expired = apiError(undefined, 401);
  const unavailable = apiError("unavailable", 503);
  for (const [read, join, error] of [
    [() => { throw expired; }, undefined, expired],
    [() => { throw unavailable; }, undefined, unavailable],
    [lapsed, () => { throw expired; }, expired],
  ]) {
    const { calls, api } = lobby({ read, join });
    await assert.rejects(restoreRoom(savedRoom("started"), api), (thrown) => thrown === error);
    assert.deepEqual(calls.leave, []);
  }
});

test("only not_found and stale_membership say that a membership lapsed", () => {
  assert.equal(membershipLapsed(apiError("not_found", 404)), true);
  assert.equal(membershipLapsed(apiError("stale_membership", 409)), true);
  assert.equal(membershipLapsed(apiError("unavailable", 503)), false);
  assert.equal(membershipLapsed(apiError(undefined, 401)), false);
  assert.equal(membershipLapsed(undefined), false);
});
