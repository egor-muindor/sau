package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"sau/internal/fineup"
	"sau/internal/history"
	"sau/internal/secrets"
	"sau/internal/site"
	"sau/internal/statefile"
	"sau/internal/translation"
)

// fakeSite records every call in order, so that tests can assert the exact
// sequence of steps, and answers from the configured hooks. The hooks take the
// call number because the site answers differently on a repeated page load:
// the CSRF token changes on every GET of the form.
//
// The counters and the call log are under a mutex, because a batch drives the
// same site from several runs at once. The hooks run outside the lock: a hook
// that sleeps to widen a race window must not serialize the fake itself.
type fakeSite struct {
	mu    sync.Mutex
	calls []string

	login  func(call int) error
	form   func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error)
	submit func(call int) (site.SubmitResult, error)

	logins  int
	forms   int
	submits int

	found   []site.Translation
	findErr error
	// apiCalls counts FindPublished only. The success and unknown-outcome
	// branches must leave it at zero.
	apiCalls int

	lastForm  site.CreateForm
	lastDraft translation.Draft
	lastCh    translation.Channel
	lastUp    site.UploadedFields
}

func (s *fakeSite) Login(ctx context.Context, user string, pass secrets.Secret) error {
	s.mu.Lock()
	s.calls = append(s.calls, "Login")
	s.logins++
	n := s.logins
	s.mu.Unlock()
	if s.login != nil {
		return s.login(n)
	}
	return nil
}

func (s *fakeSite) CreateForm(ctx context.Context, seriesID int, ch translation.Channel) (site.CreateForm, error) {
	s.mu.Lock()
	s.calls = append(s.calls, fmt.Sprintf("CreateForm(%d,%s)", seriesID, ch))
	s.forms++
	n := s.forms
	s.mu.Unlock()
	if s.form != nil {
		return s.form(n, seriesID, ch)
	}
	return defaultForm(n), nil
}

func (s *fakeSite) Submit(ctx context.Context, f site.CreateForm, d translation.Draft, ch translation.Channel, up site.UploadedFields) (site.SubmitResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, "Submit")
	s.submits++
	n := s.submits
	s.lastForm, s.lastDraft, s.lastCh, s.lastUp = f, d, ch, up
	s.mu.Unlock()
	if s.submit != nil {
		return s.submit(n)
	}
	return site.SubmitResult{TranslationID: 4242, Location: "/translations/update/4242"}, nil
}

func (s *fakeSite) FindPublished(ctx context.Context, seriesID int, episode string, t translation.TranslationType, authors string) ([]site.Translation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "FindPublished")
	s.apiCalls++
	return s.found, s.findErr
}

// defaultForm mimics a fresh page load: a new CSRF token every time, the same
// upload server, two hosts in the video pool.
func defaultForm(call int) site.CreateForm {
	return site.CreateForm{
		CSRF: fmt.Sprintf("csrf-%d", call),
		Video: site.UploaderConfig{
			ServerID:   77,
			ServerURL:  "https://s77.example",
			ServerURLs: []string{"https://s77a.example", "https://s77b.example"},
			AllowedExt: []string{"mp4", "mkv"},
		},
		Sub: site.UploaderConfig{
			ServerID:   77,
			ServerURL:  "https://s77.example",
			ServerURLs: []string{"https://s77a.example"},
			AllowedExt: []string{"ass", "srt"},
		},
	}
}

// fakeUploader emits one EventChunkDone per not-yet-done part, so that tests
// can observe the state being written after every chunk, and records every
// Spec it received.
//
// The events go out from two goroutines on purpose: the real uploader calls the
// hook from every chunk goroutine and from the keep-alive one, so a hook that is
// not safe for concurrent use would be a bug that only shows up under -race.
type fakeUploader struct {
	mu      sync.Mutex
	specs   []fineup.Spec
	calls   int
	parts   int            // total number of parts in the file
	extra   []fineup.Event // emitted after the chunk events
	respond func(call int, s fineup.Spec) (fineup.Result, error)
	deletes []string
	delErr  error
}

