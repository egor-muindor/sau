package fineup

import (
	"math/rand"
	"sync"
	"time"
)

// picker chooses the host for a chunk, reproducing the rule the site's script
// uses (main.min.js, initFileUploader, onUploadChunk):
//
//  1. while the part index is inside the pool, take the host at that index,
//     unless it is marked failed;
//  2. otherwise take the fastest measured host that is not failed;
//  3. otherwise take a random host that is not failed;
//  4. if every host is failed, clear the marks and take a random one.
//
// A single host pool, the russian channel, short-circuits before these rules:
// pick always returns the only host and fail is a no-op, so the health marks
// never end the upload there; the per chunk retry budget does.
//
// The pool holds request endpoints, already normalized by Endpoint. A picker is
// shared by the chunk goroutines, so every method takes the lock.
type picker struct {
	mu     sync.Mutex
	pool   []string
	rand   *rand.Rand
	failed map[string]bool
	speed  map[string]float64 // bytes per second, by host
}

// newPicker builds a picker over an already normalized pool. A nil r means the
// global source of math/rand.
func newPicker(pool []string, r *rand.Rand) *picker {
	return &picker{
		pool:   append([]string(nil), pool...),
		rand:   r,
		failed: make(map[string]bool),
		speed:  make(map[string]float64),
	}
}

// pick returns the host to use for the chunk with the given index, or the empty
// string when the pool is empty.
//
// A pool of one host is the RU channel: there is nothing to balance and nowhere
// to fall back to, so the choice is fixed.
func (p *picker) pick(partIndex int) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.pool) == 0 {
		return ""
	}
	if len(p.pool) == 1 {
		return p.pool[0]
	}
	if partIndex < len(p.pool) {
		if host := p.pool[partIndex]; !p.failed[host] {
			return host
		}
	} else if host := p.fastestLocked(); host != "" {
		return host
	}
	return p.randomLocked()
}

// fail marks a host as broken so that it is skipped from now on. Its speed
// measurement is dropped too: it described a host that no longer answers.
//
// With a single host the mark is skipped entirely: the retry budget of the
// chunk decides when to give up, not the health of the pool. Otherwise the
// first network hiccup would kill the only host and abort the upload instantly.
func (p *picker) fail(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.pool) == 1 {
		return
	}
	p.failed[host] = true
	delete(p.speed, host)
}

// record stores the throughput of a finished chunk. A non positive duration is
// ignored: it would divide by zero and says nothing about the host. A host
// already marked failed is ignored too: its measurement must not resurface
// after a rule 4 reset clears the failure marks.
func (p *picker) record(host string, bytes int64, d time.Duration) {
	if d <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.failed[host] {
		return
	}
	p.speed[host] = float64(bytes) / d.Seconds()
}

// fastestLocked returns the fastest measured host that is not failed, or the
// empty string when nothing has been measured yet.
func (p *picker) fastestLocked() string {
	best := ""
	bestSpeed := 0.0
	for _, host := range p.pool {
		if p.failed[host] {
			continue
		}
		speed, measured := p.speed[host]
		if !measured {
			continue
		}
		if best == "" || speed > bestSpeed {
			best, bestSpeed = host, speed
		}
	}
	return best
}

// randomLocked returns a random host that is not failed. When every host is
// failed the marks are cleared first, so that the pool never runs dry.
func (p *picker) randomLocked() string {
	alive := make([]string, 0, len(p.pool))
	for _, host := range p.pool {
		if !p.failed[host] {
			alive = append(alive, host)
		}
	}
	if len(alive) == 0 {
		p.failed = make(map[string]bool)
		alive = append(alive, p.pool...)
	}
	return alive[p.intn(len(alive))]
}

func (p *picker) intn(n int) int {
	if p.rand == nil {
		return rand.Intn(n)
	}
	return p.rand.Intn(n)
}
