// A lobby plays one game per round: once its game has ended, a member reopens
// it (Back to lobby), it waits again in its next round, and the host can start
// the next game. The page keeps one result document and one example player per
// round, and starts over when the round changes.

// resultURL fills a game's result URL with the lobby ID and, where the game
// asks for it, the round, so the game can tell which of the lobby's games the
// page wants.
export function resultURL(template, lobby) {
  return template.replaceAll("{lobby_id}", encodeURIComponent(lobby.id)).replaceAll("{round}", encodeURIComponent(String(lobby.round)));
}

// reopenMessage is what the page shows when Back to lobby is refused, or null
// for a refusal the page explains like any other.
export function reopenMessage(error) {
  if (error.code === "game_running") return "The game is still running. Back to lobby opens the lobby once it has ended.";
  if (error.code === "phase_conflict") return "This lobby can’t be opened again.";
  return null;
}
