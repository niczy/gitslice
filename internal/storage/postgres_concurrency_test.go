package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

func newPostgresStorageForIsolationTest(t *testing.T) *PostgresStorage {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}

	ctx := context.Background()
	st, err := NewPostgresStorage(ctx, dsn, NewInMemoryObjectStore(), fmt.Sprintf("test-pg-iso-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("NewPostgresStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestPostgresStorageFileContentReadWriteIsolation(t *testing.T) {
	ctx := context.Background()
	st := newPostgresStorageForIsolationTest(t)

	slice := &models.Slice{ID: "slice-1", Name: "S", Files: []string{"file-1"}, Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	content := &models.FileContent{FileID: "file-1", Path: "src/main.go", Content: []byte("hello"), Size: 5, Hash: "hash-1"}
	if err := st.AddFileContent(ctx, content); err != nil {
		t.Fatalf("AddFileContent failed: %v", err)
	}
	content.Content[0] = 'x'

	files, err := st.GetSliceFiles(ctx, "slice-1")
	if err != nil {
		t.Fatalf("GetSliceFiles failed: %v", err)
	}
	if len(files) != 1 || string(files[0].Content) != "hello" {
		t.Fatalf("expected stored content to remain hello, got %+v", files)
	}

	files[0].Content[0] = 'y'
	filesAgain, err := st.GetSliceFiles(ctx, "slice-1")
	if err != nil {
		t.Fatalf("GetSliceFiles second failed: %v", err)
	}
	if string(filesAgain[0].Content) != "hello" {
		t.Fatalf("read mutation should not alias stored content, got %q", string(filesAgain[0].Content))
	}
}

func TestPostgresStorageGetSliceFileByPathIsolation(t *testing.T) {
	ctx := context.Background()
	st := newPostgresStorageForIsolationTest(t)

	slice := &models.Slice{ID: "slice-1", Name: "S", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	entry := &models.DirectoryEntry{ID: "entry-1", Path: "app/main.go", Type: "file", ParentID: "slice-1", Content: []byte("abc"), Size: 3}
	if err := st.AddEntry(ctx, entry); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	file, err := st.GetSliceFileByPath(ctx, "slice-1", "app/main.go")
	if err != nil {
		t.Fatalf("GetSliceFileByPath failed: %v", err)
	}
	if string(file.Content) != "abc" {
		t.Fatalf("unexpected content: %q", string(file.Content))
	}

	file.Content[0] = 'z'
	fileAgain, err := st.GetSliceFileByPath(ctx, "slice-1", "app/main.go")
	if err != nil {
		t.Fatalf("GetSliceFileByPath second failed: %v", err)
	}
	if string(fileAgain.Content) != "abc" {
		t.Fatalf("read mutation should not alias stored fallback content, got %q", string(fileAgain.Content))
	}
}
