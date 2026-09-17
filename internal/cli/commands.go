package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"sau/internal/config"
	"sau/internal/history"
	"sau/internal/publish"
	"sau/internal/site"
	"sau/internal/translation"
)

func cmdUpload(ctx context.Context, args []string, d Deps) error {
	o, layer, err := parseUpload(args, d)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(layer, d)
	if err != nil {
		return err
	}

	// A bad value is a usage error no matter whether it came from a flag, from
	// the environment or from a file.
	ch, err := translation.ParseChannel(cfg.Channel)
	if err != nil {
		return usagef("invalid channel %q: use all, cdn or ru", cfg.Channel)
	}
	et, err := translation.ParseEpisodeType(cfg.EpisodeType)
	if err != nil {
		return usagef("invalid episode type %q", cfg.EpisodeType)
	}
	tt, err := translation.ParseTranslationType(cfg.Type)
	if err != nil {
		return usagef("invalid translation type %q", cfg.Type)
	}
	if cfg.SeriesID <= 0 {
		return usagef("upload requires --series, SAU_SERIES or series in a configuration file")
	}

	draft := translation.Draft{
		SeriesID:      cfg.SeriesID,
		EpisodeNumber: o.episode,
		EpisodeType:   et,
		Type:          tt,
		Authors:       cfg.Authors,
		AddedByAuthor: cfg.AddedByAuthor,
		VideoPath:     o.video,
		SubPath:       o.sub,
	}
	if err := draft.Validate(); err != nil {
		return usagef("%v", err)
	}

	// A mistyped path is a usage error, not a failed run. The runner stats the
	// file again when it needs the size; this check is only here so that a typo
	// reads like one. The subtitle file is left to the runner: it is resolved
	// against the upload, not against the command line.
	switch info, err := os.Stat(o.video); {
	case err != nil:
		return usagef("cannot read the video file: %v", err)
	case info.IsDir():
		return usagef("cannot read the video file: %s is a directory", o.video)
	}

	if d.Runner == nil {
		return errors.New("cli: no runner configured")
	}
	runner, err := d.Runner(cfg, newLogger(d, cfg, o.verbose, o.debug))
	if err != nil {
		return err
	}

	out, err := runner.Run(ctx, publish.Request{
		Draft:       draft,
		Channel:     ch,
		User:        cfg.User,
		Password:    buildPassword(cfg, d),
		Fresh:       o.fresh,
		DryRun:      o.dryRun,
		NoSubmit:    o.noSubmit,
		Concurrency: cfg.Concurrency,
	})
	if err != nil {
		return err
	}
	return printUploadResult(d, o.asJSON, out)
}

func printUploadResult(d Deps, asJSON bool, out publish.Outcome) error {
	if asJSON {
		return printJSON(d.Stdout, struct {
			TranslationID int `json:"translationId"`
		}{out.TranslationID})
	}
	if out.TranslationID > 0 {
		fmt.Fprintf(d.Stdout, "published: translation %d\n", out.TranslationID)
		return nil
	}
	if out.VideoField != "" {
		fmt.Fprintf(d.Stdout, "video field: %s\n", out.VideoField)
	}
	if out.SubField != "" {
		fmt.Fprintf(d.Stdout, "sub field: %s\n", out.SubField)
	}
	return nil
}

func cmdLogin(ctx context.Context, args []string, d Deps) error {
	o, layer, err := parseLogin(args, d)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(layer, d)
	if err != nil {
		return err
	}
	if o.user != "" {
		cfg.User = o.user
	}
	if cfg.User == "" {
		return usagef("login requires --user, SAU_USER or user in a configuration file")
	}
	if d.Site == nil {
		return errors.New("cli: no site client configured")
	}
	client, err := d.Site(cfg, newLogger(d, cfg, true, false))
	if err != nil {
		return err
	}

	if o.check {
		// A session cookie lives for about a month, so this is the cheap way to
		// find out whether a password will be needed at all.
		//
		// It has to ask for something that is behind the login. The read-only
		// API answers without a cookie, so a dead session would look healthy
		// there; the create form does not exist for a stranger.
		ch, err := translation.ParseChannel(cfg.Channel)
		if err != nil {
			return usagef("invalid channel %q: use all, cdn or ru", cfg.Channel)
		}
		seriesID := cfg.SeriesID
		if seriesID <= 0 {
			// Any title will do: the question is about the session, not about
			// this particular series.
			seriesID = 1
		}
		if _, err := client.CreateForm(ctx, seriesID, ch); err != nil {
			if errors.Is(err, site.ErrNotAuthorized) {
				fmt.Fprintln(d.Stdout, "session: expired")
			}
			return err
		}
		fmt.Fprintln(d.Stdout, "session: ok")
		return nil
	}

	pass, ok, err := buildPassword(cfg, d).Password(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("cli: no password source produced a password")
	}
	if err := client.Login(ctx, cfg.User, pass); err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "logged in as %s on %s\n", cfg.User, cfg.Mirror)

	if o.savePassword {
		if err := savePassword(cfg.User, pass); err != nil {
			fmt.Fprintf(d.Stderr, "sau: could not store the password: %v\n", err)
		} else {
			fmt.Fprintln(d.Stdout, "password stored in the system keyring")
		}
	}
	return nil
}

