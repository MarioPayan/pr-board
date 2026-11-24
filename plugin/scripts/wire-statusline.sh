#!/usr/bin/env sh
# Wire `pr-board line` into the Claude Code statusline.
#
# A plugin cannot own the statusline: statusLine is a user setting, and no
# plugin component sets it. So this appends one line to the script the
# statusline runs. Nothing here overwrites anything: an existing statusline
# script is copied to statusline.sh.before-pr-board before the line is
# appended, an existing statusLine setting is left exactly as it is, and a
# second run does nothing.
#
# Flags:
#   --print   change nothing; print the line to add yourself

set -eu

CLAUDE_DIR="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
SL="${PR_BOARD_STATUSLINE:-$CLAUDE_DIR/statusline.sh}"
SETTINGS="${CLAUDE_SETTINGS:-$CLAUDE_DIR/settings.json}"
PRINT=0

for arg in "$@"; do
    case "$arg" in
        --print) PRINT=1 ;;
        -h | --help) sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "wire-statusline: unknown flag $arg" >&2; exit 2 ;;
    esac
done

say() { printf '%s\n' "$*"; }
die() { printf 'wire-statusline: %s\n' "$*" >&2; exit 1; }

bin=$(command -v pr-board 2>/dev/null || true)
for c in "$HOME/.local/bin/pr-board" "$HOME/go/bin/pr-board"; do
    [ -n "$bin" ] && break
    [ -x "$c" ] && bin=$c
done
[ -n "$bin" ] || die "pr-board is not installed; see the README's Install section"

# </dev/null: the statusline's stdin is Claude Code's payload, which other
# segments may still need and this one does not.
SNIPPET="s=\$(\"$bin\" line 2>/dev/null </dev/null); [ -n \"\$s\" ] && printf '  %s' \"\$s\""

if [ "$PRINT" -eq 1 ]; then
    say "Add this to the script your statusLine runs:"
    say ""
    say "  $SNIPPET"
    exit 0
fi

mkdir -p "$CLAUDE_DIR"
if [ ! -f "$SL" ]; then
    printf '%s\n' '#!/usr/bin/env bash' '# Claude Code statusline.' "$SNIPPET" > "$SL"
    chmod +x "$SL"
    say "wrote $SL"
elif grep -q 'pr-board.* line' "$SL" 2>/dev/null; then
    say "$SL already runs pr-board line, left alone"
else
    cp "$SL" "$SL.before-pr-board"
    printf '\n%s\n%s\n' '# PR Board - appended by pr-board wire-statusline' "$SNIPPET" >> "$SL"
    say "appended to $SL (previous copy: $SL.before-pr-board)"
fi
if grep -q 'prmeter' "$SL" 2>/dev/null; then
    say "note: $SL still runs prmeter, which pr-board line replaces; remove that line"
fi

# Only ever adds statusLine when there is none: someone who has one made a choice.
if [ -f "$SETTINGS" ] && grep -q '"statusLine"' "$SETTINGS" 2>/dev/null; then
    say "settings.json already sets statusLine, left alone"
elif command -v jq >/dev/null 2>&1; then
    [ -f "$SETTINGS" ] || echo '{}' > "$SETTINGS"
    jq -e . "$SETTINGS" >/dev/null 2>&1 || die "$SETTINGS is not valid JSON; fix it first, nothing was changed"
    cp "$SETTINGS" "$SETTINGS.before-pr-board"
    tmp=$(mktemp)
    jq --arg cmd "bash \"$SL\"" '.statusLine = {type: "command", command: $cmd, refreshInterval: 10}' \
        "$SETTINGS" > "$tmp" || { rm -f "$tmp"; die "could not write statusLine, nothing was changed"; }
    mv "$tmp" "$SETTINGS"
    say "set statusLine in $SETTINGS (previous copy: $SETTINGS.before-pr-board)"
else
    say "jq is missing, so statusLine was not set. Add to $SETTINGS:"
    say "  \"statusLine\": {\"type\": \"command\", \"command\": \"bash $SL\", \"refreshInterval\": 10}"
fi

say ""
printf 'your statusline segment:  '
"$bin" line </dev/null
say ""
