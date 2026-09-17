//go:build windows

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

// The console devices are package variables so that the no-console branch is
// testable.
var (
	conInPath  = "CONIN$"
	conOutPath = "CONOUT$"
)

// ReadPassword reads the password with echo off.
//
// The console devices are opened directly instead of using standard input, so
// that the prompt works even when stdin is taken by a pipe or a file.
//
// ctx is only checked before the console is opened. The blocking read of
// term.ReadPassword below cannot be interrupted by a context in a portable
// way (there is no cross-platform way to cancel a read from a console
// handle), so an already-cancelled context is honored up front, and a
// context that is cancelled while the person is typing has no effect until
// they finish.
func ReadPassword(ctx context.Context, label string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	in, err := os.OpenFile(conInPath, os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("%w (%s: %v)", ErrNoConsole, conInPath, err)
	}
	defer in.Close()

	out, err := os.OpenFile(conOutPath, os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("%w (%s: %v)", ErrNoConsole, conOutPath, err)
	}
	defer out.Close()

	if _, err := fmt.Fprint(out, label); err != nil {
		return "", fmt.Errorf("%w (%s: %v)", ErrNoConsole, conOutPath, err)
	}
	b, err := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("secrets: read password: %w", err)
	}
	return string(b), nil
}
