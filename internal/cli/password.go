package cli

import (
	"context"
	"fmt"
	"os"

	"sau/internal/config"
	"sau/internal/secrets"
)

// buildPassword assembles the password chain: environment, file, system
// keyring, interactive prompt. The chain is lazy: nothing is read until a login
// is actually needed.
//
// The file source follows the _FILE convention and covers docker secrets and
// LoadCredential= in systemd, which is the answer for a headless machine.
func buildPassword(cfg config.Config, d Deps) secrets.Source {
	return secrets.Chain(
		secrets.EnvSource{Name: "SAU_PASSWORD", Lookup: d.Env},
		secrets.FileSource{Name: "SAU_PASSWORD_FILE", Lookup: d.Env, ReadFile: os.ReadFile},
		secrets.KeyringSource{
			Ring:    secrets.SystemKeyring{},
			Service: keyringService,
			User:    cfg.User,
			// A Linux box without a graphical session has no keyring. That is
			// not a failure: say so once and move on to the next source.
			Warn: func(msg string) { fmt.Fprintf(d.Stderr, "sau: %s\n", msg) },
		},
		secrets.PromptSource{
			Prompt: secrets.ReadPassword,
			Label:  "password for " + cfg.User + " on " + cfg.Mirror + ": ",
		},
	)
}

// promptContext keeps the signature of secrets.ReadPassword honest: it takes a
// context and a label and returns the typed password.
var _ func(context.Context, string) (string, error) = secrets.ReadPassword
