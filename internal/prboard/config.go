package prboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is ~/.config/pr-board/config.json.
type Config struct {
	Owners      []string `json:"owners"`
	Author      string   `json:"author"`
	MergedDays  int      `json:"merged_days"`
	CloneRoots  []string `json:"clone_roots"`
	IdleMinutes int      `json:"idle_minutes"`
}

// DefaultConfig is written when the config file is missing. It names no
// owners: the page asks for them until the config lists at least one.
func DefaultConfig() Config {
	return Config{
		Owners:      []string{},
		Author:      "@me",
		MergedDays:  7,
		CloneRoots:  []string{"~/repos", "~/src", "~/code"},
		IdleMinutes: 10,
	}
}

// Paths holds every file pr-board reads or writes. Tests point it at a temp dir.
type Paths struct {
	Config string // ~/.config/pr-board/config.json
	State  string // ~/.local/state/pr-board
	Data   string // ~/.local/share/pr-board
}

// DefaultPaths resolves the XDG-style locations under $HOME.
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return PathsUnder(home, "pr-board"), nil
}

// PathsUnder is the config, state and data locations of the tool called name
// in home.
func PathsUnder(home, name string) Paths {
	return Paths{
		Config: filepath.Join(home, ".config", name, "config.json"),
		State:  filepath.Join(home, ".local", "state", name),
		Data:   filepath.Join(home, ".local", "share", name),
	}
}

// Migrate moves the config file and the data directory (prs.json with the
// cleaned-PR memory) from old to cur when cur has none and old has one. The
// state directory is not moved: it holds the lock and log of a server that
// may still be running under the old name. It returns one line per move.
func Migrate(old, cur Paths) ([]string, error) {
	var moved []string
	move := func(from, to string) error {
		if _, err := os.Lstat(to); !errors.Is(err, os.ErrNotExist) {
			return err // exists already, or cannot tell
		}
		if _, err := os.Lstat(from); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		moved = append(moved, from+" -> "+to)
		return nil
	}
	if err := move(old.Config, cur.Config); err != nil {
		return moved, err
	}
	os.Remove(filepath.Dir(old.Config)) // only succeeds when it is now empty
	if err := move(old.Data, cur.Data); err != nil {
		return moved, err
	}
	return moved, nil
}

func (p Paths) Lock() string  { return filepath.Join(p.State, "server.json") }
func (p Paths) Flock() string { return filepath.Join(p.State, "server.lock") }
func (p Paths) Log() string   { return filepath.Join(p.State, "server.log") }
func (p Paths) Loops() string { return filepath.Join(p.State, "loops") }
func (p Paths) Store() string { return filepath.Join(p.Data, "prs.json") }

// LoadConfig reads the config, writing the defaults first when it is missing.
// Missing fields fall back to their defaults.
func LoadConfig(path string) (Config, error) {
	def := DefaultConfig()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		out, _ := json.MarshalIndent(def, "", "  ")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return def, err
		}
		return def, WriteFileAtomic(path, append(out, '\n'))
	}
	if err != nil {
		return def, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return def, fmt.Errorf("%s: %w", path, err)
	}
	if c.Owners == nil {
		c.Owners = []string{}
	}
	if c.Author == "" {
		c.Author = def.Author
	}
	if c.MergedDays <= 0 {
		c.MergedDays = def.MergedDays
	}
	if c.CloneRoots == nil {
		c.CloneRoots = def.CloneRoots
	}
	if c.IdleMinutes <= 0 {
		c.IdleMinutes = def.IdleMinutes
	}
	return c, nil
}

// ExpandHome turns a leading ~ into $HOME.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// WriteFileAtomic writes to a temp file in the same directory and renames it.
func WriteFileAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
