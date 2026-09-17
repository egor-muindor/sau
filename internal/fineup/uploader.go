package fineup

import (
	"math/rand"
	"net/http"
	"time"
)

// Uploader performs chunked uploads. The zero value works; the fields exist so
// that tests can make time and randomness deterministic and so that the caller
// can supply the single redacting transport the project requires.
type Uploader struct {
	Client        *http.Client
	TouchInterval time.Duration // 0 means DefaultTouchInterval
	MaxAttempts   int           // 0 means DefaultMaxAttempts
	Backoff       func(attempt int) time.Duration
	Rand          *rand.Rand
	Now           func() time.Time
}
