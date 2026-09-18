package cli

import (
	"fmt"
	"sync"

	"sau/internal/fineup"
	"sau/internal/progress"
)

// batchReporter is the reporter of one episode when several run at once. It
// owns a line of the shared progress.Multi and prefixes its text with the
// file name, so that the messages of parallel uploads can be told apart.
// Without a terminal there is no Multi and the text lines are all there is.
//
// Questions are never asked here: publish refuses them first when parallel
// is above one, and this reporter only exists then.
type batchReporter struct {
	d     Deps
	multi *progress.Multi // nil when stderr is not a terminal
	name  string

	mu   sync.Mutex
	line *progress.Line
}

// say prints a text line above the progress block, or plainly without one.
func (r *batchReporter) say(msg string) {
	if r.multi != nil {
		r.multi.Print(msg)
		return
	}
	fmt.Fprintln(r.d.Stderr, msg)
}

// Begin opens a line for the file. The second Begin of an item is the
// subtitle file: the video's line is finished first.
func (r *batchReporter) Begin(name string, total int64, parts int) {
	r.say(fmt.Sprintf("%s: %d bytes in %d chunks", name, total, parts))
	if r.multi == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.line != nil {
		r.multi.Finish(r.line)
	}
	r.line = r.multi.Add(name, total)
}

func (r *batchReporter) Info(msg string) { r.say(r.name + ": " + msg) }

func (r *batchReporter) Warn(msg string) { r.say("sau: " + r.name + ": " + msg) }

func (r *batchReporter) Event(e fineup.Event) {
	switch e.Kind {
	case fineup.EventChunkDone:
		r.mu.Lock()
		line := r.line
		r.mu.Unlock()
		if line != nil {
			line.Add(e.Bytes)
		}
	case fineup.EventHostFailed:
		r.say(fmt.Sprintf("sau: %s: host %s failed, trying another one", r.name, e.Host))
	case fineup.EventFinalizing:
		r.say(fmt.Sprintf("%s: finalizing on %s", r.name, e.Host))
	}
}

func (r *batchReporter) Ask(question string) (bool, error) {
	return false, fmt.Errorf("cli: cannot ask %q while several episodes are running", firstLine(question))
}

// Done is called by RunBatch once the item's run has returned: the last
// line is finished and its slot freed.
func (r *batchReporter) Done() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.line != nil && r.multi != nil {
		r.multi.Finish(r.line)
		r.line = nil
	}
}
