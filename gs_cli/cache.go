package main

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
)

// CacheManager coordinates the client-side global cache for slice objects.
// Cached blobs are stored under ~/.gitslice/cache/objects/<hash>.
type CacheManager struct {
	root string
}

// NewCacheManager constructs a cache manager rooted at the default cache location.
func NewCacheManager() (*CacheManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	return newCacheManagerWithRoot(filepath.Join(home, ".gitslice", "cache"))
}

func newCacheManagerWithRoot(root string) (*CacheManager, error) {
	objectsDir := filepath.Join(root, "objects")
	if err := os.MkdirAll(objectsDir, 0o755); err != nil {
		return nil, err
	}

	return &CacheManager{root: root}, nil
}

func (c *CacheManager) objectPath(hash string) string {
	return filepath.Join(c.root, "objects", hash)
}

func (c *CacheManager) objectsDir() string {
	return filepath.Join(c.root, "objects")
}

// HasObject returns true if the cache already contains the blob for the given hash.
func (c *CacheManager) HasObject(hash string) (bool, error) {
	if hash == "" {
		return false, errors.New("missing hash for cache lookup")
	}

	_, err := os.Stat(c.objectPath(hash))
	if err == nil {
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	return false, err
}

// ReadObject loads a cached blob by hash.
func (c *CacheManager) ReadObject(hash string) ([]byte, error) {
	if hash == "" {
		return nil, errors.New("missing hash for cache read")
	}

	return os.ReadFile(c.objectPath(hash))
}

// StoreObject writes a blob to the cache under its hash.
func (c *CacheManager) StoreObject(hash string, data []byte) error {
	if hash == "" {
		return errors.New("missing hash for cache write")
	}

	return os.WriteFile(c.objectPath(hash), data, 0o644)
}

// ListObjectHashes returns all cached object hashes currently stored on disk.
func (c *CacheManager) ListObjectHashes() ([]string, error) {
	entries, err := os.ReadDir(c.objectsDir())
	if err != nil {
		return nil, err
	}

	hashes := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "" {
			continue
		}
		hashes = append(hashes, name)
	}
	sort.Strings(hashes)
	return hashes, nil
}
