package publish

import (
	"fmt"
	"time"

	"sau/internal/site"
	"sau/internal/translation"
)

// decision is the verdict of the resume table in docs/architecture.md §8.
type decision int

const (
	resumeNew decision = iota
	resumeContinue
	resumeRestartFileChanged
	resumeRestartServerChanged
	resumeReupload
)

func (d decision) String() string {
	switch d {
	case resumeNew:
		return "new"
	case resumeContinue:
		return "continue"
	case resumeRestartFileChanged:
		return "restart-file-changed"
	case resumeRestartServerChanged:
		return "restart-server-changed"
	case resumeReupload:
		return "reupload"
	}
	return "unknown"
}

// ChannelMismatchError refuses to touch an upload made in another channel.
// The channel is not a detail: the server id is the same across channels while
// the host sets do not overlap, so the chunks are simply not there. Exit code 2.
type ChannelMismatchError struct{ Saved, Requested translation.Channel }

func (e *ChannelMismatchError) Error() string {
	return fmt.Sprintf(
		"publish: the saved upload was made in channel %s, but %s was requested; "+
			"continue with --channel %s or start over with --fresh",
		e.Saved, e.Requested, e.Saved)
}

// decideResume implements the resume table. Resuming is allowed only when both
// the server and the channel match; the host list may change freely.
func decideResume(st *UploadState, f site.CreateForm, ch translation.Channel,
	size int64, mod time.Time) (decision, error) {

	if st == nil {
		return resumeNew, nil
	}
	if st.Size != size || !st.ModTime.Equal(mod) {
		return resumeRestartFileChanged, nil
	}
	if st.Channel != ch {
		return resumeNew, &ChannelMismatchError{Saved: st.Channel, Requested: ch}
	}
	if st.ServerID == f.Video.ServerID {
		return resumeContinue, nil
	}
	if st.Video == nil || st.Video.Result == nil {
		return resumeRestartServerChanged, nil
	}
	return resumeReupload, nil
}
