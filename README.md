# PR Board

Every open pull request you wrote, on one local page, each row coloured by what it still
needs: green to merge, yellow to approve, red when something is missing. A PR that waits on
another sits right under it, across repositories too, and the same counts ride along in
your Claude Code statusline.

![PR Board in a browser on a fictional organization: header counters for ready to merge, something missing, ready to approve and drafts above a filter bar; a To review band with teammates' PRs that ask for your review; then one band per category, each PR a row with a coloured left bar, its number, repository and branch, title, signal chips and the next step](docs/images/board.svg)

## What you are looking at

![One band annotated: the category from the title, its repositories, open PRs per colour and the merged count; then each row's left bar colour, number, repository and target branch, title without its category tag, signal chips and next step, with a PR that waits on #412 indented under it. Below, the bar colours and every signal chip with its meaning](docs/images/anatomy.svg)

Every value is read from the PR itself: its title, description, reviews, checks and merge
state. [The page in detail](docs/page.md) covers the colours, signals, chains, filters and
`[required:]`, with real screenshots.

## Get it

Needs Go 1.26 and the [GitHub CLI](https://cli.github.com) logged in (`gh auth status`).

```sh
go install github.com/MarioPayan/pr-board/cmd/pr-board@latest
pr-board demo --open     # a fictional board: no GitHub login, none of your files touched
pr-board up --open       # yours: start the server, or reuse it, and open the page
```

The first run writes `~/.config/pr-board/config.json`. List the GitHub users or
organizations to search under `"owners"`; until you do, the page says where to add them.

To keep the board alive while you work and put it in the statusline, add the Claude Code
plugin:

```sh
claude plugin marketplace add MarioPayan/pr-board
claude plugin install pr-board@pr-board
```

Its SessionStart hook starts the board with each session, `/pr-board` opens it, and
`/pr-board:statusline` [wires the segment](docs/terminal.md) into your statusline after
backing up what is there.

## Why

| | |
|---|---|
| **What to do next** | Each row says what is missing and the next step: merge, wait for a reviewer, fix failing CI, merge #412 first |
| **Merge order in sight** | A stacked branch, or `[required:api#4021]` in a description, puts the PR under the one it waits on, in any repository |
| **Your reviews too** | Teammates' PRs that request your review sit in their own band on top, out of your counts |
| **No token, no model** | GitHub access is your `gh` login, and every value is derived from the PR; nothing is summarised or guessed |
| **Local only** | The server binds `127.0.0.1`, checks the `Host` header and wants its own header on every POST, so no other web page can drive it |
| **Gone when idle** | One server per machine; by default it exits 10 minutes after the last check-in from an open page or a Claude session |
| **Clean up asks first** | It deletes merged branches and their worktrees only after showing the plan, and keeps any with uncommitted changes, unpushed commits or a live Claude session |
| **Cheap in the statusline** | `pr-board line` reads one file and never calls GitHub: 5.7 ms median on the 50-PR demo board, [measured](docs/terminal.md) |

## In the terminal

![A terminal running pr-board list on the fictional demo board: one row per pull request with a CI light and a review light, the repository, the number in its state's colour and the title, worst first; along the bottom, the statusline segment [PRs] with its five counts](docs/images/terminal.svg)

`pr-board line` is the statusline segment: ready to merge, something missing, ready to
approve, waiting on your review and drafts, the label linked to the board. `pr-board list`
spells it out a row per PR, and `pr-board legend` prints the key.
[In the terminal](docs/terminal.md) has the glyphs, the knobs and the wiring.

## Commands

```sh
pr-board                              # same as pr-board up
pr-board up [--session ID] [--open]   # start the server or reuse the running one; prints its URL
pr-board refresh                      # collect from GitHub now
pr-board cleanup [--dry-run] [--yes]  # print the cleanup plan; without --dry-run ask y/N, then run it
pr-board status                       # pid, port, idle deadline, who checked in
pr-board stop
pr-board line                         # the statusline segment
pr-board list                         # a row per open PR, in the terminal
pr-board legend                       # what the segment's and list's glyphs mean
pr-board demo [--port N] [--open]     # serve the fictional demo board until Ctrl-C
```

**[How it works](docs/how-it-works.md)**: configuration, files, collecting, the safety
rules behind Clean up, and development.

## License

[MIT](LICENSE)
