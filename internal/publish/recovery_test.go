package publish

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"sau/internal/fineup"
	"sau/internal/site"
	"sau/internal/translation"
)

func TestPoolExhaustedFetchesFreshForm(t *testing.T) {
	h := newHarness(t)
	h.up.parts = 3
	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		f := defaultForm(call)
		if call >= 2 {
			f.Video.ServerURLs = []string{"https://s77c.example", "https://s77d.example"}
		}
		return f, nil
	}
	h.up.respond = func(call int, s fineup.Spec) (fineup.Result, error) {
		if call == 1 {
			return fineup.Result{}, &fineup.PoolExhaustedError{Part: 2, Done: []int{0, 1},
				Last: errors.New("all hosts failed")}
		}
		return fineup.Result{UUID: s.UUID, Name: "episode 01 (1080p).mp4", Size: 1024, Parts: 3, Endpoint: s.Base}, nil
	}

	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Upload servers are never cached: a fresh form was fetched mid-upload.
	eq(t, "calls", strings.Join(h.site.calls, ","),
		"CreateForm(36866,cdn),CreateForm(36866,cdn),CreateForm(36866,cdn),Submit")
	eq(t, "upload calls", h.up.calls, 2)

	second := h.up.specs[1]
	eq(t, "Spec.Done", second.Done, []int{0, 1})
	eq(t, "Spec.Pool", second.Pool, []string{"https://s77c.example", "https://s77d.example"})
	eq(t, "Spec.UUID kept", second.UUID, h.up.specs[0].UUID)
}

// A fresh form may point at another server; then the resume table decides.
func TestPoolExhaustedWithServerChangeRestarts(t *testing.T) {
	h := newHarness(t)
	h.up.parts = 3
	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		f := defaultForm(call)
		if call >= 2 {
			f.Video.ServerID = 88
			f.Sub.ServerID = 88
		}
		return f, nil
	}
	h.up.respond = func(call int, s fineup.Spec) (fineup.Result, error) {
		if call == 1 {
			return fineup.Result{}, &fineup.PoolExhaustedError{Part: 2, Done: []int{0, 1}}
		}
		return fineup.Result{UUID: s.UUID, Name: "episode 01 (1080p).mp4", Size: 1024, Parts: 3, Endpoint: s.Base}, nil
	}

	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	second := h.up.specs[1]
	eq(t, "Spec.Done", len(second.Done), 0)
	if second.UUID == h.up.specs[0].UUID {
		t.Error("a new server means a new upload id")
	}
}

func TestPoolExhaustedTwiceFails(t *testing.T) {
	h := newHarness(t)
	h.up.parts = 3
	h.up.respond = func(call int, s fineup.Spec) (fineup.Result, error) {
		return fineup.Result{}, &fineup.PoolExhaustedError{Part: 2, Done: []int{0, 1}}
	}

	_, err := h.run.Run(context.Background(), h.request())
	var uf *UploadFailedError
	if !errors.As(err, &uf) {
		t.Fatalf("got %v, want *UploadFailedError", err)
	}
	eq(t, "upload calls", h.up.calls, 2)
	// The state survives so that the next run can resume.
	st := h.state()
	eq(t, "phase", st.Phase, PhaseUploading)
	eq(t, "done", st.Video.Done, []int{0, 1})
	eq(t, "submits", h.site.submits, 0)
}

// The protocol gives no way to ask the server which chunks it has, so a failed
// finalize costs one full re-upload under the same id.
func TestFinalizeErrorReuploadsOnce(t *testing.T) {
	h := newHarness(t)
	h.up.parts = 2
	h.up.respond = func(call int, s fineup.Spec) (fineup.Result, error) {
		if call == 1 {
			return fineup.Result{}, &fineup.FinalizeError{Host: "https://s77a.example",
				Err: errors.New("500")}
		}
		return fineup.Result{UUID: s.UUID, Name: "episode 01 (1080p).mp4", Size: 1024, Parts: 2, Endpoint: s.Base}, nil
	}

	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "upload calls", h.up.calls, 2)
	second := h.up.specs[1]
	eq(t, "Spec.UUID kept", second.UUID, h.up.specs[0].UUID)
	eq(t, "Spec.Done reset", len(second.Done), 0)
	// No fresh form: the server did not change.
	eq(t, "form calls", h.site.forms, 2)
}

