package site

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sau/internal/secrets"
	"sau/internal/translation"
)

// newTestServer starts an httptest server on mux and closes it with the test.
func newTestServer(t *testing.T, mux *http.ServeMux) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newTestClient builds a Client over the given mirrors with a no-op Sleep,
// so retry tests never actually wait.
func newTestClient(t *testing.T, mirrors ...string) *Client {
	t.Helper()
	c, err := New(Options{
		Mirrors:   mirrors,
		Transport: http.DefaultTransport,
		Sleep:     func(time.Duration) {},
		UserAgent: "sau-test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// formBody reads the whole request body as a string.
func formBody(t *testing.T, r *http.Request) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Options{Transport: http.DefaultTransport}); err == nil {
		t.Error("New with no mirrors: err = nil, want error")
	}
	if _, err := New(Options{Mirrors: []string{"https://example.org"}}); err == nil {
		t.Error("New with no transport: err = nil, want error")
	}
	c := newTestClient(t, "https://a.example", "https://b.example")
	if c.Mirror() != "https://a.example" {
		t.Errorf("Mirror() = %q, want the first mirror", c.Mirror())
	}
}

func TestLoginSuccess(t *testing.T) {
	var gotBody, gotCT, gotMethod string
	mux := http.NewServeMux()
	mux.HandleFunc("/users/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(mustReadFixture(t, "login_page.html"))
			return
		}
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		gotBody = formBody(t, r)
		http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: "sess-1", Path: "/", HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "aaaa8ed0", Value: "login-1", Path: "/", Expires: time.Now().Add(30 * 24 * time.Hour)})
		w.Header().Set("Location", "/")
		w.WriteHeader(http.StatusFound)
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	if err := c.Login(context.Background(), "user@example.com", secrets.New("s3cr3t pass")); err != nil {
		t.Fatalf("Login: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotCT != "application/x-www-form-urlencoded; charset=UTF-8" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	// The CSRF token comes from the login page fixture, so the body is fully known.
	want := string(EncodeLoginForm(testCSRF, "user@example.com", secrets.New("s3cr3t pass")))
	if gotBody != want {
		t.Errorf("login body mismatch\n got: %s\nwant: %s", gotBody, want)
	}
}

// The login cookie must be stored in the jar, so the next request carries it.
func TestLoginStoresCookies(t *testing.T) {
	var second string
	mux := http.NewServeMux()
	mux.HandleFunc("/users/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write(mustReadFixture(t, "login_page.html"))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: "sess-1", Path: "/", HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "aaaa8ed0", Value: "login-1", Path: "/", Expires: time.Now().Add(30 * 24 * time.Hour)})
		w.Header().Set("Location", "/")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		second = r.Header.Get("Cookie")
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	if err := c.Login(context.Background(), "u", secrets.New("p")); err != nil {
		t.Fatalf("Login: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/probe", nil)
	resp, err := c.httpClient(c.Mirror()).Do(req)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	resp.Body.Close()

	if !strings.Contains(second, "PHPSESSID=sess-1") || !strings.Contains(second, "aaaa8ed0=login-1") {
		t.Errorf("second request cookies = %q, want both login cookies", second)
	}
}

// A wrong password answers 200 with the login page again, never a redirect.
func TestLoginWrongPassword(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(mustReadFixture(t, "login_page.html"))
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	err := c.Login(context.Background(), "u", secrets.New("wrong"))
	if !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("Login error = %v, want ErrNotAuthorized", err)
	}
}

// The client must not follow the 302: a redirect is the success signal itself.
func TestClientDoesNotFollowRedirects(t *testing.T) {
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/users/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write(mustReadFixture(t, "login_page.html"))
			return
		}
		w.Header().Set("Location", "/")
		w.WriteHeader(http.StatusFound)
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	if err := c.Login(context.Background(), "u", secrets.New("p")); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if hits != 0 {
		t.Errorf("redirect target was fetched %d times, want 0", hits)
	}
}

func TestCreateFormSendsChannelCookie(t *testing.T) {
	tests := []struct {
		name string
		ch   translation.Channel
		want string
	}{
		{"all", translation.ChannelAll, "0"},
		{"cdn is the default and is still sent explicitly", translation.ChannelCDN, "2"},
		{"ru", translation.ChannelRU, "3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotCookie string
			mux := http.NewServeMux()
			mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.RequestURI()
				if ck, err := r.Cookie("upload-channel"); err == nil {
					gotCookie = ck.Value
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write(mustReadFixture(t, "create_form.html"))
			})
			srv := newTestServer(t, mux)
			c := newTestClient(t, srv.URL)

			f, err := c.CreateForm(context.Background(), 36866, tt.ch)
			if err != nil {
				t.Fatalf("CreateForm: %v", err)
			}
			if gotPath != "/translations/create?seriesId=36866" {
				t.Errorf("request URI = %q", gotPath)
			}
			if gotCookie != tt.want {
				t.Errorf("upload-channel cookie = %q, want %q", gotCookie, tt.want)
			}
			if f.CSRF != testCSRF {
				t.Errorf("CSRF = %q", f.CSRF)
			}
			if f.Video.ServerID != 28 || f.Video.ServerURL != "https://t-time28.melon-soda.org" {
				t.Errorf("Video = %+v", f.Video)
			}
			if len(f.Sub.AllowedExt) == 0 {
				t.Error("Sub.AllowedExt is empty")
			}
		})
	}
}

