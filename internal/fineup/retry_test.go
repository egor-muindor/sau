package fineup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	if DefaultMaxConns != 5 {
		t.Fatalf("DefaultMaxConns = %d, want 5", DefaultMaxConns)
	}
	if DefaultMaxAttempts != 4 {
		t.Fatalf("DefaultMaxAttempts = %d, want 4", DefaultMaxAttempts)
	}
	if DefaultTouchInterval != 15*time.Second {
		t.Fatalf("DefaultTouchInterval = %v, want 15s", DefaultTouchInterval)
	}
}

func TestIsRetryable(t *testing.T) {
	netErr := &net.OpError{Op: "dial", Err: errors.New("connection refused")}

	tests := []struct {
		name   string
		err    error
		status int
		want   bool
	}{
		{"ok", nil, 200, false},
		{"created", nil, 201, false},
		{"bad request", nil, 400, false},
		{"unauthorized", nil, 401, false},
		{"forbidden", nil, 403, false},
		{"not found", nil, 404, false},
		{"gone", nil, 410, false},
		{"unprocessable", nil, 422, false},
		{"request timeout", nil, 408, true},
		{"too many requests", nil, 429, true},
		{"internal server error", nil, 500, true},
		{"bad gateway", nil, 502, true},
		{"service unavailable", nil, 503, true},
		{"gateway timeout", nil, 504, true},
		{"network error", netErr, 0, true},
		{"unexpected eof", io.ErrUnexpectedEOF, 0, true},
		{"wrapped network error", fmt.Errorf("post chunk: %w", netErr), 0, true},
		{"context canceled", context.Canceled, 0, false},
		{"wrapped context canceled", fmt.Errorf("chunk 3: %w", context.Canceled), 0, false},
		{"context deadline exceeded", context.DeadlineExceeded, 0, false},
		{"wrapped deadline exceeded", fmt.Errorf("chunk 3: %w", context.DeadlineExceeded), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryable(tt.err, tt.status); got != tt.want {
				t.Fatalf("isRetryable(%v, %d) = %v, want %v", tt.err, tt.status, got, tt.want)
			}
		})
	}
}

func TestDefaultBackoff(t *testing.T) {
	tests := []struct {
		name    string
		attempt int
		want    time.Duration
	}{
		{"zero is treated as the first attempt", 0, time.Second},
		{"negative is treated as the first attempt", -3, time.Second},
		{"first", 1, time.Second},
		{"second", 2, 2 * time.Second},
		{"third", 3, 4 * time.Second},
		{"fourth", 4, 8 * time.Second},
		{"fifth is capped", 5, 8 * time.Second},
		{"far beyond the budget is capped", 40, 8 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultBackoff(tt.attempt); got != tt.want {
				t.Fatalf("defaultBackoff(%d) = %v, want %v", tt.attempt, got, tt.want)
			}
		})
	}
}

func TestDefaultBackoffNeverDecreases(t *testing.T) {
	prev := time.Duration(0)
	for attempt := 1; attempt <= 10; attempt++ {
		got := defaultBackoff(attempt)
		if got < prev {
			t.Fatalf("defaultBackoff(%d) = %v, less than the previous %v", attempt, got, prev)
		}
		prev = got
	}
}
