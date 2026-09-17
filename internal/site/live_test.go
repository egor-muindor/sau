//go:build live

// WARNING: this test talks to the live site with the user's real account.
//
// It logs in, opens a real upload form, uploads a small file to the real upload
// servers and then deletes it. It NEVER submits the form: a submitted form
// creates a public translation that cannot be taken back cleanly, and the
// public API does not confirm the submission for several minutes, so a failed
// run here would leave a human decision behind.
//
// The test is excluded from every ordinary run by the "live" build tag. It is
// run by hand, by the account owner, and by nobody else:
//
//	SAU_LIVE_USER=... account name
//	SAU_PASSWORD=... password for that account
//	SAU_LIVE_SERIES=... series id to open the form for
//	SAU_LIVE_FILE=... path to a small file to upload
//	go test -tags live -run TestLive ./internal/site/ -v
//
// Keep SAU_LIVE_FILE small: a few hundred kilobytes is enough to exercise the
// chunking, the finalisation and the delete.
package site

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"sau/internal/fineup"
	"sau/internal/httplog"
	"sau/internal/secrets"
	"sau/internal/translation"
)

func liveEnv(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skipf("%s is not set", name)
	}
	return v
}

func TestLiveUploadAndDelete(t *testing.T) {
	user := liveEnv(t, "SAU_LIVE_USER")
	pass := liveEnv(t, "SAU_PASSWORD")
	seriesID := liveEnv(t, "SAU_LIVE_SERIES")
	path := liveEnv(t, "SAU_LIVE_FILE")

	id, err := strconv.Atoi(seriesID)
	if err != nil {
		t.Fatalf("SAU_LIVE_SERIES is not a number: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("SAU_LIVE_FILE: %v", err)
	}
	if info.Size() > 5<<20 {
		t.Fatalf("SAU_LIVE_FILE is %d bytes; use something small", info.Size())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Every HTTP exchange in this test goes through the same redacting
	// transport the tool uses in production, so the password, the CSRF token
	// and the cookie never reach t.Log by accident.
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	tr := httplog.New(http.DefaultTransport, logger)

	client, err := New(Options{
		Mirrors:   []string{"https://smotret-anime.online"},
		Transport: tr,
		UserAgent: "sau-live-contract-test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := client.Login(ctx, user, secrets.New(pass)); err != nil {
		t.Fatalf("Login: %v", err)
	}

	// The upload servers must be fetched fresh from the form page, never
	// cached: the site rotates the pool per channel (docs/architecture.md,
	// invariant on upload-server caching).
	form, err := client.CreateForm(ctx, id, translation.DefaultChannel)
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	if form.CSRF == "" {
		t.Fatal("CreateForm returned an empty CSRF token")
	}
	if len(form.Video.ServerURLs) == 0 {
		t.Fatal("CreateForm returned no upload servers")
	}
	t.Logf("serverId=%d servers=%v allowed=%v",
		form.Video.ServerID, form.Video.ServerURLs, form.Video.AllowedExt)

	up := fineup.New(&http.Client{Transport: tr})
	spec := fineup.Spec{
		Path:     path,
		Base:     fineup.Endpoint(form.Video.ServerURL),
		Pool:     form.Video.ServerURLs,
		PartSize: fineup.DefaultPartSize,
		MaxConns: 2,
	}

	res, err := up.Upload(ctx, spec, func(e fineup.Event) {
		t.Logf("event kind=%d part=%d host=%s attempt=%d err=%v",
			e.Kind, e.Part, e.Host, e.Attempt, e.Err)
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	t.Logf("uploaded uuid=%s parts=%d endpoint=%s lastHost=%s",
		res.UUID, res.Parts, res.Endpoint, res.LastHost)

	if res.Size != info.Size() {
		t.Errorf("uploaded size = %d, want %d", res.Size, info.Size())
	}

	// Always clean up, even if the checks above failed: an orphan upload keeps
	// occupying the server until it prunes it. Delete must be addressed by
	// LastHost, not Endpoint: the channel that served the chunks may not be
	// the one named by the server id alone.
	if err := up.Delete(ctx, res.LastHost, res.UUID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	t.Log("deleted")

	// No Submit call anywhere in this file. Keep it that way.
}

// testWriter adapts testing.T.Log to io.Writer for the slog text handler.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
