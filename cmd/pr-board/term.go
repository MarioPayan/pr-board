package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MarioPayan/pr-board/internal/prboard"
)

type lookupEnv func(string) (string, bool)

// readConfig is the config, or the defaults when there is none. Unlike
// LoadConfig it never writes the file: the statusline must not create files.
func readConfig(p prboard.Paths) prboard.Config {
	if p.Config == "" {
		return prboard.DefaultConfig()
	}
	if _, err := os.Stat(p.Config); errors.Is(err, os.ErrNotExist) {
		return prboard.DefaultConfig()
	}
	c, _ := prboard.LoadConfig(p.Config)
	return c
}

// liveLock is the lock when its pid is alive. It asks the server nothing,
// so it costs a file read, not a round trip.
func liveLock(p prboard.Paths) (prboard.Lock, bool) {
	if p.State == "" {
		return prboard.Lock{}, false
	}
	l, err := prboard.ReadLock(p.Lock())
	return l, err == nil && prboard.PIDAlive(l.PID)
}

// wantASCII: PR_BOARD_ASCII=1 forces the ASCII glyphs, 0 forbids them;
// unset decides from the locale.
func wantASCII(env lookupEnv) bool {
	switch v, _ := env("PR_BOARD_ASCII"); v {
	case "1":
		return true
	case "0":
		return false
	}
	loc := ""
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v, _ := env(k); v != "" {
			loc = v
			break
		}
	}
	loc = strings.ToLower(loc)
	return !strings.Contains(loc, "utf8") && !strings.Contains(loc, "utf-8")
}

func envInt(env lookupEnv, k string, def int) int {
	if v, ok := env(k); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// termFromEnv reads the terminal knobs. The label links to the board: the
// running server's port, or the configured one when none runs (the
// statusline starts it). PR_BOARD_LINK changes the target, "" unlinks it.
func termFromEnv(p prboard.Paths, env lookupEnv, now time.Time) prboard.Term {
	cfg := readConfig(p)
	t := prboard.Term{
		ASCII:      wantASCII(env),
		Label:      "[PRs]",
		Now:        now,
		Stale:      4 * prboard.DefaultRefreshEvery,
		Owners:     cfg.Owners,
		RepoChars:  envInt(env, "PR_BOARD_REPO_CHARS", 12),
		TitleChars: envInt(env, "PR_BOARD_TITLE_CHARS", 60),
		Max:        envInt(env, "PR_BOARD_MAX", 15),
	}
	if v, _ := env("NO_COLOR"); v != "" {
		t.NoColor = true
	}
	if v, ok := env("PR_BOARD_LABEL"); ok {
		t.Label = v
	}
	if v, ok := env("PR_BOARD_LINK"); ok {
		t.Link = v
	} else if l, ok := liveLock(p); ok {
		t.Link = l.URL()
	} else {
		t.Link = prboard.Lock{Port: cfg.Port}.URL()
	}
	return t
}

// cmdLine prints the statusline segment from the stored board, then starts
// a detached server when the lock names none, so the label opens a live page.
// It never calls the network and never fails: anything missing prints nothing.
func cmdLine(out io.Writer, p prboard.Paths, env lookupEnv, now time.Time, spawn func() error) {
	st, err := prboard.LoadStore(p.Store())
	if err == nil {
		fmt.Fprint(out, termFromEnv(p, env, now).Segment(st))
	}
	if _, ok := liveLock(p); !ok && spawn != nil {
		_ = spawn()
	}
}

// cmdList prints the block from the stored board; it starts nothing.
func cmdList(out io.Writer, p prboard.Paths, env lookupEnv, now time.Time) {
	st, err := prboard.LoadStore(p.Store())
	if err != nil {
		fmt.Fprintln(os.Stderr, "pr-board:", err)
		return
	}
	fmt.Fprint(out, termFromEnv(p, env, now).List(st))
}
