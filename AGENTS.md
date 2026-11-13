# Agent guide

## The rule that matters most

**Keep the lobby game-neutral.** This repository is a lobby that any game can use. It stores each member's, seat's and lobby's game settings as opaque JSON, calls the game's hooks, and knows nothing of bots, maps, scores or any other game concept. Anything specific to one game belongs in that game's repository, behind the hooks, the game API and the frontend catalog. A game may appear in docs and tests only as an example, such as Island Battles (`INTODAN/island-battles-go`), the first game that uses the lobby.

When a game needs something the lobby doesn't offer, add a generic mechanism that any game could use (a registration field, a hook, a catalog entry), not a rule for that game.

## Where the work is

The open work lives in GitHub issues on `INTODAN/mpl`, and **issue #1 is the work queue**. When asked to start implementing, continue, or "do the next issues", don't pick issues yourself:

1. Read #1. Find the first wave with unticked items, and the items in it whose **needs** are all closed and that nobody has claimed.
2. Claim each item you take by editing #1, as it describes.
3. Implement one issue per pull request, with `Closes #N` in its body (or a link such as `INTODAN/island-battles-go#231`, for an issue that lives in a game's repository). Items marked ∥ can run in parallel in separate branches, worktrees or subagents. Items marked → wait for the item named.
4. Don't merge. The owner reviews and merges, and ticks the item in #1.

Each issue is written to be implemented on its own: read the whole issue, plus the decisions and issues it links, before writing code.

## Labels

- `agent-ready`: fully specified; implement it as written.
- `design-first`: post the mockup or design on the issue, then stop until the owner picks.
- `needs-decision`: blocked on the owner. Ask on the issue; don't guess.
- `discussion`: a future idea. Don't implement it.
- `owner-action`: needs the owner's credentials or settings. Don't do it. When your change needs a manual step, say so in the PR and add it to the owner's checklist that #1 names.
- `epic`: a parent issue. Work on its sub-issues, not on the epic itself.

## How to work in this repository

- Commands for building, formatting, testing and checking the API documents and the generated Python models are under **Commands** in [README.md](README.md), with the checks that run on every pull request.
- Skills in [`.agents/skills/`](.agents/skills/) hold the repository's conventions. Read the one that applies before changing that area:
  - `lobby-development`: Huma endpoints, application services, GORM persistence and migrations, hooks and the game API
  - `breaking-changes`: any change to the player API, the game API, events, stored data or the Python client
- After changing an API, re-export the API documents and regenerate the Python models in the same change. On a rebase conflict in generated files, regenerate them instead of merging by hand.
- Games pin the module by version (`go get github.com/INTODAN/mpl@vX.Y.Z`) and the Python client by its PyPI version. A change here reaches a game only after a release and the game's own update; say in the PR what a game must change.

## Releasing

Don't run the Release workflow (`.github/workflows/release.yml`), and don't create tags or GitHub releases, unless the owner asks. It runs only by hand, from `main`, as [README.md](README.md#releases) describes.
