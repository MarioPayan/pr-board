package prboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoopExitsWhenSessionGone(t *testing.T) {
	p := tempPaths(t)
	s := &Server{Idle: time.Minute}
	lk, done := startServer(t, p, s)
	defer func() { s.Stop(); <-done }()

	var agentCalls, ticks atomic.Int32
	run := func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		if name != "claude" {
			return nil, errors.New("unexpected " + name)
		}
		if agentCalls.Add(1) < 3 {
			return []byte(`[{"sessionId":"sess-2","cwd":"/x"},{"sessionId":"other"}]`), nil
		}
		return []byte(`[{"sessionId":"other"}]`), nil
	}
	l := Loop{Paths: p, Session: "sess-2", Run: run, Every: 10 * time.Millisecond,
		Up: func(ctx context.Context) (Lock, error) { ticks.Add(1); return lk, nil }}
	errc := make(chan error, 1)
	go func() { errc <- l.Main(context.Background()) }()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not exit when the session left claude agents")
	}
	if ticks.Load() != 3 {
		t.Errorf("ticks: %d (first tick runs before any agents check, then two while listed)", ticks.Load())
	}
	s.mu.Lock()
	_, ok := s.clients["session:sess-2"]
	s.mu.Unlock()
	if !ok {
		t.Error("no heartbeat from the session")
	}
	// a second loop for the same session exits at once
	fl, err := Flock(p.Loops() + "/sess-2.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer fl.Close()
	if err := (Loop{Paths: p, Session: "sess-2"}).Main(context.Background()); err != nil {
		t.Errorf("duplicate loop: %v", err)
	}
}

func TestLoadStoreIgnoresOldAINotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prs.json")
	old := `{"prs":[{"repo":"acme/app","number":1,"state":"OPEN"}],
"ai":{"acme/app#1":{"hash":"h","merge_after":[{"repo":"","n":2,"reason":"x"}],"not_meant_to_merge":true,"effort":"Checkout"}},
"cleaned":["acme/app#3"]}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.PRs) != 1 || s.PRs[0].Key() != "acme/app#1" || len(s.Cleaned) != 1 {
		t.Fatalf("store: %+v", s)
	}
	if d := Derive(s.PRs[0], now); d.State != StApprove || d.Label == "Parked" {
		t.Errorf("an old not-meant-to-merge note must not park the PR: %+v", d)
	}
}
