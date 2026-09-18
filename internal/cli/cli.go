// Package cli parses the command line and turns it into calls on the publish
// runner and the site client.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"sau/internal/config"
	"sau/internal/publish"
	"sau/internal/secrets"
	"sau/internal/site"
	"sau/internal/translation"
)

// Exit codes, as documented in docs/architecture.md section 6.
const (
	ExitOK          = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitAuth        = 3
	ExitRejected    = 4
	ExitUpload      = 5
	ExitUnknown     = 6
	ExitInterrupted = 130
)

// publishRunner is the part of publish.Runner the commands need. Tests
// substitute it.
type publishRunner interface {
	Run(ctx context.Context, req publish.Request) (publish.Outcome, error)
	RunBatch(ctx context.Context, items []publish.BatchItem, parallel int) []publish.BatchResult
	CheckSession(ctx context.Context, req publish.Request) error
	Abort(ctx context.Context, path string) error
	Resolve(ctx context.Context, path string, submitted bool) error
	Status() ([]publish.UploadState, error)
}

// siteAPI is the part of site.Client the read-only commands need.
type siteAPI interface {
	Login(ctx context.Context, user string, pass secrets.Secret) error
	CreateForm(ctx context.Context, seriesID int, ch translation.Channel) (site.CreateForm, error)
	Series(ctx context.Context, id int) (site.Series, error)
	Episodes(ctx context.Context, seriesID int) ([]site.Episode, error)
}

// Deps holds everything the command line does not own itself.
type Deps struct {
	Runner func(cfg config.Config, log *slog.Logger) (publishRunner, error)
	Site   func(cfg config.Config, log *slog.Logger) (siteAPI, error)
	Env    func(string) (string, bool)
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// dirs is a variable so that tests can point the tool at a temporary directory.
var dirs = config.Dirs

const usageText = `usage: sau <command> [flags]

  login    [--user U] [--save-password] [--check]
  upload   <video>... [--sub f.ass] [--episode N] [--series ID]
           [--episode-type tv] [--type voiceRu] [--authors "..."] [--by-author]
           [--channel cdn|all|ru] [--parallel 1] [--yes] [--dry-run]
           [--no-submit] [--fresh] [--concurrency 5] [--json] [-v] [--debug]
           several files, or one without --episode, make a batch: the numbers
           come from [[episodes]] or episode_pattern in .sau.toml
  status                       unfinished uploads
  abort    <video>             delete the uploaded file and drop its state
  resolve  <video> --submitted | --resend
  series   <id>                title information from the read-only API
  stats    [--series ID] [--since DATE] [--json]
  version                      print the version

exit codes: 0 ok, 1 error, 2 usage, 3 authorization, 4 rejected by the site,
5 upload failed (state kept), 6 submission outcome unknown, 130 interrupted
`

// usageError is anything the user can fix by retyping the command.
// An empty message means the flag package already printed the details.
type usageError struct{ msg string }

func (e *usageError) Error() string {
	if e.msg == "" {
		return "invalid arguments"
	}
	return e.msg
}

func usagef(format string, a ...any) error {
	return &usageError{msg: fmt.Sprintf(format, a...)}
}

// parseErr converts a flag package error into something Run understands.
func parseErr(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return err
	}
	return &usageError{}
}

// Run executes one command line and returns the process exit code.
func Run(ctx context.Context, args []string, d Deps) int {
	d = withDefaults(d)
	defer persistSessions(d)

	err := dispatch(ctx, args, d)

	// The bar of the file that uploaded last has its own redraw goroutine, and
	// only the next Begin would stop it. Take it down here, before the result
	// is printed, so that a redraw cannot land in the middle of the message.
	stopProgress()

	if err == nil {
		return ExitOK
	}
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	reportError(d.Stderr, err)
	return exitCode(err)
}

func withDefaults(d Deps) Deps {
	if d.Stdin == nil {
		d.Stdin = os.Stdin
	}
	if d.Stdout == nil {
		d.Stdout = os.Stdout
	}
	if d.Stderr == nil {
		d.Stderr = os.Stderr
	}
	if d.Env == nil {
		d.Env = os.LookupEnv
	}
	// The builders need the filled-in Deps: the console reporter writes to
	// these streams and the session writer reports failures on Stderr.
	if d.Runner == nil {
		deps := d
		d.Runner = func(cfg config.Config, log *slog.Logger) (publishRunner, error) {
			return buildRunner(deps, cfg, log)
		}
	}
	if d.Site == nil {
		deps := d
		d.Site = func(cfg config.Config, log *slog.Logger) (siteAPI, error) {
			return buildSite(deps, cfg, log)
		}
	}
	return d
}

func dispatch(ctx context.Context, args []string, d Deps) error {
	if len(args) == 0 {
		return usagef("no command given")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(d.Stdout, usageText)
		return nil
	case "version", "--version", "-V":
		return cmdVersion(d)
	case "login":
		return cmdLogin(ctx, rest, d)
	case "upload":
		return cmdUpload(ctx, rest, d)
	case "status":
		return cmdStatus(ctx, rest, d)
	case "abort":
		return cmdAbort(ctx, rest, d)
	case "resolve":
		return cmdResolve(ctx, rest, d)
	case "series":
		return cmdSeries(ctx, rest, d)
	case "stats":
		return cmdStats(ctx, rest, d)
	default:
		return usagef("unknown command %q", cmd)
	}
}

// loadConfig resolves the configuration and fills the paths that are derived
// from the platform state directory.
func loadConfig(layer config.Layer, d Deps) (config.Config, error) {
	configDir, stateDir, err := dirs()
	if err != nil {
		return config.Config{}, err
	}
	cfg, err := config.Load(layer, d.Env, ".sau.toml", filepath.Join(configDir, "config.toml"))
	if err != nil {
		return config.Config{}, err
	}
	if cfg.StateDir == "" {
		cfg.StateDir = filepath.Join(stateDir, "uploads")
	}
	if cfg.SessionDir == "" {
		cfg.SessionDir = filepath.Join(stateDir, "sessions")
	}
	if cfg.HistoryPath == "" {
		cfg.HistoryPath = filepath.Join(stateDir, "history.jsonl")
	}
	if cfg.LogPath == "" {
		cfg.LogPath = filepath.Join(stateDir, "debug.log")
	}
	return cfg, nil
}

func newFlagSet(name string, d Deps) *flag.FlagSet {
	fs := flag.NewFlagSet("sau "+name, flag.ContinueOnError)
	fs.SetOutput(d.Stderr)
	return fs
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
