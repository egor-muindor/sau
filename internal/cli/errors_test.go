package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"sau/internal/publish"
	"sau/internal/site"
	"sau/internal/translation"
)

func TestSiteNotAuthorizedIsAnAuthFailure(t *testing.T) {
	if got := exitCode(fmt.Errorf("check: %w", site.ErrNotAuthorized)); got != ExitAuth {
		t.Errorf("exitCode(site.ErrNotAuthorized) = %d, want %d", got, ExitAuth)
	}
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
		says []string // fragments that must appear on stderr
	}{
		{name: "success", err: nil, want: ExitOK},
		{
			name: "channel mismatch",
			err:  &publish.ChannelMismatchError{Saved: translation.ChannelRU, Requested: translation.ChannelCDN},
			want: 2,
			says: []string{"--channel", "--fresh"},
		},
		{name: "authorization", err: publish.ErrAuth, want: 3, says: []string{"sau login"}},
		{
			name: "rejected",
			err:  &site.RejectedError{Messages: []string{"Episode number is required.", "Authors are required."}},
			want: 4,
			says: []string{"Episode number is required.", "Authors are required."},
		},
		{
			name: "upload failed",
			err:  &publish.UploadFailedError{Err: errors.New("pool exhausted")},
			want: 5,
			says: []string{"pool exhausted", "resume"},
		},
		{
			// A destructive command stopped because only a human can say
			// whether the form reached the site. Same code as an unresolved
			// outcome, because that is what it is.
			name: "pending decision",
			err:  &publish.PendingDecisionError{},
			want: 6,
			says: []string{"sau resolve"},
		},
		{
			// An upload that failed because the session died is an
			// authorization problem: a resume would just fail the same way.
			name: "authorization inside an upload failure",
			err:  &publish.UploadFailedError{Err: publish.ErrAuth},
			want: ExitAuth,
			says: []string{"sau login"},
		},
		{
			name: "unknown outcome",
			err:  &publish.UnknownOutcomeError{},
			want: 6,
			says: []string{"sau resolve", "unknown"},
		},
		{name: "interrupted", err: context.Canceled, want: ExitInterrupted, says: []string{"interrupted"}},
		{
			// Ctrl-C during an upload arrives wrapped in the upload failure.
			// It is still an interruption, not a failed upload: the cause
			// decides the code, not the outermost type.
			name: "interrupted during an upload",
			err:  &publish.UploadFailedError{Err: context.Canceled},
			want: ExitInterrupted,
			says: []string{"interrupted", "resume"},
		},
		{
			name: "deadline exceeded",
			err:  &publish.UploadFailedError{Err: context.DeadlineExceeded},
			want: ExitInterrupted,
			says: []string{"interrupted"},
		},
		{name: "other", err: errors.New("disk on fire"), want: ExitError, says: []string{"disk on fire"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.err); got != tc.want {
				t.Errorf("exitCode = %d, want %d", got, tc.want)
			}
		})
	}

	// The same table, but through a real command line and a fake runner: the
	// code must survive the trip and the message must reach stderr.
	for _, tc := range cases {
		if tc.err == nil {
			continue
		}
		t.Run("cli/"+tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.runner.err = tc.err
			v := video(t)
			code := h.run("upload", "--episode", "1", "--series", "5", "--authors", "T", v)
			if code != tc.want {
				t.Fatalf("exit code = %d, want %d; stderr = %q", code, tc.want, h.errOut.String())
			}
			for _, s := range tc.says {
				if !strings.Contains(h.errOut.String(), s) {
					t.Errorf("stderr = %q, want it to mention %q", h.errOut.String(), s)
				}
			}
		})
	}
}

func TestErrAuthWrapped(t *testing.T) {
	err := fmt.Errorf("login on mirror: %w", publish.ErrAuth)
	if got := exitCode(err); got != ExitAuth {
		t.Errorf("exitCode(wrapped ErrAuth) = %d, want %d", got, ExitAuth)
	}
}

func TestUploadJSONOutput(t *testing.T) {
	h := newHarness(t)
	h.runner.out = publish.Outcome{TranslationID: 4242}
	v := video(t)

	if code := h.run("upload", "--episode", "1", "--series", "5", "--authors", "T", "--json", v); code != ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.errOut.String())
	}
	var got struct {
		TranslationID int `json:"translationId"`
	}
	if err := json.Unmarshal([]byte(h.out.String()), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v; %q", err, h.out.String())
	}
	if got.TranslationID != 4242 {
		t.Errorf("translationId = %d, want 4242", got.TranslationID)
	}
}