func TestCreateFormNotAuthorized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(mustReadFixture(t, "login_page.html"))
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	if _, err := c.CreateForm(context.Background(), 36866, translation.ChannelCDN); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("CreateForm error = %v, want ErrNotAuthorized", err)
	}
}

func TestCreateFormUnusablePage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>nothing</body></html>`))
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	if _, err := c.CreateForm(context.Background(), 36866, translation.ChannelCDN); !errors.Is(err, ErrNoForm) {
		t.Errorf("CreateForm error = %v, want ErrNoForm", err)
	}
}

// testCreateForm is the parsed fixture form, reused by the submit tests.
func testCreateForm(t *testing.T) CreateForm {
	t.Helper()
	p, err := ParsePage(strings.NewReader(string(mustReadFixture(t, "create_form.html"))))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	f, err := p.CreateForm()
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	return f
}

func TestSubmitSuccess(t *testing.T) {
	var gotBody, gotURI, gotCT string
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.URL.RequestURI()
		gotCT = r.Header.Get("Content-Type")
		gotBody = formBody(t, r)
		w.Header().Set("Location", "/translations/update/5995522")
		w.WriteHeader(http.StatusFound)
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	f := testCreateForm(t)
	up := UploadedFields{Video: &UploadedFile{
		Name: "[Team] Title - 01 [1080p].mp4",
		UUID: "580fcb20-5c8c-419c-88d1-5b889298c20f",
		Size: 6531378,
	}}

	orig := newBatchID
	newBatchID = func() string { return "9ea18edd-baaf-485c-8a51-b0df0b0a055c" }
	t.Cleanup(func() { newBatchID = orig })

	res, err := c.Submit(context.Background(), f, testDraft(), translation.ChannelCDN, up)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.TranslationID != 5995522 {
		t.Errorf("TranslationID = %d, want 5995522", res.TranslationID)
	}
	if res.Location != "/translations/update/5995522" {
		t.Errorf("Location = %q", res.Location)
	}
	if gotURI != "/translations/create?seriesId=36866" {
		t.Errorf("request URI = %q", gotURI)
	}
	if gotCT != "application/x-www-form-urlencoded; charset=UTF-8" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if gotBody != wantSubmitBody {
		t.Errorf("submit body mismatch\n got: %s\nwant: %s", gotBody, wantSubmitBody)
	}
}

func TestSubmitRejected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(mustReadFixture(t, "create_form_rejected.html"))
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	_, err := c.Submit(context.Background(), testCreateForm(t), testDraft(), translation.ChannelCDN, UploadedFields{})
	var rej *RejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("Submit error = %v, want *RejectedError", err)
	}
	want := []string{"Номер серии обязателен."}
	if !reflect.DeepEqual(rej.Messages, want) {
		t.Errorf("Messages = %v, want %v", rej.Messages, want)
	}
}

func TestSubmitNotAuthorized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(mustReadFixture(t, "login_page.html"))
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	_, err := c.Submit(context.Background(), testCreateForm(t), testDraft(), translation.ChannelCDN, UploadedFields{})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("Submit error = %v, want ErrNotAuthorized", err)
	}
}

// Without an uploaded subtitle file the subtitle field is an empty string,
// exactly as in the capture, and never an empty JSON object.
func TestSubmitEmptySubtitleField(t *testing.T) {
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		gotBody = formBody(t, r)
		w.Header().Set("Location", "/translations/update/1")
		w.WriteHeader(http.StatusFound)
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	_, err := c.Submit(context.Background(), testCreateForm(t), testDraft(), translation.ChannelCDN, UploadedFields{})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !strings.Contains(gotBody, "&TranslationAdminForm%5BsubFileNew%5D=&qqfile=&yt0=&dynpage=1") {
		t.Errorf("subtitle field is not empty in %s", gotBody)
	}
}

// submitOnce runs a Submit against the given base URL and returns the error.
func submitOnce(t *testing.T, c *Client) error {
	t.Helper()
	_, err := c.Submit(context.Background(), testCreateForm(t), testDraft(), translation.ChannelCDN, UploadedFields{})
	return err
}

// (a) Connection refused: the TCP dial failed, nothing left this machine.
func TestSubmitConnectionRefusedIsNotSent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nobody listens there any more

	c := newTestClient(t, "http://"+addr)
	err = submitOnce(t, c)

	var ns *NotSentError
	if !errors.As(err, &ns) {
		t.Fatalf("err = %v (%T), want *NotSentError", err, err)
	}
}

// (b) Certificate not trusted: the handshake failed, the body never went out.
func TestSubmitTLSFailureIsNotSent(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/translations/update/1")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	// Deliberately do NOT use srv.Client(): the default transport does not
	// trust the test certificate.
	c := newTestClient(t, srv.URL)
	err := submitOnce(t, c)

	var ns *NotSentError
	if !errors.As(err, &ns) {
		t.Fatalf("err = %v (%T), want *NotSentError", err, err)
	}
}

// (c) The server read the whole body and then dropped the connection without
// answering. This is the dangerous case: the site may well have created the
// translation. It must never be retried automatically.
func TestSubmitHijackedConnectionIsUnknown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("ResponseWriter is not a Hijacker")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close() // no response at all
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	err := submitOnce(t, c)

	var un *UnknownOutcomeError
	if !errors.As(err, &un) {
		t.Fatalf("err = %v (%T), want *UnknownOutcomeError", err, err)
	}
	var ns *NotSentError
	if errors.As(err, &ns) {
		t.Fatal("a dropped connection after the body must never be classified as not sent")
	}
}

// (d) Timeout: the request went out, the answer never came.
func TestSubmitTimeoutIsUnknown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		// The body has to be drained before waiting: with unread request data
		// buffered, net/http does not notice the client hanging up, the request
		// context is never cancelled, and the server's Close blocks forever.
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done() // answer only once the client has given up
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)
	c.Timeout = 100 * time.Millisecond

	err := submitOnce(t, c)

	var un *UnknownOutcomeError
	if !errors.As(err, &un) {
		t.Fatalf("err = %v (%T), want *UnknownOutcomeError", err, err)
	}
}

// (e) The host name does not resolve.
func TestSubmitDNSFailureIsNotSent(t *testing.T) {
	// An HTTP proxy resolves host names itself and answers for every name, so
	// the resolver failure this test needs never reaches the client. That is a
	// property of the environment, not of the classification; skip rather than
	// weaken the rule.
	req, _ := http.NewRequest(http.MethodGet, "http://nonexistent.invalid", nil)
	if proxy, err := http.ProxyFromEnvironment(req); err == nil && proxy != nil {
		t.Skip("a proxy is configured for external hosts: name resolution is done by the proxy")
	}

	c := newTestClient(t, "http://nonexistent.invalid")
	err := submitOnce(t, c)

	var ns *NotSentError
	if !errors.As(err, &ns) {
		t.Fatalf("err = %v (%T), want *NotSentError", err, err)
	}
}

// The same rule, checked on the error value itself, so the DNS branch stays
// covered in environments where every name resolves through a proxy.
func TestClassifyTransportErrDNS(t *testing.T) {
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &net.DNSError{Err: "no such host", Name: "nonexistent.invalid", IsNotFound: true},
	}
	var ns *NotSentError
	if got := classifyTransportErr(err); !errors.As(got, &ns) {
		t.Fatalf("classifyTransportErr(dns) = %v (%T), want *NotSentError", got, got)
	}
}

// A 5xx answer is an answer, but it says nothing about whether the site created
// the translation. Per the "everything else" row of the table in
// docs/architecture.md §7 that is an unknown outcome — and the request is sent
// exactly once, because a submit is never retried.
func TestSubmitServerErrorIsUnknownAndNotRetried(t *testing.T) {
	var calls int
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	err := submitOnce(t, c)

	var un *UnknownOutcomeError
	if !errors.As(err, &un) {
		t.Fatalf("err = %v (%T), want *UnknownOutcomeError", err, err)
	}
	if calls != 1 {
		t.Errorf("server was called %d times, want exactly 1: a submit is never retried", calls)
	}
}

func TestClassifyTransportErrNil(t *testing.T) {
	if got := classifyTransportErr(nil); got != nil {
		t.Errorf("classifyTransportErr(nil) = %v, want nil", got)
	}
}

// newCountingClient builds a client that records the Sleep durations instead of
// waiting, so retry backoff can be asserted without slowing the test down.
func newCountingClient(t *testing.T, sleeps *[]time.Duration, mirrors ...string) *Client {
	t.Helper()
	c, err := New(Options{
		Mirrors:   mirrors,
		Transport: http.DefaultTransport,
		Sleep:     func(d time.Duration) { *sleeps = append(*sleeps, d) },
		UserAgent: "sau-test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestGetRetriesOnServerError(t *testing.T) {
	var calls int
	var sleeps []time.Duration
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(mustReadFixture(t, "create_form.html"))
	})
	srv := newTestServer(t, mux)
	c := newCountingClient(t, &sleeps, srv.URL)

	f, err := c.CreateForm(context.Background(), 36866, translation.ChannelCDN)
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	if f.CSRF != testCSRF {
		t.Errorf("CSRF = %q", f.CSRF)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
	if len(sleeps) != 2 {
		t.Fatalf("Sleep called %d times, want 2", len(sleeps))
	}
	if !(sleeps[0] > 0 && sleeps[1] > sleeps[0]) {
		t.Errorf("backoff = %v, want increasing and positive", sleeps)
	}
}

func TestGetGivesUpAfterThreeAttempts(t *testing.T) {
	var calls int
	var sleeps []time.Duration
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := newTestServer(t, mux)
	c := newCountingClient(t, &sleeps, srv.URL)

	if _, err := c.CreateForm(context.Background(), 36866, translation.ChannelCDN); err == nil {
		t.Fatal("CreateForm: err = nil, want an error after the retries are spent")
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

// A submit must go out exactly once even on a retryable status.
func TestSubmitIsNeverRetriedOn503(t *testing.T) {
	var calls int
	var sleeps []time.Duration
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := newTestServer(t, mux)
	c := newCountingClient(t, &sleeps, srv.URL)

	_, err := c.Submit(context.Background(), testCreateForm(t), testDraft(), translation.ChannelCDN, UploadedFields{})
	var un *UnknownOutcomeError
	if !errors.As(err, &un) {
		t.Fatalf("err = %v (%T), want *UnknownOutcomeError", err, err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want exactly 1", calls)
	}
	if len(sleeps) != 0 {
		t.Errorf("Sleep called %d times during a submit, want 0", len(sleeps))
	}
}

func TestAPIRetries(t *testing.T) {
	var calls int
	var sleeps []time.Duration
	mux := http.NewServeMux()
	mux.HandleFunc("/api/series/1", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"data":{"id":1,"titles":{"ru":"t"},"season":"s","year":2026,"numberOfEpisodes":1}}`))
	})
	srv := newTestServer(t, mux)
	c := newCountingClient(t, &sleeps, srv.URL)

	s, err := c.Series(context.Background(), 1)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if s.ID != 1 {
		t.Errorf("Series = %+v", s)
	}
	if calls != 3 || len(sleeps) != 2 {
		t.Errorf("calls = %d, sleeps = %v; want 3 and 2 sleeps", calls, sleeps)
	}
}

