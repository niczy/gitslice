package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

func newPostgresNativeStorageForIsolationTest(t *testing.T) *PostgresNativeStorage {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}

	ctx := context.Background()
	st, err := NewPostgresNativeStorage(ctx, dsn, NewInMemoryObjectStore(), fmt.Sprintf("test-native-iso-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("NewPostgresNativeStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestPostgresNativeStorageFileContentReadWriteIsolation(t *testing.T) {
	ctx := context.Background()
	st := newPostgresNativeStorageForIsolationTest(t)

	slice := &models.Slice{ID: "slice-1", Name: "S", Files: []string{"file-1"}, Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       "slice-1:src/main.go",
		Path:     "src/main.go",
		Type:     "file",
		ParentID: "slice-1",
		Size:     5,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "src/main.go", "slice-1"); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	content := []byte("hello")
	manifest, err := WriteSliceFileManifest(ctx, st, "slice-1", "src/main.go", content)
	if err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	content[0] = 'x'

	file, err := ReadSliceFileContent(ctx, st, "slice-1", "src/main.go")
	if err != nil {
		t.Fatalf("ReadSliceFileContent failed: %v", err)
	}
	if string(file.Content) != "hello" {
		t.Fatalf("expected stored content to remain hello, got %+v", file)
	}
	if file.Hash != manifest.Hash {
		t.Fatalf("expected stored hash %q, got %q", manifest.Hash, file.Hash)
	}

	file.Content[0] = 'y'
	fileAgain, err := ReadSliceFileContent(ctx, st, "slice-1", "src/main.go")
	if err != nil {
		t.Fatalf("ReadSliceFileContent second failed: %v", err)
	}
	if string(fileAgain.Content) != "hello" {
		t.Fatalf("read mutation should not alias stored content, got %q", string(fileAgain.Content))
	}
}

func TestPostgresNativeStorageUsesVersionedManifestRefs(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}

	ctx := context.Background()
	objectStore := NewInMemoryObjectStore()
	st, err := NewPostgresNativeStorage(ctx, dsn, objectStore, fmt.Sprintf("test-native-manifest-ref-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("NewPostgresNativeStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}

	source := &models.Slice{ID: "source-slice", Name: "Source", Owners: []string{"alice"}, CreatedBy: "alice"}
	home := &models.Slice{ID: "home_alice", Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, source); err != nil {
		t.Fatalf("CreateSlice(source) failed: %v", err)
	}
	if err := st.CreateSlice(ctx, home); err != nil {
		t.Fatalf("CreateSlice(home) failed: %v", err)
	}

	filePath := "alice/app/main.go"
	content := []byte("package main\n\nfunc main() {}\n")
	manifest, err := WriteSliceFileManifest(ctx, st, source.ID, filePath, content)
	if err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}

	if _, err := objectStore.GetObject(ctx, st.objKey("manifests", source.ID, filePath)); err != ErrEntryNotFound {
		t.Fatalf("expected no source slice-local manifest object, got %v", err)
	}
	if got, err := st.GetFileManifest(ctx, source.ID, filePath); err != nil {
		t.Fatalf("GetFileManifest(source) failed: %v", err)
	} else if got.Hash != manifest.Hash || got.Path != filePath {
		t.Fatalf("source manifest = %#v, want hash=%q path=%q", got, manifest.Hash, filePath)
	}

	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           "alice",
		Path:             filePath,
		SourceSliceID:    source.ID,
		SourceCommitHash: "commit-1",
		ManifestHash:     manifest.Hash,
		ContentHash:      manifest.Hash,
		EntryType:        "file",
		UpdatedAt:        time.Now(),
	}}); err != nil {
		t.Fatalf("UpsertHomePathHeads failed: %v", err)
	}

	if _, err := objectStore.GetObject(ctx, st.objKey("manifests", "root", filePath)); err != ErrEntryNotFound {
		t.Fatalf("expected no root slice-local manifest object, got %v", err)
	}
	if _, err := objectStore.GetObject(ctx, st.objKey("manifests", home.ID, filePath)); err != ErrEntryNotFound {
		t.Fatalf("expected no home slice-local manifest object, got %v", err)
	}
	projted, err := ReadSliceFileContent(ctx, st, "root", filePath)
	if err != nil {
		t.Fatalf("ReadSliceFileContent(root) failed: %v", err)
	}
	if !bytes.Equal(projted.Content, content) {
		t.Fatalf("projected root content mismatch: got %q want %q", projted.Content, content)
	}
	if projted.Hash != manifest.Hash {
		t.Fatalf("projected root hash=%q want %q", projted.Hash, manifest.Hash)
	}
	homeFile, err := ReadSliceFileContent(ctx, st, home.ID, filePath)
	if err != nil {
		t.Fatalf("ReadSliceFileContent(home) failed: %v", err)
	}
	if !bytes.Equal(homeFile.Content, content) || homeFile.Hash != manifest.Hash {
		t.Fatalf("projected home content mismatch: hash=%q content=%q", homeFile.Hash, homeFile.Content)
	}
}

