package cli

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"sau/internal/fineup"
	"sau/internal/progress"
	"sau/internal/publish"
)

// consoleReporter prints what the runner is doing and drives the progress bar.
//
// Everything it writes goes to stderr, including the bar and the question in
// Ask. Stdout carries the result of the command and nothing else, so that
// --json can be piped straight into a parser.
//
// The bar is built in Begin, because that is where the total size arrives: a
// fineup event carries the size of its own chunk only. Each file gets its own
// bar and its own redraw goroutine, so the percentage is per file rather than
// per run.
type consoleReporter struct {
	d Deps

	// in is read once per reporter rather than once per question: a fresh
	// bufio.Reader would buffer past the newline and swallow the next answer.
	inOnce sync.Once
	in     *bufio.Reader

	mu   sync.Mutex
	bar  *progress.Bar
	stop context.CancelFunc
	done chan struct{}

	retries atomic.Int64
}

// activeReporter is the reporter built for this run, so that Run can take the
// bar down before anything else writes to the terminal. Tests that substitute
// Deps.Runner never set it.
var activeReporter *consoleReporter

func newConsoleReporter(d Deps) publish.Reporter {
	r := &consoleReporter{d: d}
	activeReporter = r
	return r
}

// consoleFor returns the reporter the runner of this run will use, so that a
// question asked by the command and a question asked by the runner read the
// same stdin buffer: a second bufio.Reader would swallow whatever the first one
// read ahead. Without a runner-built reporter (tests substitute Deps.Runner)
// a fresh one is made.
func consoleFor(d Deps) *consoleReporter {
	if r := activeReporter; r != nil {
		return r
	}
	return &consoleReporter{d: d}
}

// stopProgress takes down the bar of the file that was uploading last.
//
// Begin stops the bar of the previous file, so every bar but the last one is
// accounted for. The last one has to be stopped here: otherwise its redraw
// goroutine outlives the command and overwrites the final message.
func stopProgress() {
	r := activeReporter
	if r == nil {
		return
	}
	activeReporter = nil
	r.stopBar()
}

// stopBar cancels the redraw goroutine and waits for it. Waiting matters: Run
// draws the bar one last time on its way out, and that render must not land in
// the middle of the error report.
func (r *consoleReporter) stopBar() {
	r.mu.Lock()
	stop, done := r.stop, r.done
	r.stop, r.done, r.bar = nil, nil, nil
	r.mu.Unlock()

	if stop == nil {
		return
	}
	stop()
	<-done
	// The bar leaves the cursor on its own line, which it owns with a carriage
	// return only. Close that line before anything else is printed.
	fmt.Fprintln(r.d.Stderr)
}

// Begin starts a fresh bar for the file that is about to be uploaded and stops
// the bar of the previous one. Redrawing runs on its own goroutine so that the
// display never slows the upload down.
func (r *consoleReporter) Begin(name string, total int64, parts int) {
	r.stopBar()
	fmt.Fprintf(r.d.Stderr, "%s: %d bytes in %d chunks\n", name, total, parts)

	bar := &progress.Bar{W: r.d.Stderr, Total: total, Interval: progress.DefaultInterval}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	r.mu.Lock()
	r.bar, r.stop, r.done = bar, cancel, done
	r.mu.Unlock()

	go func() {
		defer close(done)
		bar.Run(ctx)
	}()
}

func (r *consoleReporter) Info(msg string) { fmt.Fprintln(r.d.Stderr, msg) }

func (r *consoleReporter) Warn(msg string) { fmt.Fprintln(r.d.Stderr, "sau: "+msg) }

func (r *consoleReporter) Event(e fineup.Event) {
	switch e.Kind {
	case fineup.EventChunkDone:
		r.mu.Lock()
		bar := r.bar
		r.mu.Unlock()
		if bar != nil {
			bar.Add(e.Bytes)
		}
	case fineup.EventChunkFailed:
		r.retries.Add(1)
	case fineup.EventHostFailed:
		fmt.Fprintf(r.d.Stderr, "\nsau: host %s failed, trying another one\n", e.Host)
	case fineup.EventFinalizing:
		fmt.Fprintf(r.d.Stderr, "\nfinalizing on %s\n", e.Host)
	}
}

// Ask asks a yes or no question. With no console there is no answer, and the
// caller must treat that as a refusal rather than as a yes: guessing "yes"
// after an unknown outcome would hide a lost publication, guessing "no" would
// risk a duplicate.
func (r *consoleReporter) Ask(question string) (bool, error) {
	if r.d.Stdin == nil {
		return false, fmt.Errorf("cli: cannot ask %q: no console", question)
	}
	fmt.Fprintf(r.d.Stderr, "%s [y/N] ", question)
	r.inOnce.Do(func() { r.in = bufio.NewReader(r.d.Stdin) })
	line, err := r.in.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("cli: cannot read the answer: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
