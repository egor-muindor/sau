package fineup

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"sau/internal/fineup/fakeup"
)

// singleHostSpec writes a file and builds a spec whose pool holds exactly one
// host, the way the RU channel serves it.
func singleHostSpec(t *testing.T, urls []string, size int) (Spec, []byte) {
	t.Helper()

	if len(urls) != 1 {
		t.Fatalf("pool has %d hosts, want 1", len(urls))
	}
	data := bytes.Repeat([]byte("ru"), size/2)
	path := filepath.Join(t.TempDir(), "episode.mp4")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}, data
}

func TestUploadSingleHostRetriesOnTheSameHost(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 1)
	spec, data := singleHostSpec(t, urls, 3000)

	u := New(&http.Client{})
	u.TouchInterval = time.Hour                      // keep the ping out of this test
	u.Backoff = func(int) time.Duration { return 0 } // no real waiting in a test

	// Two failures in a row on the only host. The chunk must still get through,
	// because the retry budget is four attempts.
	s.FailHost(s.Hosts()[0], 2)

	var mu sync.Mutex
	var failed, hostFailed int
	res, err := u.Upload(context.Background(), spec, func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		switch e.Kind {
		case EventChunkFailed:
			failed++
		case EventHostFailed:
			hostFailed++
		}
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.Parts != 3 {
		t.Errorf("Parts = %d, want 3", res.Parts)
	}
	got, ok := s.Assembled(res.UUID)
	if !ok {
		t.Fatal("the file was not finalized")
	}
	if !bytes.Equal(got, data) {
		t.Errorf("uploaded %d bytes, want %d", len(got), len(data))
	}

	mu.Lock()
	defer mu.Unlock()
	if failed != 2 {
		t.Errorf("EventChunkFailed count = %d, want 2", failed)
	}
	// The only host is never declared dead: there is nowhere to move to.
	if hostFailed != 0 {
		t.Errorf("EventHostFailed count = %d, want 0 for a single-host pool", hostFailed)
	}
}

func TestUploadSingleHostExhaustsTheRetryBudget(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 1)
	spec, _ := singleHostSpec(t, urls, 3000)

	u := New(&http.Client{})
	u.TouchInterval = time.Hour
	u.Backoff = func(int) time.Duration { return 0 }
	u.MaxAttempts = 4

	// More failures than the budget allows.
	s.FailHost(s.Hosts()[0], 10)

	_, err := u.Upload(context.Background(), spec, nil)
	var pe *PoolExhaustedError
	if !errors.As(err, &pe) {
		t.Fatalf("Upload error = %v, want *PoolExhaustedError", err)
	}
	if pe.Last == nil {
		t.Error("PoolExhaustedError.Last is nil, want the last server error")
	}
	var se *ServerError
	if !errors.As(pe.Last, &se) {
		t.Errorf("PoolExhaustedError.Last = %T, want *ServerError", pe.Last)
	}
	if _, ok := s.Assembled(""); ok {
		t.Error("nothing must be finalized after the budget is spent")
	}
}

func TestUploadSingleHostWaitsBetweenAttempts(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 1)
	spec, _ := singleHostSpec(t, urls, 1000)

	u := New(&http.Client{})
	u.TouchInterval = time.Hour

	var mu sync.Mutex
	var waits []int
	u.Backoff = func(attempt int) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		waits = append(waits, attempt)
		return 0
	}

	s.FailHost(s.Hosts()[0], 2)
	if _, err := u.Upload(context.Background(), spec, nil); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// With one host there is nowhere else to go, so the retry has to wait.
	if len(waits) != 2 {
		t.Fatalf("Backoff calls = %v, want two", waits)
	}
	if waits[0] != 1 || waits[1] != 2 {
		t.Errorf("Backoff attempts = %v, want 1 then 2", waits)
	}
}

func TestUploadMultiHostDoesNotWait(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 3)

	data := bytes.Repeat([]byte("x"), 1000)
	path := filepath.Join(t.TempDir(), "episode.mp4")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	u := New(&http.Client{})
	u.TouchInterval = time.Hour

	var mu sync.Mutex
	called := 0
	u.Backoff = func(int) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		called++
		return 0
	}

	s.FailHost(s.Hosts()[0], 1)
	spec := Spec{
		Path:     path,
		Base:     Endpoint(urls[0]),
		Pool:     urls,
		PartSize: 1000,
		MaxConns: 1,
	}
	if _, err := u.Upload(context.Background(), spec, nil); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// With a healthy host still available the retry goes straight to it.
	if called != 0 {
		t.Errorf("Backoff was called %d times, want 0 when other hosts remain", called)
	}
}
