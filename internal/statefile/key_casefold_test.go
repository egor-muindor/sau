//go:build darwin || windows

package statefile_test

import (
	"testing"

	"sau/internal/statefile"
)

// The file system ignores case here, so both spellings name one file and must
// map to one state key.
func TestKeyFoldsCase(t *testing.T) {
	a, err := statefile.Key("/tmp/Anime/Episode01.MP4")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	b, err := statefile.Key("/tmp/anime/episode01.mp4")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if a != b {
		t.Errorf("Key: %q != %q, want one key for one file", a, b)
	}
}
