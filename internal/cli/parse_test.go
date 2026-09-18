package cli

import (
	"strings"
	"testing"

	"sau/internal/translation"
)

func TestRunUsageErrors(t *testing.T) {
	v := video(t)

	cases := []struct {
		name string
		args []string
		want string // a fragment that must appear on stderr
	}{
		{"no command", nil, "no command"},
		{"unknown command", []string{"frobnicate"}, "unknown command"},
		{"upload without episode", []string{"upload", "--series", "1", "--authors", "T", v}, "--episode"},
		{"upload without a file", []string{"upload", "--episode", "1", "--series", "1"}, "at least one video file"},
		{"upload without series", []string{"upload", "--episode", "1", "--authors", "T", v}, "--series"},
		{"bad channel", []string{"upload", "--episode", "1", "--series", "1", "--authors", "T", "--channel", "moon", v}, "channel"},
		{"bad episode type", []string{"upload", "--episode", "1", "--series", "1", "--authors", "T", "--episode-type", "book", v}, "episode type"},
		{"unknown flag", []string{"upload", "--nope", v}, ""},
		{"resolve without a decision", []string{"resolve", v}, "--submitted"},
		{"resolve with both decisions", []string{"resolve", "--submitted", "--resend", v}, "--submitted"},
		{"resolve without a file", []string{"resolve", "--submitted"}, "exactly one"},
		{"abort without a file", []string{"abort"}, "exactly one"},
		{"series without an id", []string{"series"}, "exactly one"},
		{"series with a non-numeric id", []string{"series", "xyz"}, "number"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			code := h.run(tc.args...)
			if code != ExitUsage {
				t.Fatalf("exit code = %d, want %d; stderr = %q", code, ExitUsage, h.errOut.String())
			}
			if tc.want != "" && !strings.Contains(h.errOut.String(), tc.want) {
				t.Errorf("stderr = %q, want it to mention %q", h.errOut.String(), tc.want)
			}
			if !strings.Contains(h.errOut.String(), "usage:") {
				t.Errorf("stderr = %q, want the usage text", h.errOut.String())
			}
			if len(h.runner.calls) != 0 {
				t.Errorf("runner was called: %v", h.runner.calls)
			}
		})
	}
}

func TestUploadBuildsRequestFromFlags(t *testing.T) {
	h := newHarness(t)
	v := video(t)

	code := h.run("upload",
		"--episode", "7.5",
		"--series", "42",
		"--episode-type", "ova",
		"--type", "subRu",
		"--authors", "Team (Alice, Bob)",
		"--by-author",
		"--channel", "ru",
		"--concurrency", "3",
		"--fresh",
		"--no-submit",
		"--sub", "subs.ass",
		v,
	)
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if len(h.runner.calls) != 1 || h.runner.calls[0] != "Run" {
		t.Fatalf("runner calls = %v", h.runner.calls)
	}

	req := h.runner.req
	if req.Draft.SeriesID != 42 {
		t.Errorf("SeriesID = %d", req.Draft.SeriesID)
	}
	if req.Draft.EpisodeNumber != "7.5" {
		t.Errorf("EpisodeNumber = %q", req.Draft.EpisodeNumber)
	}
	if req.Draft.EpisodeType != translation.OVA {
		t.Errorf("EpisodeType = %q", req.Draft.EpisodeType)
	}
	if req.Draft.Type != translation.SubRu {
		t.Errorf("Type = %q", req.Draft.Type)
	}
	if req.Draft.Authors != "Team (Alice, Bob)" {
		t.Errorf("Authors = %q", req.Draft.Authors)
	}
	if !req.Draft.AddedByAuthor {
		t.Error("AddedByAuthor = false")
	}
	if req.Draft.VideoPath != v {
		t.Errorf("VideoPath = %q, want %q", req.Draft.VideoPath, v)
	}
	if req.Draft.SubPath != "subs.ass" {
		t.Errorf("SubPath = %q", req.Draft.SubPath)
	}
	if req.Channel != translation.ChannelRU {
		t.Errorf("Channel = %v", req.Channel)
	}
	if req.Concurrency != 3 {
		t.Errorf("Concurrency = %d", req.Concurrency)
	}
	if !req.Fresh || !req.NoSubmit || req.DryRun {
		t.Errorf("Fresh=%v NoSubmit=%v DryRun=%v", req.Fresh, req.NoSubmit, req.DryRun)
	}
	if req.Password == nil {
		t.Error("Password source is nil")
	}
}

func TestUploadTakesDefaultsFromEnvironment(t *testing.T) {
	h := newHarness(t)
	h.env["SAU_SERIES"] = "17"
	h.env["SAU_AUTHORS"] = "Solo"
	h.env["SAU_CHANNEL"] = "all"
	v := video(t)

	if code := h.run("upload", "--episode", "3", v); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if h.runner.req.Draft.SeriesID != 17 {
		t.Errorf("SeriesID = %d, want 17", h.runner.req.Draft.SeriesID)
	}
	if h.runner.req.Channel != translation.ChannelAll {
		t.Errorf("Channel = %v, want all", h.runner.req.Channel)
	}
	// Defaults survive: nothing set the episode type or the translation type.
	if h.runner.req.Draft.EpisodeType != translation.TV {
		t.Errorf("EpisodeType = %q, want tv", h.runner.req.Draft.EpisodeType)
	}
	if h.runner.req.Draft.Type != translation.VoiceRu {
		t.Errorf("Type = %q, want voiceRu", h.runner.req.Draft.Type)
	}
}

