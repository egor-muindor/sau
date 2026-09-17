//go:build !darwin && !windows

package fsx_test

import (
	"testing"

	"sau/internal/fsx"
)

// On case-sensitive file systems the two spellings are different files and
// must stay different after normalization.
func TestNormalizePathKeepsCase(t *testing.T) {
	a, err := fsx.NormalizePath("Anime/Episode01.MP4")
	if err != nil {
		t.Fatalf("NormalizePath: %v", err)
	}
	b, err := fsx.NormalizePath("anime/episode01.mp4")
	if err != nil {
		t.Fatalf("NormalizePath: %v", err)
	}
	if a == b {
		t.Errorf("NormalizePath collapsed case on a case-sensitive system: %q", a)
	}
}
