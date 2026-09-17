// Package statefile stores the state of unfinished uploads: one JSON file per
// uploaded file, written atomically and privately.
package statefile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sau/internal/fsx"
)

// ErrNotFound reports that there is no state for the key.
var ErrNotFound = errors.New("statefile: not found")

// Store is a directory of state files.
type Store struct {
	Dir string
}

// Key derives the state key of a file path. The path is normalized first, so
// that two spellings of one file on a case-insensitive file system share a
// single state.
func Key(path string) (string, error) {
	n, err := fsx.NormalizePath(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(n))
	return hex.EncodeToString(sum[:])[:16], nil
}

func (s Store) path(key string) string {
	return filepath.Join(s.Dir, key+".json")
}

// Save writes v as indented JSON, atomically, with mode 0600.
func (s Store) Save(key string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := fsx.MkdirPrivate(s.Dir); err != nil {
		return err
	}
	return fsx.WriteFileAtomic(s.path(key), data, 0o600)
}

// Load decodes the state for key into v. A missing state is ErrNotFound.
func (s Store) Load(key string, v any) error {
	path := s.path(key)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("statefile: decode %s: %w", path, err)
	}
	return nil
}

// Delete removes the state for key. A missing state is not an error.
func (s Store) Delete(key string) error {
	err := os.Remove(s.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Keys lists the state keys in the directory, sorted. A missing directory
// yields no keys and no error.
func (s Store) Keys() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		keys = append(keys, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(keys)
	return keys, nil
}
