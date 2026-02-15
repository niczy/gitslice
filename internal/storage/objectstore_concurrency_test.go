package storage

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestFilesystemObjectStoreConcurrentWritesSameKey(t *testing.T) {
	ctx := context.Background()
	store, err := NewFilesystemObjectStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemObjectStore failed: %v", err)
	}

	key := "shared-key"
	payloadA := []byte("payload-a")
	payloadB := []byte("payload-b")

	const workers = 32
	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := payloadA
			if i%2 == 0 {
				payload = payloadB
			}
			if err := store.PutObject(ctx, key, payload); err != nil {
				errCh <- fmt.Errorf("worker %d put failed: %w", i, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	got, err := store.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if !bytes.Equal(got, payloadA) && !bytes.Equal(got, payloadB) {
		t.Fatalf("unexpected payload after concurrent writes: %q", string(got))
	}
}

func TestInMemoryObjectStoreConcurrentReadWriteIsolation(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryObjectStore()

	key := "concurrent-key"
	if err := store.PutObject(ctx, key, []byte("seed")); err != nil {
		t.Fatalf("PutObject seed failed: %v", err)
	}

	const workers = 64
	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := []byte(fmt.Sprintf("payload-%d", i))
			if err := store.PutObject(ctx, key, body); err != nil {
				errCh <- fmt.Errorf("put failed: %w", err)
				return
			}

			got, err := store.GetObject(ctx, key)
			if err != nil {
				errCh <- fmt.Errorf("get failed: %w", err)
				return
			}
			if len(got) == 0 {
				errCh <- fmt.Errorf("empty payload")
				return
			}

			got[0] = 'x'
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	final, err := store.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject final failed: %v", err)
	}
	if len(final) == 0 {
		t.Fatalf("final payload empty")
	}
	if final[0] == 'x' {
		t.Fatalf("stored payload was mutated through read alias")
	}
}
