package progress

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Rendering is driven by explicit redraw calls, not by the ticker, so the
// output is fully deterministic.
func TestMultiRendersOneLinePerActiveUpload(t *testing.T) {
	var buf bytes.Buffer
	m := &Multi{W: &buf}

	a := m.Add("ep02.mp4", 1000)
	b := m.Add("ep03.mp4", 2000)
	a.Add(1000)
	b.Add(500)
	m.redraw()
	m.Print("sau: ep03.mp4: host x failed")
	m.Finish(a)
	m.redraw()

	out := buf.String()
	for _, want := range []string{"ep02.mp4", "ep03.mp4", "100%", "25%", "host x failed", "\x1b[2A"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	// The block is redrawn in place: cursor up by the height of the block,
	// never by more lines than were drawn.
	if strings.Contains(out, "\x1b[3A") {
		t.Errorf("output moves the cursor up 3 lines but at most 2 were active:\n%s", out)
	}
	// A finished line is printed once, above the block, and leaves it.
	last := out[strings.LastIndex(out, "\x1b["):]
	if strings.Contains(last, "ep02.mp4") {
		t.Errorf("the finished line is still part of the last block:\n%q", last)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output does not end with a newline: %q", out[len(out)-10:])
	}
}

func TestMultiTextGoesAboveTheBlock(t *testing.T) {
	var buf bytes.Buffer
	m := &Multi{W: &buf}
	l := m.Add("ep.mp4", 10)
	m.redraw()
	m.Print("note")
	m.Finish(l)

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	// Block: ep.mp4. Then: up 1, note, ep.mp4. Then: up 1, ep.mp4 (finished).
	want := []string{"ep.mp4", "\x1b[1Anote", "ep.mp4", "\x1b[1Aep.mp4"}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%q", len(lines), len(want), lines)
	}
	for i, w := range want {
		if !strings.HasPrefix(lines[i], w) {
			t.Errorf("line %d = %q, want prefix %q", i, lines[i], w)
		}
	}
}

func TestMultiLinesHaveAFixedWidth(t *testing.T) {
	var buf bytes.Buffer
	m := &Multi{W: &buf}
	l := m.Add("a very long file name that goes past the column and must be cut.mp4", 1234567)
	l.Add(999)
	m.Print("short")
	m.Finish(l)
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		// Only one line is ever active here, so the only cursor movement
		// that can precede a line is "up by one".
		line = strings.TrimPrefix(line, "\x1b[1A")
		if n := len([]rune(line)); n != lineWidth {
			t.Errorf("line width = %d, want %d: %q", n, lineWidth, line)
		}
	}
}

func TestMultiWritesNothingWithoutLines(t *testing.T) {
	var buf bytes.Buffer
	m := &Multi{W: &buf, Interval: time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.Run(ctx)
	if buf.Len() != 0 {
		t.Errorf("output = %q, want nothing", buf.String())
	}
}

// Run draws the final state once when ctx is already done.
func TestMultiRunDrawsFinalState(t *testing.T) {
	var buf bytes.Buffer
	m := &Multi{W: &buf}
	l := m.Add("ep.mp4", 4)
	l.Add(2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.Run(ctx)
	out := buf.String()
	if !strings.Contains(out, "ep.mp4") || !strings.Contains(out, " 50%") {
		t.Errorf("output = %q, want the line at 50%%", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("output = %q, want exactly one line", out)
	}
}

func TestIsTerminal(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) {
		t.Error("a buffer is not a terminal")
	}
	if IsTerminal(nil) {
		t.Error("nil is not a terminal")
	}
	var nilFile *os.File
	if IsTerminal(nilFile) {
		t.Error("a nil *os.File is not a terminal")
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) {
		t.Error("a regular file is not a terminal")
	}
}

// Two full-width byte counts, the normal size of an episode, must fit in a
// line without the total being cut off.
func TestMultiLineFitsFullWidthByteCounts(t *testing.T) {
	var buf bytes.Buffer
	m := &Multi{W: &buf}
	l := m.Add("ep02.mp4", 456_700_000)
	l.Add(123_400_000)
	m.Finish(l)
	if !strings.Contains(buf.String(), "123.4MB/456.7MB") {
		t.Errorf("output = %q, want it to contain 123.4MB/456.7MB intact", buf.String())
	}
}
