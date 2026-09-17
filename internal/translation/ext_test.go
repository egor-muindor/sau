package translation

import "testing"

func TestHasAllowedExt(t *testing.T) {
	video := []string{"mp4", "mkv", "webm", "avi", "mov"}
	subs := []string{"ass", "ssa", "srt", "vtt", "webvtt"}

	tests := []struct {
		name    string
		path    string
		allowed []string
		want    bool
	}{
		{"plain match", "episode.mp4", video, true},
		{"match in a directory", "/home/user/ongoing/episode.mkv", video, true},
		{"upper case extension", "EPISODE.MP4", video, true},
		{"mixed case extension", "episode.MkV", video, true},
		{"upper case in the allowed list", "episode.mp4", []string{"MP4"}, true},
		{"dot in the allowed list", "episode.mp4", []string{".mp4"}, true},
		{"subtitles", "episode.ass", subs, true},
		{"long extension", "episode.webvtt", subs, true},
		{"wrong extension", "episode.txt", video, false},
		{"subtitle extension against video list", "episode.ass", video, false},
		{"no extension", "episode", video, false},
		{"trailing dot", "episode.", video, false},
		{"dotfile without extension", "/home/user/.hidden", video, false},
		{"empty allowed list", "episode.mp4", nil, false},
		{"empty path", "", video, false},
		{"extension is a substring", "episode.mp42", video, false},
		{"several dots", "[Team] Title - 01 [1080p].v2.mp4", video, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasAllowedExt(tt.path, tt.allowed); got != tt.want {
				t.Fatalf("HasAllowedExt(%q, %v) = %v, want %v", tt.path, tt.allowed, got, tt.want)
			}
		})
	}
}
