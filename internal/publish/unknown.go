package publish

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sau/internal/history"
	"sau/internal/statefile"
)

// unknownQuestion shows exactly what was sent and warns that the API cannot
// answer this question: a fresh publication is invisible there for minutes,
// so "not in the API" is not evidence of anything.
func unknownQuestion(st *UploadState) string {
	var b strings.Builder
	b.WriteString("A previous run sent the translation form but never learned the outcome.\n")
	if !st.SubmittedAt.IsZero() {
		b.WriteString("Sent at: " + st.SubmittedAt.Format(time.RFC3339) + "\n")
	}
	if s := st.Snapshot; s != nil {
		fmt.Fprintf(&b, "Series: %d\nEpisode: %s (%s)\nType: %s\nAuthors: %s\n",
			s.SeriesID, s.EpisodeNumber, s.EpisodeType, s.Type, s.Authors)
		fmt.Fprintf(&b, "Added by author: %v\nChannel: %s\nVideo: %s\n",
			s.AddedByAuthor, s.Channel, s.VideoName)
		if s.SubName != "" {
			b.WriteString("Subtitles: " + s.SubName + "\n")
		}
	}
	b.WriteString("Warning: absence in API does not prove the form was not submitted.\n")
	b.WriteString("Check the site. Is this translation already published?")
	return b.String()
}

// askUnknown resolves the submit_unknown phase with a human. It reports true
// when the publication is confirmed and the run is over.
func (r *Runner) askUnknown(key string, st *UploadState) (bool, error) {
	if r.Report == nil {
		return false, &UnknownOutcomeError{State: st}
	}
	ok, err := r.Report.Ask(unknownQuestion(st))
	if err != nil {
		// No console: the dedicated resolve command with explicit flags is the
		// way out. The phase stays as it is.
		return false, &UnknownOutcomeError{State: st}
	}
	if !ok {
		st.Phase = PhaseUploaded
		if err := r.State.Save(key, st); err != nil {
			return false, err
		}
		r.info("resending the form: a fresh page, the server and channel check, then submit")
		return false, nil
	}
	return true, r.closeSubmitted(key, st)
}

// closeSubmitted records the publication from the snapshot and drops the state.
// The translation id stays zero: it only ever comes from the redirect.
func (r *Runner) closeSubmitted(key string, st *UploadState) error {
	if r.History.Path != "" {
		var rec history.Record
		rec.At = r.now()
		if s := st.Snapshot; s != nil {
			rec.Series.ID = s.SeriesID
			rec.Episode.Number = s.EpisodeNumber
			rec.Episode.Type = s.EpisodeType
			rec.Translation.Type = s.Type
			rec.Translation.Authors = s.Authors
			rec.Upload.Channel = s.Channel
			rec.Upload.File = s.VideoName
		}
		rec.Upload.ServerID = st.ServerID
		rec.Upload.Started = st.StartedAt
		rec.Upload.Finished = st.SubmittedAt
		if st.Video != nil && st.Video.Result != nil {
			rec.Upload.Size = st.Video.Result.Size
			rec.Upload.Parts = st.Video.Result.Parts
		}
		if err := r.History.Append(rec); err != nil {
			return err
		}
	}
	return r.State.Delete(key)
}

// Resolve settles an unknown outcome without a dialog, for environments with
// no console at all.
func (r *Runner) Resolve(ctx context.Context, path string, submitted bool) error {
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
	if !st.Phase.NeedsDecision() {
		return fmt.Errorf("publish: %s: the state is in phase %q, there is no unknown outcome to resolve",
			path, st.Phase)
	}
	if submitted {
		return r.closeSubmitted(key, st)
	}
	st.Phase = PhaseUploaded
	return r.State.Save(key, st)
}
