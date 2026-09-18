// Package publish is the publication scenario: log in, fetch the form, upload,
// build the hidden field, submit. It is the only package that knows the order
// of the steps and the only one that owns the resume state.
package publish

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"sau/internal/fineup"
	"sau/internal/history"
	"sau/internal/secrets"
	"sau/internal/site"
	"sau/internal/statefile"
	"sau/internal/translation"
)

// Site is the slice of the site client this scenario needs.
type Site interface {
	Login(ctx context.Context, user string, pass secrets.Secret) error
	CreateForm(ctx context.Context, seriesID int, ch translation.Channel) (site.CreateForm, error)
	Submit(ctx context.Context, f site.CreateForm, d translation.Draft, ch translation.Channel, up site.UploadedFields) (site.SubmitResult, error)
	FindPublished(ctx context.Context, seriesID int, episode string, t translation.TranslationType, authors string) ([]site.Translation, error)
}

// Uploader is the slice of the chunked uploader this scenario needs.
type Uploader interface {
	Upload(ctx context.Context, s fineup.Spec, on func(fineup.Event)) (fineup.Result, error)
	Delete(ctx context.Context, endpoint, uuid string) error
}

// StateStore persists resume state. Load returns statefile.ErrNotFound when
// there is nothing stored for the key.
type StateStore interface {
	Load(key string, v any) error
	Save(key string, v any) error
	Delete(key string) error
	Keys() ([]string, error)
}

// Reporter is the user-facing side: progress, warnings and the one question
// this program is allowed to ask.
//
// Begin announces the file that is about to be uploaded, once per file and
// before its first chunk. It is the only way the progress display learns the
// total size: a fineup.Event carries the size of its own chunk and nothing more.
type Reporter interface {
	Begin(name string, total int64, parts int)
	Info(msg string)
	Warn(msg string)
	Event(e fineup.Event)
	Ask(question string) (bool, error)
}

type Phase string

const (
	PhaseUploading Phase = "uploading"
	PhaseUploaded  Phase = "uploaded"
	// PhaseSubmitting is written before the form goes out and cleared only
	// once the state is dropped. A run that dies anywhere in between leaves
	// this marker, which reads as "may have been sent" and is settled by a
	// human, never by an automatic retry.
	PhaseSubmitting    Phase = "submitting"
	PhaseSubmitUnknown Phase = "submit_unknown"
)

// NeedsDecision reports whether a saved state describes a form that may
// already be on the site. Both phases mean the same thing to the next run:
// ask, do not send. cli uses it to keep such files out of a batch.
func (p Phase) NeedsDecision() bool {
	return p == PhaseSubmitting || p == PhaseSubmitUnknown
}

type FileUpload struct {
	UUID       string
	PartSize   int64
	TotalParts int
	Done       []int
	Result     *fineup.Result
}

// Snapshot is what was sent, minus the token and the hidden field: without it
// there is nothing to show the human when the outcome is unknown.
type Snapshot struct {
	SeriesID      int
	EpisodeNumber string
	EpisodeType   string
	Type          string
	Authors       string
	AddedByAuthor bool
	Channel       string
	VideoName     string
	SubName       string
}

// UploadState stores the server id and the channel, never the host list: the
// hosts are routes to a server and change without the server changing.
type UploadState struct {
	Path        string
	SeriesID    int
	Size        int64
	ModTime     time.Time
	Phase       Phase
	ServerID    int
	Channel     translation.Channel
	Video       *FileUpload
	Sub         *FileUpload
	Snapshot    *Snapshot
	SubmittedAt time.Time
	StartedAt   time.Time
}

type Request struct {
	Draft       translation.Draft
	Channel     translation.Channel
	User        string
	Password    secrets.Source
	Fresh       bool
	DryRun      bool
	NoSubmit    bool
	Concurrency int
}

type Outcome struct {
	TranslationID int
	VideoField    string
	SubField      string
	State         *UploadState
}

