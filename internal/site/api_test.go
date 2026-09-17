package site

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"sau/internal/translation"
)

func TestAPISeries(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/series/36866", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"id":36866,"titles":{"ru":"Тайтл","romaji":"Title","en":"Title"},"season":"fall_2026","year":2026,"numberOfEpisodes":12}}`))
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	s, err := c.Series(context.Background(), 36866)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if s.ID != 36866 || s.Year != 2026 || s.NumberOfEpisodes != 12 || s.Season != "fall_2026" {
		t.Errorf("Series = %+v", s)
	}
	if s.Titles["ru"] != "Тайтл" || s.Titles["romaji"] != "Title" {
		t.Errorf("Titles = %v", s.Titles)
	}
}

func TestAPIErrorInsideOK(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/series/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// HTTP 200 with an error object in the body — the site does this.
		w.Write([]byte(`{"error":{"code":403,"message":"Access denied"}}`))
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	_, err := c.Series(context.Background(), 1)
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v (%T), want *APIError", err, err)
	}
	if ae.Code != 403 || ae.Message != "Access denied" {
		t.Errorf("APIError = %+v", ae)
	}
}

// The API silently ignores filters it does not understand, so the client
// re-checks every record it got back.
func TestAPIEpisodesRechecksSeriesID(t *testing.T) {
	var gotURI string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/episodes", func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.URL.RequestURI()
		w.Write([]byte(`{"data":[
			{"id":11,"seriesId":36866,"episodeInt":"1","episodeFull":"1 серия","episodeType":"tv"},
			{"id":12,"seriesId":99999,"episodeInt":"1","episodeFull":"чужая серия","episodeType":"tv"},
			{"id":13,"seriesId":36866,"episodeInt":"2","episodeFull":"2 серия","episodeType":"tv"}
		]}`))
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	eps, err := c.Episodes(context.Background(), 36866)
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if gotURI != "/api/episodes?seriesId=36866&limit=1000" {
		t.Errorf("request URI = %q", gotURI)
	}
	want := []Episode{
		{ID: 11, EpisodeInt: "1", EpisodeFull: "1 серия", EpisodeType: "tv"},
		{ID: 13, EpisodeInt: "2", EpisodeFull: "2 серия", EpisodeType: "tv"},
	}
	if !reflect.DeepEqual(eps, want) {
		t.Errorf("Episodes = %+v, want %+v", eps, want)
	}
}

func TestAPITranslationsRechecksType(t *testing.T) {
	var gotURI string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/translations", func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.URL.RequestURI()
		w.Write([]byte(`{"data":[
			{"id":1,"seriesId":36866,"episodeId":11,"typeKind":"voice","typeLang":"ru","authorsSummary":"Team (Alice & Bob)","url":"https://smotret-anime.online/translations/embed/1","qualityType":"bd","width":1920,"height":1080},
			{"id":2,"seriesId":36866,"episodeId":11,"typeKind":"sub","typeLang":"ru","authorsSummary":"Other","url":"u2","qualityType":"tv","width":1280,"height":720},
			{"id":3,"seriesId":36866,"episodeId":11,"typeKind":"voice","typeLang":"en","authorsSummary":"Other","url":"u3","qualityType":"tv","width":1280,"height":720},
			{"id":4,"seriesId":99999,"episodeId":11,"typeKind":"voice","typeLang":"ru","authorsSummary":"Alien","url":"u4","qualityType":"tv","width":0,"height":0}
		]}`))
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	ts, err := c.Translations(context.Background(), 36866, translation.VoiceRu)
	if err != nil {
		t.Fatalf("Translations: %v", err)
	}
	if gotURI != "/api/translations?seriesId=36866&type=voiceRu&limit=1000" {
		t.Errorf("request URI = %q", gotURI)
	}
	if len(ts) != 1 || ts[0].ID != 1 {
		t.Fatalf("Translations = %+v, want only the voice/ru record of this series", ts)
	}
	if ts[0].EpisodeID != 11 || ts[0].Width != 1920 || ts[0].Height != 1080 || ts[0].QualityType != "bd" {
		t.Errorf("Translation = %+v", ts[0])
	}
}

func TestSplitTranslationType(t *testing.T) {
	tests := []struct {
		in         translation.TranslationType
		kind, lang string
	}{
		{translation.VoiceRu, "voice", "ru"},
		{translation.SubRu, "sub", "ru"},
		{translation.SubJa, "sub", "ja"},
		{translation.VoiceEn, "voice", "en"},
		{translation.SubUk, "sub", "uk"},
		{translation.Raw, "raw", ""},
	}
	for _, tt := range tests {
		kind, lang := splitTranslationType(tt.in)
		if kind != tt.kind || lang != tt.lang {
			t.Errorf("splitTranslationType(%q) = %q,%q; want %q,%q", tt.in, kind, lang, tt.kind, tt.lang)
		}
	}
}

