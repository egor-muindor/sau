// Package history is the append-only log of finished publications and the
// aggregates computed over it (docs/architecture.md §10).
//
// One line of JSON per record: appending stays atomic and the file stays
// readable by eye. Volume is tiny — dozens of lines per season — so the
// aggregates just read the whole file.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"sau/internal/fsx"
)

// Record is one finished publication. The title, the episode and the
// translation are separate fields, not one string, so that aggregates can be
// computed over them.
type Record struct {
	At     time.Time `json:"at"`
	Series struct {
		ID     int    `json:"id"`
		Title  string `json:"title"`
		Season string `json:"season"`
		Year   int    `json:"year"`
	} `json:"series"`
	Episode struct {
		Number string `json:"number"`
		ID     int    `json:"id"`
		Type   string `json:"type"`
		Title  string `json:"title"`
	} `json:"episode"`
	Translation struct {
		ID      int    `json:"id"`
		Type    string `json:"type"`
		Authors string `json:"authors"`
		URL     string `json:"url"`
		Quality string `json:"quality"`
		Width   int    `json:"width"`
		Height  int    `json:"height"`
	} `json:"translation"`
	Upload struct {
		File        string        `json:"file"`
		Size        int64         `json:"size"`
		Parts       int           `json:"parts"`
		Duration    time.Duration `json:"duration"`
		BytesPerSec float64       `json:"bytesPerSec"`
		Retries     int           `json:"retries"`
		Hosts       []string      `json:"hosts"`
		Channel     string        `json:"channel"`
		ServerID    int           `json:"serverId"`
		Started     time.Time     `json:"started"`
		Finished    time.Time     `json:"finished"`
	} `json:"upload"`
}

// Log is the journal file.
type Log struct {
	Path string
}

// Append adds one record as a single line. The file is opened in append mode
// and is never rewritten.
func (l Log) Append(r Record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(l.Path); dir != "." && dir != "" {
		if err := fsx.MkdirPrivate(dir); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// ReadAll reads every record. A missing file is not an error.
func (l Log) ReadAll() ([]Record, error) {
	f, err := os.Open(l.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("history: %s line %d: %w", l.Path, n, err)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Filter selects records for aggregation. A zero SeriesID means any title, a
// zero Since means any time.
type Filter struct {
	SeriesID int
	Since    time.Time
}

// Stats is the summary of the selected records.
type Stats struct {
	Count           int
	Bytes           int64
	AvgBytesPerSec  float64
	Retries         int
	ByHost          map[string]int
	MissingEpisodes []string
}

func (f Filter) match(r Record) bool {
	if f.SeriesID != 0 && r.Series.ID != f.SeriesID {
		return false
	}
	if !f.Since.IsZero() && r.At.Before(f.Since) {
		return false
	}
	return true
}

// Aggregate summarizes the records that pass the filter.
//
// AvgBytesPerSec is the weighted average: total bytes over total duration.
// MissingEpisodes is computed only when every selected record belongs to one
// title, and only over whole episode numbers — otherwise the numbers of
// different titles would be mixed into one sequence.
func Aggregate(rs []Record, f Filter) Stats {
	st := Stats{ByHost: make(map[string]int)}

	var totalSeconds float64
	seriesIDs := make(map[int]bool)
	numbers := make(map[int]bool)
	for _, r := range rs {
		if !f.match(r) {
			continue
		}
		st.Count++
		st.Bytes += r.Upload.Size
		st.Retries += r.Upload.Retries
		totalSeconds += r.Upload.Duration.Seconds()

		seen := make(map[string]bool, len(r.Upload.Hosts))
		for _, h := range r.Upload.Hosts {
			if seen[h] {
				continue
			}
			seen[h] = true
			st.ByHost[h]++
		}

		seriesIDs[r.Series.ID] = true
		if n, err := strconv.Atoi(strings.TrimSpace(r.Episode.Number)); err == nil {
			numbers[n] = true
		}
	}

	if totalSeconds > 0 {
		st.AvgBytesPerSec = float64(st.Bytes) / totalSeconds
	}
	if len(seriesIDs) == 1 && len(numbers) > 0 {
		st.MissingEpisodes = missing(numbers)
	}
	return st
}

func missing(numbers map[int]bool) []string {
	if len(numbers) == 0 {
		return nil
	}
	present := make([]int, 0, len(numbers))
	for n := range numbers {
		present = append(present, n)
	}
	sort.Ints(present)

	var out []string
	for n := present[0]; n <= present[len(present)-1]; n++ {
		if !numbers[n] {
			out = append(out, strconv.Itoa(n))
		}
	}
	return out
}
