package httplog

import (
	"net/http"
	"strings"
	"testing"
)

func TestRedactHeadersHidesCredentials(t *testing.T) {
	h := http.Header{}
	h.Set("Cookie", "PHPSESSID=SECRET-COOKIE")
	h.Set("Authorization", "Bearer SECRET-TOKEN")
	h.Set("Set-Cookie", "identity=SECRET-IDENTITY; HttpOnly")
	h.Set("Proxy-Authorization", "Basic SECRET-BASIC")
	h.Set("Www-Authenticate", "Basic realm=SECRET-REALM")
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("User-Agent", "sau/1.0")

	got := redactHeaders(h)

	for _, name := range []string{"Cookie", "Authorization", "Set-Cookie", "Proxy-Authorization", "Www-Authenticate"} {
		if got[name] != redacted {
			t.Errorf("header %s = %q, want %q", name, got[name], redacted)
		}
	}
	if got["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want it kept as is", got["Content-Type"])
	}
	if got["User-Agent"] != "sau/1.0" {
		t.Errorf("User-Agent = %q, want it kept as is", got["User-Agent"])
	}
}

func TestRedactHeadersJoinsRepeatedValues(t *testing.T) {
	h := http.Header{}
	h.Add("Accept", "text/html")
	h.Add("Accept", "application/json")

	if got := redactHeaders(h)["Accept"]; got != "text/html, application/json" {
		t.Errorf("Accept = %q, want the values joined", got)
	}
}

func TestRedactFormHidesLoginFieldsAndCsrf(t *testing.T) {
	body := []byte("csrf=TOKENVALUE%3D%3D&LoginForm%5Busername%5D=me%40example.test" +
		"&LoginForm%5Bpassword%5D=SECRET-PASS&yt0=&dynpage=1")

	got := redactForm(body, DefaultMaxBody)

	for _, leaked := range []string{"TOKENVALUE", "me%40example.test", "SECRET-PASS"} {
		if strings.Contains(got, leaked) {
			t.Errorf("redactForm leaks %q: %s", leaked, got)
		}
	}
	want := "csrf=[redacted]&LoginForm%5Busername%5D=[redacted]" +
		"&LoginForm%5Bpassword%5D=[redacted]&yt0=&dynpage=1"
	if got != want {
		t.Errorf("redactForm =\n%s\nwant\n%s", got, want)
	}
}

func TestRedactFormKeepsOrderAndRepeatedKeys(t *testing.T) {
	body := []byte("csrf=T&TranslationAdminForm%5BseriesIdNew%5D=36866" +
		"&TranslationAdminForm%5BepisodeNumberNew%5D=2&upload-channel=2" +
		"&TranslationAdminForm%5BvideoFileNew%5D=%7B%7D&qqfile=" +
		"&TranslationAdminForm%5BsubFileNew%5D=&qqfile=&yt0=&dynpage=1")

	got := redactForm(body, DefaultMaxBody)

	want := "csrf=[redacted]&TranslationAdminForm%5BseriesIdNew%5D=36866" +
		"&TranslationAdminForm%5BepisodeNumberNew%5D=2&upload-channel=2" +
		"&TranslationAdminForm%5BvideoFileNew%5D=%7B%7D&qqfile=" +
		"&TranslationAdminForm%5BsubFileNew%5D=&qqfile=&yt0=&dynpage=1"
	if got != want {
		t.Errorf("redactForm =\n%s\nwant\n%s", got, want)
	}
	if strings.Count(got, "qqfile=") != 2 {
		t.Errorf("redactForm dropped a repeated key: %s", got)
	}
}

func TestRedactFormHandlesUnescapedBrackets(t *testing.T) {
	got := redactForm([]byte("LoginForm[password]=SECRET-PASS&x=1"), DefaultMaxBody)

	if strings.Contains(got, "SECRET-PASS") {
		t.Errorf("redactForm leaks the password: %s", got)
	}
	if got != "LoginForm[password]=[redacted]&x=1" {
		t.Errorf("redactForm = %s", got)
	}
}

func TestRedactFormTruncatesLongBodies(t *testing.T) {
	body := []byte("x=" + strings.Repeat("a", DefaultMaxBody+100))

	got := redactForm(body, DefaultMaxBody)

	if len(got) > DefaultMaxBody+len(truncationMark) {
		t.Errorf("len(redactForm) = %d, want at most %d", len(got), DefaultMaxBody+len(truncationMark))
	}
	if !strings.HasSuffix(got, truncationMark) {
		t.Errorf("redactForm does not mark the truncation: %q", got[len(got)-20:])
	}
}

func TestRedactCSRFInHTML(t *testing.T) {
	cases := []string{
		`<input type="hidden" name="csrf" value="TOKENVALUE" />`,
		`<input type="hidden" value="TOKENVALUE" name="csrf" />`,
		`<input type='hidden' name='csrf' value='TOKENVALUE' />`,
	}
	for _, in := range cases {
		got := redactCSRF(in)
		if strings.Contains(got, "TOKENVALUE") {
			t.Errorf("redactCSRF leaks the token: %s", got)
		}
		if !strings.Contains(got, redacted) {
			t.Errorf("redactCSRF = %s, want it to contain %s", got, redacted)
		}
	}
}

func TestRedactCSRFKeepsOtherFields(t *testing.T) {
	in := `<input type="hidden" name="serverId" value="28" />`
	if got := redactCSRF(in); got != in {
		t.Errorf("redactCSRF = %s, want it unchanged", got)
	}
}
