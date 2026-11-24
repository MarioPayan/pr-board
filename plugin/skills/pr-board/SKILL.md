---
name: pr-board
description: Open PR Board, the local page that shows your open pull requests per repository with their review, CI and merge state. Use for "/pr-board", "show PR Board", "open the board of my PRs".
---

# pr-board

1. Run `pr-board up` (Bash). It starts the local server or reuses the running one and prints its URL.
   If the command is missing, say that pr-board is not installed and point to the README install step (`go install ./cmd/pr-board` from the pr-board repo); stop there.
2. Print the URL it returned as a link, nothing else.

Do not run `pr-board cleanup` from this skill: cleaning up deletes branches and needs the user's explicit go.
