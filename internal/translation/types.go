// Package translation holds the domain model of a translation being published:
// episode and translation kinds, the delivery channel and the draft to submit.
package translation

import "fmt"

// EpisodeType is the kind of episode a translation belongs to. The values are
// the ones the site's form accepts verbatim.
type EpisodeType string

const (
	TV        EpisodeType = "tv"
	OVA       EpisodeType = "ova"
	ONA       EpisodeType = "ona"
	Movie     EpisodeType = "movie"
	Special   EpisodeType = "special"
	TVSpecial EpisodeType = "tv_special"
	Preview   EpisodeType = "preview"
)

var episodeTypes = []EpisodeType{TV, OVA, ONA, Movie, Special, TVSpecial, Preview}

// ParseEpisodeType converts a form value into an EpisodeType. The match is
// exact: the site accepts these spellings and no others.
func ParseEpisodeType(s string) (EpisodeType, error) {
	for _, t := range episodeTypes {
		if string(t) == s {
			return t, nil
		}
	}
	return "", fmt.Errorf("translation: unknown episode type %q", s)
}

// TranslationType is the kind of translation: raw, subtitles or voice-over in a
// given language. Values are the ones the site's form accepts verbatim.
type TranslationType string

const (
	Raw     TranslationType = "raw"
	SubJa   TranslationType = "subJa"
	SubEn   TranslationType = "subEn"
	VoiceEn TranslationType = "voiceEn"
	SubUk   TranslationType = "subUk"
	VoiceUk TranslationType = "voiceUk"
	SubRu   TranslationType = "subRu"
	VoiceRu TranslationType = "voiceRu"
)

var translationTypes = []TranslationType{Raw, SubJa, SubEn, VoiceEn, SubUk, VoiceUk, SubRu, VoiceRu}

// ParseTranslationType converts a form value into a TranslationType. The match
// is exact and case sensitive: the site spells these in camel case.
func ParseTranslationType(s string) (TranslationType, error) {
	for _, t := range translationTypes {
		if string(t) == s {
			return t, nil
		}
	}
	return "", fmt.Errorf("translation: unknown translation type %q", s)
}
