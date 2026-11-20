import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { UnauthorizedError } from "centrifuge";
import {
	AuthenticationError,
	ClientClosedError,
	CompatibilityError,
	LobbyClient,
	LobbyError,
	MembershipError,
	TransientError,
} from "../src/index.js";
import {
	type Call,
	Harness,
	json,
	LOBBY,
	OTHER,
	problem,
	SCOPE,
	settle,
	snapshot,
	TEAM,
	until,
} from "./harness.js";

let h: Harness;

beforeEach(() => {
	h = new Harness();
});

afterEach(async () => {
	await h.client.close();
});

describe("following a lobby", () => {
	test("join returns the first snapshot after the subscription is acknowledged", async () => {
		const result = await h.join();
		expect(result.lobby.id).toBe(LOBBY);
		expect(h.sdk.address).toBe("ws://test/ws");
		expect(h.sdk.options.token).toBe("connection-jwt");
		// The channel is authorized by the connection; subscriptions carry no token.
		expect(h.sub.options).toEqual({ recoverable: true, positioned: true });
		const [join] = h.callsTo("/memberships");
		expect(join?.body).toEqual({
			game_key: "quiz-demo",
			join_code: "ABCD",
			client_instance_id: h.client.clientInstanceId,
		});
		expect(join?.authorization).toBe("Bearer retained-secret");
	});

	test("an event makes the client read the newer snapshot", async () => {
		await h.join();
		const updates = h.client.updates(SCOPE);
		expect((await updates.next()).value?.lobby.version).toBe(1);
		h.version = 2;
		h.sub.publish(2);
		expect((await updates.next()).value?.lobby.version).toBe(2);
	});

	test("duplicate and out-of-order events read once each", async () => {
		await h.join();
		const before = h.gets;
		h.version = 3;
		h.sub.publish(3);
		h.sub.publish(2);
		h.sub.publish(3);
		await until(() => h.gets > before);
		await settle();
		expect(h.gets - before).toBeLessThanOrEqual(2);
		expect((await h.client.snapshot(SCOPE)).lobby.version).toBe(3);
	});

	test("an event of another lobby is ignored and an unknown schema stops the scope", async () => {
		await h.join();
		const before = h.gets;
		h.sub.publish(5, { lobby_id: OTHER });
		await settle();
		expect(h.gets).toBe(before);
		h.sub.publish(5, { schema_version: 2 });
		await settle();
		expect(() => h.client.updates(SCOPE)).toThrow(CompatibilityError);
		// A stopped scope leaves its channel.
		expect(h.sdk.subscriptions).toHaveLength(0);
	});

	test("a slow reader skips to the newest snapshot", async () => {
		await h.join();
		const updates = h.client.updates(SCOPE);
		await updates.next();
		for (const version of [2, 3, 4]) {
			h.version = version;
			h.sub.publish(version);
			await until(() => h.gets >= version);
		}
		expect((await updates.next()).value?.lobby.version).toBe(4);
	});

	test("two lobbies are followed apart", async () => {
		await h.join();
		h.hook = (call) =>
			call.path.endsWith("/memberships") ? json(200, snapshot(1, 1, OTHER)) : undefined;
		const other = await h.client.joinLobby({ gameKey: "quiz-demo", joinCode: "EFGH" });
		expect(other.lobby.id).toBe(OTHER);
		expect(h.sdk.subscriptions.map((sub) => sub.channel)).toEqual([
			`lobby:quiz-demo:${LOBBY}`,
			`lobby:quiz-demo:${OTHER}`,
		]);
	});

	test("a server unsubscribe is reauthorized by a read before resubscribing", async () => {
		await h.join();
		const before = h.gets;
		const sub = h.sub;
		let readsAtResubscribe = -1;
		const subscribe = sub.subscribe.bind(sub);
		sub.subscribe = () => {
			readsAtResubscribe = h.gets;
			subscribe();
		};
		sub.unsubscribe();
		await until(() => sub.calls === 2);
		expect(readsAtResubscribe).toBe(before + 1);
	});

	test("an unexpected failure stops one scope, and later joins still sync", async () => {
		await h.join();
		const sub = h.sub;
		sub.subscribe = () => {
			throw new TypeError("SDK failure");
		};
		sub.unsubscribe();
		await until(() => h.sdk.subscriptions.length === 0);
		await expect(h.client.snapshot(SCOPE)).rejects.toThrow("background_failure");
		h.hook = (call) =>
			call.path.endsWith("/memberships") ? json(200, snapshot(1, 1, OTHER)) : undefined;
		const other = await h.client.joinLobby({ gameKey: "quiz-demo", joinCode: "EFGH" });
		expect(other.lobby.id).toBe(OTHER);
	});

	test("a realtime client that can't connect fails each join instead of hanging", async () => {
		h.failConnect = true;
		await expect(h.join()).rejects.toThrow("realtime_unavailable");
		await expect(h.join()).rejects.toThrow("realtime_unavailable");
		h.failConnect = false;
		expect((await h.join()).lobby.id).toBe(LOBBY);
	});

	test("two joins of one lobby at once leave the newer one following it", async () => {
		const [first, second] = await Promise.allSettled([h.join(), h.join()]);
		expect(first.status).toBe("rejected");
		expect(second.status).toBe("fulfilled");
		expect((await h.client.snapshot(SCOPE)).lobby.id).toBe(LOBBY);
		expect(h.sdk.subscriptions).toHaveLength(1);
	});

	test("periodic reconcile reads even while disconnected", async () => {
		await h.join();
		h.sdk.emit("disconnected");
		const before = h.gets;
		await h.time.advance(15_000);
		await until(() => h.gets === before + 1);
		expect(h.callsTo("/heartbeat")).toHaveLength(0);
	});
});

