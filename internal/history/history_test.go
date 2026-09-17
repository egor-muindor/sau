package history_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sau/internal/history"
)

func sampleRecord(seriesID int, episode string, at time.Time) history.Record {
	var r history.Record
	r.At = at
	r.Series.ID = seriesID
	r.Series.Title = "Sample Title"
	r.Series.Season = "fall"
	r.Series.Year = 2026
	r.Episode.Number = episode
	r.Episode.ID = 371562
	r.Episode.Type = "tv"
	r.Episode.Title = "Sample Episode"
	r.Translation.ID = 5995522
	r.Translation.Type = "voiceRu"
	r.Translation.Authors = "Team (Alice, Bob)"
	r.Translation.URL = "https://smotret-anime.online/translations/5995522"
	r.Translation.Quality = "bd"
	r.Translation.Width = 1920
	r.Translation.Height = 1080
	r.Upload.File = "[Team] Title - 01 [1080p].mp4"
	r.Upload.Size = 1_500_000_000
	r.Upload.Parts = 300
	r.Upload.Duration = 20 * time.Minute
	r.Upload.BytesPerSec = 1_250_000
	r.Upload.Retries = 2
	r.Upload.Hosts = []string{"https://t-time28.melon-soda.org"}
	r.Upload.Channel = "cdn"
	r.Upload.ServerID = 28
	r.Upload.Started = at
	r.Upload.Finished = at.Add(20 * time.Minute)
	return r
}

func TestAppendWritesOneLinePerRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "history.jsonl")
	l := history.Log{Path: path}

	at := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	if err := l.Append(sampleRecord(36866, "1", at)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.Append(sampleRecord(36866, "2", at.Add(7*24*time.Hour))); err != nil {
		t.Fatalf("Append: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("file has %d lines, want 2:\n%s", len(lines), data)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			t.Errorf("line %d is not a single JSON object: %q", i, line)
		}
	}
}

func TestAppendFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "history.jsonl")
	l := history.Log{Path: path}
	if err := l.Append(sampleRecord(36866, "1", time.Now())); err != nil {
		t.Fatalf("Append: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtimeAllowsModeCheck() && fi.Mode().Perm() != 0o600 {
		t.Errorf("history file mode = %#o, want 0600", fi.Mode().Perm())
	}
}

func TestReadAllRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	l := history.Log{Path: path}
	at := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	want := sampleRecord(36866, "1", at)
	if err := l.Append(want); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := l.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ReadAll returned %d records, want 1", len(got))
	}
	r := got[0]
	if !r.At.Equal(want.At) {
		t.Errorf("At = %v, want %v", r.At, want.At)
	}
	if r.Series.ID != want.Series.ID || r.Series.Title != want.Series.Title {
		t.Errorf("Series = %+v, want %+v", r.Series, want.Series)
	}
	if r.Episode.Number != want.Episode.Number || r.Episode.ID != want.Episode.ID {
		t.Errorf("Episode = %+v, want %+v", r.Episode, want.Episode)
	}
	if r.Translation.ID != want.Translation.ID || r.Translation.Authors != want.Translation.Authors {
		t.Errorf("Translation = %+v, want %+v", r.Translation, want.Translation)
	}
	if r.Upload.Size != want.Upload.Size || r.Upload.Duration != want.Upload.Duration {
		t.Errorf("Upload = %+v, want %+v", r.Upload, want.Upload)
	}
	if len(r.Upload.Hosts) != 1 || r.Upload.Hosts[0] != want.Upload.Hosts[0] {
		t.Errorf("Upload.Hosts = %v, want %v", r.Upload.Hosts, want.Upload.Hosts)
	}
}

func TestReadAllSkipsBlankLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	l := history.Log{Path: path}
	if err := l.Append(sampleRecord(36866, "1", time.Now())); err != nil {
		t.Fatalf("Append: %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteString("\n   \n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := l.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("ReadAll returned %d records, want 1", len(got))
	}
}

func TestReadAllMissingFileIsEmpty(t *testing.T) {
	l := history.Log{Path: filepath.Join(t.TempDir(), "nope.jsonl")}
	got, err := l.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got != nil {
		t.Errorf("ReadAll = %v, want nil", got)
	}
}

func TestReadAllRejectsCorruptLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(path, []byte("{not json\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := (history.Log{Path: path}).ReadAll(); err == nil {
		t.Error("ReadAll on a corrupt line = nil error, want an error")
	}
}