func cmdStatus(ctx context.Context, args []string, d Deps) error {
	if err := parseStatus(args, d); err != nil {
		return err
	}
	cfg, err := loadConfig(config.Layer{}, d)
	if err != nil {
		return err
	}
	runner, err := d.Runner(cfg, newLogger(d, cfg, false, false))
	if err != nil {
		return err
	}
	states, err := runner.Status()
	if err != nil {
		return err
	}
	if len(states) == 0 {
		fmt.Fprintln(d.Stdout, "no unfinished uploads")
		return nil
	}

	slices.SortFunc(states, func(a, b publish.UploadState) int {
		return a.StartedAt.Compare(b.StartedAt)
	})

	tw := tabwriter.NewWriter(d.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "FILE\tPHASE\tCHANNEL\tPARTS\tSTARTED")
	for _, st := range states {
		parts := "-"
		if st.Video != nil && st.Video.TotalParts > 0 {
			parts = fmt.Sprintf("%d/%d", len(st.Video.Done), st.Video.TotalParts)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			filepath.Base(st.Path), st.Phase, st.Channel, parts,
			st.StartedAt.Local().Format(time.RFC3339))
	}
	return tw.Flush()
}

func cmdAbort(ctx context.Context, args []string, d Deps) error {
	path, err := parseAbort(args, d)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(config.Layer{}, d)
	if err != nil {
		return err
	}
	runner, err := d.Runner(cfg, newLogger(d, cfg, true, false))
	if err != nil {
		return err
	}
	if err := runner.Abort(ctx, path); err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "aborted: %s\n", path)
	return nil
}

func cmdResolve(ctx context.Context, args []string, d Deps) error {
	o, err := parseResolve(args, d)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(config.Layer{}, d)
	if err != nil {
		return err
	}
	runner, err := d.Runner(cfg, newLogger(d, cfg, true, false))
	if err != nil {
		return err
	}
	if err := runner.Resolve(ctx, o.path, o.submitted); err != nil {
		return err
	}
	if o.submitted {
		fmt.Fprintf(d.Stdout, "recorded as submitted: %s\n", o.path)
	} else {
		fmt.Fprintf(d.Stdout, "ready to submit again: %s\n", o.path)
	}
	return nil
}

func cmdSeries(ctx context.Context, args []string, d Deps) error {
	id, err := parseSeries(args, d)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(config.Layer{}, d)
	if err != nil {
		return err
	}
	client, err := d.Site(cfg, newLogger(d, cfg, false, false))
	if err != nil {
		return err
	}
	s, err := client.Series(ctx, id)
	if err != nil {
		return err
	}
	eps, err := client.Episodes(ctx, id)
	if err != nil {
		return err
	}

	fmt.Fprintf(d.Stdout, "series %d\n", s.ID)
	for _, key := range slices.Sorted(maps.Keys(s.Titles)) {
		fmt.Fprintf(d.Stdout, "  title[%s]: %s\n", key, s.Titles[key])
	}
	fmt.Fprintf(d.Stdout, "  season: %s %d\n", s.Season, s.Year)
	fmt.Fprintf(d.Stdout, "  episodes announced: %d\n", s.NumberOfEpisodes)
	fmt.Fprintf(d.Stdout, "  episodes present: %d\n", len(eps))
	for _, e := range eps {
		fmt.Fprintf(d.Stdout, "    %-6s %-10s id=%d\n", e.EpisodeInt, e.EpisodeType, e.ID)
		if e.EpisodeFull != "" {
			fmt.Fprintf(d.Stdout, "           %s\n", e.EpisodeFull)
		}
	}
	return nil
}

func cmdStats(ctx context.Context, args []string, d Deps) error {
	o, err := parseStats(args, d)
	if err != nil {
		return err
	}
	var since time.Time
	if o.since != "" {
		since, err = time.Parse("2006-01-02", o.since)
		if err != nil {
			return usagef("--since expects a date as YYYY-MM-DD, got %q", o.since)
		}
	}
	cfg, err := loadConfig(config.Layer{}, d)
	if err != nil {
		return err
	}

	records, err := history.Log{Path: cfg.HistoryPath}.ReadAll()
	if err != nil {
		return err
	}
	stats := history.Aggregate(records, history.Filter{SeriesID: o.series, Since: since})

	if o.asJSON {
		return printJSON(d.Stdout, stats)
	}
	fmt.Fprintf(d.Stdout, "uploads: %d\n", stats.Count)
	fmt.Fprintf(d.Stdout, "bytes: %d\n", stats.Bytes)
	fmt.Fprintf(d.Stdout, "average speed: %.0f B/s\n", stats.AvgBytesPerSec)
	fmt.Fprintf(d.Stdout, "chunk retries: %d\n", stats.Retries)
	for _, host := range slices.Sorted(maps.Keys(stats.ByHost)) {
		fmt.Fprintf(d.Stdout, "  %s: %d\n", host, stats.ByHost[host])
	}
	if len(stats.MissingEpisodes) > 0 {
		fmt.Fprintf(d.Stdout, "missing episodes: %s\n", strings.Join(stats.MissingEpisodes, ", "))
	}
	return nil
}
