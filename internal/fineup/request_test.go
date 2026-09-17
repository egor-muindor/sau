package fineup

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

const testUUID = "580fcb20-5c8c-419c-88d1-5b889298c20f"

func testSpec() Spec {
	return Spec{
		Path:     "/tmp/[Team] Title - 01 [1080p].mp4",
		Base:     Endpoint("https://t-time28.melon-soda.org"),
		Pool:     []string{"https://t-time28.melon-soda.org", "https://time28.melon-soda.org"},
		UUID:     testUUID,
		PartSize: DefaultPartSize,
		MaxConns: DefaultMaxConns,
	}
}

// multipartField is one decoded part of a multipart body.
type multipartField struct {
	Name     string
	FileName string
	Value    string
}

// readMultipart decodes a request body built by chunkRequest, preserving the
// order of the parts.
func readMultipart(t *testing.T, req *http.Request) []multipartField {
	t.Helper()

	mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parsing Content-Type %q: %v", req.Header.Get("Content-Type"), err)
	}
	if mediaType != "multipart/form-data" {
		t.Fatalf("Content-Type is %q, want multipart/form-data", mediaType)
	}
	if params["boundary"] == "" {
		t.Fatal("Content-Type carries no boundary")
	}

	body, err := req.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	defer body.Close()

	r := multipart.NewReader(body, params["boundary"])
	var out []multipartField
	for {
		part, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading part %d: %v", len(out), err)
		}
		value, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("reading the body of part %q: %v", part.FormName(), err)
		}
		out = append(out, multipartField{
			Name:     part.FormName(),
			FileName: part.FileName(),
			Value:    string(value),
		})
		part.Close()
	}
	return out
}

func fieldNames(fields []multipartField) []string {
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.Name)
	}
	return names
}

