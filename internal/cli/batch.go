package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"sau/internal/config"
	"sau/internal/progress"
	"sau/internal/publish"
	"sau/internal/secrets"
	"sau/internal/site"
	"sau/internal/statefile"
	"sau/internal/translation"
)

// uploadItem is one file of the command line with everything resolved: the
// number, the subtitles, the draft and, later, the saved state.
type uploadItem struct {
	path    string
	episode string
	sub     string
	draft   translation.Draft
	state   *publish.UploadState // nil when there is nothing saved
}

func (it uploadItem) request(o uploadOpts, cfg config.Config, ch translation.Channel, pass secrets.Source) publish.Request {
	return publish.Request{
		Draft:       it.draft,
		Channel:     ch,
		User:        cfg.User,
		Password:    pass,
		Fresh:       o.fresh,
		DryRun:      o.dryRun,
		NoSubmit:    o.noSubmit,
		Concurrency: cfg.Concurrency,
	}
}

// episodeValue orders items by number: "10" comes after "3", "5.5" between
// 5 and 6. The drafts have been validated, so the number parses.
func (it uploadItem) episodeValue() float64 {
	v, _ := strconv.ParseFloat(it.episode, 64)
	return v
}

// planItems checks every file and resolves its number and subtitles. Any
// problem stops the command with a usage error before the site is touched:
// a file that does not exist, a number that cannot be determined, two files
// with one number, one file named twice.
func planItems(o uploadOpts, cfg config.Config, base translation.Draft) ([]uploadItem, error) {
	seenFile := map[string]bool{}
	seenEpisode := map[string]string{}
	items := make([]uploadItem, 0, len(o.videos))
	for _, path := range o.videos {
		// A mistyped path is a usage error, not a failed run. The runner stats
		// the file again when it needs the size; this check is only here so
		// that a typo reads like one. The subtitle file is left to the runner:
		// it is resolved against the upload, not against the command line.
		switch info, err := os.Stat(path); {
		case err != nil:
			return nil, usagef("cannot read the video file: %v", err)
		case info.IsDir():
			return nil, usagef("cannot read the video file: %s is a directory", path)
		}
		key, err := statefile.Key(path)
		if err != nil {
			return nil, err
		}
		if seenFile[key] {
			return nil, usagef("%s is given twice", path)
		}
		seenFile[key] = true

		name := filepath.Base(path)
		it := uploadItem{path: path, episode: o.episode, sub: o.sub}
		if it.episode == "" {
			it.episode, it.sub = resolveEpisode(name, cfg)
			if it.episode == "" {
				return nil, usagef("cannot determine the episode number of %s: use --episode, an [[episodes]] entry or episode_pattern in .sau.toml", name)
			}
			// The configuration resolves a relative sub against its own
			// file, which the project file names relative to the working
			// directory. Pin it now, before anything changes directory.
			if it.sub != "" && !filepath.IsAbs(it.sub) {
				abs, err := filepath.Abs(it.sub)
				if err != nil {
					return nil, err
				}
				it.sub = abs
			}
		}
		if other, dup := seenEpisode[it.episode]; dup {
			return nil, usagef("episode %s is given twice: %s and %s", it.episode, other, name)
		}
		seenEpisode[it.episode] = name

		it.draft = base
		it.draft.EpisodeNumber = it.episode
		it.draft.VideoPath = path
		it.draft.SubPath = it.sub
		if err := it.draft.Validate(); err != nil {
			return nil, usagef("%s: %v", name, err)
		}
		items = append(items, it)
	}
	return items, nil
}

// resolveEpisode finds the number of a file: an [[episodes]] entry first,
// the pattern second. It returns "" when neither applies.
func resolveEpisode(name string, cfg config.Config) (episode, sub string) {
	for _, o := range cfg.Episodes {
		if o.File == name {
			return o.Episode, o.Sub
		}
	}
	if cfg.EpisodePattern != nil {
		if m := cfg.EpisodePattern.FindStringSubmatch(name); m != nil {
			return trimEpisode(m[1]), ""
		}
	}
	return "", ""
}

// trimEpisode drops leading zeros: file names say "02", the site says "2".
func trimEpisode(s string) string {
	t := strings.TrimLeft(s, "0")
	if t == "" || strings.HasPrefix(t, ".") {
		t = "0" + t
	}
	return t
}

