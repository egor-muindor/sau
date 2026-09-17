package site

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrNotAuthorized means the session is gone: the site answered with the login
// page instead of the page that was asked for. Changing the password invalidates
// the session and shows up exactly like this; a single re-login fixes it.
var ErrNotAuthorized = errors.New("site: not authorized")

// RejectedError means the site refused the form and listed the reasons.
// Retrying is safe: nothing was created.
type RejectedError struct{ Messages []string }

func (e *RejectedError) Error() string {
	return "site: form rejected: " + strings.Join(e.Messages, "; ")
}

// NotSentError means the request provably never reached the server: connection
// refused, name resolution failure, certificate failure. Retrying is safe.
type NotSentError struct{ Err error }

func (e *NotSentError) Error() string { return "site: request not sent: " + e.Err.Error() }
func (e *NotSentError) Unwrap() error { return e.Err }

// UnknownOutcomeError means the outcome cannot be determined: a timeout, or the
// connection dropped after the body went out. Automatic retry is forbidden
// forever — only a human resolves it (docs/architecture.md §7).
type UnknownOutcomeError struct{ Err error }

func (e *UnknownOutcomeError) Error() string { return "site: outcome unknown: " + e.Err.Error() }
func (e *UnknownOutcomeError) Unwrap() error { return e.Err }

// HTTPError is a final HTTP answer that is neither a page nor data: a 403, a
// 404, or a 5xx that outlived the retries. It is reported as itself, because
// "the site said no" and "the page has no form on it" call for different
// answers from the user.
type HTTPError struct {
	Status int
	URL    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("site: http %d for %s", e.Status, e.URL)
}

// APIError is an error object returned inside a 200 response of the read-only API.
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("site: api error %d: %s", e.Code, e.Message) }

// classifyTransportErr splits a transport error into the only two classes that
// matter for a form submit. Getting this wrong means either a duplicate
// publication or a lost one, so the rule is narrow and explicit: only errors
// that prove the request never left this machine count as "not sent".
// Everything else — a timeout, a connection dropped after the body went out —
// is an unknown outcome, and an unknown outcome is never retried automatically.
func classifyTransportErr(err error) error {
	if err == nil {
		return nil
	}

	// Name resolution failed: no connection was ever made.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &NotSentError{Err: err}
	}

	// Certificate problems: the handshake failed before any request body.
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	var verifyErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostnameErr) ||
		errors.As(err, &certInvalid) || errors.As(err, &verifyErr) ||
		errors.As(err, &recordErr) {
		return &NotSentError{Err: err}
	}

	// The dial itself failed: connection refused, network unreachable.
	// Only Op == "dial" qualifies; a failure on read or write happens after
	// the body may already have been delivered.
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return &NotSentError{Err: err}
	}

	return &UnknownOutcomeError{Err: err}
}

// classifySubmitErr is the name this rule carries at the call site that gives it
// its weight: a form submit, where the classification decides whether a retry is
// allowed at all. The rule itself is the same on every request.
func classifySubmitErr(err error) error { return classifyTransportErr(err) }
