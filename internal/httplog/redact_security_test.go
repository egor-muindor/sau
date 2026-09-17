package httplog

import (
	"net/http"
	"strings"
	"testing"
)

func TestRedactHeadersHidesExtraSecretHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Csrf-Token", "SECRET-XCSRF")
	h.Set("X-Auth-Token", "SECRET-XAUTH")
	h.Set("X-My-Secret-Value", "SECRET-CUSTOM")
	h.Set("X-Password-Reset", "SECRET-PASSWORD")

	got := redactHeaders(h)

	for _, name := range []string{"X-Csrf-Token", "X-Auth-Token", "X-My-Secret-Value", "X-Password-Reset"} {
		if got[name] != redacted {
			t.Errorf("header %s = %q, want %q", name, got[name], redacted)
		}
	}
}

func TestRedactHeadersRedactsRefererQuery(t *testing.T) {
	h := http.Header{}
	h.Set("Referer", "https://example.test/translations/create?seriesId=36866&token=SECRET-REF-TOKEN")

	got := redactHeaders(h)["Referer"]

	if strings.Contains(got, "SECRET-REF-TOKEN") {
		t.Errorf("Referer leaks the query: %s", got)
	}
	if !strings.HasPrefix(got, "https://example.test/translations/create") {
		t.Errorf("Referer = %q, want the path kept", got)
	}
}

func TestRedactCSRFInJSON(t *testing.T) {
	in := `{"csrf":"TOKENVALUE","other":"kept"}`

	got := redactCSRF(in)

	if strings.Contains(got, "TOKENVALUE") {
		t.Errorf("redactCSRF leaks the token: %s", got)
	}
	if !strings.Contains(got, `"csrf":"[redacted]"`) {
		t.Errorf("redactCSRF = %s, want the JSON csrf field redacted", got)
	}
	if !strings.Contains(got, `"other":"kept"`) {
		t.Errorf("redactCSRF = %s, want other fields untouched", got)
	}
}

func TestRedactFormTruncatesAtGivenLimit(t *testing.T) {
	body := []byte("x=" + strings.Repeat("a", 100))

	got := redactForm(body, 50)

	if len(got) > 50+len(truncationMark) {
		t.Errorf("len(redactForm) = %d, want at most %d", len(got), 50+len(truncationMark))
	}
	if !strings.HasSuffix(got, truncationMark) {
		t.Errorf("redactForm does not mark the truncation: %q", got)
	}
}
