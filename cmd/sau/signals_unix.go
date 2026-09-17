//go:build unix

package main

import (
	"os"
	"syscall"
)

// interruptSignals also covers SIGHUP: closing the terminal during a multi-hour
// upload must stop it the same way Ctrl-C does.
var interruptSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}
