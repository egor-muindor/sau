package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTOML(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.toml")

	cfg, err := Load(nil, envOf(nil), missing, missing)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mirror != "https://smotret-anime.online" {
		t.Errorf("Mirror = %q", cfg.Mirror)
	}
	if len(cfg.Mirrors) != 5 {
		t.Errorf("Mirrors = %v, want 5 entries", cfg.Mirrors)
	}
	if cfg.Mirrors[0] != "https://smotret-anime.online" || cfg.Mirrors[4] != "https://anime365.ru" {
		t.Errorf("Mirrors = %v", cfg.Mirrors)
	}
	if cfg.Channel != "cdn" {
		t.Errorf("Channel = %q", cfg.Channel)
	}
	if cfg.Concurrency != 5 {
		t.Errorf("Concurrency = %d", cfg.Concurrency)
	}
	if cfg.EpisodeType != "tv" {
		t.Errorf("EpisodeType = %q", cfg.EpisodeType)
	}
	if cfg.Type != "voiceRu" {
		t.Errorf("Type = %q", cfg.Type)
	}
}

func TestLoadPriority(t *testing.T) {
	const userTOML = `
user = "user-from-user-file"
series = 1
channel = "all"
concurrency = 2
type = "subRu"
`
	const projTOML = `
series = 2
authors = "Team (Alice, Bob)"
concurrency = 3
by_author = true
`

	cases := []struct {
		name  string
		user  string
		proj  string
		env   map[string]string
		flags Layer
		want  func(t *testing.T, c Config)
	}{
		{
			name: "user file only",
			user: userTOML,
			want: func(t *testing.T, c Config) {
				if c.User != "user-from-user-file" {
					t.Errorf("User = %q", c.User)
				}
				if c.SeriesID != 1 {
					t.Errorf("SeriesID = %d", c.SeriesID)
				}
				if c.Channel != "all" {
					t.Errorf("Channel = %q", c.Channel)
				}
				if c.Type != "subRu" {
					t.Errorf("Type = %q", c.Type)
				}
			},
		},
		{
			name: "project file beats user file",
			user: userTOML,
			proj: projTOML,
			want: func(t *testing.T, c Config) {
				if c.SeriesID != 2 {
					t.Errorf("SeriesID = %d, want 2", c.SeriesID)
				}
				if c.Concurrency != 3 {
					t.Errorf("Concurrency = %d, want 3", c.Concurrency)
				}
				if c.Authors != "Team (Alice, Bob)" {
					t.Errorf("Authors = %q", c.Authors)
				}
				if !c.AddedByAuthor {
					t.Error("AddedByAuthor = false, want true")
				}
				if c.User != "user-from-user-file" {
					t.Errorf("User = %q, want the user-file value to survive", c.User)
				}
			},
		},
		{
			name: "environment beats both files",
			user: userTOML,
			proj: projTOML,
			env: map[string]string{
				"SAU_SERIES":       "3",
				"SAU_CONCURRENCY":  "4",
				"SAU_EPISODE_TYPE": "ova",
				"SAU_TYPE":         "voiceRu",
				"SAU_AUTHORS":      "Solo",
				"SAU_CHANNEL":      "ru",
				"SAU_USER":         "user-from-env",
				"SAU_MIRROR":       "https://anime365.ru",
			},
			want: func(t *testing.T, c Config) {
				if c.SeriesID != 3 {
					t.Errorf("SeriesID = %d, want 3", c.SeriesID)
				}
				if c.Concurrency != 4 {
					t.Errorf("Concurrency = %d, want 4", c.Concurrency)
				}
				if c.EpisodeType != "ova" {
					t.Errorf("EpisodeType = %q", c.EpisodeType)
				}
				if c.Authors != "Solo" {
					t.Errorf("Authors = %q", c.Authors)
				}
				if c.Channel != "ru" {
					t.Errorf("Channel = %q", c.Channel)
				}
				if c.User != "user-from-env" {
					t.Errorf("User = %q", c.User)
				}
				if c.Mirror != "https://anime365.ru" {
					t.Errorf("Mirror = %q", c.Mirror)
				}
			},
		},
		{
			name:  "flags beat everything",
			user:  userTOML,
			proj:  projTOML,
			env:   map[string]string{"SAU_SERIES": "3", "SAU_CONCURRENCY": "4"},
			flags: Layer{"series": "9", "concurrency": "7", "channel": "cdn"},
			want: func(t *testing.T, c Config) {
				if c.SeriesID != 9 {
					t.Errorf("SeriesID = %d, want 9", c.SeriesID)
				}
				if c.Concurrency != 7 {
					t.Errorf("Concurrency = %d, want 7", c.Concurrency)
				}
				if c.Channel != "cdn" {
					t.Errorf("Channel = %q", c.Channel)
				}
			},
		},
		{
			name: "mirrors list from a file",
			user: "mirrors = [\"https://a.example\", \"https://b.example\"]\n",
			want: func(t *testing.T, c Config) {
				if len(c.Mirrors) != 2 || c.Mirrors[0] != "https://a.example" {
					t.Errorf("Mirrors = %v", c.Mirrors)
				}
			},
		},
		{
			name: "directory keys",
			user: "state_dir = \"/s\"\nsession_dir = \"/c\"\nhistory_path = \"/h.jsonl\"\nlog_path = \"/l.log\"\n",
			want: func(t *testing.T, c Config) {
				if c.StateDir != "/s" || c.SessionDir != "/c" || c.HistoryPath != "/h.jsonl" || c.LogPath != "/l.log" {
					t.Errorf("dirs = %q %q %q %q", c.StateDir, c.SessionDir, c.HistoryPath, c.LogPath)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			userPath := filepath.Join(dir, "config.toml")
			projPath := filepath.Join(dir, ".sau.toml")
			if tc.user != "" {
				writeTOML(t, dir, "config.toml", tc.user)
			}
			if tc.proj != "" {
				writeTOML(t, dir, ".sau.toml", tc.proj)
			}
			cfg, err := Load(tc.flags, envOf(tc.env), projPath, userPath)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			tc.want(t, cfg)
		})
	}
}

func TestLoadForbiddenKeys(t *testing.T) {
	for _, key := range forbiddenKeys {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			p := writeTOML(t, dir, "config.toml", key+" = \"whatever\"\n")
			_, err := Load(nil, envOf(nil), filepath.Join(dir, "absent.toml"), p)
			if err == nil {
				t.Fatalf("Load: want an error for key %q", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error %q does not name the key", err)
			}
			if !strings.Contains(err.Error(), "upload servers are never cached") {
				t.Errorf("error %q does not explain why", err)
			}
		})
	}
}

