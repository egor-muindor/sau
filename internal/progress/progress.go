// Package progress renders a single-line upload progress bar.
//
// The bar uses a carriage return and plain ASCII only: no escape sequences, so
// it behaves the same in a Windows console, in a pipe and in a CI log.
package progress

import (
	"context"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultInterval is the redraw period when Interval is zero.
const DefaultInterval = 200 * time.Millisecond

const barWidth = 20

// Bar counts bytes atomically and redraws on a timer, so that reporting never
// slows the upload down.
type Bar struct {
	W        io.Writer
	Total    int64
	Interval time.Duration

	n atomic.Int64
}

// Add records n more bytes. It is safe to call from several goroutines.
func (b *Bar) Add(n int64) { b.n.Add(n) }

// Run redraws the bar until ctx is done, then draws once more so that the final
// state is visible. It returns when ctx is done.
func (b *Bar) Run(ctx context.Context) {
	interval := b.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			b.render()
			return
		case <-t.C:
			b.render()
		}
	}
}

func (b *Bar) render() {
	cur := b.n.Load()
	pct := 0
	if b.Total > 0 {
		pct = int(cur * 100 / b.Total)
		if pct > 100 {
			pct = 100
		}
		if pct < 0 {
			pct = 0
		}
	}
	filled := barWidth * pct / 100

	var sb strings.Builder
	sb.WriteByte('\r')
	sb.WriteByte('[')
	sb.WriteString(strings.Repeat("=", filled))
	sb.WriteString(strings.Repeat(" ", barWidth-filled))
	sb.WriteString("] ")
	sb.WriteString(strconv.Itoa(pct))
	sb.WriteString("% ")
	sb.WriteString(formatBytes(cur))
	sb.WriteByte('/')
	sb.WriteString(formatBytes(b.Total))
	io.WriteString(b.W, sb.String())
}

// formatBytes renders a byte count in ASCII with one decimal place.
func formatBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return strconv.FormatInt(n, 10) + "B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	whole := n / div
	frac := (n % div) * 10 / div
	return strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(frac, 10) +
		string([]byte{"kMGTPE"[exp]}) + "B"
}
