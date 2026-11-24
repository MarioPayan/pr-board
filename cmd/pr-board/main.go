// Command pr-board serves a local map of your open GitHub pull requests. See README.md.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/MarioPayan/pr-board/internal/prboard"
)

const usage = `usage:
  pr-board [up] [--session ID] [--open]   start the server or reuse the running one; print its URL
  pr-board refresh                        collect from GitHub now
  pr-board cleanup [--dry-run] [--yes]    clean up merged PRs (asks y/N unless --yes)
  pr-board status                         show the running server
  pr-board line                           statusline segment from the stored board; never waits
  pr-board list                           your open PRs and the ones to review, one row each
  pr-board legend                         what the segment and the list's glyphs mean
  pr-board stop                           stop the running server
  pr-board collect --json                 collect once without a server and print the store
  pr-board demo [--port N] [--open]       serve a fictional board (no GitHub, no files touched) until Ctrl-C
  pr-board session-loop --session ID      check in for a Claude session until it ends
  pr-board hook session-start             SessionStart hook: reads the payload on stdin, always exits 0
`

func main() {
	args := os.Args[1:]
	cmd := "up"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	} else if len(args) > 0 && args[0] == "--demo" {
		cmd, args = "demo", args[1:]
	}
	switch cmd {
	case "hook":
		hook(args, os.Stdin) // never fails the session
		return
	case "line":
		// a statusline segment: never waits, never shouts
		p, err := defaultPaths(io.Discard)
		if err == nil {
			cmdLine(os.Stdout, p, os.LookupEnv, time.Now(), func() error { return prboard.SpawnDetached(p.Log(), "serve") })
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch cmd {
	case "up":
		err = cmdUp(ctx, args, os.Stdout)
	case "serve":
		err = cmdServe(ctx)
	case "refresh":
		err = cmdRefresh(ctx, os.Stdout)
	case "cleanup":
		err = cmdCleanup(ctx, args, os.Stdin, os.Stdout)
	case "status":
		err = cmdStatus(ctx, os.Stdout)
	case "stop":
		err = cmdStop(ctx, os.Stdout)
	case "collect":
		err = cmdCollect(ctx, args, os.Stdout)
	case "demo":
		err = cmdDemo(ctx, args, os.Stdout)
	case "list":
		cmdList(os.Stdout, paths(), os.LookupEnv, time.Now())
	case "legend", "key":
		fmt.Print(termFromEnv(prboard.Paths{}, os.LookupEnv, time.Now()).Legend())
	case "session-loop":
		err = cmdSessionLoop(ctx, args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pr-board:", err)
		os.Exit(1)
	}
}

func paths() prboard.Paths {
	p, err := defaultPaths(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pr-board:", err)
		os.Exit(1)
	}
	return p
}

// defaultPaths resolves the paths under $HOME and moves the files the tool
// kept under its old name, pr-map, on the first run after the rename.
func defaultPaths(log io.Writer) (prboard.Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return prboard.Paths{}, err
	}
	p := prboard.PathsUnder(home, "pr-board")
	moved, err := prboard.Migrate(prboard.PathsUnder(home, "pr-map"), p)
	for _, m := range moved {
		fmt.Fprintln(log, "pr-board: moved", m)
	}
	if err != nil {
		fmt.Fprintln(log, "pr-board: moving the old pr-map files:", err)
	}
	return p, nil
}

func config(p prboard.Paths) prboard.Config {
	c, err := prboard.LoadConfig(p.Config)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pr-board: config:", err, "(using defaults)")
	}
	return c
}

func up(ctx context.Context, p prboard.Paths) (prboard.Lock, bool, error) {
	return prboard.Up(ctx, p, func() error { return prboard.SpawnDetached(p.Log(), "serve") }, 10*time.Second)
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	return fs
}

func cmdUp(ctx context.Context, args []string, out io.Writer) error {
	fs := newFlags("up")
	session := fs.String("session", "", "Claude session id to check in for")
	open := fs.Bool("open", false, "open the page in a browser")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p := paths()
	l, started, err := up(ctx, p)
	if err != nil {
		return err
	}
	if *session != "" {
		c := prboard.Client{Lock: l}
		if err := c.Post(ctx, "/api/heartbeat", map[string]string{"client": "session:" + *session}, nil); err != nil {
			return err
		}
	}
	verb := "running"
	if started {
		verb = "started"
	}
	fmt.Fprintf(out, "%s (%s, pid %d)\n", l.URL(), verb, l.PID)
	if *open {
		openBrowser(l.URL())
	}
	return nil
}

