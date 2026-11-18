import { expect, test } from "bun:test";
import { Signal } from "../src/reconcile.js";

test("a signal keeps no listener of a waiter that stopped waiting", () => {
	const signal = new Signal();
	let calls = 0;
	for (let i = 0; i < 1000; i++) {
		const cancel = signal.onSet(() => {
			calls += 1;
		});
		cancel();
	}
	signal.set();
	expect(calls).toBe(0);
});

test("a signal calls a listener once, and at once when already set", () => {
	const signal = new Signal();
	let calls = 0;
	signal.onSet(() => {
		calls += 1;
	});
	signal.set();
	signal.set();
	expect(calls).toBe(1);
	signal.onSet(() => {
		calls += 1;
	});
	expect(calls).toBe(2);
	signal.clear();
	signal.onSet(() => {
		calls += 1;
	});
	expect(calls).toBe(2);
});
