// Package fsx provides private, atomic file writes and path normalization.
//
// Platform differences live in build-tagged files; there is no runtime GOOS
// check anywhere in this package.
package fsx

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path so that a reader never observes a
// partially written file: the data goes into a temporary file in the same
// directory, is flushed to disk, and is then renamed over the target.
//
// The temporary file is removed if anything fails before the rename.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("fsx: write %s: %w", path, err)
	}
	tmp := f.Name()
	committed := false
	defer func() {
		if !committed {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("fsx: write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsx: write %s: %w", path, err)
	}
	if err := f.Chmod(perm); err != nil {
		return fmt.Errorf("fsx: write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("fsx: write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("fsx: write %s: %w", path, err)
	}
	committed = true
	return nil
}

// MkdirPrivate creates path and all missing parents with mode 0700.
func MkdirPrivate(path string) error {
	return os.MkdirAll(path, 0o700)
}
