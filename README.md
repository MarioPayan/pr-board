# PR Board

A local page that lays out your open GitHub pull requests: one trunk per repository with its default branch, one lane per PR pointing into it, stacked and required PRs chained left to right, and each card coloured by what it still needs. Merged PRs fold under the trunk until you clean them up. A small Go server (`pr-board`) collects the data with your `gh` login; an optional Claude Code plugin keeps it alive while sessions run. Everything on the board is derived from the PR itself; no model reads it.

## Screenshots

All screenshots come from `pr-board demo`, a fictional organization.

![The board in light mode: four repositories with green, yellow and red PRs, a required-PR chain and a stacked branch](docs/screenshots/pr-board-light.png)

![The same board in dark mode](docs/screenshots/pr-board-dark.png)

![The legend drawer open on the right, with the colours, icons and links](docs/screenshots/pr-board-legend.png)

![Filtered to one repository and two states: the chain stays whole, the PR that does not match is dimmed](docs/screenshots/pr-board-filtered.png)

## What the page shows

| Colour | Meaning |
|---|---|
| green | Ready to merge: not a draft, approved, every check passed and none running, mergeable, not behind, no branch rule blocking, no changes requested, no required PR still open |
| yellow | Ready to approve: not a draft and everything above is in place except an approval |
| red | Something missing: any other open PR (draft, CI failing or running, conflicts, behind, changes requested, a branch rule, a required PR still open); the icons say what |
| purple | Merged, until Clean up removes it |

Every open card has the same height: one header row (number, state, base branch, category), the title on one line (the full title in its tooltip), one signals row that never wraps (icons that do not fit fold into a `+N` chip whose tooltip lists them) and one Next line naming the missing thing. Merged cards are shorter, and all the same height. Icons carry a count where one applies and a tooltip with the detail (which jobs fail, who approved). The info button in the header opens the legend drawer, which lists the colours, every icon and the links.

A solid link joins a PR to the PR whose branch is its base, or to the PR its description requires. The tag in a card's header is its category: the first token in square brackets in the title (`[Checkout]`, `[Design system]`), each bracket split on `|`, skipping the repository's own name (`[web]`), issue keys (`[WEB-123]`) and `required:` lists. No such token, no tag. The title is shown unchanged.

### Filters

The bar under the header filters the cards on the page: a search over number, title and branch, Repo, State and Category (with "No category") multi-selects, and a Show merged switch (off by default; the merged folds open as before when it is on). A chain is shown whole when any PR in it matches, with the others dimmed, so the merge order stays visible; a repository with nothing left hides. "Showing N of M open" counts the matching open PRs, and Clear resets everything.

The filters live in the URL fragment, so a link reproduces the view:

```text
http://127.0.0.1:<port>/#state=missing,approve&repo=web&q=checkout&cat=Checkout&merged=1
```

State values are `ready`, `approve` and `missing`; `cat=~none` is "No category".

### Required PRs

Name the PRs that must merge first anywhere in the description, outside fenced code:

```text
[required:acme/infra#478, acme:design-system#12, api#4021, #4543]
```

`owner/repo#N` and `owner:repo#N` are full references, `repo#N` uses the PR's owner, `#N` its repository. The keyword is case-insensitive, several blocks merge, and malformed items are skipped. An open PR in the same repository on the board becomes the lane's previous card (solid link, like a stacked branch; when several qualify, the first one, and the rest stay chips). Every other requirement is a lock chip, "after repo#N", coloured by the target's state, which the collector looks up with one extra GraphQL query when the target is not on the board: green when merged, amber when open, red when closed without merging, amber "not found" when GitHub does not know it. Any requirement that is not merged makes the card red with "merge #N first".

## Install

```sh
GOBIN=$HOME/.local/bin go install ./cmd/pr-board     # from a clone
```

Needs `gh` logged in (`gh auth status`) and, for the session check-in and cleanup's live-session check, `claude` on `PATH`.

The Claude Code plugin lives in `plugin/`; the repository root is a one-plugin marketplace:

```sh
claude plugin marketplace add /path/to/pr-board
claude plugin install pr-board@pr-board
```

It adds a SessionStart hook and the `/pr-board` skill. The hook looks for `pr-board` on `PATH`, then in `~/.local/bin` and `~/go/bin`, and exits 0 without doing anything when it is missing.

## Usage