describe("heartbeats", () => {
	test("are sent only while connected, for this client's lease", async () => {
		await h.join();
		h.sdk.emit("connecting");
		await h.time.advance(15_000);
		expect(h.callsTo("/heartbeat")).toHaveLength(0);
		h.sdk.emit("connected");
		await h.time.advance(15_000);
		const heartbeats = h.callsTo("/heartbeat");
		expect(heartbeats).toHaveLength(1);
		expect(heartbeats[0]?.body).toEqual({
			client_instance_id: h.client.clientInstanceId,
			membership_generation: 1,
		});
	});

	test("a newer version is read, and a stale membership stops the scope", async () => {
		await h.join();
		h.version = 7;
		await h.time.advance(15_000);
		await until(() => h.gets >= 2);
		expect((await h.client.snapshot(SCOPE)).lobby.version).toBe(7);
		h.hook = (call) =>
			call.path.endsWith("/heartbeat") ? problem(409, "stale_membership") : undefined;
		await h.time.advance(15_000);
		await expect(h.client.snapshot(SCOPE)).rejects.toBeInstanceOf(MembershipError);
		expect(h.callsTo("/memberships")).toHaveLength(1);
	});
});

describe("tokens and errors", () => {
	test("a refused connection token is terminal", async () => {
		await h.join();
		h.hook = (call) =>
			call.path.endsWith("/connection-token") ? problem(401, "unauthenticated") : undefined;
		await expect(h.sdk.options.getToken()).rejects.toBeInstanceOf(UnauthorizedError);
		await expect(h.client.snapshot(SCOPE)).rejects.toBeInstanceOf(AuthenticationError);
	});

	test("an unavailable connection token is transient", async () => {
		await h.join();
		h.hook = (call) =>
			call.path.endsWith("/connection-token") ? problem(503, "unavailable") : undefined;
		await expect(h.sdk.options.getToken()).rejects.toBeInstanceOf(TransientError);
		expect((await h.client.snapshot(SCOPE)).lobby.version).toBe(1);
	});

	test.each([
		[401, "unauthenticated", AuthenticationError],
		[404, "not_found", MembershipError],
	])("a %i on a read ends the updates", async (status, code, kind) => {
		await h.join();
		const updates = h.client.updates(SCOPE);
		await updates.next();
		h.hook = (call) => (call.method === "GET" ? problem(status, code) : undefined);
		h.sub.publish(2);
		await expect(updates.next()).rejects.toBeInstanceOf(kind);
	});

	test("transient reads back off and recover", async () => {
		await h.join();
		let failures = 0;
		h.hook = (call) => {
			if (call.method === "GET" && failures < 2) {
				failures += 1;
				return problem(503, "unavailable");
			}
			return undefined;
		};
		h.version = 2;
		h.sub.publish(2);
		await until(() => failures === 1);
		await h.time.advance(100);
		await until(() => failures === 2);
		await h.time.advance(200);
		const updates = h.client.updates(SCOPE);
		expect((await updates.next()).value?.lobby.version).toBe(2);
	});

	test("the game's message and problem type reach the caller", async () => {
		await h.join();
		h.hook = (call) =>
			call.path.endsWith("/ready")
				? problem(409, "game_requirement", {
						detail: "Choose a bot first",
						type: "https://game.example/problems/choose-bot",
					})
				: undefined;
		const error = await h.client.setReady(SCOPE, true).catch((caught) => caught);
		expect(error).toBeInstanceOf(LobbyError);
		expect(error.code).toBe("game_requirement");
		expect(error.detail).toBe("Choose a bot first");
		expect(error.problemType).toBe("https://game.example/problems/choose-bot");
		// The client keeps following the lobby.
		expect((await h.client.snapshot(SCOPE)).lobby.version).toBe(1);
	});

	test("other errors carry no server text", async () => {
		await h.join();
		h.hook = (call) =>
			call.path.endsWith("/start")
				? problem(409, "not_ready", { detail: "internal words" })
				: undefined;
		const error = await h.client.start(SCOPE).catch((caught) => caught);
		expect(error.code).toBe("not_ready");
		expect(error.detail).toBeUndefined();
	});

	test("a network failure is transient", async () => {
		const client = new LobbyClient("http://test", {
			credential: "secret",
			fetch: () => Promise.reject(new TypeError("offline")),
		});
		await expect(client.issuePlayerToken("quiz-demo")).rejects.toBeInstanceOf(TransientError);
		await client.close();
	});

	test("a base URL that isn't absolute http(s) is refused at once", () => {
		expect(() => new LobbyClient("lobby.example.org")).toThrow(TypeError);
		expect(() => new LobbyClient("ftp://lobby.example.org")).toThrow(TypeError);
		expect(() => new LobbyClient("https://lobby.example.org/")).not.toThrow();
	});

	test("a malformed success is a compatibility error", async () => {
		h.hook = (call) => (call.path.endsWith("/memberships") ? json(200, { lobby: {} }) : undefined);
		await expect(h.join()).rejects.toBeInstanceOf(CompatibilityError);
	});
});

