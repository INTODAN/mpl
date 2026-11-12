// The server can throttle a request with 429 when it is busy; the request was
// not run, and asking again a moment later usually works. This module sends
// such a request again, a few times, and gives the one plain message the page
// shows when it stays throttled. The page's own API calls, a room module's
// calls and the requests relayed for a game's frame all use it.

// What the user reads when a request stays throttled.
export const serverBusyMessage = "The server is busy. Try again in a moment.";
// The wait before each retry; three entries mean up to three retries.
export const retryDelaysMs = [500, 1000, 2000];
// Each wait grows by a random share of up to this fraction.
export const jitterFraction = 0.25;
// No wait is longer than this, whatever Retry-After asks for.
export const maxRetryWaitMs = 5000;
// Time a caller with its own timeout adds for the waits of a retried request.
export const retryWaitBudgetMs = maxRetryWaitMs;

// Lobby requests that set a value, so sending them again changes nothing.
// Reopening a lobby after its game is one: a lobby that already waits comes
// back unchanged. A request to create a lobby, join, start, add or remove a
// seat, start or stop watching, or remove a watcher is not here and is never
// retried; GET always is, such as the list of watchers.
const repeatableLobbyRequests = [
  { method: "PUT", path: /\/lobbies\/[^/]+\/team$/ },
  { method: "PUT", path: /\/lobbies\/[^/]+\/ready$/ },
  { method: "PUT", path: /\/lobbies\/[^/]+\/settings$/ },
  { method: "PUT", path: /\/lobbies\/[^/]+\/member-settings$/ },
  { method: "POST", path: /\/lobbies\/[^/]+\/heartbeat$/ },
  { method: "POST", path: /\/lobbies\/[^/]+\/reopen$/ },
];

// isRepeatableLobbyRequest tells whether a request to the lobby API may be
// sent again after a 429. path is the request's path, without the query.
export function isRepeatableLobbyRequest(method, path) {
  if (method === "GET") return true;
  return repeatableLobbyRequests.some((entry) => entry.method === method && entry.path.test(path));
}

// retryAfterMs returns how long the response's Retry-After header asks to
// wait, or 0 when it has none that can be read.
function retryAfterMs(response) {
  const value = response.headers.get("Retry-After");
  if (value === null || value.trim() === "") return 0;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) return seconds * 1000;
  const date = Date.parse(value);
  return Number.isNaN(date) ? 0 : Math.max(0, date - Date.now());
}

// retryWaitMs returns the wait before retry number attempt (0 for the first).
export function retryWaitMs(attempt, response, random = Math.random) {
  const base = retryDelaysMs[attempt] * (1 + jitterFraction * random());
  return Math.min(maxRetryWaitMs, Math.max(base, retryAfterMs(response)));
}

function sleepUnlessAborted(milliseconds, signal) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(new DOMException("aborted", "AbortError"));
    const onAbort = () => {
      clearTimeout(timer);
      reject(new DOMException("aborted", "AbortError"));
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, milliseconds);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

// fetchRetrying calls fetch(url, init) and, while the response is 429 and
// repeatable is true, waits and calls it again, up to retryDelaysMs.length
// times. It returns the last response. init must be reusable: a string body
// and no stream. An abort of init.signal ends a wait with an AbortError.
// sleep and random are for tests.
export async function fetchRetrying(fetch, url, init, { repeatable, sleep, random } = {}) {
  const wait = sleep ?? ((milliseconds) => sleepUnlessAborted(milliseconds, init?.signal));
  for (let attempt = 0; ; attempt++) {
    const response = await fetch(url, init);
    if (response.status !== 429 || !repeatable || attempt >= retryDelaysMs.length) return response;
    await response.body?.cancel().catch(() => {});
    await wait(retryWaitMs(attempt, response, random));
  }
}
