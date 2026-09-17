package fineup

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"
)

// testPool is the pool shape of the CDN channel: four mirrors of one server.
func testPool() []string {
	return []string{
		Endpoint("https://t-time28.melon-soda.org"),
		Endpoint("https://time28.melon-soda.org"),
		Endpoint("https://time28.anime-on.ru"),
		Endpoint("https://t-time28.anime-on.ru"),
	}
}

func TestPickerOrdinalChoice(t *testing.T) {
	pool := testPool()
	p := newPicker(pool, rand.New(rand.NewSource(1)))

	for i, want := range pool {
		if got := p.pick(i); got != want {
			t.Fatalf("pick(%d) = %q, want %q", i, got, want)
		}
	}
}

func TestPickerSkipsAFailedHostInOrdinalRange(t *testing.T) {
	pool := testPool()
	p := newPicker(pool, rand.New(rand.NewSource(1)))
	p.fail(pool[1])

	if got := p.pick(0); got != pool[0] {
		t.Fatalf("pick(0) = %q, want %q", got, pool[0])
	}
	got := p.pick(1)
	if got == pool[1] {
		t.Fatalf("pick(1) returned the failed host %q", got)
	}
	if !contains(pool, got) {
		t.Fatalf("pick(1) = %q, which is not in the pool", got)
	}
}

func TestPickerSingleHostPoolAlwaysReturnsIt(t *testing.T) {
	// The russian channel offers exactly one host. Failing it must not make the
	// picker give up: the retry budget of a chunk decides when to stop, not the
	// pool. Otherwise the first network hiccup would kill the whole upload.
	only := Endpoint("https://ru-time28.anime-on.ru/")
	p := newPicker([]string{only}, rand.New(rand.NewSource(1)))

	if got := p.pick(0); got != only {
		t.Fatalf("pick(0) = %q, want %q", got, only)
	}
	p.fail(only)
	if got := p.pick(0); got != only {
		t.Fatalf("after fail, pick(0) = %q, want %q", got, only)
	}
	if got := p.pick(7); got != only {
		t.Fatalf("after fail, pick(7) = %q, want %q", got, only)
	}
}

func TestPickerEmptyPool(t *testing.T) {
	p := newPicker(nil, rand.New(rand.NewSource(1)))
	if got := p.pick(0); got != "" {
		t.Fatalf("pick(0) on an empty pool = %q, want the empty string", got)
	}
}

func TestPickerCopiesThePool(t *testing.T) {
	pool := testPool()
	p := newPicker(pool, rand.New(rand.NewSource(1)))
	pool[0] = "https://evil.example/upload.php"

	if got := p.pick(0); got == pool[0] {
		t.Fatalf("pick(0) followed a mutation of the caller's slice: %q", got)
	}
}

func TestPickerPrefersTheFastestHostBeyondTheOrdinalRange(t *testing.T) {
	pool := testPool()
	p := newPicker(pool, rand.New(rand.NewSource(1)))

	// The same number of bytes took a different time on each host, so the host
	// with the shortest duration is the fastest one.
	const chunk = DefaultPartSize
	p.record(pool[0], chunk, 4*time.Second)
	p.record(pool[1], chunk, 1*time.Second) // fastest
	p.record(pool[2], chunk, 2*time.Second)
	p.record(pool[3], chunk, 8*time.Second)

	for i := 0; i < 20; i++ {
		if got := p.pick(len(pool) + i); got != pool[1] {
			t.Fatalf("pick(%d) = %q, want the fastest host %q", len(pool)+i, got, pool[1])
		}
	}
}

func TestPickerSkipsTheFastestHostOnceItFails(t *testing.T) {
	pool := testPool()
	p := newPicker(pool, rand.New(rand.NewSource(1)))

	const chunk = DefaultPartSize
	p.record(pool[0], chunk, 4*time.Second)
	p.record(pool[1], chunk, 1*time.Second)
	p.record(pool[2], chunk, 2*time.Second)

	if got := p.pick(len(pool)); got != pool[1] {
		t.Fatalf("pick(%d) = %q, want %q", len(pool), got, pool[1])
	}
	p.fail(pool[1])
	if got := p.pick(len(pool)); got != pool[2] {
		t.Fatalf("after failing the fastest, pick(%d) = %q, want the runner up %q", len(pool), got, pool[2])
	}
}

func TestPickerZeroDurationIsIgnored(t *testing.T) {
	pool := testPool()
	p := newPicker(pool, rand.New(rand.NewSource(1)))

	p.record(pool[0], DefaultPartSize, 0)
	p.record(pool[1], DefaultPartSize, -time.Second)
	p.record(pool[2], DefaultPartSize, 3*time.Second)

	if got := p.pick(len(pool)); got != pool[2] {
		t.Fatalf("pick(%d) = %q, want %q: only one host has a usable measurement", len(pool), got, pool[2])
	}
}

func TestPickerFallsBackToRandomWithoutMeasurements(t *testing.T) {
	pool := testPool()
	p := newPicker(pool, rand.New(rand.NewSource(1)))

	seen := make(map[string]int)
	for i := 0; i < 400; i++ {
		host := p.pick(len(pool) + i)
		if !contains(pool, host) {
			t.Fatalf("pick returned %q, which is not in the pool", host)
		}
		seen[host]++
	}
	if len(seen) != len(pool) {
		t.Fatalf("random choice used %d of %d hosts over 400 picks: %v", len(seen), len(pool), seen)
	}
}

