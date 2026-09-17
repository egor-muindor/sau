package httplog_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"sau/internal/httplog"
	"sau/internal/secrets"
	"sau/internal/site"
	"sau/internal/translation"
)

var clientCanaries = []string{
	"CSRF-CANARY-1",
	"USER-CANARY-1",
	"PASS-CANARY-1",
	"COOKIE-CANARY-1",
	"IDENTITY-CANARY-1",
}

// testCSRFRe matches the placeholder token of the fixtures, whatever the
// attribute order around it.
var testCSRFRe = regexp.MustCompile(`TESTCSRF[A-Za-z0-9+/=]*`)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "site", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return testCSRFRe.ReplaceAllString(string(data), "CSRF-CANARY-1")
}

func TestCanaryThroughSiteClient(t *testing.T) {
	loginPage := fixture(t, "login_page.html")
	createForm := fixture(t, "create_form.html")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/users/login"):
			w.Header().Add("Set-Cookie", "PHPSESSID=COOKIE-CANARY-1; HttpOnly; Path=/")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(loginPage))

		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/users/login"):
			w.Header().Add("Set-Cookie", "PHPSESSID=COOKIE-CANARY-1; HttpOnly; Path=/")
			w.Header().Add("Set-Cookie", "aaaa8ed0=IDENTITY-CANARY-1; HttpOnly; Path=/")
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusFound)

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/translations/create"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(createForm))

		case r.Method == http.MethodPost:
			// Any other POST is the form submission.
			w.Header().Set("Location", "/translations/update/5995522")
			w.WriteHeader(http.StatusFound)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	buf := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c, err := site.New(site.Options{
		Mirrors:   []string{srv.URL},
		Transport: httplog.New(http.DefaultTransport, log),
		UserAgent: "sau-test/1.0",
	})
	if err != nil {
		t.Fatalf("site.New: %v", err)
	}

	ctx := context.Background()

	if err := c.Login(ctx, "USER-CANARY-1", secrets.New("PASS-CANARY-1")); err != nil {
		t.Fatalf("Login: %v", err)
	}

	form, err := c.CreateForm(ctx, 36866, translation.ChannelCDN)
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}

	draft := translation.Draft{
		SeriesID:      36866,
		EpisodeNumber: "2",
		EpisodeType:   translation.TV,
		Type:          translation.VoiceRu,
		Authors:       "Team (Alice, Bob)",
		VideoPath:     "/tmp/[Team] Title - 02 [1080p].mp4",
	}
	up := site.UploadedFields{
		Video: &site.UploadedFile{
			Name: "[Team] Title - 02 [1080p].mp4",
			UUID: "11111111-2222-3333-4444-555555555555",
			Size: 1_500_000_000,
		},
	}

	if _, err := c.Submit(ctx, form, draft, translation.ChannelCDN, up); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	out := buf.String()
	if len(out) == 0 {
		t.Fatal("the log is empty: the test would pass for the wrong reason")
	}
	for _, canary := range clientCanaries {
		if strings.Contains(out, canary) {
			t.Errorf("the log leaks %q:\n%s", canary, out)
		}
	}
	for _, want := range []string{"POST", "GET", "/users/login", "/translations/create"} {
		if !strings.Contains(out, want) {
			t.Errorf("the log lacks %q:\n%s", want, out)
		}
	}
}
