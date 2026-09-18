package httplog

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// DefaultMaxBody is how much of a textual body goes into the log.
const DefaultMaxBody = 4096

// Transport logs every request and response it carries, through the redactor.
// It is the single logging point of both transports of the tool.
type Transport struct {
	Base    http.RoundTripper
	Log     *slog.Logger
	MaxBody int
}

// New wraps base with a logging transport.
func New(base http.RoundTripper, log *slog.Logger) *Transport {
	return &Transport{Base: base, Log: log, MaxBody: DefaultMaxBody}
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *Transport) maxBody() int {
	if t.MaxBody > 0 {
		return t.MaxBody
	}
	return DefaultMaxBody
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.Log == nil {
		return t.base().RoundTrip(r)
	}

	t.logRequest(r)

	resp, err := t.base().RoundTrip(r)
	if err != nil {
		t.Log.Debug("http error",
			slog.String("method", r.Method),
			slog.String("url", r.URL.Redacted()),
			slog.String("err", redactErr(err)))
		return nil, err
	}

	t.logResponse(r, resp)
	return resp, nil
}

// redactErr turns a transport error into a loggable message with any
// userinfo in its URL hidden. net/http wraps dial and TLS failures in
// *url.Error, whose URL field is a plain string that may still carry
// credentials verbatim.
func redactErr(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		target := uerr.URL
		if parsed, perr := url.Parse(uerr.URL); perr == nil {
			target = parsed.Redacted()
		}
		return uerr.Op + " " + target + ": " + uerr.Err.Error()
	}
	return err.Error()
}

func (t *Transport) logRequest(r *http.Request) {
	attrs := []any{
		slog.String("method", r.Method),
		slog.String("url", r.URL.Redacted()),
		slog.Any("headers", redactHeaders(r.Header)),
	}

	if isFormContentType(r.Header.Get("Content-Type")) {
		body, err := peekFormBody(r)
		if err != nil {
			attrs = append(attrs, slog.String("body_err", err.Error()))
		} else {
			attrs = append(attrs,
				slog.Int64("body_bytes", int64(len(body))),
				slog.String("body", redactForm(body, t.maxBody())))
		}
	} else {
		// Chunk uploads are megabytes of multipart; only their size is logged.
		attrs = append(attrs, slog.Int64("body_bytes", r.ContentLength))
	}

	t.Log.Debug("http request", attrs...)
}

func (t *Transport) logResponse(r *http.Request, resp *http.Response) {
	// Responses here are pages and small JSON answers; the tool never
	// downloads a large body, so reading it whole is safe and keeps the
	// restored body exact.
	var data []byte
	if resp.Body != nil {
		var err error
		data, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(data))
		if err != nil {
			t.Log.Debug("http response",
				slog.String("method", r.Method),
				slog.String("url", r.URL.Redacted()),
				slog.Int("status", resp.StatusCode),
				slog.Any("headers", redactHeaders(resp.Header)),
				slog.String("body_err", err.Error()))
			return
		}
	}

	attrs := []any{
		slog.String("method", r.Method),
		slog.String("url", r.URL.Redacted()),
		slog.Int("status", resp.StatusCode),
		slog.Any("headers", redactHeaders(resp.Header)),
		slog.Int64("body_bytes", int64(len(data))),
	}
	if isTextualContentType(resp.Header.Get("Content-Type")) && len(data) > 0 {
		// Redact the whole body first, then truncate the already-redacted
		// text. Doing it the other way around lets a cut land inside a
		// value="..." or "csrf":"..." and leave a half-redacted secret in
		// the log, because the regex has nothing left to match.
		attrs = append(attrs, slog.String("body", truncate(redactCSRF(string(data)), t.maxBody())))
	}

	t.Log.Debug("http response", attrs...)
}

// peekFormBody returns the request body in full, without consuming it for the
// underlying transport. Form bodies (login, submit) are always small, so
// reading them whole keeps body_bytes accurate; the log preview is truncated
// separately by redactForm.
func peekFormBody(r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	if r.GetBody != nil {
		rc, err := r.GetBody()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	data, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return data, nil
}

func isFormContentType(ct string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "application/x-www-form-urlencoded")
}

func isTextualContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	return strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "application/json")
}
