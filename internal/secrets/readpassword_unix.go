//go:build unix

package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"
)

// ErrNoConsole reports that there is no console to type the password into.
var ErrNoConsole = errors.New(
	"secrets: no console available; set SAU_PASSWORD or SAU_PASSWORD_FILE")

// ttyPath is a package variable so that the no-console branch is testable.
var ttyPath = "/dev/tty"

// ReadPassword reads the password with echo off.
//
// The console is opened directly instead of using standard input, so that the
// prompt works even when stdin is taken by a pipe or a file.
//
// ctx is only checked before the console is opened. The blocking read of
// term.ReadPassword below cannot be interrupted by a context in a portable
// way (there is no cross-platform way to cancel a read from a terminal fd),
// so an already-cancelled context is honored up front, and a context that is
// cancelled while the person is typing has no effect until they finish.
func ReadPassword(ctx context.Context, label string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	f, err := os.OpenFile(ttyPath, os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("%w (%s: %v)", ErrNoConsole, ttyPath, err)
	}
	defer f.Close()

	if _, err := fmt.Fprint(f, label); err != nil {
		return "", fmt.Errorf("%w (%s: %v)", ErrNoConsole, ttyPath, err)
	}
	b, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(f)
	if err != nil {
		return "", fmt.Errorf("secrets: read password: %w", err)
	}
	return string(b), nil
}
