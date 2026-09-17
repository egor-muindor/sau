package publish

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sau/internal/site"
	"sau/internal/translation"
)

// The state file outlives the run and is read by a human. Three things must
// never reach it: the CSRF token, which is a live credential for the session;
// the hidden upload field, which is regenerated from a fresh form anyway; and
// the password, which belongs nowhere but memory.
//
// The submit_unknown phase is the worst case: it stores a snapshot of what was
// sent, so it is the one branch where the form could leak into the file.
func TestStateNeverStoresTokenFieldOrPassword(t *testing.T) {
	const (
		canaryCSRF = "CANARY-CSRF-TOKEN-8f21"
		canaryPass = "CANARY-PASSWORD-3b7c"
	)
	h := newHarness(t)
	h.pass.pass = canaryPass
	h.site.form = func(call int, seriesID int, ch translation.Channel) (site.CreateForm, error) {
		f := defaultForm(call)
		f.CSRF = canaryCSRF
		return f, nil
	}
	h.site.submit = func(call int) (SubmitResultAlias, error) {
		return SubmitResultAlias{}, &site.UnknownOutcomeError{Err: errors.New("timeout")}
	}

	req := h.request()
	req.Draft.SubPath = h.writeSub()
	if _, err := h.run.Run(context.Background(), req); err == nil {
		t.Fatal("expected an unknown outcome")
	}
	eq(t, "phase", h.state().Phase, PhaseSubmitUnknown)

	raw := string(h.store.raw(h.key()))
	if raw == "" {
		t.Fatal("nothing was written to the store")
	}
	for _, forbidden := range []string{
		canaryCSRF,
		canaryPass,
		// A fragment of the hidden field: its JSON members would appear
		// verbatim if the field were ever stored.
		"upload successful",
		"batchId",
		"endpoint",
	} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("the state file contains %q:\n%s", forbidden, raw)
		}
	}
}