func TestPostgresNativeStorageStartupDoesNotRebuildDirectorySizes(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}

	ctx := context.Background()
	namespace := fmt.Sprintf("test-native-no-startup-rebuild-%d", time.Now().UnixNano())
	objectStore := NewInMemoryObjectStore()
	st, err := NewPostgresNativeStorage(ctx, dsn, objectStore, namespace)
	if err != nil {
		t.Fatalf("NewPostgresNativeStorage failed: %v", err)
	}

	slice := &models.Slice{ID: "slice-startup-rebuild", Name: "Startup rebuild", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       nativeEntryID(slice.ID, "docs"),
		Path:     "docs",
		Type:     "directory",
		ParentID: slice.ID,
	}); err != nil {
		t.Fatalf("AddEntry(docs) failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       nativeEntryID(slice.ID, "docs/readme.md"),
		Path:     "docs/readme.md",
		Type:     "file",
		ParentID: nativeEntryID(slice.ID, "docs"),
		Size:     7,
	}); err != nil {
		t.Fatalf("AddEntry(docs/readme.md) failed: %v", err)
	}
	if _, err := st.pool.Exec(ctx, `
		UPDATE directory_entries
		SET size = 999
		WHERE slice_id = $1 AND path = 'docs' AND type = 'directory'
	`, slice.ID); err != nil {
		t.Fatalf("corrupt directory size: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	st, err = NewPostgresNativeStorage(ctx, dsn, objectStore, namespace)
	if err != nil {
		t.Fatalf("NewPostgresNativeStorage reopen failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	docs, err := st.GetEntryByPath(ctx, slice.ID, "docs")
	if err != nil {
		t.Fatalf("GetEntryByPath(docs) failed: %v", err)
	}
	if docs.Size != 999 {
		t.Fatalf("startup should not rebuild directory sizes, got docs size %d", docs.Size)
	}
	if err := st.RebuildIndexes(ctx); err != nil {
		t.Fatalf("RebuildIndexes failed: %v", err)
	}
	docs, err = st.GetEntryByPath(ctx, slice.ID, "docs")
	if err != nil {
		t.Fatalf("GetEntryByPath(docs after rebuild) failed: %v", err)
	}
	if docs.Size != 7 {
		t.Fatalf("explicit rebuild should repair docs size to 7, got %d", docs.Size)
	}
}

func TestPostgresNativeStorageConcurrentFileLockOneFails(t *testing.T) {
	ctx := context.Background()
	st := newPostgresNativeStorageForIsolationTest(t)

	sliceA := &models.Slice{ID: "slice-lock-a", Name: "A", Owners: []string{"alice"}, CreatedBy: "alice"}
	sliceB := &models.Slice{ID: "slice-lock-b", Name: "B", Owners: []string{"bob"}, CreatedBy: "bob"}
	if err := st.CreateSlice(ctx, sliceA); err != nil {
		t.Fatalf("CreateSlice A failed: %v", err)
	}
	if err := st.CreateSlice(ctx, sliceB); err != nil {
		t.Fatalf("CreateSlice B failed: %v", err)
	}

	type lockAttempt struct {
		sliceID string
		fileIDs []string
		err     error
	}

	start := make(chan struct{})
	results := make(chan lockAttempt, 2)
	var wg sync.WaitGroup
	lock := func(sliceID string, fileIDs []string) {
		defer wg.Done()
		<-start
		err := st.LockSliceAndFiles(ctx, sliceID, fileIDs)
		results <- lockAttempt{sliceID: sliceID, fileIDs: fileIDs, err: err}
	}

	wg.Add(2)
	go lock(sliceA.ID, []string{"shared.txt"})
	go lock(sliceB.ID, []string{"shared.txt"})
	close(start)
	wg.Wait()
	close(results)

	var success *lockAttempt
	lockHeldCount := 0
	for result := range results {
		switch result.err {
		case nil:
			copyResult := result
			success = &copyResult
		case ErrLockHeld:
			lockHeldCount++
		default:
			t.Fatalf("unexpected lock result for %s: %v", result.sliceID, result.err)
		}
	}
	if success == nil {
		t.Fatal("expected one concurrent file lock attempt to succeed")
	}
	if lockHeldCount != 1 {
		t.Fatalf("expected exactly one ErrLockHeld result, got %d", lockHeldCount)
	}

	st.UnlockSliceAndFiles(ctx, success.sliceID, success.fileIDs)
}

func TestPostgresNativeStorageConcurrentSliceLockOneFails(t *testing.T) {
	ctx := context.Background()
	st := newPostgresNativeStorageForIsolationTest(t)

	slice := &models.Slice{ID: "slice-lock-single", Name: "Single", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	type lockAttempt struct {
		fileIDs []string
		err     error
	}

	start := make(chan struct{})
	results := make(chan lockAttempt, 2)
	var wg sync.WaitGroup
	lock := func(fileIDs []string) {
		defer wg.Done()
		<-start
		err := st.LockSliceAndFiles(ctx, slice.ID, fileIDs)
		results <- lockAttempt{fileIDs: fileIDs, err: err}
	}

	wg.Add(2)
	go lock([]string{"a.txt"})
	go lock([]string{"b.txt"})
	close(start)
	wg.Wait()
	close(results)

	var success *lockAttempt
	lockHeldCount := 0
	for result := range results {
		switch result.err {
		case nil:
			copyResult := result
			success = &copyResult
		case ErrLockHeld:
			lockHeldCount++
		default:
			t.Fatalf("unexpected same-slice lock result: %v", result.err)
		}
	}
	if success == nil {
		t.Fatal("expected one concurrent same-slice lock attempt to succeed")
	}
	if lockHeldCount != 1 {
		t.Fatalf("expected exactly one ErrLockHeld result for same-slice race, got %d", lockHeldCount)
	}

	st.UnlockSliceAndFiles(ctx, slice.ID, success.fileIDs)
}
