package publish

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sau/internal/fineup"
	"sau/internal/translation"
)

func unknownState(h *harness) *UploadState {
	return &UploadState{
		Phase:       PhaseSubmitUnknown,
		ServerID:    77,
		Channel:     translation.ChannelCDN,
		StartedAt:   h.now.Add(-time.Hour),
		SubmittedAt: h.now.Add(-30 * time.Minute),
		Video: &FileUpload{
			UUID: "u-1", PartSize: fineup.DefaultPartSize, TotalParts: 2, Done: []int{0, 1},
			Result: &fineup.Result{UUID: "u-1", Name: "episode 01 (1080p).mp4", Size: 1024, Parts: 2,
				Endpoint: "https://s77.example/upload.php"},
		},
		Snapshot: &Snapshot{
			SeriesID: 36866, EpisodeNumber: "1", EpisodeType: "tv", Type: "voiceRu",
			Authors: "Team (Alice, Bob)", Channel: "cdn", VideoName: "episode 01 (1080p).mp4",
		},
	}
}

func TestUnknownAsksBeforeTouchingTheNetwork(t *testing.T) {
	h := newHarness(t)
	h.putState(unknownState(h))
	h.rep.answer = true

	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Nothing was uploaded and nothing was submitted.
	eq(t, "upload calls", h.up.calls, 0)
	eq(t, "submits", h.site.submits, 0)
	eq(t, "site calls", len(h.site.calls), 0)
	eq(t, "api calls", h.site.apiCalls, 0)

	if len(h.rep.questions) != 1 {
		t.Fatalf("questions = %d, want 1", len(h.rep.questions))
	}
	q := h.rep.questions[0]
	for _, want := range []string{
		"absence in API does not prove the form was not submitted",
		"36866", "voiceRu", "Team (Alice, Bob)", "cdn", "episode 01 (1080p).mp4",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("question must contain %q:\n%s", want, q)
		}
	}
}

func TestUnknownAnsweredYes(t *testing.T) {
	h := newHarness(t)
	h.putState(unknownState(h))
	h.rep.answer = true

	out, err := h.run.Run(context.Background(), h.request())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The translation id is unknown: it only comes from the redirect we lost.
	eq(t, "TranslationID", out.TranslationID, 0)
	keys, _ := h.store.Keys()
	eq(t, "state keys", len(keys), 0)
	recs := h.records()
	eq(t, "history records", len(recs), 1)
	eq(t, "record series", recs[0].Series.ID, 36866)
	eq(t, "record translation id", recs[0].Translation.ID, 0)
}

func TestUnknownAnsweredNoGoesBackToTheNormalPath(t *testing.T) {
	h := newHarness(t)
	h.putState(unknownState(h))
	h.rep.answer = false

	out, err := h.run.Run(context.Background(), h.request())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// A fresh form, the server and channel check, then the submit.
	eq(t, "calls", strings.Join(h.site.calls, ","),
		"CreateForm(36866,cdn),CreateForm(36866,cdn),Submit")
	eq(t, "TranslationID", out.TranslationID, 4242)
}

func TestUnknownWithoutConsoleStaysUnknown(t *testing.T) {
	h := newHarness(t)
	h.putState(unknownState(h))
	h.rep.askErr = errors.New("no console")

	_, err := h.run.Run(context.Background(), h.request())
	var ue *UnknownOutcomeError
	if !errors.As(err, &ue) {
		t.Fatalf("got %v, want *UnknownOutcomeError", err)
	}
	eq(t, "phase", h.state().Phase, PhaseSubmitUnknown)
	eq(t, "upload calls", h.up.calls, 0)
}

func TestResolveSubmitted(t *testing.T) {
	h := newHarness(t)
	h.putState(unknownState(h))

	if err := h.run.Resolve(context.Background(), h.video, true); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	keys, _ := h.store.Keys()
	eq(t, "state keys", len(keys), 0)
	eq(t, "history records", len(h.records()), 1)
	eq(t, "questions", len(h.rep.questions), 0)
}

func TestResolveResend(t *testing.T) {
	h := newHarness(t)
	h.putState(unknownState(h))

	if err := h.run.Resolve(context.Background(), h.video, false); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	eq(t, "phase", h.state().Phase, PhaseUploaded)
	eq(t, "history records", len(h.records()), 0)
}

func TestResolveRefusesOtherPhases(t *testing.T) {
	h := newHarness(t)
	h.putState(&UploadState{
		Phase: PhaseUploading, ServerID: 77, Channel: translation.ChannelCDN,
		Video: &FileUpload{UUID: "u", PartSize: fineup.DefaultPartSize, Done: []int{0}},
	})
	if err := h.run.Resolve(context.Background(), h.video, true); err == nil {
		t.Fatal("expected an error: there is nothing to resolve")
	}
	eq(t, "history records", len(h.records()), 0)
}

// With no reporter there is nobody to ask, and the phase has no automatic
// exit, so the run stops exactly where it stood.
func TestUnknownWithoutReporterStaysUnknown(t *testing.T) {
	h := newHarness(t)
	h.putState(unknownState(h))
	h.run.Report = nil

	_, err := h.run.Run(context.Background(), h.request())
	var ue *UnknownOutcomeError
	if !errors.As(err, &ue) {
		t.Fatalf("got %v, want *UnknownOutcomeError", err)
	}
	eq(t, "state carried", ue.State.Phase, PhaseSubmitUnknown)
	eq(t, "phase", h.state().Phase, PhaseSubmitUnknown)
	eq(t, "site calls", len(h.site.calls), 0)
	eq(t, "upload calls", h.up.calls, 0)
	eq(t, "history records", len(h.records()), 0)
}
