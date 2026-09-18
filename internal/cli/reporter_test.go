package cli

import (
	"strings"
	"testing"

	"go.uber.org/goleak"

	"sau/internal/fineup"
	"sau/internal/progress"
)

// TestProgressStopsWithTheRun pins down the rule that the bar of the last file
// is taken down before Run returns. Begin starts a redraw goroutine per file,
// and only Begin of the next file stops the previous one, so without an
// explicit stop the goroutine of the last file outlives the command and keeps
// writing to the terminal while the error message is being printed.
func TestProgressStopsWithTheRun(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	var out, errOut strings.Builder
	d := Deps{Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader("")}

	r := newConsoleReporter(d)
	r.Begin("episode.mp4", 1000, 2)

	stopProgress()

	if activeReporter != nil {
		t.Error("activeReporter still set after stopProgress")
	}
	if !strings.HasSuffix(errOut.String(), "\n") {
		t.Errorf("stderr = %q, want the bar to end with a newline", errOut.String())
	}

	// A second call must be harmless: Run stops the bar whether or not an
	// upload ever started.
	stopProgress()
}

func TestProgressStopIsSafeWithoutAnyUpload(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	stopProgress()
}

func TestReporterAskReadsTheAnswer(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	var out, errOut strings.Builder
	for _, tc := range []struct {
		in   string
		want bool
	}{{"y\n", true}, {"yes\n", true}, {"n\n", false}, {"\n", false}} {
		d := Deps{Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader(tc.in)}
		got, err := newConsoleReporter(d).Ask("submit the form?")
		if err != nil {
			t.Fatalf("Ask(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("Ask(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	stopProgress()
}

func TestBatchReporterDrawsOnItsOwnLine(t *testing.T) {
	var errOut strings.Builder
	m := &progress.Multi{W: &errOut}
	r := &batchReporter{d: Deps{Stderr: &errOut}, multi: m, name: "ep02.mp4"}

	r.Begin("ep02.mp4", 1000, 2)
	r.Event(fineup.Event{Kind: fineup.EventChunkDone, Bytes: 500})
	r.Warn("host a failed")
	r.Info("finalizing")
	r.Done()

	out := errOut.String()
	for _, want := range []string{
		"ep02.mp4: 1000 bytes in 2 chunks",
		"sau: ep02.mp4: host a failed",
		"ep02.mp4: finalizing",
		"50%",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr lacks %q:\n%s", want, out)
		}
	}
}

func TestBatchReporterWithoutATerminalPrintsTextOnly(t *testing.T) {
	var errOut strings.Builder
	r := &batchReporter{d: Deps{Stderr: &errOut}, name: "ep02.mp4"}

	r.Begin("ep02.mp4", 1000, 2)
	r.Event(fineup.Event{Kind: fineup.EventChunkDone, Bytes: 500})
	r.Event(fineup.Event{Kind: fineup.EventHostFailed, Host: "host-a"})
	r.Done()

	out := errOut.String()
	if strings.Contains(out, "\x1b") {
		t.Errorf("stderr contains an escape sequence without a terminal:\n%q", out)
	}
	for _, want := range []string{"ep02.mp4: 1000 bytes in 2 chunks", "host host-a failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr lacks %q:\n%s", want, out)
		}
	}
	if _, err := r.Ask("really?"); err == nil {
		t.Error("Ask with several episodes running must fail rather than guess")
	}
}
