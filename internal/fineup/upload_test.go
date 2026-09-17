package fineup

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"sau/internal/fineup/fakeup"
)

// testFile writes a deterministic file of the given size and returns its path
// and contents.
func testFile(t *testing.T, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	p := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	return p, data
}

// collector gathers events from Upload. Events arrive from several goroutines,
// so access is guarded.
type collector struct {
	mu     sync.Mutex
	events []Event
}

func (c *collector) on(e Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *collector) all() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.events...)
}

func (c *collector) count(k EventKind) int {
	n := 0
	for _, e := range c.all() {
		if e.Kind == k {
			n++
		}
	}
	return n
}

func (c *collector) last(k EventKind) (Event, bool) {
	var found Event
	ok := false
	for _, e := range c.all() {
		if e.Kind == k {
			found, ok = e, true
		}
	}
	return found, ok
}

func TestUploadSingleConnection(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 1)

	path, data := testFile(t, 2500)
	u := New(&http.Client{})
	var c collector

	res, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}, c.on)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if res.Parts != 3 {
		t.Fatalf("Result.Parts = %d, want 3", res.Parts)
	}
	if res.Size != int64(len(data)) {
		t.Fatalf("Result.Size = %d, want %d", res.Size, len(data))
	}
	if res.Name != "video.mp4" {
		t.Fatalf("Result.Name = %q, want video.mp4", res.Name)
	}
	if res.Endpoint != Endpoint(urls[0]) {
		t.Fatalf("Result.Endpoint = %q, want %q", res.Endpoint, Endpoint(urls[0]))
	}
	if res.UUID == "" {
		t.Fatalf("Result.UUID is empty")
	}
	if len(res.UUID) != 36 || res.UUID[14] != '4' {
		t.Fatalf("Result.UUID = %q, want a v4 uuid", res.UUID)
	}

	got, ok := s.Assembled(res.UUID)
	if !ok {
		t.Fatalf("file was not finalized")
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("assembled file differs: %d bytes vs %d", len(got), len(data))
	}
	if rf := s.ResumeFlags(res.UUID); rf[0] || rf[1] || rf[2] {
		t.Fatalf("qqresume must be absent on a fresh upload: %v", rf)
	}
}

func TestUploadConcurrentFinalizesOnLastChunkHost(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 4)

	path, data := testFile(t, 11500) // 11 full parts of 1000 + 500
	u := New(&http.Client{})
	var c collector

	res, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 5,
	}, c.on)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.Parts != 12 {
		t.Fatalf("Result.Parts = %d, want 12", res.Parts)
	}
	if s.PartCount(res.UUID) != 12 {
		t.Fatalf("server stored %d parts, want 12", s.PartCount(res.UUID))
	}
	got, ok := s.Assembled(res.UUID)
	if !ok || !bytes.Equal(got, data) {
		t.Fatalf("assembled file differs (ok=%v, %d bytes)", ok, len(got))
	}

	// Chunks must have been spread over more than one mirror.
	used := map[string]bool{}
	for _, h := range s.PartHosts(res.UUID) {
		used[h] = true
	}
	if len(used) < 2 {
		t.Fatalf("all chunks went to one host: %v", used)
	}

	// Finalization goes to the host of the chunk that finished last. That host
	// is read from Result.LastHost rather than from the last chunk event: two
	// goroutines finishing together commit in one order and emit in another, so
	// the last event is not reliably the last commit.
	if _, ok := c.last(EventChunkDone); !ok {
		t.Fatalf("no EventChunkDone")
	}
	wantHost := strings.TrimPrefix(strings.TrimSuffix(res.LastHost, "/upload.php"), "http://")
	if s.DoneHost(res.UUID) != wantHost {
		t.Fatalf("DoneHost = %q, want %q (host of last successful chunk)",
			s.DoneHost(res.UUID), wantHost)
	}
	if fin, ok := c.last(EventFinalizing); !ok || fin.Host != res.LastHost {
		t.Fatalf("EventFinalizing host = %q (present=%v), want %q", fin.Host, ok, res.LastHost)
	}
}

func TestUploadFirstPartsGoToPoolInOrder(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 4)
	hosts := s.Hosts()

	path, _ := testFile(t, 11500)
	u := New(&http.Client{})

	res, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 5,
	}, nil)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	ph := s.PartHosts(res.UUID)
	for i := 0; i < len(hosts); i++ {
		if ph[i] != hosts[i] {
			t.Fatalf("part %d went to %q, want %q (index rule, protocol.md)", i, ph[i], hosts[i])
		}
	}
	for i := len(hosts); i < 12; i++ {
		if ph[i] == "" {
			t.Fatalf("part %d was not uploaded", i)
		}
	}
}

