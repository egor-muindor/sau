package cli

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sau/internal/publish"
	"sau/internal/site"
	"sau/internal/translation"

	"go.uber.org/goleak"
)

const batchTOML = `
series = 36866
authors = "Team (Alice, Bob)"
episode_pattern = ' - (\d+(?:\.\d+)?) '

[[episodes]]
file = "[T] Show FIX 1080p.mp4"
episode = "2"
sub = "02.ass"
`

// batchSetup writes the project file and the named video files into one
// directory and returns a harness running there plus the file paths, in the
// order given.
func batchSetup(t *testing.T, toml string, names ...string) (*harness, []string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".sau.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(names))
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return newHarnessIn(t, dir), paths
}

func threeEpisodes(t *testing.T) (*harness, []string) {
	t.Helper()
	return batchSetup(t, batchTOML,
		"[T] Show FIX 1080p.mp4",
		"[T] Show - 03 [1080p].mp4",
		"[T] Show - 04 [1080p].mp4",
	)
}

func TestUploadBatchPlanAndOrder(t *testing.T) {
	h, files := threeEpisodes(t)
	h.runner.out = publish.Outcome{TranslationID: 4242}

	// Out of episode order on the command line; the plan sorts by number.
	code := h.run("upload", "--yes", files[2], files[0], files[1])
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if got := strings.Join(h.runner.calls, ","); got != "Status,CheckSession,RunBatch" {
		t.Fatalf("runner calls = %q", got)
	}

	items := h.runner.batchItems
	if len(items) != 3 {
		t.Fatalf("batch items = %d, want 3", len(items))
	}
	for i, want := range []string{"2", "3", "4"} {
		if items[i].Request.Draft.EpisodeNumber != want {
			t.Errorf("items[%d].EpisodeNumber = %q, want %q", i, items[i].Request.Draft.EpisodeNumber, want)
		}
		if items[i].Request.Draft.SeriesID != 36866 || items[i].Request.Draft.Authors != "Team (Alice, Bob)" {
			t.Errorf("items[%d] draft = %+v", i, items[i].Request.Draft)
		}
		if items[i].Report != nil {
			t.Errorf("items[%d].Report set with parallel 1; the console reporter must be used", i)
		}
	}
	if want := filepath.Join(filepath.Dir(files[0]), "02.ass"); items[0].Request.Draft.SubPath != want {
		t.Errorf("items[0].SubPath = %q, want %q", items[0].Request.Draft.SubPath, want)
	}
	if items[1].Request.Draft.SubPath != "" {
		t.Errorf("items[1].SubPath = %q, want none", items[1].Request.Draft.SubPath)
	}
	if h.runner.batchParallel != 1 {
		t.Errorf("parallel = %d, want 1", h.runner.batchParallel)
	}

	out := h.out.String()
	for _, want := range []string{
		"FILE", "EPISODE", "SUB", "STATE",
		"[T] Show FIX 1080p.mp4", "02.ass", "new",
		"series 36866, voiceRu, Team (Alice, Bob), channel cdn, parallel 1",
		"RESULT", "ok, translation 4242",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if i2, i3, i4 := strings.Index(out, "FIX"), strings.Index(out, "- 03"), strings.Index(out, "- 04"); !(i2 < i3 && i3 < i4) {
		t.Errorf("plan is not in episode order:\n%s", out)
	}
	if strings.Contains(h.errOut.String(), "continue?") {
		t.Errorf("stderr = %q, want no question with --yes", h.errOut.String())
	}
}

func TestUploadBatchAsksBeforeStarting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stdin string
		runs  bool
	}{
		{"answer y", "y\n", true},
		{"answer n", "n\n", false},
		{"no console", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, files := threeEpisodes(t)
			h.deps.Stdin = strings.NewReader(tc.stdin)
			code := h.run("upload", files[0], files[1], files[2])
			if code != ExitOK {
				t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
			}
			if !strings.Contains(h.errOut.String(), "continue? [y/N]") {
				t.Errorf("stderr = %q, want the question", h.errOut.String())
			}
			ran := strings.Contains(strings.Join(h.runner.calls, ","), "RunBatch")
			if ran != tc.runs {
				t.Errorf("RunBatch called = %v, want %v (calls %v)", ran, tc.runs, h.runner.calls)
			}
			if !tc.runs && !strings.Contains(h.out.String(), "nothing done") {
				t.Errorf("stdout = %q, want \"nothing done\"", h.out.String())
			}
		})
	}
}

