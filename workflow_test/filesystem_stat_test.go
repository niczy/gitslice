package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/homeslice"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
)

func TestFilesystemStatJSONIncludesDirectoryRecursiveSize(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	username := workflowUsername(t)
	workspaceID := homeslice.IDForUsername(username)
	client := newFilesystemClient(t)

	dirPath := fmt.Sprintf("/%s/fs-stat-%d/docs", username, time.Now().UnixNano())
	if _, err := client.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: workspaceID,
		Path:        dirPath + "/readme.md",
		Content:     []byte("hello"),
	}); err != nil {
		t.Fatalf("WriteFile(readme) failed: %v", err)
	}
	if _, err := client.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: workspaceID,
		Path:        dirPath + "/guides/setup.md",
		Content:     []byte("world!!"),
	}); err != nil {
		t.Fatalf("WriteFile(setup) failed: %v", err)
	}

	statResp := runCLIJSONOrFail[filesystemStatJSON](t, "", "fs", "stat", dirPath)
	if !statResp.Exists {
		t.Fatalf("expected %q to exist, got %+v", dirPath, statResp)
	}
	if statResp.Entry.Path != dirPath {
		t.Fatalf("entry.path = %q, want %q", statResp.Entry.Path, dirPath)
	}
	if statResp.Entry.Type != filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY.String() {
		t.Fatalf("entry.type = %q, want %q", statResp.Entry.Type, filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY.String())
	}
	if statResp.Entry.Size != 12 {
		t.Fatalf("entry.size = %d, want %d", statResp.Entry.Size, 12)
	}
}
