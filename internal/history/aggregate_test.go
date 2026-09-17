package history_test

import (
	"reflect"
	"testing"
	"time"

	"sau/internal/history"
)

func rec(seriesID int, episode string, at time.Time, size int64, d time.Duration, retries int, hosts ...string) history.Record {
	var r history.Record
	r.At = at
	r.Series.ID = seriesID
	r.Episode.Number = episode
	r.Upload.Size = size
	r.Upload.Duration = d
	r.Upload.Retries = retries
	r.Upload.Hosts = hosts
	return r
}

func TestAggregateCountsBytesAndRetries(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rs := []history.Record{
		rec(36866, "1", base, 1000, 10*time.Second, 1, "hostA"),
		rec(36866, "2", base.AddDate(0, 0, 7), 3000, 10*time.Second, 2, "hostA", "hostB"),
	}

	got := history.Aggregate(rs, history.Filter{})

	if got.Count != 2 {
		t.Errorf("Count = %d, want 2", got.Count)
	}
	if got.Bytes != 4000 {
		t.Errorf("Bytes = %d, want 4000", got.Bytes)
	}
	if got.Retries != 3 {
		t.Errorf("Retries = %d, want 3", got.Retries)
	}
	if got.AvgBytesPerSec != 200 {
		t.Errorf("AvgBytesPerSec = %v, want 200", got.AvgBytesPerSec)
	}
	wantHosts := map[string]int{"hostA": 2, "hostB": 1}
	if !reflect.DeepEqual(got.ByHost, wantHosts) {
		t.Errorf("ByHost = %v, want %v", got.ByHost, wantHosts)
	}
}

func TestAggregateFiltersBySeries(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rs := []history.Record{
		rec(36866, "1", base, 1000, time.Second, 0, "hostA"),
		rec(11111, "1", base, 5000, time.Second, 7, "hostZ"),
	}

	got := history.Aggregate(rs, history.Filter{SeriesID: 36866})

	if got.Count != 1 || got.Bytes != 1000 || got.Retries != 0 {
		t.Errorf("Stats = %+v, want only the 36866 record", got)
	}
	if _, ok := got.ByHost["hostZ"]; ok {
		t.Errorf("ByHost = %v, want no hostZ", got.ByHost)
	}
}

func TestAggregateFiltersBySince(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rs := []history.Record{
		rec(36866, "1", base, 1000, time.Second, 0, "hostA"),
		rec(36866, "2", base.AddDate(0, 0, 10), 2000, time.Second, 0, "hostA"),
	}

	got := history.Aggregate(rs, history.Filter{Since: base.AddDate(0, 0, 5)})

	if got.Count != 1 || got.Bytes != 2000 {
		t.Errorf("Stats = %+v, want only the later record", got)
	}
}

func TestAggregateFindsMissingEpisodes(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rs := []history.Record{
		rec(36866, "1", base, 1, time.Second, 0),
		rec(36866, "2", base, 1, time.Second, 0),
		rec(36866, "5", base, 1, time.Second, 0),
		rec(36866, "7", base, 1, time.Second, 0),
	}

	got := history.Aggregate(rs, history.Filter{SeriesID: 36866})

	want := []string{"3", "4", "6"}
	if !reflect.DeepEqual(got.MissingEpisodes, want) {
		t.Errorf("MissingEpisodes = %v, want %v", got.MissingEpisodes, want)
	}
}

func TestAggregateIgnoresFractionalEpisodesForGaps(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rs := []history.Record{
		rec(36866, "1", base, 1, time.Second, 0),
		rec(36866, "1.5", base, 1, time.Second, 0),
		rec(36866, "3", base, 1, time.Second, 0),
	}

	got := history.Aggregate(rs, history.Filter{SeriesID: 36866})

	want := []string{"2"}
	if !reflect.DeepEqual(got.MissingEpisodes, want) {
		t.Errorf("MissingEpisodes = %v, want %v", got.MissingEpisodes, want)
	}
}

// Episode numbers of different titles must not be mixed into one sequence.
func TestAggregateSkipsGapsAcrossSeveralSeries(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rs := []history.Record{
		rec(36866, "1", base, 1, time.Second, 0),
		rec(11111, "9", base, 1, time.Second, 0),
	}

	got := history.Aggregate(rs, history.Filter{})

	if got.MissingEpisodes != nil {
		t.Errorf("MissingEpisodes = %v, want nil for a mix of series", got.MissingEpisodes)
	}
}

func TestAggregateOnEmptyInput(t *testing.T) {
	got := history.Aggregate(nil, history.Filter{})

	if got.Count != 0 || got.Bytes != 0 || got.AvgBytesPerSec != 0 {
		t.Errorf("Stats = %+v, want zero stats", got)
	}
	if got.ByHost == nil {
		t.Error("ByHost = nil, want an initialized map")
	}
	if got.MissingEpisodes != nil {
		t.Errorf("MissingEpisodes = %v, want nil", got.MissingEpisodes)
	}
}
