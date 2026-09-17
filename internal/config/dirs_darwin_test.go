//go:build darwin

package config

import (
	"path/filepath"
	"testing"
)

func TestDirsDarwin(t *testing.T) {
	t.Setenv("SAU_CONFIG_DIR", "")
	t.Setenv("SAU_STATE_DIR", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, state, err := Dirs()
	if err != nil {
		t.Fatalf("Dirs: %v", err)
	}
	want := filepath.Join(home, "Library", "Application Support", "sau")
	if cfg != want {
		t.Errorf("config dir = %q, want %q", cfg, want)
	}
	if state != want {
		t.Errorf("state dir = %q, want %q", state, want)
	}
}
