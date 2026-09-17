package statefile_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sau/internal/statefile"
)

type payload struct {
	UUID string `json:"uuid"`
	Done []int  `json:"done"`
}

func TestKeyIsSixteenHexChars(t *testing.T) {
	key, err := statefile.Key("/tmp/anime/ep01.mp4")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if len(key) != 16 {
		t.Fatalf("len(Key) = %d, want 16 (key = %q)", len(key), key)
	}
	for _, r := range key {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("Key = %q, want lowercase hex only", key)
		}
	}
}

func TestKeyIsStableAcrossSpellings(t *testing.T) {
	a, err := statefile.Key("/tmp/anime/ep01.mp4")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	b, err := statefile.Key("/tmp/anime/./sub/../ep01.mp4")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if a != b {
		t.Errorf("Key not stable: %q != %q", a, b)
	}
}

func TestKeyDiffersForDifferentFiles(t *testing.T) {
	a, err := statefile.Key("/tmp/anime/ep01.mp4")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	b, err := statefile.Key("/tmp/anime/ep02.mp4")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if a == b {
		t.Errorf("Key collision for different files: %q", a)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := statefile.Store{Dir: filepath.Join(t.TempDir(), "state")}
	want := payload{UUID: "11111111-2222-3333-4444-555555555555", Done: []int{0, 1, 2}}

	if err := s.Save("abcdef0123456789", want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var got payload
	if err := s.Load("abcdef0123456789", &got); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestSaveWritesIndentedJSONPrivately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s := statefile.Store{Dir: dir}
	if err := s.Save("abcdef0123456789", payload{UUID: "u", Done: []int{1}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "abcdef0123456789.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "\n  \"uuid\"") {
		t.Errorf("state file is not indented JSON:\n%s", data)
	}

	fi, err := os.Stat(filepath.Join(dir, "abcdef0123456789.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtimeAllowsModeCheck() && fi.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %#o, want 0600", fi.Mode().Perm())
	}
}

func TestLoadMissingReturnsErrNotFound(t *testing.T) {
	s := statefile.Store{Dir: t.TempDir()}
	var got payload
	err := s.Load("0000000000000000", &got)
	if !errors.Is(err, statefile.ErrNotFound) {
		t.Errorf("Load(missing) = %v, want ErrNotFound", err)
	}
}

func TestDeleteMissingIsNotAnError(t *testing.T) {
	s := statefile.Store{Dir: t.TempDir()}
	if err := s.Delete("0000000000000000"); err != nil {
		t.Errorf("Delete(missing) = %v, want nil", err)
	}
}

func TestDeleteRemovesState(t *testing.T) {
	s := statefile.Store{Dir: filepath.Join(t.TempDir(), "state")}
	if err := s.Save("abcdef0123456789", payload{UUID: "u"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Delete("abcdef0123456789"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var got payload
	if err := s.Load("abcdef0123456789", &got); !errors.Is(err, statefile.ErrNotFound) {
		t.Errorf("Load after Delete = %v, want ErrNotFound", err)
	}
}

func TestKeysListsSavedStatesSorted(t *testing.T) {
	s := statefile.Store{Dir: filepath.Join(t.TempDir(), "state")}
	for _, k := range []string{"bbbbbbbbbbbbbbbb", "aaaaaaaaaaaaaaaa"} {
		if err := s.Save(k, payload{UUID: k}); err != nil {
			t.Fatalf("Save(%s): %v", k, err)
		}
	}

	keys, err := s.Keys()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	want := []string{"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("Keys = %v, want %v", keys, want)
	}
}

func TestLoadWrapsDecodeErrorWithPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abcdef0123456789.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s := statefile.Store{Dir: dir}
	var got payload
	err := s.Load("abcdef0123456789", &got)
	if err == nil {
		t.Fatal("Load on a corrupt file = nil error, want an error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("Load error = %q, want it to mention %q", err.Error(), path)
	}
}

func TestKeysOnMissingDirIsEmpty(t *testing.T) {
	s := statefile.Store{Dir: filepath.Join(t.TempDir(), "nope")}
	keys, err := s.Keys()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("Keys = %v, want empty", keys)
	}
}
