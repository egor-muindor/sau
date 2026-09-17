//go:build !darwin && !windows

package statefile_test

import (
	"testing"

	"sau/internal/statefile"
)

// The file system is case-sensitive here: two spellings are two files and must
// get two keys.
func TestKeyKeepsCase(t *testing.T) {
	a, err := statefile.Key("/tmp/Anime/Episode01.MP4")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	b, err := statefile.Key("/tmp/anime/episode01.mp4")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if a == b {
		t.Errorf("Key collapsed case on a case-sensitive system: %q", a)
	}
}
