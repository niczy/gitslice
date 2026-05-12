package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// FilesystemObjectStore stores objects as files on disk.
//
// Keys are mapped to a stable sha256 hex to avoid path traversal and filesystem
// incompatibilities. This store does not support listing and is intended for
// local/prototype deployments.
type FilesystemObjectStore struct {
	root string
}

func NewFilesystemObjectStore(root string) (*FilesystemObjectStore, error) {
	if root == "" {
		return nil, fmt.Errorf("filesystem object store root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve object store root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create object store root: %w", err)
	}
	return &FilesystemObjectStore{root: abs}, nil
}

func (s *FilesystemObjectStore) objectPath(key string) string {
	sum := sha256.Sum256([]byte(key))
	hexSum := hex.EncodeToString(sum[:])
	// Small fan-out to avoid too many files in one directory.
	return filepath.Join(s.root, hexSum[:2], hexSum)
}

func (s *FilesystemObjectStore) PutObject(ctx context.Context, key string, body []byte) error {
	_ = ctx

	target := s.objectPath(key)
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Atomic write: write to a unique temp file then rename.
	tmpFile, err := os.CreateTemp(dir, filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := tmpFile.Name()
	if _, err := tmpFile.Write(body); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *FilesystemObjectStore) GetObject(ctx context.Context, key string) ([]byte, error) {
	_ = ctx

	target := s.objectPath(key)
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return data, nil
}

func (s *FilesystemObjectStore) HasObject(ctx context.Context, key string) (bool, error) {
	_ = ctx

	target := s.objectPath(key)
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *FilesystemObjectStore) DeleteObject(ctx context.Context, key string) error {
	_ = ctx

	target := s.objectPath(key)
	if err := os.Remove(target); err != nil {
		if os.IsNotExist(err) {
			return ErrEntryNotFound
		}
		return err
	}
	return nil
}