func cmdServe(ctx context.Context) error {
	p := paths()
	cfg := config(p)
	lg := log.New(os.Stderr, "pr-board ", log.LstdFlags)
	s := &prboard.Server{
		Paths: p,
		// read the config on every refresh, so owners added to it count
		// without a restart
		Collect: func(ctx context.Context, prev *prboard.Store) (*prboard.Store, error) {
			return prboard.Collector{Cfg: config(p), Run: prboard.ExecRunner, Now: time.Now}.Collect(ctx, prev)
		},
		Setup:   func() string { return setupNeeded(p) },
		Cleaner: cleaner(cfg),
		Idle:    time.Duration(cfg.IdleMinutes) * time.Minute,
		Port:    cfg.Port,
		Log:     lg,
	}
	err := s.Run(ctx)
	if errors.Is(err, prboard.ErrRunning) {
		lg.Print(err)
		return nil
	}
	return err
}

// setupNeeded is the config path while the config names no owners, else "".
func setupNeeded(p prboard.Paths) string {
	if c, err := prboard.LoadConfig(p.Config); err == nil && len(c.Owners) == 0 {
		return p.Config
	}
	return ""
}

func cleaner(cfg prboard.Config) *prboard.Cleaner {
	var roots []string
	for _, r := range cfg.CloneRoots {
		roots = append(roots, prboard.ExpandHome(r))
	}
	return &prboard.Cleaner{
		Run: prboard.ExecRunner, Roots: roots, Depth: 3, Now: time.Now, MinAge: 30 * time.Minute,
		Sessions: prboard.LiveSessionCwds(prboard.ExecRunner),
	}
}

func client(ctx context.Context) (prboard.Client, error) {
	l, _, err := up(ctx, paths())
	return prboard.Client{Lock: l}, err
}

func cmdRefresh(ctx context.Context, out io.Writer) error {
	c, err := client(ctx)
	if err != nil {
		return err
	}
	var v prboard.View
	if err := c.Post(ctx, "/api/refresh", nil, &v); err != nil {
		return err
	}
	fmt.Fprintf(out, "%d open, %d merged\n", v.Open, v.Merged)
	return nil
}

func cmdCleanup(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	fs := newFlags("cleanup")
	dry := fs.Bool("dry-run", false, "print the plan only")
	yes := fs.Bool("yes", false, "do not ask")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := client(ctx)
	if err != nil {
		return err
	}
	var plan prboard.CleanupPlan
	if err := c.Get(ctx, "/api/cleanup", &plan); err != nil {
		return err
	}
	printPlan(out, plan.Items)
	if *dry || len(plan.Items) == 0 {
		return nil
	}
	if !*yes {
		fmt.Fprint(out, "Clean up these PRs? [y/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "nothing done")
			return nil
		}
	}
	var res struct {
		Items []prboard.PlanItem `json:"items"`
	}
	if err := c.Post(ctx, "/api/cleanup", map[string]string{"token": plan.Token}, &res); err != nil {
		return err
	}
	for _, it := range res.Items {
		fmt.Fprintf(out, "%s#%d\n", it.Repo, it.N)
		for _, s := range it.Steps {
			fmt.Fprintf(out, "  %s: %s\n", s.Describe(), s.Result)
		}
		if it.Kept() {
			fmt.Fprintln(out, "  kept on the map")
		}
	}
	return nil
}

func printPlan(out io.Writer, items []prboard.PlanItem) {
	if len(items) == 0 {
		fmt.Fprintln(out, "no merged PRs on the map")
		return
	}
	fmt.Fprintf(out, "Clean up %d merged PRs:\n", len(items))
	for _, it := range items {
		fmt.Fprintf(out, "%s#%d %s\n", it.Repo, it.N, it.Title)
		if len(it.Steps) == 0 {
			fmt.Fprintln(out, "  remove from the map only (no branch left on GitHub or locally)")
		}
		for _, s := range it.Steps {
			fmt.Fprintf(out, "  %s\n", s.Describe())
		}
	}
}

func cmdStatus(ctx context.Context, out io.Writer) error {
	l, err := prboard.LiveServer(ctx, paths().Lock())
	if err != nil {
		fmt.Fprintln(out, "not running:", err)
		return nil
	}
	var h map[string]any
	if err := (prboard.Client{Lock: l}).Get(ctx, "/api/health", &h); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s pid %d, started %s, exits when idle after %v\n", l.URL(), l.PID, l.Started.Local().Format("15:04:05"), h["idle_exit"])
	if cl, ok := h["clients"].(map[string]any); ok {
		for k, v := range cl {
			fmt.Fprintf(out, "  %s last check-in %v\n", k, v)
		}
	}
	return nil
}

