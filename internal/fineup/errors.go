package fineup

import (
	"errors"
	"fmt"
)

// ErrNoClient is returned when an Uploader has no HTTP client. The zero value
// is unusable on purpose: see New.
var ErrNoClient = errors.New("fineup: no HTTP client configured")

// ServerError is what an upload server answered when the answer was not a
// success: a non 2xx status, a body that is not the expected JSON, or a JSON
// object with success set to false. The body is kept so that the failure can be
// shown to a human instead of being summarized away.
type ServerError struct {
	Status int
	Body   string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("fineup: upload server answered %d: %s", e.Status, e.Body)
}

// PoolExhaustedError is what a chunk that ran out of attempts produces. Done
// lists every chunk index the server holds, in ascending order: the ones this
// run uploaded plus the ones Spec.Done already carried. It is meant to be saved
// as is and handed back as the next Spec.Done, so that a resume starts where
// this run stopped instead of from the beginning.
type PoolExhaustedError struct {
	Part int
	Done []int
	Last error
}

func (e *PoolExhaustedError) Error() string {
	return fmt.Sprintf("fineup: chunk %d: every attempt failed, %d chunks uploaded: %v",
		e.Part, len(e.Done), e.Last)
}

func (e *PoolExhaustedError) Unwrap() error { return e.Last }

// FinalizeError means every chunk arrived but the finalizing request did not
// succeed. Host is the host it was sent to, the one of the last successful
// chunk, because that is where the server keeps the parts.
type FinalizeError struct {
	Host string
	Err  error
}

func (e *FinalizeError) Error() string {
	return fmt.Sprintf("fineup: finalizing on %s: %v", e.Host, e.Err)
}

func (e *FinalizeError) Unwrap() error { return e.Err }
