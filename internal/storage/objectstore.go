package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	gcsstorage "cloud.google.com/go/storage"
)

// ObjectStore defines a minimal API used by storage implementations to persist file content.
// The interface is intentionally tiny to support lightweight in-memory fakes in tests while
// enabling a GCS-backed implementation for production.
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

// GCSObjectStore stores file content in a Google Cloud Storage bucket.
type GCSObjectStore struct {
	client *gcsstorage.Client
	bucket string
}

// NewGCSObjectStore creates an object store backed by GCS.
func NewGCSObjectStore(client *gcsstorage.Client, bucket string) *GCSObjectStore {
	return &GCSObjectStore{client: client, bucket: bucket}
}

// PutObject uploads the payload to GCS.
func (s *GCSObjectStore) PutObject(ctx context.Context, key string, body []byte) error {
	w := s.client.Bucket(s.bucket).Object(key).NewWriter(ctx)
	if _, err := w.Write(body); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

// GetObject downloads an object from GCS.
func (s *GCSObjectStore) GetObject(ctx context.Context, key string) ([]byte, error) {
	r, err := s.client.Bucket(s.bucket).Object(key).NewReader(ctx)
	if err != nil {
		if errors.Is(err, gcsstorage.ErrObjectNotExist) {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	defer r.Close()

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// DeleteObject removes an object from GCS.
func (s *GCSObjectStore) DeleteObject(ctx context.Context, key string) error {
	err := s.client.Bucket(s.bucket).Object(key).Delete(ctx)
	if errors.Is(err, gcsstorage.ErrObjectNotExist) {
		return ErrEntryNotFound
	}
	return err
}

// FilesystemObjectStore stores object payloads under a local directory.
type FilesystemObjectStore struct {
	baseDir string
}

// NewFilesystemObjectStore creates an object store backed by the local filesystem.
func NewFilesystemObjectStore(baseDir string) *FilesystemObjectStore {
	return &FilesystemObjectStore{baseDir: baseDir}
}

func (s *FilesystemObjectStore) objectPath(key string) (string, error) {
	if key == "" {
		return "", errors.New("object key is empty")
	}
	cleanKey := filepath.Clean(key)
	if cleanKey == "." || strings.HasPrefix(cleanKey, "..") {
		return "", errors.New("invalid object key")
	}
	fullPath := filepath.Join(s.baseDir, cleanKey)
	base := filepath.Clean(s.baseDir) + string(os.PathSeparator)
	if !strings.HasPrefix(fullPath, base) {
		return "", errors.New("object path escapes base directory")
	}
	return fullPath, nil
}

// PutObject writes the payload to disk.
func (s *FilesystemObjectStore) PutObject(ctx context.Context, key string, body []byte) error {
	_ = ctx
	fullPath, err := s.objectPath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, body, 0644)
}

// GetObject reads a payload from disk.
func (s *FilesystemObjectStore) GetObject(ctx context.Context, key string) ([]byte, error) {
	_ = ctx
	fullPath, err := s.objectPath(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return data, nil
}

// DeleteObject removes a payload from disk.
func (s *FilesystemObjectStore) DeleteObject(ctx context.Context, key string) error {
	_ = ctx
	fullPath, err := s.objectPath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(fullPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrEntryNotFound
		}
		return err
	}
	return nil
}
