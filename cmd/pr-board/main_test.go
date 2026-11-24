package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MarioPayan/pr-board/internal/prboard"
)

func TestHookIgnoresBadInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, c := range []struct {
		name, args, stdin string
	}{
		{"not json", "session-start", "garbage"},
		{"no session id", "session-start", `{"cwd":"/x"}`},
		{"unsafe session id", "session-start", `{"session_id":"../../etc"}`},
		{"unknown event", "other", `{"session_id":"abc"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			hook(strings.Fields(c.args), strings.NewReader(c.stdin)) // must return, never exit
		})
	}
	if _, err := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(err) {
		t.Errorf("hook started something on bad input: %v", err)
	}
}

func TestDefaultPathsMovesTheOldPrMapFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldCfg := filepath.Join(home, ".config", "pr-map", "config.json")
	oldStore := filepath.Join(home, ".local", "share", "pr-map", "prs.json")
	for path, body := range map[string]string{oldCfg: `{"owners":["acme"]}`, oldStore: `{"prs":[],"cleaned":["acme/web#3"]}`} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var log strings.Builder
	p, err := defaultPaths(&log)
	if err != nil {
		t.Fatal(err)
	}
	if p.Config != filepath.Join(home, ".config", "pr-board", "config.json") || p.Store() != filepath.Join(home, ".local", "share", "pr-board", "prs.json") {
		t.Fatalf("paths %+v", p)
	}
	for _, f := range []string{p.Config, p.Store()} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s not moved: %v", f, err)
		}
	}
	if strings.Count(log.String(), "moved") != 2 {
		t.Errorf("log: %q", log.String())
	}
}

func TestSetupNeededUntilOwnersAreSet(t *testing.T) {
	d := t.TempDir()
	p := prboard.PathsUnder(d, "pr-board")
	if got := setupNeeded(p); got != p.Config {
		t.Errorf("fresh config: setup %q want %q", got, p.Config)
	}
	if err := os.WriteFile(p.Config, []byte(`{"owners":["acme"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := setupNeeded(p); got != "" {
		t.Errorf("configured: setup %q", got)
	}
}

// lineWriter sends each write to a channel.
type lineWriter chan string

func (w lineWriter) Write(b []byte) (int, error) { w <- string(b); return len(b), nil }

func TestDemoServesTheFictionalBoard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	out := make(lineWriter, 4)
	done := make(chan error, 1)
	go func() { done <- cmdDemo(ctx, nil, out) }()
	var url string
	select {
	case line := <-out:
		url = strings.Fields(line)[0]
	case err := <-done:
		t.Fatalf("demo exited: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("demo did not start")
	}
	resp, err := http.Get(url + "api/prs")
	if err != nil {
		t.Fatal(err)
	}
	var v prboard.View
	err = json.NewDecoder(resp.Body).Decode(&v)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if v.Open < 10 || v.Merged < 4 || v.Setup != "" || len(v.Repos) != 6 || len(v.Categories) != 7 || !strings.HasPrefix(v.Repos[0].Key, "acme/") {
		t.Errorf("demo view: open %d merged %d setup %q repos %d categories %d", v.Open, v.Merged, v.Setup, len(v.Repos), len(v.Categories))
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("demo stop: %v", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("demo wrote into HOME: %v", entries)
	}
}
