package progress

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBar(t *testing.T) {
	var buf bytes.Buffer
	b := &Bar{W: &buf, Total: 1000, Interval: 5 * time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Run(ctx)
	}()

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				b.Add(10)
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()

	cancel()
	<-done

	out := buf.String()
	if out == "" {
		t.Fatal("Bar wrote nothing")
	}
	if strings.Contains(out, "\x1b") {
		t.Error("output contains an escape sequence")
	}
	if strings.Contains(out, "\n") {
		t.Error("output contains a newline; the bar must stay on one line")
	}
	for i := 0; i < len(out); i++ {
		if out[i] >= 0x80 {
			t.Fatalf("non-ASCII byte %#x at offset %d", out[i], i)
		}
	}

	lines := strings.Split(out, "\r")
	if lines[0] != "" {
		t.Errorf("output does not start with a carriage return: %q", lines[0])
	}
	for _, l := range lines[1:] {
		if l == "" {
			t.Error("empty rendered line")
		}
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "100%") {
		t.Errorf("last line = %q, want it to contain 100%%", last)
	}
}

func TestBarZeroTotal(t *testing.T) {
	var buf bytes.Buffer
	b := &Bar{W: &buf, Total: 0, Interval: time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.Run(ctx)
	if !strings.Contains(buf.String(), "0%") {
		t.Errorf("output = %q, want 0%% for an unknown total", buf.String())
	}
}