// Runner performs publications. One Runner may be driven by several Run calls
// at once (RunBatch does this): the collaborators are shared and the form
// submissions are serialized by the submit lock, so that the CSRF token of a
// freshly loaded page is used before another run loads the next one.
type Runner struct {
	Site     Site
	Uploader Uploader
	State    StateStore
	History  history.Log
	Report   Reporter
	Now      func() time.Time
	Stat     func(string) (os.FileInfo, error)

	// submitMu serializes "fresh form → Submit → phase write". It is a
	// pointer so that the per-item runners of a batch, each with its own
	// reporter, share one lock; submitLock builds it on first use.
	submitMu *sync.Mutex
	// loginMu serializes the recovery of a dead session for the same reason:
	// the runs of a batch share one cookie jar, and two logins interleaved
	// on it (the login page fetched by one, posted by the other with a
	// stale token) fail for no good reason.
	loginMu  *sync.Mutex
	lockOnce sync.Once
}

// submitLock returns the mutex that serializes form submissions, building it
// on first use. A runner built with a lock already set keeps it.
func (r *Runner) submitLock() *sync.Mutex {
	r.locks()
	return r.submitMu
}

// loginLock returns the mutex that serializes session recovery, building it
// on first use. A runner built with a lock already set keeps it.
func (r *Runner) loginLock() *sync.Mutex {
	r.locks()
	return r.loginMu
}

func (r *Runner) locks() {
	r.lockOnce.Do(func() {
		if r.submitMu == nil {
			r.submitMu = &sync.Mutex{}
		}
		if r.loginMu == nil {
			r.loginMu = &sync.Mutex{}
		}
	})
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) stat(p string) (os.FileInfo, error) {
	if r.Stat != nil {
		return r.Stat(p)
	}
	return os.Stat(p)
}

func (r *Runner) info(msg string) {
	if r.Report != nil {
		r.Report.Info(msg)
	}
}

func (r *Runner) warn(msg string) {
	if r.Report != nil {
		r.Report.Warn(msg)
	}
}

// begin announces a file to the reporter before its first chunk goes out. The
// total size and the number of chunks are known only after PlanParts, and they
// cannot be derived from the events: an event carries the size of its own chunk.
func (r *Runner) begin(path string, partSize int64) error {
	if r.Report == nil {
		return nil
	}
	fi, err := r.stat(path)
	if err != nil {
		return err
	}
	parts, err := fineup.PlanParts(fi.Size(), partSize)
	if err != nil {
		return err
	}
	r.Report.Begin(filepath.Base(path), fi.Size(), len(parts))
	return nil
}

// newUUID returns a uuid v4 in the canonical 8-4-4-4-12 form. publish owns the
// upload id because fineup cannot report the one it generated when the upload
// fails, and without the id the chunks already on the server are unreachable.
//
// It panics rather than returning an error: a failure of crypto/rand is fatal.
// The same signature is used in fineup and site, deliberately.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("publish: crypto/rand failed: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// tracker accumulates the upload measurements that go into the history record.
// The uploader calls the event hook from every chunk goroutine, so the tracker
// guards itself instead of trusting the caller.
type tracker struct {
	mu      sync.Mutex
	hosts   []string
	seen    map[string]bool
	retries int
}

func newTracker() *tracker { return &tracker{seen: map[string]bool{}} }

func (t *tracker) add(e fineup.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch e.Kind {
	case fineup.EventChunkDone:
		if e.Host != "" && !t.seen[e.Host] {
			t.seen[e.Host] = true
			t.hosts = append(t.hosts, e.Host)
		}
	case fineup.EventChunkFailed:
		t.retries++
	}
}

func (t *tracker) snapshot() (hosts []string, retries int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.hosts...), t.retries
}

