# How it works

## Install

```sh
go install github.com/MarioPayan/pr-board/cmd/pr-board@main   # into $GOBIN, or ~/go/bin
GOBIN=$HOME/.local/bin go install ./cmd/pr-board              # or from a clone
```

Needs Go 1.26 (the version in `go.mod`), `gh` logged in (`gh auth status`) and, for the session check-in and cleanup's live-session check, `claude` on `PATH`. There are no prebuilt binaries yet.

The Claude Code plugin lives in `plugin/`; the repository root is a one-plugin marketplace:

```sh
claude plugin marketplace add MarioPayan/pr-board   # or the path to a clone
claude plugin install pr-board@pr-board
```

It adds a SessionStart hook, the `/pr-board` skill and the `/pr-board:statusline` skill (see [In the terminal](terminal.md)). The plugin does not install the binary. The hook looks for `pr-board` on `PATH`, then in `~/.local/bin` and `~/go/bin`, and exits 0 without doing anything when it is missing.

## Usage

```sh
pr-board                              # same as pr-board up
pr-board up [--session ID] [--open]   # start the server or reuse the running one; prints its URL
pr-board refresh                      # collect from GitHub now
pr-board cleanup [--dry-run] [--yes]  # print the cleanup plan; without --dry-run ask y/N, then run it
pr-board status                       # pid, port, idle deadline, who checked in
pr-board line                         # the statusline segment (see terminal.md)
pr-board list                         # a row per open PR, in the terminal
pr-board legend                       # what the segment's and list's glyphs mean
pr-board stop
pr-board collect --json               # collect once without a server and print store, derived state and view
pr-board demo [--port N] [--open]     # serve the fictional demo board until Ctrl-C (also: pr-board --demo)
```

`refresh` and `cleanup` start the server when none runs, and so does `line`, detached and after printing; `status`, `stop`, `list` and `legend` never do. `demo` needs no GitHub login and touches none of the files below: it runs from a temporary directory, apart from the real server, and has no cleanup.

Config: `~/.config/pr-board/config.json`, written with these defaults on first run:

```json
{"owners":[],"author":"@me","merged_days":7,"clone_roots":["~/repos","~/src","~/code"],"idle_minutes":10,"port":7878}
```

`owners` lists the GitHub users or organizations to search; until it names one, the page shows where to add them instead of an empty board. The server reads the config on every refresh, so no restart is needed.

Files: `~/.local/state/pr-board/server.json` (lock: pid, port, start time), `server.log`, `loops/<session>.lock`; `~/.local/share/pr-board/prs.json` (the board, written atomically).

Upgrading from pr-map: on the first run, when `~/.config/pr-board/config.json` or `~/.local/share/pr-board/` is missing and the old `pr-map` one exists, it is moved over (the config, and `prs.json` with the list of cleaned-up PRs). The old state directory is left alone; stop the old server with `pr-map stop`.

## How it runs

- **One server per machine.** `pr-board up` reuses the server named in the lock file after checking that its pid is alive and that `/api/health` on its port answers with that pid; otherwise it starts a new one detached. The server holds an exclusive `flock`, so two racing starts end with one server. It binds `127.0.0.1:7878` (the config's `port`), so a saved link keeps working across restarts, and falls back to a free port when that one is taken.
- **Collecting.** On start, on Refresh and every 5 minutes: `gh search prs` for your open PRs, those merged in the last `merged_days` and the open PRs that request your review, then batched `gh api graphql` queries (15 PRs each) for review, check, merge and thread state. Merged PRs stay on the board until cleaned up; closed-unmerged ones leave it.
- **Staying alive.** Start counts as a check-in, and so does every heartbeat: each Claude session's loop posts one every 3 minutes, and an open page posts one every 60 seconds and re-fetches the board every 5 minutes. The server exits `idle_minutes` after the last check-in and removes its lock file.
- **Session check-in.** The SessionStart hook runs `pr-board up` for the session and starts a detached `pr-board session-loop`, which posts a heartbeat every 3 minutes and exits when `claude agents --json` no longer lists its session.

## Safety rules

- **Local only.** Every request must carry `Host: 127.0.0.1:<port>` or `localhost:<port>`, which blocks DNS rebinding. Every POST must carry `X-PR-Board: 1`, and if it has an `Origin` it must be the server's own; another web page can therefore not trigger a refresh, a stop or a cleanup.
- **Clean up asks first.** `GET /api/cleanup` only plans and returns a one-time token valid for 10 minutes; `POST /api/cleanup` spends it, plans those PRs again against the current disk, and runs that plan. The page shows the plan before the Clean up button; the CLI prints it and asks y/N unless `--yes`.
- **What Clean up deletes, per merged PR:**
  - the GitHub head branch (`gh api -X DELETE repos/O/R/git/refs/heads/B`), only if it still exists, lives in the same repository (not a fork) and is not the default branch;
  - local worktrees on that branch, in clones under `clone_roots` (3 levels deep, symlinked roots counted once) whose remote is that repository;
  - the local branch.
- **What it keeps, and says why:** a worktree with uncommitted changes, a HEAD commit younger than 30 minutes, or a live Claude session inside it (`claude agents --json` cwd); the main clone's own checkout; a local branch whose tip is neither the PR head nor an ancestor of it (unpushed work; when the PR head commit is not in the clone, it is fetched from `pull/N/head` first, and a head that still cannot be found keeps the branch as "could not verify"); any branch named like the default branch. A PR with anything kept or failed stays on the board, so a second Clean up can finish it.
- The hook never blocks or fails a session: it exits 0 on every path.
- No tokens: GitHub access is your `gh` login.

## Development

```sh
go vet ./... && go test ./...
```

Every image in the README and these docs comes from the demo board, so it shows the fictional org `acme` and never anyone's real pull requests. Change the page, the signals, `list` or `line`, and regenerate both sets, or they start describing a board that no longer exists:

```sh
python3 tools/readme-images.py   # docs/images/*.svg: builds pr-board, serves the demo, reads /api/prs, list and line
node scripts/shots.mjs           # docs/screenshots/*.png: builds pr-board, serves the demo on a free port, stops it
```

`readme-images.py` needs only Go and Python's standard library. The SVGs are schematics of the page, laid out from its CSS: it reads the colour tokens, the icon paths and the signal table from `internal/prboard/web/index.html`, checks the row grid it draws against the page's rules, and stops with an error when they drift. `shots.mjs` runs Playwright through `npx` (nothing is added to the repository) and needs its Chromium once: `npx playwright install chromium`. It prints the row heights per kind and any console error for each screenshot.

Tests cover the colour rules, the fixed signal order, chains across repositories, grouping by category and repository, the GraphQL parser on a recorded-shape fixture, collection merges and the PRs to review, the Host/Origin/header guard, idle exit, singleton reuse and stale locks, the session loop's exit, loading an old store, moving the old pr-map files, the setup message, the demo board, the statusline segment (counts, zeros, empty store, stale age, ASCII, `NO_COLOR`, the link and when it starts a server) and the `list` block, and the cleanup skip rules on temporary git repositories with a fake `gh`, including a PR head that has to be fetched first.
