package prboard

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMigrateMovesConfigAndStore(t *testing.T) {
	home := t.TempDir()
	old, cur := PathsUnder(home, "old-name"), PathsUnder(home, "pr-board")
	writeFile(t, old.Config, `{"owners":["acme"]}`)
	writeFile(t, old.Store(), `{"prs":[],"cleaned":["acme/web#3"]}`)
	writeFile(t, old.Lock(), `{"pid":1}`)

	moved, err := Migrate(old, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 {
		t.Errorf("moved %q", moved)
	}
	c, err := LoadConfig(cur.Config)
	if err != nil || !slices.Equal(c.Owners, []string{"acme"}) {
		t.Errorf("config after the move: %+v %v", c, err)
	}
	s, err := LoadStore(cur.Store())
	if err != nil || !slices.Equal(s.Cleaned, []string{"acme/web#3"}) {
		t.Errorf("the cleaned-PR memory did not move: %+v %v", s, err)
	}
	for _, gone := range []string{old.Config, filepath.Dir(old.Config), old.Data} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s still there: %v", gone, err)
		}
	}
	if _, err := os.Stat(old.Lock()); err != nil {
		t.Errorf("the old state dir (a running server's lock) must stay: %v", err)
	}

	// a second run has nothing to move
	if moved, err := Migrate(old, cur); err != nil || len(moved) != 0 {
		t.Errorf("second run moved %q (%v)", moved, err)
	}
}

func TestMigrateKeepsExistingNewFiles(t *testing.T) {
	home := t.TempDir()
	old, cur := PathsUnder(home, "old-name"), PathsUnder(home, "pr-board")
	writeFile(t, old.Config, `{"owners":["old"]}`)
	writeFile(t, old.Store(), `{"prs":[],"cleaned":["old/x#1"]}`)
	writeFile(t, cur.Config, `{"owners":["new"]}`)
	writeFile(t, cur.Store(), `{"prs":[],"cleaned":[]}`)

	moved, err := Migrate(old, cur)
	if err != nil || len(moved) != 0 {
		t.Fatalf("moved %q (%v)", moved, err)
	}
	if got := readFile(t, cur.Config); !strings.Contains(got, `"new"`) {
		t.Errorf("new config overwritten: %s", got)
	}
	if got := readFile(t, old.Store()); !strings.Contains(got, "old/x#1") {
		t.Errorf("old store touched: %s", got)
	}
}

func TestMigrateWithNothingOld(t *testing.T) {
	home := t.TempDir()
	moved, err := Migrate(PathsUnder(home, "old-name"), PathsUnder(home, "pr-board"))
	if err != nil || len(moved) != 0 {
		t.Errorf("moved %q (%v)", moved, err)
	}
}
func TestDefaultConfigHasNoOwners(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Owners) != 0 {
		t.Errorf("default owners %q", c.Owners)
	}
	if got := readFile(t, path); !strings.Contains(got, `"owners": []`) {
		t.Errorf("written default should list owners as []: %s", got)
	}
}
