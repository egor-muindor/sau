package httplog

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// stubRT is a RoundTripper that returns a canned response or error and
// remembers whether the request body reached it intact.
type stubRT struct {
	resp    *http.Response
	err     error
	gotBody string
	calls   int
}

func (s *stubRT) RoundTrip(r *http.Request) (*http.Response, error) {
	s.calls++
	if r.Body != nil {
		data, _ := io.ReadAll(r.Body)
		s.gotBody = string(data)
		_ = r.Body.Close()
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func newLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func response(status int, contentType, body string) *http.Response {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestNewSetsDefaultMaxBody(t *testing.T) {
	log, _ := newLogger()
	tr := New(http.DefaultTransport, log)

	if tr.MaxBody != DefaultMaxBody {
		t.Errorf("MaxBody = %d, want %d", tr.MaxBody, DefaultMaxBody)
	}
	if tr.Base == nil || tr.Log != log {
		t.Errorf("New did not wire Base and Log")
	}
}

func TestTransportLogsFormRequestRedacted(t *testing.T) {
	log, buf := newLogger()
	stub := &stubRT{resp: response(302, "text/html", "")}
	tr := New(stub, log)

	body := "csrf=TOKENVALUE&LoginForm%5Busername%5D=me&LoginForm%5Bpassword%5D=SECRET-PASS&yt0=&dynpage=1"
	req, err := http.NewRequest(http.MethodPost, "https://example.test/users/login", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", "PHPSESSID=SECRET-COOKIE")

	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	out := buf.String()
	for _, leaked := range []string{"SECRET-PASS", "SECRET-COOKIE", "TOKENVALUE"} {
		if strings.Contains(out, leaked) {
			t.Errorf("log leaks %q:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, "POST") || !strings.Contains(out, "/users/login") {
		t.Errorf("log lacks the method or the URL:\n%s", out)
	}
	if !strings.Contains(out, redacted) {
		t.Errorf("log lacks the redaction mark:\n%s", out)
	}
	// The body must still reach the underlying transport.
	if stub.gotBody != body {
		t.Errorf("underlying body = %q, want it intact", stub.gotBody)
	}
}

func TestTransportDoesNotReadNonFormBodies(t *testing.T) {
	log, buf := newLogger()
	stub := &stubRT{resp: response(200, "application/json", `{"success":true}`)}
	tr := New(stub, log)

	chunk := strings.Repeat("BINARY-CANARY", 100)
	req, err := http.NewRequest(http.MethodPost, "https://upload.test/upload.php", strings.NewReader(chunk))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary=abc")

	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "BINARY-CANARY") {
		t.Errorf("log contains a multipart body:\n%s", out)
	}
	if !strings.Contains(out, "body_bytes") {
		t.Errorf("log lacks body_bytes:\n%s", out)
	}
	if stub.gotBody != chunk {
		t.Error("multipart body did not reach the underlying transport intact")
	}
}

func TestTransportLogsResponseAndRestoresBody(t *testing.T) {
	log, buf := newLogger()
	page := `<html><body><input type="hidden" name="csrf" value="TOKENVALUE" /><p>hello</p></body></html>`
	resp := response(200, "text/html; charset=utf-8", page)
	resp.Header.Set("Set-Cookie", "identity=SECRET-IDENTITY; HttpOnly")
	tr := New(&stubRT{resp: resp}, log)

	req, err := http.NewRequest(http.MethodGet, "https://example.test/translations/create?seriesId=36866", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	got, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	data, err := io.ReadAll(got.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != page {
		t.Errorf("response body = %q, want it restored intact", data)
	}

	out := buf.String()
	for _, leaked := range []string{"TOKENVALUE", "SECRET-IDENTITY"} {
		if strings.Contains(out, leaked) {
			t.Errorf("log leaks %q:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, "200") {
		t.Errorf("log lacks the status:\n%s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("log lacks the HTML body preview:\n%s", out)
	}
	if !strings.Contains(out, "seriesId=36866") {
		t.Errorf("log lacks the query:\n%s", out)
	}
}

func TestTransportTruncatesLongResponseBody(t *testing.T) {
	log, buf := newLogger()
	tr := New(&stubRT{resp: response(200, "text/html", strings.Repeat("a", 5000))}, log)
	tr.MaxBody = 100

	req, err := http.NewRequest(http.MethodGet, "https://example.test/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if !strings.Contains(buf.String(), truncationMark) {
		t.Errorf("log does not mark the truncation:\n%s", buf.String())
	}
	if strings.Count(buf.String(), "a") > 1000 {
		t.Error("log contains the whole body, want it truncated")
	}
}

func TestTransportLogsBinaryResponseBySizeOnly(t *testing.T) {
	log, buf := newLogger()
	tr := New(&stubRT{resp: response(200, "application/octet-stream", "BINARY-CANARY-BODY")}, log)

	req, err := http.NewRequest(http.MethodGet, "https://example.test/file", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if strings.Contains(buf.String(), "BINARY-CANARY-BODY") {
		t.Errorf("log contains a binary body:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "body_bytes") {
		t.Errorf("log lacks body_bytes:\n%s", buf.String())
	}
}

func TestTransportLogsTransportError(t *testing.T) {
	log, buf := newLogger()
	boom := errors.New("dial tcp: connection refused")
	tr := New(&stubRT{err: boom}, log)

	req, err := http.NewRequest(http.MethodGet, "https://example.test/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	if _, err := tr.RoundTrip(req); !errors.Is(err, boom) {
		t.Fatalf("RoundTrip err = %v, want boom", err)
	}
	if !strings.Contains(buf.String(), "connection refused") {
		t.Errorf("log lacks the transport error:\n%s", buf.String())
	}
}

func TestTransportWithoutLoggerIsTransparent(t *testing.T) {
	stub := &stubRT{resp: response(200, "text/html", "<html></html>")}
	tr := &Transport{Base: stub}

	req, err := http.NewRequest(http.MethodGet, "https://example.test/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if stub.calls != 1 {
		t.Errorf("underlying calls = %d, want 1", stub.calls)
	}
}

var _ http.RoundTripper = (*Transport)(nil)