func (u *fakeUploader) Upload(ctx context.Context, s fineup.Spec, on func(fineup.Event)) (fineup.Result, error) {
	u.mu.Lock()
	u.calls++
	call := u.calls
	u.specs = append(u.specs, s)
	parts, extra, respond := u.parts, u.extra, u.respond
	u.mu.Unlock()

	done := map[int]bool{}
	for _, i := range s.Done {
		done[i] = true
	}
	for i := 0; i < parts; i++ {
		if done[i] {
			continue
		}
		emitConcurrently(on, fineup.Event{Kind: fineup.EventChunkDone, Part: i, Host: hostFor(s, i), Bytes: s.PartSize})
	}
	for _, e := range extra {
		emitConcurrently(on, e)
	}
	if respond != nil {
		return respond(call, s)
	}
	return fineup.Result{
		UUID:     s.UUID,
		Name:     filepath.Base(s.Path),
		Size:     1024,
		Parts:    parts,
		Endpoint: s.Base,
		LastHost: hostFor(s, 0),
	}, nil
}

// emitConcurrently delivers one event from a separate goroutine and waits for
// it, keeping the order of events deterministic while still making every call
// cross a goroutine boundary.
func emitConcurrently(on func(fineup.Event), e fineup.Event) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		on(e)
	}()
	wg.Wait()
}

func (u *fakeUploader) Delete(ctx context.Context, endpoint, uuid string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.deletes = append(u.deletes, endpoint+" "+uuid)
	return u.delErr
}

func hostFor(s fineup.Spec, i int) string {
	if len(s.Pool) == 0 {
		return s.Base
	}
	return s.Pool[i%len(s.Pool)]
}

// lastSpec returns the Spec of the last Upload call.
func (u *fakeUploader) lastSpec(t *testing.T) fineup.Spec {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.specs) == 0 {
		t.Fatal("no Upload calls recorded")
	}
	return u.specs[len(u.specs)-1]
}

// memStore is a StateStore backed by a map of marshalled JSON, so that tests
// exercise the same round-trip as the real statefile.Store. It is guarded by a
// mutex because the state is saved from the event hook.
type memStore struct {
	mu sync.Mutex
	m  map[string][]byte
	// saveErr is returned by Save once saveOK successful saves have happened,
	// so that a test can break the store part way through a run.
	saveErr   error
	saveOK    int
	saves     int
	deleteErr error
}

func newMemStore() *memStore { return &memStore{m: map[string][]byte{}} }

func (s *memStore) Load(key string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[key]
	if !ok {
		return statefile.ErrNotFound
	}
	return json.Unmarshal(b, v)
}

func (s *memStore) Save(key string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	if s.saveErr != nil && s.saves > s.saveOK {
		return s.saveErr
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.m[key] = b
	return nil
}

func (s *memStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.m, key)
	return nil
}

// raw returns the bytes stored under key, so that a test can inspect what
// actually reaches the disk rather than the struct it came from.
func (s *memStore) raw(key string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.m[key]...)
}

func (s *memStore) Keys() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.m))
	for k := range s.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// beginCall is one recorded Begin, with the position it took in the overall
// call order, so that tests can assert Begin came before the first chunk event.
type beginCall struct {
	Name  string
	Total int64
	Parts int
	At    int // number of Event calls seen before this Begin
}

type fakeReporter struct {
	mu        sync.Mutex
	begins    []beginCall
	infos     []string
	warns     []string
	events    []fineup.Event
	questions []string
	answer    bool
	askErr    error
}

func (r *fakeReporter) Begin(name string, total int64, parts int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.begins = append(r.begins, beginCall{Name: name, Total: total, Parts: parts, At: len(r.events)})
}

func (r *fakeReporter) Info(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.infos = append(r.infos, msg)
}

func (r *fakeReporter) Warn(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warns = append(r.warns, msg)
}

func (r *fakeReporter) Event(e fineup.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *fakeReporter) Ask(question string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.questions = append(r.questions, question)
	if r.askErr != nil {
		return false, r.askErr
	}
	return r.answer, nil
}

// fakeSource is a secrets.Source stub.
type fakeSource struct {
	pass  string
	ok    bool
	err   error
	calls int
}

func (s *fakeSource) Password(ctx context.Context) (secrets.Secret, bool, error) {
	s.calls++
	return secrets.New(s.pass), s.ok, s.err
}