describe("sessions", () => {
	test("a new session sends no bearer header and keeps the credential", async () => {
		const fresh = new Harness(null);
		await expect(fresh.join()).rejects.toBeInstanceOf(AuthenticationError);
		const session = await fresh.client.createSession("Ada");
		expect(session.access_token).toBe("new-secret");
		expect(fresh.client.credential).toBe("new-secret");
		const [call] = fresh.callsTo("/sessions");
		expect(call?.authorization).toBeNull();
		expect(call?.body).toEqual({ display_name: "Ada" });
		await expect(fresh.client.createSession("Ada")).rejects.toThrow("session_already_set");
		await fresh.client.close();
	});

	test("a player token is issued for one game", async () => {
		h.hook = (call) =>
			call.path.endsWith("/player-tokens")
				? json(200, { token: "player-jwt", expires_at: "2026-09-20T00:10:00Z" })
				: undefined;
		const token = await h.client.issuePlayerToken("quiz-demo");
		expect(token.token).toBe("player-jwt");
		expect(h.callsTo("/player-tokens")[0]?.body).toEqual({ game_key: "quiz-demo" });
	});
});

describe("accounts", () => {
	const account = {
		id: "66666666-6666-4666-8666-666666666666",
		display_name: "Dana",
		created_at: "2026-11-20T12:00:00Z",
	};

	test("signing in with Google keeps the session and manages its account", async () => {
		const fresh = new Harness(null);
		fresh.hook = (call) => {
			if (call.path.endsWith("/sign-in")) return json(200, { google_client_id: "client-id" });
			if (call.path.endsWith("/sessions/google")) {
				const body = call.body as { display_name?: string };
				if (body.display_name === undefined) return problem(422, "display_name_required");
				return json(201, {
					player_id: LOBBY,
					access_token: "signed-in-secret",
					expires_at: "2026-11-21T12:00:00Z",
					account,
				});
			}
			if (call.path.endsWith("/account")) {
				if (call.method === "DELETE") return new Response(null, { status: 204 });
				if (call.method === "PUT") return json(200, { ...account, ...(call.body as object) });
				return json(200, account);
			}
			return undefined;
		};
		expect((await fresh.client.signInSettings()).google_client_id).toBe("client-id");
		await expect(fresh.client.signInWithGoogle("id-token")).rejects.toMatchObject({
			code: "display_name_required",
			status: 422,
		});
		const session = await fresh.client.signInWithGoogle("id-token", "Dana");
		expect(session.account.display_name).toBe("Dana");
		expect(fresh.client.credential).toBe("signed-in-secret");
		const signIns = fresh.callsTo("/sessions/google");
		expect(signIns.map((call) => [call.authorization, call.body])).toEqual([
			[null, { credential: "id-token" }],
			[null, { credential: "id-token", display_name: "Dana" }],
		]);
		await expect(fresh.client.signInWithGoogle("id-token")).rejects.toThrow("session_already_set");
		expect((await fresh.client.account()).display_name).toBe("Dana");
		expect((await fresh.client.renameAccount("Kraken")).display_name).toBe("Kraken");
		await fresh.client.deleteAccount();
		expect(fresh.callsTo("/account").map((call) => [call.method, call.authorization])).toEqual([
			["GET", "Bearer signed-in-secret"],
			["PUT", "Bearer signed-in-secret"],
			["DELETE", "Bearer signed-in-secret"],
		]);
		expect(fresh.client.credential).toBeUndefined();
		await fresh.client.close();
	});
});

