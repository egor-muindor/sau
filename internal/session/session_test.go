package session_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"sau/internal/session"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

func cookieNames(cs []*http.Cookie) []string {
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return names
}

func TestJarPersistsLongLivedAndSessionCookies(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	store := session.Store{Dir: dir}

	j, err := store.Jar("example.test")
	if err != nil {
		t.Fatalf("Jar: %v", err)
	}
	u := mustURL(t, "https://example.test/")
	j.SetCookies(u, []*http.Cookie{
		{Name: "identity", Value: "IDENTITY-VALUE", Path: "/", Expires: time.Now().Add(30 * 24 * time.Hour)},
		{Name: "PHPSESSID", Value: "SESSION-VALUE", Path: "/"},
	})

	if err := j.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	reloaded, err := store.Jar("example.test")
	if err != nil {
		t.Fatalf("Jar again: %v", err)
	}
	got := cookieNames(reloaded.Cookies(u))
	want := []string{"PHPSESSID", "identity"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("reloaded cookies = %v, want %v", got, want)
	}
	for _, c := range reloaded.Cookies(u) {
		switch c.Name {
		case "identity":
			if c.Value != "IDENTITY-VALUE" {
				t.Errorf("identity value = %q, want IDENTITY-VALUE", c.Value)
			}
		case "PHPSESSID":
			if c.Value != "SESSION-VALUE" {
				t.Errorf("PHPSESSID value = %q, want SESSION-VALUE", c.Value)
			}
		}
	}
}

func TestJarDoesNotPersistExpiredCookies(t *testing.T) {
	store := session.Store{Dir: filepath.Join(t.TempDir(), "sessions")}
	j, err := store.Jar("example.test")
	if err != nil {
		t.Fatalf("Jar: %v", err)
	}
	u := mustURL(t, "https://example.test/")
	j.SetCookies(u, []*http.Cookie{
		{Name: "fresh", Value: "F", Path: "/", Expires: time.Now().Add(time.Hour)},
		{Name: "stale", Value: "S", Path: "/", Expires: time.Now().Add(-time.Hour)},
	})
	if err := j.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	reloaded, err := store.Jar("example.test")
	if err != nil {
		t.Fatalf("Jar again: %v", err)
	}
	for _, c := range reloaded.Cookies(u) {
		if c.Name == "stale" {
			t.Fatal("expired cookie was persisted")
		}
	}
	if len(reloaded.Cookies(u)) != 1 {
		t.Errorf("reloaded cookies = %v, want just [fresh]", cookieNames(reloaded.Cookies(u)))
	}
}

func TestJarFileIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	store := session.Store{Dir: dir}
	j, err := store.Jar("example.test")
	if err != nil {
		t.Fatalf("Jar: %v", err)
	}
	j.SetCookies(mustURL(t, "https://example.test/"), []*http.Cookie{
		{Name: "identity", Value: "IDENTITY-VALUE", Path: "/"},
	})
	if err := j.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	fi, err := os.Stat(filepath.Join(dir, "example.test.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtimeAllowsModeCheck() && fi.Mode().Perm() != 0o600 {
		t.Errorf("session file mode = %#o, want 0600", fi.Mode().Perm())
	}
}

// The mirror may carry a port; a colon is not portable in a file name.
func TestJarFileNameReplacesColon(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	store := session.Store{Dir: dir}
	j, err := store.Jar("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("Jar: %v", err)
	}
	j.SetCookies(mustURL(t, "https://127.0.0.1:8080/"), []*http.Cookie{
		{Name: "identity", Value: "IDENTITY-VALUE", Path: "/"},
	})
	if err := j.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "127.0.0.1_8080.json")); err != nil {
		t.Errorf("Stat: %v, want the file named 127.0.0.1_8080.json", err)
	}
}

func TestJarOnMissingFileIsEmpty(t *testing.T) {
	store := session.Store{Dir: filepath.Join(t.TempDir(), "sessions")}
	j, err := store.Jar("example.test")
	if err != nil {
		t.Fatalf("Jar: %v", err)
	}
	if got := j.Cookies(mustURL(t, "https://example.test/")); len(got) != 0 {
		t.Errorf("cookies = %v, want none", cookieNames(got))
	}
}

func TestJarRejectsCorruptFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "example.test.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store := session.Store{Dir: dir}
	if _, err := store.Jar("example.test"); err == nil {
		t.Error("Jar on a corrupt file = nil error, want an error")
	}
}

// Jar must satisfy http.CookieJar so it can be handed to an http.Client.
var _ http.CookieJar = (*session.Jar)(nil)

// A host-only cookie (no Domain attribute) must not turn into a domain
// cookie after a round trip through disk: it must not be sent to subdomains,
// while an explicit domain cookie must be.
func TestJarPreservesHostOnlyAcrossPersist(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	store := session.Store{Dir: dir}
	j, err := store.Jar("example.test")
	if err != nil {
		t.Fatalf("Jar: %v", err)
	}
	u := mustURL(t, "https://example.test/")
	j.SetCookies(u, []*http.Cookie{
		{Name: "hostonly", Value: "H", Path: "/"},
		{Name: "domained", Value: "D", Path: "/", Domain: "example.test"},
	})
	if err := j.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	reloaded, err := store.Jar("example.test")
	if err != nil {
		t.Fatalf("Jar again: %v", err)
	}

	sub := mustURL(t, "https://sub.example.test/")
	subNames := cookieNames(reloaded.Cookies(sub))
	for _, name := range subNames {
		if name == "hostonly" {
			t.Errorf("host-only cookie was sent to a subdomain: %v", subNames)
		}
	}
	found := false
	for _, name := range subNames {
		if name == "domained" {
			found = true
		}
	}
	if !found {
		t.Errorf("domain cookie was not sent to a subdomain: %v", subNames)
	}
}

// A cookie scoped to a sub-path must survive Persist/Load and still be sent
// on requests to that sub-path: acceptance must be checked against the
// cookie's own path, not the root URL used to set it.
func TestJarPersistsCookieWithNonRootPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	store := session.Store{Dir: dir}
	j, err := store.Jar("example.test")
	if err != nil {
		t.Fatalf("Jar: %v", err)
	}
	j.SetCookies(mustURL(t, "https://example.test/"), []*http.Cookie{
		{Name: "upload", Value: "U", Path: "/upload"},
	})
	if err := j.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	reloaded, err := store.Jar("example.test")
	if err != nil {
		t.Fatalf("Jar again: %v", err)
	}
	got := cookieNames(reloaded.Cookies(mustURL(t, "https://example.test/upload")))
	if len(got) != 1 || got[0] != "upload" {
		t.Errorf("cookies on /upload = %v, want [upload]", got)
	}
}

// The internal jar may reject a cookie (foreign domain, __Host- prefix
// mismatch, ...). Persist must not resurrect what the standard jar refused.
func TestJarDoesNotPersistCookiesRejectedByInnerJar(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	store := session.Store{Dir: dir}
	j, err := store.Jar("example.test")
	if err != nil {
		t.Fatalf("Jar: %v", err)
	}
	u := mustURL(t, "https://example.test/")
	j.SetCookies(u, []*http.Cookie{
		{Name: "foreign", Value: "F", Path: "/", Domain: "not-example.test"},
		{Name: "ok", Value: "OK", Path: "/"},
	})
	if err := j.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "example.test.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), "foreign") {
		t.Errorf("rejected cookie was persisted:\n%s", data)
	}
	if !strings.Contains(string(data), "\"ok\"") {
		t.Errorf("accepted cookie was not persisted:\n%s", data)
	}
}