// deadMirror returns the URL of an address nobody listens on.
func deadMirror(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "http://" + addr
}

func TestMirrorFailoverOnNotSent(t *testing.T) {
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(mustReadFixture(t, "create_form.html"))
	})
	alive := newTestServer(t, mux)
	dead := deadMirror(t)

	var sleeps []time.Duration
	c := newCountingClient(t, &sleeps, dead, alive.URL)
	if c.Mirror() != dead {
		t.Fatalf("Mirror() = %q, want the first (dead) mirror", c.Mirror())
	}

	f, err := c.CreateForm(context.Background(), 36866, translation.ChannelCDN)
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	if f.CSRF != testCSRF {
		t.Errorf("CSRF = %q", f.CSRF)
	}
	if hits != 1 {
		t.Errorf("second mirror was hit %d times, want 1", hits)
	}
	if c.Mirror() != alive.URL {
		t.Errorf("Mirror() = %q, want the second mirror after failover", c.Mirror())
	}
}

// The session is bound to a domain, so the jar is asked for per mirror host.
func TestJarsCalledPerMirrorHost(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(mustReadFixture(t, "create_form.html"))
	})
	alive := newTestServer(t, mux)
	dead := deadMirror(t)

	var asked []string
	c, err := New(Options{
		Mirrors:   []string{dead, alive.URL},
		Transport: http.DefaultTransport,
		Sleep:     func(time.Duration) {},
		Jars: func(host string) http.CookieJar {
			asked = append(asked, host)
			j, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			return j
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.CreateForm(context.Background(), 36866, translation.ChannelCDN); err != nil {
		t.Fatalf("CreateForm: %v", err)
	}

	aliveHost := strings.TrimPrefix(alive.URL, "http://")
	deadHost := strings.TrimPrefix(dead, "http://")
	want := []string{deadHost, aliveHost}
	if !reflect.DeepEqual(asked, want) {
		t.Errorf("Jars called with %v, want %v", asked, want)
	}
}

// A single mirror must never "fail over" onto itself in a loop.
func TestSingleMirrorDoesNotFailOver(t *testing.T) {
	var sleeps []time.Duration
	dead := deadMirror(t)
	c := newCountingClient(t, &sleeps, dead)

	if _, err := c.CreateForm(context.Background(), 36866, translation.ChannelCDN); err == nil {
		t.Fatal("CreateForm: err = nil, want an error")
	}
	if c.Mirror() != dead {
		t.Errorf("Mirror() = %q, want the only mirror", c.Mirror())
	}
}

// The channel cookie belongs to the mirror the request actually goes to.
// Setting it once before the retry loop loses it on failover, and the site then
// falls back to its own default channel — silently uploading to the wrong hosts.
func TestChannelCookieFollowsMirrorFailover(t *testing.T) {
	var gotCookie string
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		hits++
		if ck, err := r.Cookie("upload-channel"); err == nil {
			gotCookie = ck.Value
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(mustReadFixture(t, "create_form.html"))
	})
	alive := newTestServer(t, mux)
	dead := deadMirror(t)

	var sleeps []time.Duration
	c := newCountingClient(t, &sleeps, dead, alive.URL)

	if _, err := c.CreateForm(context.Background(), 36866, translation.ChannelRU); err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	if hits != 1 {
		t.Fatalf("second mirror was hit %d times, want 1", hits)
	}
	if gotCookie != "3" {
		t.Errorf("upload-channel on the mirror that served the request = %q, want %q", gotCookie, "3")
	}
}

// A 4xx is a final answer, not a page: reporting it as "this is not a create
// form" hides the real reason from the user.
func TestGetReturnsHTTPErrorOnClientError(t *testing.T) {
	var calls int
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	_, err := c.CreateForm(context.Background(), 36866, translation.ChannelCDN)
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v (%T), want *HTTPError", err, err)
	}
	if he.Status != http.StatusForbidden {
		t.Errorf("Status = %d, want 403", he.Status)
	}
	if !strings.Contains(he.URL, "/translations/create") {
		t.Errorf("URL = %q, want the requested path", he.URL)
	}
	// A 403 is not worth repeating: the site said no, it is not busy.
	if calls != 1 {
		t.Errorf("calls = %d, want exactly 1", calls)
	}
}

