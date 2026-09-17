package translation

import "testing"

func TestParseEpisodeType(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want EpisodeType
		ok   bool
	}{
		{"tv", "tv", TV, true},
		{"ova", "ova", OVA, true},
		{"ona", "ona", ONA, true},
		{"movie", "movie", Movie, true},
		{"special", "special", Special, true},
		{"tv special", "tv_special", TVSpecial, true},
		{"preview", "preview", Preview, true},
		{"unknown", "season", "", false},
		{"empty", "", "", false},
		{"wrong case", "TV", "", false},
		{"dash instead of underscore", "tv-special", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEpisodeType(tt.in)
			if tt.ok && err != nil {
				t.Fatalf("ParseEpisodeType(%q) returned error %v, want none", tt.in, err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("ParseEpisodeType(%q) returned no error, want one", tt.in)
			}
			if got != tt.want {
				t.Fatalf("ParseEpisodeType(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseTranslationType(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want TranslationType
		ok   bool
	}{
		{"raw", "raw", Raw, true},
		{"japanese subs", "subJa", SubJa, true},
		{"english subs", "subEn", SubEn, true},
		{"english voice", "voiceEn", VoiceEn, true},
		{"ukrainian subs", "subUk", SubUk, true},
		{"ukrainian voice", "voiceUk", VoiceUk, true},
		{"russian subs", "subRu", SubRu, true},
		{"russian voice", "voiceRu", VoiceRu, true},
		{"unknown", "dubRu", "", false},
		{"empty", "", "", false},
		{"wrong case", "voiceru", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTranslationType(tt.in)
			if tt.ok && err != nil {
				t.Fatalf("ParseTranslationType(%q) returned error %v, want none", tt.in, err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("ParseTranslationType(%q) returned no error, want one", tt.in)
			}
			if got != tt.want {
				t.Fatalf("ParseTranslationType(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
