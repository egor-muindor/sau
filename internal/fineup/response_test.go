package fineup

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// response builds a fake *http.Response with a body that records whether it was
// closed.
func response(status int, body string) (*http.Response, *closeTracker) {
	tracker := &closeTracker{Reader: strings.NewReader(body)}
	return &http.Response{
		StatusCode: status,
		Body:       tracker,
		Header:     make(http.Header),
	}, tracker
}

type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error {
	c.closed = true
	return nil
}

func TestParseResponse(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantUUID string
		wantErr  bool
	}{
		{
			name:     "chunk accepted",
			status:   200,
			body:     `{"success":true,"uuid":"` + testUUID + `","uploadName":null}`,
			wantUUID: testUUID,
		},
		{
			name:     "finalized",
			status:   200,
			body:     `{"success":true,"uuid":"` + testUUID + `"}`,
			wantUUID: testUUID,
		},
		{
			name:     "deleted",
			status:   200,
			body:     `{"success":true,"uuid":"` + testUUID + `"}`,
			wantUUID: testUUID,
		},
		{
			name:     "success without a uuid",
			status:   200,
			body:     `{"success":true}`,
			wantUUID: "",
		},
		{
			name:     "created",
			status:   201,
			body:     `{"success":true,"uuid":"` + testUUID + `"}`,
			wantUUID: testUUID,
		},
		{name: "success is false", status: 200, body: `{"success":false,"error":"no space left"}`, wantErr: true},
		{name: "success is missing", status: 200, body: `{"uuid":"` + testUUID + `"}`, wantErr: true},
		{name: "empty body", status: 200, body: ``, wantErr: true},
		{name: "html instead of json", status: 200, body: `<html><body>502</body></html>`, wantErr: true},
		{name: "truncated json", status: 200, body: `{"success":tr`, wantErr: true},
		{name: "bad request", status: 400, body: `{"success":false}`, wantErr: true},
		{name: "not found", status: 404, body: `not found`, wantErr: true},
		{name: "internal server error", status: 500, body: `<html>oops</html>`, wantErr: true},
		{name: "bad gateway", status: 502, body: ``, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, tracker := response(tt.status, tt.body)
			uuid, err := parseResponse(resp)

			if !tracker.closed {
				t.Fatal("parseResponse did not close the body")
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseResponse returned no error, want one")
				}
				var se *ServerError
				if !errors.As(err, &se) {
					t.Fatalf("parseResponse returned %T (%v), want a *ServerError", err, err)
				}
				if se.Status != tt.status {
					t.Fatalf("ServerError.Status = %d, want %d", se.Status, tt.status)
				}
				if se.Body != tt.body {
					t.Fatalf("ServerError.Body = %q, want %q", se.Body, tt.body)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseResponse returned error %v, want none", err)
			}
			if uuid != tt.wantUUID {
				t.Fatalf("parseResponse returned uuid %q, want %q", uuid, tt.wantUUID)
			}
		})
	}
}

func TestServerErrorMessage(t *testing.T) {
	err := &ServerError{Status: 503, Body: "service unavailable"}
	msg := err.Error()
	if !strings.Contains(msg, "503") {
		t.Fatalf("Error() = %q, want it to mention the status", msg)
	}
	if !strings.Contains(msg, "service unavailable") {
		t.Fatalf("Error() = %q, want it to mention the body", msg)
	}
}

func TestParseResponseTruncatesAHugeBody(t *testing.T) {
	huge := strings.Repeat("x", maxResponseBody*3)
	resp, _ := response(500, huge)

	_, err := parseResponse(resp)
	var se *ServerError
	if !errors.As(err, &se) {
		t.Fatalf("parseResponse returned %T (%v), want a *ServerError", err, err)
	}
	if len(se.Body) > maxResponseBody {
		t.Fatalf("ServerError.Body is %d bytes, want at most %d", len(se.Body), maxResponseBody)
	}
}