// harness wires a Runner onto the fakes and a real temporary video file.
type harness struct {
	t        *testing.T
	site     *fakeSite
	up       *fakeUploader
	store    *memStore
	rep      *fakeReporter
	pass     *fakeSource
	run      *Runner
	dir      string
	video    string
	histPath string
	now      time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	video := filepath.Join(dir, "episode 01 (1080p).mp4")
	if err := os.WriteFile(video, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &harness{
		t:        t,
		site:     &fakeSite{},
		up:       &fakeUploader{parts: 2},
		store:    newMemStore(),
		rep:      &fakeReporter{answer: true},
		pass:     &fakeSource{pass: "hunter2", ok: true},
		dir:      dir,
		video:    video,
		histPath: filepath.Join(dir, "history.jsonl"),
		now:      time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
	}
	h.run = &Runner{
		Site:     h.site,
		Uploader: h.up,
		State:    h.store,
		History:  history.Log{Path: h.histPath},
		Report:   h.rep,
		Now:      func() time.Time { return h.now },
		Stat:     os.Stat,
	}
	return h
}

// writeSub creates a subtitle file next to the video and returns its path.
func (h *harness) writeSub() string {
	h.t.Helper()
	p := filepath.Join(h.dir, "episode 01.ass")
	if err := os.WriteFile(p, []byte("[Script Info]"), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return p
}

// writeVideo creates another video file in the harness directory and returns
// its path. Batch tests need several files with distinct state keys.
func (h *harness) writeVideo(name string) string {
	h.t.Helper()
	p := filepath.Join(h.dir, name)
	if err := os.WriteFile(p, make([]byte, 1024), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return p
}

// requestFor is request() for another video file and episode number.
func (h *harness) requestFor(video, episode string) Request {
	req := h.request()
	req.Draft.VideoPath = video
	req.Draft.EpisodeNumber = episode
	return req
}

func (h *harness) request() Request {
	return Request{
		Draft: translation.Draft{
			SeriesID:      36866,
			EpisodeNumber: "1",
			EpisodeType:   translation.TV,
			Type:          translation.VoiceRu,
			Authors:       "Team (Alice, Bob)",
			VideoPath:     h.video,
		},
		Channel:     translation.ChannelCDN,
		User:        "user",
		Password:    h.pass,
		Concurrency: 5,
	}
}

func (h *harness) key() string {
	h.t.Helper()
	k, err := statefile.Key(h.video)
	if err != nil {
		h.t.Fatal(err)
	}
	return k
}

// putStateFor stores st under the key of video, filling size and mtime from
// disk so that decideResume sees an unchanged file.
func (h *harness) putStateFor(video string, st *UploadState) {
	h.t.Helper()
	fi, err := os.Stat(video)
	if err != nil {
		h.t.Fatal(err)
	}
	st.Path = video
	if st.Size == 0 {
		st.Size = fi.Size()
	}
	if st.ModTime.IsZero() {
		st.ModTime = fi.ModTime()
	}
	k, err := statefile.Key(video)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.Save(k, st); err != nil {
		h.t.Fatal(err)
	}
}

// putState stores st under the harness video's key.
func (h *harness) putState(st *UploadState) {
	h.t.Helper()
	h.putStateFor(h.video, st)
}

// state loads the stored state; it fails the test if there is none.
func (h *harness) state() *UploadState {
	h.t.Helper()
	var st UploadState
	if err := h.store.Load(h.key(), &st); err != nil {
		h.t.Fatalf("load state: %v", err)
	}
	return &st
}

func (h *harness) records() []history.Record {
	h.t.Helper()
	rs, err := history.Log{Path: h.histPath}.ReadAll()
	if err != nil {
		h.t.Fatal(err)
	}
	return rs
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// reporterFunc is a Reporter whose Event hook is supplied by the test.
type reporterFunc func(e fineup.Event)

func (f reporterFunc) Begin(name string, total int64, parts int) {}
func (f reporterFunc) Info(msg string)                           {}
func (f reporterFunc) Warn(msg string)                           {}
func (f reporterFunc) Event(e fineup.Event)                      { f(e) }
func (f reporterFunc) Ask(q string) (bool, error) {
	return false, fmt.Errorf("no console")
}

// SubmitResultAlias keeps the submit hooks readable in tests.
type SubmitResultAlias = site.SubmitResult

// asErr is errors.As with a friendlier name for the tests.
func asErr(err error, target any) bool { return errors.As(err, target) }

func sortStrings(s []string) { sort.Strings(s) }

// orderReporter records Begin and chunk events in one sequence, so that a test
// can assert which came first per file rather than only per run.
type orderReporter struct {
	mu  sync.Mutex
	log []string
}

func (r *orderReporter) Begin(name string, total int64, parts int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, "begin:"+name)
}

func (r *orderReporter) Info(msg string) {}
func (r *orderReporter) Warn(msg string) {}

func (r *orderReporter) Event(e fineup.Event) {
	if e.Kind != fineup.EventChunkDone {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, fmt.Sprintf("chunk:%d", e.Part))
}

func (r *orderReporter) Ask(q string) (bool, error) { return false, fmt.Errorf("no console") }

func (r *orderReporter) entries() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.log...)
}