func TestUploadBatchRejectsEpisodeAndSubWithSeveralFiles(t *testing.T) {
	for _, tc := range []struct{ flag, value, want string }{
		{"--episode", "3", "--episode"},
		{"--sub", "x.ass", "--sub"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			h, files := threeEpisodes(t)
			code := h.run("upload", tc.flag, tc.value, files[0], files[1])
			if code != ExitUsage {
				t.Fatalf("exit code = %d, want %d; stderr = %q", code, ExitUsage, h.errOut.String())
			}
			if !strings.Contains(h.errOut.String(), tc.want) {
				t.Errorf("stderr = %q, want it to mention %s", h.errOut.String(), tc.want)
			}
			if len(h.runner.calls) != 0 {
				t.Errorf("runner was called: %v", h.runner.calls)
			}
		})
	}
}

func TestUploadBatchParallelFlag(t *testing.T) {
	h, files := threeEpisodes(t)
	if code := h.run("upload", "--yes", "--parallel", "2", files[0], files[1], files[2]); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if h.runner.batchParallel != 2 {
		t.Errorf("parallel = %d, want 2", h.runner.batchParallel)
	}
	// With several workers every item carries its own reporter, so that
	// the lines of the progress display can be told apart.
	for i, it := range h.runner.batchItems {
		if it.Report == nil {
			t.Errorf("items[%d].Report is nil with parallel 2", i)
		}
	}
	if !strings.Contains(h.out.String(), "parallel 2") {
		t.Errorf("stdout = %q, want the plan footer to say parallel 2", h.out.String())
	}

	h2, files2 := threeEpisodes(t)
	if code := h2.run("upload", "--yes", "--parallel", "0", files2[0]); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}

func TestUploadBatchPreflightErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		h, files := threeEpisodes(t)
		code := h.run("upload", "--yes", files[0], filepath.Join(filepath.Dir(files[0]), "[T] Show - 09 [1080p].mp4"))
		if code != ExitUsage {
			t.Fatalf("exit code = %d, want %d; stderr = %q", code, ExitUsage, h.errOut.String())
		}
		if !strings.Contains(h.errOut.String(), "- 09") {
			t.Errorf("stderr = %q, want it to name the file", h.errOut.String())
		}
	})
	t.Run("same file twice", func(t *testing.T) {
		h, files := threeEpisodes(t)
		code := h.run("upload", "--yes", files[1], files[1])
		if code != ExitUsage {
			t.Fatalf("exit code = %d, want %d; stderr = %q", code, ExitUsage, h.errOut.String())
		}
		if !strings.Contains(h.errOut.String(), "twice") {
			t.Errorf("stderr = %q", h.errOut.String())
		}
	})
	t.Run("two files with one number", func(t *testing.T) {
		h, files := batchSetup(t, batchTOML, "[T] Show - 03 [1080p].mp4", "[T] Show - 03 [720p].mp4")
		code := h.run("upload", "--yes", files[0], files[1])
		if code != ExitUsage {
			t.Fatalf("exit code = %d, want %d; stderr = %q", code, ExitUsage, h.errOut.String())
		}
		if !strings.Contains(h.errOut.String(), "episode 3") {
			t.Errorf("stderr = %q, want it to name the number", h.errOut.String())
		}
	})
	t.Run("number not resolvable", func(t *testing.T) {
		h, files := batchSetup(t, batchTOML, "[T] Show - 03 [1080p].mp4", "random.mp4")
		code := h.run("upload", "--yes", files[0], files[1])
		if code != ExitUsage {
			t.Fatalf("exit code = %d, want %d; stderr = %q", code, ExitUsage, h.errOut.String())
		}
		if !strings.Contains(h.errOut.String(), "random.mp4") || !strings.Contains(h.errOut.String(), "--episode") {
			t.Errorf("stderr = %q, want the file and the way out", h.errOut.String())
		}
	})
	t.Run("nothing touches the runner", func(t *testing.T) {
		h, files := batchSetup(t, batchTOML, "random.mp4")
		h.run("upload", "--yes", files[0])
		if len(h.runner.calls) != 0 {
			t.Errorf("runner was called: %v", h.runner.calls)
		}
	})
}

