import {
	LobbyClient,
	type RealtimeClient,
	type RealtimeSubscription,
	type Scope,
	type Snapshot,
} from "../src/index.js";

export const LOBBY = "11111111-1111-4111-8111-111111111111";
export const OTHER = "22222222-2222-4222-8222-222222222222";
export const TEAM = "33333333-3333-4333-8333-333333333333";
export const SCOPE: Scope = { gameKey: "quiz-demo", lobbyId: LOBBY };

export function snapshot(version = 1, generation = 1, lobbyId = LOBBY): Snapshot {
	return {
		channel: `lobby:quiz-demo:${lobbyId}`,
		membership_generation: generation,
		members: [],
		teams: [],
		start_roster: [],
		start_rules: { every_team_needs_member: false },
		watcher_count: 0,
		lobby: {
			id: lobbyId,
			game_key: "quiz-demo",
			version,
			capacity: 4,
			created_at: "2026-09-20T00:00:00Z",
			host_player_id: null,
			name: "Room",
			join_code: "ABCD",
			phase: "waiting",
			round: 1,
			game_settings: {},
		},
	} as Snapshot;
}

export function json(status: number, body: unknown): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
	});
}

export function problem(status: number, code: string, extra: Record<string, unknown> = {}) {
	return json(status, { code, status, title: code, detail: code, ...extra });
}

/** Lets pending promises and the client's loops run. */
export async function settle(rounds = 20): Promise<void> {
	for (let i = 0; i < rounds; i++) await new Promise((resolve) => setTimeout(resolve, 0));
}

export async function until(predicate: () => boolean): Promise<void> {
	for (let i = 0; i < 200; i++) {
		if (predicate()) return;
		await new Promise((resolve) => setTimeout(resolve, 0));
	}
	if (!predicate()) throw new Error("condition did not become true");
}

export class FakeTime {
	now = 0;
	waiters: { at: number; resolve: () => void }[] = [];

	clock = () => this.now;

	sleep = (ms: number, signal: AbortSignal): Promise<void> =>
		new Promise((resolve) => {
			if (ms <= 0 || signal.aborted) return resolve();
			const waiter = {
				at: this.now + ms,
				resolve: () => {
					this.waiters = this.waiters.filter((item) => item !== waiter);
					resolve();
				},
			};
			this.waiters.push(waiter);
			signal.addEventListener("abort", waiter.resolve, { once: true });
		});

	async advance(ms: number): Promise<void> {
		this.now += ms;
		for (const waiter of [...this.waiters]) if (waiter.at <= this.now) waiter.resolve();
		await settle();
	}
}

type Listener = (context: { data: unknown }) => void;

class Emitter {
	listeners = new Map<string, Listener[]>();

	on(event: string, listener: Listener): this {
		this.listeners.set(event, [...(this.listeners.get(event) ?? []), listener]);
		return this;
	}

	emit(event: string, context: { data: unknown } = { data: undefined }): void {
		for (const listener of this.listeners.get(event) ?? []) listener(context);
	}
}

export class FakeSubscription extends Emitter implements RealtimeSubscription {
	calls = 0;
	acknowledged = false;
	#waiters: (() => void)[] = [];

	constructor(
		readonly sdk: FakeSDK,
		readonly channel: string,
		readonly options: unknown,
	) {
		super();
	}

	subscribe(): void {
		this.calls += 1;
		if (this.sdk.autoAck) this.ack();
	}

