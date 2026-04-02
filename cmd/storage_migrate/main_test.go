package main

import (
	"context"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/searchindex"
	"github.com/niczy/gitslice/internal/storage"
)

func TestRunSearchIndexMaintenanceBuildsAndReusesArtifacts(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "home.tester", Name: "home.tester", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	manifest, err := storage.WriteSliceFileManifest(ctx, st, slice.ID, "tester/docs/readme.md", []byte("hello maintenance\n"))
	if err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	commitHash := "commit-search-maintenance"
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    slice.ID,
		Timestamp:  time.Now(),
		Files: map[string]string{
			"tester/docs/readme.md": manifest.Hash,
		},
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, slice.ID, &models.Commit{
		CommitHash: commitHash,
		Message:    "seed",
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}
	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	meta.HeadCommitHash = commitHash
	if err := st.UpdateSliceMetadata(ctx, slice.ID, meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	first, err := runSearchIndexMaintenance(ctx, st, searchIndexRunOptions{
		CommitsPerSlice:      5,
		IncludeWorkspaceHead: true,
	})
	if err != nil {
		t.Fatalf("runSearchIndexMaintenance(first) failed: %v", err)
	}
	if first.Built == 0 {
		t.Fatalf("expected initial maintenance to build artifacts, got %+v", first)
	}
	if _, err := st.GetSliceSearchArtifact(ctx, slice.ID, commitHash, searchindex.CurrentArtifactVersion); err != nil {
		t.Fatalf("GetSliceSearchArtifact failed: %v", err)
	}
	if _, err := st.GetWorkspaceSearchArtifact(ctx, slice.ID, searchindex.CurrentArtifactVersion); err != nil {
		t.Fatalf("GetWorkspaceSearchArtifact failed: %v", err)
	}

	second, err := runSearchIndexMaintenance(ctx, st, searchIndexRunOptions{
		CommitsPerSlice:      5,
		IncludeWorkspaceHead: true,
	})
	if err != nil {
		t.Fatalf("runSearchIndexMaintenance(second) failed: %v", err)
	}
	if second.Hits == 0 {
		t.Fatalf("expected repeated maintenance to hit cached artifacts, got %+v", second)
	}
}

func TestValidateObjectStoreMigrationConfigRejectsMismatchedR2Namespace(t *testing.T) {
	cfg := storage.ObjectStoreConfig{
		Type:     "r2",
		R2Bucket: "bucket",
		R2Prefix: "staging",
	}
	if err := validateObjectStoreMigrationConfig("target", cfg, "production"); err == nil {
		t.Fatalf("expected mismatched R2 namespace validation failure")
	}
}

func TestValidateObjectStoreMigrationConfigAllowsMatchingR2Namespace(t *testing.T) {
	cfg := storage.ObjectStoreConfig{
		Type:     "r2",
		R2Bucket: "bucket",
		R2Prefix: "production/core",
	}
	if err := validateObjectStoreMigrationConfig("target", cfg, "production"); err != nil {
		t.Fatalf("expected matching R2 namespace to validate, got %v", err)
	}
}

func TestValidateObjectStoreMigrationConfigRequiresEnvForR2(t *testing.T) {
	cfg := storage.ObjectStoreConfig{
		Type:     "r2",
		R2Bucket: "bucket",
		R2Prefix: "production",
	}
	if err := validateObjectStoreMigrationConfig("source", cfg, ""); err == nil {
		t.Fatalf("expected missing R2 env validation failure")
	}
}

func TestSameObjectStoreConfig(t *testing.T) {
	left := storage.ObjectStoreConfig{
		Type:       "r2",
		R2Bucket:   "bucket",
		R2Prefix:   "production",
		R2Endpoint: "https://account.r2.cloudflarestorage.com",
	}
	right := storage.ObjectStoreConfig{
		Type:       "r2",
		R2Bucket:   "bucket",
		R2Prefix:   "production/",
		R2Endpoint: "https://account.r2.cloudflarestorage.com",
	}
	if !sameObjectStoreConfig(left, right) {
		t.Fatalf("expected matching object-store configs to compare equal")
	}
}
