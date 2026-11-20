import type { components } from "./generated/openapi.js";

type Schemas = components["schemas"];
type WithoutSchema<T> = Omit<T, "$schema">;

export type Snapshot = WithoutSchema<Schemas["Snapshot"]>;
export type Lobby = Schemas["Lobby"];
export type Member = Schemas["Member"];
export type Team = Schemas["Team"];
export type TeamDefinition = Schemas["TeamDefinition"];
export type StartRules = Schemas["StartRules"];
export type StartRosterEntry = Schemas["StartRosterEntry"];
export type GuestSession = WithoutSchema<Schemas["GuestSession"]>;
export type AccountSession = WithoutSchema<Schemas["AccountSession"]>;
export type Account = WithoutSchema<Schemas["Account"]>;
export type SignInSettings = WithoutSchema<Schemas["SignInSettings"]>;
export type PlayerToken = WithoutSchema<Schemas["PlayerToken"]>;
export type ConnectionToken = WithoutSchema<Schemas["ConnectionToken"]>;
export type WatchAccess = WithoutSchema<Schemas["WatchAccess"]>;
export type Watcher = Schemas["Watcher"];
export type Problem = Schemas["Problem"];
export type LobbyInvalidation = Schemas["LobbyInvalidation"];
/** A game's own settings: a JSON object that only the game reads. */
export type GameSettings = Record<string, unknown>;

/** Names one lobby the client follows: its game and its ID. */
export interface Scope {
	readonly gameKey: string;
	readonly lobbyId: string;
}

export function scopeOf(snapshot: Pick<Snapshot, "lobby">): Scope {
	return { gameKey: snapshot.lobby.game_key, lobbyId: snapshot.lobby.id };
}
