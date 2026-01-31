package s3store

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/niczy/gitslice/internal/storage"
)

// Client captures the subset of the AWS SDK client used by the S3-backed object store.
type Client interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// ObjectStore stores file content in an S3-compatible bucket.
type ObjectStore struct {
	client Client
	bucket string
}

// New creates an object store backed by S3.
func New(client Client, bucket string) *ObjectStore {
	return &ObjectStore{client: client, bucket: bucket}
}

// PutObject uploads the payload to S3.
func (s *ObjectStore) PutObject(ctx context.Context, key string, body []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
		Body:   bytes.NewReader(body),
	})
	return err
}

// GetObject downloads an object from S3.
func (s *ObjectStore) GetObject(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err != nil {
		var notFound *types.NoSuchKey
		if errors.As(err, &notFound) {
			return nil, storage.ErrEntryNotFound
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
func (s *ObjectStore) DeleteObject(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	var notFound *types.NoSuchKey
	if errors.As(err, &notFound) {
		return storage.ErrEntryNotFound
	}
	return err
}

var _ storage.ObjectStore = (*ObjectStore)(nil)
