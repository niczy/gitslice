package storage

import (
	"context"
	"sync"
)

// ObjectStore defines a minimal API used by storage implementations to persist file content.
// The interface is intentionally tiny to support lightweight in-memory fakes in tests while
// enabling an S3-backed implementation for production.
type ObjectStore interface {
	PutObject(ctx context.Context, key string, body []byte) error
	GetObject(ctx context.Context, key string) ([]byte, error)
	DeleteObject(ctx context.Context, key string) error
}

// InMemoryObjectStore is a test-friendly object store that keeps content in process memory.
// It is safe for concurrent use.
type InMemoryObjectStore struct {
	mu    sync.RWMutex
	store map[string][]byte
}

// NewInMemoryObjectStore constructs an in-memory object store.
func NewInMemoryObjectStore() *InMemoryObjectStore {
	return &InMemoryObjectStore{store: make(map[string][]byte)}
}

// PutObject saves the provided payload.
func (s *InMemoryObjectStore) PutObject(ctx context.Context, key string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = ctx
	data := make([]byte, len(body))
	copy(data, body)
	s.store[key] = data
	return nil
}

// GetObject retrieves the stored payload.
func (s *InMemoryObjectStore) GetObject(ctx context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_ = ctx
	data, ok := s.store[key]
	if !ok {
		return nil, ErrEntryNotFound
	}

	copyData := make([]byte, len(data))
	copy(copyData, data)
	return copyData, nil
}

// DeleteObject removes the stored payload.
func (s *InMemoryObjectStore) DeleteObject(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = ctx
	delete(s.store, key)
	return nil
}
