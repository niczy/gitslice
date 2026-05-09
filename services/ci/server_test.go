package ci

import (
	"bytes"
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

func TestManifestIndexTriggersAppliesToManifest(t *testing.T) {
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
	platformManifest := writeCIFile(t, st, homeSliceID, "alice/.gitslice/ci.yaml", []byte(`
version: 1
defaults:
  runner_pool: default
  shell: bash
runner_pools:
  default:
    executor: shell
`))
	apiManifest := writeCIFile(t, st, homeSliceID, "alice/api/.gs-ci.yaml", []byte(`
version: 1
name: api
watch:
  - "**/*.go"
applies_to:
  - "/shared/lib/**"
jobs:
  integration:
    required: true
    commands:
      - go test ./...
`))
	sharedManifest := writeCIFile(t, st, homeSliceID, "alice/shared/lib/util.go", []byte("package lib\n"))
	for _, filePath := range []string{"alice/.gitslice/ci.yaml", "alice/api/.gs-ci.yaml", "alice/shared/lib/util.go"} {
		if err := st.AddFileToSlice(context.Background(), filePath, homeSliceID); err != nil {
			t.Fatalf("AddFileToSlice(%s) failed: %v", filePath, err)
		}
	}
	meta, err := st.GetSliceMetadata(context.Background(), homeSliceID)
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	meta.HeadCommitHash = "base-shared"
	if err := st.UpdateSliceMetadata(context.Background(), homeSliceID, meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}
	if err := st.SaveCommitSnapshot(context.Background(), &models.CommitSnapshot{
		CommitHash: "base-shared",
		SliceID:    homeSliceID,
		Files: map[string]string{
			"alice/.gitslice/ci.yaml":  platformManifest.Hash,
			"alice/api/.gs-ci.yaml":    apiManifest.Hash,
			"alice/shared/lib/util.go": sharedManifest.Hash,
		},
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}

	cs := &models.Changeset{
		ID:             "chg_ci_applies_to",
		Hash:           common.GenerateChangesetVersionHash(),
		SliceID:        homeSliceID,
		BaseCommitHash: "base-shared",
		ModifiedFiles:  []string{"alice/shared/lib/util.go"},
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
		ModifiedFiles:  []string{"alice/shared/lib/util.go"},
		FileHashes:     map[string]string{"alice/shared/lib/util.go": sharedManifest.Hash},
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
	run, err := svc.GetRun(ctx, &civ1.GetRunRequest{RunId: resp.GetRunId()})
	if err != nil {
		t.Fatalf("GetRun failed: %v", err)
	}
	if len(run.GetJobs()) != 1 {
		t.Fatalf("job count = %d, want 1: %#v", len(run.GetJobs()), run.GetJobs())
	}
	job := run.GetJobs()[0]
	if job.GetManifestPath() != "/api/.gs-ci.yaml" || job.GetJobKey() != "integration" {
		t.Fatalf("job = %#v, want api integration from applies_to", job)
	}
	index, err := st.ListCIManifestIndex(context.Background(), "alice", "base-shared")
	if err != nil {
		t.Fatalf("ListCIManifestIndex failed: %v", err)
	}
	if len(index) != 1 || index[0].ManifestPath != "/api/.gs-ci.yaml" || len(index[0].AppliesToGlobs) != 1 {
		t.Fatalf("manifest index = %#v, want indexed api manifest with applies_to", index)
	}
}

func TestRunnerMVPExecutesQueuedJob(t *testing.T) {
	userCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
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
	platformManifest := writeCIFile(t, st, homeSliceID, "alice/.gitslice/ci.yaml", []byte(`
version: 1
defaults:
  runner_pool: default
  shell: bash
  timeout_seconds: 120
runner_pools:
  default:
    executor: shell
cache:
  enabled: true
  paths:
    - ".cache/go"
`))
	folderManifest := writeCIFile(t, st, homeSliceID, "alice/api/.gs-ci.yaml", []byte(`
version: 1
name: api
watch:
  - "**/*.go"
jobs:
  unit:
    required: true
    env:
      FOO: bar
    artifacts:
      - "dist/**"
    cache:
      paths:
        - ".cache/npm"
    commands:
      - printf ok
`))
	mainManifest := writeCIFile(t, st, homeSliceID, "alice/api/main.go", []byte("package main\n"))
	if err := st.SaveCommitSnapshot(context.Background(), &models.CommitSnapshot{
		CommitHash: "base-1",
		SliceID:    homeSliceID,
		Files: map[string]string{
			"alice/.gitslice/ci.yaml": platformManifest.Hash,
			"alice/api/.gs-ci.yaml":   folderManifest.Hash,
			"alice/api/main.go":       mainManifest.Hash,
		},
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}

	cs := &models.Changeset{
		ID:             "chg_ci_runner",
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
	runResp, err := svc.StartRun(userCtx, &civ1.StartRunRequest{ChangesetId: cs.ID, JobKey: "unit"})
	if err != nil {
		t.Fatalf("StartRun failed: %v", err)
	}
	tokenResp, err := svc.CreateRunnerToken(userCtx, &civ1.CreateRunnerTokenRequest{Name: "vm-1", Pool: "default", Ttl: "30m"})
	if err != nil {
		t.Fatalf("CreateRunnerToken failed: %v", err)
	}
	registerResp, err := svc.RegisterRunner(context.Background(), &civ1.RegisterRunnerRequest{RegistrationToken: tokenResp.GetToken(), Version: "test", Executor: "shell"})
	if err != nil {
		t.Fatalf("RegisterRunner failed: %v", err)
	}
	runnerCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+registerResp.GetRunnerToken()))
	pollResp, err := svc.PollJobs(runnerCtx, &civ1.PollJobsRequest{RunnerId: registerResp.GetRunnerId(), MaxJobs: 1})
	if err != nil {
		t.Fatalf("PollJobs failed: %v", err)
	}
	if len(pollResp.GetJobs()) != 1 {
		t.Fatalf("poll job count = %d, want 1", len(pollResp.GetJobs()))
	}
	jobID := pollResp.GetJobs()[0].GetJobId()
	claimResp, err := svc.ClaimJob(runnerCtx, &civ1.ClaimJobRequest{RunnerId: registerResp.GetRunnerId(), JobId: jobID})
	if err != nil {
		t.Fatalf("ClaimJob failed: %v", err)
	}
	payload, err := svc.GetJobPayload(runnerCtx, &civ1.GetJobPayloadRequest{JobId: jobID, LeaseId: claimResp.GetLeaseId()})
	if err != nil {
		t.Fatalf("GetJobPayload failed: %v", err)
	}
	if payload.GetRunId() != runResp.GetRunId() || len(payload.GetCommands()) != 1 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if !payloadContainsPath(payload.GetFiles(), "/api/main.go") || !payloadContainsPath(payload.GetFiles(), "/api/.gs-ci.yaml") {
		t.Fatalf("payload did not include expected workspace files: %#v", payload.GetFiles())
	}
	if payload.GetEnv()["FOO"] != "bar" {
		t.Fatalf("payload env = %#v, want FOO", payload.GetEnv())
	}
	if payload.GetTimeoutSeconds() != 120 {
		t.Fatalf("timeout = %d, want 120", payload.GetTimeoutSeconds())
	}
	if !stringSliceContains(payload.GetArtifacts(), "/api/dist/**") {
		t.Fatalf("artifacts = %#v, want /api/dist/**", payload.GetArtifacts())
	}
	if !stringSliceContains(payload.GetCachePaths(), ".cache/go") || !stringSliceContains(payload.GetCachePaths(), ".cache/npm") {
		t.Fatalf("cache paths = %#v, want platform and job cache paths", payload.GetCachePaths())
	}
	if _, err := svc.UploadArtifact(runnerCtx, &civ1.UploadArtifactRequest{JobId: jobID, LeaseId: claimResp.GetLeaseId(), Path: "/api/dist/result.txt", Payload: []byte("artifact")}); err != nil {
		t.Fatalf("UploadArtifact failed: %v", err)
	}
	artifacts, err := st.ListCIArtifacts(context.Background(), storage.CIArtifactListFilter{JobID: jobID})
	if err != nil {
		t.Fatalf("ListCIArtifacts failed: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Path != "/api/dist/result.txt" || string(artifacts[0].Payload) != "artifact" {
		t.Fatalf("artifacts = %#v, want uploaded result", artifacts)
	}
	if _, err := svc.AppendLog(runnerCtx, &civ1.AppendLogRequest{JobId: jobID, LeaseId: claimResp.GetLeaseId(), ChunkIndex: 0, Stream: "stdout", Payload: []byte("ok\n")}); err != nil {
		t.Fatalf("AppendLog failed: %v", err)
	}
	if _, err := svc.CompleteStep(runnerCtx, &civ1.CompleteStepRequest{JobId: jobID, LeaseId: claimResp.GetLeaseId(), StepIndex: 0, Status: "passed"}); err != nil {
		t.Fatalf("CompleteStep failed: %v", err)
	}
	if _, err := svc.CompleteJob(runnerCtx, &civ1.CompleteJobRequest{JobId: jobID, LeaseId: claimResp.GetLeaseId(), Status: "passed"}); err != nil {
		t.Fatalf("CompleteJob failed: %v", err)
	}
	run, err := svc.GetRun(userCtx, &civ1.GetRunRequest{RunId: runResp.GetRunId()})
	if err != nil {
		t.Fatalf("GetRun failed: %v", err)
	}
	if run.GetStatus() != "passed" {
		t.Fatalf("run status = %q, want passed", run.GetStatus())
	}
	checks, err := svc.ListChecks(userCtx, &civ1.ListChecksRequest{ChangesetId: cs.ID, ChangesetVersionId: snapshot.ID})
	if err != nil {
		t.Fatalf("ListChecks failed: %v", err)
	}
	if len(checks.GetChecks()) != 1 || checks.GetChecks()[0].GetStatus() != "passed" {
		t.Fatalf("checks = %#v, want one passed check", checks.GetChecks())
	}
}

func TestAppendLogRedactsSensitiveEnvAndRejectsOversizedChunks(t *testing.T) {
	st := storage.NewInMemoryStorage()
	now := time.Now()
	if err := st.CreateCIRunner(context.Background(), &storage.CIRunner{
		ID:        "ci_runner_logs",
		HomeID:    "alice",
		Name:      "logger",
		Pool:      "default",
		Executor:  "shell",
		Status:    "busy",
		TokenHash: hashRunnerToken("runner-log-token"),
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateCIRunner failed: %v", err)
	}
	if err := st.CreateCIPlan(context.Background(), &storage.CIPlan{
		Run: &storage.CIRun{ID: "ci_run_logs", HomeID: "alice", ChangesetID: "chg_logs", ChangesetVersionID: "snap-1", PlanHash: "plan", Status: "running", CreatedAt: now},
		Jobs: []*storage.CIJob{{
			ID:             "ci_job_logs",
			RunID:          "ci_run_logs",
			JobKey:         "unit",
			CheckName:      "unit",
			RunnerPool:     "default",
			Status:         "running",
			RunnerID:       "ci_runner_logs",
			LeaseID:        "lease-logs",
			LeaseExpiresAt: timePtr(now.Add(time.Minute)),
			Env: map[string]string{
				"NPM_TOKEN": "super-secret-token",
				"REGULAR":   "visible-value",
			},
		}},
	}); err != nil {
		t.Fatalf("CreateCIPlan failed: %v", err)
	}

	svc := &server{st: st}
	runnerCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer runner-log-token"))
	_, err := svc.AppendLog(runnerCtx, &civ1.AppendLogRequest{
		JobId:      "ci_job_logs",
		LeaseId:    "lease-logs",
		ChunkIndex: 0,
		Stream:     "stdout",
		Payload:    []byte("token=super-secret-token regular=visible-value\n"),
	})
	if err != nil {
		t.Fatalf("AppendLog failed: %v", err)
	}
	chunks, err := st.ListCILogChunks(context.Background(), storage.CILogChunkListFilter{JobID: "ci_job_logs"})
	if err != nil {
		t.Fatalf("ListCILogChunks failed: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(chunks))
	}
	if bytes.Contains(chunks[0].Payload, []byte("super-secret-token")) {
		t.Fatalf("log payload was not redacted: %q", chunks[0].Payload)
	}
	if !bytes.Contains(chunks[0].Payload, []byte("[REDACTED]")) || !bytes.Contains(chunks[0].Payload, []byte("visible-value")) {
		t.Fatalf("unexpected redacted payload: %q", chunks[0].Payload)
	}

	_, err = svc.AppendLog(runnerCtx, &civ1.AppendLogRequest{
		JobId:      "ci_job_logs",
		LeaseId:    "lease-logs",
		ChunkIndex: 1,
		Stream:     "stdout",
		Payload:    bytes.Repeat([]byte("x"), maxCILogChunkPayloadBytes+1),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("oversized AppendLog error = %v, want InvalidArgument", err)
	}
}

func TestExpiredLeasesRequeueThenFailAfterMaxAttempts(t *testing.T) {
	st := storage.NewInMemoryStorage()
	now := time.Now()
	if err := st.CreateCIRunner(context.Background(), &storage.CIRunner{
		ID:        "ci_runner_expired",
		HomeID:    "alice",
		Name:      "expired",
		Pool:      "default",
		Executor:  "shell",
		Status:    "busy",
		TokenHash: hashRunnerToken("expired-token"),
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateCIRunner failed: %v", err)
	}
	if err := st.CreateCIPlan(context.Background(), &storage.CIPlan{
		Run: &storage.CIRun{ID: "ci_run_expired", HomeID: "alice", ChangesetID: "chg_expired", ChangesetVersionID: "snap-1", PlanHash: "plan", Status: "running", CreatedAt: now},
		Jobs: []*storage.CIJob{
			{
				ID:             "ci_job_retry",
				RunID:          "ci_run_expired",
				JobKey:         "retry",
				CheckName:      "retry",
				RunnerPool:     "default",
				Status:         "running",
				RunnerID:       "ci_runner_expired",
				LeaseID:        "lease-retry",
				LeaseExpiresAt: timePtr(now.Add(-time.Minute)),
				AttemptCount:   1,
				MaxAttempts:    2,
			},
			{
				ID:             "ci_job_fail",
				RunID:          "ci_run_expired",
				JobKey:         "fail",
				CheckName:      "fail",
				RunnerPool:     "default",
				Status:         "running",
				RunnerID:       "ci_runner_expired",
				LeaseID:        "lease-fail",
				LeaseExpiresAt: timePtr(now.Add(-time.Minute)),
				AttemptCount:   2,
				MaxAttempts:    2,
			},
		},
	}); err != nil {
		t.Fatalf("CreateCIPlan failed: %v", err)
	}

	svc := &server{st: st}
	if err := svc.reconcileExpiredLeases(context.Background(), now); err != nil {
		t.Fatalf("reconcileExpiredLeases failed: %v", err)
	}
	retryJob, err := st.GetCIJob(context.Background(), "ci_job_retry")
	if err != nil {
		t.Fatalf("GetCIJob retry failed: %v", err)
	}
	if retryJob.Status != "queued" || retryJob.RunnerID != "" || retryJob.LeaseID != "" {
		t.Fatalf("retry job = %#v, want queued with cleared lease", retryJob)
	}
	failJob, err := st.GetCIJob(context.Background(), "ci_job_fail")
	if err != nil {
		t.Fatalf("GetCIJob fail failed: %v", err)
	}
	if failJob.Status != "failed" || !failJob.InfraFailure {
		t.Fatalf("fail job = %#v, want failed infra failure", failJob)
	}
}

func TestRevokeRunnerCanRequeueOrCancelLeasedJobs(t *testing.T) {
	userCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	if _, err := st.EnsureUser(context.Background(), "alice"); err != nil {
		t.Fatalf("EnsureUser failed: %v", err)
	}
	now := time.Now()
	if err := createRunningRunnerJobForTest(st, "ci_runner_revoke_requeue", "ci_job_revoke_requeue", "lease-requeue", now); err != nil {
		t.Fatalf("create requeue fixture failed: %v", err)
	}
	if err := createRunningRunnerJobForTest(st, "ci_runner_revoke_cancel", "ci_job_revoke_cancel", "lease-cancel", now); err != nil {
		t.Fatalf("create cancel fixture failed: %v", err)
	}
	svc := &server{st: st}

	if _, err := svc.RevokeRunner(userCtx, &civ1.RevokeRunnerRequest{RunnerId: "ci_runner_revoke_requeue", RequeueLeased: true}); err != nil {
		t.Fatalf("RevokeRunner requeue failed: %v", err)
	}
	requeued, err := st.GetCIJob(context.Background(), "ci_job_revoke_requeue")
	if err != nil {
		t.Fatalf("GetCIJob requeued failed: %v", err)
	}
	if requeued.Status != "queued" || requeued.RunnerID != "" || requeued.LeaseID != "" {
		t.Fatalf("requeued job = %#v, want queued", requeued)
	}
	requeueRunner, err := st.GetCIRunner(context.Background(), "ci_runner_revoke_requeue")
	if err != nil {
		t.Fatalf("GetCIRunner requeue failed: %v", err)
	}
	if requeueRunner.Status != "revoked" || requeueRunner.TokenHash != "" {
		t.Fatalf("requeue runner = %#v, want revoked credential", requeueRunner)
	}

	if _, err := svc.RevokeRunner(userCtx, &civ1.RevokeRunnerRequest{RunnerId: "ci_runner_revoke_cancel", CancelLeased: true}); err != nil {
		t.Fatalf("RevokeRunner cancel failed: %v", err)
	}
	cancelled, err := st.GetCIJob(context.Background(), "ci_job_revoke_cancel")
	if err != nil {
		t.Fatalf("GetCIJob cancelled failed: %v", err)
	}
	if cancelled.Status != "cancelled" || !cancelled.InfraFailure {
		t.Fatalf("cancelled job = %#v, want cancelled infra failure", cancelled)
	}

	_, err = svc.RevokeRunner(userCtx, &civ1.RevokeRunnerRequest{RunnerId: "ci_runner_revoke_cancel", RequeueLeased: true, CancelLeased: true})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("RevokeRunner conflicting flags error = %v, want InvalidArgument", err)
	}
}

func payloadContainsPath(files []*civ1.WorkspaceFile, want string) bool {
	for _, file := range files {
		if file.GetPath() == want {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func createRunningRunnerJobForTest(st storage.Storage, runnerID, jobID, leaseID string, now time.Time) error {
	if err := st.CreateCIRunner(context.Background(), &storage.CIRunner{
		ID:        runnerID,
		HomeID:    "alice",
		Name:      runnerID,
		Pool:      "default",
		Executor:  "shell",
		Status:    "busy",
		TokenHash: hashRunnerToken(runnerID + "-token"),
		CreatedAt: now,
	}); err != nil {
		return err
	}
	runID := "ci_run_" + runnerID
	return st.CreateCIPlan(context.Background(), &storage.CIPlan{
		Run: &storage.CIRun{ID: runID, HomeID: "alice", ChangesetID: "chg_" + runnerID, ChangesetVersionID: "snap-1", PlanHash: "plan", Status: "running", CreatedAt: now},
		Jobs: []*storage.CIJob{{
			ID:             jobID,
			RunID:          runID,
			JobKey:         "unit",
			CheckName:      "unit",
			RunnerPool:     "default",
			Status:         "running",
			RunnerID:       runnerID,
			LeaseID:        leaseID,
			LeaseExpiresAt: timePtr(now.Add(time.Minute)),
			AttemptCount:   1,
			MaxAttempts:    2,
		}},
	})
}

func timePtr(value time.Time) *time.Time {
	return &value
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
