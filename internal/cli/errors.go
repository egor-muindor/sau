package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"sau/internal/publish"
	"sau/internal/site"
)

// exitCode maps a failure onto the process exit code documented in
// docs/architecture.md section 6.
func exitCode(err error) int {
	if err == nil {
		return ExitOK
	}

	// An interruption is checked before every failure type, because it arrives
	// wrapped in one: Ctrl-C during an upload surfaces as *UploadFailedError
	// around context.Canceled. The cause decides the code, so that stopping the
	// tool by hand reports 130 rather than an upload failure.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ExitInterrupted
	}

	// A batch prints its own summary; the error only carries the worst code.
	var be *batchError
	if errors.As(err, &be) {
		return be.code
	}

	var ue *usageError
	if errors.As(err, &ue) {
		return ExitUsage
	}
	var cme *publish.ChannelMismatchError
	if errors.As(err, &cme) {
		return ExitUsage // the caller asked for the wrong channel: code 2
	}
	// Authorization is checked before the upload failure that may carry it: an
	// upload that died because the session expired is an authorization
	// problem, and resuming it would fail exactly the same way.
	if errors.Is(err, publish.ErrAuth) || errors.Is(err, site.ErrNotAuthorized) {
		return ExitAuth
	}
	var re *site.RejectedError
	if errors.As(err, &re) {
		return ExitRejected
	}
	var uf *publish.UploadFailedError
	if errors.As(err, &uf) {
		return ExitUpload
	}
	var uo *publish.UnknownOutcomeError
	if errors.As(err, &uo) {
		return ExitUnknown
	}
	var pd *publish.PendingDecisionError
	if errors.As(err, &pd) {
		return ExitUnknown
	}
	return ExitError
}

// reportError explains the failure and, where the user has a next move, names
// it. The wording matters: after an unknown outcome the tool must never retry
// on its own, so the message has to hand the decision back to the person.
func reportError(w io.Writer, err error) {
	var ue *usageError
	if errors.As(err, &ue) {
		if ue.msg != "" {
			fmt.Fprintf(w, "sau: %s\n", ue.msg)
		}
		fmt.Fprint(w, usageText)
		return
	}

	var be *batchError
	if errors.As(err, &be) {
		fmt.Fprintf(w, "sau: %s\n", be.Error())
		return
	}

	// Interruption comes first here for the same reason it does in exitCode:
	// the message has to agree with the code that goes with it.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		fmt.Fprintln(w, "sau: interrupted")
		var uf *publish.UploadFailedError
		if errors.As(err, &uf) {
			fmt.Fprintln(w, "hint: the state was saved; run the same command again to resume")
		}
		return
	}

	var cme *publish.ChannelMismatchError
	if errors.As(err, &cme) {
		fmt.Fprintf(w, "sau: this upload was started on channel %s, but %s was requested\n",
			cme.Saved, cme.Requested)
		fmt.Fprintf(w, "     the channel decides which hosts hold the chunks, so the parts already\n")
		fmt.Fprintf(w, "     uploaded are not reachable on the requested one\n")
		fmt.Fprintf(w, "hint: rerun with --channel %s, or discard the parts with --fresh\n", cme.Saved)
		return
	}

	if errors.Is(err, publish.ErrAuth) || errors.Is(err, site.ErrNotAuthorized) {
		fmt.Fprintln(w, "sau: authorization failed")
		fmt.Fprintln(w, "hint: run: sau login")
		return
	}

	var re *site.RejectedError
	if errors.As(err, &re) {
		fmt.Fprintln(w, "sau: the site rejected the form:")
		for _, m := range re.Messages {
			fmt.Fprintf(w, "     %s\n", m)
		}
		return
	}

	var uf *publish.UploadFailedError
	if errors.As(err, &uf) {
		fmt.Fprintf(w, "sau: upload failed: %v\n", uf.Err)
		fmt.Fprintln(w, "hint: the state was saved; run the same command again to resume")
		return
	}

	var uo *publish.UnknownOutcomeError
	if errors.As(err, &uo) {
		fmt.Fprintln(w, "sau: the outcome of the submission is unknown")
		fmt.Fprintln(w, "     the form was sent but no answer arrived, and the public API does not")
		fmt.Fprintln(w, "     tell the truth about this for several minutes, so nothing was retried")
		fmt.Fprintln(w, "hint: check the site, then run one of")
		fmt.Fprintln(w, "       sau resolve <video> --submitted")
		fmt.Fprintln(w, "       sau resolve <video> --resend")
		return
	}

	var pd *publish.PendingDecisionError
	if errors.As(err, &pd) {
		fmt.Fprintln(w, "sau: an earlier submission is still unresolved")
		fmt.Fprintln(w, "     this command would discard the only record of what was sent, so it")
		fmt.Fprintln(w, "     stopped; only a person can say whether the form reached the site")
		fmt.Fprintln(w, "hint: check the site, then run one of")
		fmt.Fprintln(w, "       sau resolve <video> --submitted")
		fmt.Fprintln(w, "       sau resolve <video> --resend")
		return
	}

	fmt.Fprintf(w, "sau: %v\n", err)
}
