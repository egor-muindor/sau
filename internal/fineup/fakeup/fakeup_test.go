package fakeup_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"sau/internal/fineup/fakeup"
)

// postChunk sends one chunk exactly the way Fine Uploader does.
func postChunk(t *testing.T, base, uuid, name string, size int64, total, idx int,
	offset int64, body []byte, resume bool) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	write := func(k, v string) {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	write("qqpartindex", strconv.Itoa(idx))
	write("qqpartbyteoffset", strconv.FormatInt(offset, 10))
	write("qqchunksize", strconv.Itoa(len(body)))
	write("qqtotalparts", strconv.Itoa(total))
	write("qqtotalfilesize", strconv.FormatInt(size, 10))
	write("qqfilename", name)
	if resume {
		write("qqresume", "true")
	}
	write("qquuid", uuid)
	fw, err := w.CreateFormFile("qqfile", name)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/upload.php", &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func TestServerAssemblesFileFromTwoHosts(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 2)
	hosts := s.Hosts()

	const uuid = "11111111-2222-4333-8444-555555555555"
	data := []byte("hello world, chunked upload")
	half := len(data) / 2

	r0 := decode(t, postChunk(t, urls[0], uuid, "a.mp4", int64(len(data)), 2, 0, 0, data[:half], false))
	if r0["success"] != true || r0["uuid"] != uuid {
		t.Fatalf("chunk 0 response = %v", r0)
	}
	if v, ok := r0["uploadName"]; !ok || v != nil {
		t.Fatalf("chunk response must carry uploadName:null, got %v (present=%v)", v, ok)
	}
	r1 := decode(t, postChunk(t, urls[1], uuid, "a.mp4", int64(len(data)), 2, 1, int64(half), data[half:], true))
	if r1["success"] != true {
		t.Fatalf("chunk 1 response = %v", r1)
	}

	if _, ok := s.Assembled(uuid); ok {
		t.Fatalf("file must not be assembled before ?done")
	}

	req, err := http.NewRequest(http.MethodPost, urls[1]+"/upload.php?done",
		strings.NewReader("qquuid="+uuid+"&qqfilename=a.mp4&qqtotalfilesize="+
			strconv.Itoa(len(data))+"&qqtotalparts=2"))
	if err != nil {
		t.Fatalf("new done request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	rd := decode(t, resp)
	if rd["success"] != true {
		t.Fatalf("done response = %v", rd)
	}
	if _, ok := rd["uploadName"]; ok {
		t.Fatalf("done response must not carry uploadName: %v", rd)
	}

	got, ok := s.Assembled(uuid)
	if !ok || !bytes.Equal(got, data) {
		t.Fatalf("assembled = %q (ok=%v), want %q", got, ok, data)
	}
	if s.DoneHost(uuid) != hosts[1] {
		t.Fatalf("DoneHost = %q, want %q", s.DoneHost(uuid), hosts[1])
	}
	if ph := s.PartHosts(uuid); ph[0] != hosts[0] || ph[1] != hosts[1] {
		t.Fatalf("PartHosts = %v, want {0:%s 1:%s}", ph, hosts[0], hosts[1])
	}
	if rf := s.ResumeFlags(uuid); rf[0] || !rf[1] {
		t.Fatalf("ResumeFlags = %v, want {0:false 1:true}", rf)
	}
}

func TestServerTouchDeleteAndFailures(t *testing.T) {
	s := fakeup.New()
	urls := s.StartN(t, 1)
	hosts := s.Hosts()

	const uuid = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

	resp, err := http.Get(urls[0] + "/upload.php?touch=1&file=/" + uuid)
	if err != nil {
		t.Fatalf("touch: %v", err)
	}
	resp.Body.Close()
	if s.Touches(uuid) != 1 {
		t.Fatalf("Touches = %d, want 1", s.Touches(uuid))
	}

	req, err := http.NewRequest(http.MethodDelete, urls[0]+"/upload.php?/"+uuid+"&", nil)
	if err != nil {
		t.Fatalf("new delete: %v", err)
	}
	dresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	rd := decode(t, dresp)
	if rd["success"] != true || rd["uuid"] != uuid {
		t.Fatalf("delete response = %v", rd)
	}
	if !s.Deleted(uuid) {
		t.Fatalf("Deleted(%s) = false", uuid)
	}

	s.FailHost(hosts[0], 1)
	fresp, err := http.Get(urls[0] + "/upload.php?touch=1&file=/" + uuid)
	if err != nil {
		t.Fatalf("touch after FailHost: %v", err)
	}
	body, _ := io.ReadAll(fresp.Body)
	fresp.Body.Close()
	if fresp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", fresp.StatusCode)
	}
	if !strings.Contains(string(body), `"success":false`) {
		t.Fatalf("body = %s, want success:false", body)
	}
	ok2, err := http.Get(urls[0] + "/upload.php?touch=1&file=/" + uuid)
	if err != nil {
		t.Fatalf("touch after budget: %v", err)
	}
	ok2.Body.Close()
	if ok2.StatusCode != http.StatusOK {
		t.Fatalf("status after failure budget = %d, want 200", ok2.StatusCode)
	}

	s.Reset()
	if s.Touches(uuid) != 0 || s.Deleted(uuid) {
		t.Fatalf("Reset did not clear state")
	}
}