func TestUploadFailoverToAnotherHost(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 3)
	hosts := s.Hosts()

	path, data := testFile(t, 2500)
	u := New(&http.Client{})
	u.Backoff = func(int) time.Duration { return 0 }
	var c collector

	// The host that the index rule picks for part 0 rejects it once.
	s.FailHost(hosts[0], 1)

	res, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}, c.on)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	got, ok := s.Assembled(res.UUID)
	if !ok || !bytes.Equal(got, data) {
		t.Fatalf("assembled file differs (ok=%v)", ok)
	}
	if h := s.PartHosts(res.UUID)[0]; h == hosts[0] {
		t.Fatalf("part 0 stayed on the failed host %q", h)
	}
	ev, ok := c.last(EventHostFailed)
	if !ok {
		t.Fatalf("no EventHostFailed emitted")
	}
	if !strings.Contains(ev.Host, hosts[0]) {
		t.Fatalf("EventHostFailed.Host = %q, want it to mention %q", ev.Host, hosts[0])
	}
	if c.count(EventChunkFailed) != 1 {
		t.Fatalf("EventChunkFailed count = %d, want 1", c.count(EventChunkFailed))
	}
	if c.count(EventChunkDone) != 3 {
		t.Fatalf("EventChunkDone count = %d, want 3", c.count(EventChunkDone))
	}
}

func TestUploadPoolExhausted(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 2)

	path, _ := testFile(t, 2500)
	u := New(&http.Client{})
	u.Backoff = func(int) time.Duration { return 0 }
	var c collector

	// The last part is rejected on every host, whatever the attempt.
	s.FailPart(2, 100)

	// MaxConns 1 makes the order deterministic: parts 0 and 1 succeed first.
	_, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}, c.on)
	if err == nil {
		t.Fatalf("Upload succeeded, want *PoolExhaustedError")
	}
	var pe *PoolExhaustedError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %T (%v), want *PoolExhaustedError", err, err)
	}
	if pe.Part != 2 {
		t.Fatalf("PoolExhaustedError.Part = %d, want 2", pe.Part)
	}
	if !reflect.DeepEqual(pe.Done, []int{0, 1}) {
		t.Fatalf("PoolExhaustedError.Done = %v, want [0 1]", pe.Done)
	}
	if pe.Last == nil {
		t.Fatalf("PoolExhaustedError.Last is nil, want the last server error")
	}
	var se *ServerError
	if !errors.As(pe.Last, &se) {
		t.Fatalf("Last = %T, want *ServerError", pe.Last)
	}
	if c.count(EventChunkFailed) != DefaultMaxAttempts {
		t.Fatalf("EventChunkFailed = %d, want %d", c.count(EventChunkFailed), DefaultMaxAttempts)
	}
	if _, ok := s.Assembled(""); ok {
		t.Fatalf("nothing must be finalized")
	}
	if c.count(EventFinalizing) != 0 {
		t.Fatalf("EventFinalizing must not be emitted on failure")
	}
}

func TestUploadCancelReturnsFastAndLeaksNothing(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 2)
	// goleak must ignore the httptest server goroutines started above.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	for _, h := range s.Hosts() {
		s.Delay(h, 300*time.Millisecond)
	}

	path, _ := testFile(t, 20000)
	u := New(&http.Client{})
	u.TouchInterval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := u.Upload(ctx, Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 5,
	}, nil)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Upload took %v after cancel, want prompt return", elapsed)
	}
	if _, ok := s.Assembled(""); ok {
		t.Fatalf("nothing must be finalized after cancel")
	}
}

func TestUploadResumeReusesUUIDAndSetsResumeFlag(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 2)

	path, data := testFile(t, 2500)
	u := New(&http.Client{})

	// First run: everything is uploaded, the uuid is remembered.
	first, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}, nil)
	if err != nil {
		t.Fatalf("first Upload: %v", err)
	}

	// Second run resumes it: part 0 is known to be on the server already.
	second, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		UUID:     first.UUID,
		PartSize: 1000,
		Done:     []int{0},
		MaxConns: 1,
	}, nil)
	if err != nil {
		t.Fatalf("resumed Upload: %v", err)
	}
	if second.UUID != first.UUID {
		t.Fatalf("UUID = %q, want the saved one %q", second.UUID, first.UUID)
	}
	if second.Parts != 3 {
		t.Fatalf("Result.Parts = %d, want 3 (Done does not shrink the plan)", second.Parts)
	}

	rf := s.ResumeFlags(first.UUID)
	if rf[0] {
		t.Fatalf("part 0 must not be re-sent, resume flags = %v", rf)
	}
	if !rf[1] || !rf[2] {
		t.Fatalf("qqresume must be set on every remaining chunk, got %v", rf)
	}
	got, ok := s.Assembled(first.UUID)
	if !ok || !bytes.Equal(got, data) {
		t.Fatalf("assembled file differs after resume (ok=%v)", ok)
	}
}

