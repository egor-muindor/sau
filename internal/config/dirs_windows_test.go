//go:build windows

package config

import (
	"path/filepath"
	"testing"
)

func TestDirsWindows(t *testing.T) {
	t.Setenv("SAU_CONFIG_DIR", "")
	t.Setenv("SAU_STATE_DIR", "")
	roaming := t.TempDir()
	local := t.TempDir()
	t.Setenv("APPDATA", roaming)
	t.Setenv("LOCALAPPDATA", local)

	cfg, state, err := Dirs()
	if err != nil {
		t.Fatalf("Dirs: %v", err)
	}
	if want := filepath.Join(roaming, "sau"); cfg != want {
		t.Errorf("config dir = %q, want %q", cfg, want)
	}
	// State lives in the local, non-roaming directory: it is machine specific
	// and must not be synchronised between machines.
	if want := filepath.Join(local, "sau"); state != want {
		t.Errorf("state dir = %q, want %q", state, want)
	}
}
