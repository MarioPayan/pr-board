package prboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// LoadStore reads prs.json; a missing file is an empty store. Keys the store
// no longer has (the old "ai" notes) are ignored.
func LoadStore(path string) (*Store, error) {
	s := &Store{PRs: []PR{}, Cleaned: []string{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return &Store{PRs: []PR{}, Cleaned: []string{}}, fmt.Errorf("%s: %w", path, err)
	}
	if s.PRs == nil {
		s.PRs = []PR{}
	}
	if s.Cleaned == nil {
		s.Cleaned = []string{}
	}
	return s, nil
}

// SaveStore writes prs.json atomically.
func SaveStore(path string, s *Store) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(b, '\n'))
}
