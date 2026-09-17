package secrets_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"sau/internal/secrets"
)

// canary is the value that must never appear in any output.
const canary = "hunter2-canary"

const redacted = "[redacted]"

func TestSecretNeverPrintsItsValue(t *testing.T) {
	s := secrets.New(canary)

	type holder struct {
		P secrets.Secret
	}

	textBuf := &bytes.Buffer{}
	textLog := slog.New(slog.NewTextHandler(textBuf, nil))
	textLog.Info("login", slog.Any("p", s))

	jsonBuf := &bytes.Buffer{}
	jsonLog := slog.New(slog.NewJSONHandler(jsonBuf, nil))
	jsonLog.Info("login", slog.Any("p", s))

	jsonPlain, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal(Secret): %v", err)
	}
	jsonStruct, err := json.Marshal(holder{P: s})
	if err != nil {
		t.Fatalf("json.Marshal(struct): %v", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%v", map[string]secrets.Secret{"p": s})

	cases := []struct {
		name string
		got  string
	}{
		{"fmt.Sprint", fmt.Sprint(s)},
		{"verb v", fmt.Sprintf("%v", s)},
		{"verb s", fmt.Sprintf("%s", s)},
		{"verb q", fmt.Sprintf("%q", s)},
		{"verb plus v", fmt.Sprintf("%+v", s)},
		{"verb sharp v", fmt.Sprintf("%#v", s)},
		{"verb x", fmt.Sprintf("%x", s)},
		{"struct verb v", fmt.Sprintf("%v", holder{P: s})},
		{"struct verb plus v", fmt.Sprintf("%+v", holder{P: s})},
		{"struct verb sharp v", fmt.Sprintf("%#v", holder{P: s})},
		{"pointer verb plus v", fmt.Sprintf("%+v", &s)},
		{"json.Marshal", string(jsonPlain)},
		{"json.Marshal struct", string(jsonStruct)},
		{"slog text handler", textBuf.String()},
		{"slog json handler", jsonBuf.String()},
		{"map through strings.Builder", sb.String()},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if strings.Contains(c.got, canary) {
				t.Fatalf("output leaks the secret: %q", c.got)
			}
			if !strings.Contains(c.got, redacted) {
				t.Fatalf("output = %q, want it to contain %q", c.got, redacted)
			}
		})
	}

	// fmt does not call Format for a field it cannot see: when a Secret sits
	// in an unexported field of another package's struct, fmt falls back to
	// reflecting the field's own value. These cases only require that the
	// canary never leaks that way; the fallback prints a pointer address, not
	// "[redacted]", which is why they are checked separately from cases above.
	unexported := []struct {
		name string
		got  string
	}{
		{"unexported field fmt.Sprint", fmt.Sprint(struct{ p secrets.Secret }{s})},
		{"unexported field verb plus v", fmt.Sprintf("%+v", struct{ p secrets.Secret }{s})},
		{"unexported field verb sharp v", fmt.Sprintf("%#v", struct{ p secrets.Secret }{s})},
		{"unexported field pointer verb plus v", fmt.Sprintf("%+v", &struct{ p secrets.Secret }{s})},
	}
	for _, c := range unexported {
		t.Run(c.name, func(t *testing.T) {
			if strings.Contains(c.got, canary) {
				t.Fatalf("output leaks the secret: %q", c.got)
			}
		})
	}
}

func TestSecretRevealReturnsTheValue(t *testing.T) {
	if got := secrets.New(canary).Reveal(); got != canary {
		t.Errorf("Reveal = %q, want %q", got, canary)
	}
}

func TestZeroSecretRevealReturnsEmptyString(t *testing.T) {
	if got := (secrets.Secret{}).Reveal(); got != "" {
		t.Errorf("Reveal of zero Secret = %q, want empty string", got)
	}
}

func TestSecretIsZero(t *testing.T) {
	if !(secrets.Secret{}).IsZero() {
		t.Error("zero Secret: IsZero = false, want true")
	}
	if !secrets.New("").IsZero() {
		t.Error("New(\"\"): IsZero = false, want true")
	}
	if secrets.New(canary).IsZero() {
		t.Error("New(canary): IsZero = true, want false")
	}
}

func TestSecretCopyRevealsTheSameValue(t *testing.T) {
	s := secrets.New(canary)
	cp := s
	if got := cp.Reveal(); got != canary {
		t.Errorf("copy: Reveal = %q, want %q", got, canary)
	}
}

func TestSecretMarshalText(t *testing.T) {
	got, err := secrets.New(canary).MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if string(got) != redacted {
		t.Errorf("MarshalText = %q, want %q", got, redacted)
	}
}

func TestSecretLogValue(t *testing.T) {
	got := secrets.New(canary).LogValue()
	if got.String() != redacted {
		t.Errorf("LogValue = %q, want %q", got.String(), redacted)
	}
}
