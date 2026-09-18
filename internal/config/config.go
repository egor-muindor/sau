// Package config loads the tool configuration from flags, the environment and
// two TOML files.
package config

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"sau/internal/translation"
)

// EpisodeOverride pins the episode number, and optionally the subtitle file,
// of one video file. It comes from an [[episodes]] table of a configuration
// file and is matched by the bare file name, verbatim.
type EpisodeOverride struct {
	File    string
	Episode string
	// Sub is empty when there are no subtitles. A relative path in the file
	// is resolved against the directory of the file that named it.
	Sub string
}

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

	// EpisodePattern extracts the episode number from a bare file name; its
	// only capture group is the number. Nil when no file set it. There is no
	// flag and no environment variable for it: it lives in the files only.
	EpisodePattern *regexp.Regexp
	// Episodes are the per-file overrides. The project file replaces the
	// user file's list as a whole; the two lists are never merged.
	Episodes []EpisodeOverride
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

// fileData is what one configuration file contributes: the flat keys and the
// structured ones. The structured ones override the previous file only when
// the file actually set them, which is what the nil checks and hasEpisodes
// express.
type fileData struct {
	layer       Layer
	mirrors     []string
	pattern     *regexp.Regexp
	episodes    []EpisodeOverride
	hasEpisodes bool
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
		fd, ok, err := readTOML(path)
		if err != nil {
			return Config{}, err
		}
		if !ok {
			continue
		}
		if fd.mirrors != nil {
			cfg.Mirrors = fd.mirrors
		}
		if fd.pattern != nil {
			cfg.EpisodePattern = fd.pattern
		}
		if fd.hasEpisodes {
			cfg.Episodes = fd.episodes
		}
		if err := apply(&cfg, fd.layer); err != nil {
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
func readTOML(path string) (fileData, bool, error) {
	var fd fileData
	if path == "" {
		return fd, false, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return fd, false, nil
	}
	if err != nil {
		return fd, false, err
	}
	// A byte order mark written by a Windows editor is not part of the document.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return fd, false, fmt.Errorf("config %s: %w", path, err)
	}

	for _, k := range forbiddenKeys {
		if _, ok := raw[k]; ok {
			return fd, false, fmt.Errorf(
				"config %s: key %q is not allowed: upload servers are never cached", path, k)
		}
	}

	fd.layer = Layer{}
	for _, k := range slices.Sorted(maps.Keys(raw)) {
		v := raw[k]
		switch k {
		case "mirrors":
			items, ok := v.([]any)
			if !ok {
				return fd, false, fmt.Errorf("config %s: key %q must be an array of strings", path, k)
			}
			for _, it := range items {
				s, ok := it.(string)
				if !ok {
					return fd, false, fmt.Errorf("config %s: key %q must be an array of strings", path, k)
				}
				fd.mirrors = append(fd.mirrors, s)
			}
			continue
		case "episode_pattern":
			re, err := parseEpisodePattern(path, v)
			if err != nil {
				return fd, false, err
			}
			fd.pattern = re
			continue
		case "episodes":
			eps, err := parseEpisodes(path, v)
			if err != nil {
				return fd, false, err
			}
			fd.episodes, fd.hasEpisodes = eps, true
			continue
		}
		switch t := v.(type) {
		case string:
			fd.layer[k] = t
		case int64:
			fd.layer[k] = strconv.FormatInt(t, 10)
		case bool:
			fd.layer[k] = strconv.FormatBool(t)
		default:
			return fd, false, fmt.Errorf("config %s: key %q has an unsupported type", path, k)
		}
	}
	return fd, true, nil
}

// parseEpisodePattern compiles episode_pattern and insists on exactly one
// capture group: the group is the episode number, and with zero or two groups
// there is no way to say which part of the name that is.
func parseEpisodePattern(path string, v any) (*regexp.Regexp, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("config %s: key \"episode_pattern\" must be a string", path)
	}
	re, err := regexp.Compile(s)
	if err != nil {
		return nil, fmt.Errorf("config %s: key \"episode_pattern\": %w", path, err)
	}
	if n := re.NumSubexp(); n != 1 {
		return nil, fmt.Errorf(
			"config %s: key \"episode_pattern\" must have exactly one capture group for the episode number, got %d",
			path, n)
	}
	return re, nil
}

// parseEpisodes reads the [[episodes]] tables. Only file, episode and sub are
// allowed; the number passes the same check as --episode; a file named twice
// is an error, because two numbers for one file cannot both be right.
func parseEpisodes(path string, v any) ([]EpisodeOverride, error) {
	if arr, isAny := v.([]any); isAny && len(arr) == 0 {
		return []EpisodeOverride{}, nil
	}
	tables, ok := v.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("config %s: key \"episodes\" must be an array of [[episodes]] tables", path)
	}
	dir := filepath.Dir(path)
	seen := map[string]bool{}
	out := make([]EpisodeOverride, 0, len(tables))
	for i, t := range tables {
		var o EpisodeOverride
		for _, k := range slices.Sorted(maps.Keys(t)) {
			switch k {
			case "file", "sub":
				s, ok := t[k].(string)
				if !ok {
					return nil, fmt.Errorf("config %s: episodes[%d]: key %q must be a string", path, i, k)
				}
				if k == "file" {
					o.File = s
				} else {
					o.Sub = s
				}
			case "episode":
				switch n := t[k].(type) {
				case string:
					o.Episode = n
				case int64:
					o.Episode = strconv.FormatInt(n, 10)
				default:
					return nil, fmt.Errorf("config %s: episodes[%d]: key \"episode\" must be a string or an integer", path, i)
				}
			default:
				return nil, fmt.Errorf("config %s: episodes[%d]: unknown key %q (allowed: file, episode, sub)", path, i, k)
			}
		}
		if o.File == "" {
			return nil, fmt.Errorf("config %s: episodes[%d]: file is required", path, i)
		}
		if strings.ContainsAny(o.File, `/\`) {
			return nil, fmt.Errorf("config %s: episodes[%d]: file %q must be a bare file name, without a directory", path, i, o.File)
		}
		if seen[o.File] {
			return nil, fmt.Errorf("config %s: episodes: file %q is listed twice", path, o.File)
		}
		seen[o.File] = true
		if err := translation.ValidateEpisodeNumber(o.Episode); err != nil {
			return nil, fmt.Errorf("config %s: episodes[%d] (%s): %v", path, i, o.File, err)
		}
		if o.Sub != "" && !filepath.IsAbs(o.Sub) {
			o.Sub = filepath.Join(dir, o.Sub)
		}
		out = append(out, o)
	}
	return out, nil
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