func TestLoadForbiddenKeyInProjectFile(t *testing.T) {
	dir := t.TempDir()
	p := writeTOML(t, dir, ".sau.toml", "server_urls = [\"https://x\"]\n")
	_, err := Load(nil, envOf(nil), p, filepath.Join(dir, "absent.toml"))
	if err == nil || !strings.Contains(err.Error(), "server_urls") {
		t.Fatalf("Load: %v, want an error naming server_urls", err)
	}
}

func TestLoadStripsBOM(t *testing.T) {
	dir := t.TempDir()
	p := writeTOML(t, dir, "config.toml", "\xef\xbb\xbfuser = \"bom\"\n")
	cfg, err := Load(nil, envOf(nil), filepath.Join(dir, "absent.toml"), p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.User != "bom" {
		t.Errorf("User = %q, want %q", cfg.User, "bom")
	}
}

func TestLoadUnknownKey(t *testing.T) {
	dir := t.TempDir()
	p := writeTOML(t, dir, "config.toml", "nonsense = 1\n")
	_, err := Load(nil, envOf(nil), filepath.Join(dir, "absent.toml"), p)
	if err == nil || !strings.Contains(err.Error(), "nonsense") {
		t.Fatalf("Load: %v, want an error naming the unknown key", err)
	}
}

func TestLoadBadInteger(t *testing.T) {
	_, err := Load(Layer{"series": "abc"}, envOf(nil), "", "")
	if err == nil {
		t.Fatal("Load: want an error for a non-numeric series")
	}
}
