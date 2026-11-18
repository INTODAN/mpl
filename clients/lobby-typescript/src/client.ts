/**
 * The player API's generated types over fetch, plus the official Centrifugo
 * SDK. No identity, lease, connection or background work is created by the
 * constructor.
 */
import { Centrifuge, UnauthorizedError } from "centrifuge";
import createFetchClient from "openapi-fetch";
import {
	AuthenticationError,
	ClientClosedError,
	CompatibilityError,
	LobbyError,
	MembershipError,
	responseError,
	TransientError,
} from "./errors.js";
import type { paths } from "./generated/openapi.js";
import {
	Consumer,
	Latch,
	type RealtimeSubscription,
	ScopeState,
	Signal,
	scopeKey,
} from "./reconcile.js";
import {
	type ConnectionToken,
	type GameSettings,
	type GuestSession,
	type PlayerToken,
	type Scope,
	type Snapshot,
	scopeOf,
	type TeamDefinition,
	type WatchAccess,
	type Watcher,
} from "./types.js";

/** The realtime SDK's client, as far as the lobby client uses it. */
export interface RealtimeClient {
	on(event: "connected" | "connecting" | "disconnected", listener: () => void): unknown;
	connect(): void;
	disconnect(): void;
	newSubscription(
		channel: string,
		options: { recoverable: boolean; positioned: boolean },
	): RealtimeSubscription;
	removeSubscription(subscription: RealtimeSubscription): void;
}

/** Builds the realtime client; by default, the official Centrifugo SDK. */
export type RealtimeFactory = (
	websocketUrl: string,
	options: { token: string; getToken: () => Promise<string> },
) => RealtimeClient;

/** Waits ms milliseconds, or less when signal aborts. */
export type Sleep = (ms: number, signal: AbortSignal) => Promise<void>;

export interface LobbyClientOptions {
	/** A guest credential from an earlier createSession, to resume that player. */
	credential?: string;
	/** The fetch to call the lobby with; the global fetch by default. */
	fetch?: (request: Request) => Promise<Response>;
	/** Builds the realtime client; the Centrifugo SDK by default. */
	realtime?: RealtimeFactory;
	sleep?: Sleep;
	/** A monotonic clock in milliseconds. */
	clock?: () => number;
	/** How long each HTTP request may take, in milliseconds. Default 10000. */
	requestTimeout?: number;
	/** How long close() waits for each release, in milliseconds. Default 2000. */
	releaseTimeout?: number;
}

const HEARTBEAT_INTERVAL = 15_000;

const defaultRealtime: RealtimeFactory = (websocketUrl, options) =>
	new Centrifuge(websocketUrl, options) as unknown as RealtimeClient;

const defaultSleep: Sleep = (ms, signal) =>
	new Promise((resolve) => {
		if (signal.aborted) return resolve();
		const timer = setTimeout(done, ms);
		function done() {
			clearTimeout(timer);
			signal.removeEventListener("abort", done);
			resolve();
		}
		signal.addEventListener("abort", done, { once: true });
	});

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

function uuid(value: string, name: string): string {
	const normalized = value.toLowerCase();
	if (!UUID.test(normalized)) throw new TypeError(`${name} must be a UUID`);
	return normalized;
}

