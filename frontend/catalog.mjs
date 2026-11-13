// A game's catalog URLs are either paths on the lobby page's own origin, or
// absolute URLs on the game's own origin, the catalog entry's `origin`, for a
// game whose site is not the lobby's. The host checked every URL against
// those two origins when it started (handler.go). The page loads the game's
// modules and documents, and calls its API, wherever the catalog points.
// Below them are the page's texts that take the game's own words from its
// catalog entry.

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

// The page's texts that depend on a game's words. A game names its seats that
// no person plays, such as its bots, in its catalog entry's seat_names
// ({singular: "a bot", plural: "bots"}). For a game that names none, the
// texts name no such seats, but for calling one a seat where the host has
// just tried to add one.
export function seatTexts(game) {
  const names = game?.seat_names;
  return {
    // The host tried to add a seat whose choice can't be added.
    unavailable: `Choose ${names?.singular || "a seat"} that can be added.`,
    // The host's new settings have fewer teams than the lobby has members.
    tooFewTeams: names
      ? `These settings have fewer teams than the lobby has players and ${names.plural}. Remove ${names.singular}, or choose settings with more teams.`
      : "These settings have fewer teams than the lobby has players. Choose settings with more teams.",
    // What Back to lobby keeps.
    reopen: `Once the game has ended, this opens the lobby again for the next game, with the same ${names ? `players, teams and ${names.plural}` : "players and teams"}.`,
  };
}

// The heading of the example player's panel: the example strategy in a demo,
// and otherwise the heading that the game's entry gives its example player,
// such as "Your bot", or none.
export function examplePlayerHeading(game, demo) {
  return demo ? "Example strategy · " : game?.example_player?.heading || "";
}
