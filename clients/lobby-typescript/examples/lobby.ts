#!/usr/bin/env bun
/**
 * A small manual client: creates or joins a lobby, prints each snapshot, and
 * closes on Ctrl-C, which releases this process's lease but keeps the
 * membership.
 *
 *   bun examples/lobby.ts --name Ada
 *   bun examples/lobby.ts --name Grace --join-code ABCDEFGH
 */
import { parseArgs } from "node:util";
import { LobbyClient, LobbyError, scopeOf } from "../src/index.js";

const { values } = parseArgs({
	options: {
		"api-url": { type: "string", default: "http://localhost:8890" },
		"game-key": { type: "string", default: "mock-game" },
		name: { type: "string", default: "Ada" },
		"join-code": { type: "string" },
	},
});

const client = new LobbyClient(values["api-url"]);
process.once("SIGINT", () => {
	void client.close();
});

try {
	await client.createSession(values.name);
	const gameKey = values["game-key"];
	const joinCode = values["join-code"];
	const first = joinCode
		? await client.joinLobby({ gameKey, joinCode })
		: await client.createLobby({
				gameKey,
				name: `${values.name}'s lobby`,
				teams: [{ name: "Red" }, { name: "Blue" }],
				requestId: crypto.randomUUID(),
			});
	console.log(`Lobby ${first.lobby.name}, join code ${first.lobby.join_code}`);
	for await (const snapshot of client.updates(scopeOf(first))) {
		const names = (snapshot.members ?? []).map((member) => member.display_name).join(", ");
		console.log(`v${snapshot.lobby.version} ${snapshot.lobby.phase}: ${names}`);
	}
} catch (error) {
	if (!(error instanceof LobbyError) || error.code !== "client_closed") throw error;
} finally {
	await client.close();
}
