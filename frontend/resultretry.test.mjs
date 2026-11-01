import assert from "node:assert/strict";
import test from "node:test";
import { resultRetryDelay } from "./resultretry.mjs";

const delay = (failures, extra = {}) => resultRetryDelay({ phase: "started", loaded: false, failures, ...extra });

test("retries back off from one second and settle at ten", () => {
  assert.deepEqual([1, 2, 3, 4, 5, 6, 50].map((failures) => delay(failures)), [1000, 2000, 4000, 10000, 10000, 10000, 10000]);
});

test("a lobby that hasn't started isn't retried", () => {
  for (const phase of ["waiting", "finished", undefined]) assert.equal(delay(1, { phase }), null);
});

test("a loaded result stops the retries", () => {
  assert.equal(delay(1, { loaded: true }), null);
  assert.equal(delay(7, { loaded: true }), null);
});

test("a failure count that isn't a positive whole number gets no retry", () => {
  for (const failures of [0, -1, 1.5, NaN, undefined, "2"]) assert.equal(delay(failures), null);
});
