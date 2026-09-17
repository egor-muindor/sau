package config

import (
	"path/filepath"
	"testing"
)

func TestDirsEnvironmentOverride(t *testing.T) {
	cfgWant := filepath.Join(t.TempDir(), "cfg")
	stateWant := filepath.Join(t.TempDir(), "state")
	t.Setenv("SAU_CONFIG_DIR", cfgWant)
	t.Setenv("SAU_STATE_DIR", stateWant)

	cfg, state, err := Dirs()
	if err != nil {
		t.Fatalf("Dirs: %v", err)
	}
	if cfg != filepath.Clean(cfgWant) {
		t.Errorf("config dir = %q, want %q", cfg, cfgWant)
	}
	if state != filepath.Clean(stateWant) {
		t.Errorf("state dir = %q, want %q", state, stateWant)
	}
}

func TestDirsSuffix(t *testing.T) {
	t.Setenv("SAU_CONFIG_DIR", "")
	t.Setenv("SAU_STATE_DIR", "")

	cfg, state, err := Dirs()
	if err != nil {
		t.Fatalf("Dirs: %v", err)
	}
	if filepath.Base(cfg) != "sau" {
		t.Errorf("config dir = %q, want a directory named sau", cfg)
	}
	if filepath.Base(state) != "sau" {
		t.Errorf("state dir = %q, want a directory named sau", state)
	}
}
