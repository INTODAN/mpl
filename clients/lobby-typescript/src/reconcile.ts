/** Bounded per-scope reconciliation state, independent of the socket transport. */
import { CompatibilityError, type LobbyError, MembershipError } from "./errors.js";
import type { Scope, Snapshot } from "./types.js";

export function scopeKey(scope: Scope): string {
	return `${scope.gameKey}/${scope.lobbyId}`;
}

/** A one-shot event: once set, every waiter, present and future, resumes. */
export class Latch {
	#set = false;
	#resolve!: () => void;
	readonly promise = new Promise<void>((resolve) => {
		this.#resolve = resolve;
	});

	get isSet(): boolean {
		return this.#set;
	}

	set(): void {
		this.#set = true;
		this.#resolve();
	}
}

/**
 * A resettable event, like asyncio.Event. Waiters register a listener and
 * remove it when they stop waiting, so a long-lived signal holds none of
 * their state.
 */
export class Signal {
	#set = false;
	readonly #listeners = new Set<() => void>();

	set(): void {
		this.#set = true;
		const listeners = [...this.#listeners];
		this.#listeners.clear();
		for (const listener of listeners) listener();
	}

	clear(): void {
		this.#set = false;
	}

	/** Calls listener once the signal is set, at once if it is. Returns a function that cancels it. */
	onSet(listener: () => void): () => void {
		if (this.#set) {
			listener();
			return () => {};
		}
		this.#listeners.add(listener);
		return () => {
			this.#listeners.delete(listener);
		};
	}
}

type Item = Snapshot | LobbyError;

/** One reader of a scope's snapshots. It keeps only the newest unread item. */
export class Consumer {
	#item: Item | undefined;
	#waiter: ((item: Item) => void) | undefined;

	put(item: Item): void {
		if (this.#waiter) {
			const waiter = this.#waiter;
			this.#waiter = undefined;
			waiter(item);
		} else {
			this.#item = item;
		}
	}

	next(): Promise<Item> {
		if (this.#item !== undefined) {
			const item = this.#item;
			this.#item = undefined;
			return Promise.resolve(item);
		}
		return new Promise((resolve) => {
			this.#waiter = resolve;
		});
	}
}

/** The realtime SDK's subscription, as far as the client uses it. */
export interface RealtimeSubscription {
	on(event: "subscribed" | "subscribing" | "unsubscribed", listener: () => void): unknown;
	on(event: "publication", listener: (context: { data: unknown }) => void): unknown;
	subscribe(): void;
	unsubscribe(): void;
	ready(timeout?: number): Promise<void>;
}

export class ScopeState {
	subscription: RealtimeSubscription | undefined;
	accepted: Snapshot | undefined;
	targetVersion = 0;
	dirty = false;
	acknowledged = false;
	everAcknowledged = false;
	resubscribe = false;
	stopped = false;
	error: LobbyError | undefined;
	retryAt = 0;
	failures = 0;
	readonly initial = new Latch();
	readonly terminal = new Latch();
	readonly consumers = new Set<Consumer>();

	constructor(
		readonly scope: Scope,
		readonly channel: string,
		readonly generation: number,
	) {}

	get acceptedVersion(): number {
		return this.accepted ? this.accepted.lobby.version : 0;
	}

	/** Accepts a newer snapshot of this membership. Returns whether it was newer. */
	install(snapshot: Snapshot): boolean {
		if (
			snapshot.lobby.game_key !== this.scope.gameKey ||
			snapshot.lobby.id !== this.scope.lobbyId ||
			snapshot.channel !== this.channel
		) {
			throw new CompatibilityError("snapshot_scope_mismatch");
		}
		if (snapshot.membership_generation !== this.generation) {
			// Only an explicit join acquires a newer generation's lease.
			throw new MembershipError("stale_membership", 409);
		}
		if (snapshot.lobby.version <= this.acceptedVersion) return false;
		this.accepted = snapshot;
		for (const consumer of this.consumers) consumer.put(snapshot);
		return true;
	}

	fail(error: LobbyError): void {
		if (this.stopped) return;
		this.stopped = true;
		this.error = error;
		this.dirty = false;
		this.initial.set();
		this.terminal.set();
		for (const consumer of this.consumers) consumer.put(error);
	}
}
