package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheManagerStoresAndReadsObjects(t *testing.T) {
	tmpDir := t.TempDir()

	cache, err := newCacheManagerWithRoot(tmpDir)
	if err != nil {
		t.Fatalf("failed to initialize cache: %v", err)
	}

	hash := "abc123"
	data := []byte("hello, cache")

	if err := cache.StoreObject(hash, data); err != nil {
		t.Fatalf("failed to store object: %v", err)
	}

	exists, err := cache.HasObject(hash)
	if err != nil {
		t.Fatalf("unexpected error from HasObject: %v", err)
	}
	if !exists {
		t.Fatalf("expected cached object for %s", hash)
	}

	loaded, err := cache.ReadObject(hash)
	if err != nil {
		t.Fatalf("failed to read object: %v", err)
	}

	if string(loaded) != string(data) {
		t.Fatalf("cached data mismatch: got %q want %q", string(loaded), string(data))
	}

	// Ensure objects are written to the expected location
	expectedPath := filepath.Join(tmpDir, "objects", hash)
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected cached file at %s: %v", expectedPath, err)
	}
}

func TestCacheManagerListObjectHashes(t *testing.T) {
	tmpDir := t.TempDir()

	cache, err := newCacheManagerWithRoot(tmpDir)
	if err != nil {
		t.Fatalf("failed to initialize cache: %v", err)
	}

	if err := cache.StoreObject("def456", []byte("second")); err != nil {
		t.Fatalf("failed to store second object: %v", err)
	}
	if err := cache.StoreObject("abc123", []byte("first")); err != nil {
		t.Fatalf("failed to store first object: %v", err)
	}

	hashes, err := cache.ListObjectHashes()
	if err != nil {
		t.Fatalf("ListObjectHashes failed: %v", err)
	}

	if got, want := len(hashes), 2; got != want {
		t.Fatalf("expected %d hashes, got %d", want, got)
	}
	if hashes[0] != "abc123" || hashes[1] != "def456" {
		t.Fatalf("unexpected hash ordering: %#v", hashes)
	}
}

func TestCacheManagerStatsAndClearObjects(t *testing.T) {
	tmpDir := t.TempDir()

	cache, err := newCacheManagerWithRoot(tmpDir)
	if err != nil {
		t.Fatalf("failed to initialize cache: %v", err)
	}

	if err := cache.StoreObject("abc123", []byte("first")); err != nil {
		t.Fatalf("failed to store first object: %v", err)
	}
	if err := cache.StoreObject("def456", []byte("second object")); err != nil {
		t.Fatalf("failed to store second object: %v", err)
	}

	stats, err := cache.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.ObjectCount != 2 {
		t.Fatalf("expected 2 objects, got %d", stats.ObjectCount)
	}
	if stats.TotalBytes <= 0 {
		t.Fatalf("expected cache bytes > 0, got %d", stats.TotalBytes)
	}

	if err := cache.ClearObjects(); err != nil {
		t.Fatalf("ClearObjects failed: %v", err)
	}

	stats, err = cache.Stats()
	if err != nil {
		t.Fatalf("Stats after clear failed: %v", err)
	}
	if stats.ObjectCount != 0 || stats.TotalBytes != 0 {
		t.Fatalf("expected empty cache after clear, got %+v", stats)
	}
}
