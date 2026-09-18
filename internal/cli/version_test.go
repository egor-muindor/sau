package cli

import (
	"strings"
	"testing"
)

// The version is stamped at build time; a plain build reports "dev".
func TestVersionCommand(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-V"}} {
		h := newHarness(t)
		if code := h.run(args...); code != ExitOK {
			t.Fatalf("%v: exit code = %d, stderr = %q", args, code, h.errOut.String())
		}
		if got := strings.TrimSpace(h.out.String()); got != "sau dev" {
			t.Errorf("%v: stdout = %q, want %q", args, got, "sau dev")
		}
	}
}

func TestVersionUsesStampedValue(t *testing.T) {
	old := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = old })
	h := newHarness(t)
	h.run("version")
	if got := strings.TrimSpace(h.out.String()); got != "sau 1.2.3" {
		t.Errorf("stdout = %q, want %q", got, "sau 1.2.3")
	}
}
