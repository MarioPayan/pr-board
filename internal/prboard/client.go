package prboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Client talks to a running server.
type Client struct {
	Lock Lock
	HTTP *http.Client
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (c Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.Lock.URL(), "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-PR-Board", "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s %s: %s", method, path, e.Error)
		}
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// Get calls a GET endpoint.
func (c Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// Post calls a POST endpoint.
func (c Client) Post(ctx context.Context, path string, in, out any) error {
	return c.do(ctx, http.MethodPost, path, in, out)
}

// Up returns the running server, starting one with spawn when there is none
// or the lock is stale. started reports whether spawn ran.
func Up(ctx context.Context, p Paths, spawn func() error, wait time.Duration) (l Lock, started bool, err error) {
	if l, err := LiveServer(ctx, p.Lock()); err == nil {
		return l, false, nil
	}
	if err := spawn(); err != nil {
		return Lock{}, false, fmt.Errorf("start server: %w", err)
	}
	deadline := time.Now().Add(wait)
	for {
		if l, err := LiveServer(ctx, p.Lock()); err == nil {
			return l, true, nil
		}
		if time.Now().After(deadline) {
			return Lock{}, true, fmt.Errorf("server did not answer within %s; see %s", wait, p.Log())
		}
		select {
		case <-ctx.Done():
			return Lock{}, true, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// SpawnDetached starts this binary with args in its own session, output
// appended to logPath, and does not wait for it.
func SpawnDetached(logPath string, args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dirOf(logPath), 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devnull.Close()
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, lf, lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "."
}

// ErrNotRunning is returned by commands that never start a server.
var ErrNotRunning = errors.New("pr-board is not running")
