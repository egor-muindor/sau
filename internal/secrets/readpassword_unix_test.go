//go:build unix

package secrets

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// There is no console at all: the error must be recognizable and must point at
// the environment variables.
func TestReadPasswordWithoutConsole(t *testing.T) {
	old := ttyPath
	ttyPath = filepath.Join(t.TempDir(), "no-such-tty")
	t.Cleanup(func() { ttyPath = old })

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
// opened: ReadPassword blocks on a read that ctx cannot interrupt once
// started, so the only place ctx can be honored is before that read begins.
func TestReadPasswordWithCancelledContext(t *testing.T) {
	old := ttyPath
	ttyPath = filepath.Join(t.TempDir(), "no-such-tty")
	t.Cleanup(func() { ttyPath = old })

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
