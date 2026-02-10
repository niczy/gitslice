package storage

import (
	"bytes"
	"context"
	"testing"
)

func TestFilesystemObjectStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := NewFilesystemObjectStore(dir)
	if err != nil {
		t.Fatalf("NewFilesystemObjectStore: %v", err)
	}

	key := "some/key:with:chars"
	body := []byte("hello world")

	if err := store.PutObject(ctx, key, body); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	got, err := store.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("unexpected body: got=%q want=%q", string(got), string(body))
	}

	if err := store.DeleteObject(ctx, key); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	_, err = store.GetObject(ctx, key)
	if err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound after delete, got: %v", err)
	}
}
