// The relay fetches a game's data for its result document. The document runs
// in a sandboxed frame with an opaque origin and no credential, so it asks the
// page with a game.request message. The page fetches only same-origin paths
// under the game's data URL prefix for the shown lobby, only with GET, and
// answers with the response alone; the credential never reaches the frame.

// The most requests one result document has relayed at a time. A document
// that asks for more is refused until earlier ones finish.
export const maxRelayedRequests = 8;
// The largest response body the page reads and passes on, enough for a game's
// biggest download, such as a saved demo of a long game.
export const maxResponseBytes = 64 * 1024 * 1024;
// How long the page waits for a whole response, its headers and its body,
// unless the request asks for another time with timeout_seconds. A request
// may ask for at most maxTimeoutSeconds, for a large download over a slow
// connection.
export const defaultTimeoutSeconds = 8;
export const maxTimeoutSeconds = 120;
const maxPathLength = 2048;
const maxIDLength = 200;

// relayURL returns the absolute URL to fetch for a requested path, or throws
// an Error that says why the path is refused. The path must be the canonical
// form of a same-origin absolute path, so a browser would request exactly
// what is checked: no dot segments, no percent-encoding before the query, no
// backslashes, spaces, control characters or fragment. It must start with
// prefixTemplate with "{lobby_id}" replaced by lobbyID.
export function relayURL(prefixTemplate, lobbyID, path, origin) {
  if (typeof prefixTemplate !== "string" || !prefixTemplate) throw new Error("this game has no data to request");
  if (typeof lobbyID !== "string" || !lobbyID) throw new Error("no lobby is shown");
  if (typeof path !== "string" || path.length > maxPathLength) throw new Error(`path must be a string of at most ${maxPathLength} characters`);
  // Browsers drop tabs and line breaks and read a backslash as a slash, so
  // these could turn a checked path into another one.
  if (!path.startsWith("/") || path.startsWith("//") || /[\\#\u0000- \u007f]/.test(path)) {
    throw new Error("path must be a same-origin absolute path without a fragment");
  }
  const queryStart = path.indexOf("?");
  const pathname = queryStart === -1 ? path : path.slice(0, queryStart);
  // A server may decode %2F or %2E%2E into a path of its own.
  if (pathname.includes("%")) throw new Error("path must not be percent-encoded");
  let url;
  try {
    url = new URL(path, origin);
  } catch {
    throw new Error("path is not a valid URL path");
  }
  if (url.origin !== origin || url.pathname !== pathname) throw new Error("path must be in canonical form, without . or .. segments");
  const prefix = prefixTemplate.replaceAll("{lobby_id}", encodeURIComponent(lobbyID));
  if (!url.pathname.startsWith(prefix)) throw new Error("path is outside this lobby's game data");
  return url.href;
}

// requestID returns the message's ID, or undefined when it has none that the
// page can echo: a string of at most 200 characters or a finite number.
function requestID(data) {
  const id = data?.id;
  if ((typeof id === "string" && id.length <= maxIDLength) || (typeof id === "number" && Number.isFinite(id))) return id;
  return undefined;
}

function refused(id, error) {
  return { type: "lobby.response", id, status: 0, body: null, error };
}

// refuseRequest answers a game.request message with error and without
// fetching, or returns null for a message that relayRequest would ignore.
export function refuseRequest(data, error) {
  const id = requestID(data);
  if (data?.type !== "game.request" || id === undefined) return null;
  return refused(id, error);
}

class ResponseTooLarge extends Error {}

// readText reads a response body as text, and throws ResponseTooLarge once it
// passes maxBytes, without reading the rest.
async function readText(response, maxBytes) {
  if (Number(response.headers.get("Content-Length")) > maxBytes) throw new ResponseTooLarge();
  if (!response.body) return "";
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let text = "";
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) return text + decoder.decode();
    size += value.byteLength;
    if (size > maxBytes) {
      await reader.cancel().catch(() => {});
      throw new ResponseTooLarge();
    }
    text += decoder.decode(value, { stream: true });
  }
}

// relayRequest answers one game.request message with a lobby.response
// message: the HTTP status and the body, parsed when it is JSON and as text
// otherwise. A refused or failed request, one that takes longer than its
// timeout_seconds, or one whose body is larger than maxBytes, has status 0, a
// null body and an error. A message without a usable ID is ignored and
// returns null.
export async function relayRequest(data, { prefix, lobbyID, origin, credential, fetch = globalThis.fetch, maxBytes = maxResponseBytes }) {
  const id = requestID(data);
  if (data?.type !== "game.request" || id === undefined) return null;
  if (data.method !== undefined && data.method !== "GET") return refused(id, "only GET requests are relayed");
  const timeoutSeconds = data.timeout_seconds ?? defaultTimeoutSeconds;
  if (typeof timeoutSeconds !== "number" || !(timeoutSeconds > 0 && timeoutSeconds <= maxTimeoutSeconds)) {
    return refused(id, `timeout_seconds must be a number of seconds, more than 0 and at most ${maxTimeoutSeconds}`);
  }
  let url;
  try {
    url = relayURL(prefix, lobbyID, data.path, origin);
  } catch (error) {
    return refused(id, error.message);
  }
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutSeconds * 1000);
  try {
    const response = await fetch(url, {
      method: "GET", headers: { Authorization: `Bearer ${credential}`, Accept: "application/json" },
      redirect: "error", cache: "no-store", signal: controller.signal,
    });
    const text = await readText(response, maxBytes);
    let body = text;
    if (/\bjson\b/i.test(response.headers.get("Content-Type") || "")) {
      try { body = JSON.parse(text); } catch { /* Keep the text. */ }
    }
    return { type: "lobby.response", id, status: response.status, body };
  } catch (error) {
    if (error instanceof ResponseTooLarge) return refused(id, `the response is larger than ${maxBytes} bytes`);
    return refused(id, error.name === "AbortError" ? "the request timed out" : "the request failed");
  } finally {
    clearTimeout(timer);
  }
}
