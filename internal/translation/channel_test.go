package translation

import "testing"

func TestParseChannel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Channel
		ok   bool
	}{
		{"all", "all", ChannelAll, true},
		{"cdn", "cdn", ChannelCDN, true},
		{"ru", "ru", ChannelRU, true},
		{"upper case", "CDN", ChannelCDN, true},
		{"mixed case", "Ru", ChannelRU, true},
		{"surrounding spaces", "  all  ", ChannelAll, true},
		{"unknown", "eu", 0, false},
		{"empty", "", 0, false},
		{"numeric", "2", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseChannel(tt.in)
			if tt.ok && err != nil {
				t.Fatalf("ParseChannel(%q) returned error %v, want none", tt.in, err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("ParseChannel(%q) returned no error, want one", tt.in)
			}
			if got != tt.want {
				t.Fatalf("ParseChannel(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestChannelStringAndCookieValue(t *testing.T) {
	tests := []struct {
		name   string
		c      Channel
		str    string
		cookie string
	}{
		{"all", ChannelAll, "all", "0"},
		{"cdn", ChannelCDN, "cdn", "2"},
		{"ru", ChannelRU, "ru", "3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.String(); got != tt.str {
				t.Fatalf("Channel(%d).String() = %q, want %q", tt.c, got, tt.str)
			}
			if got := tt.c.CookieValue(); got != tt.cookie {
				t.Fatalf("Channel(%d).CookieValue() = %q, want %q", tt.c, got, tt.cookie)
			}
		})
	}
}

func TestChannelRoundTrip(t *testing.T) {
	for _, c := range []Channel{ChannelAll, ChannelCDN, ChannelRU} {
		got, err := ParseChannel(c.String())
		if err != nil {
			t.Fatalf("ParseChannel(%q) returned error %v, want none", c.String(), err)
		}
		if got != c {
			t.Fatalf("round trip of %d gave %d", c, got)
		}
	}
}

func TestDefaultChannelIsCDN(t *testing.T) {
	if DefaultChannel != ChannelCDN {
		t.Fatalf("DefaultChannel = %d, want ChannelCDN (%d)", DefaultChannel, ChannelCDN)
	}
	if DefaultChannel.CookieValue() != "2" {
		t.Fatalf("DefaultChannel.CookieValue() = %q, want \"2\"", DefaultChannel.CookieValue())
	}
}
