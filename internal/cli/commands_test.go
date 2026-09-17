package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sau/internal/fineup"
	"sau/internal/history"
	"sau/internal/publish"
	"sau/internal/site"
	"sau/internal/translation"
)

func TestStatusPrintsTable(t *testing.T) {
	h := newHarness(t)
	h.runner.states = []publish.UploadState{
		{
			Path:      "/videos/ep01.mp4",
			Size:      1500,
			Phase:     publish.PhaseUploading,
			Channel:   translation.ChannelCDN,
			StartedAt: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
			Video:     &publish.FileUpload{TotalParts: 10, Done: []int{0, 1, 2}},
		},
		{
			Path:      "/videos/ep02.mp4",
			Phase:     publish.PhaseSubmitUnknown,
			Channel:   translation.ChannelRU,
			StartedAt: time.Date(2026, 9, 17, 13, 0, 0, 0, time.UTC),
		},
		{
			// publish owns the set of phases and adds to it. status prints
			// whatever it is given rather than translating a fixed list, so a
			// phase this package has never heard of must still show up.
			Path:      "/videos/ep03.mp4",
			Phase:     publish.Phase("submitting"),
			Channel:   translation.ChannelAll,
			StartedAt: time.Date(2026, 9, 17, 14, 0, 0, 0, time.UTC),
		},
	}

	if code := h.run("status"); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	out := h.out.String()
	for _, want := range []string{
		"ep01.mp4", "uploading", "3/10", "cdn",
		"ep02.mp4", "submit_unknown", "ru",
		"ep03.mp4", "submitting", "all",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}
}

func TestStatusEmpty(t *testing.T) {
	h := newHarness(t)
	if code := h.run("status"); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(h.out.String(), "no unfinished uploads") {
		t.Errorf("stdout = %q", h.out.String())
	}
}

func TestAbort(t *testing.T) {
	h := newHarness(t)
	if code := h.run("abort", "/videos/ep01.mp4"); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if h.runner.aborted != "/videos/ep01.mp4" {
		t.Errorf("Abort path = %q", h.runner.aborted)
	}
}

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want bool
	}{{"--submitted", true}, {"--resend", false}} {
		t.Run(tc.flag, func(t *testing.T) {
			h := newHarness(t)
			if code := h.run("resolve", tc.flag, "/videos/ep01.mp4"); code != ExitOK {
				t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
			}
			if h.runner.resolve.path != "/videos/ep01.mp4" {
				t.Errorf("Resolve path = %q", h.runner.resolve.path)
			}
			if h.runner.resolve.submitted != tc.want {
				t.Errorf("Resolve submitted = %v, want %v", h.runner.resolve.submitted, tc.want)
			}
		})
	}
}

func TestSeries(t *testing.T) {
	h := newHarness(t)
	h.site.series = site.Series{
		ID:               8445,
		Titles:           map[string]string{"ru": "Название", "romaji": "Namae"},
		Season:           "fall",
		Year:             2026,
		NumberOfEpisodes: 12,
	}
	h.site.episodes = []site.Episode{
		{ID: 1, EpisodeInt: "1", EpisodeFull: "1 серия", EpisodeType: "tv"},
		{ID: 2, EpisodeInt: "2", EpisodeFull: "2 серия", EpisodeType: "tv"},
	}

	if code := h.run("series", "8445"); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	out := h.out.String()
	for _, want := range []string{"8445", "Namae", "Название", "2026", "12", "1 серия", "2 серия"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}
	if len(h.site.calls) != 2 {
		t.Errorf("site calls = %v, want Series and Episodes", h.site.calls)
	}
}