/** JSON with sorted keys, so equal requests compare equal. */
function canonicalJSON(value: unknown): string {
	if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(",")}]`;
	if (value !== null && typeof value === "object") {
		const entries = Object.entries(value)
			.filter(([, item]) => item !== undefined)
			.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
		return `{${entries.map(([key, item]) => `${JSON.stringify(key)}:${canonicalJSON(item)}`).join(",")}}`;
	}
	return JSON.stringify(value);
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return value !== null && typeof value === "object" && !Array.isArray(value);
}

function checkSnapshot(value: unknown): Snapshot {
	const lobby = isRecord(value) ? value.lobby : undefined;
	if (
		!isRecord(value) ||
		!isRecord(lobby) ||
		typeof lobby.id !== "string" ||
		typeof lobby.game_key !== "string" ||
		typeof lobby.version !== "number" ||
		typeof value.channel !== "string" ||
		typeof value.membership_generation !== "number"
	) {
		throw new CompatibilityError("invalid_http_response");
	}
	return value as unknown as Snapshot;
}

interface FetchResult<T> {
	data?: T;
	error?: unknown;
	response: Response;
}

interface RequestInit {
	headers: Record<string, string>;
	signal: AbortSignal;
}

export class LobbyClient {
	/** The guest credential; keep it to resume this player in a later client. */
	credential: string | undefined;
	/** This client's instance, which owns its leases. */
	readonly clientInstanceId: string = crypto.randomUUID();

	readonly #http;
	readonly #realtime: RealtimeFactory;
	readonly #sleep: Sleep;
	readonly #clock: () => number;
	readonly #requestTimeout: number;
	readonly #releaseTimeout: number;
	#sdk: RealtimeClient | undefined;
	#sdkPending: Promise<void> | undefined;
	#connected = false;
	#closed = false;
	#authError: AuthenticationError | undefined;
	readonly #states = new Map<string, ScopeState>();
	readonly #leases = new Map<string, { scope: Scope; generation: number }>();
	readonly #dirty = new Set<string>();
	readonly #wakeup = new Signal();
	readonly #closing = new Latch();
	readonly #abort = new AbortController();
	readonly #tasks = new Set<Promise<void>>();
	#closePromise: Promise<void> | undefined;
	readonly #createPayloads = new Map<string, string>();

	constructor(baseUrl: string, options: LobbyClientOptions = {}) {
		this.credential = options.credential;
		this.#http = createFetchClient<paths>({
			baseUrl: baseUrl.replace(/\/+$/, ""),
			...(options.fetch ? { fetch: options.fetch } : {}),
		});
		this.#realtime = options.realtime ?? defaultRealtime;
		this.#sleep = options.sleep ?? defaultSleep;
		this.#clock = options.clock ?? (() => performance.now());
		this.#requestTimeout = options.requestTimeout ?? 10_000;
		this.#releaseTimeout = options.releaseTimeout ?? 2_000;
	}

	#checkOpen(): void {
		if (this.#closed) throw new ClientClosedError();
	}

	#requireAuth(): void {
		this.#checkOpen();
		if (this.#authError) throw this.#authError;
		if (!this.credential) throw new AuthenticationError("missing_credential", 401);
	}

	#spawn(work: Promise<void>): void {
		const task = work.then(
			() => {},
			(error: unknown) => {
				if (this.#closed) return;
				const failure = error instanceof LobbyError ? error : new LobbyError("background_failure");
				for (const state of this.#states.values()) this.#fail(state, failure);
			},
		);
		this.#tasks.add(task);
		void task.finally(() => this.#tasks.delete(task));
	}

	async #request<T>(
		call: (init: RequestInit) => Promise<FetchResult<T>>,
		{ authenticated = true, signal }: { authenticated?: boolean; signal?: AbortSignal } = {},
	): Promise<T> {
		if (authenticated && !this.credential) {
			throw new AuthenticationError("missing_credential", 401);
		}
		const headers: Record<string, string> = this.credential
			? { Authorization: `Bearer ${this.credential}` }
			: {};
		let result: FetchResult<T>;
		try {
			result = await call({
				headers,
				signal:
					signal ??
					AbortSignal.any([this.#abort.signal, AbortSignal.timeout(this.#requestTimeout)]),
			});
		} catch (error) {
			if (error instanceof SyntaxError) throw new CompatibilityError("invalid_http_response");
			if (this.#closed && !signal) throw new ClientClosedError();
			throw new TransientError("network_error");
		}
		const status = result.response.status;
		if (status >= 400) {
			const problem = isRecord(result.error) ? result.error : {};
			const code =
				typeof problem.code === "string" && problem.code ? problem.code : `http_${status}`;
			const error = responseError(
				status,
				code,
				typeof problem.detail === "string" ? problem.detail : undefined,
				typeof problem.type === "string" ? problem.type : undefined,
			);
			if (error instanceof AuthenticationError) {
				this.#authError = error;
				this.#connected = false;
				for (const state of this.#states.values()) this.#fail(state, error);
				if (this.#sdk && !this.#closed) this.#sdk.disconnect();
			}
			throw error;
		}
		if (status !== 204 && result.data === undefined) {
			throw new CompatibilityError("invalid_http_response");
		}
		return result.data as T;
	}

	/** Creates a guest player and keeps its credential in `credential`. */
	async createSession(displayName: string): Promise<GuestSession> {
		this.#checkOpen();
		if (this.credential) throw new LobbyError("session_already_set");
		const session = await this.#request(
			(init) =>
				this.#http.POST("/lobby/v1/sessions", { ...init, body: { display_name: displayName } }),
			{ authenticated: false },
		);
		this.credential = session.access_token;
		this.#authError = undefined;
		return session;
	}

	async #connectionToken(): Promise<ConnectionToken> {
		this.#requireAuth();
		return await this.#request((init) =>
			this.#http.POST("/lobby/v1/connection-token", { ...init, body: {} }),
		);
	}

	/**
	 * Issues a player token for one game: a short-lived JWT that the game
	 * verifies with the lobby's public keys. Send it to the game's API as
	 * `Authorization: Bearer <token>` and get a new one before `expires_at`.
	 */
	async issuePlayerToken(gameKey: string): Promise<PlayerToken> {
		this.#requireAuth();
		return await this.#request((init) =>
			this.#http.POST("/lobby/v1/player-tokens", { ...init, body: { game_key: gameKey } }),
		);
	}

	async #getToken(): Promise<string> {
		try {
			return (await this.#connectionToken()).token;
		} catch (error) {
			if (error instanceof AuthenticationError) throw new UnauthorizedError(error.code);
			throw error;
		}
	}

	async #ensureSdk(): Promise<void> {
		this.#requireAuth();
		if (this.#sdk) return;
		this.#sdkPending ??= this.#openSdk().finally(() => {
			this.#sdkPending = undefined;
		});
		await this.#sdkPending;
	}

	async #openSdk(): Promise<void> {
		const token = await this.#connectionToken();
		this.#checkOpen();
		const sdk = this.#realtime(token.websocket_url, {
			token: token.token,
			getToken: () => this.#getToken(),
		});
		sdk.on("connected", () => {
			this.#connected = !this.#closed && this.#authError === undefined;
		});
		sdk.on("connecting", () => {
			this.#connected = false;
		});
		sdk.on("disconnected", () => {
			this.#connected = false;
		});
		this.#sdk = sdk;
		this.#spawn(this.#worker());
		this.#spawn(this.#ticker());
		sdk.connect();
	}

	/**
	 * Creates a lobby and follows it. Each team holds one member, so the lobby
	 * holds `teams.length` members.
	 *
	 * `gameSettings` is the game's own JSON object; the host can change it,
	 * with the teams, while the lobby waits (changeSettings). `requestId` makes
	 * a retry safe: the same request again returns the same lobby.
	 */
	async createLobby(request: {
		gameKey: string;
		name: string;
		teams: TeamDefinition[];
		requestId: string;
		gameSettings?: GameSettings;
	}): Promise<Snapshot> {
		this.#requireAuth();
		const body = {
			game_key: request.gameKey,
			name: request.name,
			teams: request.teams,
			request_id: uuid(request.requestId, "requestId"),
			client_instance_id: this.clientInstanceId,
			...(request.gameSettings ? { game_settings: request.gameSettings } : {}),
		};
		const fingerprint: Record<string, unknown> = { ...body };
		if (!request.gameSettings || Object.keys(request.gameSettings).length === 0) {
			// The server treats omitted and empty settings as the same request.
			delete fingerprint.game_settings;
		}
		const payload = canonicalJSON(fingerprint);
		const previous = this.#createPayloads.get(body.request_id);
		if (previous !== undefined && previous !== payload) {
			throw new LobbyError("idempotency_conflict", 409);
		}
		this.#createPayloads.set(body.request_id, payload);
		const result = await this.#request((init) =>
			this.#http.POST("/lobby/v1/lobbies", { ...init, body }),
		);
		return await this.#attach(result);
	}

	/** Joins a lobby with its join code and follows it. */
	async joinLobby(request: { gameKey: string; joinCode: string }): Promise<Snapshot> {
		this.#requireAuth();
		const result = await this.#request((init) =>
			this.#http.POST("/lobby/v1/memberships", {
				...init,
				body: {
					game_key: request.gameKey,
					join_code: request.joinCode,
					client_instance_id: this.clientInstanceId,
				},
			}),
		);
		return await this.#attach(result);
	}

	async #attach(value: unknown): Promise<Snapshot> {
		const result = checkSnapshot(value);
		this.#checkOpen();
		const scope = scopeOf(result);
		const key = scopeKey(scope);
		this.#leases.set(key, { scope, generation: result.membership_generation });
		const old = this.#states.get(key);
		if (old) {
			this.#fail(old, new MembershipError("membership_replaced"));
			this.#removeSubscription(old);
		}
		const state = new ScopeState(scope, result.channel, result.membership_generation);
		this.#states.set(key, state);
		try {
			await this.#ensureSdk();
			const sdk = this.#sdk;
			if (!sdk) throw new ClientClosedError();
			const subscription = sdk.newSubscription(result.channel, {
				recoverable: true,
				positioned: true,
			});
			state.subscription = subscription;
			this.#listen(state, subscription);
			subscription.subscribe();
			await this.#awaitReady(state);
			await state.initial.promise;
			if (state.error) throw state.error;
			if (!state.accepted) throw new ClientClosedError();
			return state.accepted;
		} catch (error) {
			const failure =
				error instanceof LobbyError ? error : new MembershipError("synchronization_failed");
			this.#fail(state, failure);
			if (!(error instanceof LobbyError)) this.#removeSubscription(state);
			throw error;
		}
	}

	#listen(state: ScopeState, subscription: RealtimeSubscription): void {
		subscription.on("subscribed", () => {
			if (!this.#current(state)) return;
			state.acknowledged = true;
			state.everAcknowledged = true;
			state.resubscribe = false;
			this.#signal(state);
		});
		subscription.on("subscribing", () => {
			if (this.#current(state)) state.acknowledged = false;
		});
		subscription.on("unsubscribed", () => {
			if (!this.#current(state)) return;
			state.acknowledged = false;
			state.resubscribe = true;
			this.#signal(state);
		});
		subscription.on("publication", (context) => {
			// Event handlers never await HTTP or SDK work and never throw into the SDK.
			if (!this.#current(state)) return;
			try {
				let data = context.data;
				if (typeof data === "string") data = JSON.parse(data);
				if (data instanceof Uint8Array) data = JSON.parse(new TextDecoder().decode(data));
				if (!isRecord(data)) throw new Error("invalid event");
				if (data.game_key !== state.scope.gameKey || data.lobby_id !== state.scope.lobbyId) {
					return;
				}
				if (data.schema_version !== 1) throw new Error("unsupported schema");
				if (
					data.type !== "lobby.changed" ||
					typeof data.version !== "number" ||
					!Number.isInteger(data.version) ||
					data.version < 1 ||
					typeof data.event_id !== "string"
				) {
					throw new Error("invalid event");
				}
				this.#signal(state, data.version);
			} catch {
				this.#fail(state, new CompatibilityError("invalidation_schema"));
			}
		});
	}

	async #awaitReady(state: ScopeState): Promise<void> {
		while (this.#current(state)) {
			const subscription = state.subscription;
			if (!subscription) break;
			const outcome = await Promise.race([
				subscription.ready().then(
					() => ({ ready: true as const }),
					(error: unknown) => ({ ready: false as const, error }),
				),
				state.terminal.promise.then(() => undefined),
			]);
			if (state.error) throw state.error;
			if (outcome?.ready) return;
			// A server denial is classified by the worker's authorized read.
			state.resubscribe = true;
			this.#signal(state);
			await this.#pause(100);
		}
		if (state.error) throw state.error;
		throw new ClientClosedError();
	}

	#current(state: ScopeState): boolean {
		return !this.#closed && !state.stopped && this.#states.get(scopeKey(state.scope)) === state;
	}

	#signal(state: ScopeState, version = 0): void {
		if (!this.#current(state)) return;
		state.targetVersion = Math.max(state.targetVersion, version);
		state.dirty = true;
		this.#dirty.add(scopeKey(state.scope));
		this.#wakeup.set();
	}

	#fail(state: ScopeState, error: LobbyError): void {
		state.fail(error);
		const key = scopeKey(state.scope);
		if (this.#states.get(key) === state) this.#dirty.delete(key);
		this.#wakeup.set();
	}

	/** Waits ms milliseconds, or until the client closes. */
	async #pause(ms: number, wakeOn?: Promise<void>): Promise<void> {
		const controller = new AbortController();
		const stop = () => controller.abort();
		void this.#closing.promise.then(stop);
		void wakeOn?.then(stop);
		await this.#sleep(ms, controller.signal);
		controller.abort();
	}

	static #path(scope: Scope) {
		return { game_key: scope.gameKey, lobby_id: scope.lobbyId };
	}

	#state(scope: Scope): ScopeState {
		this.#requireAuth();
		const state = this.#states.get(scopeKey(scope));
		if (!state) throw new MembershipError("not_joined");
		if (state.error) throw state.error;
		return state;
	}

	async #read(scope: Scope): Promise<Snapshot> {
		return checkSnapshot(
			await this.#request((init) =>
				this.#http.GET("/lobby/v1/games/{game_key}/lobbies/{lobby_id}", {
					...init,
					params: { path: LobbyClient.#path(scope) },
				}),
			),
		);
	}

	async #worker(): Promise<void> {
		while (!this.#closed) {
			this.#wakeup.clear();
			const now = this.#clock();
			const candidates = [...this.#dirty]
				.map((key) => this.#states.get(key))
				.filter((state): state is ScopeState => state !== undefined && state.retryAt <= now);
			for (const state of candidates) {
				const key = scopeKey(state.scope);
				if (!this.#current(state) || (!state.everAcknowledged && !state.resubscribe)) {
					this.#dirty.delete(key);
					continue;
				}
				this.#dirty.delete(key);
				state.dirty = false;
				try {
					const result = await this.#read(state.scope);
					if (!this.#current(state)) continue;
					state.install(result);
					if (state.resubscribe) {
						// A stale revocation can unsubscribe a newer lease; reauthorize first.
						state.resubscribe = false;
						state.subscription?.subscribe();
						if (state.resubscribe) {
							this.#retry(state);
							continue;
						}
					} else if (state.acknowledged && state.acceptedVersion >= state.targetVersion) {
						state.initial.set();
					}
					if (result.lobby.version < state.targetVersion) {
						this.#retry(state);
					} else {
						state.failures = 0;
						state.retryAt = 0;
						if (state.dirty) this.#signal(state);
					}
				} catch (error) {
					if (error instanceof TransientError) {
						this.#retry(state);
					} else if (error instanceof LobbyError) {
						if (this.#current(state)) this.#fail(state, error);
					} else {
						throw error;
					}
				}
			}
			if (this.#closed) break;
			const delays = [...this.#dirty]
				.map((key) => this.#states.get(key))
				.filter((state): state is ScopeState => state !== undefined)
				.map((state) => Math.max(0, state.retryAt - this.#clock()));
			if (delays.length > 0) {
				await this.#pause(Math.min(...delays), this.#wakeup.wait());
			} else {
				await Promise.race([this.#wakeup.wait(), this.#closing.promise]);
			}
		}
	}

	#retry(state: ScopeState): void {
		if (!this.#current(state)) return;
		state.failures += 1;
		state.retryAt = this.#clock() + Math.min(100 * 2 ** Math.min(state.failures - 1, 7), 10_000);
		this.#signal(state);
	}

	async #ticker(): Promise<void> {
		let deadline = this.#clock() + HEARTBEAT_INTERVAL;
		while (!this.#closed) {
			await this.#pause(Math.max(0, deadline - this.#clock()));
			if (this.#closed) break;
			deadline = this.#clock() + HEARTBEAT_INTERVAL;
			await this.#tick();
		}
	}

	async #tick(): Promise<void> {
		for (const state of [...this.#states.values()]) {
			if (!this.#current(state)) continue;
			if (state.everAcknowledged) this.#signal(state);
			if (!this.#connected) continue;
			try {
				const result = await this.#request((init) =>
					this.#http.POST("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/heartbeat", {
						...init,
						params: { path: LobbyClient.#path(state.scope) },
						body: {
							client_instance_id: this.clientInstanceId,
							membership_generation: state.generation,
						},
					}),
				);
				if (this.#current(state)) {
					if (result.membership_generation !== state.generation) {
						throw new MembershipError("stale_membership", 409);
					}
					if (result.version > state.acceptedVersion) this.#signal(state, result.version);
				}
			} catch (error) {
				// A transient failure: the next heartbeat retries this same lease.
				if (error instanceof TransientError) continue;
				if (!(error instanceof LobbyError)) throw error;
				if (this.#current(state)) this.#fail(state, error);
			}
		}
	}

	/** Reads the lobby now. The client also keeps it current by itself. */
	async snapshot(scope: Scope): Promise<Snapshot> {
		return await this.#command(scope, () => this.#read(scope));
	}

	async #command(scope: Scope, call: () => Promise<unknown>): Promise<Snapshot> {
		const state = this.#state(scope);
		try {
			const result = checkSnapshot(await call());
			if (!this.#current(state)) throw state.error ?? new MembershipError("membership_replaced");
			state.install(result);
			if (!state.accepted) throw new CompatibilityError("invalid_http_response");
			return state.accepted;
		} catch (error) {
			if (
				error instanceof MembershipError ||
				error instanceof AuthenticationError ||
				error instanceof CompatibilityError
			) {
				this.#fail(state, error);
			}
			throw error;
		}
	}

	/** Chooses a team, or none with null. */
	async selectTeam(scope: Scope, teamId: string | null): Promise<Snapshot> {
		return await this.#command(scope, () =>
			this.#request((init) =>
				this.#http.PUT("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/team", {
					...init,
					params: { path: LobbyClient.#path(scope) },
					body: { team_id: teamId === null ? null : uuid(teamId, "teamId") },
				}),
			),
		);
	}

	async setReady(scope: Scope, ready: boolean): Promise<Snapshot> {
		return await this.#command(scope, () =>
			this.#request((init) =>
				this.#http.PUT("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/ready", {
					...init,
					params: { path: LobbyClient.#path(scope) },
					body: { ready },
				}),
			),
		);
	}

	/** Starts the game. Only the host can, once the lobby's start rules hold. */
	async start(scope: Scope): Promise<Snapshot> {
		return await this.#command(scope, () =>
			this.#request((init) =>
				this.#http.POST("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/start", {
					...init,
					params: { path: LobbyClient.#path(scope) },
					body: {},
				}),
			),
		);
	}

	/**
	 * Brings a started lobby whose game has ended back to waiting.
	 *
	 * Any active person in the lobby can, to play another game in it. The
	 * lobby's round goes up by one; members, teams, seats, the game settings
	 * and member settings stay, every person's ready is cleared and the start
	 * roster is emptied. A lobby that already waits comes back unchanged, so
	 * calling it again is safe. While the game runs it throws LobbyError with
	 * code game_running; for a closed lobby, or a game that doesn't play again,
	 * phase_conflict.
	 */
	async reopen(scope: Scope): Promise<Snapshot> {
		return await this.#command(scope, () =>
			this.#request((init) =>
				this.#http.POST("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/reopen", {
					...init,
					params: { path: LobbyClient.#path(scope) },
				}),
			),
		);
	}

	/**
	 * Replaces the lobby's game settings and teams while it waits.
	 *
	 * Only the host can. The rules are a create request's: the game checks
	 * gameSettings against the new teams, and LobbyError with code
	 * invalid_game_settings carries its message. The teams are rebuilt by
	 * position: the lobby's first team keeps its ID and takes the first name,
	 * and so on; a person on a removed team loses their team and a seat on one
	 * is removed. Every person's ready is cleared. A change that leaves more
	 * members than teams throws LobbyError with code lobby_full.
	 */
	async changeSettings(
		scope: Scope,
		change: { teams: TeamDefinition[]; gameSettings?: GameSettings },
	): Promise<Snapshot> {
		return await this.#command(scope, () =>
			this.#request((init) =>
				this.#http.PUT("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/settings", {
					...init,
					params: { path: LobbyClient.#path(scope) },
					body: {
						teams: change.teams,
						...(change.gameSettings ? { game_settings: change.gameSettings } : {}),
					},
				}),
			),
		);
	}

	/**
	 * Adds a seat that no person plays, such as the game's bot, to a team.
	 *
	 * Only the host can, while the lobby waits. `seatSettings` is the game's
	 * own JSON object for the seat; settings the game rejects throw LobbyError
	 * with code invalid_seat_settings and the game's message. The seat is a
	 * member of kind seat in the returned snapshot.
	 *
	 * `requestId` makes a retry safe: after a transport failure, call again
	 * with the same requestId, team and settings and the lobby returns the
	 * snapshot without a second seat; other settings or another team with the
	 * same requestId throw LobbyError with code idempotency_conflict. The
	 * client generates one when omitted, and doesn't retry by itself.
	 */
	async addSeat(
		scope: Scope,
		seat: { teamId: string; seatSettings: GameSettings; requestId?: string },
	): Promise<Snapshot> {
		const body = {
			request_id: uuid(seat.requestId ?? crypto.randomUUID(), "requestId"),
			team_id: uuid(seat.teamId, "teamId"),
			seat_settings: seat.seatSettings,
		};
		return await this.#command(scope, () =>
			this.#request((init) =>
				this.#http.POST("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/seats", {
					...init,
					params: { path: LobbyClient.#path(scope) },
					body,
				}),
			),
		);
	}

	/**
	 * Sets your own settings for the lobby's game while it waits.
	 *
	 * `memberSettings` is the game's own JSON object for you, such as what
	 * plays for you; the snapshot shows every member's. Settings the game
	 * rejects throw LobbyError with code invalid_member_settings and the game's
	 * message, and a started lobby throws phase_conflict. New settings clear
	 * your ready; the same settings again change nothing.
	 */
	async setMemberSettings(scope: Scope, memberSettings: GameSettings): Promise<Snapshot> {
		return await this.#command(scope, () =>
			this.#request((init) =>
				this.#http.PUT("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/member-settings", {
					...init,
					params: { path: LobbyClient.#path(scope) },
					body: { member_settings: memberSettings },
				}),
			),
		);
	}

	/** Removes a seat that the host added, by its member ID. */
	async removeSeat(scope: Scope, seatId: string): Promise<Snapshot> {
		return await this.#command(scope, () =>
			this.#request((init) =>
				this.#http.DELETE("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/seats/{seat_id}", {
					...init,
					params: { path: { ...LobbyClient.#path(scope), seat_id: uuid(seatId, "seatId") } },
				}),
			),
		);
	}

	/**
	 * Watches a lobby with its join code, without taking a seat.
	 *
	 * Returns the lobby's ID and its watch channel, where the lobby's game
	 * publishes what watchers may see; the game documents its own watcher
	 * reads. A watcher holds no lease and needs no heartbeat, so this client
	 * doesn't follow the lobby: subscribe to the channel with the Centrifugo
	 * SDK. For an active member of the lobby, `member` is true and no watcher
	 * row is added. A wrong code, or one of another game, throws not_found; a
	 * watcher the host kicked throws watcher_kicked, and a new watcher beyond
	 * the lobby's limit watchers_full.
	 */
	async watch(request: { gameKey: string; joinCode: string }): Promise<WatchAccess> {
		this.#requireAuth();
		return await this.#request((init) =>
			this.#http.POST("/lobby/v1/watches", {
				...init,
				body: { game_key: request.gameKey, join_code: request.joinCode },
			}),
		);
	}

	/** Stops watching a lobby. A kicked watcher stays kicked. */
	async unwatch(scope: Scope): Promise<void> {
		this.#requireAuth();
		await this.#request((init) =>
			this.#http.DELETE("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/watch", {
				...init,
				params: { path: LobbyClient.#path(scope) },
			}),
		);
	}

	/** Lists the lobby's watchers that the host hasn't kicked, for its members. */
	async watchers(scope: Scope): Promise<Watcher[]> {
		this.#requireAuth();
		const result = await this.#request((init) =>
			this.#http.GET("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/watchers", {
				...init,
				params: { path: LobbyClient.#path(scope) },
			}),
		);
		return [...(result.watchers ?? [])];
	}

	/**
	 * Lets the host remove a watcher, who is unsubscribed at once and can't
	 * watch again with the join code.
	 */
	async kickWatcher(scope: Scope, playerId: string): Promise<void> {
		this.#requireAuth();
		await this.#request((init) =>
			this.#http.DELETE("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/watchers/{player_id}", {
				...init,
				params: { path: { ...LobbyClient.#path(scope), player_id: uuid(playerId, "playerId") } },
			}),
		);
	}

	/**
	 * Yields the lobby's newest snapshot, first the current one and then each
	 * newer one. A slow reader skips to the newest. It throws the error that
	 * stopped the client from following the lobby, such as a MembershipError
	 * once the player leaves.
	 */
	updates(scope: Scope): AsyncGenerator<Snapshot, never, undefined> {
		const state = this.#state(scope);
		const consumer = new Consumer();
		state.consumers.add(consumer);
		if (state.accepted) consumer.put(state.accepted);
		return (async function* () {
			try {
				while (true) {
					const item = await consumer.next();
					if (item instanceof LobbyError) throw item;
					yield item;
				}
			} finally {
				state.consumers.delete(consumer);
			}
		})();
	}

	/** Leaves the lobby: the membership ends, not just this client's lease. */
	async leave(scope: Scope): Promise<void> {
		const state = this.#state(scope);
		const key = scopeKey(scope);
		this.#fail(state, new MembershipError("left_membership"));
		try {
			await this.#request((init) =>
				this.#http.DELETE("/lobby/v1/games/{game_key}/lobbies/{lobby_id}/membership", {
					...init,
					params: { path: LobbyClient.#path(scope) },
				}),
			);
			this.#leases.delete(key);
		} finally {
			this.#removeSubscription(state);
			if (this.#states.get(key) === state) {
				this.#states.delete(key);
				this.#dirty.delete(key);
			}
		}
	}

	#removeSubscription(state: ScopeState): void {
		const subscription = state.subscription;
		if (!subscription) return;
		state.subscription = undefined;
		try {
			subscription.unsubscribe();
		} catch {}
		try {
			this.#sdk?.removeSubscription(subscription);
		} catch {}
	}

	/**
	 * Releases this client's leases, so the other members see it disconnected
	 * at once, and stops following its lobbies. The memberships stay: rejoin
	 * with the same credential to come back. Calling it again is safe.
	 */
	close(): Promise<void> {
		if (!this.#closePromise) {
			this.#closed = true;
			this.#connected = false;
			for (const state of this.#states.values()) this.#fail(state, new ClientClosedError());
			this.#closing.set();
			this.#wakeup.set();
			this.#abort.abort();
			this.#closePromise = this.#cleanup();
		}
		return this.#closePromise;
	}

	async #cleanup(): Promise<void> {
		await Promise.allSettled([...this.#tasks]);
		await this.#sdkPending?.catch(() => {});
		await Promise.allSettled(
			[...this.#leases.values()].map(({ scope, generation }) =>
				this.#request(
					(init) =>
						this.#http.DELETE(
							"/lobby/v1/games/{game_key}/lobbies/{lobby_id}/leases/{client_instance_id}",
							{
								...init,
								params: {
									path: { ...LobbyClient.#path(scope), client_instance_id: this.clientInstanceId },
									query: { generation },
								},
							},
						),
					{ signal: AbortSignal.timeout(this.#releaseTimeout) },
				),
			),
		);
		for (const state of this.#states.values()) this.#removeSubscription(state);
		try {
			this.#sdk?.disconnect();
		} catch {}
		this.#leases.clear();
		this.#dirty.clear();
	}
}
