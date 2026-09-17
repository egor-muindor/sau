package fineup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// New returns an Uploader using c. A nil client is a programming error and
// panics: the uploader must never fall back to http.DefaultClient, because
// that would send chunk traffic around the redacting transport the caller
// wires in (see internal/httplog).
func New(c *http.Client) *Uploader {
	if c == nil {
		panic("fineup: New called with a nil *http.Client")
	}
	return &Uploader{Client: c}
}

// newUUID returns a random RFC 4122 version 4 UUID in the canonical
// 8-4-4-4-12 form.
//
// It returns no error and panics instead: a failure of crypto/rand means the
// system has no entropy source, which is fatal rather than recoverable, and an
// error return here would spread a branch nobody can act on through every
// caller. The same signature is used in site and publish, on purpose.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("fineup: crypto/rand failed: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// client returns the configured client or ErrNoClient; there is deliberately
// no fallback to http.DefaultClient (see New).
func (u *Uploader) client() (*http.Client, error) {
	if u.Client == nil {
		return nil, ErrNoClient
	}
	return u.Client, nil
}

func (u *Uploader) attempts() int {
	if u.MaxAttempts > 0 {
		return u.MaxAttempts
	}
	return DefaultMaxAttempts
}

func (u *Uploader) backoff(attempt int) time.Duration {
	if u.Backoff != nil {
		return u.Backoff(attempt)
	}
	return defaultBackoff(attempt)
}

func (u *Uploader) touchInterval() time.Duration {
	if u.TouchInterval > 0 {
		return u.TouchInterval
	}
	return DefaultTouchInterval
}

func (u *Uploader) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

// Upload sends the file described by s in chunks and finalizes it. The file is
// opened once and read positionally, which is safe from several goroutines.
// Finalization goes to the host of the last successful chunk, never to Base.
//
// on is called from every chunk goroutine and from the keep-alive goroutine, so
// it must be safe for concurrent use. It may be nil. It is called inline, so a
// slow callback slows the upload down.
func (u *Uploader) Upload(ctx context.Context, s Spec, on func(Event)) (Result, error) {
	if _, err := u.client(); err != nil {
		return Result{}, err
	}
	emit := func(e Event) {
		if on != nil {
			on(e)
		}
	}

	f, err := os.Open(s.Path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Result{}, err
	}
	size := fi.Size()
	name := filepath.Base(s.Path)

	if s.PartSize <= 0 {
		s.PartSize = DefaultPartSize
	}
	parts, err := PlanParts(size, s.PartSize)
	if err != nil {
		return Result{}, err
	}
	// publish normally fills Spec.UUID itself and saves it before the first
	// chunk; an empty one means a caller that does not keep state.
	if s.UUID == "" {
		s.UUID = newUUID()
	}

	pool := make([]string, 0, len(s.Pool))
	for _, h := range s.Pool {
		pool = append(pool, Endpoint(h))
	}
	if len(pool) == 0 {
		pool = []string{s.Base}
	}
	p := newPicker(pool, u.Rand)
	single := len(pool) == 1

	skip := make(map[int]bool, len(s.Done))
	for _, i := range s.Done {
		skip[i] = true
	}
	resume := len(s.Done) > 0
	// A saved state that claims every chunk is already there leaves nothing to
	// send, and then there is no host to finalize on. The pool cannot supply
	// one: a server id does not say which hosts hold the chunks, the delivery
	// channel does, and it changes without the id changing. So the last chunk
	// is sent again and its host answers the question.
	if len(skip) >= len(parts) {
		delete(skip, parts[len(parts)-1].Index)
	}

	// Keep-alive: the server discards partial uploads without it. The goroutine
	// is guaranteed to be finished before Upload returns.
	tctx, cancelTouch := context.WithCancel(ctx)
	var tw sync.WaitGroup
	tw.Add(1)
	go func() {
		defer tw.Done()
		u.touchLoop(tctx, s.Base, s.UUID, emit)
	}()
	defer func() {
		cancelTouch()
		tw.Wait()
	}()

	g, gctx := errgroup.WithContext(ctx)
	limit := s.MaxConns
	if limit <= 0 {
		limit = DefaultMaxConns
	}
	g.SetLimit(limit)

	var mu sync.Mutex
	lastHost := ""
	doneIdx := append([]int(nil), s.Done...)

	// commit records a chunk the server has accepted. It runs inside uploadPart
	// just before EventChunkDone is emitted, so that a caller reading the event
	// stream and the returned LastHost never sees the two disagree.
	commit := func(index int, host string) {
		mu.Lock()
		defer mu.Unlock()
		lastHost = host
		doneIdx = append(doneIdx, index)
	}

	for _, part := range parts {
		if skip[part.Index] {
			continue
		}
		part := part
		g.Go(func() error {
			return u.uploadPart(gctx, s, name, size, len(parts), part, resume, f, p, single, emit, commit)
		})
	}
	if err := g.Wait(); err != nil {
		var pe *PoolExhaustedError
		if errors.As(err, &pe) {
			mu.Lock()
			d := append([]int(nil), doneIdx...)
			mu.Unlock()
			sort.Ints(d)
			pe.Done = d
		}
		return Result{}, err
	}

	emit(Event{Kind: EventFinalizing, Host: lastHost})
	if err := u.finalize(ctx, lastHost, s.UUID, name, size, len(parts)); err != nil {
		return Result{}, &FinalizeError{Host: lastHost, Err: err}
	}

	return Result{
		UUID:     s.UUID,
		Name:     name,
		Size:     size,
		Parts:    len(parts),
		Endpoint: s.Base,
		LastHost: lastHost,
	}, nil
}