// runBatch is the upload command for several files, and for one file whose
// number came from the configuration: plan, question, batch, summary.
func runBatch(ctx context.Context, d Deps, o uploadOpts, cfg config.Config, ch translation.Channel,
	pass secrets.Source, runner publishRunner, items []uploadItem) error {

	if err := attachStates(runner, items, ch, o.fresh); err != nil {
		return err
	}
	slices.SortFunc(items, func(a, b uploadItem) int {
		return cmp.Compare(a.episodeValue(), b.episodeValue())
	})

	// Files whose last submission has no known outcome are not uploaded
	// again and not asked about here: they get their own list and the
	// command that settles them.
	var run, pending []uploadItem
	for _, it := range items {
		if it.state != nil && it.state.Phase.NeedsDecision() {
			pending = append(pending, it)
		} else {
			run = append(run, it)
		}
	}

	// The plan goes to stdout, except under --json, where stdout is the
	// array and nothing else. The question is still asked there unless
	// --yes, and nobody should answer it blind: the plan then goes to
	// stderr, next to the question.
	if len(run) > 0 {
		switch {
		case !o.asJSON:
			printPlan(d.Stdout, run, cfg, ch, o.parallel)
		case !o.yes && !o.dryRun:
			printPlan(d.Stderr, run, cfg, ch, o.parallel)
		}
	}
	for _, it := range pending {
		fmt.Fprintf(d.Stderr, "sau: %s: an earlier submission is unresolved, not in this batch; run: sau resolve %q --submitted | --resend\n",
			filepath.Base(it.path), it.path)
	}
	if len(run) == 0 {
		return nothingDone(d, o.asJSON)
	}

	if o.dryRun {
		// The existing dry run, one file after another; no question, because
		// nothing is at stake.
		results := make([]publish.BatchResult, 0, len(run))
		for _, it := range run {
			item := publish.BatchItem{Request: it.request(o, cfg, ch, pass)}
			out, err := runner.Run(ctx, item.Request)
			results = append(results, publish.BatchResult{Item: item, Outcome: &out, Err: err})
		}
		return printSummary(d, o.asJSON, results)
	}

	if !o.yes {
		ok, err := consoleFor(d).Ask("continue?")
		if err != nil || !ok {
			return nothingDone(d, o.asJSON)
		}
	}

	// The session is checked, and the login happens if it must, once and
	// before the parallel part: a password prompt in the middle of several
	// progress lines would be unreadable.
	if err := runner.CheckSession(ctx, run[0].request(o, cfg, ch, pass)); err != nil {
		return err
	}

	batch := make([]publish.BatchItem, 0, len(run))
	var multi *progress.Multi
	var stopMulti func()
	if o.parallel > 1 {
		// The items must not prompt: the session was just checked, so a
		// relogin in the middle of the batch can only use a stored password.
		pass = buildStoredPassword(cfg, d)
		if progress.IsTerminal(d.Stderr) {
			multi = &progress.Multi{W: d.Stderr, Interval: progress.DefaultMultiInterval}
			mctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				multi.Run(mctx)
			}()
			stopMulti = func() {
				cancel()
				<-done
			}
		}
		for _, it := range run {
			batch = append(batch, publish.BatchItem{
				Request: it.request(o, cfg, ch, pass),
				Report:  &batchReporter{d: d, multi: multi, name: filepath.Base(it.path)},
			})
		}
	} else {
		for _, it := range run {
			batch = append(batch, publish.BatchItem{Request: it.request(o, cfg, ch, pass)})
		}
	}

	results := runner.RunBatch(ctx, batch, o.parallel)
	if stopMulti != nil {
		stopMulti()
	}
	// With one worker the runs drove the single bar, and the one of the last
	// file is still redrawing. Run stops it too, but only after this returns,
	// and the summary is printed here: take it down first, or the header
	// lands on the bar line.
	stopProgress()
	return printSummary(d, o.asJSON, results)
}

// attachStates reads the saved state of every item. A file saved in another
// channel stops the command, exactly as it does for one file: the chunks are
// not reachable in the requested channel. With fresh the runner discards the
// state before it looks at the channel, so the comparison is skipped and the
// item is planned as new; a state that needs a decision is kept regardless,
// because the runner refuses to discard that one.
func attachStates(runner publishRunner, items []uploadItem, ch translation.Channel, fresh bool) error {
	states, err := runner.Status()
	if err != nil {
		return err
	}
	byKey := map[string]publish.UploadState{}
	for _, st := range states {
		if k, err := statefile.Key(st.Path); err == nil {
			byKey[k] = st
		}
	}
	for i := range items {
		k, err := statefile.Key(items[i].path)
		if err != nil {
			return err
		}
		st, ok := byKey[k]
		if !ok {
			continue
		}
		if fresh && !st.Phase.NeedsDecision() {
			continue
		}
		if fresh {
			items[i].state = &st
			continue
		}
		if st.Channel != ch {
			return &publish.ChannelMismatchError{Saved: st.Channel, Requested: ch}
		}
		items[i].state = &st
	}
	return nil
}

func nothingDone(d Deps, asJSON bool) error {
	if asJSON {
		return printJSON(d.Stdout, []batchResultView{})
	}
	fmt.Fprintln(d.Stdout, "nothing done")
	return nil
}

func printPlan(w io.Writer, items []uploadItem, cfg config.Config, ch translation.Channel, parallel int) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "FILE\tEPISODE\tSUB\tSTATE")
	for _, it := range items {
		sub := "-"
		if it.sub != "" {
			sub = filepath.Base(it.sub)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", filepath.Base(it.path), it.episode, sub, planState(it.state))
	}
	tw.Flush()
	fmt.Fprintf(w, "series %d, %s, %s, channel %s, parallel %d\n", cfg.SeriesID, cfg.Type, cfg.Authors, ch, parallel)
}

