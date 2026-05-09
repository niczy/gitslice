package ci

import (
	"context"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	civ1 "github.com/niczy/gitslice/proto/ci"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestStartRunPlansAndPersistsQueuedChecks(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	if _, err := st.EnsureUser(context.Background(), "alice"); err != nil {
		t.Fatalf("EnsureUser failed: %v", err)
	}
	homeSliceID := homeslice.IDForUsername("alice")
	homeSlice := &models.Slice{
		ID:        homeSliceID,
		Name:      "alice",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := st.CreateSlice(context.Background(), homeSlice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	writeCIFile(t, st, homeSliceID, "alice/.gitslice/ci.yaml", []byte(`
version: 1
defaults:
  runner_pool: default
  image: golang:1.24
  shell: bash
  timeout_seconds: 900
runner_pools:
  default:
    executor: shell
`))
	writeCIFile(t, st, homeSliceID, "alice/api/.gs-ci.yaml", []byte(`
version: 1
name: api
watch:
  - "**/*.go"
jobs:
  unit:
    required: true
    commands:
      - go test ./...
  lint:
    required: false
    commands:
      - go vet ./...
`))
	mainManifest := writeCIFile(t, st, homeSliceID, "alice/api/main.go", []byte("package main\n"))

	cs := &models.Changeset{
		ID:             "chg_ci_manual",
		Hash:           common.GenerateChangesetVersionHash(),
		SliceID:        homeSliceID,
		BaseCommitHash: "base-1",
		ModifiedFiles:  []string{"alice/api/main.go"},
		Status:         models.ChangesetStatusPending,
		Author:         "alice",
		CreatedAt:      time.Now(),
	}
	if err := st.CreateChangeset(context.Background(), cs); err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	snapshot := &models.ChangesetSnapshot{
		ID:             common.GenerateChangesetSnapshotID(cs.ID, 1),
		ChangesetID:    cs.ID,
		Version:        1,
		Hash:           cs.Hash,
		BaseCommitHash: cs.BaseCommitHash,
		ModifiedFiles:  []string{"alice/api/main.go"},
		FileHashes:     map[string]string{"alice/api/main.go": mainManifest.Hash},
		Author:         "alice",
		CreatedAt:      time.Now(),
	}
	if err := st.CreateChangesetSnapshot(context.Background(), snapshot); err != nil {
		t.Fatalf("CreateChangesetSnapshot failed: %v", err)
	}

	svc := &server{st: st}
	resp, err := svc.StartRun(ctx, &civ1.StartRunRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("StartRun failed: %v", err)
	}
	if resp.GetRunId() == "" || resp.GetStatus() != "queued" {
		t.Fatalf("unexpected StartRun response: %#v", resp)
	}

	run, err := svc.GetRun(ctx, &civ1.GetRunRequest{RunId: resp.GetRunId()})
	if err != nil {
		t.Fatalf("GetRun failed: %v", err)
	}
	if run.GetChangesetVersionId() != snapshot.ID {
		t.Fatalf("run version = %q, want %q", run.GetChangesetVersionId(), snapshot.ID)
	}
	if len(run.GetJobs()) != 2 {
		t.Fatalf("job count = %d, want 2", len(run.GetJobs()))
	}
	if run.GetJobs()[0].GetWorkingDirectory() != "/api" {
		t.Fatalf("job working directory = %q, want /api", run.GetJobs()[0].GetWorkingDirectory())
	}

	checks, err := svc.ListChecks(ctx, &civ1.ListChecksRequest{ChangesetId: cs.ID, ChangesetVersionId: snapshot.ID})
	if err != nil {
		t.Fatalf("ListChecks failed: %v", err)
	}
	if len(checks.GetChecks()) != 1 {
		t.Fatalf("check count = %d, want 1", len(checks.GetChecks()))
	}
	check := checks.GetChecks()[0]
	if check.GetCheckName() != "api/unit" || !check.GetRequired() || check.GetStatus() != "queued" {
		t.Fatalf("unexpected check: %#v", check)
	}
	if check.GetPlanHash() == "" || check.GetRunId() != resp.GetRunId() {
		t.Fatalf("check did not attach to plan/run: %#v", check)
	}
}

func TestListChecksRequiresChangeset(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	if _, err := st.EnsureUser(context.Background(), "alice"); err != nil {
		t.Fatalf("EnsureUser failed: %v", err)
	}

	svc := &server{st: st}
	_, err := svc.ListChecks(ctx, &civ1.ListChecksRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ListChecks error = %v, want InvalidArgument", err)
	}
}

func writeCIFile(tb testing.TB, st storage.Storage, sliceID, filePath string, body []byte) *models.FileManifest {
	tb.Helper()
	manifest, err := storage.WriteSliceFileManifest(context.Background(), st, sliceID, filePath, body)
	if err != nil {
		tb.Fatalf("WriteSliceFileManifest(%s) failed: %v", filePath, err)
	}
	if err := st.AddEntry(context.Background(), &models.DirectoryEntry{
		ID:       sliceID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: sliceID,
		Size:     int64(len(body)),
		Hash:     manifest.Hash,
	}); err != nil {
		tb.Fatalf("AddEntry(%s) failed: %v", filePath, err)
	}
	return manifest
}
