package publish

import (
	"errors"
	"strings"
	"testing"
	"time"

	"sau/internal/fineup"
	"sau/internal/site"
	"sau/internal/translation"
)

func TestDecideResume(t *testing.T) {
	mod := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	const size = int64(1024)

	form := func(serverID int) site.CreateForm {
		f := defaultForm(1)
		f.Video.ServerID = serverID
		f.Sub.ServerID = serverID
		return f
	}
	state := func(serverID int, ch translation.Channel, assembled bool) *UploadState {
		st := &UploadState{
			Size: size, ModTime: mod, Phase: PhaseUploading,
			ServerID: serverID, Channel: ch,
			Video: &FileUpload{UUID: "u", PartSize: fineup.DefaultPartSize, Done: []int{0}},
		}
		if assembled {
			st.Video.Result = &fineup.Result{UUID: "u", Parts: 2}
		}
		return st
	}

	tests := []struct {
		name string
		st   *UploadState
		f    site.CreateForm
		ch   translation.Channel
		size int64
		mod  time.Time
		want decision
	}{
		{"no state", nil, form(77), translation.ChannelCDN, size, mod, resumeNew},
		{"size changed", state(77, translation.ChannelCDN, false), form(77),
			translation.ChannelCDN, size + 1, mod, resumeRestartFileChanged},
		{"mtime changed", state(77, translation.ChannelCDN, false), form(77),
			translation.ChannelCDN, size, mod.Add(time.Second), resumeRestartFileChanged},
		{"same server and channel", state(77, translation.ChannelCDN, false), form(77),
			translation.ChannelCDN, size, mod, resumeContinue},
		{"server changed, not assembled", state(77, translation.ChannelCDN, false), form(88),
			translation.ChannelCDN, size, mod, resumeRestartServerChanged},
		{"server changed, assembled", state(77, translation.ChannelCDN, true), form(88),
			translation.ChannelCDN, size, mod, resumeReupload},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decideResume(tt.st, tt.f, tt.ch, tt.size, tt.mod)
			if err != nil {
				t.Fatalf("decideResume: %v", err)
			}
			eq(t, "decision", got, tt.want)
		})
	}
}

// The host list may change completely while the server stays the same: hosts
// are routes, the server id is the storage.
func TestDecideResumeIgnoresHostChange(t *testing.T) {
	mod := time.Now()
	st := &UploadState{Size: 1024, ModTime: mod, ServerID: 77, Channel: translation.ChannelCDN,
		Video: &FileUpload{UUID: "u", Done: []int{0, 1}}}
	f := defaultForm(1)
	f.Video.ServerURLs = []string{"https://brand-new-host.example"}

	got, err := decideResume(st, f, translation.ChannelCDN, 1024, mod)
	if err != nil {
		t.Fatalf("decideResume: %v", err)
	}
	eq(t, "decision", got, resumeContinue)
}

func TestDecideResumeChannelMismatch(t *testing.T) {
	mod := time.Now()
	st := &UploadState{Size: 1024, ModTime: mod, ServerID: 77, Channel: translation.ChannelRU,
		Video: &FileUpload{UUID: "u", Done: []int{0}}}

	_, err := decideResume(st, defaultForm(1), translation.ChannelCDN, 1024, mod)
	var cm *ChannelMismatchError
	if !errors.As(err, &cm) {
		t.Fatalf("got %v, want *ChannelMismatchError", err)
	}
	eq(t, "Saved", cm.Saved, translation.ChannelRU)
	eq(t, "Requested", cm.Requested, translation.ChannelCDN)
	msg := cm.Error()
	for _, want := range []string{"--channel ru", "--fresh"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q must contain %q", msg, want)
		}
	}
}
