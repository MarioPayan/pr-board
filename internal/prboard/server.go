package prboard

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"
)

//go:embed web/index.html
var webFS embed.FS

// ErrRunning means another server holds the server lock.
var ErrRunning = errors.New("another pr-board server is running")

const tokenFor = 10 * time.Minute

// Server is the local pr-board server.
type Server struct {
	Paths        Paths
	Collect      func(ctx context.Context, prev *Store) (*Store, error)
	Cleaner      *Cleaner
	Idle         time.Duration
	RefreshEvery time.Duration
	Now          func() time.Time
	Log          *log.Logger
	// Setup returns the config file's path while it names no GitHub owners,
	// else "". The page then shows how to set it up instead of an empty board.
	Setup func() string
	// Port to listen on; 0 picks a free one.
	Port int
	// Ready, when set, is called once the server listens and its lock is written.
	Ready func(Lock)

	mu      sync.Mutex
	store   *Store
	last    time.Time            // last check-in
	clients map[string]time.Time // client -> last heartbeat
	tokens  map[string]token
	port    int
	started time.Time
	stopc   chan struct{}
	stopped sync.Once

	refreshMu sync.Mutex
}

type token struct {
	Keys  []string
	Until time.Time
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) logf(format string, a ...any) {
	if s.Log != nil {
		s.Log.Printf(format, a...)
	}
}

func (s *Server) checkin(client string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = s.now()
	s.clients[client] = s.last
}

// Stop asks Run to return.
func (s *Server) Stop() { s.stopped.Do(func() { close(s.stopc) }) }

func (s *Server) init() {
	s.clients = map[string]time.Time{}
	s.tokens = map[string]token{}
	s.stopc = make(chan struct{})
	if s.Idle == 0 {
		s.Idle = 10 * time.Minute
	}
	if s.RefreshEvery == 0 {
		s.RefreshEvery = 5 * time.Minute
	}
}

// Run takes the server lock, listens on 127.0.0.1, writes the lock file,
// refreshes on start and every RefreshEvery, and returns after Idle without
// a check-in, on Stop, or when ctx ends. The lock file is removed on return.
func (s *Server) Run(ctx context.Context) error {
	s.init()
	if err := os.MkdirAll(s.Paths.State, 0o755); err != nil {
		return err
	}
	fl, err := Flock(s.Paths.Flock())
	if err != nil {
		return ErrRunning
	}
	defer fl.Close()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.Port))
	if err != nil {
		return err
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	st, err := LoadStore(s.Paths.Store())
	if err != nil {
		s.logf("store: %v (starting empty)", err)
	}
	s.store = st
	s.started = s.now()
	lk := Lock{PID: os.Getpid(), Port: s.port, Started: s.started}
	b, _ := json.Marshal(lk)
	if err := WriteFileAtomic(s.Paths.Lock(), b); err != nil {
		ln.Close()
		return err
	}
	defer func() {
		if cur, err := ReadLock(s.Paths.Lock()); err == nil && cur.PID == lk.PID && cur.Port == lk.Port {
			os.Remove(s.Paths.Lock())
		}
	}()
	s.checkin("start")
	s.logf("listening on %s", lk.URL())
	if s.Ready != nil {
		s.Ready(lk)
	}

	hs := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go hs.Serve(ln)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go s.refreshLoop(ctx)

	tick := min(max(s.Idle/10, 10*time.Millisecond), 30*time.Second)
	t := time.NewTicker(tick)
	defer t.Stop()
	reason := ""
	for reason == "" {
		select {
		case <-ctx.Done():
			reason = "context done"
		case <-s.stopc:
			reason = "stop requested"
		case <-t.C:
			s.mu.Lock()
			idle := s.now().Sub(s.last)
			s.mu.Unlock()
			if idle >= s.Idle {
				reason = fmt.Sprintf("idle for %s", idle.Round(time.Second))
			}
		}
	}
	s.logf("exiting: %s", reason)
	sctx, scancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer scancel()
	hs.Shutdown(sctx)
	return nil
}

func (s *Server) refreshLoop(ctx context.Context) {
	for {
		if err := s.Refresh(ctx); err != nil {
			s.logf("refresh: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.RefreshEvery):
		}
	}
}

// Refresh collects once and saves the store. A failed collect keeps the old
// data and records the error for the page.
func (s *Server) Refresh(ctx context.Context) error {
	if s.Collect == nil {
		return nil
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.mu.Lock()
	prev := s.store
	s.mu.Unlock()
	next, err := s.Collect(ctx, prev)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.store.Errors = []string{"refresh failed: " + err.Error()}
		return err
	}
	s.store = next
	return SaveStore(s.Paths.Store(), s.store)
}

// Handler serves the page and the API behind the Host/Origin guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/prs", s.prs)
	mux.HandleFunc("POST /api/refresh", s.refresh)
	mux.HandleFunc("POST /api/heartbeat", s.heartbeat)
	mux.HandleFunc("GET /api/cleanup", s.cleanupPlan)
	mux.HandleFunc("POST /api/cleanup", s.cleanupRun)
	mux.HandleFunc("POST /api/stop", s.stop)
	return s.guard(mux)
}

