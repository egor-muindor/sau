// Command sau publishes a translation on smotret-anime.online in one command.
package main

import (
	"context"
	"os"
	"os/signal"

	"sau/internal/cli"
)

func main() {
	// The first signal cancels the context, which stops the chunk uploads and
	// the touch goroutine cleanly and leaves the state on disk for a resume.
	ctx, stop := signal.NotifyContext(context.Background(), interruptSignals...)
	defer stop()

	os.Exit(cli.Run(ctx, os.Args[1:], cli.Deps{}))
}
