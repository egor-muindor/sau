package publish

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sau/internal/fineup"
	"sau/internal/site"
	"sau/internal/translation"
)

// batchOf builds n items on fresh video files, episodes 1..n, in that order.
func batchOf(h *harness, n int) []BatchItem {
	h.t.Helper()
	items := make([]BatchItem, 0, n)
	for i := 1; i <= n; i++ {
		video := h.video
		if i > 1 {
			video = h.writeVideo("episode 0" + string(rune('0'+i)) + " (1080p).mp4")
		}
		items = append(items, BatchItem{Request: h.requestFor(video, string(rune('0'+i)))})
	}
	return items
}

// maxConcurrent records the highest number of callers inside the guarded
// section at any one time.
type maxConcurrent struct{ inFlight, max atomic.Int32 }

// gateTimeout bounds every wait in the rendezvous helpers, so that a bug in
// the batch fails the test instead of hanging it.
const gateTimeout = 5 * time.Second

// rendezvous holds every caller until want of them are inside, then releases
// them all together and rearms for the next group. It makes "n uploads at
// once" a fact rather than a bet on timing.
type rendezvous struct {
	t    *testing.T
	want int

	mu sync.Mutex
	n  int
	ch chan struct{}
}

func newRendezvous(t *testing.T, want int) *rendezvous {
	return &rendezvous{t: t, want: want, ch: make(chan struct{})}
}

func (r *rendezvous) wait() {
	r.mu.Lock()
	r.n++
	ch := r.ch
	if r.n == r.want {
		close(ch)
		r.n = 0
		r.ch = make(chan struct{})
	}
	r.mu.Unlock()
	select {
	case <-ch:
	case <-time.After(gateTimeout):
		r.t.Errorf("rendezvous: fewer than %d callers arrived", r.want)
	}
}

// counter closes done once n calls have been counted.
type counter struct {
	n    atomic.Int32
	want int32
	done chan struct{}
	once sync.Once
}

func newCounter(want int32) *counter {
	return &counter{want: want, done: make(chan struct{})}
}

func (c *counter) add() {
	if c.n.Add(1) == c.want {
		c.once.Do(func() { close(c.done) })
	}
}

func (m *maxConcurrent) enter() func() {
	n := m.inFlight.Add(1)
	for {
		cur := m.max.Load()
		if n <= cur || m.max.CompareAndSwap(cur, n) {
			break
		}
	}
	return func() { m.inFlight.Add(-1) }
}

func TestRunBatchKeepsInputOrderAndLimitsWorkers(t *testing.T) {
	h := newHarness(t)
	var uploads maxConcurrent
	// Every upload waits for a partner: with two workers and four items the
	// uploads run in pairs, and the fake proves both of a pair were inside.
	pairs := newRendezvous(t, 2)
	h.up.respond = func(call int, s fineup.Spec) (fineup.Result, error) {
		defer uploads.enter()()
		pairs.wait()
		return fineup.Result{UUID: s.UUID, Name: filepath.Base(s.Path), Size: 1024, Parts: 2, Endpoint: s.Base}, nil
	}
	items := batchOf(h, 4)

	results := h.run.RunBatch(context.Background(), items, 2)

	eq(t, "results", len(results), 4)
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("results[%d].Err = %v", i, r.Err)
		}
		eq(t, "results order", r.Item.Request.Draft.EpisodeNumber, items[i].Request.Draft.EpisodeNumber)
		if r.Outcome == nil || r.Outcome.TranslationID != 4242 {
			t.Errorf("results[%d].Outcome = %+v", i, r.Outcome)
		}
	}
	eq(t, "uploads in flight at once", uploads.max.Load(), int32(2))
	eq(t, "submits", h.site.submits, 4)
	eq(t, "history records", len(h.records()), 4)
}

func TestRunBatchSequentialRunsInOrder(t *testing.T) {
	h := newHarness(t)
	var order []string
	h.up.respond = func(call int, s fineup.Spec) (fineup.Result, error) {
		order = append(order, filepath.Base(s.Path))
		return fineup.Result{UUID: s.UUID, Name: filepath.Base(s.Path), Size: 1024, Parts: 2, Endpoint: s.Base}, nil
	}
	items := batchOf(h, 3)
	results := h.run.RunBatch(context.Background(), items, 1)
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("results[%d].Err = %v", i, r.Err)
		}
	}
	eq(t, "upload order", strings.Join(order, ","),
		"episode 01 (1080p).mp4,episode 02 (1080p).mp4,episode 03 (1080p).mp4")
}

func TestRunBatchContinuesAfterAFailure(t *testing.T) {
	h := newHarness(t)
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		if call == 2 {
			return SubmitResultAlias{}, &site.RejectedError{Messages: []string{"Authors are required."}}
		}
		return SubmitResultAlias{TranslationID: 4242, Location: "/translations/update/4242"}, nil
	}
	results := h.run.RunBatch(context.Background(), batchOf(h, 3), 1)

	var re *site.RejectedError
	if !errors.As(results[1].Err, &re) {
		t.Fatalf("results[1].Err = %v, want *site.RejectedError", results[1].Err)
	}
	if results[0].Err != nil || results[2].Err != nil {
		t.Errorf("neighbours failed: %v, %v", results[0].Err, results[2].Err)
	}
	eq(t, "submits", h.site.submits, 3)
}

