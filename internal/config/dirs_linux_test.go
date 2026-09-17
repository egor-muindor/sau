//go:build linux

package config

import (
	"path/filepath"
	"testing"
)

func TestDirsLinuxXDG(t *testing.T) {
	t.Setenv("SAU_CONFIG_DIR", "")
	t.Setenv("SAU_STATE_DIR", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".state"))

	cfg, state, err := Dirs()
	if err != nil {
		t.Fatalf("Dirs: %v", err)
	}
	if want := filepath.Join(home, ".config", "sau"); cfg != want {
		t.Errorf("config dir = %q, want %q", cfg, want)
	}
	if want := filepath.Join(home, ".state", "sau"); state != want {
		t.Errorf("state dir = %q, want %q", state, want)
	}
}

func TestDirsLinuxStateFallback(t *testing.T) {
	t.Setenv("SAU_CONFIG_DIR", "")
	t.Setenv("SAU_STATE_DIR", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")

	_, state, err := Dirs()
	if err != nil {
		t.Fatalf("Dirs: %v", err)
	}
	if want := filepath.Join(home, ".local", "state", "sau"); state != want {
		t.Errorf("state dir = %q, want %q", state, want)
	}
}
