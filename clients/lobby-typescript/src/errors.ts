/**
 * Typed public errors. Credentials and server response bodies are never
 * included, except the game's own message for invalid_game_settings,
 * invalid_seat_settings, invalid_member_settings and game_requirement, and the
 * game's problem type for game_requirement, which the game writes for clients.
 */

export class LobbyError extends Error {
	override name = "LobbyError";
	readonly code: string;
	readonly status: number | undefined;
	readonly detail: string | undefined;
	/**
	 * For game_requirement, the game's own problem type, such as a URI that
	 * means the player has yet to choose something in the game's API.
	 */
	readonly problemType: string | undefined;

	constructor(code: string, status?: number, detail?: string, problemType?: string) {
		super(detail ? `${code}: ${detail}` : code);
		this.code = code;
		this.status = status;
		this.detail = detail;
		this.problemType = problemType;
	}
}

/** The credential is missing, expired or refused (401). The client stops following its lobbies. */
export class AuthenticationError extends LobbyError {
	override name = "AuthenticationError";
}

/** The membership is gone, replaced or stale: rejoin to follow the lobby again. */
export class MembershipError extends LobbyError {
	override name = "MembershipError";
}

/** The server answered with something this client doesn't understand: upgrade the client. */
export class CompatibilityError extends LobbyError {
	override name = "CompatibilityError";
}

/** A network failure, a 5xx or a 429: the same call can be tried again. */
export class TransientError extends LobbyError {
	override name = "TransientError";
}

/** The client was closed. */
export class ClientClosedError extends LobbyError {
	override name = "ClientClosedError";
	constructor() {
		super("client_closed");
	}
}

export function responseError(
	status: number,
	code: string,
	detail?: string,
	problemType?: string,
): LobbyError {
	if (
		code === "invalid_game_settings" ||
		code === "invalid_seat_settings" ||
		code === "invalid_member_settings"
	) {
		// The game's validator wrote this message for clients, such as which
		// settings field is invalid.
		return new LobbyError(code, status, detail || undefined);
	}
	if (code === "game_requirement") {
		// The game refuses readiness, or the start, until the player does
		// something in the game's own API; its message says what.
		return new LobbyError(code, status, detail || undefined, problemType || undefined);
	}
	if (status === 401) return new AuthenticationError(code, status);
	if (status === 404 || code === "stale_membership") return new MembershipError(code, status);
	if (status >= 500 || status === 429) return new TransientError(code, status);
	return new LobbyError(code, status);
}