// planState describes what the runner will do with the saved state. States
// that need a decision never reach the table: runBatch diverts them first.
func planState(st *publish.UploadState) string {
	switch {
	case st == nil:
		return "new"
	case st.Phase == publish.PhaseUploaded:
		return "uploaded, submit only"
	case st.Video != nil && st.Video.TotalParts > 0:
		return fmt.Sprintf("resume %d%%", len(st.Video.Done)*100/st.Video.TotalParts)
	default:
		return "resume"
	}
}

// batchResultView is one row of the summary, and one element of the --json
// array.
type batchResultView struct {
	File          string `json:"file"`
	Episode       string `json:"episode"`
	Status        string `json:"status"`
	TranslationID int    `json:"translationId,omitempty"`
	Error         string `json:"error,omitempty"`
}

func viewOf(r publish.BatchResult) batchResultView {
	v := batchResultView{
		File:    filepath.Base(r.Item.Request.Draft.VideoPath),
		Episode: r.Item.Request.Draft.EpisodeNumber,
		Status:  resultStatus(r),
	}
	if r.Outcome != nil {
		v.TranslationID = r.Outcome.TranslationID
	}
	if r.Err != nil {
		v.Error = r.Err.Error()
	}
	return v
}

// resultStatus is the RESULT column: one line, the site's first message for
// a rejection, the first line of the question for a refused decision. It is
// the one-line twin of reportError in errors.go: a new error type goes into
// both.
func resultStatus(r publish.BatchResult) string {
	err := r.Err
	if r.Question != "" {
		return "needs decision: " + firstLine(r.Question)
	}
	if err == nil {
		switch {
		case r.Outcome != nil && r.Outcome.TranslationID > 0:
			return fmt.Sprintf("ok, translation %d", r.Outcome.TranslationID)
		case r.Item.Request.DryRun:
			return "ok, dry run"
		case r.Item.Request.NoSubmit:
			return "ok, not submitted"
		default:
			return "ok"
		}
	}
	var rejected *site.RejectedError
	var uploadFailed *publish.UploadFailedError
	var unknown *publish.UnknownOutcomeError
	var pending *publish.PendingDecisionError
	var mismatch *publish.ChannelMismatchError
	switch {
	case errors.Is(err, publish.ErrCancelled):
		return "cancelled"
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return "interrupted (state kept)"
	case errors.Is(err, publish.ErrAuth) || errors.Is(err, site.ErrNotAuthorized):
		return "authorization failed"
	case errors.As(err, &rejected):
		if len(rejected.Messages) > 0 {
			return "rejected: " + rejected.Messages[0]
		}
		return "rejected"
	case errors.As(err, &uploadFailed):
		return "upload failed (state kept)"
	case errors.As(err, &unknown), errors.As(err, &pending):
		return "outcome unknown, run sau resolve"
	case errors.As(err, &mismatch):
		return fmt.Sprintf("channel mismatch: saved in %s", mismatch.Saved)
	default:
		return "error: " + err.Error()
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// printSummary prints the table or the JSON array and turns the results into
// the exit code of the command.
func printSummary(d Deps, asJSON bool, results []publish.BatchResult) error {
	views := make([]batchResultView, 0, len(results))
	for _, r := range results {
		views = append(views, viewOf(r))
	}
	if asJSON {
		if err := printJSON(d.Stdout, views); err != nil {
			return err
		}
	} else {
		tw := tabwriter.NewWriter(d.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "EPISODE\tRESULT")
		for _, v := range views {
			fmt.Fprintf(tw, "%s\t%s\n", v.Episode, v.Status)
		}
		tw.Flush()
	}

	code, failed := batchExitCode(results)
	if code == ExitOK {
		return nil
	}
	return &batchError{code: code, failed: failed, total: len(results)}
}

// severity orders the exit codes: the batch reports the worst one. Cancelled
// items carry no code of their own; the failure that cancelled them does.
var severity = map[int]int{
	ExitInterrupted: 7,
	ExitAuth:        6,
	ExitUnknown:     5,
	ExitRejected:    4,
	ExitUpload:      3,
	ExitUsage:       2,
	ExitError:       1,
}

func batchExitCode(results []publish.BatchResult) (code, failed int) {
	code = ExitOK
	for _, r := range results {
		if r.Err == nil {
			continue
		}
		failed++
		if errors.Is(r.Err, publish.ErrCancelled) {
			continue
		}
		if c := exitCode(r.Err); severity[c] > severity[code] {
			code = c
		}
	}
	return code, failed
}

// batchError carries the exit code of a batch whose summary is already
// printed; reportError adds one line, not a second table.
type batchError struct {
	code   int
	failed int
	total  int
}

func (e *batchError) Error() string {
	return fmt.Sprintf("%d of %d episodes did not finish; see the summary above", e.failed, e.total)
}