// uploadPart sends one chunk, retrying up to MaxAttempts times.
//
// Every failure is retried, but only some of them are the host's fault. A
// transport error, a 5xx, a 408 or a 429 mean the host is sick: it is marked
// failed and the next attempt goes to another one. Any other 4xx means the
// server understood the request and refused this chunk; retrying on the same
// pool is right, and dropping a healthy host over it would drain the pool
// (docs/architecture.md §7).
//
// With a pool of a single host there is nowhere else to go, so the retry is
// spaced by Backoff instead (docs/architecture.md §9).
func (u *Uploader) uploadPart(ctx context.Context, s Spec, name string, size int64,
	total int, part Part, resume bool, f *os.File, p *picker, single bool,
	emit func(Event), commit func(index int, host string)) error {

	var last error
	for attempt := 1; attempt <= u.attempts(); attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		host := p.pick(part.Index)
		emit(Event{Kind: EventChunkStart, Part: part.Index, Host: host,
			Bytes: part.Size, Attempt: attempt})

		body := io.NewSectionReader(f, part.Offset, part.Size)
		req, err := chunkRequest(ctx, host, s, name, size, total, part, resume, body)
		if err != nil {
			return err
		}
		started := u.now()
		resp, err := u.mustClient().Do(req)
		if err == nil {
			_, err = parseResponse(resp)
		}
		if err == nil {
			p.record(host, part.Size, u.now().Sub(started))
			commit(part.Index, host)
			emit(Event{Kind: EventChunkDone, Part: part.Index, Host: host,
				Bytes: part.Size, Attempt: attempt})
			return nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		last = err
		emit(Event{Kind: EventChunkFailed, Part: part.Index, Host: host,
			Bytes: part.Size, Attempt: attempt, Err: err})

		// parseResponse hides the status inside a *ServerError; a transport
		// error carries none, and isRetryable treats that as the host's fault.
		status := 0
		var se *ServerError
		if errors.As(err, &se) {
			status = se.Status
			err = nil
		}
		if !single {
			// Another host is available. If this one is to blame, mark it and
			// move over at once, without a pause.
			if isRetryable(err, status) {
				p.fail(host)
				emit(Event{Kind: EventHostFailed, Part: part.Index, Host: host,
					Attempt: attempt, Err: last})
			}
			continue
		}
		// One host, the RU channel: nobody to blame and nowhere to move, so
		// wait instead and try the same host again. The pause is pointless
		// after the last attempt, so it is skipped there.
		if attempt < u.attempts() {
			if err := sleepCtx(ctx, u.backoff(attempt)); err != nil {
				return err
			}
		}
	}
	return &PoolExhaustedError{Part: part.Index, Last: last}
}

// sleepCtx waits for d, or returns early when ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (u *Uploader) touchLoop(ctx context.Context, base, uuid string, emit func(Event)) {
	t := time.NewTicker(u.touchInterval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			req, err := touchRequest(ctx, base, uuid)
			if err != nil {
				return
			}
			resp, err := u.mustClient().Do(req)
			if err == nil {
				_, err = parseResponse(resp)
			}
			// A failed ping is reported and then forgotten. It is not fatal on
			// its own: the next tick may well succeed, and the upload must not
			// be torn down over a keep-alive that the server ignored once.
			emit(Event{Kind: EventTouch, Host: base, Err: err})
		}
	}
}

func (u *Uploader) finalize(ctx context.Context, host, uuid, name string,
	size int64, total int) error {
	req, err := doneRequest(ctx, host, uuid, name, size, total)
	if err != nil {
		return err
	}
	resp, err := u.mustClient().Do(req)
	if err != nil {
		return err
	}
	_, err = parseResponse(resp)
	return err
}

// Delete removes an upload. Pass Result.LastHost, the host of the last
// successful chunk and the one finalization went to. Result.Endpoint also works
// when the pool is a set of mirrors of one server, since mirrors share the
// storage; LastHost is the safe choice because nothing guarantees they are.
//
// The endpoint is taken as given, already normalized by Endpoint: that function
// appends "/upload.php" unconditionally and is not idempotent, so normalizing
// again here would produce ".../upload.php/upload.php".
func (u *Uploader) Delete(ctx context.Context, endpoint, uuid string) error {
	if _, err := u.client(); err != nil {
		return err
	}
	req, err := deleteRequest(ctx, endpoint, uuid)
	if err != nil {
		return err
	}
	resp, err := u.mustClient().Do(req)
	if err != nil {
		return err
	}
	_, err = parseResponse(resp)
	return err
}

// mustClient is for the request paths, which run only after Upload or Delete
// has already validated the client.
func (u *Uploader) mustClient() *http.Client {
	c, err := u.client()
	if err != nil {
		panic(err)
	}
	return c
}
