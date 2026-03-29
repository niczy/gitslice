package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeS3Server struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

func newFakeS3Server() *fakeS3Server {
	return &fakeS3Server{objects: make(map[string][]byte)}
}

func (s *fakeS3Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket, key, ok := s.parsePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	compoundKey := bucket + "/" + key

	switch r.Method {
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		s.objects[compoundKey] = append([]byte(nil), body...)
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		s.mu.RLock()
		body, ok := s.objects[compoundKey]
		s.mu.RUnlock()
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	case http.MethodHead:
		s.mu.RLock()
		_, ok := s.objects[compoundKey]
		s.mu.RUnlock()
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		s.mu.Lock()
		delete(s.objects, compoundKey)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *fakeS3Server) parsePath(rawPath string) (bucket string, key string, ok bool) {
	trimmed := strings.TrimPrefix(rawPath, "/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func newTestR2ObjectStore(t *testing.T, prefix string) (*R2ObjectStore, *fakeS3Server) {
	t.Helper()

	handler := newFakeS3Server()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	store, err := NewR2ObjectStore(R2ObjectStoreConfig{
		Bucket:          "test-bucket",
		Prefix:          prefix,
		Endpoint:        server.URL,
		Region:          "auto",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("NewR2ObjectStore failed: %v", err)
	}

	return store, handler
}

func TestR2ObjectStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, backing := newTestR2ObjectStore(t, "production")

	key := "some/key:with:chars"
	body := []byte("hello world")

	if err := store.PutObject(ctx, key, body); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	backing.mu.RLock()
	if _, ok := backing.objects["test-bucket/production/"+key]; !ok {
		backing.mu.RUnlock()
		t.Fatalf("expected prefixed object to be stored in backing server")
	}
	backing.mu.RUnlock()

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

func TestR2ObjectStoreMissingObjectSemantics(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestR2ObjectStore(t, "staging")

	if _, err := store.GetObject(ctx, "missing"); err != ErrEntryNotFound {
		t.Fatalf("expected GetObject missing to return ErrEntryNotFound, got %v", err)
	}
	if err := store.DeleteObject(ctx, "missing"); err != ErrEntryNotFound {
		t.Fatalf("expected DeleteObject missing to return ErrEntryNotFound, got %v", err)
	}
}

func TestR2ObjectStoreConcurrentReads(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestR2ObjectStore(t, "staging")

	key := "shared-key"
	payload := []byte("payload")
	if err := store.PutObject(ctx, key, payload); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	const readers = 32
	var wg sync.WaitGroup
	errCh := make(chan error, readers)

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := store.GetObject(ctx, key)
			if err != nil {
				errCh <- fmt.Errorf("reader %d: %w", i, err)
				return
			}
			if !bytes.Equal(got, payload) {
				errCh <- fmt.Errorf("reader %d: unexpected payload %q", i, string(got))
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}
