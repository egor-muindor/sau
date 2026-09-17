//go:build unix

package fsx_test

import (
	"os"
	"path/filepath"
	"testing"

	"sau/internal/fsx"
)

// A failed write must not leave a temporary file behind. The directory is made
// read-only so that creating the temporary file fails.
func TestWriteFileAtomicLeavesNoTempOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "state")
	if err := fsx.MkdirPrivate(dir); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := fsx.WriteFileAtomic(filepath.Join(dir, "state.json"), []byte("payload"), 0o600)
	if err == nil {
		t.Fatal("WriteFileAtomic = nil, want error on a read-only directory")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("dir contains %d entries, want 0 (no temporary files left)", len(entries))
	}
}
