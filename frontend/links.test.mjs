import assert from "node:assert/strict";
import test from "node:test";
import { addressWithout, inviteLink, readLinkParams } from "./links.mjs";

test("an invite link joins, and the code is upper-cased", () => {
  assert.deepEqual(readLinkParams("?join=abcd2345"), { action: "join", code: "ABCD2345", game: null, problem: null, rest: "" });
  assert.deepEqual(readLinkParams("?join=%20ABCD2345%20"), { action: "join", code: "ABCD2345", game: null, problem: null, rest: "" });
});

test("a watch link watches", () => {
  assert.deepEqual(readLinkParams("?watch=ABCD2345"), { action: "watch", code: "ABCD2345", game: null, problem: null, rest: "" });
});

test("a link names its game when it has one", () => {
  assert.equal(readLinkParams("?join=ABCD2345&game=island-battles").game, "island-battles");
  assert.equal(readLinkParams("?watch=ABCD2345&game=quiz").game, "quiz");
  // A game key the lobby API would refuse is ignored: the page's selected game is used.
  assert.equal(readLinkParams("?join=ABCD2345&game=Not%20A%20Key").game, null);
  assert.equal(readLinkParams("?join=ABCD2345&game=").game, null);
});

test("a link that has both parameters joins", () => {
  const params = readLinkParams("?watch=WWWWWWWW&join=JJJJJJJJ");
  assert.equal(params.action, "join");
  assert.equal(params.code, "JJJJJJJJ");
});

test("other parameters stay in the address", () => {
  assert.equal(readLinkParams("?debug=1&join=ABCD2345&game=quiz&x=y").rest, "?debug=1&x=y");
  assert.equal(addressWithout("/lobby/ui/", "?debug=1", "#a"), "/lobby/ui/?debug=1#a");
  assert.equal(addressWithout("/lobby/ui/", ""), "/lobby/ui/");
});

test("a page without a link has nothing to do", () => {
  assert.deepEqual(readLinkParams(""), { action: null, code: "", game: null, problem: null, rest: "" });
  assert.deepEqual(readLinkParams("?debug=1"), { action: null, code: "", game: null, problem: null, rest: "?debug=1" });
});

test("a code that cannot be a join code is a problem, not a request", () => {
  for (const search of ["?join=", "?join=ABC", "?join=ABCD23456", "?join=ABCD-345", "?join=%3Cscript%3E", "?join=../x"]) {
    const params = readLinkParams(search);
    assert.equal(params.action, null, search);
    assert.match(params.problem, /invite link has no valid lobby code/, search);
    assert.equal(params.code, "", search);
  }
  assert.match(readLinkParams("?watch=x").problem, /watch link has no valid lobby code/);
  // The problem link's parameters are still consumed.
  assert.equal(readLinkParams("?join=x&keep=1").rest, "?keep=1");
});

test("the invite link carries the code and the game", () => {
  assert.equal(inviteLink("https://play.example", "/lobby/ui/", "ABCD2345", "island-battles"), "https://play.example/lobby/ui/?join=ABCD2345&game=island-battles");
  assert.equal(inviteLink("https://play.example", "/lobby/ui/", "ABCD2345"), "https://play.example/lobby/ui/?join=ABCD2345");
  // What the link carries is what the page reads back.
  const link = new URL(inviteLink("https://play.example", "/lobby/ui/", "ABCD2345", "quiz"));
  assert.deepEqual(readLinkParams(link.search), { action: "join", code: "ABCD2345", game: "quiz", problem: null, rest: "" });
});
