// A game's catalog URLs are either paths on the lobby page's own origin, or
// absolute URLs on the game's own origin, the catalog entry's `origin`, for a
// game whose site is not the lobby's. The host checked every URL against
// those two origins when it started (handler.go). The page loads the game's
// modules and documents, and calls its API, wherever the catalog points.

// gameURL is the origin of the game's site: its catalog `origin`, or the
// lobby page's own for a game whose URLs are all paths. The page passes it to
// the game's modules, which reach the rest of the game's API there.
export function gameURL(game, pageOrigin) {
  return game?.origin || pageOrigin;
}

// locate splits a catalog URL into the origin it is on and its path, which
// may hold placeholders such as {lobby_id}. It returns null for a value that
// is neither a path nor an absolute URL on the game's origin.
export function locate(game, value, pageOrigin) {
  if (typeof value !== "string" || !value) return null;
  if (value.startsWith("/") && !value.startsWith("//")) return { origin: pageOrigin, path: value };
  const origin = game?.origin;
  if (origin && value.startsWith(`${origin}/`)) return { origin, path: value.slice(origin.length) };
  return null;
}

// catalogURL is a catalog URL made absolute, with its placeholders intact, or
// null when locate refuses it.
export function catalogURL(game, value, pageOrigin) {
  const located = locate(game, value, pageOrigin);
  return located ? located.origin + located.path : null;
}

// resolvingFetch returns a fetch that sends a path, such as one that a
// game's example player asks for, to base, the game's origin, and any other
// URL unchanged.
export function resolvingFetch(fetch, base) {
  return (url, init) => fetch(typeof url === "string" && url.startsWith("/") && !url.startsWith("//") ? base + url : url, init);
}
