package config

import (
	"os"
	"path/filepath"
)

// Dirs returns the configuration directory and the state directory.
// SAU_CONFIG_DIR and SAU_STATE_DIR override the platform defaults; when a
// variable is set the platform lookup is skipped entirely, so the tool still
// works in an environment without a home directory.
func Dirs() (config, state string, err error) {
	config = os.Getenv("SAU_CONFIG_DIR")
	if config == "" {
		config, err = defaultConfigDir()
		if err != nil {
			return "", "", err
		}
	}
	state = os.Getenv("SAU_STATE_DIR")
	if state == "" {
		state, err = defaultStateDir()
		if err != nil {
			return "", "", err
		}
	}
	return filepath.Clean(config), filepath.Clean(state), nil
}
