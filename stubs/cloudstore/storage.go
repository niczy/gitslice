// Package storage provides a Google Cloud Storage client.
// This is a build stub that provides just enough surface area
// to allow offline compilation without network access.
// The real GCS operations are provided by the actual package in production.
package storage

import (
	"context"
	"errors"
	"io"

	"google.golang.org/api/option"
)

// ErrObjectNotExist is returned when an object is not found.
var ErrObjectNotExist = errors.New("storage: object doesn't exist")

// Client is a client for interacting with Google Cloud Storage.
type Client struct{}

// NewClient creates a new Google Cloud Storage client.
func NewClient(ctx context.Context, opts ...option.ClientOption) (*Client, error) {
	return &Client{}, nil
}

// Close closes the client.
func (c *Client) Close() error { return nil }

// Bucket returns a BucketHandle for the given bucket name.
func (c *Client) Bucket(name string) *BucketHandle {
	return &BucketHandle{name: name}
}

// BucketHandle provides operations on a GCS bucket.
type BucketHandle struct {
	name string
}

// Object returns an ObjectHandle for the given object name.
func (b *BucketHandle) Object(name string) *ObjectHandle {
	return &ObjectHandle{bucket: b.name, name: name}
}

// ObjectHandle provides operations on a GCS object.
type ObjectHandle struct {
	bucket string
	name   string
}

// NewWriter returns a storage Writer for writing to the object.
func (o *ObjectHandle) NewWriter(ctx context.Context) *Writer {
	return &Writer{}
}

// NewReader creates a new Reader to read the contents of the object.
func (o *ObjectHandle) NewReader(ctx context.Context) (*Reader, error) {
	return nil, ErrObjectNotExist
}

// Delete deletes the single specified object.
func (o *ObjectHandle) Delete(ctx context.Context) error {
	return ErrObjectNotExist
}

// Writer writes data to GCS.
type Writer struct {
	buf []byte
}

// Write appends bytes to the writer.
func (w *Writer) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// Close closes the writer.
func (w *Writer) Close() error { return nil }

// Reader reads data from GCS.
type Reader struct{}

// Read reads bytes from the reader.
func (r *Reader) Read(p []byte) (int, error) { return 0, io.EOF }

// Close closes the reader.
func (r *Reader) Close() error { return nil }