// Exhausting the retries on a 5xx reports the status too.
func TestGetGivesUpWithHTTPError(t *testing.T) {
	var sleeps []time.Duration
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := newTestServer(t, mux)
	c := newCountingClient(t, &sleeps, srv.URL)

	_, err := c.CreateForm(context.Background(), 36866, translation.ChannelCDN)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusServiceUnavailable {
		t.Fatalf("err = %v (%T), want *HTTPError with 503", err, err)
	}
}

func TestNewNormalizesAndValidatesMirrors(t *testing.T) {
	c, err := New(Options{
		Mirrors:   []string{"https://a.example/", "https://b.example///"},
		Transport: http.DefaultTransport,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Mirror() != "https://a.example" {
		t.Errorf("Mirror() = %q, want the trailing slash trimmed", c.Mirror())
	}

	bad := []string{"", "a.example", "https://", "/translations", "://x"}
	for _, m := range bad {
		if _, err := New(Options{Mirrors: []string{m}, Transport: http.DefaultTransport}); err == nil {
			t.Errorf("New with mirror %q: err = nil, want an error", m)
		}
	}
}

// The runs of a batch share one Client. A failover in one goroutine moves the
// mirror index and adds a per-mirror client while the others read both, so
// without a lock the race detector fires. And when several goroutines report
// the same dead mirror, the client must step past it once, not once per
// report: otherwise the rotation lands back on the dead one and a run burns
// its three attempts there.
func TestCreateFormConcurrentDuringFailover(t *testing.T) {
	page := mustReadFixture(t, "create_form.html")
	var hits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/translations/create", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	})
	alive := newTestServer(t, mux)
	dead := deadMirror(t)
	c := newTestClient(t, dead, alive.URL)

	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := c.CreateForm(context.Background(), 36866, translation.ChannelCDN)
			if err == nil && f.CSRF != testCSRF {
				err = fmt.Errorf("CSRF = %q", f.CSRF)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("CreateForm: %v", err)
		}
	}
	if c.Mirror() != alive.URL {
		t.Errorf("Mirror() = %q, want the alive mirror after failover", c.Mirror())
	}
	if got := hits.Load(); got < n {
		t.Errorf("alive mirror hit %d times, want at least %d", got, n)
	}
}
