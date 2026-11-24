package prboard

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

var sessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// ValidSession reports whether id is safe to use in a file name.
func ValidSession(id string) bool { return sessionID.MatchString(id) }

// Loop is `pr-board session-loop`: it checks in for one Claude session until
// the session is gone.
type Loop struct {
	Paths   Paths
	Session string
	Run     Runner
	Every   time.Duration
	Up      func(ctx context.Context) (Lock, error)
	Log     *log.Logger
}

func (l Loop) logf(format string, a ...any) {
	if l.Log != nil {
		l.Log.Printf(format, a...)
	}
}

func (l Loop) client() string { return "session:" + l.Session }

// sessionAlive asks `claude agents --json` whether the session is listed.
func (l Loop) sessionAlive(ctx context.Context) (bool, error) {
	agents, err := ClaudeAgents(ctx, l.Run)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(agents, func(a Agent) bool { return a.SessionID == l.Session }), nil
}

// Main runs until the session is gone or ctx ends. A second loop for the
// same session exits at once.
func (l Loop) Main(ctx context.Context) error {
	if !ValidSession(l.Session) {
		return fmt.Errorf("invalid session id %q", l.Session)
	}
	if err := os.MkdirAll(l.Paths.Loops(), 0o755); err != nil {
		return err
	}
	fl, err := Flock(filepath.Join(l.Paths.Loops(), l.Session+".lock"))
	if err != nil {
		l.logf("session %s: a loop already runs", l.Session)
		return nil
	}
	defer fl.Close()
	fails := 0
	for tick := 0; ; tick++ {
		// the session may not be listed yet when SessionStart fires
		if tick > 0 {
			alive, err := l.sessionAlive(ctx)
			switch {
			case err != nil:
				if fails++; fails >= 3 {
					return fmt.Errorf("claude agents failed 3 times: %w", err)
				}
			case !alive:
				l.logf("session %s is gone", l.Session)
				return nil
			default:
				fails = 0
			}
		}
		if err := l.Tick(ctx); err != nil {
			l.logf("session %s: %v", l.Session, err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(l.Every):
		}
	}
}

// Tick checks in once, starting the server when none runs.
func (l Loop) Tick(ctx context.Context) error {
	lk, err := l.Up(ctx)
	if err != nil {
		return err
	}
	return Client{Lock: lk}.Post(ctx, "/api/heartbeat", map[string]string{"client": l.client()}, nil)
}