func TestUploadTouchesWhileRunningAndStopsAfter(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 1)
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	// Slow chunks so that the 10ms ticker fires several times.
	for _, h := range s.Hosts() {
		s.Delay(h, 30*time.Millisecond)
	}

	path, _ := testFile(t, 2500)
	// A transport of its own, closed before the leak check: the keep-alive
	// connections of a shared client outlive the upload by design and would be
	// reported as leaked goroutines that are not ours.
	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	u := New(&http.Client{Transport: tr})
	u.TouchInterval = 10 * time.Millisecond

	res, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}, nil)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	n := s.Touches(res.UUID)
	if n == 0 {
		t.Fatalf("no keep-alive pings were sent")
	}
	time.Sleep(60 * time.Millisecond)
	if after := s.Touches(res.UUID); after != n {
		t.Fatalf("pings continued after Upload returned: %d then %d", n, after)
	}
}

func TestUploadFinalizeError(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 2)

	path, _ := testFile(t, 2500)
	u := New(&http.Client{})
	s.FailDone(10)

	_, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}, nil)
	if err == nil {
		t.Fatalf("Upload succeeded, want *FinalizeError")
	}
	var fe *FinalizeError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %T (%v), want *FinalizeError", err, err)
	}
	if fe.Host == "" {
		t.Fatalf("FinalizeError.Host is empty, want the last chunk host")
	}
	if fe.Host == Endpoint(urls[0]) && len(urls) > 1 {
		// Not an error by itself, but the host must come from a chunk, not Base.
		t.Logf("finalize host equals Base; make sure it came from a chunk")
	}
	var se *ServerError
	if !errors.As(fe.Err, &se) {
		t.Fatalf("FinalizeError.Err = %T, want *ServerError", fe.Err)
	}
}

func TestDeleteRemovesUpload(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 1)

	path, _ := testFile(t, 2500)
	u := New(&http.Client{})

	res, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}, nil)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if err := u.Delete(context.Background(), Endpoint(urls[0]), res.UUID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !s.Deleted(res.UUID) {
		t.Fatalf("server did not see the delete for %s", res.UUID)
	}

	// A rejected delete surfaces as *ServerError.
	s.FailHost(s.Hosts()[0], 1)
	err = u.Delete(context.Background(), Endpoint(urls[0]), res.UUID)
	var se *ServerError
	if !errors.As(err, &se) {
		t.Fatalf("Delete error = %T (%v), want *ServerError", err, err)
	}
}

func TestUploadProgressEvents(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 2)

	const size = 2500
	path, _ := testFile(t, size)
	u := New(&http.Client{})
	var c collector

	res, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 2,
	}, c.on)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if got := c.count(EventChunkStart); got != res.Parts {
		t.Fatalf("EventChunkStart = %d, want %d", got, res.Parts)
	}
	if got := c.count(EventChunkDone); got != res.Parts {
		t.Fatalf("EventChunkDone = %d, want %d", got, res.Parts)
	}

	var total int64
	seen := map[int]bool{}
	for _, e := range c.all() {
		if e.Kind != EventChunkDone {
			continue
		}
		if seen[e.Part] {
			t.Fatalf("EventChunkDone repeated for part %d", e.Part)
		}
		seen[e.Part] = true
		if e.Host == "" {
			t.Fatalf("EventChunkDone for part %d has no Host", e.Part)
		}
		if e.Attempt < 1 {
			t.Fatalf("EventChunkDone for part %d has Attempt %d", e.Part, e.Attempt)
		}
		total += e.Bytes
	}
	if total != size {
		t.Fatalf("sum of EventChunkDone Bytes = %d, want %d", total, size)
	}
	if c.count(EventFinalizing) != 1 {
		t.Fatalf("EventFinalizing = %d, want 1", c.count(EventFinalizing))
	}
}

