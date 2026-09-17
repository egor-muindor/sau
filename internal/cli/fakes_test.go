package cli

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sau/internal/config"
	"sau/internal/publish"
	"sau/internal/secrets"
	"sau/internal/site"
	"sau/internal/translation"
)

type fakeRunner struct {
	// onRun runs inside Run, where a real runner would be driving the
	// reporter. It lets a test check where the reporter's prose lands.
	onRun   func()
	req     publish.Request
	calls   []string
	out     publish.Outcome
	err     error
	states  []publish.UploadState
	aborted string
	resolve struct {
		path      string
		submitted bool
	}
}

func (f *fakeRunner) Run(ctx context.Context, req publish.Request) (publish.Outcome, error) {
	f.calls = append(f.calls, "Run")
	f.req = req
	if f.onRun != nil {
		f.onRun()
	}
	return f.out, f.err
}

func (f *fakeRunner) Abort(ctx context.Context, path string) error {
	f.calls = append(f.calls, "Abort")
	f.aborted = path
	return f.err
}

func (f *fakeRunner) Resolve(ctx context.Context, path string, submitted bool) error {
	f.calls = append(f.calls, "Resolve")
	f.resolve.path = path
	f.resolve.submitted = submitted
	return f.err
}

func (f *fakeRunner) Status() ([]publish.UploadState, error) {
	f.calls = append(f.calls, "Status")
	return f.states, f.err
}

type fakeSite struct {
	calls    []string
	series   site.Series
	episodes []site.Episode
	form     site.CreateForm
	formReq  struct {
		seriesID int
		channel  translation.Channel
	}
	err error
}

func (f *fakeSite) CreateForm(ctx context.Context, seriesID int, ch translation.Channel) (site.CreateForm, error) {
	f.calls = append(f.calls, "CreateForm")
	f.formReq.seriesID = seriesID
	f.formReq.channel = ch
	return f.form, f.err
}

func (f *fakeSite) Login(ctx context.Context, user string, pass secrets.Secret) error {
	f.calls = append(f.calls, "Login")
	return f.err
}

func (f *fakeSite) Series(ctx context.Context, id int) (site.Series, error) {
	f.calls = append(f.calls, "Series")
	return f.series, f.err
}

func (f *fakeSite) Episodes(ctx context.Context, seriesID int) ([]site.Episode, error) {
	f.calls = append(f.calls, "Episodes")
	return f.episodes, f.err
}

// harness wires a Run call onto fakes and captured output.
type harness struct {
	runner *fakeRunner
	site   *fakeSite
	env    map[string]string
	out    strings.Builder
	errOut strings.Builder
	deps   Deps
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessIn(t, t.TempDir())
}

// newHarnessIn runs the command line from dir. Every test gets its own working
// directory and its own platform directories, so neither a real ./.sau.toml nor
// a real user config can reach the test.
func newHarnessIn(t *testing.T, dir string) *harness {
	t.Helper()
	t.Chdir(dir)

	cfgDir := t.TempDir()
	old := dirs
	dirs = func() (string, string, error) { return cfgDir, cfgDir, nil }
	t.Cleanup(func() { dirs = old })

	h := &harness{runner: &fakeRunner{}, site: &fakeSite{}, env: map[string]string{}}
	h.deps = Deps{
		Runner: func(cfg config.Config, log *slog.Logger) (publishRunner, error) {
			return h.runner, nil
		},
		Site: func(cfg config.Config, log *slog.Logger) (siteAPI, error) {
			return h.site, nil
		},
		Env: func(k string) (string, bool) {
			v, ok := h.env[k]
			return v, ok
		},
		Stdin:  strings.NewReader(""),
		Stdout: &h.out,
		Stderr: &h.errOut,
	}
	return h
}

func (h *harness) run(args ...string) int {
	return Run(context.Background(), args, h.deps)
}

// video creates a file that Draft.Validate will accept as a video path.
func video(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "episode.mp4")
	if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