func cmdStop(ctx context.Context, out io.Writer) error {
	p := paths()
	l, err := prboard.LiveServer(ctx, p.Lock())
	if err != nil {
		fmt.Fprintln(out, "not running")
		return nil
	}
	if err := (prboard.Client{Lock: l}).Post(ctx, "/api/stop", nil, nil); err != nil {
		return err
	}
	for range 50 {
		if _, err := os.Stat(p.Lock()); errors.Is(err, os.ErrNotExist) && !prboard.PIDAlive(l.PID) {
			fmt.Fprintf(out, "stopped pid %d\n", l.PID)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("pid %d did not stop within 5s", l.PID)
}

func cmdCollect(ctx context.Context, args []string, out io.Writer) error {
	fs := newFlags("collect")
	asJSON := fs.Bool("json", false, "print the store as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*asJSON {
		return errors.New("collect needs --json")
	}
	p := paths()
	prev, _ := prboard.LoadStore(p.Store())
	col := prboard.Collector{Cfg: config(p), Run: prboard.ExecRunner, Now: time.Now}
	st, err := col.Collect(ctx, prev)
	if err != nil {
		return err
	}
	type row struct {
		prboard.PR
		Derived prboard.Derived `json:"derived"`
	}
	rows := []row{}
	for _, pr := range st.PRs {
		rows = append(rows, row{PR: pr, Derived: prboard.Derive(pr, time.Now())})
	}
	errs := st.Errors
	if errs == nil {
		errs = []string{}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"refreshed": st.Refreshed, "errors": errs, "prs": rows, "view": prboard.BuildView(st, time.Now())})
}

// cmdDemo serves the built-in fictional board from a temporary directory,
// apart from the real server, its lock and its data. It runs in the
// foreground until interrupted; cleanup is not available.
func cmdDemo(ctx context.Context, args []string, out io.Writer) error {
	fs := newFlags("demo")
	port := fs.Int("port", 0, "port to listen on (0 picks a free one)")
	open := fs.Bool("open", false, "open the page in a browser")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "pr-board-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	p := prboard.PathsUnder(dir, "pr-board")
	// the board is there before the first refresh runs
	if err := prboard.SaveStore(p.Store(), prboard.DemoStore(time.Now())); err != nil {
		return err
	}
	s := &prboard.Server{
		Paths: p,
		Port:  *port,
		Idle:  24 * time.Hour,
		Collect: func(ctx context.Context, prev *prboard.Store) (*prboard.Store, error) {
			st := prboard.DemoStore(time.Now())
			if prev != nil {
				st.Cleaned = prev.Cleaned
			}
			return st, nil
		},
		Ready: func(l prboard.Lock) {
			fmt.Fprintf(out, "%s (demo, Ctrl-C to stop)\n", l.URL())
			if *open {
				openBrowser(l.URL())
			}
		},
	}
	return s.Run(ctx)
}

func openBrowser(url string) {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	_ = exec.Command(opener, url).Start()
}

func cmdSessionLoop(ctx context.Context, args []string) error {
	fs := newFlags("session-loop")
	session := fs.String("session", "", "Claude session id")
	every := fs.Duration("every", 3*time.Minute, "check-in interval")
	once := fs.Bool("once", false, "run one tick and exit (for testing)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !prboard.ValidSession(*session) {
		return fmt.Errorf("invalid --session %q", *session)
	}
	p := paths()
	l := prboard.Loop{
		Paths: p, Session: *session, Run: prboard.ExecRunner, Every: *every,
		Up: func(ctx context.Context) (prboard.Lock, error) {
			lk, _, err := up(ctx, p)
			return lk, err
		},
		Log: log.New(os.Stderr, "pr-board loop ", log.LstdFlags),
	}
	if *once {
		return l.Tick(ctx)
	}
	return l.Main(ctx)
}

// hook handles Claude Code hook events. It must never block or fail the
// session, so every error is swallowed.
func hook(args []string, in io.Reader) {
	if len(args) == 0 || args[0] != "session-start" {
		return
	}
	var payload struct {
		SessionID string `json:"session_id"`
	}
	b, _ := io.ReadAll(io.LimitReader(in, 1<<20))
	if json.Unmarshal(b, &payload) != nil || !prboard.ValidSession(payload.SessionID) {
		return
	}
	p, err := defaultPaths(io.Discard)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if l, _, err := up(ctx, p); err == nil {
		_ = prboard.Client{Lock: l}.Post(ctx, "/api/heartbeat", map[string]string{"client": "session:" + payload.SessionID}, nil)
	}
	_ = prboard.SpawnDetached(p.Log(), "session-loop", "--session", payload.SessionID)
}
