package fsx_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sau/internal/fsx"
)

func TestWriteFileAtomicCreatesFileWithPerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := fsx.WriteFileAtomic(path, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("content = %q, want %q", got, "payload")
	}

	// On the happy path no temporary file is left behind: exactly one entry.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("dir contains %d entries (%s), want exactly 1", len(entries), strings.Join(names, ", "))
	}
}

func TestWriteFileAtomicOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := fsx.WriteFileAtomic(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := fsx.WriteFileAtomic(path, []byte("second"), 0o600); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("content = %q, want %q", got, "second")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("dir contains %d entries, want exactly 1", len(entries))
	}
}

func TestMkdirPrivateIsIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a", "b")

	if err := fsx.MkdirPrivate(path); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}
	if err := fsx.MkdirPrivate(path); err != nil {
		t.Fatalf("MkdirPrivate again: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("%s is not a directory", path)
	}
}

func TestCheckPrivateAcceptsPrivateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	if err := fsx.WriteFileAtomic(path, []byte("[]"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	if err := fsx.CheckPrivate(path); err != nil {
		t.Errorf("CheckPrivate(0600) = %v, want nil", err)
	}
}

func TestNormalizePathIsAbsoluteAndClean(t *testing.T) {
	got, err := fsx.NormalizePath("a/../b/./c.mp4")
	if err != nil {
		t.Fatalf("NormalizePath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("NormalizePath = %q, want absolute path", got)
	}
	if strings.Contains(got, "..") {
		t.Errorf("NormalizePath = %q, want cleaned path without ..", got)
	}
	if !strings.HasSuffix(got, filepath.Join("b", "c.mp4")) &&
		!strings.HasSuffix(strings.ToLower(got), strings.ToLower(filepath.Join("b", "c.mp4"))) {
		t.Errorf("NormalizePath = %q, want it to end with b/c.mp4", got)
	}
}

func TestWriteFileAtomicErrorMentionsTargetPath(t *testing.T) {
	// A directory used as the target path makes the rename fail; the error
	// must name the target, not just the anonymous temp file.
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	err := fsx.WriteFileAtomic(target, []byte("payload"), 0o600)
	if err == nil {
		t.Fatal("WriteFileAtomic = nil, want error when the target is a directory")
	}
	if !strings.Contains(err.Error(), target) {
		t.Errorf("WriteFileAtomic error = %q, want it to mention %q", err.Error(), target)
	}
}

func TestNormalizePathIsStable(t *testing.T) {
	a, err := fsx.NormalizePath("/tmp/anime/ep01.mp4")
	if err != nil {
		t.Fatalf("NormalizePath: %v", err)
	}
	b, err := fsx.NormalizePath("/tmp/anime/../anime/ep01.mp4")
	if err != nil {
		t.Fatalf("NormalizePath: %v", err)
	}
	if a != b {
		t.Errorf("NormalizePath not stable: %q != %q", a, b)
	}
}
