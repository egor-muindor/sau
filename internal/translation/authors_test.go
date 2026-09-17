package translation

import "testing"

func TestNormalizeAuthors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"single name", "Team", "team"},
		{"already normal", "alice, bob", "alice, bob"},
		{"comma separated", "Alice, Bob", "alice, bob"},
		{"ampersand separated", "Alice & Bob", "alice, bob"},
		{"russian conjunction", "Алиса и Борис", "алиса, борис"},
		{"mixed separators", "Alice, Bob & Carol", "alice, bob, carol"},
		{"no spaces around comma", "Alice,Bob", "alice, bob"},
		{"no spaces around ampersand", "Alice&Bob", "alice, bob"},
		{"collapsed inner spaces", "Alice   Smith", "alice smith"},
		{"trimmed", "   Alice, Bob   ", "alice, bob"},
		{"empty parts dropped", "Alice, , Bob", "alice, bob"},
		{"trailing separator", "Alice, Bob,", "alice, bob"},
		{"empty string", "", ""},
		{"separators only", " , & ", ""},
		{"team with members comma", "Team (Alice, Bob)", "team (alice, bob)"},
		{"team with members ampersand", "Team (Alice & Bob)", "team (alice, bob)"},
		{"cyrillic team", "Команда (Участник1, Участник2)", "команда (участник1, участник2)"},
		{"tabs and newlines", "Alice\t&\nBob", "alice, bob"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeAuthors(tt.in); got != tt.want {
				t.Fatalf("NormalizeAuthors(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestAuthorsEqual(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{"identical", "Team", "Team", true},
		{"case differs", "Team", "TEAM", true},
		{
			"the site rewrote the comma as an ampersand",
			"Команда (Участник1, Участник2)",
			"Команда (Участник1 & Участник2)",
			true,
		},
		{"spacing differs", "Alice,Bob", "Alice,   Bob", true},
		{"conjunction versus ampersand", "Алиса и Борис", "Алиса & Борис", true},
		{"different teams", "Team A", "Team B", false},
		{"one member missing", "Team (Alice, Bob)", "Team (Alice)", false},
		{"both empty", "", "", true},
		{"one empty", "Team", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AuthorsEqual(tt.a, tt.b); got != tt.want {
				t.Fatalf("AuthorsEqual(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