describe("commands", () => {
	const request = "44444444-4444-4444-8444-444444444444";
	const SEAT = "55555555-5555-4555-8555-555555555555";

	test("create sends the game's settings and refuses a different retry", async () => {
		const result = await h.client.createLobby({
			gameKey: "quiz-demo",
			name: "Room",
			teams: [{ name: "Red" }, { name: "Blue" }],
			requestId: request.toUpperCase(),
			gameSettings: { rounds: 3 },
		});
		expect(result.lobby.id).toBe(LOBBY);
		expect(h.callsTo("/lobbies")[0]?.body).toEqual({
			game_key: "quiz-demo",
			name: "Room",
			teams: [{ name: "Red" }, { name: "Blue" }],
			request_id: request,
			client_instance_id: h.client.clientInstanceId,
			game_settings: { rounds: 3 },
		});
		await expect(
			h.client.createLobby({
				gameKey: "quiz-demo",
				name: "Room",
				teams: [{ name: "Red" }, { name: "Blue" }],
				requestId: request,
				gameSettings: { rounds: 4 },
			}),
		).rejects.toThrow("idempotency_conflict");
	});

	test("rejected game settings carry the game's message", async () => {
		h.hook = (call) =>
			call.path.endsWith("/lobbies")
				? problem(422, "invalid_game_settings", { detail: "rounds must be at most 9" })
				: undefined;
		const error = await h.client
			.createLobby({
				gameKey: "quiz-demo",
				name: "Room",
				teams: [{ name: "Red" }],
				requestId: request,
				gameSettings: { rounds: 99 },
			})
			.catch((caught) => caught);
		expect(error.code).toBe("invalid_game_settings");
		expect(error.detail).toBe("rounds must be at most 9");
	});

	test("each command sends its body and installs the snapshot", async () => {
		await h.join();
		const bodies: Record<string, Call | undefined> = {};
		h.version = 2;
		await h.client.selectTeam(SCOPE, TEAM);
		bodies.team = h.callsTo("/team")[0];
		h.version = 3;
		await h.client.setReady(SCOPE, true);
		h.version = 4;
		await h.client.setMemberSettings(SCOPE, { bot: { kind: "default", level: 2 } });
		h.version = 5;
		await h.client.addSeat(SCOPE, { teamId: TEAM, seatSettings: { level: 1 }, requestId: request });
		h.version = 6;
		await h.client.removeSeat(SCOPE, SEAT);
		h.version = 7;
		await h.client.changeSettings(SCOPE, { teams: [{ name: "Red" }], gameSettings: { rounds: 2 } });
		h.version = 8;
		await h.client.start(SCOPE);
		h.version = 9;
		const reopened = await h.client.reopen(SCOPE);

		expect(reopened.lobby.version).toBe(9);
		expect(h.callsTo("/team")[0]?.body).toEqual({ team_id: TEAM });
		expect(h.callsTo("/ready")[0]?.body).toEqual({ ready: true });
		expect(h.callsTo("/member-settings")[0]?.body).toEqual({
			member_settings: { bot: { kind: "default", level: 2 } },
		});
		expect(h.callsTo("/seats", "POST")[0]?.body).toEqual({
			request_id: request,
			team_id: TEAM,
			seat_settings: { level: 1 },
		});
		expect(h.callsTo(`/seats/${SEAT}`, "DELETE")).toHaveLength(1);
		expect(h.callsTo("/settings")[0]?.body).toEqual({
			teams: [{ name: "Red" }],
			game_settings: { rounds: 2 },
		});
		expect(h.callsTo("/start")[0]?.body).toEqual({});
		expect(h.callsTo("/reopen")).toHaveLength(1);
	});

	test("a seat gets a request ID when none is given", async () => {
		await h.join();
		await h.client.addSeat(SCOPE, { teamId: TEAM, seatSettings: {} });
		const body = h.callsTo("/seats", "POST")[0]?.body as { request_id: string };
		expect(body.request_id).toMatch(/^[0-9a-f-]{36}$/);
	});

	test("a command on a lobby the client doesn't follow fails", async () => {
		await expect(h.client.setReady(SCOPE, true)).rejects.toThrow("not_joined");
	});

	test("watchers watch with the code and the host kicks them", async () => {
		h.hook = (call) => {
			if (call.path.endsWith("/watches")) {
				return json(200, {
					game_key: "quiz-demo",
					lobby_id: LOBBY,
					channel: `watch:quiz-demo:${LOBBY}`,
					member: false,
				});
			}
			if (call.path.endsWith("/watchers") && call.method === "GET") {
				return json(200, {
					watchers: [{ player_id: OTHER, display_name: "Grace", watching_since: "x" }],
				});
			}
			return undefined;
		};
		const access = await h.client.watch({ gameKey: "quiz-demo", joinCode: "ABCD" });
		expect(access.channel).toBe(`watch:quiz-demo:${LOBBY}`);
		expect(h.callsTo("/watches")[0]?.body).toEqual({ game_key: "quiz-demo", join_code: "ABCD" });
		const watchers = await h.client.watchers(SCOPE);
		expect(watchers.map((watcher) => watcher.player_id)).toEqual([OTHER]);
		await h.client.kickWatcher(SCOPE, OTHER);
		expect(h.callsTo(`/watchers/${OTHER}`, "DELETE")).toHaveLength(1);
		await h.client.unwatch(SCOPE);
		expect(h.callsTo("/watch", "DELETE")).toHaveLength(1);
		// A watcher holds no lease, so nothing is followed.
		expect(h.sdks).toHaveLength(0);
	});

	test("leave ends the membership and the updates", async () => {
		await h.join();
		const updates = h.client.updates(SCOPE);
		await updates.next();
		await h.client.leave(SCOPE);
		await expect(updates.next()).rejects.toThrow("left_membership");
		expect(h.callsTo("/membership", "DELETE")).toHaveLength(1);
		expect(h.sdk.subscriptions).toHaveLength(0);
		await expect(h.client.snapshot(SCOPE)).rejects.toThrow("not_joined");
		await h.client.close();
		expect(h.callsTo(`/leases/${h.client.clientInstanceId}`)).toHaveLength(0);
	});
});

