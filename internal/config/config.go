// Package config loads the tool configuration from flags, the environment and
// two TOML files.
package config

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"

	"github.com/BurntSushi/toml"
)

// Config is the fully resolved configuration.
type Config struct {
	Mirror  string
	Mirrors []string

	User string

	SeriesID      int
	EpisodeType   string
	Type          string
	Authors       string
	AddedByAuthor bool

	Channel     string
	Concurrency int

	StateDir    string
	SessionDir  string
	HistoryPath string
	LogPath     string
}

// Layer is one source of settings: flat keys mapped to their textual values.
// Only keys that were actually set belong in a layer.
type Layer map[string]string

// forbiddenKeys must never appear in any configuration file: upload servers are
// parsed from a fresh form page before every upload and are never cached.
var forbiddenKeys = []string{"server", "server_id", "server_url", "server_urls", "upload_servers"}

var defaultMirrors = []string{
	"https://smotret-anime.online",
	"https://smotret-anime.org",
	"https://smotret-anime.ru",
	"https://anime-365.ru",
	"https://anime365.ru",
}

var envKeys = []struct{ env, key string }{
	{"SAU_SERIES", "series"},
	{"SAU_EPISODE_TYPE", "episode_type"},
	{"SAU_TYPE", "type"},
	{"SAU_AUTHORS", "authors"},
	{"SAU_CHANNEL", "channel"},
	{"SAU_USER", "user"},
	{"SAU_MIRROR", "mirror"},
	{"SAU_CONCURRENCY", "concurrency"},
}

func defaults() Config {
	return Config{
		Mirror:      "https://smotret-anime.online",
		Mirrors:     slices.Clone(defaultMirrors),
		Channel:     "cdn",
		Concurrency: 5,
		EpisodeType: "tv",
		Type:        "voiceRu",
	}
}

// Load resolves the configuration. Precedence, strongest first: flags,
// SAU_* environment variables, the project file, the user file, built-in
// defaults. Missing files are not an error.
//
// Load does not validate the channel, the episode type or the translation
// type: the caller reports those as usage errors so that a bad flag and a bad
// file entry are reported the same way.
func Load(flags Layer, env func(string) (string, bool), projectPath, userPath string) (Config, error) {
	cfg := defaults()

	for _, path := range []string{userPath, projectPath} {
		layer, mirrors, ok, err := readTOML(path)
		if err != nil {
			return Config{}, err
		}
		if !ok {
			continue
		}
		if mirrors != nil {
			cfg.Mirrors = mirrors
		}
		if err := apply(&cfg, layer); err != nil {
			return Config{}, fmt.Errorf("config %s: %w", path, err)
		}
	}

	if env != nil {
		layer := Layer{}
		for _, e := range envKeys {
			if v, ok := env(e.env); ok && v != "" {
				layer[e.key] = v
			}
		}
		if err := apply(&cfg, layer); err != nil {
			return Config{}, fmt.Errorf("environment: %w", err)
		}
	}

	if err := apply(&cfg, flags); err != nil {
		return Config{}, fmt.Errorf("flags: %w", err)
	}
	return cfg, nil
}

// readTOML reads one configuration file. It reports ok=false when the file does
// not exist.
func readTOML(path string) (Layer, []string, bool, error) {
	if path == "" {
		return nil, nil, false, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	// A byte order mark written by a Windows editor is not part of the document.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, nil, false, fmt.Errorf("config %s: %w", path, err)
	}

	for _, k := range forbiddenKeys {
		if _, ok := raw[k]; ok {
			return nil, nil, false, fmt.Errorf(
				"config %s: key %q is not allowed: upload servers are never cached", path, k)
		}
	}

	layer := Layer{}
	var mirrors []string
	for _, k := range slices.Sorted(maps.Keys(raw)) {
		v := raw[k]
		if k == "mirrors" {
			items, ok := v.([]any)
			if !ok {
				return nil, nil, false, fmt.Errorf("config %s: key %q must be an array of strings", path, k)
			}
			for _, it := range items {
				s, ok := it.(string)
				if !ok {
					return nil, nil, false, fmt.Errorf("config %s: key %q must be an array of strings", path, k)
				}
				mirrors = append(mirrors, s)
			}
			continue
		}
		switch t := v.(type) {
		case string:
			layer[k] = t
		case int64:
			layer[k] = strconv.FormatInt(t, 10)
		case bool:
			layer[k] = strconv.FormatBool(t)
		default:
			return nil, nil, false, fmt.Errorf("config %s: key %q has an unsupported type", path, k)
		}
	}
	return layer, mirrors, true, nil
}

// apply overlays one layer onto cfg. Keys are visited in a stable order so that
// the reported error is the same on every run.
func apply(cfg *Config, layer Layer) error {
	for _, k := range slices.Sorted(maps.Keys(layer)) {
		v := layer[k]
		switch k {
		case "mirror":
			cfg.Mirror = v
		case "user":
			cfg.User = v
		case "series":
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("key %q: not a number: %q", k, v)
			}
			cfg.SeriesID = n
		case "episode_type":
			cfg.EpisodeType = v
		case "type":
			cfg.Type = v
		case "authors":
			cfg.Authors = v
		case "by_author":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("key %q: not a boolean: %q", k, v)
			}
			cfg.AddedByAuthor = b
		case "channel":
			cfg.Channel = v
		case "concurrency":
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("key %q: not a number: %q", k, v)
			}
			if n < 1 {
				return fmt.Errorf("key %q: must be at least 1", k)
			}
			cfg.Concurrency = n
		case "state_dir":
			cfg.StateDir = v
		case "session_dir":
			cfg.SessionDir = v
		case "history_path":
			cfg.HistoryPath = v
		case "log_path":
			cfg.LogPath = v
		default:
			return fmt.Errorf("unknown key %q", k)
		}
	}
	return nil
}
