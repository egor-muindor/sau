package publish

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sau/internal/site"
)

func TestSubmitSuccessClosesState(t *testing.T) {
	h := newHarness(t)
	out, err := h.run.Run(context.Background(), h.request())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "TranslationID", out.TranslationID, 4242)
	keys, _ := h.store.Keys()
	eq(t, "state keys", len(keys), 0)
	eq(t, "submits", h.site.submits, 1)
	// Asserted as a fact: the API takes no part in a successful submission.
	eq(t, "api calls", h.site.apiCalls, 0)
	eq(t, "history records", len(h.records()), 1)
}

func TestSubmitRejected(t *testing.T) {
	h := newHarness(t)
	rejected := &site.RejectedError{Messages: []string{"Episode number is required.", "Authors are required."}}
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		return SubmitResultAlias{}, rejected
	}

	_, err := h.run.Run(context.Background(), h.request())
	var re *site.RejectedError
	if !errors.As(err, &re) {
		t.Fatalf("got %v, want *site.RejectedError", err)
	}
	eq(t, "messages", strings.Join(re.Messages, "|"),
		"Episode number is required.|Authors are required.")
	// Retrying is safe here: the site created nothing. The upload is kept.
	eq(t, "phase", h.state().Phase, PhaseUploaded)
	eq(t, "submits", h.site.submits, 1)
	warns := strings.Join(h.rep.warns, "|")
	for _, m := range re.Messages {
		if !strings.Contains(warns, m) {
			t.Errorf("warn output %q must contain %q", warns, m)
		}
	}
	eq(t, "history records", len(h.records()), 0)
}

func TestSubmitNotSentRetriesThreeTimes(t *testing.T) {
	h := newHarness(t)
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		return SubmitResultAlias{}, &site.NotSentError{Err: errors.New("connection refused")}
	}

	_, err := h.run.Run(context.Background(), h.request())
	var uf *UploadFailedError
	if !errors.As(err, &uf) {
		t.Fatalf("got %v, want *UploadFailedError", err)
	}
	eq(t, "submits", h.site.submits, 3)
	eq(t, "phase", h.state().Phase, PhaseUploaded)
}

func TestSubmitNotSentThenSuccess(t *testing.T) {
	h := newHarness(t)
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		if call < 3 {
			return SubmitResultAlias{}, &site.NotSentError{Err: errors.New("dns failure")}
		}
		return SubmitResultAlias{TranslationID: 4242, Location: "/translations/update/4242"}, nil
	}

	out, err := h.run.Run(context.Background(), h.request())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "submits", h.site.submits, 3)
	eq(t, "TranslationID", out.TranslationID, 4242)
	keys, _ := h.store.Keys()
	eq(t, "state keys", len(keys), 0)
}

// The single most dangerous branch in the program: the form went out and the
// answer never came. One call, no automatic retry, ever.
func TestSubmitUnknownOutcome(t *testing.T) {
	h := newHarness(t)
	req := h.request()
	req.Draft.SubPath = h.writeSub()
	req.Draft.AddedByAuthor = true
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		return SubmitResultAlias{}, &site.UnknownOutcomeError{Err: errors.New("timeout awaiting response headers")}
	}

	_, err := h.run.Run(context.Background(), req)
	var ue *UnknownOutcomeError
	if !errors.As(err, &ue) {
		t.Fatalf("got %v, want *UnknownOutcomeError", err)
	}
	eq(t, "submits", h.site.submits, 1)
	// The API is not asked: a fresh publication is invisible there for minutes.
	eq(t, "api calls", h.site.apiCalls, 0)

	st := h.state()
	eq(t, "phase", st.Phase, PhaseSubmitUnknown)
	eq(t, "submitted at", st.SubmittedAt, h.now)
	if st.Snapshot == nil {
		t.Fatal("Snapshot is nil: there would be nothing to show the human")
	}
	s := st.Snapshot
	eq(t, "snapshot series", s.SeriesID, 36866)
	eq(t, "snapshot episode", s.EpisodeNumber, "1")
	eq(t, "snapshot episode type", s.EpisodeType, "tv")
	eq(t, "snapshot type", s.Type, "voiceRu")
	eq(t, "snapshot authors", s.Authors, "Team (Alice, Bob)")
	eq(t, "snapshot by author", s.AddedByAuthor, true)
	eq(t, "snapshot channel", s.Channel, "cdn")
	eq(t, "snapshot video", s.VideoName, "episode 01 (1080p).mp4")
	eq(t, "snapshot sub", s.SubName, "episode 01.ass")
	eq(t, "state returned", ue.State.Phase, PhaseSubmitUnknown)
	eq(t, "history records", len(h.records()), 0)
}

// A login page in answer to the form means the session died and the form was
// not accepted: nothing was created, so one re-login and one retry are safe.
// The token belongs to the old session, so a fresh form comes with it.
func TestSubmitNotAuthorizedRetriesAfterLogin(t *testing.T) {
	h := newHarness(t)
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		if call == 1 {
			return SubmitResultAlias{}, site.ErrNotAuthorized
		}
		return SubmitResultAlias{TranslationID: 4242, Location: "/translations/update/4242"}, nil
	}

	out, err := h.run.Run(context.Background(), h.request())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	eq(t, "submits", h.site.submits, 2)
	eq(t, "logins", h.site.logins, 1)
	// The retry used a token from a form fetched after the login.
	eq(t, "submitted csrf", h.site.lastForm.CSRF, "csrf-3")
	eq(t, "TranslationID", out.TranslationID, 4242)
	keys, _ := h.store.Keys()
	eq(t, "state keys", len(keys), 0)
}

func TestSubmitNotAuthorizedTwiceIsErrAuth(t *testing.T) {
	h := newHarness(t)
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		return SubmitResultAlias{}, site.ErrNotAuthorized
	}

	_, err := h.run.Run(context.Background(), h.request())
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("got %v, want ErrAuth", err)
	}
	eq(t, "logins", h.site.logins, 1)
	eq(t, "submits", h.site.submits, 2)
	// The form was refused, not accepted: rolling back is safe here.
	eq(t, "phase", h.state().Phase, PhaseUploaded)
}

// An error nobody classified could mean anything, including a form that
// arrived. Fail closed: keep the marker and let a human decide.
func TestSubmitUnclassifiedErrorKeepsTheMarker(t *testing.T) {
	h := newHarness(t)
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		return SubmitResultAlias{}, errors.New("something nobody anticipated")
	}

	_, err := h.run.Run(context.Background(), h.request())
	var uf *UploadFailedError
	if !errors.As(err, &uf) {
		t.Fatalf("got %v, want *UploadFailedError", err)
	}
	eq(t, "submits", h.site.submits, 1)
	eq(t, "phase", h.state().Phase, PhaseSubmitting)
}
