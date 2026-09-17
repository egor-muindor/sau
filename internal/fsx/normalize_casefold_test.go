//go:build darwin || windows

package fsx_test

import (
	"testing"

	"sau/internal/fsx"
)

// macOS and Windows file systems are case-insensitive: the two spellings name
// the same file, so normalization must collapse them.
func TestNormalizePathFoldsCase(t *testing.T) {
	a, err := fsx.NormalizePath("Anime/Episode01.MP4")
	if err != nil {
		t.Fatalf("NormalizePath: %v", err)
	}
	b, err := fsx.NormalizePath("anime/episode01.mp4")
	if err != nil {
		t.Fatalf("NormalizePath: %v", err)
	}
	if a != b {
		t.Errorf("NormalizePath case-folding: %q != %q", a, b)
	}
}
