#!/bin/sh
# SessionStart: start or reuse the pr-board server and a detached check-in loop
# for this session. Never blocks or fails the session: exits 0 on every path.
bin=$(command -v pr-board 2>/dev/null)
for c in "$HOME/.local/bin/pr-board" "$HOME/go/bin/pr-board"; do
  [ -n "$bin" ] && break
  [ -x "$c" ] && bin=$c
done
[ -n "$bin" ] || exit 0
"$bin" hook session-start >/dev/null 2>&1 || true
exit 0
