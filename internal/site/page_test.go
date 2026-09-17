package site

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// testCSRF is the anonymized token baked into every fixture in testdata/.
const testCSRF = "TESTCSRF0000000000000000000000000000000000000000000000000000000000000000000000000000000=="

// mustReadFixture reads a file from testdata/ or fails the test.
func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestParsePageCSRF(t *testing.T) {
	p, err := ParsePage(strings.NewReader(string(mustReadFixture(t, "create_form.html"))))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if p.CSRF != testCSRF {
		t.Errorf("CSRF = %q, want %q", p.CSRF, testCSRF)
	}
	if p.IsLogin {
		t.Error("IsLogin = true, want false for the create form page")
	}
	if len(p.Errors) != 0 {
		t.Errorf("Errors = %v, want none", p.Errors)
	}
}

func TestParsePageNoCSRF(t *testing.T) {
	p, err := ParsePage(strings.NewReader(`<html><body><p>nothing here</p></body></html>`))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if p.CSRF != "" {
		t.Errorf("CSRF = %q, want empty", p.CSRF)
	}
}

func TestParsePageUploaders(t *testing.T) {
	p, err := ParsePage(strings.NewReader(string(mustReadFixture(t, "create_form.html"))))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if len(p.Uploaders) != 2 {
		t.Fatalf("len(Uploaders) = %d, want 2", len(p.Uploaders))
	}

	f, err := p.CreateForm()
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	if f.CSRF != testCSRF {
		t.Errorf("CSRF = %q, want %q", f.CSRF, testCSRF)
	}

	wantPool := []string{
		"https://t-time28.melon-soda.org",
		"https://time28.melon-soda.org",
		"https://time28.anime-on.ru",
		"https://t-time28.anime-on.ru",
	}

	if f.Video.ServerID != 28 {
		t.Errorf("Video.ServerID = %d, want 28", f.Video.ServerID)
	}
	if f.Video.ServerURL != "https://t-time28.melon-soda.org" {
		t.Errorf("Video.ServerURL = %q", f.Video.ServerURL)
	}
	if !reflect.DeepEqual(f.Video.ServerURLs, wantPool) {
		t.Errorf("Video.ServerURLs = %v, want %v", f.Video.ServerURLs, wantPool)
	}
	if !slices.Contains(f.Video.AllowedExt, "mp4") || !slices.Contains(f.Video.AllowedExt, "mkv") {
		t.Errorf("Video.AllowedExt = %v, want mp4 and mkv in it", f.Video.AllowedExt)
	}
	if slices.Contains(f.Video.AllowedExt, "ass") {
		t.Error("Video.AllowedExt must not contain subtitle extensions")
	}

	if f.Sub.ServerID != 28 {
		t.Errorf("Sub.ServerID = %d, want 28", f.Sub.ServerID)
	}
	if !reflect.DeepEqual(f.Sub.ServerURLs, wantPool) {
		t.Errorf("Sub.ServerURLs = %v, want %v", f.Sub.ServerURLs, wantPool)
	}
	wantSubExt := []string{"ass", "ssa", "srt", "vtt", "webvtt"}
	if !reflect.DeepEqual(f.Sub.AllowedExt, wantSubExt) {
		t.Errorf("Sub.AllowedExt = %v, want %v", f.Sub.AllowedExt, wantSubExt)
	}
}

func TestParsePageUploadersOrderIndependent(t *testing.T) {
	// The subtitle uploader comes first in the markup here; video must still
	// land in Uploaders[0], because the split is by formField, not by order.
	const page = `<html><body>
<div style="display:none"><input type="hidden" value="` + testCSRF + `" name="csrf"></div>
<script>
require(['fine\x2Duploader'], function () {initFileUploader({"validation":{"allowedExtensions":["ass"],"itemLimit":1},"formField":"TranslationAdminForm_subFileNew","serverId":7,"serverUrl":"https:\/\/sub.example.org","serverUrls":["https:\/\/sub.example.org"]})});
require(['fine\x2Duploader'], function () {initFileUploader({"validation":{"allowedExtensions":["mp4"],"itemLimit":1},"formField":"TranslationAdminForm_videoFileNew","serverId":7,"serverUrl":"https:\/\/vid.example.org","serverUrls":["https:\/\/vid.example.org"]})});
</script></body></html>`

	p, err := ParsePage(strings.NewReader(page))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	f, err := p.CreateForm()
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	if f.Video.ServerURL != "https://vid.example.org" {
		t.Errorf("Video.ServerURL = %q, want the video one", f.Video.ServerURL)
	}
	if f.Sub.ServerURL != "https://sub.example.org" {
		t.Errorf("Sub.ServerURL = %q, want the subtitle one", f.Sub.ServerURL)
	}
}

