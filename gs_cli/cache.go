package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type CacheStats struct {
	ObjectCount int
	TotalBytes  int64
}

// CacheManager coordinates the client-side global cache for slice objects.
// Cached blobs are stored under ~/.gitslice/cache/objects/<hash>.
type CacheManager struct {
	root  string
	mu    sync.RWMutex
	index map[string]int64
}

type cacheIndexSnapshot struct {
	Objects map[string]int64 `json:"objects"`
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

	cache := &CacheManager{
		root:  root,
		index: make(map[string]int64),
	}
	if err := cache.loadOrRebuildIndex(); err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *CacheManager) objectPath(hash string) string {
	return filepath.Join(c.root, "objects", hash)
}

func (c *CacheManager) objectsDir() string {
	return filepath.Join(c.root, "objects")
}

func (c *CacheManager) indexPath() string {
	return filepath.Join(c.root, "index.json")
}

func (c *CacheManager) Root() string {
	return c.root
}

func (c *CacheManager) loadOrRebuildIndex() error {
	raw, err := os.ReadFile(c.indexPath())
	if err == nil {
		var snapshot cacheIndexSnapshot
		if jsonErr := json.Unmarshal(raw, &snapshot); jsonErr == nil && snapshot.Objects != nil {
			c.index = snapshot.Objects
			return nil
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return c.rebuildIndexFromDisk()
}

func (c *CacheManager) rebuildIndexFromDisk() error {
	entries, err := os.ReadDir(c.objectsDir())
	if err != nil {
		return err
	}

	index := make(map[string]int64, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		index[name] = info.Size()
	}

	c.mu.Lock()
	c.index = index
	c.mu.Unlock()
	return c.PersistIndex()
}

func (c *CacheManager) PersistIndex() error {
	c.mu.RLock()
	snapshot := cacheIndexSnapshot{
		Objects: make(map[string]int64, len(c.index)),
	}
	for hash, size := range c.index {
		snapshot.Objects[hash] = size
	}
	c.mu.RUnlock()

	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(c.root, "cache-index-*.json")
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

	if _, err := tmpFile.Write(raw); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, c.indexPath()); err != nil {
		return err
	}
	cleanupTmp = false
	return nil
}

// HasObject returns true if the cache already contains the blob for the given hash.
func (c *CacheManager) HasObject(hash string) (bool, error) {
	if hash == "" {
		return false, errors.New("missing hash for cache lookup")
	}

	c.mu.RLock()
	_, ok := c.index[hash]
	c.mu.RUnlock()
	return ok, nil
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

	if err := os.WriteFile(c.objectPath(hash), data, 0o644); err != nil {
		return err
	}
	c.mu.Lock()
	c.index[hash] = int64(len(data))
	c.mu.Unlock()
	return nil
}

func (c *CacheManager) DropObjects(hashes []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, rawHash := range hashes {
		hash := rawHash
		if hash == "" {
			continue
		}
		delete(c.index, hash)
		if err := os.Remove(c.objectPath(hash)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// ListObjectHashes returns all cached object hashes from the persisted cache index.
func (c *CacheManager) ListObjectHashes() ([]string, error) {
	c.mu.RLock()
	hashes := make([]string, 0, len(c.index))
	for hash := range c.index {
		hashes = append(hashes, hash)
	}
	c.mu.RUnlock()
	sort.Strings(hashes)
	return hashes, nil
}

func (c *CacheManager) Stats() (CacheStats, error) {
	stats := CacheStats{}
	c.mu.RLock()
	for _, size := range c.index {
		stats.ObjectCount++
		stats.TotalBytes += size
	}
	c.mu.RUnlock()
	return stats, nil
}

func (c *CacheManager) ClearObjects() error {
	if err := os.RemoveAll(c.objectsDir()); err != nil {
		return err
	}
	if err := os.MkdirAll(c.objectsDir(), 0o755); err != nil {
		return err
	}
	c.mu.Lock()
	c.index = make(map[string]int64)
	c.mu.Unlock()
	return c.PersistIndex()
}
