package fineup

import (
	"math/rand"
	"testing"
	"time"
)

// A single-host pool is what the RU channel gives us. Marking that host as
// failed must never leave the picker with nothing to return: the retry budget
// of the chunk, not the health of the pool, decides when to give up.
func TestPickerSingleHostAlwaysPicks(t *testing.T) {
	const host = "https://ru-time28.anime-on.ru/upload.php"
	p := newPicker([]string{host}, rand.New(rand.NewSource(1)))

	for i := range 8 {
		if got := p.pick(i); got != host {
			t.Fatalf("pick(%d) = %q, want %q", i, got, host)
		}
	}
}

func TestPickerSingleHostSurvivesFailures(t *testing.T) {
	const host = "https://ru-time28.anime-on.ru/upload.php"
	p := newPicker([]string{host}, rand.New(rand.NewSource(1)))

	for i := range 5 {
		p.fail(host)
		if got := p.pick(i); got != host {
			t.Fatalf("after %d failures pick(%d) = %q, want %q", i+1, i, got, host)
		}
	}
}

func TestPickerSingleHostRecordDoesNotBreakChoice(t *testing.T) {
	const host = "https://ru-time28.anime-on.ru/upload.php"
	p := newPicker([]string{host}, rand.New(rand.NewSource(1)))

	p.record(host, 5_000_000, time.Second)
	p.fail(host)
	p.record(host, 5_000_000, 2*time.Second)

	if got := p.pick(0); got != host {
		t.Fatalf("pick = %q, want %q", got, host)
	}
}
