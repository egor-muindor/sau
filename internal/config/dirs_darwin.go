//go:build darwin

package config

import (
	"os"
	"path/filepath"
)

// applicationSupport is where macOS keeps per-application data. Both the
// configuration and the state live there; macOS has no separate state location.
func applicationSupport() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "sau"), nil
}

func defaultConfigDir() (string, error) { return applicationSupport() }

func defaultStateDir() (string, error) { return applicationSupport() }
