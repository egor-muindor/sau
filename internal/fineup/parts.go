// Package fineup speaks the chunked upload protocol of Fine Uploader 5.x as the
// site configures it. It knows nothing about anime or about the site's pages:
// its input is a file and a pool of hosts, its output is a result and a stream
// of events.
package fineup

import "fmt"

// DefaultPartSize is the chunk size the site configures. The library default is
// 2000000; the site overrides it, so this value is not negotiable.
const DefaultPartSize int64 = 5_000_000

// Part is one chunk of the file to upload.
type Part struct {
	Index  int
	Offset int64
	Size   int64
}

// PlanParts splits a file of the given size into chunks. A non positive
// partSize falls back to DefaultPartSize. An empty file is an error: the site
// makes chunking mandatory and there is nothing to send.
func PlanParts(size, partSize int64) ([]Part, error) {
	if size <= 0 {
		return nil, fmt.Errorf("fineup: file size must be positive, got %d", size)
	}
	if partSize <= 0 {
		partSize = DefaultPartSize
	}

	count := int((size + partSize - 1) / partSize)
	parts := make([]Part, 0, count)
	for i := 0; i < count; i++ {
		offset := int64(i) * partSize
		chunk := partSize
		if rest := size - offset; rest < chunk {
			chunk = rest
		}
		parts = append(parts, Part{Index: i, Offset: offset, Size: chunk})
	}
	return parts, nil
}