func TestStats(t *testing.T) {
	// The journal and the project configuration have to exist before the
	// harness changes into the directory, so dir is prepared first.
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	log := history.Log{Path: path}
	for i := range 3 {
		var r history.Record
		r.At = time.Date(2026, 9, 10+i, 0, 0, 0, 0, time.UTC)
		r.Series.ID = 8445
		r.Series.Title = "Namae"
		r.Episode.Number = "1"
		r.Upload.Size = 1000
		r.Upload.BytesPerSec = 500
		r.Upload.Hosts = []string{"host-a"}
		if err := log.Append(r); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	// Point the project configuration at this journal.
	cfgPath := filepath.Join(dir, ".sau.toml")
	if err := os.WriteFile(cfgPath, []byte("history_path = "+strconv.Quote(path)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := newHarnessIn(t, dir)
	if code := h.run("stats", "--json"); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	var got history.Stats
	if err := json.Unmarshal([]byte(h.out.String()), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v; %q", err, h.out.String())
	}
	if got.Count != 3 {
		t.Errorf("Count = %d, want 3", got.Count)
	}
	if got.Bytes != 3000 {
		t.Errorf("Bytes = %d, want 3000", got.Bytes)
	}

	h2 := newHarnessIn(t, dir)
	if code := h2.run("stats", "--since", "2026-09-12"); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h2.errOut.String())
	}
	if !strings.Contains(h2.out.String(), "uploads: 1") {
		t.Errorf("stdout = %q, want one upload after the cutoff", h2.out.String())
	}
}

func TestStatsBadSince(t *testing.T) {
	h := newHarness(t)
	if code := h.run("stats", "--since", "yesterday"); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}

// TestUploadJSONKeepsStdoutClean pins down the rule that stdout carries the
// result of the command and nothing else. Everything the reporter says is
// prose for a human and belongs on stderr, so that --json can be piped into a
// parser without filtering.
func TestUploadJSONKeepsStdoutClean(t *testing.T) {
	h := newHarness(t)
	h.runner.out = publish.Outcome{TranslationID: 4242}
	h.runner.onRun = func() {
		r := newConsoleReporter(h.deps)
		r.Begin("episode.mp4", 1000, 2)
		r.Info("checking the session")
		r.Warn("a translation for this episode may already exist")
		r.Event(fineup.Event{Kind: fineup.EventChunkDone, Bytes: 500, Part: 0})
		r.Event(fineup.Event{Kind: fineup.EventHostFailed, Host: "host-a"})
		r.Event(fineup.Event{Kind: fineup.EventFinalizing, Host: "host-b"})
	}
	v := video(t)

	if code := h.run("upload", "--episode", "1", "--series", "5", "--authors", "T", "--json", v); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}

	// Exactly one JSON value on stdout, and nothing after it.
	dec := json.NewDecoder(strings.NewReader(h.out.String()))
	var got struct {
		TranslationID int `json:"translationId"`
	}
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("stdout is not JSON: %v; stdout = %q", err, h.out.String())
	}
	if got.TranslationID != 4242 {
		t.Errorf("translationId = %d, want 4242", got.TranslationID)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		t.Errorf("stdout has more than one value: %q", h.out.String())
	}

	// The prose must have gone somewhere, and that somewhere is stderr.
	for _, want := range []string{"episode.mp4", "checking the session", "may already exist", "host-a", "host-b"} {
		if !strings.Contains(h.errOut.String(), want) {
			t.Errorf("stderr = %q, want it to mention %q", h.errOut.String(), want)
		}
	}
}

func TestLoginCheckAsksForSomethingThatNeedsTheSession(t *testing.T) {
	h := newHarness(t)
	h.env["SAU_USER"] = "translator"
	h.env["SAU_SERIES"] = "8445"

	if code := h.run("login", "--check"); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	// Series is public: it answers without a cookie and would report a dead
	// session as a healthy one. The create form is behind the login.
	if len(h.site.calls) != 1 || h.site.calls[0] != "CreateForm" {
		t.Fatalf("site calls = %v, want CreateForm only", h.site.calls)
	}
	if h.site.formReq.seriesID != 8445 {
		t.Errorf("CreateForm series = %d, want 8445", h.site.formReq.seriesID)
	}
	if h.site.formReq.channel != translation.ChannelCDN {
		t.Errorf("CreateForm channel = %v, want the configured default", h.site.formReq.channel)
	}
	if !strings.Contains(h.out.String(), "session: ok") {
		t.Errorf("stdout = %q", h.out.String())
	}
}

func TestLoginCheckWithoutASeriesUsesAFallback(t *testing.T) {
	h := newHarness(t)
	h.env["SAU_USER"] = "translator"

	if code := h.run("login", "--check"); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if h.site.formReq.seriesID != 1 {
		t.Errorf("CreateForm series = %d, want the fallback 1", h.site.formReq.seriesID)
	}
}

func TestLoginCheckReportsAnExpiredSession(t *testing.T) {
	h := newHarness(t)
	h.env["SAU_USER"] = "translator"
	h.site.err = site.ErrNotAuthorized

	if code := h.run("login", "--check"); code != ExitAuth {
		t.Fatalf("exit code = %d, want %d; stderr = %q", code, ExitAuth, h.errOut.String())
	}
	if !strings.Contains(h.out.String(), "session: expired") {
		t.Errorf("stdout = %q, want it to say the session expired", h.out.String())
	}
}