func TestUploadFlagBeatsEnvironment(t *testing.T) {
	h := newHarness(t)
	h.env["SAU_SERIES"] = "17"
	h.env["SAU_AUTHORS"] = "Solo"
	v := video(t)

	if code := h.run("upload", "--episode", "3", "--series", "99", v); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if h.runner.req.Draft.SeriesID != 99 {
		t.Errorf("SeriesID = %d, want 99", h.runner.req.Draft.SeriesID)
	}
}

func TestHelpIsNotAnError(t *testing.T) {
	h := newHarness(t)
	if code := h.run("help"); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(h.out.String(), "usage:") {
		t.Errorf("stdout = %q, want the usage text", h.out.String())
	}
}

// TestFlagsAndFileInterleave pins down the order shown in the usage text and in
// docs/architecture.md section 6: the file comes first, the flags follow. The
// flag package stops at the first positional argument, so both orders have to
// be stitched together by hand.
func TestFlagsAndFileInterleave(t *testing.T) {
	t.Run("upload with the file first", func(t *testing.T) {
		h := newHarness(t)
		v := video(t)
		if code := h.run("upload", v, "--episode", "7", "--series", "42", "--authors", "T"); code != ExitOK {
			t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
		}
		if h.runner.req.Draft.VideoPath != v {
			t.Errorf("VideoPath = %q, want %q", h.runner.req.Draft.VideoPath, v)
		}
		if h.runner.req.Draft.SeriesID != 42 || h.runner.req.Draft.EpisodeNumber != "7" {
			t.Errorf("series = %d, episode = %q", h.runner.req.Draft.SeriesID, h.runner.req.Draft.EpisodeNumber)
		}
	})

	t.Run("upload with the file in the middle", func(t *testing.T) {
		h := newHarness(t)
		v := video(t)
		if code := h.run("upload", "--episode", "7", v, "--series", "42", "--authors", "T"); code != ExitOK {
			t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
		}
		if h.runner.req.Draft.VideoPath != v || h.runner.req.Draft.SeriesID != 42 {
			t.Errorf("VideoPath = %q, series = %d", h.runner.req.Draft.VideoPath, h.runner.req.Draft.SeriesID)
		}
	})

	t.Run("upload with two files", func(t *testing.T) {
		h := newHarness(t)
		v := video(t)
		if code := h.run("upload", v, "--episode", "7", v); code != ExitUsage {
			t.Fatalf("exit code = %d, want %d", code, ExitUsage)
		}
	})

	for _, order := range [][]string{
		{"resolve", "/videos/ep01.mp4", "--submitted"},
		{"resolve", "--submitted", "/videos/ep01.mp4"},
	} {
		t.Run("resolve "+strings.Join(order[1:], " "), func(t *testing.T) {
			h := newHarness(t)
			if code := h.run(order...); code != ExitOK {
				t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
			}
			if h.runner.resolve.path != "/videos/ep01.mp4" || !h.runner.resolve.submitted {
				t.Errorf("path = %q, submitted = %v", h.runner.resolve.path, h.runner.resolve.submitted)
			}
		})
	}

	t.Run("abort with one file", func(t *testing.T) {
		h := newHarness(t)
		if code := h.run("abort", "/videos/ep01.mp4"); code != ExitOK {
			t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
		}
		if h.runner.aborted != "/videos/ep01.mp4" {
			t.Errorf("aborted = %q", h.runner.aborted)
		}
	})

	t.Run("abort with two files", func(t *testing.T) {
		h := newHarness(t)
		if code := h.run("abort", "/videos/ep01.mp4", "/videos/ep02.mp4"); code != ExitUsage {
			t.Fatalf("exit code = %d, want %d", code, ExitUsage)
		}
	})

	// A name that begins with a dash is still reachable, the usual way.
	t.Run("file after the terminator", func(t *testing.T) {
		h := newHarness(t)
		if code := h.run("abort", "--", "-weird.mp4"); code != ExitOK {
			t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
		}
		if h.runner.aborted != "-weird.mp4" {
			t.Errorf("aborted = %q", h.runner.aborted)
		}
	})
}

func TestStatsRejectsANegativeSeries(t *testing.T) {
	h := newHarness(t)
	if code := h.run("stats", "--series", "-3"); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d; stderr = %q", code, ExitUsage, h.errOut.String())
	}
}

// TestUploadRejectsAnUnreadableVideo keeps a mistyped path in the class of
// errors the user can fix by retyping the command. The runner stats the file
// too, but from there a missing file is an ordinary failure, and a typo should
// not look like one.
func TestUploadRejectsAnUnreadableVideo(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		h := newHarness(t)
		code := h.run("upload", "--episode", "1", "--series", "5", "--authors", "T", "/no/such/file.mp4")
		if code != ExitUsage {
			t.Fatalf("exit code = %d, want %d; stderr = %q", code, ExitUsage, h.errOut.String())
		}
		if !strings.Contains(h.errOut.String(), "/no/such/file.mp4") {
			t.Errorf("stderr = %q, want it to name the file", h.errOut.String())
		}
		if len(h.runner.calls) != 0 {
			t.Errorf("runner was called: %v", h.runner.calls)
		}
	})

	t.Run("directory", func(t *testing.T) {
		h := newHarness(t)
		code := h.run("upload", "--episode", "1", "--series", "5", "--authors", "T", t.TempDir())
		if code != ExitUsage {
			t.Fatalf("exit code = %d, want %d; stderr = %q", code, ExitUsage, h.errOut.String())
		}
	})
}
