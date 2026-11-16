// A reload of the lobby page restores the room the tab saved, with the tab's
// membership. That membership lapses once the tab has been closed for longer
// than its lease and the grace period after it, and the lobby then answers the
// saved room with not_found, although the lobby is still there.
//
// A player on a started lobby's start roster can join it again with its code
// and gets their team back, so the page does that for them. In a waiting lobby
// a player whose membership lapsed may have lost their team, and joining again
// stays their own step: the page says so and leaves the code in Join code.

// What the player reads when the tab's membership of its saved room lapsed.
export const lapsedMessage = "You’re no longer connected to that lobby. Use its code to join again.";

// What the player reads when no lobby of the saved code lets them in.
export const notFoundMessage = "No lobby found for that code and game.";

// membershipLapsed tells whether an error from the lobby API says that the
// tab's membership of its room is gone, or isn't the current one any more.
export function membershipLapsed(error) {
  return error?.code === "not_found" || error?.code === "stale_membership";
}

// restoreRoom restores room, the room the tab saved: {game_key, lobby_id,
// join_code, phase}, with the lobby's phase in the last snapshot the tab
// showed. read() reads the room's snapshot with the tab's membership, join()
// joins the lobby with the saved code, and leave(snapshot) leaves the lobby
// of a snapshot that join() answered.
//
// It resolves to {snapshot} for the page to show the room, or to {message,
// joinCode} for the start screen, with the code for the Join code field. It
// rejects with any other error of read(), and with a 401 of join(), which the
// page reports as it reports other errors.
export async function restoreRoom(room, { read, join, leave }) {
  try {
    return { snapshot: await read() };
  } catch (error) {
    if (!membershipLapsed(error)) throw error;
  }
  const startScreen = (message) => ({ message, joinCode: room.join_code ?? "" });
  // Without a membership the page can't read the lobby, so it goes by the
  // phase it saved. A tab saved before the page kept the phase has none.
  if (room.phase !== "started") return startScreen(lapsedMessage);
  let snapshot;
  try {
    snapshot = await join();
  } catch (error) {
    if (error?.status === 401) throw error;
    // As for the Join lobby button, not_found means that no lobby of the code
    // lets the player in: it is gone or closed, or its next game started
    // without them. After any other failure the code may still work.
    return startScreen(error?.code === "not_found" ? notFoundMessage : lapsedMessage);
  }
  const sameLobby = snapshot?.lobby?.id === room.lobby_id;
  if (sameLobby && snapshot.lobby.phase === "started") return { snapshot };
  // Since the tab saved it, the lobby was reopened and waits for its next
  // game, or the code now names another lobby. The join wasn't the player's
  // own step, so the page leaves again.
  try {
    await leave(snapshot);
  } catch {
    // A membership that no tab renews lapses, as this tab's did.
  }
  return startScreen(sameLobby ? lapsedMessage : notFoundMessage);
}