describe("closing", () => {
	test("close releases each lease, disconnects and stops every method", async () => {
		await h.join();
		const updates = h.client.updates(SCOPE);
		await updates.next();
		await Promise.all([h.client.close(), h.client.close()]);
		const releases = h.callsTo(`/leases/${h.client.clientInstanceId}`, "DELETE");
		expect(releases).toHaveLength(1);
		expect(releases[0]?.search).toBe("?generation=1");
		expect(releases[0]?.authorization).toBe("Bearer retained-secret");
		expect(h.sdk.disconnected).toBe(true);
		await expect(updates.next()).rejects.toBeInstanceOf(ClientClosedError);
		await expect(h.client.snapshot(SCOPE)).rejects.toBeInstanceOf(ClientClosedError);
		await expect(h.join()).rejects.toBeInstanceOf(ClientClosedError);
	});

	test("a failed release doesn't fail close", async () => {
		await h.join();
		h.hook = (call) =>
			call.path.includes("/leases/") ? problem(500, "internal_error") : undefined;
		await h.client.close();
		expect(h.sdk.disconnected).toBe(true);
	});

	test("close releases a join that waits for its acknowledgement", async () => {
		h.autoAck = false;
		const joining = h.join();
		await until(() => h.sdks.length === 1);
		await h.client.close();
		await expect(joining).rejects.toBeInstanceOf(ClientClosedError);
	});
});