func TestPickerIsReproducibleForTheSameSeed(t *testing.T) {
	sequence := func() []string {
		p := newPicker(testPool(), rand.New(rand.NewSource(1)))
		out := make([]string, 0, 50)
		for i := 0; i < 50; i++ {
			out = append(out, p.pick(len(testPool())+i))
		}
		return out
	}
	first, second := sequence(), sequence()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two pickers with the same seed diverged:\n%v\n%v", first, second)
	}
}

func TestPickerResetsWhenEveryHostFailed(t *testing.T) {
	pool := testPool()
	p := newPicker(pool, rand.New(rand.NewSource(1)))

	for _, host := range pool {
		p.fail(host)
	}

	// With every host failed the next pick must still return a host from the
	// pool, and the marks must be gone afterwards.
	got := p.pick(len(pool))
	if !contains(pool, got) {
		t.Fatalf("after failing every host, pick returned %q, which is not in the pool", got)
	}
	// The marks are cleared, so the ordinal rule applies again: pick(i) for a
	// small i returns pool[i] exactly. That only holds if nothing is failed.
	for i, want := range pool {
		if got := p.pick(i); got != want {
			t.Fatalf("after the reset, pick(%d) = %q, want %q: the failure marks were not cleared", i, got, want)
		}
	}
}

func TestPickerResetKeepsWorkingRepeatedly(t *testing.T) {
	pool := testPool()
	p := newPicker(pool, rand.New(rand.NewSource(1)))

	for round := 0; round < 3; round++ {
		for _, host := range pool {
			p.fail(host)
		}
		if got := p.pick(len(pool)); !contains(pool, got) {
			t.Fatalf("round %d: pick returned %q, which is not in the pool", round, got)
		}
	}
}

func contains(pool []string, host string) bool {
	for _, h := range pool {
		if h == host {
			return true
		}
	}
	return false
}

// TestPickerOrdinalRangeIgnoresSpeedMeasurements pins down a rule from
// main.min.js that is easy to get backwards: inside the ordinal range
// (partIndex < len(pool)), a failed host falls back to a random not-failed
// host, never to the "fastest measured" host, even when speed measurements are
// available. The fastest-host branch only ever applies beyond the ordinal
// range (partIndex >= len(pool)); see docs/protocol.md, "Выбор хоста для
// чанка", step 1.
func TestPickerOrdinalRangeIgnoresSpeedMeasurements(t *testing.T) {
	pool := testPool()[:3]
	p := newPicker(pool, rand.New(rand.NewSource(1)))

	// pool[2] is by far the fastest measured host, but it must not be
	// preferred while resolving a fallback inside the ordinal range.
	p.record(pool[0], DefaultPartSize, 4*time.Second)
	p.record(pool[1], DefaultPartSize, 4*time.Second)
	p.record(pool[2], DefaultPartSize, 100*time.Millisecond)

	p.fail(pool[1])

	for i := 0; i < 50; i++ {
		got := p.pick(1)
		if got == pool[1] {
			t.Fatalf("pick(1) returned the failed host %q", got)
		}
		if !contains(pool, got) {
			t.Fatalf("pick(1) = %q, which is not in the pool", got)
		}
	}
}

// TestPickerIsSafeForConcurrentUse exercises pick/fail/record from many
// goroutines at once, the way the uploader's chunk workers do. It only asserts
// the absence of a race and of a panic, and that pick always returns a host
// from the pool; run with -race.
func TestPickerIsSafeForConcurrentUse(t *testing.T) {
	pool := testPool()
	p := newPicker(pool, rand.New(rand.NewSource(1)))

	const goroutines = 8
	const iterations = 1000

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				host := pool[(g+i)%len(pool)]
				switch i % 3 {
				case 0:
					if got := p.pick(i); !contains(pool, got) {
						t.Errorf("pick(%d) = %q, which is not in the pool", i, got)
					}
				case 1:
					p.fail(host)
				case 2:
					p.record(host, DefaultPartSize, time.Duration(i+1)*time.Millisecond)
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestPickerRecordIgnoresAFailedHost verifies that a speed measurement for a
// host already marked failed is dropped, not stored: a failed host should
// never become eligible again through fastestLocked just because a measurement
// arrived for it after the failure.
func TestPickerRecordIgnoresAFailedHost(t *testing.T) {
	pool := testPool()[:2]
	p := newPicker(pool, rand.New(rand.NewSource(1)))

	p.fail(pool[0])
	p.record(pool[0], DefaultPartSize, time.Second) // must be dropped: pool[0] is failed

	if got := p.fastestLocked(); got != "" {
		t.Fatalf("fastestLocked() = %q, want empty: the failed host's measurement must not be stored", got)
	}

	// Failing every host resets the failure marks (rule 4), which would make a
	// stored measurement for pool[0] visible again. If record() had actually
	// stored it despite the fail, fastestLocked would now return pool[0].
	p.fail(pool[1])
	p.pick(len(pool)) // triggers the reset inside randomLocked

	if got := p.fastestLocked(); got == pool[0] {
		t.Fatalf("fastestLocked() = %q, want the discarded measurement to stay discarded after a reset", got)
	}
}
