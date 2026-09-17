package site

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeUploadFieldGolden(t *testing.T) {
	// The batch id is a fresh uuid v4 in production; pin it to the captured one.
	orig := newBatchID
	newBatchID = func() string { return "9ea18edd-baaf-485c-8a51-b0df0b0a055c" }
	t.Cleanup(func() { newBatchID = orig })

	f := &UploadedFile{
		Name: "test-upload.mp4",
		UUID: "580fcb20-5c8c-419c-88d1-5b889298c20f",
		Size: 6531378,
	}
	cfg := UploaderConfig{ServerID: 28, ServerURL: "https://t-time28.melon-soda.org"}

	got := EncodeUploadField(f, cfg)

	// Compact the fixture so the comparison does not depend on whitespace.
	// json.Compact preserves key order, so the byte comparison still pins it.
	var want bytes.Buffer
	if err := json.Compact(&want, mustReadFixture(t, "upload_field.json")); err != nil {
		t.Fatalf("compact fixture: %v", err)
	}
	if got != want.String() {
		t.Errorf("EncodeUploadField mismatch\n got: %s\nwant: %s", got, want.String())
	}
}

// The browser's JSON.stringify does not escape '&', '<' or '>'; Go's
// encoding/json does by default when marshaling into an HTML context. The
// hidden field is not HTML, so those characters must come through literally,
// matching what the site's own script would have produced.
func TestEncodeUploadFieldNoHTMLEscaping(t *testing.T) {
	f := &UploadedFile{Name: "[A&B] Title <1>.mp4", UUID: "u", Size: 1}
	got := EncodeUploadField(f, UploaderConfig{ServerID: 28, ServerURL: "https://a"})
	if !strings.Contains(got, `"name":"[A&B] Title <1>.mp4"`) {
		t.Errorf("EncodeUploadField = %s, want literal &, < and > in the name (not \\u0026 etc.)", got)
	}
}

func TestEncodeUploadFieldNil(t *testing.T) {
	if got := EncodeUploadField(nil, UploaderConfig{ServerID: 28}); got != "" {
		t.Errorf("EncodeUploadField(nil) = %q, want empty string", got)
	}
}

// The Russian channel server URL ends with a slash and the site's own script
// concatenates it verbatim, producing a double slash. Reproduce it exactly:
// see docs/architecture.md §9, "two different concatenation rules".
func TestEncodeUploadFieldEndpointVerbatim(t *testing.T) {
	tests := []struct {
		name      string
		serverURL string
		want      string
	}{
		{"no trailing slash", "https://t-time28.melon-soda.org", "https://t-time28.melon-soda.org/upload.php"},
		{"trailing slash kept", "https://ru-time28.anime-on.ru/", "https://ru-time28.anime-on.ru//upload.php"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := EncodeUploadField(&UploadedFile{Name: "a.mp4", UUID: "u", Size: 1},
				UploaderConfig{ServerID: 28, ServerURL: tt.serverURL})
			var got struct {
				Endpoint string `json:"endpoint"`
			}
			if err := json.Unmarshal([]byte(s), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Endpoint != tt.want {
				t.Errorf("endpoint = %q, want %q", got.Endpoint, tt.want)
			}
		})
	}
}

func TestNewBatchIDIsUUIDv4(t *testing.T) {
	a, b := newUUID(), newUUID()
	if a == b {
		t.Error("newUUID returned the same value twice")
	}
	if len(a) != 36 {
		t.Fatalf("len = %d, want 36: %q", len(a), a)
	}
	if a[14] != '4' {
		t.Errorf("version nibble = %q, want '4' in %q", a[14], a)
	}
	if c := a[19]; c != '8' && c != '9' && c != 'a' && c != 'b' {
		t.Errorf("variant nibble = %q, want one of 89ab in %q", c, a)
	}
}