func TestUploadWithNilCallback(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 1)

	path, data := testFile(t, 2500)
	u := New(&http.Client{})

	res, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}, nil)
	if err != nil {
		t.Fatalf("Upload with nil callback: %v", err)
	}
	if got, ok := s.Assembled(res.UUID); !ok || !bytes.Equal(got, data) {
		t.Fatalf("assembled file differs (ok=%v)", ok)
	}
}

func TestUploadRejectedChunkDoesNotBlameTheHost(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 3)

	path, data := testFile(t, 2500)
	u := New(&http.Client{})
	u.Backoff = func(int) time.Duration { return 0 }
	var c collector

	// A 400 means the server refused this chunk on its merits. The chunk is
	// still retried, but the host has done nothing wrong and must stay in the
	// rotation: marking it failed would drain the pool over a chunk problem.
	s.RejectPart(0, 2)

	res, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}, c.on)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got, ok := s.Assembled(res.UUID); !ok || !bytes.Equal(got, data) {
		t.Fatalf("assembled file differs (ok=%v)", ok)
	}
	if c.count(EventChunkFailed) != 2 {
		t.Fatalf("EventChunkFailed = %d, want 2", c.count(EventChunkFailed))
	}
	if n := c.count(EventHostFailed); n != 0 {
		t.Fatalf("EventHostFailed = %d, want 0: a 4xx is not the host's fault", n)
	}
	if c.count(EventChunkDone) != 3 {
		t.Fatalf("EventChunkDone = %d, want 3", c.count(EventChunkDone))
	}
}

func TestUploadServerErrorOnHostStillBlamesIt(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 3)

	path, _ := testFile(t, 2500)
	u := New(&http.Client{})
	u.Backoff = func(int) time.Duration { return 0 }
	var c collector

	// A 500 is the host's fault and must still mark it.
	s.FailPart(0, 1)

	if _, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}, c.on); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if c.count(EventHostFailed) != 1 {
		t.Fatalf("EventHostFailed = %d, want 1: a 5xx is the host's fault", c.count(EventHostFailed))
	}
}

func TestUploadEverythingDoneStillSendsOneChunk(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 2)

	path, data := testFile(t, 2500)
	u := New(&http.Client{})

	first, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}, nil)
	if err != nil {
		t.Fatalf("first Upload: %v", err)
	}

	// A resume whose saved state claims every chunk is already on the server.
	// The finalizing host cannot be guessed from the pool: a server id does not
	// say which hosts hold the chunks, the delivery channel does. So the last
	// chunk is sent again and the host that accepts it answers the question.
	res, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		UUID:     first.UUID,
		PartSize: 1000,
		Done:     []int{0, 1, 2},
		MaxConns: 1,
	}, nil)
	if err != nil {
		t.Fatalf("resumed Upload: %v", err)
	}

	// qqresume marks what this second run sent, and only the last chunk carries
	// it: the other two were left alone.
	rf := s.ResumeFlags(first.UUID)
	if rf[0] || rf[1] {
		t.Fatalf("chunks 0 and 1 were re-sent, resume flags = %v", rf)
	}
	if !rf[2] {
		t.Fatalf("the last chunk was not re-sent, resume flags = %v", rf)
	}
	sent := s.PartHosts(first.UUID)[2]
	if s.DoneHost(first.UUID) != sent {
		t.Fatalf("DoneHost = %q, want %q, the host of the chunk that was sent",
			s.DoneHost(first.UUID), sent)
	}
	if res.LastHost == "" || !strings.Contains(res.LastHost, sent) {
		t.Fatalf("Result.LastHost = %q, want it to mention %q", res.LastHost, sent)
	}
	if got, ok := s.Assembled(first.UUID); !ok || !bytes.Equal(got, data) {
		t.Fatalf("assembled file differs (ok=%v)", ok)
	}
}

func TestUploadResultLastHostIsTheFinalizingHost(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 4)

	path, _ := testFile(t, 11500)
	u := New(&http.Client{})

	res, err := u.Upload(context.Background(), Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 5,
	}, nil)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.LastHost == "" {
		t.Fatalf("Result.LastHost is empty")
	}
	host := strings.TrimPrefix(strings.TrimSuffix(res.LastHost, "/upload.php"), "http://")
	if s.DoneHost(res.UUID) != host {
		t.Fatalf("DoneHost = %q, want Result.LastHost %q", s.DoneHost(res.UUID), res.LastHost)
	}
	// Delete accepts that same endpoint.
	if err := u.Delete(context.Background(), res.LastHost, res.UUID); err != nil {
		t.Fatalf("Delete on Result.LastHost: %v", err)
	}
	if !s.Deleted(res.UUID) {
		t.Fatalf("server did not see the delete")
	}
}
