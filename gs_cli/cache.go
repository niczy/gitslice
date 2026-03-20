package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type CacheStats struct {
	ObjectCount int
	TotalBytes  int64
}

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

func (c *CacheManager) Root() string {
	return c.root
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

// CopyObjectToFile streams a cached blob into the target path without loading the
// full object into userspace memory.
func (c *CacheManager) CopyObjectToFile(hash, targetPath string, mode os.FileMode) error {
	if hash == "" {
		return errors.New("missing hash for cache copy")
	}

	source, err := os.Open(c.objectPath(hash))
	if err != nil {
		return err
	}
	defer source.Close()

	tmpFile, err := os.CreateTemp(filepath.Dir(targetPath), ".gitslice-cache-copy-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		_ = tmpFile.Close()
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmpFile, source); err != nil {
		return err
	}
	if err := tmpFile.Chmod(mode); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return err
	}
	cleanupTmp = false
	return nil
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

func (c *CacheManager) Stats() (CacheStats, error) {
	entries, err := os.ReadDir(c.objectsDir())
	if err != nil {
		return CacheStats{}, err
	}

	stats := CacheStats{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return CacheStats{}, err
		}
		stats.ObjectCount++
		stats.TotalBytes += info.Size()
	}
	return stats, nil
}

func (c *CacheManager) ClearObjects() error {
	if err := os.RemoveAll(c.objectsDir()); err != nil {
		return err
	}
	return os.MkdirAll(c.objectsDir(), 0o755)
}
