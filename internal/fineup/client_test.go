package fineup

import (
	"context"
	"errors"
	"testing"
)

// The uploader must never fall back to http.DefaultClient: every request has
// to go through the redacting transport the caller wires in.
func TestNewPanicsOnNilClient(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New(nil) did not panic")
		}
	}()
	New(nil)
}

func TestUploadRefusesZeroClient(t *testing.T) {
	u := &Uploader{}
	_, err := u.Upload(context.Background(), Spec{Path: "x", Base: "http://h/upload.php", Pool: []string{"http://h"}}, nil)
	if !errors.Is(err, ErrNoClient) {
		t.Fatalf("Upload without a client: err = %v, want ErrNoClient", err)
	}
	if err := u.Delete(context.Background(), "http://h/upload.php", "u"); !errors.Is(err, ErrNoClient) {
		t.Fatalf("Delete without a client: err = %v, want ErrNoClient", err)
	}
}
