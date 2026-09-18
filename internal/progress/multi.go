package progress

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/term"
)

// DefaultMultiInterval is the redraw period of a Multi when Interval is zero.
// Once a second is enough for several long uploads and keeps a scrollback
// readable.
const DefaultMultiInterval = time.Second

// nameWidth is the column for the file name inside a line.
const nameWidth = 36

// bytesWidth is the widest string formatBytes returns: three digits, a point,
// one decimal, a unit letter and "B" ("999.9MB").
const bytesWidth = 7

// lineWidth is the width every rendered line is padded or cut to. Fixed-width
// lines let the block be redrawn in place without clearing anything. It is
// derived from the layout of Line.render so that the widest realistic line
// (two full byte counts) fits without truncation:
//
//	name, " [", bar, "] ", "100%", " ", bytes, "/", bytes
const lineWidth = nameWidth + 2 + barWidth + 2 + 4 + 1 + bytesWidth + 1 + bytesWidth

// IsTerminal reports whether w is a terminal. Multi is only worth drawing on
// one: in a pipe or a log the cursor movement would be noise, so the caller
// falls back to plain text lines.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// Line is the progress of one file inside a Multi.
type Line struct {
	name  string
	total int64
	n     atomic.Int64
	done  bool // guarded by the Multi's mutex
}

// Add records n more bytes. It is safe to call from several goroutines.
func (l *Line) Add(n int64) { l.n.Add(n) }

// Multi draws one line per active upload as a block at the bottom of the
// output and redraws the block in place. Text lines (Print) and finished
// lines go above the block and scroll away with the output; the block
// itself never grows the scrollback.
//
// The block is moved with one escape sequence, cursor up ("\x1b[NA"). That
// is the one exception to the ASCII-only rule of Bar, and it is why Multi
// is drawn only on a terminal (IsTerminal): a pipe gets text lines instead.
type Multi struct {
	W        io.Writer
	Interval time.Duration

	mu      sync.Mutex
	lines   []*Line
	pending []string // text to print above the block on the next render
	drawn   int      // lines of the block written by the last render
}

// Add opens a line for name. The line appears on the next redraw.
func (m *Multi) Add(name string, total int64) *Line {
	l := &Line{name: name, total: total}
	m.mu.Lock()
	m.lines = append(m.lines, l)
	m.mu.Unlock()
	return l
}

// Finish prints the final state of l above the block and frees its slot.
func (m *Multi) Finish(l *Line) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l.done = true
	m.render()
}

// Print writes a text line above the block, keeping the block intact.
func (m *Multi) Print(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = append(m.pending, text)
	m.render()
}

// Run redraws the block until ctx is done, then draws once more so that the
// final state is visible. It returns when ctx is done.
func (m *Multi) Run(ctx context.Context) {
	interval := m.Interval
	if interval <= 0 {
		interval = DefaultMultiInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.redraw()
			return
		case <-t.C:
			m.redraw()
		}
	}
}

// redraw renders under the mutex. It is what the ticker does on every tick.
func (m *Multi) redraw() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.render()
}

// render redraws everything with the mutex held. The cursor goes up to the
// top of the previous block; then the pending text, the finished lines and
// the active lines are written, one per line. Every line of the old block is
// either finished (printed once, above) or still active (redrawn), and new
// lines only add to that, so the new output always covers the old block.
func (m *Multi) render() {
	if len(m.lines) == 0 && len(m.pending) == 0 && m.drawn == 0 {
		return
	}
	var sb strings.Builder
	if m.drawn > 0 {
		sb.WriteString("\x1b[" + strconv.Itoa(m.drawn) + "A")
	}
	for _, t := range m.pending {
		sb.WriteString(pad(t))
		sb.WriteByte('\n')
	}
	m.pending = nil

	active := make([]*Line, 0, len(m.lines))
	for _, l := range m.lines {
		if l.done {
			sb.WriteString(l.render())
			sb.WriteByte('\n')
			continue
		}
		active = append(active, l)
	}
	for _, l := range active {
		sb.WriteString(l.render())
		sb.WriteByte('\n')
	}
	m.lines = active
	m.drawn = len(active)
	io.WriteString(m.W, sb.String())
}

// render draws the line: the name in a fixed column, then the same bar Bar
// draws, padded to lineWidth.
func (l *Line) render() string {
	cur := l.n.Load()
	pct := 0
	if l.total > 0 {
		pct = int(cur * 100 / l.total)
		if pct > 100 {
			pct = 100
		}
		if pct < 0 {
			pct = 0
		}
	}
	filled := barWidth * pct / 100
	name := []rune(l.name)
	if len(name) > nameWidth {
		name = append(name[:nameWidth-1], '~')
	}
	s := fmt.Sprintf("%-*s [%s%s] %3d%% %s/%s",
		nameWidth, string(name),
		strings.Repeat("=", filled), strings.Repeat(" ", barWidth-filled),
		pct, formatBytes(cur), formatBytes(l.total))
	return pad(s)
}

// pad cuts or pads s to lineWidth runes.
func pad(s string) string {
	r := []rune(s)
	if len(r) > lineWidth {
		return string(r[:lineWidth])
	}
	return s + strings.Repeat(" ", lineWidth-len(r))
}
