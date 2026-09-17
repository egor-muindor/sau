//go:build windows

package config

import (
	"errors"
	"os"
	"path/filepath"
)

func defaultConfigDir() (string, error) {
	v := os.Getenv("APPDATA")
	if v == "" {
		return "", errors.New("config: APPDATA is not set")
	}
	return filepath.Join(v, "sau"), nil
}

// defaultStateDir uses LOCALAPPDATA on purpose: upload state and session
// cookies are specific to this machine and must not roam between machines.
func defaultStateDir() (string, error) {
	v := os.Getenv("LOCALAPPDATA")
	if v == "" {
		return "", errors.New("config: LOCALAPPDATA is not set")
	}
	return filepath.Join(v, "sau"), nil
}
