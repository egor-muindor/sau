package secrets

import (
	"context"
	"errors"

	"github.com/zalando/go-keyring"
)

// ErrNotFound reports that the system keyring holds no entry. It is a normal
// state, not a failure, and does not deserve a warning.
var ErrNotFound = keyring.ErrNotFound

// Keyring is the system password store, declared here so that it can be
// replaced in tests.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, pass string) error
	Delete(service, user string) error
}

// SystemKeyring is the real adapter over github.com/zalando/go-keyring: the
// Keychain on macOS, the Credential Manager on Windows, the Secret Service on
// Linux. It is not covered by tests — it talks to an external system.
type SystemKeyring struct{}

func (SystemKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (SystemKeyring) Set(service, user, pass string) error {
	return keyring.Set(service, user, pass)
}

func (SystemKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

var _ Keyring = SystemKeyring{}

// KeyringSource reads the password from the system store.
//
// Any failure of the store ends the source quietly with ok == false so that
// the chain can go on: this is the "Linux without a graphical session" branch.
// Warn is called for real failures, but not for a missing entry.
type KeyringSource struct {
	Ring    Keyring
	Service string
	User    string
	Warn    func(string)
}

func (k KeyringSource) Password(ctx context.Context) (Secret, bool, error) {
	if k.Ring == nil {
		return Secret{}, false, nil
	}
	v, err := k.Ring.Get(k.Service, k.User)
	if err != nil {
		if !errors.Is(err, ErrNotFound) && k.Warn != nil {
			k.Warn("system keyring unavailable (" + err.Error() +
				"); set SAU_PASSWORD or SAU_PASSWORD_FILE, or type the password")
		}
		return Secret{}, false, nil
	}
	if v == "" {
		return Secret{}, false, nil
	}
	return New(v), true, nil
}

// Save stores pass in the system keyring, for the --save-password flag.
//
// It takes a Secret, not a string, and unwraps it here. Together with
// site.EncodeLoginForm this is the second and last call of Reveal in the whole
// program, and it is deliberately inside this package: the plaintext never
// crosses a package boundary, so cli hands over a Secret and never sees the
// password itself.
func (k KeyringSource) Save(pass Secret) error {
	if k.Ring == nil {
		return errors.New("secrets: no system keyring available")
	}
	if pass.IsZero() {
		return errEmptyPassword
	}
	return k.Ring.Set(k.Service, k.User, pass.Reveal())
}
