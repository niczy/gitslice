package storage

import (
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
