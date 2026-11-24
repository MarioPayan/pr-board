package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MarioPayan/pr-board/internal/prboard"
)

func envOf(m map[string]string) lookupEnv {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := prboard.WriteFileAtomic(path, b); err != nil {
		t.Fatal(err)
	}
}

func TestLine(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	store := &prboard.Store{Refreshed: now.Add(-time.Minute), PRs: []prboard.PR{{
		Repo: "acme/web", Number: 1, Title: "t", State: "OPEN", Base: "main", Head: "f", DefaultBranch: "main",
		Mergeable: "MERGEABLE", MergeState: "CLEAN", ReviewDecision: "APPROVED", ChecksTotal: 1, UpdatedAt: now,
	}}}
	utf := map[string]string{"LANG": "en_US.UTF-8"}
	link := func(url string) string {
		return "\x1b]8;;" + url + "\x1b\\\x1b[36m[PRs]\x1b[0m\x1b]8;;\x1b\\ \x1b[32m✓1\x1b[0m"
	}
	cases := []struct {
		name      string
		env       map[string]string
		lock      bool // a live server holds the lock on port 9123
		config    string
		noStore   bool
		want      string
		wantSpawn bool
	}{
		{name: "running server: links its port, starts nothing", env: utf, lock: true, want: link("http://127.0.0.1:9123/")},
		{name: "no server: links the configured port and starts one", env: utf, config: `{"owners":["acme"],"port":7979}`, want: link("http://127.0.0.1:7979/"), wantSpawn: true},
		{name: "no server, no config: the default port", env: utf, want: link("http://127.0.0.1:7878/"), wantSpawn: true},
		{name: "PR_BOARD_LINK changes the target", env: map[string]string{"LANG": "C.UTF-8", "PR_BOARD_LINK": "https://example.test/b"}, lock: true, want: link("https://example.test/b")},
		{name: "PR_BOARD_LINK empty unlinks it", env: map[string]string{"LANG": "C.UTF-8", "PR_BOARD_LINK": ""}, lock: true, want: "\x1b[36m[PRs]\x1b[0m \x1b[32m✓1\x1b[0m"},
		{name: "NO_COLOR: no escape codes at all", env: map[string]string{"LANG": "C.UTF-8", "NO_COLOR": "1"}, lock: true, want: "[PRs] ✓1"},
		{name: "a non-UTF-8 locale falls back to ASCII", env: map[string]string{"LANG": "C", "NO_COLOR": "1"}, lock: true, want: "[PRs] +1"},
		{name: "PR_BOARD_ASCII=0 forbids the fallback", env: map[string]string{"LANG": "C", "NO_COLOR": "1", "PR_BOARD_ASCII": "0"}, lock: true, want: "[PRs] ✓1"},
		{name: "PR_BOARD_ASCII=1 forces it", env: map[string]string{"LANG": "C.UTF-8", "NO_COLOR": "1", "PR_BOARD_ASCII": "1"}, lock: true, want: "[PRs] +1"},
		{name: "PR_BOARD_LABEL renames the label", env: map[string]string{"LANG": "C.UTF-8", "NO_COLOR": "1", "PR_BOARD_LABEL": "PR"}, lock: true, want: "PR ✓1"},
		{name: "no store yet prints nothing, still starts the server", env: utf, noStore: true, want: "", wantSpawn: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := prboard.PathsUnder(t.TempDir(), "pr-board")
			if !c.noStore {
				if err := prboard.SaveStore(p.Store(), store); err != nil {
					t.Fatal(err)
				}
			}
			if c.lock {
				writeJSON(t, p.Lock(), prboard.Lock{PID: os.Getpid(), Port: 9123, Started: now})
			}
			if c.config != "" {
				if err := prboard.WriteFileAtomic(p.Config, []byte(c.config)); err != nil {
					t.Fatal(err)
				}
			}
			var out strings.Builder
			spawned := false
			cmdLine(&out, p, envOf(c.env), now, func() error { spawned = true; return nil })
			if out.String() != c.want {
				t.Errorf("got  %q\nwant %q", out.String(), c.want)
			}
			if spawned != c.wantSpawn {
				t.Errorf("spawned %v, want %v", spawned, c.wantSpawn)
			}
			if _, err := os.Stat(p.Config); c.config == "" && !os.IsNotExist(err) {
				t.Errorf("line wrote a config file: %v", err)
			}
		})
	}
}

func TestLineStaleLockStartsAServer(t *testing.T) {
	p := prboard.PathsUnder(t.TempDir(), "pr-board")
	writeJSON(t, p.Lock(), prboard.Lock{PID: 1 << 30, Port: 9123, Started: time.Now()})
	spawned := false
	cmdLine(&strings.Builder{}, p, envOf(nil), time.Now(), func() error { spawned = true; return nil })
	if !spawned {
		t.Error("a lock with a dead pid did not start a server")
	}
}

func TestListFromTheStore(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	p := prboard.PathsUnder(t.TempDir(), "pr-board")
	if err := prboard.WriteFileAtomic(p.Config, []byte(`{"owners":["acme"]}`)); err != nil {
		t.Fatal(err)
	}
	st := &prboard.Store{Refreshed: now, PRs: []prboard.PR{{Repo: "acme/web", Number: 7, Title: "fix it", State: "OPEN", Mergeable: "MERGEABLE", ChecksFailing: []string{"unit"}, UpdatedAt: now}}}
	if err := prboard.SaveStore(p.Store(), st); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	cmdList(&out, p, envOf(map[string]string{"LANG": "C.UTF-8", "NO_COLOR": "1"}), now)
	want := "⑂ acme  1 mine · 0 to review\n mine   ✗· web          7     fix it\n"
	if out.String() != want {
		t.Errorf("got\n%q\nwant\n%q", out.String(), want)
	}
}