func TestRunBatchStopsOnAuthorizationFailure(t *testing.T) {
	h := newHarness(t)
	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		return site.CreateForm{}, site.ErrNotAuthorized
	}
	results := h.run.RunBatch(context.Background(), batchOf(h, 3), 1)

	if !errors.Is(results[0].Err, ErrAuth) {
		t.Fatalf("results[0].Err = %v, want ErrAuth", results[0].Err)
	}
	for _, i := range []int{1, 2} {
		if !errors.Is(results[i].Err, ErrCancelled) {
			t.Errorf("results[%d].Err = %v, want ErrCancelled", i, results[i].Err)
		}
	}
	// One re-login for the whole batch, not one per episode: the form is
	// fetched, fetched again under the login lock, and once more after the
	// login, all by the first episode.
	eq(t, "logins", h.site.logins, 1)
	eq(t, "forms", h.site.forms, 3)
	eq(t, "uploads", h.up.calls, 0)
}

// TestRunBatchReloginHappensOnce pins down what happens when the session dies
// while several runs are in flight: every run hits the login page at once,
// and only one of them may log in. The others must reuse that login, because
// two logins interleaved on one cookie jar (the login page fetched by one,
// the form posted by another with a stale token) fail spuriously.
func TestRunBatchReloginHappensOnce(t *testing.T) {
	h := newHarness(t)
	h.site.kill()
	// The first three form fetches are the first step of the three runs; they
	// are held until all three are inside, so that all three see the dead
	// session before any of them can log in.
	together := newRendezvous(t, 3)
	// Rendezvous alone is not enough: the run that leaves it last may repeat
	// its fetch and log in before the other two get to their repeat, and
	// then they see a live session even without the lock. So the login is
	// held open until another run has repeated its fetch (call 4 is the
	// repeat of the run that logs in; call 5 is the first repeat of another
	// run). With the lock that repeat cannot happen until the login is over,
	// so the wait merely expires; without the lock it is released at once
	// and the other run, having read dead before the login finished, logs
	// in a second time.
	otherRetry := make(chan struct{})
	var otherOnce sync.Once
	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		if call <= 3 {
			together.wait()
		}
		// dead is read before the release, so that the look cannot land
		// after the login it releases.
		h.site.mu.Lock()
		dead := h.site.dead
		h.site.mu.Unlock()
		if call >= 5 {
			otherOnce.Do(func() { close(otherRetry) })
		}
		if dead {
			return site.CreateForm{}, site.ErrNotAuthorized
		}
		return defaultForm(call), nil
	}
	h.site.login = func(n int) error {
		select {
		case <-otherRetry:
		case <-time.After(time.Second):
		}
		return nil
	}

	results := h.run.RunBatch(context.Background(), batchOf(h, 3), 3)
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("results[%d].Err = %v", i, r.Err)
		}
		if r.Outcome == nil || r.Outcome.TranslationID != 4242 {
			t.Errorf("results[%d].Outcome = %+v", i, r.Outcome)
		}
	}
	eq(t, "logins", h.site.logins, 1)
	eq(t, "submits", h.site.submits, 3)
}

func TestRunBatchUserCancellationIsNotMarkedCancelled(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := h.run.RunBatch(ctx, batchOf(h, 2), 2)
	for i, r := range results {
		if !errors.Is(r.Err, context.Canceled) {
			t.Errorf("results[%d].Err = %v, want context.Canceled", i, r.Err)
		}
		if errors.Is(r.Err, ErrCancelled) {
			t.Errorf("results[%d].Err = %v: a Ctrl-C is not an authorization stop", i, r.Err)
		}
	}
}

func TestRunBatchSubmitsNeverOverlap(t *testing.T) {
	h := newHarness(t)
	var submits maxConcurrent
	// The first submit is held open until all three uploads have finished,
	// so the other two runs are provably past their uploads and at the lock.
	// Then it waits a while longer for a second submit to arrive: with the
	// lock in place none can, and the wait merely expires; with the lock
	// broken the arrival is recorded and the assertion below fails.
	uploaded := newCounter(3)
	second := make(chan struct{})
	var secondOnce sync.Once
	h.up.respond = func(call int, s fineup.Spec) (fineup.Result, error) {
		uploaded.add()
		return fineup.Result{UUID: s.UUID, Name: filepath.Base(s.Path), Size: 1024, Parts: 2, Endpoint: s.Base}, nil
	}
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		defer submits.enter()()
		if call > 1 {
			secondOnce.Do(func() { close(second) })
		} else {
			select {
			case <-uploaded.done:
			case <-time.After(gateTimeout):
				t.Error("not all uploads finished while the first submit was held")
			}
			select {
			case <-second:
			case <-time.After(100 * time.Millisecond):
			}
		}
		return SubmitResultAlias{TranslationID: 4242, Location: "/translations/update/4242"}, nil
	}
	results := h.run.RunBatch(context.Background(), batchOf(h, 3), 3)
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("results[%d].Err = %v", i, r.Err)
		}
	}
	eq(t, "submits", h.site.submits, 3)
	eq(t, "submits in flight at once", submits.max.Load(), int32(1))
}