func TestChunkRequestMethodAndURL(t *testing.T) {
	host := Endpoint("https://time28.anime-on.ru")
	req, err := chunkRequest(context.Background(), host, testSpec(),
		"[Team] Title - 01 [1080p].mp4", 12_000_000, 3,
		Part{Index: 1, Offset: 5_000_000, Size: int64(len("payload"))}, false, strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("chunkRequest returned error %v, want none", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if got := req.URL.String(); got != host {
		t.Fatalf("url = %q, want %q", got, host)
	}
	if req.ContentLength <= 0 {
		t.Fatalf("ContentLength = %d, want a positive value", req.ContentLength)
	}
}

func TestChunkRequestFieldOrder(t *testing.T) {
	const name = "[Team] Title - 01 [1080p].mp4"
	req, err := chunkRequest(context.Background(), Endpoint("https://time28.anime-on.ru"),
		testSpec(), name, 12_000_000, 3,
		Part{Index: 1, Offset: 5_000_000, Size: int64(len("chunk body"))}, false, strings.NewReader("chunk body"))
	if err != nil {
		t.Fatalf("chunkRequest returned error %v, want none", err)
	}

	fields := readMultipart(t, req)
	want := []string{
		"qqpartindex",
		"qqpartbyteoffset",
		"qqchunksize",
		"qqtotalparts",
		"qqtotalfilesize",
		"qqfilename",
		"qquuid",
		"qqfile",
	}
	got := fieldNames(fields)
	if len(got) != len(want) {
		t.Fatalf("body holds %d fields %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("field %d is %q, want %q (full order %v)", i, got[i], want[i], got)
		}
	}
}

func TestChunkRequestFieldValues(t *testing.T) {
	const name = "[Team] Title - 01 [1080p].mp4"
	req, err := chunkRequest(context.Background(), Endpoint("https://time28.anime-on.ru"),
		testSpec(), name, 12_000_000, 3,
		Part{Index: 1, Offset: 5_000_000, Size: int64(len("chunk body"))}, false, strings.NewReader("chunk body"))
	if err != nil {
		t.Fatalf("chunkRequest returned error %v, want none", err)
	}

	values := make(map[string]string)
	for _, f := range readMultipart(t, req) {
		values[f.Name] = f.Value
	}
	want := map[string]string{
		"qqpartindex":      "1",
		"qqpartbyteoffset": "5000000",
		"qqchunksize":      "10",
		"qqtotalparts":     "3",
		"qqtotalfilesize":  "12000000",
		"qqfilename":       name,
		"qquuid":           testUUID,
		"qqfile":           "chunk body",
	}
	for k, w := range want {
		if got := values[k]; got != w {
			t.Fatalf("field %q = %q, want %q", k, got, w)
		}
	}
}

func TestChunkRequestFileIsTheLastPartAndCarriesTheFilename(t *testing.T) {
	const name = "[Team] Title - 01 [1080p].mp4"
	req, err := chunkRequest(context.Background(), Endpoint("https://time28.anime-on.ru"),
		testSpec(), name, 12_000_000, 3,
		Part{Index: 0, Offset: 0, Size: int64(len("chunk body"))}, false, strings.NewReader("chunk body"))
	if err != nil {
		t.Fatalf("chunkRequest returned error %v, want none", err)
	}

	fields := readMultipart(t, req)
	last := fields[len(fields)-1]
	if last.Name != "qqfile" {
		t.Fatalf("the last part is %q, want qqfile", last.Name)
	}
	if last.FileName != name {
		t.Fatalf("qqfile carries filename %q, want %q", last.FileName, name)
	}
	for _, f := range fields[:len(fields)-1] {
		if f.FileName != "" {
			t.Fatalf("part %q carries filename %q, want none", f.Name, f.FileName)
		}
	}
}

func TestChunkRequestResumeFlag(t *testing.T) {
	tests := []struct {
		name   string
		resume bool
		want   []string
	}{
		{
			name:   "fresh upload",
			resume: false,
			want: []string{"qqpartindex", "qqpartbyteoffset", "qqchunksize", "qqtotalparts",
				"qqtotalfilesize", "qqfilename", "qquuid", "qqfile"},
		},
		{
			name:   "resumed upload",
			resume: true,
			want: []string{"qqpartindex", "qqpartbyteoffset", "qqchunksize", "qqtotalparts",
				"qqtotalfilesize", "qqfilename", "qqresume", "qquuid", "qqfile"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := chunkRequest(context.Background(), Endpoint("https://time28.anime-on.ru"),
				testSpec(), "episode.mp4", 12_000_000, 3,
				Part{Index: 2, Offset: 10_000_000, Size: int64(len("tail"))}, tt.resume, strings.NewReader("tail"))
			if err != nil {
				t.Fatalf("chunkRequest returned error %v, want none", err)
			}

			fields := readMultipart(t, req)
			got := fieldNames(fields)
			if len(got) != len(tt.want) {
				t.Fatalf("body holds %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("field %d is %q, want %q (full order %v)", i, got[i], tt.want[i], got)
				}
			}
			if tt.resume {
				for _, f := range fields {
					if f.Name == "qqresume" && f.Value != "true" {
						t.Fatalf("qqresume = %q, want \"true\"", f.Value)
					}
				}
			}
		})
	}
}

func TestChunkRequestCarriesTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := chunkRequest(ctx, Endpoint("https://time28.anime-on.ru"), testSpec(),
		"episode.mp4", 100, 1, Part{Index: 0, Offset: 0, Size: int64(len("x"))}, false, strings.NewReader("x"))
	if err != nil {
		t.Fatalf("chunkRequest returned error %v, want none", err)
	}
	if req.Context() != ctx {
		t.Fatal("the request does not carry the context it was given")
	}
}

// shortReader returns less data than it claims to have via n, and then io.EOF,
// simulating a source file that got truncated mid-read.
type shortReader struct {
	data []byte
	read bool
}

func (r *shortReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	n := copy(p, r.data)
	return n, io.EOF
}

func TestChunkRequestShortReadIsAnError(t *testing.T) {
	part := Part{Index: 4, Offset: 20_000_000, Size: 10}
	_, err := chunkRequest(context.Background(), Endpoint("https://time28.anime-on.ru"),
		testSpec(), "episode.mp4", 12_000_000, 3, part, false,
		&shortReader{data: []byte("abc")}) // 3 bytes, part.Size says 10
	if err == nil {
		t.Fatal("chunkRequest returned no error for a short read, want one")
	}
	want := "fineup: chunk 4: short read: got 3, want 10"
	if got := err.Error(); got != want {
		t.Fatalf("chunkRequest error = %q, want %q", got, want)
	}
}

func TestDoneRequest(t *testing.T) {
	host := Endpoint("https://time28.melon-soda.org")
	req, err := doneRequest(context.Background(), host, testUUID,
		"[Team] Title - 01 [1080p].mp4", 1_106_621_110, 222)
	if err != nil {
		t.Fatalf("doneRequest returned error %v, want none", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if got, want := req.URL.String(), host+"?done"; got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Content-Type"), "application/x-www-form-urlencoded"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}

	body, err := req.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	defer body.Close()
	raw, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	want := "qquuid=" + testUUID +
		"&qqfilename=%5BTeam%5D+Title+-+01+%5B1080p%5D.mp4" +
		"&qqtotalfilesize=1106621110" +
		"&qqtotalparts=222"
	if got := string(raw); got != want {
		t.Fatalf("body =\n%s\nwant\n%s", got, want)
	}
	if req.ContentLength != int64(len(want)) {
		t.Fatalf("ContentLength = %d, want %d", req.ContentLength, len(want))
	}
}

func TestDoneRequestFieldOrderIsStable(t *testing.T) {
	// url.Values.Encode would sort the keys alphabetically and put qqfilename
	// first. The captured request starts with qquuid, so the body is built by
	// hand and this test guards that.
	req, err := doneRequest(context.Background(), Endpoint("https://time28.melon-soda.org"),
		testUUID, "episode.mp4", 100, 1)
	if err != nil {
		t.Fatalf("doneRequest returned error %v, want none", err)
	}
	body, err := req.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	defer body.Close()
	raw, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}

	want := []string{"qquuid", "qqfilename", "qqtotalfilesize", "qqtotalparts"}
	pairs := strings.Split(string(raw), "&")
	if len(pairs) != len(want) {
		t.Fatalf("body holds %d pairs %q, want %d", len(pairs), pairs, len(want))
	}
	for i, w := range want {
		if key, _, _ := strings.Cut(pairs[i], "="); key != w {
			t.Fatalf("pair %d has key %q, want %q", i, key, w)
		}
	}
}

func TestDoneRequestCarriesTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := doneRequest(ctx, Endpoint("https://time28.melon-soda.org"), testUUID, "episode.mp4", 100, 1)
	if err != nil {
		t.Fatalf("doneRequest returned error %v, want none", err)
	}
	if req.Context() != ctx {
		t.Fatal("the request does not carry the context it was given")
	}
}

func TestDeleteRequest(t *testing.T) {
	tests := []struct {
		name string
		host string
		uuid string
		want string
	}{
		{
			name: "cdn mirror",
			host: Endpoint("https://t-time28.melon-soda.org"),
			uuid: testUUID,
			want: "https://t-time28.melon-soda.org/upload.php?/" + testUUID + "&",
		},
		{
			name: "russian host, served with a trailing slash",
			host: Endpoint("https://ru-time28.anime-on.ru/"),
			uuid: testUUID,
			want: "https://ru-time28.anime-on.ru/upload.php?/" + testUUID + "&",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := deleteRequest(context.Background(), tt.host, tt.uuid)
			if err != nil {
				t.Fatalf("deleteRequest returned error %v, want none", err)
			}
			if req.Method != http.MethodDelete {
				t.Fatalf("method = %q, want DELETE", req.Method)
			}
			if got := req.URL.String(); got != tt.want {
				t.Fatalf("url = %q, want %q", got, tt.want)
			}
			if req.Body != nil {
				t.Fatal("the request carries a body, want none")
			}
		})
	}
}

