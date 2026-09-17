package fineup

import "testing"

func TestEndpoint(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no trailing slash",
			in:   "https://t-time28.melon-soda.org",
			want: "https://t-time28.melon-soda.org/upload.php",
		},
		{
			name: "trailing slash, as the russian channel serves it",
			in:   "https://ru-time28.anime-on.ru/",
			want: "https://ru-time28.anime-on.ru/upload.php",
		},
		{
			name: "several trailing slashes",
			in:   "https://time28.anime-on.ru///",
			want: "https://time28.anime-on.ru/upload.php",
		},
		{
			name: "path prefix is kept",
			in:   "https://example.org/files/",
			want: "https://example.org/files/upload.php",
		},
		{
			name: "empty string",
			in:   "",
			want: "/upload.php",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Endpoint(tt.in); got != tt.want {
				t.Fatalf("Endpoint(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEndpointIsIdempotentAcrossAChannelSwitch(t *testing.T) {
	// The same physical server is offered with and without a trailing slash
	// depending on the delivery channel. Both spellings must produce the same
	// request URL, otherwise a resume would hit a different path.
	withSlash := Endpoint("https://ru-time28.anime-on.ru/")
	without := Endpoint("https://ru-time28.anime-on.ru")
	if withSlash != without {
		t.Fatalf("Endpoint disagrees on the trailing slash: %q vs %q", withSlash, without)
	}
}
