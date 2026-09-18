package publish

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"sau/internal/fineup"
)

// BatchItem is one episode of a batch.
type BatchItem struct {
	// Request is the draft with VideoPath, SubPath and EpisodeNumber set.
	Request Request
	// Report, when non-nil, receives the progress of this item instead of
	// Runner.Report. A batch with parallel > 1 needs one reporter per item:
	// a Reporter does not say which file an Event belongs to. If the value
	// also has a Done() method, RunBatch calls it once the item's run has
	// returned, so that a multi-line display can close the item's line.
	Report Reporter
}

// BatchResult is the outcome of one item. Outcome is always set: Run returns
// a value even on failure, and then it carries the state the item was left
// in (see UploadFailedError). Question is the question the run
// wanted to ask when questions were refused (parallel > 1): the item then
// needs a human decision, and Err says which phase it was left in.
type BatchResult struct {
	Item     BatchItem
	Outcome  *Outcome
	Err      error
	Question string
}

// ErrCancelled marks the items that were not run, or were interrupted,
// because another item of the batch failed authorization. Their state is on
// disk exactly as an interrupted run leaves it.
var ErrCancelled = errors.New("publish: cancelled: another episode failed authorization")

// RunBatch runs the items with parallel workers, in the given order, and
// returns one result per item in the same order. An item's error does not
// stop the others. An authorization failure does: nothing about the next
// item would go differently, so the context is cancelled, runs in flight
// leave their state behind and the remaining items are marked ErrCancelled.
//
// Questions to the human are answered only when parallel is 1. With more
// workers the console belongs to nobody, so the run is told there is no
// console, and the question is recorded in the result instead.
func (r *Runner) RunBatch(ctx context.Context, items []BatchItem, parallel int) []BatchResult {
	results := make([]BatchResult, len(items))
	for i := range items {
		results[i].Item = items[i]
	}
	if len(items) == 0 {
		return results
	}
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(items) {
		parallel = len(items)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var authFailed atomic.Bool

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				res := &results[i]
				if ctx.Err() != nil {
					if authFailed.Load() {
						res.Err = ErrCancelled
					} else {
						res.Err = ctx.Err()
					}
					continue
				}
				item := items[i]
				rep := &batchReporter{inner: r.itemReporter(item), quiet: parallel > 1}
				out, err := r.forItem(rep).Run(ctx, item.Request)
				if d, ok := item.Report.(interface{ Done() }); ok {
					d.Done()
				}
				res.Outcome, res.Err, res.Question = &out, err, rep.asked()
				if errors.Is(err, ErrAuth) {
					authFailed.Store(true)
					cancel()
				}
				if isCancellation(err) && authFailed.Load() {
					res.Err = ErrCancelled
				}
			}
		}()
	}
	for i := range items {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

// CheckSession fetches one form page for the series of req and discards it.
// It exists for the start of a batch: the session is checked and, if needed,
// the password is asked for and the login happens once, before the parallel
// part, where a prompt would collide with the progress display. The form is
// discarded on purpose: upload servers are never cached.
func (r *Runner) CheckSession(ctx context.Context, req Request) error {
	_, err := r.createForm(ctx, req)
	return err
}

// itemReporter picks the reporter for an item: its own, or the runner's.
func (r *Runner) itemReporter(item BatchItem) Reporter {
	if item.Report != nil {
		return item.Report
	}
	return r.Report
}

// forItem returns a runner for one item of a batch: the same collaborators
// and the same submit lock, a reporter of its own.
func (r *Runner) forItem(rep Reporter) *Runner {
	return &Runner{
		Site:     r.Site,
		Uploader: r.Uploader,
		State:    r.State,
		History:  r.History,
		Report:   rep,
		Now:      r.Now,
		Stat:     r.Stat,
		submitMu: r.submitLock(),
	}
}

// batchReporter is the reporter a batch run sees. It forwards everything to
// the item's reporter, except that with quiet set it refuses questions: the
// error makes askUnknown leave the phase alone, exactly as with no console,
// and the question is kept for the summary.
type batchReporter struct {
	inner Reporter
	quiet bool

	mu       sync.Mutex
	question string
}

func (b *batchReporter) Begin(name string, total int64, parts int) {
	if b.inner != nil {
		b.inner.Begin(name, total, parts)
	}
}

func (b *batchReporter) Info(msg string) {
	if b.inner != nil {
		b.inner.Info(msg)
	}
}

func (b *batchReporter) Warn(msg string) {
	if b.inner != nil {
		b.inner.Warn(msg)
	}
}

func (b *batchReporter) Event(e fineup.Event) {
	if b.inner != nil {
		b.inner.Event(e)
	}
}

func (b *batchReporter) Ask(question string) (bool, error) {
	if !b.quiet {
		if b.inner == nil {
			return false, errors.New("publish: no reporter to ask")
		}
		return b.inner.Ask(question)
	}
	b.mu.Lock()
	b.question = question
	b.mu.Unlock()
	return false, errors.New("publish: cannot ask while several episodes are running at once")
}

// asked returns the question that was refused, or "".
func (b *batchReporter) asked() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.question
}
