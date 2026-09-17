package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"sau/internal/config"
	"sau/internal/fsx"
)

// newLogger builds the structured logger. Everything that reaches it has
// already passed the redactor in internal/httplog; nothing here bypasses it.
//
// With --debug the full trace goes to a file next to the journal, so that it
// can be attached to a bug report without repeating the upload.
func newLogger(d Deps, cfg config.Config, verbose, debug bool) *slog.Logger {
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelInfo
	}
	var w io.Writer = d.Stderr
	if debug {
		level = slog.LevelDebug
		f, err := openLogFile(cfg.LogPath)
		if err != nil {
			// --debug was asked for and cannot be delivered to the file. Say so
			// rather than quietly writing the trace somewhere else: the point
			// of the flag is to have a file to attach to a bug report.
			fmt.Fprintf(d.Stderr, "sau: could not open the debug log %s: %v\n", cfg.LogPath, err)
			fmt.Fprintln(d.Stderr, "sau: writing the trace to stderr instead")
		} else {
			w = f
		}
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

func openLogFile(path string) (*os.File, error) {
	if path == "" {
		return nil, os.ErrInvalid
	}
	if err := fsx.MkdirPrivate(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}