func TestFinalizeErrorTwiceFails(t *testing.T) {
	h := newHarness(t)
	h.up.respond = func(call int, s fineup.Spec) (fineup.Result, error) {
		return fineup.Result{}, &fineup.FinalizeError{Host: "https://s77a.example",
			Err: errors.New("500")}
	}
	_, err := h.run.Run(context.Background(), h.request())
	var uf *UploadFailedError
	if !errors.As(err, &uf) {
		t.Fatalf("got %v, want *UploadFailedError", err)
	}
	eq(t, "upload calls", h.up.calls, 2)
}

// Video and subtitles must end up on the same upload server: the hidden field
// of each names the server from the form fetched after the upload. If the
// server changes while the subtitles are going up, re-uploading only the
// subtitles would leave the video field pointing at a server that does not
// hold it. So a server change restarts both files, video first.
func TestServerChangeDuringSubtitlesRestartsBothFiles(t *testing.T) {
	h := newHarness(t)
	h.up.parts = 2
	req := h.request()
	req.Draft.SubPath = h.writeSub()

	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		f := defaultForm(call)
		if call >= 2 {
			f.Video.ServerID = 88
			f.Sub.ServerID = 88
		}
		return f, nil
	}
	subFailed := false
	h.up.respond = func(call int, s fineup.Spec) (fineup.Result, error) {
		if strings.HasSuffix(s.Path, ".ass") && !subFailed {
			subFailed = true
			return fineup.Result{}, &fineup.PoolExhaustedError{Part: 1, Done: []int{0}}
		}
		return fineup.Result{UUID: s.UUID, Name: filepath.Base(s.Path), Size: 1024, Parts: 2,
			Endpoint: s.Base, LastHost: s.Pool[0]}, nil
	}

	if _, err := h.run.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The whole upload starts over on the new server, video first.
	eq(t, "upload calls", h.up.calls, 4)
	var order []string
	for _, s := range h.up.specs {
		order = append(order, filepath.Ext(s.Path))
	}
	eq(t, "upload order", strings.Join(order, ","), ".mp4,.ass,.mp4,.ass")

	// The video really was re-uploaded, not just replayed from its old state.
	eq(t, "video Done reset", len(h.up.specs[2].Done), 0)
	eq(t, "sub Done reset", len(h.up.specs[3].Done), 0)

	// Both hidden fields name the new server.
	eq(t, "submitted server", h.site.lastForm.Video.ServerID, 88)
	eq(t, "sub server", h.site.lastForm.Sub.ServerID, 88)
}

// The saved Result carries the endpoint of the pool the chunks went to, but
// hosts are routes and the form is the only source of them. A resume must take
// the base and the pool from the fresh form, never from the saved result.
func TestResumeTakesHostsFromFreshForm(t *testing.T) {
	h := newHarness(t)
	h.up.parts = 3
	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		f := defaultForm(call)
		f.Video.ServerURL = "https://s77-new.example"
		f.Video.ServerURLs = []string{"https://s77e.example", "https://s77f.example"}
		return f, nil
	}
	h.putState(&UploadState{
		Phase: PhaseUploading, ServerID: 77, Channel: translation.ChannelCDN,
		Video: &FileUpload{UUID: "kept-uuid", PartSize: fineup.DefaultPartSize, Done: []int{0, 1},
			Result: &fineup.Result{UUID: "kept-uuid", Endpoint: "https://s77-stale.example/upload.php",
				LastHost: "https://s77a-stale.example/upload.php"}},
	})

	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	spec := h.up.specs[0]
	eq(t, "Spec.UUID", spec.UUID, "kept-uuid")
	eq(t, "Spec.Base", spec.Base, fineup.Endpoint("https://s77-new.example"))
	eq(t, "Spec.Pool", spec.Pool, []string{"https://s77e.example", "https://s77f.example"})
}

// A cancelled upload is not a failed upload: the exit code for an interrupted
// run differs from the one for a broken one, so the cancellation must reach
// the caller unwrapped.
func TestContextCancellationIsNotWrapped(t *testing.T) {
	h := newHarness(t)
	h.up.respond = func(call int, s fineup.Spec) (fineup.Result, error) {
		return fineup.Result{}, context.Canceled
	}

	_, err := h.run.Run(context.Background(), h.request())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	var uf *UploadFailedError
	if errors.As(err, &uf) {
		t.Error("a cancellation must not be reported as a failed upload")
	}
	// Whatever arrived is still on disk, so the next run resumes.
	eq(t, "phase", h.state().Phase, PhaseUploading)
}
