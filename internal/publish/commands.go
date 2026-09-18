package publish

import (
	"context"
	"errors"
	"sort"

	"sau/internal/fineup"
	"sau/internal/site"
	"sau/internal/statefile"
)

// deleteAddress is where a finished upload can be removed. The finalizing host
// is the one that holds the parts; the base endpoint is only a fallback for a
// state written before any chunk was finalized.
func deleteAddress(fu *FileUpload) string {
	if fu == nil || fu.Result == nil {
		return ""
	}
	if fu.Result.LastHost != "" {
		return fu.Result.LastHost
	}
	return fu.Result.Endpoint
}

// discardRemote deletes from the upload server whatever the state points at.
//
// A finished upload records the host that finalized it, and that host is used
// directly. An unfinished one records nothing: hosts are never stored, because
// they are routes that change without the server changing. The only way to
// address those chunks is a fresh form, and it counts only if it still names
// the server the chunks went to. Mirrors of one server share its storage, so
// any of its addresses will do.
//
// Failures are reported and swallowed: leftovers expire on their own, and
// refusing to abort because of them would be worse.
func (r *Runner) discardRemote(ctx context.Context, st *UploadState, seriesID int) {
	var form *site.CreateForm

	for _, fu := range []*FileUpload{st.Video, st.Sub} {
		if fu == nil || fu.UUID == "" {
			continue
		}
		addr := deleteAddress(fu)
		if addr == "" {
			if form == nil {
				id := st.SeriesID
				if id == 0 {
					id = seriesID
				}
				f, err := r.Site.CreateForm(ctx, id, st.Channel)
				if err != nil {
					r.warn("could not fetch a form to delete the unfinished upload: " + err.Error())
					return
				}
				form = &f
			}
			if form.Video.ServerID != st.ServerID {
				r.warn("the unfinished chunks are unreachable on a different server; leaving them to expire")
				return
			}
			cfg := form.Video
			if fu == st.Sub {
				cfg = form.Sub
			}
			addr = fineup.Endpoint(cfg.ServerURL)
		}
		if derr := r.Uploader.Delete(ctx, addr, fu.UUID); derr != nil {
			// A leftover on the server is not worth keeping the state for.
			r.warn("could not delete " + fu.UUID + " from the server: " + derr.Error())
		}
	}
}

// Abort removes what was uploaded from the upload server and drops the state.
func (r *Runner) Abort(ctx context.Context, path string) error {
	key, err := statefile.Key(path)
	if err != nil {
		return err
	}
	st, err := r.loadState(key)
	if err != nil {
		return err
	}
	if st == nil {
		return statefile.ErrNotFound
	}
	if st.Phase.NeedsDecision() {
		// The snapshot in this state is the only record of what went out.
		// Deleting it, or the chunks, before the question is answered would
		// destroy the evidence the answer depends on.
		return &PendingDecisionError{State: st}
	}
	r.discardRemote(ctx, st, 0)
	return r.State.Delete(key)
}

// Status lists every unfinished upload.
func (r *Runner) Status() ([]UploadState, error) {
	keys, err := r.State.Keys()
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	out := make([]UploadState, 0, len(keys))
	for _, k := range keys {
		var st UploadState
		if lerr := r.State.Load(k, &st); lerr != nil {
			if errors.Is(lerr, statefile.ErrNotFound) {
				continue
			}
			return nil, lerr
		}
		out = append(out, st)
	}
	return out, nil
}
