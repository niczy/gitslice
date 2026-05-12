package storage

import (
	"bytes"
	"context"
	"testing"
)

func TestObjectStoreContract(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		factory func(t *testing.T) ObjectStore
	}{
		{
			name: "in-memory",
			factory: func(t *testing.T) ObjectStore {
				t.Helper()
				return NewInMemoryObjectStore()
			},
		},
		{
			name: "filesystem",
			factory: func(t *testing.T) ObjectStore {
				t.Helper()
				store, err := NewFilesystemObjectStore(t.TempDir())
				if err != nil {
					t.Fatalf("NewFilesystemObjectStore failed: %v", err)
				}
				return store
			},
		},
		{
			name: "r2",
			factory: func(t *testing.T) ObjectStore {
				t.Helper()
				store, _ := newTestR2ObjectStore(t, "contract")
				return store
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runObjectStoreContract(ctx, t, tc.factory(t))
		})
	}
}

func runObjectStoreContract(ctx context.Context, t *testing.T, store ObjectStore) {
	t.Helper()

	key := "nested/object:key"
	exists, err := store.HasObject(ctx, key)
	if err != nil {
		t.Fatalf("HasObject missing failed: %v", err)
	}
	if exists {
		t.Fatalf("HasObject missing = true, want false")
	}
	if _, err := store.GetObject(ctx, key); err != ErrEntryNotFound {
		t.Fatalf("GetObject missing = %v, want %v", err, ErrEntryNotFound)
	}
	if err := store.DeleteObject(ctx, key); err != ErrEntryNotFound {
		t.Fatalf("DeleteObject missing = %v, want %v", err, ErrEntryNotFound)
	}

	body := []byte("hello world")
	if err := store.PutObject(ctx, key, body); err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}
	body[0] = 'x'

	exists, err = store.HasObject(ctx, key)
	if err != nil {
		t.Fatalf("HasObject present failed: %v", err)
	}
	if !exists {
		t.Fatalf("HasObject present = false, want true")
	}

	got, err := store.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if !bytes.Equal(got, []byte("hello world")) {
		t.Fatalf("GetObject payload = %q, want original body", string(got))
	}
	got[0] = 'y'
	gotAgain, err := store.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject second read failed: %v", err)
	}
	if !bytes.Equal(gotAgain, []byte("hello world")) {
		t.Fatalf("stored payload was mutated through read alias: %q", string(gotAgain))
	}

	replacement := []byte("replacement")
	if err := store.PutObject(ctx, key, replacement); err != nil {
		t.Fatalf("PutObject replacement failed: %v", err)
	}
	got, err = store.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject replacement failed: %v", err)
	}
	if !bytes.Equal(got, replacement) {
		t.Fatalf("replacement payload = %q, want %q", string(got), string(replacement))
	}

	if err := store.DeleteObject(ctx, key); err != nil {
		t.Fatalf("DeleteObject failed: %v", err)
	}
	exists, err = store.HasObject(ctx, key)
	if err != nil {
		t.Fatalf("HasObject after delete failed: %v", err)
	}
	if exists {
		t.Fatalf("HasObject after delete = true, want false")
	}
	if _, err := store.GetObject(ctx, key); err != ErrEntryNotFound {
		t.Fatalf("GetObject after delete = %v, want %v", err, ErrEntryNotFound)
	}
}
