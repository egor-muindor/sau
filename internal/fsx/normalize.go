package fsx

import "path/filepath"

// NormalizePath turns p into an absolute, cleaned path and applies the
// platform's case rule, so that two spellings of the same file produce the
// same string. It is the basis of the state file key.
func NormalizePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return foldCase(filepath.Clean(abs)), nil
}
