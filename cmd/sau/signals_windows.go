//go:build windows

package main

import "os"

// Windows has no SIGTERM or SIGHUP; os.Interrupt is what a console sends.
var interruptSignals = []os.Signal{os.Interrupt}
