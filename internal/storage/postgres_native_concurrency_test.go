package storage

import (
	"context"
	"fmt"
	"os"
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

	files, err := ListSliceFileContents(ctx, st, "slice-1")
	if err != nil {
		t.Fatalf("ListSliceFileContents failed: %v", err)
	}
	if len(files) != 1 || string(files[0].Content) != "hello" {
		t.Fatalf("expected stored content to remain hello, got %+v", files)
	}
	if files[0].Hash != manifest.Hash {
		t.Fatalf("expected stored hash %q, got %q", manifest.Hash, files[0].Hash)
	}

	files[0].Content[0] = 'y'
	filesAgain, err := ListSliceFileContents(ctx, st, "slice-1")
	if err != nil {
		t.Fatalf("ListSliceFileContents second failed: %v", err)
	}
	if string(filesAgain[0].Content) != "hello" {
		t.Fatalf("read mutation should not alias stored content, got %q", string(filesAgain[0].Content))
	}
}
