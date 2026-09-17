package translation

import (
	"errors"
	"strings"
	"testing"
)

func validDraft() Draft {
	return Draft{
		SeriesID:      36866,
		EpisodeNumber: "1",
		EpisodeType:   TV,
		Type:          VoiceRu,
		Authors:       "Team (Alice, Bob)",
		AddedByAuthor: false,
		VideoPath:     "/tmp/episode.mp4",
		SubPath:       "",
	}
}

func TestFieldErrorMessage(t *testing.T) {
	err := &FieldError{Field: "episode", Msg: "is required"}
	if got, want := err.Error(), "episode: is required"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestDraftValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(d *Draft)
		fields []string // expected offending field names; empty means valid
	}{
		{"valid", func(d *Draft) {}, nil},
		{"valid with subtitles", func(d *Draft) { d.SubPath = "/tmp/episode.ass" }, nil},
		{"valid fractional episode", func(d *Draft) { d.EpisodeNumber = "5.5" }, nil},
		{"valid added by author", func(d *Draft) { d.AddedByAuthor = true }, nil},
		{"series zero", func(d *Draft) { d.SeriesID = 0 }, []string{"series"}},
		{"series negative", func(d *Draft) { d.SeriesID = -1 }, []string{"series"}},
		{"episode empty", func(d *Draft) { d.EpisodeNumber = "" }, []string{"episode"}},
		{"episode spaces only", func(d *Draft) { d.EpisodeNumber = "   " }, []string{"episode"}},
		{"episode not a number", func(d *Draft) { d.EpisodeNumber = "one" }, []string{"episode"}},
		{"episode zero", func(d *Draft) { d.EpisodeNumber = "0" }, []string{"episode"}},
		{"episode zero decimal", func(d *Draft) { d.EpisodeNumber = "0.0" }, []string{"episode"}},
		{"episode negative", func(d *Draft) { d.EpisodeNumber = "-3" }, []string{"episode"}},
		{"episode NaN", func(d *Draft) { d.EpisodeNumber = "NaN" }, []string{"episode"}},
		{"episode Inf", func(d *Draft) { d.EpisodeNumber = "Inf" }, []string{"episode"}},
		{"episode plus Inf", func(d *Draft) { d.EpisodeNumber = "+Inf" }, []string{"episode"}},
		{"episode hex float", func(d *Draft) { d.EpisodeNumber = "0x1p4" }, []string{"episode"}},
		{"episode underscore digit separator", func(d *Draft) { d.EpisodeNumber = "1_0" }, []string{"episode"}},
		{"episode surrounded by spaces", func(d *Draft) { d.EpisodeNumber = " 5 " }, []string{"episode"}},
		{"episode type unknown", func(d *Draft) { d.EpisodeType = "season" }, []string{"episode-type"}},
		{"episode type empty", func(d *Draft) { d.EpisodeType = "" }, []string{"episode-type"}},
		{"type unknown", func(d *Draft) { d.Type = "dubRu" }, []string{"type"}},
		{"type empty", func(d *Draft) { d.Type = "" }, []string{"type"}},
		{"authors empty", func(d *Draft) { d.Authors = "" }, []string{"authors"}},
		{"authors spaces only", func(d *Draft) { d.Authors = "  \t " }, []string{"authors"}},
		{"video empty", func(d *Draft) { d.VideoPath = "" }, []string{"video"}},
		{"video spaces only", func(d *Draft) { d.VideoPath = "  " }, []string{"video"}},
		{
			"everything wrong",
			func(d *Draft) {
				d.SeriesID = 0
				d.EpisodeNumber = ""
				d.EpisodeType = "season"
				d.Type = "dubRu"
				d.Authors = ""
				d.VideoPath = ""
			},
			[]string{"series", "episode", "episode-type", "type", "authors", "video"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := validDraft()
			tt.mutate(&d)
			err := d.Validate()
			if len(tt.fields) == 0 {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want errors for %v", tt.fields)
			}
			for _, field := range tt.fields {
				var fe *FieldError
				found := false
				for _, e := range flattenErrors(err) {
					if errors.As(e, &fe) && fe.Field == field {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("Validate() = %q, want a *FieldError for field %q", err, field)
				}
			}
			if got, want := strings.Count(err.Error(), "\n")+1, len(tt.fields); got != want {
				t.Fatalf("Validate() reported %d problems, want %d: %v", got, want, err)
			}
		})
	}
}

// flattenErrors unwraps the tree produced by errors.Join into a flat slice.
func flattenErrors(err error) []error {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []error{err}
	}
	var out []error
	for _, e := range joined.Unwrap() {
		out = append(out, flattenErrors(e)...)
	}
	return out
}
