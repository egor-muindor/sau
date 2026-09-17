package publish

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The window between sending the form and dropping the state is where a
// duplicate publication is born: a crash there would leave the state looking
// like "uploaded, not yet sent", and the next run would send it again. So the
// state says "submitting" before the request goes out, and that phase is
// resolved by a human, exactly like an unknown outcome.
func TestStateIsSubmittingWhileSubmitIsInFlight(t *testing.T) {
	h := newHarness(t)
	var seen *UploadState
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		seen = h.state()
		return SubmitResultAlias{TranslationID: 4242, Location: "/translations/update/4242"}, nil
	}

	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if seen == nil {
		t.Fatal("Submit was not called")
	}
	eq(t, "phase during submit", seen.Phase, PhaseSubmitting)
	eq(t, "submitted at", seen.SubmittedAt, h.now)
	if seen.Snapshot == nil {
		t.Fatal("the snapshot must be on disk before the form goes out")
	}
	eq(t, "snapshot video", seen.Snapshot.VideoName, "episode 01 (1080p).mp4")
}

// Even a panic in the middle of the request leaves the marker behind.
func TestSubmittingSurvivesAPanicInSubmit(t *testing.T) {
	h := newHarness(t)
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		panic("connection exploded")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected the panic to propagate")
			}
		}()
		_, _ = h.run.Run(context.Background(), h.request())
	}()
	eq(t, "phase", h.state().Phase, PhaseSubmitting)
}

// The form went out and was accepted, but the state could not be dropped. The
// state stays in submitting, which is the safe reading: the next run asks
// rather than sending a second time.
func TestSuccessfulSubmitWithFailedDeleteGoesToDialogNextRun(t *testing.T) {
	h := newHarness(t)
	h.store.deleteErr = errors.New("state directory is read only")

	out, err := h.run.Run(context.Background(), h.request())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "TranslationID", out.TranslationID, 4242)
	eq(t, "phase", h.state().Phase, PhaseSubmitting)
	if !strings.Contains(strings.Join(h.rep.warns, "|"), "state") {
		t.Errorf("the failure to drop the state must be reported: %v", h.rep.warns)
	}

	// Second run: the human is asked, and nothing is sent a second time.
	h.store.deleteErr = nil
	h.rep.answer = true
	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	eq(t, "submits", h.site.submits, 1)
	eq(t, "questions", len(h.rep.questions), 1)
	keys, _ := h.store.Keys()
	eq(t, "state keys", len(keys), 0)
}

func TestSubmittingPhaseAsksHumanLikeUnknown(t *testing.T) {
	h := newHarness(t)
	st := unknownState(h)
	st.Phase = PhaseSubmitting
	h.putState(st)
	h.rep.answer = false

	if _, err := h.run.Run(context.Background(), h.request()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Answering "no" puts it back on the normal path, nothing was auto-sent.
	eq(t, "questions", len(h.rep.questions), 1)
	eq(t, "calls", strings.Join(h.site.calls, ","),
		"CreateForm(36866,cdn),CreateForm(36866,cdn),Submit")
}

func TestResolveAcceptsSubmitting(t *testing.T) {
	h := newHarness(t)
	st := unknownState(h)
	st.Phase = PhaseSubmitting
	h.putState(st)

	if err := h.run.Resolve(context.Background(), h.video, true); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	keys, _ := h.store.Keys()
	eq(t, "state keys", len(keys), 0)
	eq(t, "history records", len(h.records()), 1)
}

func TestStatusIncludesSubmitting(t *testing.T) {
	h := newHarness(t)
	st := unknownState(h)
	st.Phase = PhaseSubmitting
	h.putState(st)

	got, err := h.run.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	eq(t, "count", len(got), 1)
	eq(t, "phase", got[0].Phase, PhaseSubmitting)
}

// A save that fails is reported once per run rather than silently dropped: the
// resume state is the only thing standing between a broken connection and a
// re-upload from zero.
func TestSaveErrorIsWarned(t *testing.T) {
	h := newHarness(t)
	h.up.parts = 3
	h.store.saveErr = errors.New("disk is full")
	h.store.saveOK = 1 // the first save succeeds, everything after it fails

	if _, err := h.run.Run(context.Background(), h.request()); err == nil {
		t.Fatal("expected the run to fail once the state cannot be saved")
	}
	warns := strings.Join(h.rep.warns, "|")
	if !strings.Contains(warns, "disk is full") {
		t.Errorf("warnings must name the save failure, got %v", h.rep.warns)
	}
}

// A state that may already have been published is not something to throw away
// on a whim: deleting the chunks or the state file would destroy the only
// record of what was sent. Both aborting and starting over refuse until the
// human has answered.
func TestAbortRefusesWhileADecisionIsPending(t *testing.T) {
	for _, phase := range []Phase{PhaseSubmitting, PhaseSubmitUnknown} {
		t.Run(string(phase), func(t *testing.T) {
			h := newHarness(t)
			st := unknownState(h)
			st.Phase = phase
			h.putState(st)

			err := h.run.Abort(context.Background(), h.video)
			var pd *PendingDecisionError
			if !errors.As(err, &pd) {
				t.Fatalf("got %v, want *PendingDecisionError", err)
			}
			eq(t, "state phase", pd.State.Phase, phase)
			if !strings.Contains(err.Error(), "sau resolve --submitted") {
				t.Errorf("the message must point at the resolve command, got %q", err)
			}
			eq(t, "deletes", len(h.up.deletes), 0)
			eq(t, "state kept", h.state().Phase, phase)
		})
	}
}

func TestFreshRefusesWhileADecisionIsPending(t *testing.T) {
	for _, phase := range []Phase{PhaseSubmitting, PhaseSubmitUnknown} {
		t.Run(string(phase), func(t *testing.T) {
			h := newHarness(t)
			st := unknownState(h)
			st.Phase = phase
			h.putState(st)
			req := h.request()
			req.Fresh = true

			_, err := h.run.Run(context.Background(), req)
			var pd *PendingDecisionError
			if !errors.As(err, &pd) {
				t.Fatalf("got %v, want *PendingDecisionError", err)
			}
			eq(t, "deletes", len(h.up.deletes), 0)
			eq(t, "upload calls", h.up.calls, 0)
			eq(t, "questions", len(h.rep.questions), 0)
			eq(t, "state kept", h.state().Phase, phase)
		})
	}
}
