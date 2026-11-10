// Watching a lobby: someone with a guest session and the lobby's join code
// follows its game without a seat. A watcher sees no room, since the lobby
// shares its members only with them; the page shows the game's own watcher
// document once the game has started, and a short wait before that. The host
// can remove a watcher, who is told so and returns to the start screen.

// What the person reads for the lobby API's watcher errors.
const watchMessages = {
  not_found: "No lobby found for that code and game.",
  watcher_kicked: "The host removed you from this lobby’s watchers.",
  watchers_full: "This lobby has as many watchers as it allows. Try again later.",
};

// kickedMessage is what a removed watcher reads on the start screen.
export const kickedMessage = watchMessages.watcher_kicked;

// watchMessage returns the sentence for a refused request to watch or stop
// watching, or null for an error the page explains like any other.
export function watchMessage(error) {
  return watchMessages[error?.code] ?? null;
}

// wasKicked tells whether an error from the lobby API says the host removed
// this watcher.
export function wasKicked(error) {
  return error?.code === "watcher_kicked";
}

// A game offers watching when its catalog entry has a watcher document.
export function canWatch(game) {
  return Boolean(game?.watch_result_url);
}

// watchRelayPrefix is the data prefix that a watcher's document may read, or
// null for a game without one. It never falls back to the prefix of a
// player's document, which may read what a watcher may not.
export function watchRelayPrefix(game) {
  return game?.watch_data_url_prefix || null;
}

// relayPrefix is the data prefix of the document a person sees: a watcher's
// document may read the watcher prefix only, a player's the player prefix.
export function relayPrefix(game, watching) {
  return watching ? watchRelayPrefix(game) : game?.data_url_prefix || null;
}

// watchResultURL fills a game's watcher document URL with the lobby ID.
export function watchResultURL(template, lobbyID) {
  return template.replaceAll("{lobby_id}", encodeURIComponent(lobbyID));
}

// The wait before asking again for a watcher's document, which is missing until
// the game has started: after the first, second and third failure, and then
// every steadyWatchDelayMs.
export const watchRetryDelaysMs = [1000, 2000, 4000];
export const steadyWatchDelayMs = 5000;

// watchRetryDelay returns the milliseconds to wait after the failures-th failed
// read in a row, at least 1.
export function watchRetryDelay(failures) {
  if (!Number.isInteger(failures) || failures < 1) return steadyWatchDelayMs;
  return watchRetryDelaysMs[failures - 1] ?? steadyWatchDelayMs;
}

// watchersChanged tells whether the list of watchers must be read again after
// the lobby's snapshot changed: when its version moved on and it counts
// watchers or the page shows some. Reading is only for members.
export function watchersChanged({ lastVersion, version, watcherCount, shownCount }) {
  return version !== lastVersion && (watcherCount > 0 || shownCount > 0);
}
