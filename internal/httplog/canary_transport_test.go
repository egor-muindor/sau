package httplog

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// canaries are the values that must never reach the log.
var canaries = []string{
	"CSRF-CANARY-1",
	"USER-CANARY-1",
	"PASS-CANARY-1",
	"COOKIE-CANARY-1",
	"AUTH-CANARY-1",
}

func TestCanaryThroughTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "PHPSESSID=COOKIE-CANARY-1; HttpOnly; Path=/")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `<html><body><form id="login-form" action="/users/login" method="post">`+
			`<input type="hidden" name="csrf" value="CSRF-CANARY-1" />`+
			`<input name="LoginForm[username]" /><input type="password" name="LoginForm[password]" />`+
			`</form></body></html>`)
	}))
	defer srv.Close()

	buf := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Transport: New(http.DefaultTransport, log), Jar: jar}

	// 1. The login form is submitted with all the canaries in it. The body is
	// spelled out rather than built with url.Values: the order of the fields
	// is part of what the site expects.
	body := "csrf=CSRF-CANARY-1&LoginForm%5Busername%5D=USER-CANARY-1" +
		"&LoginForm%5Bpassword%5D=PASS-CANARY-1&yt0=&dynpage=1"

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/users/login", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer AUTH-CANARY-1")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// 2. A second request carries the session cookie back to the server.
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	if len(jar.Cookies(u)) == 0 {
		t.Fatal("the cookie jar is empty: the exchange did not happen as expected")
	}
	resp2, err := client.Get(srv.URL + "/translations/create?seriesId=36866")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()

	out := buf.String()
	if len(out) == 0 {
		t.Fatal("the log is empty: the test would pass for the wrong reason")
	}
	for _, c := range canaries {
		if strings.Contains(out, c) {
			t.Errorf("the log leaks %q:\n%s", c, out)
		}
	}
	// The exchange itself must be there.
	for _, want := range []string{"POST", "GET", "/users/login", "/translations/create", "seriesId=36866", "200"} {
		if !strings.Contains(out, want) {
			t.Errorf("the log lacks %q:\n%s", want, out)
		}
	}
}
