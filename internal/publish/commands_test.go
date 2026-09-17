package publish

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"sau/internal/fineup"
	"sau/internal/site"
	"sau/internal/translation"
)

func TestReloginOnce(t *testing.T) {
	h := newHarness(t)
	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		if call == 1 {
			return site.CreateForm{}, site.ErrNotAuthorized
		}
		return defaultForm(call), nil
	}

	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "calls", strings.Join(h.site.calls[:3], ","),
		"CreateForm(36866,cdn),Login,CreateForm(36866,cdn)")
	eq(t, "password lookups", h.pass.calls, 1)
	eq(t, "logins", h.site.logins, 1)
}

func TestReloginFailsWithErrAuth(t *testing.T) {
	h := newHarness(t)
	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		return site.CreateForm{}, site.ErrNotAuthorized
	}
	_, err := h.run.Run(context.Background(), h.request())
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("got %v, want ErrAuth", err)
	}
	eq(t, "logins", h.site.logins, 1)
}

func TestNoPasswordIsErrAuth(t *testing.T) {
	h := newHarness(t)
	h.pass.ok = false
	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		return site.CreateForm{}, site.ErrNotAuthorized
	}
	_, err := h.run.Run(context.Background(), h.request())
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("got %v, want ErrAuth", err)
	}
	eq(t, "logins", h.site.logins, 0)
}

func TestDryRun(t *testing.T) {
	h := newHarness(t)
	h.site.found = []site.Translation{{ID: 111, AuthorsSummary: "Team (Alice & Bob)"}}
	req := h.request()
	req.DryRun = true

	out, err := h.run.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "calls", strings.Join(h.site.calls, ","), "CreateForm(36866,cdn),FindPublished")
	eq(t, "upload calls", h.up.calls, 0)
	eq(t, "submits", h.site.submits, 0)
	// A false negative from the API is harmless here, so the duplicate check
	// lives in --dry-run and nowhere else on the publishing path.
	eq(t, "api calls", h.site.apiCalls, 1)
	if !strings.Contains(strings.Join(h.rep.warns, "|"), "duplicate") {
		t.Errorf("a possible duplicate must be reported: %v", h.rep.warns)
	}
	if out.State == nil {
		t.Fatal("Outcome.State must carry the plan")
	}
	eq(t, "planned parts", out.State.Video.TotalParts, 1)
	keys, _ := h.store.Keys()
	eq(t, "dry run writes no state", len(keys), 0)
}

func TestNoSubmit(t *testing.T) {
	h := newHarness(t)
	req := h.request()
	req.NoSubmit = true

	out, err := h.run.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "upload calls", h.up.calls, 1)
	eq(t, "submits", h.site.submits, 0)
	eq(t, "phase", h.state().Phase, PhaseUploaded)
	// The hidden field is built from the SECOND form, the one fetched after
	// the upload. Its batch id is random, so the two are compared with that
	// one member blanked out.
	want := site.EncodeUploadField(
		&site.UploadedFile{Name: "episode 01 (1080p).mp4", UUID: h.up.specs[0].UUID, Size: 1024},
		defaultForm(2).Video)
	eq(t, "VideoField", withoutBatchID(t, out.VideoField), withoutBatchID(t, want))
	eq(t, "SubField", out.SubField, "")
	eq(t, "history records", len(h.records()), 0)
}

