---
name: statusline
description: Put PR Board's segment ([PRs] ✗5 ◷2 ✓1 ⊙4, linked to the board) in the Claude Code statusline. Use for "/pr-board:statusline", "add PR Board to my statusline", "wire pr-board line".
---

# statusline

A plugin cannot set `statusLine` itself, so this appends one line to the script the statusline runs.

1. Tell the user what will change before running anything: `~/.claude/statusline.sh` gets one line appended (copied first to `statusline.sh.before-pr-board`; created when missing), and `statusLine` in `~/.claude/settings.json` is set only when nothing sets it yet (copied first to `settings.json.before-pr-board`). A second run changes nothing.
2. Run `sh "${CLAUDE_PLUGIN_ROOT}/scripts/wire-statusline.sh"` (Bash). If the user only wants the line, run it with `--print` instead and show the line.
3. Report its output as it is, including any note that the script still runs `prmeter`, which `pr-board line` replaces. Say the segment shows from the next statusline refresh.

If it says pr-board is not installed, point to the README install step and stop.
