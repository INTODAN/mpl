// Links that bring someone to a lobby: an invite link that joins it
// (?join=CODE) and a link that watches it (?watch=CODE). Both may name the game
// with &game=KEY, since the page lists several games; without it the page's
// selected game is used. The page reads the parameters once when it loads,
// takes them out of the address bar, and acts on them after the player has a
// name.

const codePattern = /^[A-Z0-9]{8}$/;
const gameKeyPattern = /^[a-z][a-z0-9-]{0,31}$/;

// readLinkParams reads the parameters of a page's query string, "?join=ABCD1234".
// It returns what the page should do, and the query string without the
// parameters it consumed, so the page can leave the rest of the address alone:
//   { action: "join" | "watch" | null, code, game, problem, rest }
// A link that has both parameters joins. A code that is not eight letters and
// digits is a problem to tell the person, not a request. The code is
// upper-cased, as the join form does.
export function readLinkParams(search) {
  const params = new URLSearchParams(search);
  const joinGiven = params.has("join");
  const watchGiven = params.has("watch");
  const raw = (params.get(joinGiven ? "join" : "watch") ?? "").trim().toUpperCase();
  const gameValue = (params.get("game") ?? "").trim();
  for (const name of ["join", "watch", "game"]) params.delete(name);
  const rest = params.toString();
  const result = { action: null, code: "", game: gameKeyPattern.test(gameValue) ? gameValue : null, problem: null, rest: rest ? `?${rest}` : "" };
  if (!joinGiven && !watchGiven) return result;
  const action = joinGiven ? "join" : "watch";
  if (!codePattern.test(raw)) return { ...result, problem: `This ${action === "join" ? "invite" : "watch"} link has no valid lobby code.` };
  return { ...result, action, code: raw };
}

// inviteLink returns the link that joins a lobby, for a page at origin and
// pathname such as "https://play.example" and "/lobby/ui/".
export function inviteLink(origin, pathname, code, gameKey) {
  const params = new URLSearchParams({ join: code });
  if (gameKey) params.set("game", gameKey);
  return `${origin}${pathname}?${params}`;
}

// addressWithout is the path and query to show once the link's parameters are
// consumed. hash is kept: the page uses it for its own invitations.
export function addressWithout(pathname, rest, hash = "") {
  return `${pathname}${rest}${hash}`;
}
