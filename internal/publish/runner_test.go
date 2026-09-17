package publish

import (
	"context"
	"os"
	"strings"
	"testing"

	"sau/internal/fineup"
	"sau/internal/translation"
)

func TestRunHappyPath(t *testing.T) {
	h := newHarness(t)
	out, err := h.run.Run(context.Background(), h.request())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The sequence of steps is the contract: a fresh form for the upload
	// servers, the upload, then a second fresh form for the CSRF, then submit.
	// Login is not called: the session is alive because CreateForm succeeded.
	eq(t, "calls", strings.Join(h.site.calls, ","),
		"CreateForm(36866,cdn),CreateForm(36866,cdn),Submit")
	eq(t, "upload calls", h.up.calls, 1)
	eq(t, "logins", h.site.logins, 0)
	// The public API takes no part in the decision to submit.
	eq(t, "api calls", h.site.apiCalls, 0)

	// The CSRF comes from the SECOND form load: it changes on every load.
	eq(t, "submitted csrf", h.site.lastForm.CSRF, "csrf-2")

	spec := h.up.lastSpec(t)
	eq(t, "Spec.Base", spec.Base, fineup.Endpoint("https://s77.example"))
	eq(t, "Spec.Pool", spec.Pool, []string{"https://s77a.example", "https://s77b.example"})
	eq(t, "Spec.PartSize", spec.PartSize, fineup.DefaultPartSize)
	eq(t, "Spec.MaxConns", spec.MaxConns, 5)
	eq(t, "Spec.Path", spec.Path, h.video)
	if spec.UUID == "" {
		t.Error("Spec.UUID is empty: publish must own the upload id")
	}

	if h.site.lastUp.Video == nil {
		t.Fatal("UploadedFields.Video is nil")
	}
	eq(t, "video uuid", h.site.lastUp.Video.UUID, spec.UUID)
	eq(t, "video name", h.site.lastUp.Video.Name, "episode 01 (1080p).mp4")
	if h.site.lastUp.Sub != nil {
		t.Error("UploadedFields.Sub must be nil without a subtitle file")
	}

	eq(t, "TranslationID", out.TranslationID, 4242)

	// Begin announces the file before a single chunk event arrives, and it
	// carries the real size and the real part count: the events cannot supply
	// them, so this is the only source the progress bar has.
	if len(h.rep.begins) != 1 {
		t.Fatalf("Begin calls = %d, want exactly one per file", len(h.rep.begins))
	}
	b := h.rep.begins[0]
	eq(t, "Begin name", b.Name, "episode 01 (1080p).mp4")
	eq(t, "Begin total", b.Total, int64(1024))
	eq(t, "Begin parts", b.Parts, 1)
	if b.At != 0 {
		t.Errorf("Begin arrived after %d chunk events, want before the first one", b.At)
	}

	keys, _ := h.store.Keys()
	eq(t, "state keys after success", len(keys), 0)
	eq(t, "history records", len(h.records()), 1)
}

func TestRunUploadsSubtitlesAfterVideo(t *testing.T) {
	h := newHarness(t)
	req := h.request()
	req.Draft.SubPath = h.writeSub()

	if _, err := h.run.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "calls", strings.Join(h.site.calls, ","),
		"CreateForm(36866,cdn),CreateForm(36866,cdn),Submit")
	eq(t, "upload calls", h.up.calls, 2)
	eq(t, "first upload path", h.up.specs[0].Path, h.video)
	eq(t, "second upload path", h.up.specs[1].Path, req.Draft.SubPath)
	// The subtitle pool comes from the Sub uploader config, not the video one.
	eq(t, "sub pool", h.up.specs[1].Pool, []string{"https://s77a.example"})
	if h.site.lastUp.Sub == nil {
		t.Fatal("UploadedFields.Sub is nil")
	}

	// One Begin per file, in upload order: the two files are announced
	// separately, because each has its own size and its own bar.
	if len(h.rep.begins) != 2 {
		t.Fatalf("Begin calls = %d, want one per file", len(h.rep.begins))
	}
	eq(t, "first Begin", h.rep.begins[0].Name, "episode 01 (1080p).mp4")
	eq(t, "second Begin", h.rep.begins[1].Name, "episode 01.ass")
	if h.rep.begins[1].At < h.rep.begins[0].At {
		t.Error("the subtitle file was announced before the video one")
	}
}

func TestRunRejectsDisallowedExtension(t *testing.T) {
	h := newHarness(t)
	bad := h.dir + string(os.PathSeparator) + "episode 01.avi"
	if err := os.WriteFile(bad, make([]byte, 16), 0o644); err != nil {
		t.Fatal(err)
	}
	req := h.request()
	req.Draft.VideoPath = bad

	_, err := h.run.Run(context.Background(), req)
	if err == nil {
		t.Fatal("expected an error for a disallowed extension")
	}
	if !strings.Contains(err.Error(), "mp4") {
		t.Errorf("error must name the allowed extensions, got %q", err)
	}
	// The check happens before any byte is uploaded.
	eq(t, "upload calls", h.up.calls, 0)
}

func TestRunValidatesDraft(t *testing.T) {
	h := newHarness(t)
	req := h.request()
	req.Draft.EpisodeNumber = ""
	if _, err := h.run.Run(context.Background(), req); err == nil {
		t.Fatal("expected a validation error")
	}
	eq(t, "form calls", h.site.forms, 0)
}

func TestRunDefaultsChannelAndConcurrency(t *testing.T) {
	h := newHarness(t)
	req := h.request()
	req.Concurrency = 0
	if _, err := h.run.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "Spec.MaxConns", h.up.lastSpec(t).MaxConns, fineup.DefaultMaxConns)
	eq(t, "channel", h.site.lastCh, translation.ChannelCDN)
}

// Begin is per file, not per run: the subtitle file gets its own announcement
// before its own first chunk, otherwise a progress display would attribute the
// subtitle chunks to the video's total.
func TestBeginPrecedesFirstChunkOfEachFile(t *testing.T) {
	h := newHarness(t)
	h.up.parts = 2
	rep := &orderReporter{}
	h.run.Report = rep
	req := h.request()
	req.Draft.SubPath = h.writeSub()

	if _, err := h.run.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "order", strings.Join(rep.entries(), ","),
		"begin:episode 01 (1080p).mp4,chunk:0,chunk:1,begin:episode 01.ass,chunk:0,chunk:1")
}
