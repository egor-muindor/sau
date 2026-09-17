package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Source produces a password. ok == false means "not configured here, try the
// next one"; an error means "configured but broken" and stops the chain.
type Source interface {
	Password(ctx context.Context) (Secret, bool, error)
}

type chain []Source

// Chain tries the sources in order and returns the first one that yields a
// password. An error from any source aborts the chain: silently falling
// through would hide an unreadable secret file.
func Chain(srcs ...Source) Source { return chain(srcs) }

func (c chain) Password(ctx context.Context) (Secret, bool, error) {
	for _, s := range c {
		sec, ok, err := s.Password(ctx)
		if err != nil {
			return Secret{}, false, err
		}
		if ok {
			return sec, true, nil
		}
	}
	return Secret{}, false, nil
}

// EnvSource reads the password from an environment variable, SAU_PASSWORD in
// practice. An unset or empty variable means "not configured".
type EnvSource struct {
	Name   string
	Lookup func(string) (string, bool)
}

func (e EnvSource) Password(ctx context.Context) (Secret, bool, error) {
	lookup := e.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	v, ok := lookup(e.Name)
	if !ok || v == "" {
		return Secret{}, false, nil
	}
	return New(v), true, nil
}

// FileSource reads the password from the file named by an environment
// variable, following the _FILE convention: it covers docker secrets and
// LoadCredential= in systemd, which is the answer to headless environments.
//
// Trailing "\r" and "\n" are stripped: editors and echo add a line break that
// is not part of the password. Inner whitespace is kept.
type FileSource struct {
	Name     string
	Lookup   func(string) (string, bool)
	ReadFile func(string) ([]byte, error)
}

func (f FileSource) Password(ctx context.Context) (Secret, bool, error) {
	lookup := f.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	readFile := f.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}

	path, ok := lookup(f.Name)
	if !ok || path == "" {
		return Secret{}, false, nil
	}
	data, err := readFile(path)
	if err != nil {
		// Only the path is reported; the content never goes into an error.
		return Secret{}, false, fmt.Errorf("secrets: %s: read %s: %w", f.Name, path, err)
	}
	v := strings.TrimRight(string(data), "\r\n")
	if v == "" {
		return Secret{}, false, fmt.Errorf("secrets: %s: file %s is empty", f.Name, path)
	}
	return New(v), true, nil
}
