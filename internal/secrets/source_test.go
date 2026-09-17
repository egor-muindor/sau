package secrets_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"sau/internal/secrets"
)

// fakeSource is a scripted Source.
type fakeSource struct {
	value string
	ok    bool
	err   error
	calls *int
}

func (f fakeSource) Password(ctx context.Context) (secrets.Secret, bool, error) {
	if f.calls != nil {
		*f.calls++
	}
	if f.err != nil {
		return secrets.Secret{}, false, f.err
	}
	return secrets.New(f.value), f.ok, nil
}

func env(pairs map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := pairs[name]
		return v, ok
	}
}

func TestChainTakesTheFirstOkSource(t *testing.T) {
	third := 0
	c := secrets.Chain(
		fakeSource{ok: false},
		fakeSource{value: "from-second", ok: true},
		fakeSource{value: "from-third", ok: true, calls: &third},
	)

	got, ok, err := c.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got.Reveal() != "from-second" {
		t.Errorf("password = %q, want from-second", got.Reveal())
	}
	if third != 0 {
		t.Errorf("third source was called %d times, want 0", third)
	}
}

func TestChainStopsOnError(t *testing.T) {
	boom := errors.New("boom")
	next := 0
	c := secrets.Chain(
		fakeSource{err: boom},
		fakeSource{value: "unreachable", ok: true, calls: &next},
	)

	_, ok, err := c.Password(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if ok {
		t.Error("ok = true, want false")
	}
	if next != 0 {
		t.Errorf("next source was called %d times, want 0", next)
	}
}

func TestChainWithNoOkSource(t *testing.T) {
	_, ok, err := secrets.Chain(fakeSource{ok: false}, fakeSource{ok: false}).Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if ok {
		t.Error("ok = true, want false")
	}
}

func TestEnvSource(t *testing.T) {
	s := secrets.EnvSource{Name: "SAU_PASSWORD", Lookup: env(map[string]string{"SAU_PASSWORD": "from-env"})}

	got, ok, err := s.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if !ok || got.Reveal() != "from-env" {
		t.Errorf("Password = (%q, %v), want (from-env, true)", got.Reveal(), ok)
	}
}

func TestEnvSourceEmptyValueIsNotSet(t *testing.T) {
	s := secrets.EnvSource{Name: "SAU_PASSWORD", Lookup: env(map[string]string{"SAU_PASSWORD": ""})}

	_, ok, err := s.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if ok {
		t.Error("ok = true for an empty variable, want false")
	}
}

func TestEnvSourceMissingVariable(t *testing.T) {
	s := secrets.EnvSource{Name: "SAU_PASSWORD", Lookup: env(nil)}

	_, ok, err := s.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if ok {
		t.Error("ok = true for a missing variable, want false")
	}
}

func TestFileSourceTrimsTrailingNewline(t *testing.T) {
	s := secrets.FileSource{
		Name:   "SAU_PASSWORD_FILE",
		Lookup: env(map[string]string{"SAU_PASSWORD_FILE": "/run/secrets/sau"}),
		ReadFile: func(path string) ([]byte, error) {
			if path != "/run/secrets/sau" {
				t.Errorf("ReadFile(%q), want /run/secrets/sau", path)
			}
			return []byte("from-file\r\n"), nil
		},
	}

	got, ok, err := s.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if !ok || got.Reveal() != "from-file" {
		t.Errorf("Password = (%q, %v), want (from-file, true)", got.Reveal(), ok)
	}
}

func TestFileSourceKeepsInnerWhitespace(t *testing.T) {
	s := secrets.FileSource{
		Name:     "SAU_PASSWORD_FILE",
		Lookup:   env(map[string]string{"SAU_PASSWORD_FILE": "/run/secrets/sau"}),
		ReadFile: func(string) ([]byte, error) { return []byte(" pass word \n"), nil },
	}

	got, _, err := s.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if got.Reveal() != " pass word " {
		t.Errorf("password = %q, want %q", got.Reveal(), " pass word ")
	}
}

func TestFileSourceMissingVariable(t *testing.T) {
	s := secrets.FileSource{Name: "SAU_PASSWORD_FILE", Lookup: env(nil)}

	_, ok, err := s.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if ok {
		t.Error("ok = true for a missing variable, want false")
	}
}

func TestFileSourceMissingFileIsAnError(t *testing.T) {
	s := secrets.FileSource{
		Name:     "SAU_PASSWORD_FILE",
		Lookup:   env(map[string]string{"SAU_PASSWORD_FILE": "/run/secrets/nope"}),
		ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
	}

	_, ok, err := s.Password(context.Background())
	if err == nil {
		t.Fatal("err = nil, want an error for an unreadable secret file")
	}
	if ok {
		t.Error("ok = true, want false")
	}
}

func TestFileSourceEmptyFileIsAnError(t *testing.T) {
	s := secrets.FileSource{
		Name:     "SAU_PASSWORD_FILE",
		Lookup:   env(map[string]string{"SAU_PASSWORD_FILE": "/run/secrets/sau"}),
		ReadFile: func(string) ([]byte, error) { return []byte("\n"), nil },
	}

	if _, _, err := s.Password(context.Background()); err == nil {
		t.Fatal("err = nil, want an error for an empty secret file")
	}
}

// The error of a file source must not carry the password; it may only name the
// path. This is checked by passing a canary as the file content.
func TestFileSourceErrorDoesNotLeak(t *testing.T) {
	s := secrets.FileSource{
		Name:     "SAU_PASSWORD_FILE",
		Lookup:   env(map[string]string{"SAU_PASSWORD_FILE": "/run/secrets/sau"}),
		ReadFile: func(string) ([]byte, error) { return []byte(canary), errors.New("read failed") },
	}

	_, _, err := s.Password(context.Background())
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if got := err.Error(); len(got) > 0 && containsCanary(got) {
		t.Fatalf("error leaks the secret: %q", got)
	}
}

func containsCanary(s string) bool {
	return len(s) >= len(canary) && (s == canary || indexOf(s, canary) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

var _ secrets.Source = secrets.EnvSource{}
var _ secrets.Source = secrets.FileSource{}
