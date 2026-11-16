---
name: owner-token
description: Use in a cloud environment (GitHub Actions, Claude Code on the web) when a GitHub action fails because the environment's own token cannot do it, such as moving an item on the user-owned Island Battles project board. Covers the owner's PROJECT_TOKEN and the rules for using it.
---

# The owner's token in a cloud environment

The token a cloud environment gives you (a GitHub Actions `GITHUB_TOKEN`, or the Claude GitHub App's installation token) cannot do everything this repository's work needs. The clearest case is the [Island Battles project board](https://github.com/users/INTODAN/projects/1): it is owned by a user account, and GitHub Apps and Actions tokens cannot read or edit user-owned projects at all.

For those actions the owner keeps a classic personal access token, with the `repo` and `project` scopes, in the repository secret `PROJECT_TOKEN`. It acts as the owner, so everything done with it shows up under the owner's name.

## When to use it

Use it only when the action is not possible otherwise:

1. Try the action with the environment's own token first, unless it is one of the known cases below.
2. If it fails for lack of access (a 403, a 404 on something that exists, `Resource not accessible by integration`, a missing scope), retry that one command with the owner's token.
3. Don't use it to get around a check or a protection: branch protection, a required review, a failed check, or a rule in AGENTS.md (merging, deploying, applying). Those need the owner's go-ahead, whichever token you hold.

Known cases that always need it:

- `gh project` commands on the board (`item-list`, `item-add`, `item-edit`), as AGENTS.md's "Where the work is" describes.

## How to use it

Set it for the one command, not for the whole session:

```sh
GH_TOKEN="$PROJECT_TOKEN" gh project item-edit 1 --owner INTODAN --url <issue URL> --field Status --value "In progress"
```

A repository secret reaches you only where something exposes it:

- **GitHub Actions:** the workflow passes it as an environment variable (`env: PROJECT_TOKEN: ${{ secrets.PROJECT_TOKEN }}`). If `$PROJECT_TOKEN` is empty, the workflow doesn't pass it yet; say so in your reply or the PR instead of working around it.
- **Claude Code on the web:** the environment's settings must define `PROJECT_TOKEN`. If it is missing, ask the owner.

## Never

- print, echo or log the token, or write it to a file, a commit, an issue, a PR or a comment; the repository is public;
- pass it to a command that sends it anywhere but `api.github.com`;
- use it locally: on the owner's machine, `gh` already runs as the owner.
