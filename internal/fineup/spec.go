package fineup

// Spec describes one upload: which file, where to send it and what is already
// done. The pool is always freshly parsed from the form page, never cached: the
// site rotates its upload servers.
type Spec struct {
	Path     string
	Base     string   // Endpoint(serverURL): the base endpoint, used for touch and Result.Endpoint
	Pool     []string // serverUrls verbatim, with or without a trailing slash; normalized through Endpoint
	UUID     string   // empty means a fresh uuid v4 is generated
	PartSize int64
	Done     []int // indexes of chunks already uploaded
	MaxConns int
}

// Result is what an upload produced. Endpoint is the base endpoint, the one the
// hidden form field expects, not the host of the last chunk.
//
// LastHost is that host of the last chunk: the one finalization was sent to,
// and the one a later Delete must use. It is a request endpoint, already
// normalized by Endpoint.
type Result struct {
	UUID     string
	Name     string
	Size     int64
	Parts    int
	Endpoint string
	LastHost string
}
