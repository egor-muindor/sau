package publish

import (
	"context"
	"testing"

	"sau/internal/fineup"
	"sau/internal/translation"
)

// The state must be on disk after every successful chunk: a crash in the
// middle of a multi-hour upload must not cost the whole file.
func TestStateSavedAfterEveryChunk(t *testing.T) {
	h := newHarness(t)
	h.up.parts = 3
	var snapshots [][]int
	h.up.respond = func(call int, s fineup.Spec) (fineup.Result, error) {
		return fineup.Result{UUID: s.UUID, Name: "episode 01 (1080p).mp4", Size: 1024, Parts: 3, Endpoint: s.Base}, nil
	}
	// Spy on the store by reading it back inside the Event hook.
	h.rep = &fakeReporter{answer: true}
	h.run.Report = reporterFunc(func(e fineup.Event) {
		if e.Kind == fineup.EventChunkDone {
			st := h.state()
			snapshots = append(snapshots, append([]int(nil), st.Video.Done...))
			eq(t, "phase during upload", st.Phase, PhaseUploading)
			eq(t, "server id", st.ServerID, 77)
			eq(t, "channel", st.Channel, translation.ChannelCDN)
			eq(t, "started at", st.StartedAt, h.now)
			if st.Size == 0 || st.ModTime.IsZero() {
				t.Error("size and mtime must be stored for the file-changed check")
			}
		}
	})

	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "snapshots", len(snapshots), 3)
	eq(t, "after part 0", snapshots[0], []int{0})
	eq(t, "after part 1", snapshots[1], []int{0, 1})
	eq(t, "after part 2", snapshots[2], []int{0, 1, 2})
}

// A second run over a saved uploading state hands the uploader the parts that
// are already there and the id they belong to.
func TestResumeFeedsDoneAndUUIDToUploader(t *testing.T) {
	h := newHarness(t)
	h.up.parts = 3
	h.putState(&UploadState{
		Phase:    PhaseUploading,
		ServerID: 77,
		Channel:  translation.ChannelCDN,
		Video:    &FileUpload{UUID: "kept-uuid", PartSize: fineup.DefaultPartSize, Done: []int{0, 1}},
	})

	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	spec := h.up.specs[0]
	eq(t, "Spec.UUID", spec.UUID, "kept-uuid")
	eq(t, "Spec.Done", spec.Done, []int{0, 1})
	// Only the missing chunk was uploaded.
	eq(t, "chunk events", len(h.rep.events), 1)
}

// After the upload the phase moves on and the result is recorded, so that a
// later server change can tell "not assembled" from "assembled".
func TestPhaseUploadedAfterUpload(t *testing.T) {
	h := newHarness(t)
	var seen *UploadState
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		seen = h.state()
		return SubmitResultAlias{TranslationID: 7, Location: "/translations/update/7"}, nil
	}
	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if seen == nil {
		t.Fatal("Submit was not called")
	}
	// By the time Submit runs, the marker that says "this may have been sent"
	// is already on disk; the uploaded phase is behind us.
	eq(t, "phase at submit", seen.Phase, PhaseSubmitting)
	if seen.Video.Result == nil {
		t.Fatal("Video.Result must be recorded after the upload")
	}
	eq(t, "result parts", seen.Video.Result.Parts, 2)
	eq(t, "total parts", seen.Video.TotalParts, 2)
}

func TestChannelMismatchStopsBeforeUpload(t *testing.T) {
	h := newHarness(t)
	h.putState(&UploadState{
		Phase: PhaseUploading, ServerID: 77, Channel: translation.ChannelRU,
		Video: &FileUpload{UUID: "u", PartSize: fineup.DefaultPartSize, Done: []int{0}},
	})
	_, err := h.run.Run(context.Background(), h.request())
	var cm *ChannelMismatchError
	if !asErr(err, &cm) {
		t.Fatalf("got %v, want *ChannelMismatchError", err)
	}
	eq(t, "upload calls", h.up.calls, 0)
	// The state survives: the user may continue in the saved channel.
	if _, err := h.store.Keys(); err != nil {
		t.Fatal(err)
	}
	eq(t, "state kept", h.state().Channel, translation.ChannelRU)
}

func TestFreshDropsSavedState(t *testing.T) {
	h := newHarness(t)
	h.putState(&UploadState{
		Phase: PhaseUploading, ServerID: 77, Channel: translation.ChannelCDN,
		Video: &FileUpload{UUID: "old-uuid", PartSize: fineup.DefaultPartSize, Done: []int{0, 1}},
	})
	req := h.request()
	req.Fresh = true

	if _, err := h.run.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}
	spec := h.up.specs[0]
	if spec.UUID == "old-uuid" {
		t.Error("--fresh must start a new upload id")
	}
	eq(t, "Spec.Done", len(spec.Done), 0)
}

func TestFileChangedRestarts(t *testing.T) {
	h := newHarness(t)
	h.putState(&UploadState{
		Size: 999, Phase: PhaseUploading, ServerID: 77, Channel: translation.ChannelCDN,
		Video: &FileUpload{UUID: "old-uuid", PartSize: fineup.DefaultPartSize, Done: []int{0}},
	})
	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "Spec.Done", len(h.up.specs[0].Done), 0)
	if len(h.rep.warns) == 0 {
		t.Error("a restart because the file changed must be reported")
	}
}
