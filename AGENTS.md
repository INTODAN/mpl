# Agent guide

## The rule that matters most

**Keep the lobby game-neutral.** This repository is a lobby that any game can use. It stores each member's, seat's and lobby's game settings as opaque JSON, calls the game's hooks, and knows nothing of bots, maps, scores or any other game concept. Anything specific to one game belongs in that game's repository, behind the hooks, the game API and the frontend catalog. A game may appear in docs and tests only as an example, such as Island Battles (`INTODAN/island-battles-go`), the first game that uses the lobby.

When a game needs something the lobby doesn't offer, add a generic mechanism that any game could use (a registration field, a hook, a catalog entry), not a rule for that game.

## Where the work is

Open work in this repository and in its sibling (INTODAN/island-battles-go, its first game) is tracked on one project board, [**Island Battles**](https://github.com/users/INTODAN/projects/1). Its Status column is **Parked** (ideas: don't start them unless the owner says), **Todo**, **In progress**, **In review** (a pull request waits) and **Done**. Steps only the owner can do are in INTODAN/island-battles-go#119. When asked to start implementing, continue, or "do the next issues":

1. Take **Todo** items whose *Blocked by* issues are all closed. Check with `gh api repos/INTODAN/mpl/issues/N/dependencies/blocked_by`.
2. Move the item to **In progress** before you start, so parallel sessions don't take it too, and to **In review** when its pull request is open. `gh project` needs the `project` scope; if `gh` reports it missing, ask the owner.
   ```sh
   gh project item-list 1 --owner INTODAN --format json -L 200 --query "status:Todo"   # what's ready
   gh project item-edit 1 --owner INTODAN --url <issue URL> --field Status --value "In progress"
   ```
   A cloud session whose `gh` can't reach the board (no GraphQL, or no `project` scope) uses the **Board** workflow of INTODAN/island-battles-go (`.github/workflows/board.yml` there) instead: run it on that repository's `main` with `command: list` and read the job's log, or with `command: set`, `urls` and `status` to move items, including this repository's. It acts with the owner's `PROJECT_TOKEN` secret, which no session holds itself. A session without INTODAN/island-battles-go in its GitHub access asks the owner.
3. Implement one issue per pull request, with `Closes #N` in its body (or `Closes INTODAN/island-battles-go#N` for an issue in the sibling repository). Independent items can run in parallel in separate branches, worktrees or subagents.
4. Don't merge, unless the owner told your session to. Then merge only after an independent review, with the branch up to date and its checks green. Move the item to **Done** once it's merged and its steps after the merge are done.
5. **Every issue you file goes on the board:** `gh project item-add 1 --owner INTODAN --url <issue URL>`, then set its Status (**Parked** for a `discussion` issue, otherwise **Todo**). When it waits on another issue, add a *Blocked by* relationship on the issue (`gh api -X POST repos/INTODAN/mpl/issues/N/dependencies/blocked_by -F issue_id=<the blocking issue's numeric id>`), rather than writing it in the text.

Each issue is written to be implemented on its own: read the whole issue, plus the decisions and issues it links, before writing code.

## Labels

- `agent-ready`: fully specified; implement it as written.
- `design-first`: post the mockup or design on the issue, then stop until the owner picks.
- `needs-decision`: blocked on the owner. Ask on the issue; don't guess.
- `discussion`: a future idea. Don't implement it.
- `owner-action`: needs the owner. Ask on the issue first: most of these an agent can do with the owner's `gh` login and `aws login`.
- `epic`: a parent issue. Work on its sub-issues, not on the epic itself.

## Steps after a merge

When your change needs a step after it merges (a plan and apply, an apply-role script, a setting, a release, a check), write it in the PR. Name who does it, and assume an agent does it with the owner's `gh` login and `aws login`, asking in chat before an apply, a release, or anything irreversible. Only these go to the owner's checklist, INTODAN/island-battles-go#119:

- sign-ins an agent can't make: PyPI's and npm's websites, DNS, the owner's inbox;
- checks on real devices;
- decisions.

## How to work in this repository

- Commands for building, formatting, testing and checking the API documents and the generated Python models are under **Commands** in [README.md](README.md), with the checks that run on every pull request.
- Skills in [`.agents/skills/`](.agents/skills/) hold the repository's conventions. Read the one that applies before changing that area:
  - `lobby-development`: Huma endpoints, application services, GORM persistence and migrations, hooks and the game API
  - `breaking-changes`: any change to the player API, the game API, events, stored data or the Python or TypeScript client
- After changing an API, re-export the API documents and regenerate the Python models and the TypeScript types in the same change. On a rebase conflict in generated files, regenerate them instead of merging by hand.
- Games pin the module by version (`go get github.com/INTODAN/mpl@vX.Y.Z`) and the clients by their PyPI and npm versions. A change here reaches a game only after a release and the game's own update; say in the PR what a game must change.

## Deploying

Only merged code deploys. When the owner asks you to deploy or apply a change, use the Deploy workflow (`.github/workflows/deploy.yml`) on `main`, as [deploy/README.md](deploy/README.md#deploying-from-github-actions) describes. In a session where the owner has run `aws login`, you may also read AWS, edit the deploy bucket's settings files, and, when the owner says, run `github_apply_role.py` or log in to the VM with Lightsail's temporary keys.

1. The change must be merged first: by the owner, or by you when the owner told your session to merge.
2. For AWS resources, start a `plan` run on `main`. Read the plan from its log or summary, and tell the owner what it imports, creates, changes and destroys.
3. Only when the owner says to, start an `apply` run with `plan_run_id` set to that plan run's ID. Never pass `allow_destroy` unless the owner named each resource.
4. Then start the `vm`, `lobby` or `smoke` runs the change needs, and check the result: the job's log, and the AWS resources it should have changed.

The VM serves the lobby's tenants too. Keep `deploy/` game-neutral as well: a game appears only in the tenants' settings, which live in the deploy bucket, never in the code.

## Releasing

Don't run the Release workflow (`.github/workflows/release.yml`), and don't create tags or GitHub releases, unless the owner asks. It runs only by hand, from `main`, as [README.md](README.md#releases) describes.
