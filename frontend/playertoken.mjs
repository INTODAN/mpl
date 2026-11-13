// A game's API takes a player token, not the guest credential: a short-lived
// token that the lobby issues for one game (POST /lobby/v1/player-tokens) and
// the game verifies with the lobby's public keys. The page gets one per game
// and session, gets a new one once renewShare of its life has passed, and once
// more when the game answers 401.

// A token is renewed once this share of its life has passed.
export const renewShare = 0.8;

// createPlayerTokens keeps the page's player tokens. issue(credential,
// gameKey) asks the lobby for one and resolves to {token, expires_at}.
// Callers that need the same token at once share one request.
export function createPlayerTokens({ issue, now = () => Date.now() }) {
  const current = new Map();
  const pending = new Map();
  const keyOf = (credential, gameKey) => `${gameKey} ${credential}`;
  return {
    // get resolves to the token for credential, the guest session's, and
    // gameKey. stale is a token the game refused: a new one replaces it.
    get(credential, gameKey, stale) {
      const key = keyOf(credential, gameKey);
      const held = current.get(key);
      if (held && held.token !== stale && now() < held.renewAt) return Promise.resolve(held.token);
      if (pending.has(key)) return pending.get(key);
      const issuedAt = now();
      const request = Promise.resolve()
        .then(() => issue(credential, gameKey))
        .then(({ token, expires_at: expiresAt }) => {
          if (typeof token !== "string" || !token) throw new Error("The lobby sent no player token.");
          const life = Date.parse(expiresAt) - issuedAt;
          // Keep only the session's newest token for each game.
          for (const other of current.keys()) {
            if (other.startsWith(`${gameKey} `)) current.delete(other);
          }
          current.set(key, { token, renewAt: issuedAt + (Number.isFinite(life) ? life : 0) * renewShare });
          return token;
        })
        .finally(() => pending.delete(key));
      pending.set(key, request);
      return request;
    },
  };
}

// playerTokenFetch returns a fetch(url, init) for gameKey's API that sends
// credential's player token, and sends a request answered 401 once more with
// a new token. The request's body must be a string or absent, so it can be
// sent twice.
export function playerTokenFetch({ tokens, credential, gameKey, fetch = globalThis.fetch }) {
  return async (url, init = {}) => {
    const send = (token) => {
      const headers = new Headers(init.headers);
      headers.set("Authorization", `Bearer ${token}`);
      return fetch(url, { ...init, headers });
    };
    const token = await tokens.get(credential, gameKey);
    const response = await send(token);
    if (response.status !== 401) return response;
    return send(await tokens.get(credential, gameKey, token));
  };
}