func (r *Runner) loadState(key string) (*UploadState, error) {
	var st UploadState
	err := r.State.Load(key, &st)
	if errors.Is(err, statefile.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// createForm fetches a fresh form page. Upload servers are never cached, so
// this happens before every upload and before the submit. A dead session is
// an ordinary error here: one recovery, then give up.
func (r *Runner) createForm(ctx context.Context, req Request) (site.CreateForm, error) {
	f, err := r.Site.CreateForm(ctx, req.Draft.SeriesID, req.Channel)
	if !errors.Is(err, site.ErrNotAuthorized) {
		return f, err
	}
	err = r.recoverSession(ctx, req, func() error {
		f, err = r.Site.CreateForm(ctx, req.Draft.SeriesID, req.Channel)
		return err
	})
	if err != nil {
		return site.CreateForm{}, err
	}
	return f, nil
}

// recoverSession is called after a request answered with the login page. It
// is the single place that turns a dead session back into a live one, shared
// by the form fetch and the submit, so that both spend exactly one login and
// report the same error.
//
// Under the login lock it first repeats the request: in a batch another run
// may have logged in already, and that login is reused rather than repeated.
// Only when the repeat still answers with the login page does it log in and
// try once more; a third login page is ErrAuth.
func (r *Runner) recoverSession(ctx context.Context, req Request, try func() error) error {
	mu := r.loginLock()
	mu.Lock()
	defer mu.Unlock()

	err := try()
	if !errors.Is(err, site.ErrNotAuthorized) {
		return err
	}
	if lerr := r.relogin(ctx, req); lerr != nil {
		return lerr
	}
	err = try()
	if errors.Is(err, site.ErrNotAuthorized) {
		return ErrAuth
	}
	return err
}

// relogin fetches the password and logs in once. Callers hold the login lock.
func (r *Runner) relogin(ctx context.Context, req Request) error {
	if req.Password == nil {
		return ErrAuth
	}
	pass, ok, perr := req.Password.Password(ctx)
	if perr != nil {
		return fmt.Errorf("%w: %v", ErrAuth, perr)
	}
	if !ok {
		return ErrAuth
	}
	if lerr := r.Site.Login(ctx, req.User, pass); lerr != nil {
		if errors.Is(lerr, site.ErrNotAuthorized) {
			return ErrAuth
		}
		return lerr
	}
	return nil
}

func checkExt(path string, allowed []string) error {
	if translation.HasAllowedExt(path, allowed) {
		return nil
	}
	return fmt.Errorf("publish: %s: extension %q is not allowed, expected one of %s",
		filepath.Base(path), strings.TrimPrefix(filepath.Ext(path), "."), strings.Join(allowed, ", "))
}

// runCtx is everything one Run carries from step to step: the request, the
// state and where it is stored, and the measurements the history record needs.
type runCtx struct {
	req      Request
	key      string
	path     string
	st       *UploadState
	form     site.CreateForm
	tr       *tracker
	saveOnce sync.Once
}

// saveState writes the resume state and reports a failure once per run. The
// state is the only thing standing between a broken connection and a re-upload
// from zero, so a store that cannot be written is worth saying out loud even
// where the run can carry on.
func (r *Runner) saveState(rc *runCtx) error {
	err := r.State.Save(rc.key, rc.st)
	if err != nil {
		rc.saveOnce.Do(func() {
			r.warn("could not save the resume state: " + err.Error())
		})
	}
	return err
}

// isCancellation reports whether the error is the caller giving up rather than
// the upload failing. The two have different exit codes, so they must not be
// merged into one.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// uploadWithRecovery uploads one file. It recovers once from an exhausted host
// pool (fresh form, new hosts, keep the chunks already there) and once from a
// failed finalize (full re-upload under the same id). A second failure of the
// same kind stops the run with the state on disk.
func (r *Runner) uploadWithRecovery(ctx context.Context, rc *runCtx, form *site.CreateForm,
	video bool, path string, fu *FileUpload) (fineup.Result, error) {

	if fu.PartSize == 0 {
		fu.PartSize = fineup.DefaultPartSize
	}
	// Once per file, before the first chunk: a recovery below does not repeat it,
	// because it is the same file and the same total.
	if err := r.begin(path, fu.PartSize); err != nil {
		return fineup.Result{}, err
	}

	poolRetried, finalizeRetried := false, false
	for {
		cfg := form.Video
		if !video {
			cfg = form.Sub
		}
		if fu.UUID == "" {
			fu.UUID = newUUID()
		}
		spec := fineup.Spec{
			Path:     path,
			Base:     fineup.Endpoint(cfg.ServerURL),
			Pool:     cfg.ServerURLs,
			UUID:     fu.UUID,
			PartSize: fu.PartSize,
			Done:     fu.Done,
			MaxConns: rc.req.Concurrency,
		}
		res, err := r.Uploader.Upload(ctx, spec, r.onEvent(rc, fu))
		if err == nil {
			fu.TotalParts = res.Parts
			fu.Result = &res
			return res, nil
		}
		if isCancellation(err) {
			// Interrupted, not broken: pass it through untouched so that the
			// caller can tell the two apart. Whatever arrived is on disk.
			_ = r.saveState(rc)
			return fineup.Result{}, err
		}

		var pool *fineup.PoolExhaustedError
		var finalize *fineup.FinalizeError
		switch {
		case errors.As(err, &pool) && !poolRetried:
			poolRetried = true
			// PoolExhaustedError.Done is the authoritative list of what the
			// server holds: it already includes the indexes this run started
			// from, and it is meant to be handed back as the next Spec.Done.
			fu.Done = append([]int(nil), pool.Done...)
			sort.Ints(fu.Done)
			r.warn("every upload host failed; fetching a fresh form and continuing with the new hosts")
			fresh, ferr := r.createForm(ctx, rc.req)
			if ferr != nil {
				_ = r.saveState(rc)
				return fineup.Result{}, ferr
			}
			rerr := r.afterFreshForm(rc.st, fresh, rc.req.Channel)
			*form = fresh
			if rerr != nil {
				// Including errServerChanged: the caller restarts both files
				// against the form just installed.
				_ = r.saveState(rc)
				return fineup.Result{}, rerr
			}

		case errors.As(err, &finalize) && !finalizeRetried:
			finalizeRetried = true
			// There is no way to ask the server what it already has, and the
			// lifetime of a half-uploaded file is unknown.
			r.warn("finalizing the upload failed; re-uploading the whole file under the same id")
			fu.Done = nil

		default:
			// Give up, but leave behind exactly what the server holds, so the
			// next run resumes where this one stopped.
			if pool != nil {
				fu.Done = append([]int(nil), pool.Done...)
				sort.Ints(fu.Done)
			}
			if serr := r.saveState(rc); serr != nil {
				return fineup.Result{}, serr
			}
			return fineup.Result{}, &UploadFailedError{Err: err}
		}
		if serr := r.saveState(rc); serr != nil {
			return fineup.Result{}, serr
		}
	}
}

// errServerChanged reports that a form fetched mid-upload named a different
// upload server, so everything uploaded so far is on the wrong one and the
// whole upload has to start again, video first.
var errServerChanged = errors.New("publish: the upload server changed mid-upload")

// afterFreshForm re-applies the resume table to a form fetched mid-upload.
//
// A server change resets both files, not just the one being uploaded. The two
// hidden fields are built from one form and each names the server it was
// fetched from, so a video left on the old server would be announced under the
// new one, where it does not exist. That failure would surface only after the
// submit, with hours of upload already spent.
func (r *Runner) afterFreshForm(st *UploadState, f site.CreateForm, ch translation.Channel) error {
	d, err := decideResume(st, f, ch, st.Size, st.ModTime)
	if err != nil {
		return err
	}
	if d == resumeContinue {
		return nil
	}
	r.warn("the upload server changed while uploading; starting both files over on the new server")
	for _, fu := range []*FileUpload{st.Video, st.Sub} {
		if fu == nil {
			continue
		}
		fu.UUID = ""
		fu.Done = nil
		fu.Result = nil
		fu.TotalParts = 0
	}
	st.ServerID = f.Video.ServerID
	return errServerChanged
}

// onEvent builds the upload event hook. The uploader calls it from every chunk
// goroutine, so the state it mutates is under a mutex. The state is written
// before the event is reported: by the time anything reacts to an accepted
// chunk, that chunk is already on disk. Reporting happens outside the lock,
// because a slow display must not serialize the upload.
func (r *Runner) onEvent(rc *runCtx, fu *FileUpload) func(fineup.Event) {
	var mu sync.Mutex
	return func(e fineup.Event) {
		rc.tr.add(e)
		if e.Kind == fineup.EventChunkDone {
			mu.Lock()
			fu.Done = appendDone(fu.Done, e.Part)
			rc.st.Phase = PhaseUploading
			// A failed save is already reported by saveState; the upload keeps
			// going, because the chunks themselves are fine.
			_ = r.saveState(rc)
			mu.Unlock()
		}
		if r.Report != nil {
			r.Report.Event(e)
		}
	}
}

// appendDone adds an index once, keeping the slice sorted.
func appendDone(done []int, part int) []int {
	for _, d := range done {
		if d == part {
			return done
		}
	}
	done = append(done, part)
	sort.Ints(done)
	return done
}

func uploadedFile(path string, res fineup.Result) *site.UploadedFile {
	name := res.Name
	if name == "" {
		name = filepath.Base(path)
	}
	return &site.UploadedFile{Name: name, UUID: res.UUID, Size: res.Size}
}

func snapshotOf(req Request, up site.UploadedFields) *Snapshot {
	s := &Snapshot{
		SeriesID:      req.Draft.SeriesID,
		EpisodeNumber: req.Draft.EpisodeNumber,
		EpisodeType:   string(req.Draft.EpisodeType),
		Type:          string(req.Draft.Type),
		Authors:       req.Draft.Authors,
		AddedByAuthor: req.Draft.AddedByAuthor,
		Channel:       req.Channel.String(),
	}
	if up.Video != nil {
		s.VideoName = up.Video.Name
	}
	if up.Sub != nil {
		s.SubName = up.Sub.Name
	}
	return s
}

// appendHistory writes one record per successful publication. Titles stay
// empty on purpose: they would require an API call, and the success branch
// must not touch the API.
func (r *Runner) appendHistory(req Request, st *UploadState, res site.SubmitResult,
	up fineup.Result, tr *tracker, finished time.Time) error {

	if r.History.Path == "" {
		return nil
	}
	hosts, retries := tr.snapshot()
	var rec history.Record
	rec.At = finished
	rec.Series.ID = req.Draft.SeriesID
	rec.Episode.Number = req.Draft.EpisodeNumber
	rec.Episode.Type = string(req.Draft.EpisodeType)
	rec.Translation.ID = res.TranslationID
	rec.Translation.Type = string(req.Draft.Type)
	rec.Translation.Authors = req.Draft.Authors
	rec.Translation.URL = res.Location
	rec.Upload.File = up.Name
	rec.Upload.Size = up.Size
	rec.Upload.Parts = up.Parts
	rec.Upload.Started = st.StartedAt
	rec.Upload.Finished = finished
	rec.Upload.Duration = finished.Sub(st.StartedAt)
	if secs := rec.Upload.Duration.Seconds(); secs > 0 {
		rec.Upload.BytesPerSec = float64(up.Size) / secs
	}
	rec.Upload.Retries = retries
	rec.Upload.Hosts = hosts
	rec.Upload.Channel = req.Channel.String()
	rec.Upload.ServerID = st.ServerID
	return r.History.Append(rec)
}

// prepareState is the first of the three steps of a run: settle any unfinished
// business with the human, fetch a fresh form, apply the resume table and put
// the state on disk. A non-nil Outcome means the run is over before it began,
// which is what a dry run and a settled submission both look like.
func (r *Runner) prepareState(ctx context.Context, req Request, key string) (*runCtx, *Outcome, error) {
	path := req.Draft.VideoPath

	st, err := r.loadState(key)
	if err != nil {
		return nil, nil, err
	}
	if req.Fresh && st != nil {
		if st.Phase.NeedsDecision() {
			// Starting over would erase the snapshot of a form that may
			// already have been accepted, and with it any chance of telling
			// a duplicate from a fresh publication.
			return nil, nil, &PendingDecisionError{State: st}
		}
		// Starting over abandons whatever is on the server; deleting it first
		// keeps half-uploaded files from piling up under the account.
		r.discardRemote(ctx, st, req.Draft.SeriesID)
		if err := r.State.Delete(key); err != nil {
			return nil, nil, err
		}
		st = nil
	}

	// A submission whose outcome nobody knows is settled before anything
	// touches the network.
	if st != nil && st.Phase.NeedsDecision() {
		done, err := r.askUnknown(key, st)
		if err != nil {
			return nil, nil, err
		}
		if done {
			return nil, &Outcome{}, nil
		}
	}

	fi, err := r.stat(path)
	if err != nil {
		return nil, nil, err
	}

	form, err := r.createForm(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	if err := checkExt(path, form.Video.AllowedExt); err != nil {
		return nil, nil, err
	}
	if req.Draft.SubPath != "" {
		if err := checkExt(req.Draft.SubPath, form.Sub.AllowedExt); err != nil {
			return nil, nil, err
		}
	}

	d, err := decideResume(st, form, req.Channel, fi.Size(), fi.ModTime())
	if err != nil {
		// A channel mismatch keeps the state: the user decides.
		return nil, nil, err
	}
	switch d {
	case resumeContinue:
		r.info("resuming the saved upload")
	case resumeRestartFileChanged:
		r.warn("the file changed since the saved upload; starting over")
		st = nil
	case resumeRestartServerChanged:
		r.warn("the upload server changed and the file is not assembled; starting over")
		st = nil
	case resumeReupload:
		r.warn("the upload server changed; re-uploading the assembled file")
		st = nil
	}
	if st == nil {
		st = &UploadState{Path: path, StartedAt: r.now()}
	}
	st.Size = fi.Size()
	st.ModTime = fi.ModTime()
	st.Phase = PhaseUploading
	st.SeriesID = req.Draft.SeriesID
	st.ServerID = form.Video.ServerID
	st.Channel = req.Channel
	if st.StartedAt.IsZero() {
		st.StartedAt = r.now()
	}
	if st.Video == nil {
		st.Video = &FileUpload{PartSize: fineup.DefaultPartSize}
	}
	if req.Draft.SubPath != "" && st.Sub == nil {
		st.Sub = &FileUpload{PartSize: fineup.DefaultPartSize}
	}

	rc := &runCtx{req: req, key: key, path: path, st: st, form: form, tr: newTracker()}

	if req.DryRun {
		parts, perr := fineup.PlanParts(st.Size, st.Video.PartSize)
		if perr != nil {
			return nil, nil, perr
		}
		st.Video.TotalParts = len(parts)
		// The duplicate check lives here only: a false negative from the API
		// is harmless in a dry run and dangerous anywhere else.
		found, ferr := r.Site.FindPublished(ctx, req.Draft.SeriesID, req.Draft.EpisodeNumber,
			req.Draft.Type, req.Draft.Authors)
		if ferr != nil {
			return nil, nil, ferr
		}
		if len(found) > 0 {
			r.warn("this episode by these authors is already published; publishing now would create a duplicate")
		}
		r.info(fmt.Sprintf("dry run: %d parts of %d bytes to server %d in channel %s",
			len(parts), st.Video.PartSize, form.Video.ServerID, req.Channel))
		return rc, &Outcome{State: st}, nil
	}

	if err := r.saveState(rc); err != nil {
		return nil, nil, err
	}
	return rc, nil, nil
}

// uploadFiles is the second step: the video, then the subtitles, onto one
// server. A server change discovered mid-upload restarts the whole thing,
// video first. One such restart is allowed: a server that keeps moving is not
// something more attempts would fix.
func (r *Runner) uploadFiles(ctx context.Context, rc *runCtx) (site.UploadedFields, fineup.Result, error) {
	const maxServerRestarts = 1
	var videoRes fineup.Result

	for restart := 0; ; restart++ {
		if restart > maxServerRestarts {
			return site.UploadedFields{}, fineup.Result{}, &UploadFailedError{Err: errServerChanged}
		}
		up := site.UploadedFields{}

		res, err := r.uploadWithRecovery(ctx, rc, &rc.form, true, rc.path, rc.st.Video)
		if errors.Is(err, errServerChanged) {
			continue
		}
		if err != nil {
			return site.UploadedFields{}, fineup.Result{}, err
		}
		videoRes = res
		up.Video = uploadedFile(rc.path, res)

		if rc.req.Draft.SubPath != "" {
			subRes, serr := r.uploadWithRecovery(ctx, rc, &rc.form, false, rc.req.Draft.SubPath, rc.st.Sub)
			if errors.Is(serr, errServerChanged) {
				continue
			}
			if serr != nil {
				return site.UploadedFields{}, fineup.Result{}, serr
			}
			up.Sub = uploadedFile(rc.req.Draft.SubPath, subRes)
		}
		return up, videoRes, nil
	}
}

// errStateLost means the submitting marker could not be written. The form is
// not sent in that case: without the marker on disk, a crash during the
// request would read as "never sent" and the next run would send it again.
var errStateLost = errors.New("publish: refusing to submit: the state could not be saved")

// submitOnce is the third step, one attempt of it. The marker goes to disk
// before the request leaves, so that everything from here on reads as "may
// have been sent" until an answer arrives.
func (r *Runner) submitOnce(ctx context.Context, rc *runCtx, form site.CreateForm,
	up site.UploadedFields) (site.SubmitResult, error) {

	rc.st.Phase = PhaseSubmitting
	rc.st.SubmittedAt = r.now()
	rc.st.Snapshot = snapshotOf(rc.req, up)
	if err := r.saveState(rc); err != nil {
		return site.SubmitResult{}, fmt.Errorf("%w: %v", errStateLost, err)
	}
	return r.Site.Submit(ctx, form, rc.req.Draft, rc.req.Channel, up)
}

// submitLocked is the third step: a fresh form for the CSRF token, then the
// submit with its retry rules. It runs under the submit lock, so that the
// runs of a batch send their forms one at a time; with a single run the lock
// is uncontended and the behaviour is exactly the sequential one.
//
// submitted is false when the form was deliberately not sent (NoSubmit); the
// outcome then carries the hidden fields. On an error the outcome carries the
// state, as before, and err says what happened.
func (r *Runner) submitLocked(ctx context.Context, rc *runCtx, up site.UploadedFields) (out Outcome, res site.SubmitResult, submitted bool, err error) {
	mu := r.submitLock()
	mu.Lock()
	defer mu.Unlock()
	req := rc.req

	// The CSRF token changes on every page load, so it must come from a form
	// fetched after the upload.
	form2, err := r.createForm(ctx, req)
	if err != nil {
		return Outcome{}, site.SubmitResult{}, false, err
	}
	videoField := site.EncodeUploadField(up.Video, form2.Video)
	subField := site.EncodeUploadField(up.Sub, form2.Sub)

	if req.NoSubmit {
		r.info("the file is uploaded; the form was not submitted (--no-submit)")
		return Outcome{VideoField: videoField, SubField: subField, State: rc.st}, site.SubmitResult{}, false, nil
	}

	const notSentAttempts = 3
	reloggedIn := false
	for attempt := 1; ; attempt++ {
		res, err = r.submitOnce(ctx, rc, form2, up)
		if err == nil {
			break
		}
		if errors.Is(err, errStateLost) {
			return Outcome{State: rc.st}, site.SubmitResult{}, false, err
		}
		if isCancellation(err) {
			// The state stays in the submitting phase, which is the safe
			// reading: the next run asks instead of sending again.
			return Outcome{State: rc.st}, site.SubmitResult{}, false, err
		}

		var rejected *site.RejectedError
		var unknown *site.UnknownOutcomeError
		var notSent *site.NotSentError
		switch {
		case errors.As(err, &rejected):
			// The site created nothing; show why and keep the upload.
			for _, m := range rejected.Messages {
				r.warn(m)
			}
			rc.st.Phase = PhaseUploaded
			_ = r.saveState(rc)
			return Outcome{State: rc.st}, site.SubmitResult{}, false, err

		case errors.As(err, &unknown):
			// The request left; nobody knows what happened on the other side.
			// An automatic retry would risk a duplicate publication, so this
			// phase has no automatic exit at all.
			rc.st.Phase = PhaseSubmitUnknown
			if serr := r.saveState(rc); serr != nil {
				return Outcome{}, site.SubmitResult{}, false, serr
			}
			r.warn("the form was sent but the outcome is unknown; it will be resolved by you, not automatically")
			return Outcome{State: rc.st}, site.SubmitResult{}, false, &UnknownOutcomeError{State: rc.st}

		case errors.Is(err, site.ErrNotAuthorized):
			// The site answered with the login page, which is its way of
			// saying the form was not accepted. Nothing was created, so the
			// marker comes back off and one recovery is safe. The token
			// belonged to the dead session, so a fresh form comes with it:
			// the form fetch is the request that is repeated, and it logs
			// in only if the session is still dead (another run of a batch
			// may have revived it already).
			rc.st.Phase = PhaseUploaded
			_ = r.saveState(rc)
			if reloggedIn {
				return Outcome{State: rc.st}, site.SubmitResult{}, false, ErrAuth
			}
			reloggedIn = true
			var fresh site.CreateForm
			ferr := r.recoverSession(ctx, req, func() error {
				var e error
				fresh, e = r.Site.CreateForm(ctx, req.Draft.SeriesID, req.Channel)
				return e
			})
			if ferr != nil {
				return Outcome{State: rc.st}, site.SubmitResult{}, false, ferr
			}
			form2 = fresh
			videoField = site.EncodeUploadField(up.Video, form2.Video)
			subField = site.EncodeUploadField(up.Sub, form2.Sub)
			continue

		case errors.As(err, &notSent) && attempt < notSentAttempts:
			// The request provably never left, so the marker can come back off.
			rc.st.Phase = PhaseUploaded
			_ = r.saveState(rc)
			r.warn(fmt.Sprintf("the form did not reach the server (attempt %d of %d); retrying",
				attempt, notSentAttempts))
			continue

		case errors.As(err, &notSent):
			// Out of attempts, but still the class that provably never left
			// the machine: the marker comes off and the upload is kept.
			rc.st.Phase = PhaseUploaded
			_ = r.saveState(rc)
			return Outcome{State: rc.st}, site.SubmitResult{}, false, &UploadFailedError{Err: err}

		default:
			// Fail closed. An error nobody classified could mean anything,
			// including a form that arrived; rolling the marker back would
			// invite the next run to send it a second time. The marker stays
			// and a human settles it.
			return Outcome{State: rc.st}, site.SubmitResult{}, false, &UploadFailedError{Err: err}
		}
	}
	return Outcome{VideoField: videoField, SubField: subField}, res, true, nil
}

// Run performs the whole publication: fresh form, upload, fresh form again for
// the CSRF, submit.
func (r *Runner) Run(ctx context.Context, req Request) (Outcome, error) {
	if req.Concurrency <= 0 {
		req.Concurrency = fineup.DefaultMaxConns
	}
	if err := req.Draft.Validate(); err != nil {
		return Outcome{}, err
	}
	key, err := statefile.Key(req.Draft.VideoPath)
	if err != nil {
		return Outcome{}, err
	}

	rc, done, err := r.prepareState(ctx, req, key)
	if err != nil {
		return Outcome{}, err
	}
	if done != nil {
		return *done, nil
	}

	up, videoRes, err := r.uploadFiles(ctx, rc)
	if err != nil {
		return Outcome{}, err
	}
	rc.st.Phase = PhaseUploaded
	if err := r.saveState(rc); err != nil {
		return Outcome{}, err
	}

	out, res, submitted, err := r.submitLocked(ctx, rc, up)
	if err != nil {
		return out, err
	}
	if !submitted {
		return out, nil
	}

	// From here the publication exists. Nothing below may fail the run: the
	// worst case is a state file left in the submitting phase, which the next
	// run settles with a question instead of a second submission.
	finished := r.now()
	if err := r.appendHistory(req, rc.st, res, videoRes, rc.tr, finished); err != nil {
		r.warn("could not write the history record: " + err.Error())
	}
	if err := r.State.Delete(key); err != nil {
		r.warn("could not drop the state file: " + err.Error() +
			"; it stays in the submitting phase and the next run will ask about it")
	}
	out.TranslationID = res.TranslationID
	return out, nil
}
