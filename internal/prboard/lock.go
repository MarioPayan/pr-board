package prboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"syscall"
	"time"
)

// Lock is ~/.local/state/pr-board/server.json.
type Lock struct {
	PID     int       `json:"pid"`
	Port    int       `json:"port"`
	Started time.Time `json:"started"`
}

// URL is the page address.
func (l Lock) URL() string { return fmt.Sprintf("http://127.0.0.1:%d/", l.Port) }

// ReadLock reads the lock file.
func ReadLock(path string) (Lock, error) {
	var l Lock
	b, err := os.ReadFile(path)
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return l, err
	}
	if l.PID <= 0 || l.Port <= 0 {
		return l, errors.New("lock file without pid or port")
	}
	return l, nil
}

// PIDAlive reports whether a process with that pid exists.
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Healthy asks the server at l whether it is the pr-board server with that pid.
func Healthy(ctx context.Context, l Lock) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/health", l.Port), nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var h struct {
		OK  bool `json:"ok"`
		PID int  `json:"pid"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&h) != nil {
		return false
	}
	return h.OK && h.PID == l.PID
}

// LiveServer returns the running server's lock, or an error when the lock is
// missing or stale (dead pid, or no health answer from it).
func LiveServer(ctx context.Context, path string) (Lock, error) {
	l, err := ReadLock(path)
	if err != nil {
		return l, err
	}
	if !PIDAlive(l.PID) {
		return l, fmt.Errorf("stale lock: pid %d is gone", l.PID)
	}
	if !Healthy(ctx, l) {
		return l, fmt.Errorf("stale lock: pid %d does not answer on port %d", l.PID, l.Port)
	}
	return l, nil
}

// Flock takes an exclusive, non-blocking lock on path. Only one server (or
// one session loop per session) holds it; the OS releases it on exit.
func Flock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
