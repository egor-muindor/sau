package publish

import "errors"

// ErrAuth means the site refused the credentials; exit code 3.
var ErrAuth = errors.New("publish: authorization failed")

// UnknownOutcomeError means the form was sent but the answer never arrived.
// Only a human may resolve it; exit code 6.
type UnknownOutcomeError struct{ State *UploadState }

func (e *UnknownOutcomeError) Error() string {
	return "publish: the form was sent but the outcome is unknown; resolve it manually"
}

// PendingDecisionError means the saved state describes a form that may already
// be on the site, and the destructive command that asked for it would throw
// away the only record of what was sent. The human answers first. Exit code 6,
// the same as an unresolved outcome, because that is what it is.
type PendingDecisionError struct{ State *UploadState }

func (e *PendingDecisionError) Error() string {
	return "publish: a previous submission is still unresolved; " +
		"settle it first with sau resolve --submitted or sau resolve --resend"
}

// UploadFailedError means the upload did not finish; the state is on disk and
// resuming is possible. Exit code 5.
type UploadFailedError struct{ Err error }

func (e *UploadFailedError) Error() string { return "publish: upload failed: " + e.Err.Error() }
func (e *UploadFailedError) Unwrap() error { return e.Err }