```sh
pr-board                              # same as pr-board up
pr-board up [--session ID] [--open]   # start the server or reuse the running one; prints its URL
pr-board refresh                      # collect from GitHub now
pr-board cleanup [--dry-run] [--yes]  # print the cleanup plan; without --dry-run ask y/N, then run it
pr-board status                       # pid, port, idle deadline, who checked in
pr-board stop
pr-board collect --json               # collect once without a server and print store, derived state and view
pr-board demo [--port N] [--open]     # serve the fictional demo board until Ctrl-C (also: pr-board --demo)
```

`refresh` and `cleanup` start the server when none runs; `status` and `stop` never do. `demo` needs no GitHub login and touches none of the files below: it runs from a temporary directory, apart from the real server, and has no cleanup.

Config: `~/.config/pr-board/config.json`, written with these defaults on first run:

```json
{"owners":[],"author":"@me","merged_days":7,"clone_roots":["~/repos","~/src","~/code"],"idle_minutes":10,"port":7878}
```

`owners` lists the GitHub users or organizations to search; until it names one, the page shows where to add them instead of an empty board. The server reads the config on every refresh, so no restart is needed.

Files: `~/.local/state/pr-board/server.json` (lock: pid, port, start time), `server.log`, `loops/<session>.lock`; `~/.local/share/pr-board/prs.json` (the board, written atomically).

Upgrading from pr-map: on the first run, when `~/.config/pr-board/config.json` or `~/.local/share/pr-board/` is missing and the old `pr-map` one exists, it is moved over (the config, and `prs.json` with the list of cleaned-up PRs). The old state directory is left alone; stop the old server with `pr-map stop`.

## How it runs

- **One server per machine.** `pr-board up` reuses the server named in the lock file after checking that its pid is alive and that `/api/health` on its port answers with that pid; otherwise it starts a new one detached. The server holds an exclusive `flock`, so two racing starts end with one server. It binds `127.0.0.1:7878` (the config's `port`), so a saved link keeps working across restarts, and falls back to a free port when that one is taken.
- **Collecting.** On start, on Refresh and every 5 minutes: `gh search prs` for your open PRs and those merged in the last `merged_days`, then batched `gh api graphql` queries (15 PRs each) for review, check, merge and thread state. Merged PRs stay on the board until cleaned up; closed-unmerged ones leave it.
- **Staying alive.** Start counts as a check-in, and so does every heartbeat: each Claude session's loop posts one every 3 minutes, and an open page posts one every 60 seconds (and re-fetches the board). The server exits `idle_minutes` after the last check-in and removes its lock file.
- **Session check-in.** The SessionStart hook runs `pr-board up` for the session and starts a detached `pr-board session-loop`, which posts a heartbeat every 3 minutes and exits when `claude agents --json` no longer lists its session.

## Safety rules

- **Local only.** Every request must carry `Host: 127.0.0.1:<port>` or `localhost:<port>`, which blocks DNS rebinding. Every POST must carry `X-PR-Board: 1`, and if it has an `Origin` it must be the server's own; another web page can therefore not trigger a refresh, a stop or a cleanup.
- **Clean up asks first.** `GET /api/cleanup` only plans and returns a one-time token valid for 10 minutes; `POST /api/cleanup` spends it, plans those PRs again against the current disk, and runs that plan. The page shows the plan before the Clean up button; the CLI prints it and asks y/N unless `--yes`.
- **What Clean up deletes, per merged PR:**
  - the GitHub head branch (`gh api -X DELETE repos/O/R/git/refs/heads/B`), only if it still exists, lives in the same repository (not a fork) and is not the default branch;
  - local worktrees on that branch, in clones under `clone_roots` (3 levels deep, symlinked roots counted once) whose remote is that repository;
  - the local branch.
- **What it keeps, and says why:** a worktree with uncommitted changes, a HEAD commit younger than 30 minutes, or a live Claude session inside it (`claude agents --json` cwd); the main clone's own checkout; a local branch whose tip is neither the PR head nor an ancestor of it (unpushed work); any branch named like the default branch. A PR with anything kept or failed stays on the board, so a second Clean up can finish it.
- The hook never blocks or fails a session: it exits 0 on every path.
- No tokens: GitHub access is your `gh` login.

## Development

```sh
go vet ./... && go test ./...
```

Tests cover the colour rules, chains, the GraphQL parser on a recorded-shape fixture, collection merges, the Host/Origin/header guard, idle exit, singleton reuse and stale locks, the session loop's exit, loading an old store, moving the old pr-map files, the setup message, the demo board, and the cleanup skip rules on temporary git repositories with a fake `gh`.
