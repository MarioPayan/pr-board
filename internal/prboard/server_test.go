package prboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func tempPaths(t *testing.T) Paths {
	d := t.TempDir()
	return Paths{Config: filepath.Join(d, "config.json"), State: filepath.Join(d, "state"), Data: filepath.Join(d, "data")}
}

func TestGuard(t *testing.T) {
	s := &Server{port: 4321}
	s.init()
	s.store = &Store{}
	h := s.Handler()
	cases := []struct {
		name, method, path, host, origin string
		header                           bool
		want                             int
	}{
		{"page on 127.0.0.1", "GET", "/", "127.0.0.1:4321", "", false, 200},
		{"api on localhost", "GET", "/api/health", "localhost:4321", "", false, 200},
		{"other host (DNS rebinding)", "GET", "/api/prs", "evil.example:4321", "", false, 403},
		{"right host, wrong port", "GET", "/api/prs", "127.0.0.1:9999", "", false, 403},
		{"POST without X-PR-Board", "POST", "/api/heartbeat", "127.0.0.1:4321", "", false, 403},
		{"POST with header, no origin (CLI)", "POST", "/api/heartbeat", "127.0.0.1:4321", "", true, 200},
		{"POST from own origin (page)", "POST", "/api/heartbeat", "127.0.0.1:4321", "http://127.0.0.1:4321", true, 200},
		{"POST from localhost origin", "POST", "/api/heartbeat", "localhost:4321", "http://localhost:4321", true, 200},
		{"POST from another page", "POST", "/api/cleanup", "127.0.0.1:4321", "http://evil.example", true, 403},
		{"POST from another local port", "POST", "/api/stop", "127.0.0.1:4321", "http://127.0.0.1:8080", true, 403},
		{"POST from https origin", "POST", "/api/stop", "127.0.0.1:4321", "https://127.0.0.1:4321", true, 403},
		{"POST with null origin", "POST", "/api/stop", "127.0.0.1:4321", "null", true, 403},
		{"GET-only route rejects POST", "POST", "/api/prs", "127.0.0.1:4321", "", true, 405},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := strings.NewReader(`{"client":"test"}`)
			r := httptest.NewRequest(c.method, c.path, body)
			r.Host = c.host
			if c.origin != "" {
				r.Header.Set("Origin", c.origin)
			}
			if c.header {
				r.Header.Set("X-PR-Board", "1")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.want {
				t.Errorf("got %d want %d (%s)", w.Code, c.want, strings.TrimSpace(w.Body.String()))
			}
		})
	}
}

