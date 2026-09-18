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

func TestLoadEpisodePattern(t *testing.T) {
	dir := t.TempDir()
	p := writeTOML(t, dir, ".sau.toml", "episode_pattern = ' - (\\d+(?:\\.\\d+)?) '\n")
	cfg, err := Load(nil, envOf(nil), p, filepath.Join(dir, "absent.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EpisodePattern == nil {
		t.Fatal("EpisodePattern is nil")
	}
	m := cfg.EpisodePattern.FindStringSubmatch("[T] Show - 02 [1080p].mp4")
	if len(m) != 2 || m[1] != "02" {
		t.Errorf("submatch = %q, want the number in group 1", m)
	}
	m = cfg.EpisodePattern.FindStringSubmatch("[T] Show - 5.5 [1080p].mp4")
	if len(m) != 2 || m[1] != "5.5" {
		t.Errorf("submatch = %q, want a fractional number in group 1", m)
	}
}

func TestLoadEpisodePatternMustHaveOneGroup(t *testing.T) {
	cases := []struct{ name, body string }{
		{"no group", "episode_pattern = ' - \\d+ '\n"},
		{"two groups", "episode_pattern = '(\\d+)x(\\d+)'\n"},
		{"invalid", "episode_pattern = '('\n"},
		{"not a string", "episode_pattern = 3\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := writeTOML(t, dir, ".sau.toml", tc.body)
			_, err := Load(nil, envOf(nil), p, filepath.Join(dir, "absent.toml"))
			if err == nil || !strings.Contains(err.Error(), "episode_pattern") {
				t.Fatalf("Load: %v, want an error naming episode_pattern", err)
			}
		})
	}
}

func TestLoadEpisodes(t *testing.T) {
	dir := t.TempDir()
	p := writeTOML(t, dir, ".sau.toml", `
[[episodes]]
file = "[T] Show FIX 1080p.mp4"
episode = "2"
sub = "02.ass"

[[episodes]]
file = "[T] Show - 03 [1080p].mp4"
episode = 3

[[episodes]]
file = "[T] Show - 04 [1080p].mp4"
episode = "4.5"
sub = "subs/04.ass"
`)
	cfg, err := Load(nil, envOf(nil), p, filepath.Join(dir, "absent.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Episodes) != 3 {
		t.Fatalf("Episodes = %v, want 3 entries", cfg.Episodes)
	}
	e := cfg.Episodes[0]
	if e.File != "[T] Show FIX 1080p.mp4" || e.Episode != "2" {
		t.Errorf("Episodes[0] = %+v", e)
	}
	// A relative subtitle path is resolved against the directory of the
	// file that named it, not against the working directory.
	if want := filepath.Join(dir, "02.ass"); e.Sub != want {
		t.Errorf("Episodes[0].Sub = %q, want %q", e.Sub, want)
	}
	if cfg.Episodes[1].Episode != "3" || cfg.Episodes[1].Sub != "" {
		t.Errorf("Episodes[1] = %+v, want an integer episode accepted and no sub", cfg.Episodes[1])
	}
	if want := filepath.Join(dir, "subs", "04.ass"); cfg.Episodes[2].Episode != "4.5" || cfg.Episodes[2].Sub != want {
		t.Errorf("Episodes[2] = %+v, want episode 4.5 and sub %q", cfg.Episodes[2], want)
	}
}

func TestLoadEpisodesErrors(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"unknown key", "[[episodes]]\nfile = \"a.mp4\"\nepisode = \"1\"\nauthors = \"x\"\n", "authors"},
		{"duplicate file", "[[episodes]]\nfile = \"a.mp4\"\nepisode = \"1\"\n[[episodes]]\nfile = \"a.mp4\"\nepisode = \"2\"\n", "twice"},
		{"bad number", "[[episodes]]\nfile = \"a.mp4\"\nepisode = \"one\"\n", "episode"},
		{"zero", "[[episodes]]\nfile = \"a.mp4\"\nepisode = \"0\"\n", "episode"},
		{"missing number", "[[episodes]]\nfile = \"a.mp4\"\n", "episode"},
		{"missing file", "[[episodes]]\nepisode = \"1\"\n", "file"},
		{"file with a directory", "[[episodes]]\nfile = \"dir/a.mp4\"\nepisode = \"1\"\n", "bare file name"},
		{"not a table", "episodes = \"a.mp4\"\n", "episodes"},
		{"sub not a string", "[[episodes]]\nfile = \"a.mp4\"\nepisode = \"1\"\nsub = 1\n", "sub"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := writeTOML(t, dir, ".sau.toml", tc.body)
			_, err := Load(nil, envOf(nil), p, filepath.Join(dir, "absent.toml"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load: %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

func TestLoadEpisodeKeysProjectFileWins(t *testing.T) {
	dir := t.TempDir()
	userPath := writeTOML(t, dir, "config.toml", `
episode_pattern = 'user-(\d+)'
[[episodes]]
file = "u1.mp4"
episode = "1"
[[episodes]]
file = "u2.mp4"
episode = "2"
`)
	projPath := writeTOML(t, dir, ".sau.toml", `
[[episodes]]
file = "p9.mp4"
episode = "9"
`)
	cfg, err := Load(nil, envOf(nil), projPath, userPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The list is replaced as a whole, never merged.
	if len(cfg.Episodes) != 1 || cfg.Episodes[0].File != "p9.mp4" {
		t.Errorf("Episodes = %v, want only the project entry", cfg.Episodes)
	}
	// A key the project file did not set survives from the user file.
	if cfg.EpisodePattern == nil || cfg.EpisodePattern.String() != `user-(\d+)` {
		t.Errorf("EpisodePattern = %v, want the user file's pattern", cfg.EpisodePattern)
	}

	projPath2 := writeTOML(t, dir, "proj2.toml", "episode_pattern = 'proj-(\\d+)'\n")
	cfg, err = Load(nil, envOf(nil), projPath2, userPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EpisodePattern.String() != `proj-(\d+)` {
		t.Errorf("EpisodePattern = %v, want the project file's pattern", cfg.EpisodePattern)
	}
	if len(cfg.Episodes) != 2 {
		t.Errorf("Episodes = %v, want the user file's list to survive", cfg.Episodes)
	}
}

func TestLoadEpisodeKeysAreNotLayerKeys(t *testing.T) {
	// Flags and the environment carry flat strings; the two structured keys
	// live only in the files.
	for _, key := range []string{"episode_pattern", "episodes"} {
		_, err := Load(Layer{key: "(x)"}, envOf(nil), "", "")
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("Load with flag %q: %v, want an unknown-key error", key, err)
		}
	}
}
