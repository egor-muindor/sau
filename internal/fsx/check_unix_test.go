//go:build unix

package fsx_test

import (
	"os"
	"path/filepath"
	"testing"

	"sau/internal/fsx"
)

func TestCheckPrivateRejectsGroupAndOtherBits(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	dir := t.TempDir()

	for _, mode := range []os.FileMode{0o640, 0o604, 0o666, 0o644} {
		path := filepath.Join(dir, "f.json")
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
		if err := fsx.CheckPrivate(path); err == nil {
			t.Errorf("CheckPrivate(%#o) = nil, want error", mode)
		}
		if err := os.Remove(path); err != nil {
			t.Fatalf("Remove: %v", err)
		}
	}
}

func TestCheckPrivateReportsMissingFile(t *testing.T) {
	if err := fsx.CheckPrivate(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("CheckPrivate(missing) = nil, want error")
	}
}