// withoutBatchID re-renders the hidden field with the random batch id removed,
// so that two renderings of the same upload compare equal.
func withoutBatchID(t *testing.T, field string) string {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(field), &v); err != nil {
		t.Fatalf("hidden field is not JSON: %v\n%s", err, field)
	}
	files, _ := v["files"].([]any)
	for _, f := range files {
		if m, ok := f.(map[string]any); ok {
			delete(m, "batchId")
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAbort(t *testing.T) {
	h := newHarness(t)
	h.putState(&UploadState{
		Phase: PhaseUploaded, ServerID: 77, Channel: translation.ChannelCDN,
		Video: &FileUpload{UUID: "v-1", Result: &fineup.Result{UUID: "v-1",
			Endpoint: "https://s77.example/upload.php",
			LastHost: "https://s77a.example/upload.php"}},
		Sub: &FileUpload{UUID: "s-1", Result: &fineup.Result{UUID: "s-1",
			Endpoint: "https://s77.example/upload.php",
			LastHost: "https://s77b.example/upload.php"}},
	})

	if err := h.run.Abort(context.Background(), h.video); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	// The delete goes to the host that finalized the upload: that is where the
	// server keeps the parts, and the base endpoint may route elsewhere.
	eq(t, "deletes", strings.Join(h.up.deletes, ","),
		"https://s77a.example/upload.php v-1,https://s77b.example/upload.php s-1")
	keys, _ := h.store.Keys()
	eq(t, "state keys", len(keys), 0)
}

// Without a recorded finalization host the base endpoint is the only address
// there is, so it is used rather than skipping the deletion.
func TestAbortFallsBackToEndpoint(t *testing.T) {
	h := newHarness(t)
	h.putState(&UploadState{
		Phase: PhaseUploading, ServerID: 77, Channel: translation.ChannelCDN,
		Video: &FileUpload{UUID: "v-1", Result: &fineup.Result{UUID: "v-1",
			Endpoint: "https://s77.example/upload.php"}},
	})
	if err := h.run.Abort(context.Background(), h.video); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	eq(t, "deletes", strings.Join(h.up.deletes, ","), "https://s77.example/upload.php v-1")
}

func TestStatusListsStates(t *testing.T) {
	h := newHarness(t)
	h.putState(&UploadState{Phase: PhaseUploading, ServerID: 77,
		Channel: translation.ChannelCDN, Video: &FileUpload{UUID: "u", Done: []int{0}}})
	if err := h.store.Save("other", &UploadState{Path: "/tmp/other.mp4",
		Phase: PhaseSubmitUnknown, ServerID: 88}); err != nil {
		t.Fatal(err)
	}

	got, err := h.run.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	eq(t, "count", len(got), 2)
	phases := []string{}
	for _, st := range got {
		phases = append(phases, string(st.Phase))
	}
	sortStrings(phases)
	eq(t, "phases", strings.Join(phases, ","), "submit_unknown,uploading")
}

func TestHistoryRecordFields(t *testing.T) {
	h := newHarness(t)
	h.up.parts = 2
	h.up.extra = []fineup.Event{
		{Kind: fineup.EventChunkFailed, Part: 1, Host: "https://s77a.example"},
		{Kind: fineup.EventChunkFailed, Part: 1, Host: "https://s77b.example"},
	}
	start := h.now
	h.run.Now = func() time.Time {
		// The clock advances by a minute on every reading, so that the
		// duration in the record is not zero.
		h.now = h.now.Add(time.Minute)
		return h.now
	}

	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	recs := h.records()
	eq(t, "records", len(recs), 1)
	rec := recs[0]
	eq(t, "translation id", rec.Translation.ID, 4242)
	eq(t, "translation url", rec.Translation.URL, "/translations/update/4242")
	eq(t, "series", rec.Series.ID, 36866)
	eq(t, "episode", rec.Episode.Number, "1")
	eq(t, "channel", rec.Upload.Channel, "cdn")
	eq(t, "server id", rec.Upload.ServerID, 77)
	eq(t, "parts", rec.Upload.Parts, 2)
	eq(t, "retries", rec.Upload.Retries, 2)
	eq(t, "hosts", rec.Upload.Hosts, []string{"https://s77a.example", "https://s77b.example"})
	if rec.Upload.Duration <= 0 {
		t.Errorf("Duration = %v, want > 0", rec.Upload.Duration)
	}
	if !rec.Upload.Finished.After(start) {
		t.Errorf("Finished = %v, want after %v", rec.Upload.Finished, start)
	}
	if rec.Upload.BytesPerSec <= 0 {
		t.Errorf("BytesPerSec = %v, want > 0", rec.Upload.BytesPerSec)
	}
}

// A password source that is configured but broken stops the run: falling
// through to a login without a password would just produce a second, more
// confusing failure.
func TestPasswordSourceErrorIsReported(t *testing.T) {
	h := newHarness(t)
	h.pass.err = errors.New("keyring is locked")
	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		return site.CreateForm{}, site.ErrNotAuthorized
	}

	_, err := h.run.Run(context.Background(), h.request())
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("got %v, want ErrAuth", err)
	}
	if !strings.Contains(err.Error(), "keyring is locked") {
		t.Errorf("the underlying failure must be named, got %q", err)
	}
	eq(t, "logins", h.site.logins, 0)
	eq(t, "upload calls", h.up.calls, 0)
}

// An upload that never finished has no recorded finalization host, and hosts
// are never stored. The only way to reach the chunks is a fresh form, checked
// against the saved server.
func TestAbortUnfinishedUploadDeletesViaFreshForm(t *testing.T) {
	h := newHarness(t)
	h.putState(&UploadState{
		Phase: PhaseUploading, SeriesID: 36866, ServerID: 77, Channel: translation.ChannelCDN,
		Video: &FileUpload{UUID: "v-1", Done: []int{0}},
	})

	if err := h.run.Abort(context.Background(), h.video); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	eq(t, "calls", strings.Join(h.site.calls, ","), "CreateForm(36866,cdn)")
	eq(t, "deletes", strings.Join(h.up.deletes, ","),
		fineup.Endpoint("https://s77.example")+" v-1")
	keys, _ := h.store.Keys()
	eq(t, "state keys", len(keys), 0)
}

// If the form now names another server, the chunks are somewhere this run
// cannot address. Say so instead of deleting a stranger's upload id.
func TestAbortUnfinishedOnChangedServerWarns(t *testing.T) {
	h := newHarness(t)
	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		f := defaultForm(call)
		f.Video.ServerID = 88
		f.Sub.ServerID = 88
		return f, nil
	}
	h.putState(&UploadState{
		Phase: PhaseUploading, SeriesID: 36866, ServerID: 77, Channel: translation.ChannelCDN,
		Video: &FileUpload{UUID: "v-1", Done: []int{0}},
	})

	if err := h.run.Abort(context.Background(), h.video); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	eq(t, "deletes", len(h.up.deletes), 0)
	if !strings.Contains(strings.Join(h.rep.warns, "|"), "unreachable") {
		t.Errorf("the unreachable chunks must be reported: %v", h.rep.warns)
	}
	keys, _ := h.store.Keys()
	eq(t, "state keys", len(keys), 0)
}

// Starting over abandons whatever is on the server; deleting it first keeps
// the account from accumulating half-uploaded files.
func TestFreshDeletesRemoteChunksFirst(t *testing.T) {
	h := newHarness(t)
	h.putState(&UploadState{
		Phase: PhaseUploading, SeriesID: 36866, ServerID: 77, Channel: translation.ChannelCDN,
		Video: &FileUpload{UUID: "old-uuid", PartSize: fineup.DefaultPartSize, Done: []int{0}},
	})
	req := h.request()
	req.Fresh = true

	if _, err := h.run.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "deletes", strings.Join(h.up.deletes, ","),
		fineup.Endpoint("https://s77.example")+" old-uuid")
	if h.up.specs[0].UUID == "old-uuid" {
		t.Error("--fresh must start a new upload id")
	}
}