func TestUploadBatchChannelMismatchStopsBeforeStart(t *testing.T) {
	h, files := threeEpisodes(t)
	h.runner.states = []publish.UploadState{
		{Path: files[1], Phase: publish.PhaseUploading, Channel: translation.ChannelRU},
	}
	code := h.run("upload", "--yes", files[0], files[1], files[2])
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d; stderr = %q", code, ExitUsage, h.errOut.String())
	}
	if !strings.Contains(h.errOut.String(), "channel") {
		t.Errorf("stderr = %q", h.errOut.String())
	}
	if strings.Contains(strings.Join(h.runner.calls, ","), "RunBatch") {
		t.Errorf("RunBatch was called: %v", h.runner.calls)
	}
}

func TestUploadBatchStatesInPlan(t *testing.T) {
	h, files := threeEpisodes(t)
	h.runner.states = []publish.UploadState{
		{Path: files[0], Phase: publish.PhaseUploading, Channel: translation.ChannelCDN,
			Video: &publish.FileUpload{TotalParts: 10, Done: []int{0, 1, 2}}},
		{Path: files[1], Phase: publish.PhaseUploaded, Channel: translation.ChannelCDN},
		{Path: files[2], Phase: publish.PhaseSubmitUnknown, Channel: translation.ChannelCDN},
	}
	code := h.run("upload", "--yes", files[0], files[1], files[2])
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	out := h.out.String()
	for _, want := range []string{"resume 30%", "uploaded, submit only"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	// The unresolved one is listed separately with the way out, and is not
	// part of the batch.
	if !strings.Contains(h.errOut.String(), "- 04") || !strings.Contains(h.errOut.String(), "sau resolve") {
		t.Errorf("stderr = %q, want the pending file and a hint about sau resolve", h.errOut.String())
	}
	if n := len(h.runner.batchItems); n != 2 {
		t.Fatalf("batch items = %d, want 2", n)
	}
	for _, it := range h.runner.batchItems {
		if it.Request.Draft.VideoPath == files[2] {
			t.Error("the file that needs a decision was put in the batch")
		}
	}
}

func TestUploadBatchAllNeedADecision(t *testing.T) {
	h, files := threeEpisodes(t)
	h.runner.states = []publish.UploadState{
		{Path: files[0], Phase: publish.PhaseSubmitting, Channel: translation.ChannelCDN},
	}
	code := h.run("upload", "--yes", files[0])
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if !strings.Contains(h.out.String(), "nothing done") {
		t.Errorf("stdout = %q", h.out.String())
	}
	if strings.Contains(strings.Join(h.runner.calls, ","), "RunBatch") {
		t.Errorf("RunBatch was called: %v", h.runner.calls)
	}
}

func TestUploadBatchDryRunRunsEachInTurn(t *testing.T) {
	h, files := threeEpisodes(t)
	code := h.run("upload", "--dry-run", files[0], files[1], files[2])
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if got := strings.Join(h.runner.calls, ","); got != "Status,Run,Run,Run" {
		t.Fatalf("runner calls = %q, want a sequential dry run per episode and no question", got)
	}
	for i, req := range h.runner.reqs {
		if !req.DryRun {
			t.Errorf("reqs[%d].DryRun = false", i)
		}
	}
	if strings.Contains(h.errOut.String(), "continue?") {
		t.Error("a dry run must not ask")
	}
	if !strings.Contains(h.out.String(), "ok, dry run") {
		t.Errorf("stdout = %q, want the summary", h.out.String())
	}
}

func TestUploadBatchSummaryAndExitCode(t *testing.T) {
	rejected := &site.RejectedError{Messages: []string{"Episode number is required.", "Authors are required."}}
	uploadFailed := &publish.UploadFailedError{Err: errors.New("host gone")}
	unknown := &publish.UnknownOutcomeError{}

	cases := []struct {
		name string
		errs []error
		want int
		text []string
	}{
		{"all ok", []error{nil, nil, nil}, ExitOK, []string{"ok, translation 4242"}},
		{"rejected", []error{nil, rejected, nil}, ExitRejected, []string{"rejected: Episode number is required."}},
		{"upload failed and rejected", []error{uploadFailed, rejected, nil}, ExitRejected, []string{"upload failed (state kept)"}},
		{"unknown beats rejected", []error{unknown, rejected, nil}, ExitUnknown, []string{"outcome unknown"}},
		{"auth beats everything", []error{publish.ErrAuth, publish.ErrCancelled, unknown}, ExitAuth, []string{"authorization failed", "cancelled"}},
		{"plain error", []error{errors.New("disk full"), nil, nil}, ExitError, []string{"error: disk full"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, files := threeEpisodes(t)
			h.runner.batchFn = func(items []publish.BatchItem) []publish.BatchResult {
				res := make([]publish.BatchResult, len(items))
				for i, it := range items {
					res[i] = publish.BatchResult{Item: it, Outcome: &publish.Outcome{TranslationID: 4242}, Err: tc.errs[i]}
					if tc.errs[i] != nil {
						res[i].Outcome = &publish.Outcome{}
					}
				}
				return res
			}
			code := h.run("upload", "--yes", files[0], files[1], files[2])
			if code != tc.want {
				t.Fatalf("exit code = %d, want %d; stderr = %q", code, tc.want, h.errOut.String())
			}
			out := h.out.String()
			if !strings.Contains(out, "EPISODE") || !strings.Contains(out, "RESULT") {
				t.Errorf("stdout lacks the summary header:\n%s", out)
			}
			for _, want := range tc.text {
				if !strings.Contains(out, want) {
					t.Errorf("stdout lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestUploadBatchSummaryShowsRefusedQuestions(t *testing.T) {
	h, files := threeEpisodes(t)
	h.runner.batchFn = func(items []publish.BatchItem) []publish.BatchResult {
		res := make([]publish.BatchResult, len(items))
		for i, it := range items {
			res[i] = publish.BatchResult{Item: it, Outcome: &publish.Outcome{TranslationID: 4242}}
		}
		res[0].Err = &publish.UnknownOutcomeError{}
		res[0].Question = "A previous run sent the form.\nIs this translation already published?"
		return res
	}
	code := h.run("upload", "--yes", "--parallel", "2", files[0], files[1], files[2])
	if code != ExitUnknown {
		t.Fatalf("exit code = %d, want %d", code, ExitUnknown)
	}
	if !strings.Contains(h.out.String(), "needs decision: A previous run sent the form.") {
		t.Errorf("stdout = %q", h.out.String())
	}
}

func TestUploadBatchJSON(t *testing.T) {
	h, files := threeEpisodes(t)
	h.runner.out = publish.Outcome{TranslationID: 4242}
	code := h.run("upload", "--yes", "--json", files[0], files[1], files[2])
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	var got []struct {
		File          string `json:"file"`
		Episode       string `json:"episode"`
		Status        string `json:"status"`
		TranslationID int    `json:"translationId"`
	}
	if err := json.Unmarshal([]byte(h.out.String()), &got); err != nil {
		t.Fatalf("stdout is not a JSON array: %v; %q", err, h.out.String())
	}
	if len(got) != 3 || got[0].Episode != "2" || got[0].TranslationID != 4242 || got[0].Status != "ok, translation 4242" {
		t.Errorf("results = %+v", got)
	}
	if got[0].File != "[T] Show FIX 1080p.mp4" {
		t.Errorf("results[0].File = %q, want the bare file name", got[0].File)
	}
}

func TestUploadBatchCheckSessionFailureStopsTheBatch(t *testing.T) {
	h, files := threeEpisodes(t)
	h.runner.checkErr = publish.ErrAuth
	code := h.run("upload", "--yes", files[0], files[1])
	if code != ExitAuth {
		t.Fatalf("exit code = %d, want %d", code, ExitAuth)
	}
	if strings.Contains(strings.Join(h.runner.calls, ","), "RunBatch") {
		t.Errorf("RunBatch was called: %v", h.runner.calls)
	}
}

func TestUploadSingleFileWithEpisodeIsUnchanged(t *testing.T) {
	h, files := threeEpisodes(t)
	h.runner.out = publish.Outcome{TranslationID: 4242}
	code := h.run("upload", "--episode", "7", files[1])
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if got := strings.Join(h.runner.calls, ","); got != "Run" {
		t.Fatalf("runner calls = %q, want Run only", got)
	}
	// --episode wins over the pattern, no plan table, no question.
	if h.runner.req.Draft.EpisodeNumber != "7" {
		t.Errorf("EpisodeNumber = %q, want 7", h.runner.req.Draft.EpisodeNumber)
	}
	if strings.Contains(h.out.String(), "FILE") || strings.Contains(h.errOut.String(), "continue?") {
		t.Errorf("single file with --episode printed the plan: stdout %q, stderr %q", h.out.String(), h.errOut.String())
	}
	if !strings.Contains(h.out.String(), "published: translation 4242") {
		t.Errorf("stdout = %q", h.out.String())
	}
}

func TestUploadSingleFileWithoutEpisodeUsesTheConfig(t *testing.T) {
	h, files := threeEpisodes(t)
	code := h.run("upload", "--yes", files[1])
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if got := strings.Join(h.runner.calls, ","); got != "Status,CheckSession,RunBatch" {
		t.Fatalf("runner calls = %q", got)
	}
	if len(h.runner.batchItems) != 1 || h.runner.batchItems[0].Request.Draft.EpisodeNumber != "3" {
		t.Errorf("batch items = %+v", h.runner.batchItems)
	}
	if !strings.Contains(h.out.String(), "FILE") {
		t.Errorf("stdout = %q, want the plan table", h.out.String())
	}
}

func TestUploadPatternStripsLeadingZeros(t *testing.T) {
	h, files := batchSetup(t, batchTOML, "[T] Show - 03 [1080p].mp4", "[T] Show - 10 [1080p].mp4")
	if code := h.run("upload", "--yes", files[1], files[0]); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	got := []string{
		h.runner.batchItems[0].Request.Draft.EpisodeNumber,
		h.runner.batchItems[1].Request.Draft.EpisodeNumber,
	}
	if got[0] != "3" || got[1] != "10" {
		t.Errorf("episodes = %v, want [3 10]: numeric order, no leading zero", got)
	}
}

func TestUploadBatchFreshIgnoresSavedChannel(t *testing.T) {
	h, files := threeEpisodes(t)
	h.runner.states = []publish.UploadState{
		{Path: files[0], Phase: publish.PhaseUploading, Channel: translation.ChannelRU,
			Video: &publish.FileUpload{TotalParts: 10, Done: []int{0, 1, 2}}},
		{Path: files[2], Phase: publish.PhaseSubmitting, Channel: translation.ChannelRU},
	}
	code := h.run("upload", "--yes", "--fresh", files[0], files[1], files[2])
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if out := h.out.String(); strings.Contains(out, "resume") || strings.Contains(out, "channel mismatch") {
		t.Errorf("stdout = %q, want every item planned as new", out)
	}
	// A pending decision is still diverted, --fresh or not: discarding it
	// would lose the only record of what was sent.
	if !strings.Contains(h.errOut.String(), "sau resolve") {
		t.Errorf("stderr = %q, want the sau resolve hint", h.errOut.String())
	}
	if n := len(h.runner.batchItems); n != 2 {
		t.Fatalf("batch items = %d, want 2", n)
	}
}

// barWatch is a stdout that notices a write made while the bar of the
// reporter is still redrawing. The summary must never be such a write, or
// its header lands on the bar line.
type barWatch struct {
	w   io.Writer
	rep **consoleReporter
	// clash is set when a write finds the bar's goroutine still running.
	clash bool
}

func (b *barWatch) Write(p []byte) (int, error) {
	if r := *b.rep; r != nil {
		r.mu.Lock()
		done := r.done
		r.mu.Unlock()
		if done != nil {
			select {
			case <-done:
			default:
				b.clash = true
			}
		}
	}
	return b.w.Write(p)
}

// With one worker the batch drives the single bar, and only the next Begin
// or stopProgress takes a bar down. The summary is printed by the batch
// itself, before Run's stopProgress, so the batch has to stop the bar first.
func TestUploadBatchStopsTheBarBeforeTheSummary(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	h, files := threeEpisodes(t)
	h.runner.out = publish.Outcome{TranslationID: 4242}

	var rep *consoleReporter
	watch := &barWatch{w: &h.out, rep: &rep}
	h.deps.Stdout = watch
	h.runner.batchFn = func(items []publish.BatchItem) []publish.BatchResult {
		// What a real runner leaves behind: the bar of the last file, still
		// redrawing.
		rep = newConsoleReporter(h.deps).(*consoleReporter)
		rep.Begin("episode.mp4", 1000, 2)
		res := make([]publish.BatchResult, len(items))
		for i, it := range items {
			out := h.runner.out
			res[i] = publish.BatchResult{Item: it, Outcome: &out}
		}
		return res
	}

	if code := h.run("upload", "--yes", files[0], files[1], files[2]); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if watch.clash {
		t.Error("the summary was written while the bar was still redrawing")
	}
	if !strings.Contains(h.out.String(), "RESULT") {
		t.Errorf("stdout lacks the summary:\n%s", h.out.String())
	}
}

// With --json the plan table stays off stdout, which must remain one JSON
// array. Without --yes the question is still asked, and nobody should have
// to answer it blind: the plan goes to stderr, where the question is.
func TestUploadBatchJSONShowsThePlanBeforeTheQuestion(t *testing.T) {
	h, files := threeEpisodes(t)
	h.runner.out = publish.Outcome{TranslationID: 4242}
	h.deps.Stdin = strings.NewReader("y\n")

	if code := h.run("upload", "--json", files[0], files[1], files[2]); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	errOut := h.errOut.String()
	plan, question := strings.Index(errOut, "FILE"), strings.Index(errOut, "continue? [y/N]")
	if plan < 0 || question < 0 || plan > question {
		t.Errorf("stderr = %q, want the plan table before the question", errOut)
	}
	for _, want := range []string{"[T] Show FIX 1080p.mp4", "series 36866, voiceRu, Team (Alice, Bob), channel cdn, parallel 1"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	var got []struct{ Episode string }
	if err := json.Unmarshal([]byte(h.out.String()), &got); err != nil {
		t.Fatalf("stdout is not a JSON array: %v; %q", err, h.out.String())
	}
	if len(got) != 3 {
		t.Errorf("results = %+v", got)
	}
}

// With --json and --yes there is no question, so there is no plan either:
// the caller is a script and asked for nothing but the array.
func TestUploadBatchJSONWithYesPrintsNoPlan(t *testing.T) {
	h, files := threeEpisodes(t)
	h.runner.out = publish.Outcome{TranslationID: 4242}
	if code := h.run("upload", "--json", "--yes", files[0]); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	if strings.Contains(h.errOut.String(), "FILE") {
		t.Errorf("stderr = %q, want no plan", h.errOut.String())
	}
}
