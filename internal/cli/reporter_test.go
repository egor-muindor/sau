package cli

import (
	"strings"
	"testing"

	"go.uber.org/goleak"
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