// A raw translation has no language, so only the kind is checked.
func TestAPITranslationsRaw(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/translations", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[
			{"id":1,"seriesId":5,"episodeId":11,"typeKind":"raw","typeLang":"","authorsSummary":"","url":"u1"},
			{"id":2,"seriesId":5,"episodeId":11,"typeKind":"voice","typeLang":"ru","authorsSummary":"x","url":"u2"}
		]}`))
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	ts, err := c.Translations(context.Background(), 5, translation.Raw)
	if err != nil {
		t.Fatalf("Translations: %v", err)
	}
	if len(ts) != 1 || ts[0].ID != 1 {
		t.Errorf("Translations = %+v, want only the raw record", ts)
	}
}

// findPublishedMux serves the three steps FindPublished walks through.
func findPublishedMux(t *testing.T, calls *[]string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/episodes", func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.URL.RequestURI())
		w.Write([]byte(`{"data":[
			{"id":11,"seriesId":36866,"episodeInt":"1","episodeFull":"1 серия","episodeType":"tv"},
			{"id":12,"seriesId":36866,"episodeInt":"2","episodeFull":"2 серия","episodeType":"tv"}
		]}`))
	})
	mux.HandleFunc("/api/translations", func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.URL.RequestURI())
		// The site rewrites the authors string on save: the comma that was sent
		// comes back as an ampersand.
		w.Write([]byte(`{"data":[
			{"id":1,"seriesId":36866,"episodeId":11,"typeKind":"voice","typeLang":"ru","authorsSummary":"Team (Alice & Bob)","url":"u1","qualityType":"bd","width":1920,"height":1080},
			{"id":2,"seriesId":36866,"episodeId":11,"typeKind":"voice","typeLang":"ru","authorsSummary":"Someone Else","url":"u2","qualityType":"tv","width":1280,"height":720},
			{"id":3,"seriesId":36866,"episodeId":12,"typeKind":"voice","typeLang":"ru","authorsSummary":"Team (Alice & Bob)","url":"u3","qualityType":"bd","width":1920,"height":1080}
		]}`))
	})
	return mux
}

func TestFindPublishedMatchesNormalizedAuthors(t *testing.T) {
	var calls []string
	srv := newTestServer(t, findPublishedMux(t, &calls))
	c := newTestClient(t, srv.URL)

	got, err := c.FindPublished(context.Background(), 36866, "1", translation.VoiceRu, "Team (Alice, Bob)")
	if err != nil {
		t.Fatalf("FindPublished: %v", err)
	}
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("FindPublished = %+v, want only translation 1", got)
	}
	wantCalls := []string{
		"/api/episodes?seriesId=36866&limit=1000",
		"/api/translations?seriesId=36866&type=voiceRu&limit=1000",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestFindPublishedOtherAuthors(t *testing.T) {
	var calls []string
	srv := newTestServer(t, findPublishedMux(t, &calls))
	c := newTestClient(t, srv.URL)

	got, err := c.FindPublished(context.Background(), 36866, "1", translation.VoiceRu, "Nobody")
	if err != nil {
		t.Fatalf("FindPublished: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("FindPublished = %+v, want none", got)
	}
}

func TestFindPublishedUnknownEpisode(t *testing.T) {
	var calls []string
	srv := newTestServer(t, findPublishedMux(t, &calls))
	c := newTestClient(t, srv.URL)

	got, err := c.FindPublished(context.Background(), 36866, "7", translation.VoiceRu, "Team (Alice, Bob)")
	if err != nil {
		t.Fatalf("FindPublished: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("FindPublished = %+v, want none", got)
	}
	// An unknown episode ends the walk after one call: there is nothing to match.
	if len(calls) != 1 {
		t.Errorf("calls = %v, want only the episodes call", calls)
	}
}

// item 2: an answer that does not stop must not be read into memory whole.
func TestAPIBodyIsLimited(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/series/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"id":1,"titles":{"ru":"`))
		chunk := bytes.Repeat([]byte("x"), 64*1024)
		for written := 0; written < 5<<20; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	done := make(chan error, 1)
	go func() {
		_, err := c.Series(context.Background(), 1)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Series: err = nil, want an error on a body past the limit")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Series did not return: the body is not being limited")
	}
}

// The site writes episode numbers with a leading zero, the user types "1".
func TestFindPublishedMatchesEpisodeNumerically(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/episodes", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[
			{"id":11,"seriesId":36866,"episodeInt":"01","episodeFull":"1 серия","episodeType":"tv"},
			{"id":12,"seriesId":36866,"episodeInt":"5.5","episodeFull":"5.5 серия","episodeType":"tv"}
		]}`))
	})
	mux.HandleFunc("/api/translations", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[
			{"id":1,"seriesId":36866,"episodeId":11,"typeKind":"voice","typeLang":"ru","authorsSummary":"Team (Alice & Bob)","url":"u1"},
			{"id":2,"seriesId":36866,"episodeId":12,"typeKind":"voice","typeLang":"ru","authorsSummary":"Team (Alice & Bob)","url":"u2"}
		]}`))
	})
	srv := newTestServer(t, mux)
	c := newTestClient(t, srv.URL)

	got, err := c.FindPublished(context.Background(), 36866, "1", translation.VoiceRu, "Team (Alice, Bob)")
	if err != nil {
		t.Fatalf("FindPublished: %v", err)
	}
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("FindPublished(\"1\") = %+v, want the translation of episode 01", got)
	}

	// A fractional number is its own episode and must not collapse onto 5.
	got, err = c.FindPublished(context.Background(), 36866, "5", translation.VoiceRu, "Team (Alice, Bob)")
	if err != nil {
		t.Fatalf("FindPublished: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("FindPublished(\"5\") = %+v, want none: 5.5 is a different episode", got)
	}
}
