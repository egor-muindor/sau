// Package secrets holds the password: a type that cannot be printed and the
// chain of sources it is read from.
//
// The password never reaches the config, the state, the cookie file or the
// log by construction of the type, not by discipline of the calling code.
// Reveal is called in exactly two places in the project: site.EncodeLoginForm
// and KeyringSource.Save.
package secrets

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
)

// Redacted is what stands in for a secret in every kind of output.
const Redacted = "[redacted]"

// Secret is a password. Every output path of the standard library is covered:
// fmt through Format, encoding/json through MarshalJSON, text marshalling
// through MarshalText, structured logging through LogValue.
type Secret struct {
	// _ makes Secret incomparable with ==. After the switch to a pointer
	// field, == would compare pointer identity rather than the password,
	// which is a trap for a map key or an equality check; forcing a compile
	// error there is safer than a silently wrong comparison.
	_ [0]func()

	// v is a pointer, not a string, so that fmt's reflection fallback for
	// unexported fields (used when a Secret is embedded in another package's
	// struct, where Format is not reachable) prints a pointer address
	// instead of the string it points to.
	v *string
}

// New wraps a plain password.
func New(s string) Secret { return Secret{v: &s} }

// Reveal returns the password itself. The legitimate callers are the login
// form encoder and KeyringSource.Save.
func (s Secret) Reveal() string {
	if s.v == nil {
		return ""
	}
	return *s.v
}

// IsZero reports whether no password is held.
func (s Secret) IsZero() bool { return s.v == nil || *s.v == "" }

// errEmptyPassword is returned by a source that got no usable password from
// the person or the store, e.g. empty input at a prompt.
var errEmptyPassword = errors.New("secrets: empty password")

// String implements fmt.Stringer. fmt never reaches it while Format is
// defined (Format takes over every verb, %v included); it exists for direct
// calls, e.g. by code that calls String() explicitly.
func (s Secret) String() string { return Redacted }

// GoString implements fmt.GoStringer, covering the %#v verb. Like String,
// fmt never reaches it while Format is defined; it exists for direct calls.
func (s Secret) GoString() string { return Redacted }

// Format implements fmt.Formatter for every verb, including %x and %q, so
// that no verb can be used to squeeze the value out.
func (s Secret) Format(f fmt.State, verb rune) {
	_, _ = io.WriteString(f, Redacted)
}

// MarshalJSON implements json.Marshaler.
func (s Secret) MarshalJSON() ([]byte, error) {
	return []byte(`"` + Redacted + `"`), nil
}

// MarshalText implements encoding.TextMarshaler.
func (s Secret) MarshalText() ([]byte, error) {
	return []byte(Redacted), nil
}

// LogValue implements slog.LogValuer.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(Redacted)
}

var (
	_ fmt.Formatter  = Secret{}
	_ fmt.Stringer   = Secret{}
	_ fmt.GoStringer = Secret{}
	_ slog.LogValuer = Secret{}
)
