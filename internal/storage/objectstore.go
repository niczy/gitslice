package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
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

// S3Client captures the subset of the AWS SDK client used by S3ObjectStore.
type S3Client interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// S3ObjectStore stores file content in an S3-compatible bucket.
type S3ObjectStore struct {
	client S3Client
	bucket string
}

// NewS3ObjectStore creates an object store backed by S3.
func NewS3ObjectStore(client S3Client, bucket string) *S3ObjectStore {
	return &S3ObjectStore{client: client, bucket: bucket}
}

// PutObject uploads the payload to S3.
func (s *S3ObjectStore) PutObject(ctx context.Context, key string, body []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
		Body:   bytes.NewReader(body),
	})
	return err
}

// GetObject downloads an object from S3.
func (s *S3ObjectStore) GetObject(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err != nil {
		var notFound *types.NoSuchKey
		if errors.As(err, &notFound) {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// DeleteObject removes an object from S3.
func (s *S3ObjectStore) DeleteObject(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	var notFound *types.NoSuchKey
	if errors.As(err, &notFound) {
		return ErrEntryNotFound
	}
	return err
}
