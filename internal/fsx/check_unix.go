//go:build unix

package fsx

import (
	"fmt"
	"os"
)

// CheckPrivate reports an error if the file is readable, writable or
// executable by group or others.
func CheckPrivate(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("fsx: %s has mode %#o, want no group or other access", path, mode)
	}
	return nil
}
