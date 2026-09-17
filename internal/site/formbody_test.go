package site

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sau/internal/secrets"
	"sau/internal/translation"
)

func TestJSEscape(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"space becomes plus", "a b", "a+b"},
		{"comma percent encoded", "Alice, Bob", "Alice%2C+Bob"},
		{"parens untouched", "(x)", "(x)"},
		{"brackets encoded", "[1080p]", "%5B1080p%5D"},
		{"tilde bang star quote untouched", "~!*'", "~!*'"},
		{"unreserved untouched", "a-Z_0.9", "a-Z_0.9"},
		{"slash and colon encoded", "https://a/b", "https%3A%2F%2Fa%2Fb"},
		{"equals and amp encoded", "a=b&c", "a%3Db%26c"},
		{"cyrillic as utf8 percent", "Тест", "%D0%A2%D0%B5%D1%81%D1%82"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsEscape(tt.in); got != tt.want {
				t.Errorf("jsEscape(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// wantSubmitBody is the captured POST body of a real form submit
// (.work/captures/network.log line 455), anonymized: the authors string, the
// file name, the uuid, the batch id and the size come from the test fixtures,
// and the CSRF token is the test one. Everything else — the key order, the two
// empty qqfile fields, the channel in two fields, the empty subtitle field,
// the trailing yt0= and dynpage=1 — is byte for byte as captured.
const wantSubmitBody = "csrf=TESTCSRF0000000000000000000000000000000000000000000000000000000000000000000000000000000%3D%3D" +
	"&TranslationAdminForm%5BseriesIdNew%5D=36866" +
	"&TranslationAdminForm%5BepisodeNumberNew%5D=1" +
	"&TranslationAdminForm%5BepisodeTypeNew%5D=tv" +
	"&TranslationAdminForm%5Btype%5D=voiceRu" +
	"&TranslationAdminForm%5BauthorsNew%5D=Team+(Alice%2C+Bob)" +
	"&TranslationAdminForm%5BaddedByAuthor%5D=0" +
	"&upload-channel=2" +
	"&upload-channel-mobile=2" +
	"&TranslationAdminForm%5BvideoFileNew%5D=%7B%22files%22%3A%5B%7B%22name%22%3A%22%5BTeam%5D+Title+-+01+%5B1080p%5D.mp4%22%2C%22originalName%22%3A%22%5BTeam%5D+Title+-+01+%5B1080p%5D.mp4%22%2C%22uuid%22%3A%22580fcb20-5c8c-419c-88d1-5b889298c20f%22%2C%22size%22%3A6531378%2C%22status%22%3A%22upload+successful%22%2C%22file%22%3A%7B%22qqDropTarget%22%3A%7B%22__ym_indexer%22%3A10%7D%2C%22qqThumbnailId%22%3A0%7D%2C%22batchId%22%3A%229ea18edd-baaf-485c-8a51-b0df0b0a055c%22%2C%22id%22%3A0%7D%5D%2C%22endpoint%22%3A%22https%3A%2F%2Ft-time28.melon-soda.org%2Fupload.php%22%2C%22serverId%22%3A28%7D" +
	"&qqfile=" +
	"&TranslationAdminForm%5BsubFileNew%5D=" +
	"&qqfile=" +
	"&yt0=" +
	"&dynpage=1"

// testDraft is the anonymized draft behind wantSubmitBody. Reused by 06-site-client.md.
func testDraft() translation.Draft {
	return translation.Draft{
		SeriesID:      36866,
		EpisodeNumber: "1",
		EpisodeType:   translation.TV,
		Type:          translation.VoiceRu,
		Authors:       "Team (Alice, Bob)",
		AddedByAuthor: false,
		VideoPath:     "/tmp/[Team] Title - 01 [1080p].mp4",
	}
}

// testVideoField is the hidden field value used in wantSubmitBody.
func testVideoField(t *testing.T) string {
	t.Helper()
	orig := newBatchID
	newBatchID = func() string { return "9ea18edd-baaf-485c-8a51-b0df0b0a055c" }
	defer func() { newBatchID = orig }()
	return EncodeUploadField(
		&UploadedFile{
			Name: "[Team] Title - 01 [1080p].mp4",
			UUID: "580fcb20-5c8c-419c-88d1-5b889298c20f",
			Size: 6531378,
		},
		UploaderConfig{ServerID: 28, ServerURL: "https://t-time28.melon-soda.org"},
	)
}

func TestEncodeSubmitFormMatchesCapture(t *testing.T) {
	got := EncodeSubmitForm(testCSRF, testDraft(), translation.ChannelCDN, testVideoField(t), "")
	if string(got) != wantSubmitBody {
		t.Errorf("EncodeSubmitForm mismatch\n got: %s\nwant: %s", got, wantSubmitBody)
	}
}

func TestEncodeSubmitFormAddedByAuthor(t *testing.T) {
	d := testDraft()
	d.AddedByAuthor = true
	got := string(EncodeSubmitForm(testCSRF, d, translation.ChannelCDN, testVideoField(t), ""))
	if !strings.Contains(got, "&TranslationAdminForm%5BaddedByAuthor%5D=1&") {
		t.Errorf("addedByAuthor not encoded as 1: %s", got)
	}
}

func TestEncodeSubmitFormChannelInTwoFields(t *testing.T) {
	for _, ch := range []translation.Channel{translation.ChannelAll, translation.ChannelCDN, translation.ChannelRU} {
		got := string(EncodeSubmitForm(testCSRF, testDraft(), ch, testVideoField(t), ""))
		v := ch.CookieValue()
		if !strings.Contains(got, "&upload-channel="+v+"&upload-channel-mobile="+v+"&") {
			t.Errorf("channel %v: both channel fields not set to %q in %s", ch, v, got)
		}
	}
}

func TestEncodeSubmitFormWithSubtitles(t *testing.T) {
	sub := EncodeUploadField(&UploadedFile{Name: "s.ass", UUID: "u", Size: 10},
		UploaderConfig{ServerID: 28, ServerURL: "https://t-time28.melon-soda.org"})
	got := string(EncodeSubmitForm(testCSRF, testDraft(), translation.ChannelCDN, testVideoField(t), sub))
	if !strings.Contains(got, "&TranslationAdminForm%5BsubFileNew%5D="+jsEscape(sub)+"&qqfile=&yt0=&dynpage=1") {
		t.Errorf("subtitle field not placed as captured: %s", got)
	}
}

func TestEncodeLoginFormMatchesCapture(t *testing.T) {
	const want = "csrf=TESTCSRF0000000000000000000000000000000000000000000000000000000000000000000000000000000%3D%3D" +
		"&LoginForm%5Busername%5D=user%40example.com" +
		"&LoginForm%5Bpassword%5D=s3cr3t+pass" +
		"&yt0=" +
		"&dynpage=1"

	got := EncodeLoginForm(testCSRF, "user@example.com", secrets.New("s3cr3t pass"))
	if string(got) != want {
		t.Errorf("EncodeLoginForm mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestEncodeLoginFormEscapesPassword(t *testing.T) {
	got := string(EncodeLoginForm("c", "u", secrets.New("a&b=c д")))
	const want = "csrf=c&LoginForm%5Busername%5D=u&LoginForm%5Bpassword%5D=a%26b%3Dc+%D0%B4&yt0=&dynpage=1"
	if got != want {
		t.Errorf("EncodeLoginForm = %q, want %q", got, want)
	}
}

// Reveal() must be called in exactly one file of this package, and in no other.
// The second and last call in the program lives inside internal/secrets, in
// KeyringSource.Save; it is out of reach of this scan on purpose.
func TestRevealCalledOnlyInLoginEncoder(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), ".Reveal()") {
			hits = append(hits, f)
		}
	}
	want := []string{filepath.Join(".", "formbody.go")}
	if !reflect.DeepEqual(hits, want) {
		t.Errorf("Reveal() appears in %v, want only %v", hits, want)
	}
}
