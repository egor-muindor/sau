package cli

import (
	"flag"
	"slices"
	"strconv"

	"sau/internal/config"
)

// parseArgs parses a command line in which flags and positional arguments are
// mixed, and returns the positional ones.
//
// The flag package stops at the first argument that is not a flag, so
// "upload <video> --episode 1" would leave --episode unparsed. The documented
// order puts the file first, so the remainder is parsed again after each
// positional argument is set aside.
//
// Everything after a bare "--" is positional, which is how a file whose name
// begins with a dash is reached.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var tail []string
	if i := slices.Index(args, "--"); i >= 0 {
		tail = args[i+1:]
		args = args[:i]
	}

	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
	return append(positional, tail...), nil
}

type uploadOpts struct {
	video    string
	sub      string
	episode  string
	dryRun   bool
	noSubmit bool
	fresh    bool
	asJSON   bool
	verbose  bool
	debug    bool
}

// parseUpload reads the upload flags. It returns the options that belong to
// this run only and the configuration layer built from the flags that were
// actually given: an unset flag must not override the environment or a file.
func parseUpload(args []string, d Deps) (uploadOpts, config.Layer, error) {
	var o uploadOpts
	var (
		series      int
		episodeType string
		typ         string
		authors     string
		byAuthor    bool
		channel     string
		concurrency int
	)

	fs := newFlagSet("upload", d)
	fs.StringVar(&o.sub, "sub", "", "subtitle file to upload with the video")
	fs.StringVar(&o.episode, "episode", "", "episode number, for example 7 or 7.5")
	fs.IntVar(&series, "series", 0, "series id on the site")
	fs.StringVar(&episodeType, "episode-type", "", "episode type: tv, ova, ona, movie, special, tv_special, preview")
	fs.StringVar(&typ, "type", "", "translation type, for example voiceRu or subRu")
	fs.StringVar(&authors, "authors", "", "authors line as it should appear on the site")
	fs.BoolVar(&byAuthor, "by-author", false, "mark the translation as added by its author")
	fs.StringVar(&channel, "channel", "", "distribution channel: all, cdn or ru")
	fs.BoolVar(&o.dryRun, "dry-run", false, "do everything that has no side effects")
	fs.BoolVar(&o.noSubmit, "no-submit", false, "upload the file but do not submit the form")
	fs.BoolVar(&o.fresh, "fresh", false, "ignore any saved state and start over")
	fs.IntVar(&concurrency, "concurrency", 0, "number of chunks uploaded at once")
	fs.BoolVar(&o.asJSON, "json", false, "print the result as JSON")
	fs.BoolVar(&o.verbose, "v", false, "explain what is happening")
	fs.BoolVar(&o.debug, "debug", false, "write a full HTTP trace to the log file")

	files, err := parseArgs(fs, args)
	if err != nil {
		return o, nil, parseErr(err)
	}
	if len(files) != 1 {
		return o, nil, usagef("upload takes exactly one video file")
	}
	o.video = files[0]

	layer := config.Layer{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "series":
			layer["series"] = f.Value.String()
		case "episode-type":
			layer["episode_type"] = f.Value.String()
		case "type":
			layer["type"] = f.Value.String()
		case "authors":
			layer["authors"] = f.Value.String()
		case "by-author":
			layer["by_author"] = f.Value.String()
		case "channel":
			layer["channel"] = f.Value.String()
		case "concurrency":
			layer["concurrency"] = f.Value.String()
		}
	})

	if o.episode == "" {
		return o, nil, usagef("upload requires --episode")
	}
	return o, layer, nil
}

type loginOpts struct {
	user         string
	savePassword bool
	check        bool
}

func parseLogin(args []string, d Deps) (loginOpts, config.Layer, error) {
	var o loginOpts
	fs := newFlagSet("login", d)
	fs.StringVar(&o.user, "user", "", "account name on the site")
	fs.BoolVar(&o.savePassword, "save-password", false, "store the password in the system keyring")
	fs.BoolVar(&o.check, "check", false, "only report whether the saved session still works")
	extra, err := parseArgs(fs, args)
	if err != nil {
		return o, nil, parseErr(err)
	}
	if len(extra) != 0 {
		return o, nil, usagef("login takes no positional arguments")
	}
	layer := config.Layer{}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "user" {
			layer["user"] = f.Value.String()
		}
	})
	return o, layer, nil
}

type resolveOpts struct {
	path      string
	submitted bool
}

func parseResolve(args []string, d Deps) (resolveOpts, error) {
	var o resolveOpts
	var submitted, resend bool
	fs := newFlagSet("resolve", d)
	fs.BoolVar(&submitted, "submitted", false, "the form did reach the site; write the journal and drop the state")
	fs.BoolVar(&resend, "resend", false, "the form did not reach the site; allow submitting again")
	files, err := parseArgs(fs, args)
	if err != nil {
		return o, parseErr(err)
	}
	if len(files) != 1 {
		return o, usagef("resolve takes exactly one video file")
	}
	if submitted == resend {
		// Both or neither: the tool must never guess this. A wrong guess either
		// loses a publication or creates a duplicate.
		return o, usagef("resolve requires exactly one of --submitted or --resend")
	}
	o.path = files[0]
	o.submitted = submitted
	return o, nil
}

func parseAbort(args []string, d Deps) (string, error) {
	fs := newFlagSet("abort", d)
	files, err := parseArgs(fs, args)
	if err != nil {
		return "", parseErr(err)
	}
	if len(files) != 1 {
		return "", usagef("abort takes exactly one video file")
	}
	return files[0], nil
}

func parseSeries(args []string, d Deps) (int, error) {
	fs := newFlagSet("series", d)
	args, err := parseArgs(fs, args)
	if err != nil {
		return 0, parseErr(err)
	}
	if len(args) != 1 {
		return 0, usagef("series takes exactly one series id")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		return 0, usagef("series id must be a number, got %q", args[0])
	}
	if id <= 0 {
		return 0, usagef("series id must be positive")
	}
	return id, nil
}

type statsOpts struct {
	series int
	since  string
	asJSON bool
}

func parseStats(args []string, d Deps) (statsOpts, error) {
	var o statsOpts
	fs := newFlagSet("stats", d)
	fs.IntVar(&o.series, "series", 0, "only count uploads for this series")
	fs.StringVar(&o.since, "since", "", "only count uploads on or after this date, YYYY-MM-DD")
	fs.BoolVar(&o.asJSON, "json", false, "print the result as JSON")
	extra, err := parseArgs(fs, args)
	if err != nil {
		return o, parseErr(err)
	}
	if len(extra) != 0 {
		return o, usagef("stats takes no positional arguments")
	}
	if o.series < 0 {
		return o, usagef("--series expects a positive series id, got %d", o.series)
	}
	return o, nil
}

func parseStatus(args []string, d Deps) error {
	fs := newFlagSet("status", d)
	extra, err := parseArgs(fs, args)
	if err != nil {
		return parseErr(err)
	}
	if len(extra) != 0 {
		return usagef("status takes no positional arguments")
	}
	return nil
}