func startServer(t *testing.T, p Paths, s *Server) (Lock, chan error) {
	t.Helper()
	s.Paths = p
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if l, err := LiveServer(context.Background(), p.Lock()); err == nil {
			return l, done
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return Lock{}, nil
}

func TestIdleExit(t *testing.T) {
	p := tempPaths(t)
	s := &Server{Idle: 300 * time.Millisecond}
	l, done := startServer(t, p, s)
	// heartbeats keep it alive past the idle time
	c := Client{Lock: l}
	for range 4 {
		time.Sleep(150 * time.Millisecond)
		if err := c.Post(context.Background(), "/api/heartbeat", map[string]string{"client": "page"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-done:
		t.Fatalf("exited while heartbeats came in: %v", err)
	default:
	}
	start := time.Now()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no idle exit")
	}
	if waited := time.Since(start); waited < 200*time.Millisecond {
		t.Errorf("exited after %s, before the idle time", waited)
	}
	if _, err := os.Stat(p.Lock()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lock file left behind: %v", err)
	}
}

func TestSingleton(t *testing.T) {
	p := tempPaths(t)
	s := &Server{Idle: time.Minute}
	l, done := startServer(t, p, s)
	defer func() { s.Stop(); <-done }()

	var spawned atomic.Int32
	spawn := func() error { spawned.Add(1); return nil }
	l2, started, err := Up(context.Background(), p, spawn, time.Second)
	if err != nil || started || spawned.Load() != 0 || l2.Port != l.Port || l2.PID != l.PID {
		t.Fatalf("second up should reuse: lock %+v started %v spawned %d err %v", l2, started, spawned.Load(), err)
	}
	// a second server on the same paths refuses to run
	s2 := &Server{Paths: p, Idle: time.Minute}
	if err := s2.Run(context.Background()); !errors.Is(err, ErrRunning) {
		t.Fatalf("second server: %v", err)
	}
	if l3, err := ReadLock(p.Lock()); err != nil || l3.Port != l.Port {
		t.Fatalf("lock changed by the refused server: %+v %v", l3, err)
	}
}

func TestUpReplacesStaleLock(t *testing.T) {
	p := tempPaths(t)
	os.MkdirAll(p.State, 0o755)
	// a pid that is not running, and a live pid (ours) on a port nobody serves
	for _, stale := range []string{`{"pid":999999999,"port":1,"started":"2026-01-01T00:00:00Z"}`,
		`{"pid":` + strconv.Itoa(os.Getpid()) + `,"port":1,"started":"2026-01-01T00:00:00Z"}`} {
		os.WriteFile(p.Lock(), []byte(stale), 0o644)
		var s *Server
		var done chan error
		spawn := func() error {
			s = &Server{Paths: p, Idle: time.Minute}
			done = make(chan error, 1)
			go func() { done <- s.Run(context.Background()) }()
			return nil
		}
		l, started, err := Up(context.Background(), p, spawn, 5*time.Second)
		if err != nil || !started || l.Port == 1 {
			t.Fatalf("stale %s: lock %+v started %v err %v", stale, l, started, err)
		}
		s.Stop()
		<-done
	}
}

func TestCleanupToken(t *testing.T) {
	p := tempPaths(t)
	merged := openPR(3, "gone", "main")
	merged.State = "MERGED"
	os.MkdirAll(p.Data, 0o755)
	SaveStore(p.Store(), &Store{PRs: []PR{openPR(1, "a", "main"), merged}})
	var deletes atomic.Int32
	run := func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		if name == "gh" && len(args) > 2 && args[1] == "-X" {
			deletes.Add(1)
			return nil, nil
		}
		if name == "gh" {
			return []byte(`{"ref":"refs/heads/gone"}`), nil // branch still exists
		}
		return nil, errors.New("no " + name)
	}
	s := &Server{Idle: time.Minute, Cleaner: &Cleaner{Run: run, Now: time.Now, MinAge: 30 * time.Minute}}
	l, done := startServer(t, p, s)
	defer func() { s.Stop(); <-done }()
	c := Client{Lock: l}
	ctx := context.Background()

	var plan CleanupPlan
	if err := c.Get(ctx, "/api/cleanup", &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Key != "acme/app#3" || len(plan.Items[0].Steps) != 1 || plan.Items[0].Steps[0].Kind != "remote" {
		t.Fatalf("plan: %+v", plan.Items)
	}
	if deletes.Load() != 0 {
		t.Fatal("planning must not delete")
	}
	if err := c.Post(ctx, "/api/cleanup", map[string]string{"token": "made-up"}, nil); err == nil {
		t.Fatal("unknown token accepted")
	}
	if err := c.Post(ctx, "/api/cleanup", map[string]string{"token": plan.Token}, nil); err != nil {
		t.Fatal(err)
	}
	if deletes.Load() != 1 {
		t.Errorf("deletes: %d", deletes.Load())
	}
	if err := c.Post(ctx, "/api/cleanup", map[string]string{"token": plan.Token}, nil); err == nil {
		t.Fatal("token reused")
	}
	st, _ := LoadStore(p.Store())
	if len(st.PRs) != 1 || st.PRs[0].Number != 1 || strings.Join(st.Cleaned, ",") != "acme/app#3" {
		t.Errorf("store after cleanup: %+v", st)
	}
}

func TestStopRemovesLock(t *testing.T) {
	p := tempPaths(t)
	s := &Server{Idle: time.Minute}
	l, done := startServer(t, p, s)
	if err := (Client{Lock: l}).Post(context.Background(), "/api/stop", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop")
	}
	if _, err := os.Stat(p.Lock()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lock left: %v", err)
	}
	if _, err := http.Get(l.URL() + "api/health"); err == nil {
		t.Error("still answering")
	}
}

func TestSetupMessage(t *testing.T) {
	for _, c := range []struct {
		name, setup string
	}{{"no owners", "/home/u/.config/pr-board/config.json"}, {"configured", ""}} {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{port: 4321, Setup: func() string { return c.setup }}
			s.init()
			s.store = &Store{}
			r := httptest.NewRequest("GET", "/api/prs", nil)
			r.Host = "127.0.0.1:4321"
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			var v View
			if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
			if v.Setup != c.setup {
				t.Errorf("setup %q want %q", v.Setup, c.setup)
			}
			if has := strings.Contains(w.Body.String(), `"setup"`); has != (c.setup != "") {
				t.Errorf("setup key present %v: %s", has, w.Body.String())
			}
		})
	}
}