func TestCreateFormIncomplete(t *testing.T) {
	p, err := ParsePage(strings.NewReader(`<html><body><input type="hidden" name="csrf" value="x"></body></html>`))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if _, err := p.CreateForm(); !errors.Is(err, ErrNoForm) {
		t.Errorf("CreateForm error = %v, want ErrNoForm", err)
	}
}

func TestParsePageLogin(t *testing.T) {
	p, err := ParsePage(strings.NewReader(string(mustReadFixture(t, "login_page.html"))))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if !p.IsLogin {
		t.Error("IsLogin = false, want true for the login page")
	}
	if p.CSRF != testCSRF {
		t.Errorf("CSRF = %q, want %q", p.CSRF, testCSRF)
	}
	if len(p.Uploaders) != 0 {
		t.Errorf("Uploaders = %v, want none on the login page", p.Uploaders)
	}
	if _, err := p.CreateForm(); !errors.Is(err, ErrNoForm) {
		t.Errorf("CreateForm on login page: err = %v, want ErrNoForm", err)
	}
}

// The rejection markup is provisional: the real error block of the site has not
// been captured yet (open question 2 in docs/architecture.md §14). Only this
// fixture changes once it is.
func TestParsePageRejected(t *testing.T) {
	p, err := ParsePage(strings.NewReader(string(mustReadFixture(t, "create_form_rejected.html"))))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if p.IsLogin {
		t.Error("IsLogin = true, want false")
	}
	want := []string{"Номер серии обязателен."}
	if !reflect.DeepEqual(p.Errors, want) {
		t.Errorf("Errors = %v, want %v", p.Errors, want)
	}
	// A rejected page is still a usable form: it carries a fresh CSRF and the
	// uploader configs, so the caller can show the reasons and stop.
	if len(p.Uploaders) != 2 {
		t.Errorf("len(Uploaders) = %d, want 2", len(p.Uploaders))
	}
}

// TestParseUploadersClosingVariants covers real-world whitespace/punctuation
// around the closing of initFileUploader({...}), which the site's minified
// and unminified script both use in slightly different forms. The regex this
// replaced anchored strictly on "})}" and silently found zero uploaders on
// every one of these.
func TestParseUploadersClosingVariants(t *testing.T) {
	one := `{"formField":"TranslationAdminForm_videoFileNew","serverId":1,"serverUrl":"https://a","serverUrls":["https://a"],"validation":{"allowedExtensions":["mp4"]}}`
	two := `{"formField":"TranslationAdminForm_subFileNew","serverId":1,"serverUrl":"https://b","serverUrls":["https://b"],"validation":{"allowedExtensions":["ass"]}}`

	tests := []struct {
		name   string
		script string
	}{
		{
			"space before closing paren-brace",
			"initFileUploader( " + one + ")}",
		},
		{
			"space after closing brace",
			"initFileUploader(" + one + ") }",
		},
		{
			"newline after closing brace",
			"initFileUploader(" + one + ")\n}",
		},
		{
			"trailing semicolon, no closing brace",
			"function(){initFileUploader(" + one + ")};",
		},
		{
			"two calls back to back without )}",
			"require(['x'], function () {initFileUploader(" + one + ");});\n" +
				"require(['x'], function () {initFileUploader(" + two + ");});\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseUploaders(tt.script)
			if len(got) == 0 {
				t.Fatalf("parseUploaders(%q) found 0 uploaders, want at least 1", tt.script)
			}
		})
	}

	// The "two calls back to back" case specifically must find both.
	both := parseUploaders(tests[len(tests)-1].script)
	if len(both) != 2 {
		t.Fatalf("parseUploaders found %d uploaders, want 2: %v", len(both), both)
	}
	if both[0].FormField != videoFormField || both[1].FormField != subFormField {
		t.Errorf("uploaders = %+v, want video then sub", both)
	}
}

// TestParseUploadersBraceInString ensures the scanner does not get confused
// by a '{' or '}' character appearing inside a JSON string value.
func TestParseUploadersBraceInString(t *testing.T) {
	script := `initFileUploader({"formField":"TranslationAdminForm_videoFileNew","serverId":1,` +
		`"serverUrl":"https://a","serverUrls":["https://a"],` +
		`"validation":{"allowedExtensions":["mp4"]},"note":"a { b } c \"nested\" d"})}`
	got := parseUploaders(script)
	if len(got) != 1 {
		t.Fatalf("parseUploaders found %d uploaders, want 1: %v", len(got), got)
	}
	if got[0].ServerURL != "https://a" {
		t.Errorf("ServerURL = %q, want https://a", got[0].ServerURL)
	}
}
