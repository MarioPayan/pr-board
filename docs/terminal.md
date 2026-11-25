# In the terminal

![A terminal running pr-board list on the fictional demo board: one row per pull request with a CI light and a review light, the repository, the number in its state's colour and the title, worst first; along the bottom, the statusline segment [PRs] with its five counts](images/terminal.svg)

`pr-board line` is the board in one statusline segment. It replaces PRmeter, which did the same from its own GitHub query; this one reads the board, so the segment and the page always say the same thing.

```text
[PRs] ✓1 ✗1 ◷2 ⊙4 ✎4
      │  │  │  │  └ four of yours are drafts
      │  │  │  └ four are waiting on your review
      │  │  └ two of yours are ready to approve
      │  └ one of yours has something missing
      └ one of yours is ready to merge
```

The counts come by importance: the page's header counters green, red and yellow, then the To review band, then your drafts last. A draft counts once, dim, under `✎`, and not as something missing, as on the page. A count of zero is left out, and `[PRs]` on its own means nothing is open. Shape carries the state as well as colour, so it still reads in a colourblind palette, in a pipe or under `NO_COLOR`. `[PRs]` is a hyperlink (OSC 8) to the board, `http://127.0.0.1:<port>/`: the running server's port, or the configured `port` when none runs. A terminal without OSC 8 shows the label.

`pr-board list` spells it out from the same store, a row per pull request, yours first and then the ones to review, worst first:

```text
⑂ acme  5 mine · 1 to review
 mine   ✗✗ web          4     Fix the checkout total when a coupon expires
        ✗✓ api          2     Retry the webhook on a 502
        ·◷ web          3     Explain the empty cart
        ✓✓ web          1     Cache the price list
        ◷· design-syst… 5     DRAFT Tokens for the dark theme
 review ✓· api          9     Paginate the orders export
```

Two lights per row, **CI on the left, review on the right**, and the number in its header counter's colour (drafts grey, and last in their group):

|        | CI                     | Review                  |
| ------ | ---------------------- | ----------------------- |
| **✓**  | every check passed     | approved                |
| **◷**  | still running          | a reviewer is requested |
| **✗**  | failing, or a conflict | changes requested       |
| **·**  | the repo has no checks | nobody has been asked   |

A conflict is a red CI light: it blocks the merge and it is yours to fix, as a failing check is. `·` is the honest fourth state: a PR nobody has been asked to review is not "waiting", and a repository with no CI is not "passing". `pr-board legend` prints this key in the terminal.

Why it is shaped like this:

| | |
|---|---|
| **Already on screen** | You never ask. Asking costs a round trip and some context |
| **Not a session hook** | A `SessionStart` hook feeds the *model*, not you: Claude Code prints none of its stdout |
| **One call** | `line` reads `prs.json` and the lock file; it never calls GitHub or the server. On the 50-PR demo board it takes 5.7 ms median, process start included (200 runs, Linux x86_64) |
| **Never waits** | When the lock names no live server, `line` prints first and then starts one detached, so the link opens a live page and the board is refreshed; the server keeps refreshing itself every 5 minutes |
| **Never shouts** | No store yet, an unreadable store, no config: it prints nothing rather than an error |
| **Never lies** | A store more than 20 minutes old (four refresh windows, so a failing refresh rather than a due one) says how old it is: `(stale 25m)` |

Because `line` starts the server, a `pr-board stop` lasts until the statusline's next refresh while a Claude session is open.

To wire it, run `/pr-board:statusline` from the plugin, or the script it runs:

```sh
sh plugin/scripts/wire-statusline.sh           # append the line to ~/.claude/statusline.sh
sh plugin/scripts/wire-statusline.sh --print   # change nothing, print the line
```

A plugin cannot set the statusline itself (`statusLine` is a user setting, and no plugin component sets it), so the script appends one line to `~/.claude/statusline.sh` after copying it to `statusline.sh.before-pr-board`, or writes that file when there is none. It sets `statusLine` in `settings.json` only when nothing sets it yet, and a second run changes nothing. It also says so when the script still runs `prmeter`.

Knobs (environment):

| | |
|---|---|
| `PR_BOARD_LABEL` | what the segment answers to (default `[PRs]`, `""` drops it) |
| `PR_BOARD_LINK` | where the label points (default the board, `""` unlinks it) |
| `PR_BOARD_REPO_CHARS` | `list`'s repository name cap (default `12`) |
| `PR_BOARD_TITLE_CHARS` | `list`'s title cap (default `60`) |
| `PR_BOARD_MAX` | `list`'s rows before `+ N more` (default `15`) |
| `PR_BOARD_ASCII` | `1` forces ASCII glyphs (`x ~ + . d @`), `0` forbids the fallback; unset uses ASCII when the locale is not UTF-8 |
| `NO_COLOR` | drop the colour and the hyperlink |
