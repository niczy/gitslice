package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
)

func TestSliceVisibilityCLIGetAndSet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	if testStorage == nil {
		t.Fatalf("expected integration storage")
	}

	username := workflowUsername(t)
	sliceID := fmt.Sprintf("slice-vis-cli-%d", time.Now().UnixNano())
	filePath := "docs/guide.md"
	content := []byte("visibility guide")

	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      sliceID,
		Owners:    []string{username},
		CreatedBy: username,
		Files:     []string{filePath},
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(sliceID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: sliceID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := testStorage.AddFileToSlice(ctx, filePath, sliceID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, testStorage, sliceID, filePath, content)

	initial := runCLIJSONOrFail[sliceVisibilityJSON](t, "", "slice", "visibility", "get", sliceIDArg(sliceID))
	if initial.SliceID != sliceID {
		t.Fatalf("initial slice_id = %q, want %q", initial.SliceID, sliceID)
	}
	if initial.Visibility != "private" {
		t.Fatalf("initial visibility = %q, want %q", initial.Visibility, "private")
	}

	updated := runCLIJSONOrFail[sliceVisibilityJSON](t, "", "slice", "visibility", "set", sliceIDArg(sliceID), "public")
	if updated.Visibility != "public" {
		t.Fatalf("updated visibility = %q, want %q", updated.Visibility, "public")
	}
	stored, err := testStorage.GetSlice(ctx, sliceID)
	if err != nil {
		t.Fatalf("GetSlice failed: %v", err)
	}
	if stored.Visibility != models.VisibilityPublic {
		t.Fatalf("stored visibility = %q, want %q", stored.Visibility, models.VisibilityPublic)
	}
}

func TestFilesystemVisibilityCLIGetUsesContainingSlice(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	if testStorage == nil {
		t.Fatalf("expected integration storage")
	}

	username := workflowUsername(t)
	workspaceID := homeslice.IDForUsername(username)
	dirPath := fmt.Sprintf("/%s/visibility-cli-%d", username, time.Now().UnixNano())
	filePath := dirPath + "/note.txt"

	client := newFilesystemClient(t)
	if _, err := client.MakeDirectory(ctx, &filesystemv1.MakeDirectoryRequest{
		WorkspaceId: workspaceID,
		Path:        dirPath,
	}); err != nil {
		t.Fatalf("MakeDirectory failed: %v", err)
	}
	if _, err := client.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: workspaceID,
		Path:        filePath,
		Content:     []byte("visible note"),
	}); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	initial := runCLIJSONOrFail[pathSliceVisibilityJSON](t, "", "fs", "visibility", "get", filePath)
	if initial.SliceID != workspaceID {
		t.Fatalf("slice_id = %q, want %q", initial.SliceID, workspaceID)
	}
	if initial.Visibility != "private" {
		t.Fatalf("initial visibility = %q, want %q", initial.Visibility, "private")
	}

	updated := runCLIJSONOrFail[sliceVisibilityJSON](t, "", "slice", "visibility", "set", workspaceID, "public")
	if updated.Visibility != "public" {
		t.Fatalf("updated visibility = %q, want %q", updated.Visibility, "public")
	}

	child := runCLIJSONOrFail[pathSliceVisibilityJSON](t, "", "fs", "visibility", "get", filePath)
	if child.Visibility != "public" {
		t.Fatalf("child visibility = %q, want %q", child.Visibility, "public")
	}
	if child.SliceID != workspaceID {
		t.Fatalf("child slice_id = %q, want %q", child.SliceID, workspaceID)
	}

	runCLIJSONErrorOrFail[map[string]any](t, "", "fs", "visibility", "set", dirPath, "private", "--recursive")
}
