//go:build darwin || windows

package fsx

import "strings"

// foldCase lowercases the path: the default file systems of macOS and Windows
// are case-insensitive.
func foldCase(p string) string {
	return strings.ToLower(p)
}
