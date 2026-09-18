package translation

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// episodeNumberRe matches the only spellings of an episode number the site
// accepts: plain digits, optionally with a decimal fraction. strconv.ParseFloat
// alone is too permissive here: it also accepts "NaN", "Inf", "0x1p4", "1_0"
// and surrounding whitespace, none of which the site's form would.
var episodeNumberRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// ValidateEpisodeNumber reports whether s is an episode number the site
// accepts: plain digits, optionally with a decimal fraction, greater than
// zero. The command line, the configuration file and the draft all go through
// this one check, so a bad number is rejected the same way wherever it came
// from. The result is nil or a *FieldError for the field "episode".
func ValidateEpisodeNumber(s string) error {
	switch {
	case s == "":
		return &FieldError{Field: "episode", Msg: "is required"}
	case !episodeNumberRe.MatchString(s):
		return &FieldError{Field: "episode", Msg: "must be a positive decimal number, got " + strconv.Quote(s)}
	}
	// Safe: episodeNumberRe already guarantees valid float syntax.
	n, _ := strconv.ParseFloat(s, 64)
	if n <= 0 {
		return &FieldError{Field: "episode", Msg: "must be greater than zero, got " + strconv.Quote(s)}
	}
	return nil
}

// Draft is everything the user decides about a publication. It carries no
// knowledge of the site: the site package turns it into form fields.
type Draft struct {
	SeriesID      int
	EpisodeNumber string // a string on purpose: "5.5" is a valid episode number
	EpisodeType   EpisodeType
	Type          TranslationType
	Authors       string
	AddedByAuthor bool
	VideoPath     string
	SubPath       string // may be empty: subtitles are optional
}

// FieldError says which field of a draft is wrong and why.
type FieldError struct {
	Field string
	Msg   string
}

func (e *FieldError) Error() string {
	return e.Field + ": " + e.Msg
}

// Validate reports every problem at once, so that the user fixes the draft in a
// single pass instead of one error per run. The result is an errors.Join of
// *FieldError values, or nil when the draft is fit to submit.
func (d Draft) Validate() error {
	var errs []error

	if d.SeriesID <= 0 {
		errs = append(errs, &FieldError{Field: "series", Msg: "must be a positive series id"})
	}

	if err := ValidateEpisodeNumber(d.EpisodeNumber); err != nil {
		errs = append(errs, err)
	}

	if _, err := ParseEpisodeType(string(d.EpisodeType)); err != nil {
		errs = append(errs, &FieldError{Field: "episode-type", Msg: "unknown value " + strconv.Quote(string(d.EpisodeType))})
	}

	if _, err := ParseTranslationType(string(d.Type)); err != nil {
		errs = append(errs, &FieldError{Field: "type", Msg: "unknown value " + strconv.Quote(string(d.Type))})
	}

	if strings.TrimSpace(d.Authors) == "" {
		errs = append(errs, &FieldError{Field: "authors", Msg: "is required"})
	}

	if strings.TrimSpace(d.VideoPath) == "" {
		errs = append(errs, &FieldError{Field: "video", Msg: "is required"})
	}

	return errors.Join(errs...)
}
