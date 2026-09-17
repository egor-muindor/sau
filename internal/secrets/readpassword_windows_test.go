//go:build windows

package secrets

import (
	"context"
	"errors"
	"testing"
)

// There is no console at all: the error must be recognizable and must point at
// the environment variables.
func TestReadPasswordWithoutConsole(t *testing.T) {
	oldIn, oldOut := conInPath, conOutPath
	conInPath = "NUL:\\no-such-console-in"
	conOutPath = "NUL:\\no-such-console-out"
	t.Cleanup(func() { conInPath, conOutPath = oldIn, oldOut })

	_, err := ReadPassword(context.Background(), "Password: ")
	if err == nil {
		t.Fatal("ReadPassword = nil error, want ErrNoConsole")
	}
	if !errors.Is(err, ErrNoConsole) {
		t.Fatalf("err = %v, want it to wrap ErrNoConsole", err)
	}
	if !contains(ErrNoConsole.Error(), "SAU_PASSWORD") {
		t.Errorf("ErrNoConsole = %q, want it to mention SAU_PASSWORD", ErrNoConsole)
	}
}

// An already-cancelled context must fail fast, before the console is even
// opened: see the comment on ReadPassword for why ctx cannot interrupt the
// blocking read itself.
func TestReadPasswordWithCancelledContext(t *testing.T) {
	oldIn, oldOut := conInPath, conOutPath
	conInPath = "NUL:\\no-such-console-in"
	conOutPath = "NUL:\\no-such-console-out"
	t.Cleanup(func() { conInPath, conOutPath = oldIn, oldOut })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ReadPassword(ctx, "Password: ")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want it to wrap context.Canceled", err)
	}
	if errors.Is(err, ErrNoConsole) {
		t.Fatal("err wraps ErrNoConsole: the console must not be opened when ctx is already done")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