func TestTouchRequest(t *testing.T) {
	tests := []struct {
		name string
		base string
		uuid string
		want string
	}{
		{
			name: "cdn base endpoint",
			base: Endpoint("https://t-time28.melon-soda.org"),
			uuid: testUUID,
			want: "https://t-time28.melon-soda.org/upload.php?touch=1&file=/" + testUUID,
		},
		{
			name: "russian base endpoint",
			base: Endpoint("https://ru-time28.anime-on.ru/"),
			uuid: testUUID,
			want: "https://ru-time28.anime-on.ru/upload.php?touch=1&file=/" + testUUID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := touchRequest(context.Background(), tt.base, tt.uuid)
			if err != nil {
				t.Fatalf("touchRequest returned error %v, want none", err)
			}
			if req.Method != http.MethodGet {
				t.Fatalf("method = %q, want GET", req.Method)
			}
			if got := req.URL.String(); got != tt.want {
				t.Fatalf("url = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeleteAndTouchCarryTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	del, err := deleteRequest(ctx, Endpoint("https://time28.anime-on.ru"), testUUID)
	if err != nil {
		t.Fatalf("deleteRequest returned error %v, want none", err)
	}
	if del.Context() != ctx {
		t.Fatal("the delete request does not carry the context it was given")
	}

	touch, err := touchRequest(ctx, Endpoint("https://time28.anime-on.ru"), testUUID)
	if err != nil {
		t.Fatalf("touchRequest returned error %v, want none", err)
	}
	if touch.Context() != ctx {
		t.Fatal("the touch request does not carry the context it was given")
	}
}
