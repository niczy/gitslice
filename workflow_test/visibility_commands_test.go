package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
)

func TestSliceVisibilityCLIGetAndSetWithPropagation(t *testing.T) {
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

	updated := runCLIJSONOrFail[sliceVisibilityJSON](t, "", "slice", "visibility", "set", sliceIDArg(sliceID), "public", "--propagate", "public")
	if updated.Visibility != "public" {
		t.Fatalf("updated visibility = %q, want %q", updated.Visibility, "public")
	}
	if updated.PathPropagationMode != "public" {
		t.Fatalf("path_propagation_mode = %q, want %q", updated.PathPropagationMode, "public")
	}

	dirRule, err := testStorage.GetPathVisibilityRule(ctx, "/docs")
	if err != nil {
		t.Fatalf("GetPathVisibilityRule(dir) failed: %v", err)
	}
	if dirRule.Visibility != models.VisibilityPublic {
		t.Fatalf("dir visibility = %q, want %q", dirRule.Visibility, models.VisibilityPublic)
	}

	fileRule, err := testStorage.GetPathVisibilityRule(ctx, "/docs/guide.md")
	if err != nil {
		t.Fatalf("GetPathVisibilityRule(file) failed: %v", err)
	}
	if fileRule.Visibility != models.VisibilityPublic {
		t.Fatalf("file visibility = %q, want %q", fileRule.Visibility, models.VisibilityPublic)
	}
}

func TestFilesystemVisibilityCLIGetAndSetRecursive(t *testing.T) {
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

	initial := runCLIJSONOrFail[pathVisibilityJSON](t, "", "fs", "visibility", "get", filePath)
	if initial.WorkspaceID != workspaceID {
		t.Fatalf("workspace_id = %q, want %q", initial.WorkspaceID, workspaceID)
	}
	if initial.Visibility != "private" {
		t.Fatalf("initial visibility = %q, want %q", initial.Visibility, "private")
	}
	if initial.ExplicitRule {
		t.Fatalf("initial explicit_rule = true, want false")
	}
	if initial.EffectiveVisibility != "private" {
		t.Fatalf("initial effective_visibility = %q, want %q", initial.EffectiveVisibility, "private")
	}

	updated := runCLIJSONOrFail[pathVisibilityJSON](t, "", "fs", "visibility", "set", dirPath, "public", "--recursive")
	if updated.Path != dirPath {
		t.Fatalf("updated path = %q, want %q", updated.Path, dirPath)
	}
	if updated.Visibility != "public" {
		t.Fatalf("updated visibility = %q, want %q", updated.Visibility, "public")
	}
	if updated.EffectiveVisibility != "public" {
		t.Fatalf("updated effective_visibility = %q, want %q", updated.EffectiveVisibility, "public")
	}
	if !updated.ExplicitRule {
		t.Fatalf("updated explicit_rule = false, want true")
	}
	if !updated.Recursive {
		t.Fatalf("updated recursive = false, want true")
	}

	child := runCLIJSONOrFail[pathVisibilityJSON](t, "", "fs", "visibility", "get", filePath)
	if child.EffectiveVisibility != "public" {
		t.Fatalf("child effective_visibility = %q, want %q", child.EffectiveVisibility, "public")
	}
	if child.ResolvedFromPath != dirPath {
		t.Fatalf("child resolved_from_path = %q, want %q", child.ResolvedFromPath, dirPath)
	}

	rule, err := testStorage.GetPathVisibilityRule(ctx, dirPath)
	if err != nil {
		t.Fatalf("GetPathVisibilityRule(dir) failed: %v", err)
	}
	if rule.Visibility != models.VisibilityPublic {
		t.Fatalf("stored dir visibility = %q, want %q", rule.Visibility, models.VisibilityPublic)
	}
	if rule.EntryType != models.PathVisibilityEntryTypeDirectory {
		t.Fatalf("stored dir entry_type = %q, want %q", rule.EntryType, models.PathVisibilityEntryTypeDirectory)
	}

	if _, err := testStorage.GetPathVisibilityRule(ctx, filePath); err != storage.ErrEntryNotFound {
		t.Fatalf("GetPathVisibilityRule(file) = %v, want %v", err, storage.ErrEntryNotFound)
	}
}
