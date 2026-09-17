package fineup

// EventKind names what happened during an upload. Events are the only way a
// caller learns about progress: the uploader itself prints nothing.
type EventKind int

const (
	EventChunkStart EventKind = iota
	EventChunkDone
	EventChunkFailed
	EventHostFailed
	EventFinalizing
	EventTouch
)

// Event describes one step of an upload. Not every field is meaningful for
// every kind: Part and Bytes belong to chunk events, Err to the failures.
//
// Events reach the callback from every chunk goroutine and from the keep-alive
// goroutine, so a callback must be safe for concurrent use.
//
// EventChunkDone is the one a caller must not miss: it carries the index of a
// chunk the server has accepted, and it is where publish saves the state that
// makes a later resume possible.
type Event struct {
	Kind    EventKind
	Part    int
	Host    string
	Bytes   int64
	Attempt int
	Err     error
}
