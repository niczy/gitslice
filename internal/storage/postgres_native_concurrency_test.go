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
