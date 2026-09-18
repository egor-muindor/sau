package site

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"sau/internal/translation"
)

// Series is a title from the read-only API.
type Series struct {
	ID               int
	Titles           map[string]string
	Season           string
	Year             int
	NumberOfEpisodes int
}

// Episode is one episode of a title.
type Episode struct {
	ID          int
	EpisodeInt  string
	EpisodeFull string
	EpisodeType string
}

// Translation is one published translation.
type Translation struct {
	ID             int
	EpisodeID      int
	TypeKind       string
	TypeLang       string
	AuthorsSummary string
	URL            string
	QualityType    string
	Width, Height  int
}

// apiEnvelope is the common shape of every API answer: either data or an error
// object, both inside an HTTP 200.
type apiEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// getAPI performs a retrying GET and unwraps the envelope into v.
func (c *Client) getAPI(ctx context.Context, path string, v any) error {
	resp, err := c.doGET(ctx, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Bounded: an answer that never ends must not become an allocation that
	// never ends. A truncated body fails to parse as JSON, which is the right
	// outcome — it is not valid data.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("site: bad api answer: %w", err)
	}
	if env.Error != nil {
		return &APIError{Code: env.Error.Code, Message: env.Error.Message}
	}
	if len(env.Data) == 0 {
		return fmt.Errorf("site: api answer has no data")
	}
	return json.Unmarshal(env.Data, v)
}

func (c *Client) Series(ctx context.Context, id int) (Series, error) {
	var raw struct {
		ID               int               `json:"id"`
		Titles           map[string]string `json:"titles"`
		Season           string            `json:"season"`
		Year             int               `json:"year"`
		NumberOfEpisodes int               `json:"numberOfEpisodes"`
	}
	if err := c.getAPI(ctx, "/api/series/"+strconv.Itoa(id), &raw); err != nil {
		return Series{}, err
	}
	return Series(raw), nil
}

func (c *Client) Episodes(ctx context.Context, seriesID int) ([]Episode, error) {
	var raw []struct {
		ID          int            `json:"id"`
		SeriesID    int            `json:"seriesId"`
		EpisodeInt  numberOrString `json:"episodeInt"`
		EpisodeFull string         `json:"episodeFull"`
		EpisodeType string         `json:"episodeType"`
	}
	// The limit is explicit for the same reason as on translations: the default
	// page size of the API is not documented and has changed before.
	path := "/api/episodes?seriesId=" + strconv.Itoa(seriesID) + "&limit=1000"
	if err := c.getAPI(ctx, path, &raw); err != nil {
		return nil, err
	}
	// The API silently ignores filters it does not understand and answers with
	// plausible but wrong data, so every record is re-checked here.
	var out []Episode
	for _, e := range raw {
		if e.SeriesID != 0 && e.SeriesID != seriesID {
			continue
		}
		out = append(out, Episode{
			ID: e.ID, EpisodeInt: string(e.EpisodeInt),
			EpisodeFull: e.EpisodeFull, EpisodeType: e.EpisodeType,
		})
	}
	return out, nil
}

// splitTranslationType maps a domain type onto the kind/lang pair the API uses:
// voiceRu -> voice, ru; raw -> raw, "".
func splitTranslationType(t translation.TranslationType) (kind, lang string) {
	s := string(t)
	for i, r := range s {
		if unicode.IsUpper(r) {
			return s[:i], strings.ToLower(s[i:])
		}
	}
	return s, ""
}

func (c *Client) Translations(ctx context.Context, seriesID int, t translation.TranslationType) ([]Translation, error) {
	var raw []struct {
		ID             int    `json:"id"`
		SeriesID       int    `json:"seriesId"`
		EpisodeID      int    `json:"episodeId"`
		TypeKind       string `json:"typeKind"`
		TypeLang       string `json:"typeLang"`
		AuthorsSummary string `json:"authorsSummary"`
		URL            string `json:"url"`
		QualityType    string `json:"qualityType"`
		Width          int    `json:"width"`
		Height         int    `json:"height"`
	}
	path := "/api/translations?seriesId=" + strconv.Itoa(seriesID) +
		"&type=" + string(t) + "&limit=1000"
	if err := c.getAPI(ctx, path, &raw); err != nil {
		return nil, err
	}
	wantKind, wantLang := splitTranslationType(t)
	var out []Translation
	for _, r := range raw {
		if r.SeriesID != 0 && r.SeriesID != seriesID {
			continue
		}
		if r.TypeKind != wantKind {
			continue
		}
		if wantLang != "" && r.TypeLang != wantLang {
			continue
		}
		out = append(out, Translation{
			ID: r.ID, EpisodeID: r.EpisodeID,
			TypeKind: r.TypeKind, TypeLang: r.TypeLang,
			AuthorsSummary: r.AuthorsSummary, URL: r.URL,
			QualityType: r.QualityType, Width: r.Width, Height: r.Height,
		})
	}
	return out, nil
}

// FindPublished looks for an already published translation of the same episode
// by the same authors. There is no episode filter in the API, so the walk is
// three steps: list the episodes, find the one with this number, list the
// translations of this type, keep the ones on that episode whose authors match.
//
// Matching authors is mandatory: a popular ongoing gets seven to nine voice
// overs of the same episode from different teams, so episode plus type alone
// would point at somebody else's work. The comparison normalizes the string,
// because the site rewrites it on save.
//
// Only the number is matched, not the episode type: the contract of this method
// takes no type, so a title that numbers its specials alongside its episodes can
// match the wrong one. That is acceptable for a warning and would not be for a
// gate.
//
// This is a warning about a possible duplicate, not a gate. It never takes part
// in deciding whether to resend a form: a new translation shows up in the API
// only minutes later (docs/architecture.md §7).
func (c *Client) FindPublished(ctx context.Context, seriesID int, episode string, t translation.TranslationType, authors string) ([]Translation, error) {
	eps, err := c.Episodes(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	episodeID := 0
	for _, e := range eps {
		if episodeNumberEqual(e.EpisodeInt, episode) {
			episodeID = e.ID
			break
		}
	}
	if episodeID == 0 {
		return nil, nil
	}

	ts, err := c.Translations(ctx, seriesID, t)
	if err != nil {
		return nil, err
	}
	var out []Translation
	for _, tr := range ts {
		if tr.EpisodeID != episodeID {
			continue
		}
		if !translation.AuthorsEqual(tr.AuthorsSummary, authors) {
			continue
		}
		out = append(out, tr)
	}
	return out, nil
}

// episodeNumberEqual compares two episode numbers the way a human reads them:
// the site writes "01" where the user types "1", so a literal comparison would
// never match. A fractional number is a distinct episode, so 5.5 does not match
// 5. When either side is not a number — some titles use letters — the only safe
// comparison left is the literal one.
func episodeNumberEqual(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	af, aerr := strconv.ParseFloat(a, 64)
	bf, berr := strconv.ParseFloat(b, 64)
	if aerr == nil && berr == nil {
		return af == bf
	}
	return a == b
}

// numberOrString decodes a JSON field the API sends either as a number or as a
// string: episodeInt is 11 for whole episodes and "5.5" for fractional ones.
type numberOrString string

func (n *numberOrString) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*n = numberOrString(s)
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(b, &num); err != nil {
		return err
	}
	*n = numberOrString(num.String())
	return nil
}