	ack(): void {
		this.acknowledged = true;
		for (const waiter of this.#waiters.splice(0)) waiter();
		this.emit("subscribed");
	}

	ready(): Promise<void> {
		if (this.acknowledged) return Promise.resolve();
		return new Promise((resolve) => this.#waiters.push(resolve));
	}

	unsubscribe(): void {
		this.acknowledged = false;
		this.emit("unsubscribed");
	}

	publish(version = 1, changes: Record<string, unknown> = {}): void {
		this.emit("publication", {
			data: {
				schema_version: 1,
				type: "lobby.changed",
				event_id: "event",
				game_key: "quiz-demo",
				lobby_id: this.channel.split(":").at(-1),
				version,
				...changes,
			},
		});
	}
}

export class FakeSDK extends Emitter implements RealtimeClient {
	autoAck = true;
	subscriptions: FakeSubscription[] = [];
	disconnected = false;

	constructor(
		readonly address: string,
		readonly options: { token: string; getToken: () => Promise<string> },
	) {
		super();
	}

	connect(): void {
		this.emit("connected");
	}

	newSubscription(channel: string, options: { recoverable: boolean; positioned: boolean }) {
		const subscription = new FakeSubscription(this, channel, options);
		this.subscriptions.push(subscription);
		return subscription;
	}

	removeSubscription(subscription: RealtimeSubscription): void {
		this.subscriptions = this.subscriptions.filter((item) => item !== subscription);
	}

	disconnect(): void {
		this.disconnected = true;
		this.emit("disconnected");
	}
}

export interface Call {
	method: string;
	path: string;
	search: string;
	authorization: string | null;
	body: unknown;
}

export class Harness {
	time = new FakeTime();
	calls: Call[] = [];
	version = 1;
	generation = 1;
	gets = 0;
	hook: ((call: Call) => Response | undefined | Promise<Response | undefined>) | undefined;
	sdks: FakeSDK[] = [];
	/** Whether new subscriptions are acknowledged as soon as they subscribe. */
	autoAck = true;
	client: LobbyClient;

	constructor(credential: string | null = "retained-secret") {
		this.client = new LobbyClient("http://test/", {
			...(credential ? { credential } : {}),
			fetch: (request) => this.handle(request),
			realtime: (address, options) => {
				const sdk = new FakeSDK(address, options);
				sdk.autoAck = this.autoAck;
				this.sdks.push(sdk);
				return sdk;
			},
			sleep: this.time.sleep,
			clock: this.time.clock,
		});
	}

	get sdk(): FakeSDK {
		const sdk = this.sdks[0];
		if (!sdk) throw new Error("no SDK yet");
		return sdk;
	}

	get sub(): FakeSubscription {
		const sub = this.sdk.subscriptions[0];
		if (!sub) throw new Error("no subscription yet");
		return sub;
	}

	callsTo(suffix: string, method?: string): Call[] {
		return this.calls.filter(
			(call) => call.path.endsWith(suffix) && (method === undefined || call.method === method),
		);
	}

	async handle(request: Request): Promise<Response> {
		const url = new URL(request.url);
		const text = await request.text();
		const call: Call = {
			method: request.method,
			path: url.pathname,
			search: url.search,
			authorization: request.headers.get("Authorization"),
			body: text ? JSON.parse(text) : undefined,
		};
		this.calls.push(call);
		if (this.hook) {
			const response = await this.hook(call);
			if (response) return response;
		}
		const path = call.path;
		if (path.endsWith("/connection-token")) {
			return json(200, {
				token: "connection-jwt",
				websocket_url: "ws://test/ws",
				expires_at: "2026-09-20T00:05:00Z",
			});
		}
		if (path.endsWith("/sessions")) {
			return json(201, {
				player_id: LOBBY,
				access_token: "new-secret",
				expires_at: "2026-09-21T00:00:00Z",
			});
		}
		if (call.method === "DELETE" && !path.includes("/seats/"))
			return new Response(null, { status: 204 });
		if (path.endsWith("/heartbeat")) {
			return json(200, {
				version: this.version,
				membership_generation: this.generation,
				lease_expires_at: "2026-09-20T00:00:45Z",
				disconnected_until: null,
			});
		}
		if (call.method === "GET") this.gets += 1;
		const lobbyId = path.includes(OTHER) ? OTHER : LOBBY;
		return json(
			path.endsWith("/lobbies") ? 201 : 200,
			snapshot(this.version, this.generation, lobbyId),
		);
	}

	join(): Promise<Snapshot> {
		return this.client.joinLobby({ gameKey: "quiz-demo", joinCode: "ABCD" });
	}
}
