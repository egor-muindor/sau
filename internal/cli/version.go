package cli

import "fmt"

// Version is stamped by the release build through
// -ldflags "-X sau/internal/cli.Version=v1.2.3"; a plain go build says "dev".
var Version = "dev"

func cmdVersion(d Deps) error {
	_, err := fmt.Fprintln(d.Stdout, "sau "+Version)
	return err
}
