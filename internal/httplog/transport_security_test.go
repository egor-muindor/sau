package httplog

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestTransportRedactsBeforeTruncatingResponseBody reproduces the critical
// leak: if the raw body is truncated to MaxBody before the CSRF regex runs,
// a cut that lands inside value="..." leaves the regex nothing to match and
// the token prefix reaches the log verbatim.
func TestTransportRedactsBeforeTruncatingResponseBody(t *testing.T) {
	log, buf := newLogger()
	// The token starts well before MaxBody and ends well after it, so any
	// naive "truncate first" implementation slices right through it.
	page := `<html><body><input type="hidden" name="csrf" value="TOKENVALUE-LONG-SECRET-0123456789" /></body></html>`
	tr := New(&stubRT{resp: response(200, "text/html", page)}, log)
	tr.MaxBody = strings.Index(page, "TOKENVALUE") + 10 // cut lands mid-token

	req, err := http.NewRequest(http.MethodGet, "https://example.test/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "TOKENVALUE") {
		t.Errorf("log leaks the token when the cut lands mid-value:\n%s", out)
	}
	if !strings.Contains(out, truncationMark) {
		t.Errorf("log does not mark the truncation:\n%s", out)
	}
}

// TestTransportLogsRedactedURLWithUserinfo checks that a URL carrying
// credentials in its userinfo component never reaches the log verbatim.
func TestTransportLogsRedactedURLWithUserinfo(t *testing.T) {
	log, buf := newLogger()
	tr := New(&stubRT{resp: response(200, "text/html", "<html></html>")}, log)

	req, err := http.NewRequest(http.MethodGet, "https://USER-CANARY-1:PASS-CANARY-1@example.test/path", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "PASS-CANARY-1") {
		t.Errorf("log leaks the userinfo password:\n%s", out)
	}
}

// urlErrorRT wraps a *url.Error around the returned error the way the real
// net/http machinery does for dial and TLS failures with a URL.
type urlErrorRT struct {
	err error
}

func (u *urlErrorRT) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, &url.Error{Op: "Get", URL: r.URL.String(), Err: u.err}
}

func TestTransportLogsRedactedURLOnTransportError(t *testing.T) {
	log, buf := newLogger()
	boom := errors.New("connection refused")
	tr := New(&urlErrorRT{err: boom}, log)

	req, err := http.NewRequest(http.MethodGet, "https://USER-CANARY-1:PASS-CANARY-1@example.test/path", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("RoundTrip: want an error")
	}

	out := buf.String()
	if strings.Contains(out, "PASS-CANARY-1") {
		t.Errorf("log leaks the userinfo password in the transport error:\n%s", out)
	}
	if !strings.Contains(out, "connection refused") {
		t.Errorf("log lacks the underlying error:\n%s", out)
	}
}

// TestTransportBodyBytesReflectsActualFormBodyLength makes sure the logged
// size of a form body is not silently the truncated preview length.
func TestTransportBodyBytesReflectsActualFormBodyLength(t *testing.T) {
	log, buf := newLogger()
	stub := &stubRT{resp: response(200, "text/html", "")}
	tr := New(stub, log)
	tr.MaxBody = 50

	body := "x=" + strings.Repeat("a", 49) // 51 bytes total, over MaxBody
	req, err := http.NewRequest(http.MethodPost, "https://example.test/submit", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "body_bytes=51") {
		t.Errorf("log does not report the real body length of 51:\n%s", out)
	}
}
