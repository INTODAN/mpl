// A game's room module shows the game's own controls in a waiting room, such
// as a choice the game requires before the player gets ready. It calls the
// game's API through the page, which adds the player's credential, only for
// same-origin paths under the game's room API prefix for the shown lobby, and
// only with GET or PUT.

import { relayURL } from "./relay.mjs";

export const roomMethods = ["GET", "PUT"];
// How long the page waits for a whole response.
export const roomTimeoutMilliseconds = 8000;

// roomRequest fetches a room module's request and resolves to its status and
// body: the parsed value for a JSON response, and null otherwise. It throws an
// Error that says why when it refuses the request, or can't get an answer.
export async function roomRequest({ method = "GET", path, body }, { prefix, lobbyID, origin, credential, fetch = globalThis.fetch }) {
  if (!roomMethods.includes(method)) throw new Error(`method must be one of ${roomMethods.join(", ")}`);
  if (method === "GET" && body !== undefined) throw new Error("a GET request has no body");
  if (typeof credential !== "string" || !credential) throw new Error("the page has no player session");
  let url;
  try {
    url = relayURL(prefix, lobbyID, path, origin);
  } catch (error) {
    throw new Error(`refused: ${error.message}`);
  }
  const headers = { Accept: "application/json", Authorization: `Bearer ${credential}` };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), roomTimeoutMilliseconds);
  try {
    const response = await fetch(url, {
      method, headers, body: body === undefined ? undefined : JSON.stringify(body), redirect: "error", signal: controller.signal,
    });
    const type = response.headers.get("Content-Type") || "";
    const json = /^application\/(problem\+)?json\b/.test(type);
    return { status: response.status, body: json ? await response.json() : null };
  } catch {
    throw new Error("Cannot reach the game server. Check your connection and try again.");
  } finally {
    clearTimeout(timeout);
  }
}
