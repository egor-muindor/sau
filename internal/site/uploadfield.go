package site

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
)

// UploadedFile is one finished upload, as the hidden form field describes it.
type UploadedFile struct {
	Name string
	UUID string
	Size int64
}

// UploadedFields carries the two optional uploads of a submit.
type UploadedFields struct {
	Video *UploadedFile
	Sub   *UploadedFile
}

// uploadFieldFile mirrors the browser uploader's per-file object. The bookkeeping
// members (qqDropTarget, qqThumbnailId) are reproduced as captured: finding out
// which of them the server ignores costs more than repeating all of them.
// Field order below is the capture order and must not be rearranged.
type uploadFieldFile struct {
	Name         string `json:"name"`
	OriginalName string `json:"originalName"`
	UUID         string `json:"uuid"`
	Size         int64  `json:"size"`
	Status       string `json:"status"`
	File         struct {
		QQDropTarget struct {
			YmIndexer int `json:"__ym_indexer"`
		} `json:"qqDropTarget"`
		QQThumbnailID int `json:"qqThumbnailId"`
	} `json:"file"`
	BatchID string `json:"batchId"`
	ID      int    `json:"id"`
}

type uploadField struct {
	Files    []uploadFieldFile `json:"files"`
	Endpoint string            `json:"endpoint"`
	ServerID int               `json:"serverId"`
}

// newBatchID is a package variable so tests can pin the generated batch id.
var newBatchID = newUUID

// EncodeUploadField renders the hidden form field for one finished upload.
// A nil file yields the empty string: the captured submit sent an empty string,
// not an empty JSON object, for the missing subtitle file.
//
// The endpoint is concatenated verbatim, exactly as the site's own script does,
// including the double slash for a server URL that ends with one. Normalization
// belongs to HTTP requests only; see docs/architecture.md §9.
func EncodeUploadField(f *UploadedFile, cfg UploaderConfig) string {
	if f == nil {
		return ""
	}
	var item uploadFieldFile
	item.Name = f.Name
	item.OriginalName = f.Name
	item.UUID = f.UUID
	item.Size = f.Size
	item.Status = "upload successful"
	item.File.QQDropTarget.YmIndexer = 10
	item.File.QQThumbnailID = 0
	item.BatchID = newBatchID()
	item.ID = 0

	v := uploadField{
		Files:    []uploadFieldFile{item},
		Endpoint: cfg.ServerURL + "/upload.php",
		ServerID: cfg.ServerID,
	}
	// The browser's JSON.stringify does not HTML-escape '&', '<' or '>'; Go's
	// encoding/json does by default. The hidden field is not embedded in HTML
	// as a literal <script> payload, so SetEscapeHTML(false) reproduces what
	// the site's own script would have produced, byte for byte.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		// Unreachable: v is a plain struct of basic types with no cycles,
		// channels or unsupported types. A panic here would be a real bug,
		// not a condition a caller of EncodeUploadField could act on; "" would
		// be misread as "no file", which is a different, valid outcome.
		panic("site: encode upload field: " + err.Error())
	}
	// json.Encoder.Encode appends a trailing newline that json.Marshal does not.
	return string(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}

// newUUID returns a random uuid v4 in the canonical 8-4-4-4-12 form.
//
// It panics rather than returning an error: a failure of crypto/rand is fatal,
// not something a caller can act on. The same signature is used in fineup and
// publish, deliberately.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("site: crypto/rand failed: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