func TestRunBatchRefusesQuestionsWhenParallel(t *testing.T) {
	h := newHarness(t)
	items := batchOf(h, 2)
	h.putStateFor(items[0].Request.Draft.VideoPath, &UploadState{
		Phase:    PhaseSubmitUnknown,
		ServerID: 77,
		Channel:  translation.ChannelCDN,
		Snapshot: &Snapshot{SeriesID: 36866, EpisodeNumber: "1"},
	})

	results := h.run.RunBatch(context.Background(), items, 2)

	var uo *UnknownOutcomeError
	if !errors.As(results[0].Err, &uo) {
		t.Fatalf("results[0].Err = %v, want *UnknownOutcomeError", results[0].Err)
	}
	if !strings.Contains(results[0].Question, "already published") {
		t.Errorf("results[0].Question = %q, want the unknown-outcome question", results[0].Question)
	}
	// The human was never asked: with several episodes running there is no
	// console to ask on.
	eq(t, "questions asked", len(h.rep.questions), 0)
	if results[1].Err != nil {
		t.Errorf("results[1].Err = %v", results[1].Err)
	}
	eq(t, "results[1].Question", results[1].Question, "")
}

func TestRunBatchAsksWhenSequential(t *testing.T) {
	h := newHarness(t)
	h.rep.answer = true
	items := batchOf(h, 2)
	h.putStateFor(items[0].Request.Draft.VideoPath, &UploadState{
		Phase:    PhaseSubmitUnknown,
		ServerID: 77,
		Channel:  translation.ChannelCDN,
		Snapshot: &Snapshot{SeriesID: 36866, EpisodeNumber: "1"},
	})

	results := h.run.RunBatch(context.Background(), items, 1)

	eq(t, "questions asked", len(h.rep.questions), 1)
	if results[0].Err != nil {
		t.Errorf("results[0].Err = %v", results[0].Err)
	}
	eq(t, "results[0].Question", results[0].Question, "")
	// Confirmed as published: closed from the snapshot, not submitted again.
	eq(t, "submits", h.site.submits, 1)
}

// doneReporter is a fakeReporter that also records the Done call RunBatch
// makes once the item's run has returned.
type doneReporter struct {
	fakeReporter
	done atomic.Int32
}

func (r *doneReporter) Done() { r.done.Add(1) }

func TestRunBatchUsesTheItemReporter(t *testing.T) {
	h := newHarness(t)
	items := batchOf(h, 2)
	own := &doneReporter{}
	items[1].Report = own

	results := h.run.RunBatch(context.Background(), items, 2)
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("results[%d].Err = %v", i, r.Err)
		}
	}
	eq(t, "own reporter begins", len(own.begins), 1)
	eq(t, "own reporter begin name", own.begins[0].Name, "episode 02 (1080p).mp4")
	eq(t, "own reporter done", own.done.Load(), int32(1))
	// The runner's reporter saw only the item without a reporter of its own.
	eq(t, "runner reporter begins", len(h.rep.begins), 1)
	eq(t, "runner reporter begin name", h.rep.begins[0].Name, "episode 01 (1080p).mp4")
}

func TestRunBatchEmpty(t *testing.T) {
	h := newHarness(t)
	results := h.run.RunBatch(context.Background(), nil, 2)
	eq(t, "results", len(results), 0)
	eq(t, "forms", h.site.forms, 0)
}

func TestCheckSessionLogsInOnce(t *testing.T) {
	h := newHarness(t)
	h.site.kill()
	if err := h.run.CheckSession(context.Background(), h.request()); err != nil {
		t.Fatalf("CheckSession: %v", err)
	}
	eq(t, "calls", strings.Join(h.site.calls, ","),
		"CreateForm(36866,cdn),CreateForm(36866,cdn),Login,CreateForm(36866,cdn)")
	eq(t, "uploads", h.up.calls, 0)
}

func TestCheckSessionReportsErrAuth(t *testing.T) {
	h := newHarness(t)
	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		return site.CreateForm{}, site.ErrNotAuthorized
	}
	if err := h.run.CheckSession(context.Background(), h.request()); !errors.Is(err, ErrAuth) {
		t.Fatalf("CheckSession: %v, want ErrAuth", err)
	}
}

func TestPhaseNeedsDecision(t *testing.T) {
	for _, p := range []Phase{PhaseSubmitting, PhaseSubmitUnknown} {
		if !p.NeedsDecision() {
			t.Errorf("%s.NeedsDecision() = false", p)
		}
	}
	for _, p := range []Phase{PhaseUploading, PhaseUploaded} {
		if p.NeedsDecision() {
			t.Errorf("%s.NeedsDecision() = true", p)
		}
	}
}