// guard rejects requests that do not come from this machine's own clients:
// the Host must be the server's own address (blocks DNS rebinding), and a
// POST must carry X-PR-Board: 1 and, when it has an Origin, the server's own
// origin (blocks other web pages from triggering a cleanup).
func (s *Server) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts := []string{fmt.Sprintf("127.0.0.1:%d", s.port), fmt.Sprintf("localhost:%d", s.port)}
		if !slices.Contains(hosts, r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-PR-Board") != "1" {
				http.Error(w, "missing X-PR-Board header", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && !slices.Contains(hosts, trimScheme(o)) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

func trimScheme(origin string) string {
	const p = "http://"
	if len(origin) > len(p) && origin[:len(p)] == p {
		return origin[len(p):]
	}
	return "" // https or anything else is never this server
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	b, _ := webFS.ReadFile("web/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; script-src 'unsafe-inline'; frame-ancestors 'none'")
	w.Write(b)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	clients := map[string]string{}
	for c, t := range s.clients {
		clients[c] = t.Format(time.RFC3339)
	}
	out := map[string]any{"ok": true, "pid": os.Getpid(), "port": s.port, "started": s.started,
		"last_checkin": s.last, "idle_exit": s.last.Add(s.Idle), "clients": clients}
	s.mu.Unlock()
	writeJSON(w, 200, out)
}

func (s *Server) view() View {
	setup := ""
	if s.Setup != nil {
		setup = s.Setup()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := BuildView(s.store, s.now())
	v.Setup = setup
	return v
}

func (s *Server) prs(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.view()) }

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	if err := s.Refresh(r.Context()); err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, s.view())
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Client string `json:"client"`
	}
	if err := readJSON(r, &in); err != nil || in.Client == "" {
		http.Error(w, "want {client}", 400)
		return
	}
	s.checkin(in.Client)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) merged() []PR {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PR
	for _, p := range s.store.PRs {
		if p.State == "MERGED" {
			out = append(out, p)
		}
	}
	return out
}

// CleanupPlan is GET /api/cleanup.
type CleanupPlan struct {
	Token string     `json:"token"`
	Items []PlanItem `json:"items"`
}

func newToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) cleanupPlan(w http.ResponseWriter, r *http.Request) {
	if s.Cleaner == nil {
		writeJSON(w, 501, map[string]string{"error": "cleanup is not configured"})
		return
	}
	items := s.Cleaner.Plan(r.Context(), s.merged())
	keys := []string{}
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	t := newToken()
	s.mu.Lock()
	for k, v := range s.tokens {
		if s.now().After(v.Until) {
			delete(s.tokens, k)
		}
	}
	s.tokens[t] = token{Keys: keys, Until: s.now().Add(tokenFor)}
	s.mu.Unlock()
	writeJSON(w, 200, CleanupPlan{Token: t, Items: items})
}

// cleanupRun spends a one-time token: it re-plans the PRs that token covers
// (so the skip rules see the current disk), runs the plan, and drops every
// PR whose steps all ran.
func (s *Server) cleanupRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &in); err != nil || in.Token == "" {
		http.Error(w, "want {token}", 400)
		return
	}
	s.mu.Lock()
	tk, ok := s.tokens[in.Token]
	delete(s.tokens, in.Token)
	s.mu.Unlock()
	if !ok || s.now().After(tk.Until) || s.Cleaner == nil {
		writeJSON(w, 409, map[string]string{"error": "unknown or expired cleanup token; ask for the plan again"})
		return
	}
	var prs []PR
	for _, p := range s.merged() {
		if slices.Contains(tk.Keys, p.Key()) {
			prs = append(prs, p)
		}
	}
	items := s.Cleaner.Execute(r.Context(), s.Cleaner.Plan(r.Context(), prs))
	drop := map[string]bool{}
	for _, it := range items {
		if !it.Kept() {
			drop[it.Key] = true
		}
	}
	s.mu.Lock()
	kept := s.store.PRs[:0:0]
	for _, p := range s.store.PRs {
		if drop[p.Key()] {
			s.store.Cleaned = append(s.store.Cleaned, p.Key())
			continue
		}
		kept = append(kept, p)
	}
	s.store.PRs = kept
	err := SaveStore(s.Paths.Store(), s.store)
	s.mu.Unlock()
	if err != nil {
		s.logf("save after cleanup: %v", err)
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"ok": true})
	go s.Stop()
}
