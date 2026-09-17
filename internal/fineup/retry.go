package fineup

import (
	"context"
	"errors"
	"time"
)

// Defaults of the uploader, taken from the site's configuration of Fine
// Uploader: five connections, four attempts per chunk, a keep alive ping every
// fifteen seconds.
const (
	DefaultMaxConns      = 5
	DefaultMaxAttempts   = 4
	DefaultTouchInterval = 15 * time.Second
)

// maxBackoff caps the pause between attempts.
const maxBackoff = 8 * time.Second

// isRetryable reports whether a failed request is worth repeating. Transport
// errors, 5xx, 408 and 429 are; a cancelled context never is, because it means
// the caller asked to stop rather than the server misbehaving.
func isRetryable(err error, status int) bool {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		return true
	}
	switch status {
	case 408, 429:
		return true
	}
	return status >= 500 && status <= 599
}

// defaultBackoff is the pause before the given attempt: one second, doubling,
// capped. It matters only when the pool holds a single host; with more hosts the
// next attempt goes to another host straight away.
func defaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 4 {
		attempt = 4
	}
	d := time.Second << (attempt - 1)
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}
