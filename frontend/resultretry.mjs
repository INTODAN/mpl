// A started lobby's result document can be unavailable for a moment: the lobby
// commits as started before the host binds the game, so another player's first
// read can find nothing yet. The page then retries on its own, because nothing
// else tells it to ask again.

// How long to wait after the first, second and third failure; later failures
// wait the last delay.
export const retryDelaysMs = [1000, 2000, 4000, 10000];

// resultRetryDelay returns the milliseconds to wait before the next attempt to
// load the result document, or null when the page should not retry. failures
// counts the consecutive failed attempts so far, at least 1. The page retries
// only while the lobby has started and the document isn't loaded.
export function resultRetryDelay({ phase, loaded, failures }) {
  if (phase !== "started" || loaded) return null;
  if (!Number.isInteger(failures) || failures < 1) return null;
  return retryDelaysMs[Math.min(failures, retryDelaysMs.length) - 1];
}
