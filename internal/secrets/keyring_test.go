package secrets_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sau/internal/secrets"
)

// fakeRing is a scripted keyring.
type fakeRing struct {
	value string
	err   error

	setService, setUser, setPass string
	deleted                      bool
}

func (r *fakeRing) Get(service, user string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.value, nil
}

func (r *fakeRing) Set(service, user, pass string) error {
	r.setService, r.setUser, r.setPass = service, user, pass
	return nil
}

func (r *fakeRing) Delete(service, user string) error {
	r.deleted = true
	return nil
}

func TestKeyringSourceReturnsStoredPassword(t *testing.T) {
	s := secrets.KeyringSource{Ring: &fakeRing{value: "from-keyring"}, Service: "sau", User: "me@example.test"}

	got, ok, err := s.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if !ok || got.Reveal() != "from-keyring" {
		t.Errorf("Password = (%q, %v), want (from-keyring, true)", got.Reveal(), ok)
	}
}

// Linux without a graphical session: the keyring is simply not there. The chain
// must go on, and the user must be told why.
func TestKeyringSourceWarnsAndFallsThroughOnFailure(t *testing.T) {
	var warnings []string
	s := secrets.KeyringSource{
		Ring:    &fakeRing{err: errors.New("dbus: no session bus")},
		Service: "sau",
		User:    "me@example.test",
		Warn:    func(msg string) { warnings = append(warnings, msg) },
	}

	_, ok, err := s.Password(context.Background())
	if err != nil {
		t.Fatalf("Password = %v, want no error: the chain must continue", err)
	}
	if ok {
		t.Error("ok = true, want false")
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	if !strings.Contains(warnings[0], "SAU_PASSWORD") {
		t.Errorf("warning = %q, want it to mention SAU_PASSWORD", warnings[0])
	}
}

// No stored password is a normal state, not a failure: no warning.
func TestKeyringSourceIsQuietWhenEntryIsMissing(t *testing.T) {
	var warnings []string
	s := secrets.KeyringSource{
		Ring:    &fakeRing{err: secrets.ErrNotFound},
		Service: "sau",
		User:    "me@example.test",
		Warn:    func(msg string) { warnings = append(warnings, msg) },
	}

	_, ok, err := s.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if ok {
		t.Error("ok = true, want false")
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for a missing entry", warnings)
	}
}

func TestKeyringSourceWithoutRingIsSkipped(t *testing.T) {
	_, ok, err := secrets.KeyringSource{Service: "sau", User: "me"}.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if ok {
		t.Error("ok = true, want false")
	}
}

func TestKeyringSourceEmptyEntryIsSkipped(t *testing.T) {
	s := secrets.KeyringSource{Ring: &fakeRing{value: ""}, Service: "sau", User: "me"}

	_, ok, err := s.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if ok {
		t.Error("ok = true for an empty entry, want false")
	}
}

func TestPromptSourceAsksAndReturns(t *testing.T) {
	var asked string
	s := secrets.PromptSource{
		Label: "Password for me@example.test: ",
		Prompt: func(ctx context.Context, label string) (string, error) {
			asked = label
			return "typed-in", nil
		},
	}

	got, ok, err := s.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if !ok || got.Reveal() != "typed-in" {
		t.Errorf("Password = (%q, %v), want (typed-in, true)", got.Reveal(), ok)
	}
	if asked != "Password for me@example.test: " {
		t.Errorf("label = %q, want the configured one", asked)
	}
}

func TestPromptSourceEmptyInputIsAnError(t *testing.T) {
	s := secrets.PromptSource{Prompt: func(context.Context, string) (string, error) { return "", nil }}

	_, ok, err := s.Password(context.Background())
	if err == nil {
		t.Fatal("err = nil, want an error for empty input")
	}
	if ok {
		t.Error("ok = true, want false")
	}
}

func TestPromptSourcePropagatesError(t *testing.T) {
	boom := errors.New("boom")
	s := secrets.PromptSource{Prompt: func(context.Context, string) (string, error) { return "", boom }}

	if _, _, err := s.Password(context.Background()); !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestKeyringSourceSaveStoresThroughTheRing(t *testing.T) {
	ring := &fakeRing{}
	s := secrets.KeyringSource{Ring: ring, Service: "sau", User: "me@example.test"}

	if err := s.Save(secrets.New(canary)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if ring.setService != "sau" || ring.setUser != "me@example.test" {
		t.Errorf("Set(%q, %q), want (sau, me@example.test)", ring.setService, ring.setUser)
	}
	if ring.setPass != canary {
		t.Errorf("stored password = %q, want the real value", ring.setPass)
	}
}

func TestKeyringSourceSaveRefusesWithoutRingOrPassword(t *testing.T) {
	if err := (secrets.KeyringSource{Service: "sau", User: "me"}).Save(secrets.New(canary)); err == nil {
		t.Error("Save without a ring = nil, want an error")
	}
	s := secrets.KeyringSource{Ring: &fakeRing{}, Service: "sau", User: "me"}
	if err := s.Save(secrets.Secret{}); err == nil {
		t.Error("Save of an empty secret = nil, want an error")
	}
}

// The real adapter is not exercised against the system keyring; it is only
// checked to satisfy the interface and to compile.
var _ secrets.Keyring = secrets.SystemKeyring{}

var _ secrets.Source = secrets.KeyringSource{}
var _ secrets.Source = secrets.PromptSource{}

// The chain of the architecture: env, file, keyring, prompt.
func TestDefaultChainOrder(t *testing.T) {
	c := secrets.Chain(
		secrets.EnvSource{Name: "SAU_PASSWORD", Lookup: env(nil)},
		secrets.FileSource{Name: "SAU_PASSWORD_FILE", Lookup: env(nil)},
		secrets.KeyringSource{Ring: &fakeRing{err: secrets.ErrNotFound}, Service: "sau", User: "me"},
		secrets.PromptSource{Prompt: func(context.Context, string) (string, error) { return "typed-in", nil }},
	)

	got, ok, err := c.Password(context.Background())
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if !ok || got.Reveal() != "typed-in" {
		t.Errorf("Password = (%q, %v), want (typed-in, true)", got.Reveal(), ok)
	}
}
