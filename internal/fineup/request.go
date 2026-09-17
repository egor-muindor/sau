package fineup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// field is one ordered name and value of a multipart body.
type field struct {
	name  string
	value string
}

// chunkRequest builds the POST that carries one chunk.
//
// The order of the fields is not cosmetic: it reproduces the order in which
// Fine Uploader inserts the keys, with qqfile appended last as the only part
// carrying a filename. The host is a ready request endpoint, already normalized
// by Endpoint.
//
// The body is assembled in memory so that the request has a known
// Content-Length; a streaming body would force chunked transfer encoding on a
// PHP endpoint that has never been seen to accept it.
func chunkRequest(ctx context.Context, host string, s Spec, name string, size int64, totalParts int,
	part Part, resume bool, body io.Reader) (*http.Request, error) {

	fields := []field{
		{"qqpartindex", strconv.Itoa(part.Index)},
		{"qqpartbyteoffset", strconv.FormatInt(part.Offset, 10)},
		{"qqchunksize", strconv.FormatInt(part.Size, 10)},
		{"qqtotalparts", strconv.Itoa(totalParts)},
		{"qqtotalfilesize", strconv.FormatInt(size, 10)},
		{"qqfilename", name},
	}
	if resume {
		fields = append(fields, field{"qqresume", "true"})
	}
	fields = append(fields, field{"qquuid", s.UUID})

	var buf bytes.Buffer
	buf.Grow(int(part.Size) + 1024)
	w := multipart.NewWriter(&buf)
	for _, f := range fields {
		if err := w.WriteField(f.name, f.value); err != nil {
			return nil, err
		}
	}
	file, err := w.CreateFormFile("qqfile", name)
	if err != nil {
		return nil, err
	}
	copied, err := io.Copy(file, body)
	if err != nil {
		return nil, err
	}
	if copied != part.Size {
		return nil, fmt.Errorf("fineup: chunk %d: short read: got %d, want %d", part.Index, copied, part.Size)
	}
	if err := w.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.ContentLength = int64(buf.Len())
	return req, nil
}

// doneRequest builds the finalizing POST. It goes to the host of the last
// successful chunk, not to the base endpoint: the site's script moves the
// endpoint there after every successful chunk. Choosing that host is the
// caller's job.
//
// The body is built by hand because the order of the pairs is the captured one,
// and url.Values.Encode would sort the keys.
func doneRequest(ctx context.Context, host, uuid, name string, size int64, totalParts int) (*http.Request, error) {
	var b strings.Builder
	b.WriteString("qquuid=")
	b.WriteString(url.QueryEscape(uuid))
	b.WriteString("&qqfilename=")
	b.WriteString(url.QueryEscape(name))
	b.WriteString("&qqtotalfilesize=")
	b.WriteString(strconv.FormatInt(size, 10))
	b.WriteString("&qqtotalparts=")
	b.WriteString(strconv.Itoa(totalParts))
	body := b.String()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"?done", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.ContentLength = int64(len(body))
	return req, nil
}

// deleteRequest builds the DELETE that removes an uploaded file. The odd query,
// "?/<uuid>&", is what the site's configuration produces: the delete endpoint
// already ends with a question mark and the library appends the uuid and a
// separator. It is reproduced verbatim.
//
// Like the finalizing request, it goes to the host of the last successful chunk.
func deleteRequest(ctx context.Context, host, uuid string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodDelete, host+"?/"+uuid+"&", nil)
}

// touchRequest builds the keep alive ping. It goes to the base endpoint, every
// fifteen seconds while an upload is running; without it the server cleans up
// what has not been finalized yet.
func touchRequest(ctx context.Context, base, uuid string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, base+"?touch=1&file=/"+uuid, nil)
}
