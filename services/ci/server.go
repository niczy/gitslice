package ci

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/niczy/gitslice/internal/authresolver"
	"github.com/niczy/gitslice/internal/authz"
	ciinternal "github.com/niczy/gitslice/internal/ci"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	civ1 "github.com/niczy/gitslice/proto/ci"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RegisterGRPCServer registers the CI API surface. The initial implementation
// intentionally exposes stubs while storage, planning, scheduling, and runner
// execution are added in follow-up PRs.
func RegisterGRPCServer(srv *grpc.Server, st storage.Storage) {
	service := &server{st: st}
	civ1.RegisterCIServiceServer(srv, service)
	civ1.RegisterRunnerAdminServiceServer(srv, service)
	civ1.RegisterRunnerServiceServer(srv, service)
}

type server struct {
	civ1.UnimplementedCIServiceServer
	civ1.UnimplementedRunnerAdminServiceServer
	civ1.UnimplementedRunnerServiceServer

	st storage.Storage
}

func (s *server) StartRun(ctx context.Context, req *civ1.StartRunRequest) (*civ1.StartRunResponse, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetChangesetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "changeset_id is required")
	}
	cs, sourceSlice, snapshot, err := s.loadChangesetVersion(ctx, req.GetChangesetId(), req.GetChangesetVersionId())
	if err != nil {
		return nil, err
	}
	if !authz.HasSliceViewAccess(sourceSlice, identity.Username) {
		return nil, status.Error(codes.PermissionDenied, "not allowed to start CI for this changeset")
	}
	homeID := resolveCIHomeID(sourceSlice, cs, snapshot.ModifiedFiles)
	changedPaths := logicalChangedPaths(homeID, snapshot.ModifiedFiles)
	tree := &changesetTreeReader{
		st:       s.st,
		cs:       cs,
		snapshot: snapshot,
		homeID:   homeID,
	}
	plan, err := (&ciinternal.Planner{Tree: tree}).Plan(ctx, ciinternal.PlanInput{
		HomeID:             homeID,
		SliceID:            cs.SliceID,
		ChangesetID:        cs.ID,
		ChangesetVersionID: snapshot.ID,
		BaseCommitHash:     snapshot.BaseCommitHash,
		CandidateTreeHash:  snapshot.Hash,
		ChangedPaths:       changedPaths,
	})
	if err != nil {
		if errors.Is(err, ciinternal.ErrFileNotFound) {
			return nil, status.Error(codes.FailedPrecondition, "CI platform config /.gitslice/ci.yaml was not found")
		}
		return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("failed to plan CI: %v", err))
	}
	filteredJobs := filterPlanJobs(plan.Jobs, req.GetManifestPath(), req.GetJobKey())
	if len(plan.Jobs) > 0 && len(filteredJobs) == 0 {
		return nil, status.Error(codes.NotFound, "no CI jobs matched the requested manifest/job filter")
	}
	trigger := strings.TrimSpace(req.GetTriggerEvent())
	if trigger == "" {
		trigger = "manual"
	}
	attempt, err := s.nextRunAttempt(ctx, cs.ID, snapshot.ID, plan.PlanHash, trigger)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to compute CI run attempt: %v", err))
	}

	runID := "ci_run_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	now := time.Now()
	runStatus := "queued"
	var finishedAt *time.Time
	if len(filteredJobs) == 0 {
		runStatus = "success"
		finishedAt = &now
	}
	ciPlan := buildStorageCIPlan(runID, attempt, trigger, identity.Username, now, finishedAt, plan, filteredJobs)
	if err := s.st.CreateCIPlan(ctx, ciPlan); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create CI run: %v", err))
	}
	return &civ1.StartRunResponse{RunId: runID, Status: runStatus}, nil
}

func (s *server) GetRun(ctx context.Context, req *civ1.GetRunRequest) (*civ1.Run, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	run, err := s.st.GetCIRun(ctx, req.GetRunId())
	if err != nil {
		return nil, ciStorageError(err, "CI run not found")
	}
	if err := s.authorizeRun(ctx, identity.Username, run); err != nil {
		return nil, err
	}
	jobs, err := s.st.ListCIJobs(ctx, storage.CIJobListFilter{RunID: run.ID, Limit: 1000})
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list CI jobs: %v", err))
	}
	return ciRunToProto(run, jobs), nil
}

func (s *server) ListRuns(ctx context.Context, req *civ1.ListRunsRequest) (*civ1.ListRunsResponse, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	filter := storage.CIRunListFilter{
		ChangesetID: strings.TrimSpace(req.GetChangesetId()),
		Status:      strings.TrimSpace(req.GetStatus()),
		Limit:       int(req.GetLimit()),
	}
	if filter.ChangesetID == "" {
		filter.HomeID = identity.Username
	} else if err := s.authorizeChangeset(ctx, identity.Username, filter.ChangesetID); err != nil {
		return nil, err
	}
	runs, err := s.st.ListCIRuns(ctx, filter)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list CI runs: %v", err))
	}
	resp := &civ1.ListRunsResponse{}
	for _, run := range runs {
		jobs, err := s.st.ListCIJobs(ctx, storage.CIJobListFilter{RunID: run.ID, Limit: 1000})
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list CI jobs: %v", err))
		}
		resp.Runs = append(resp.Runs, ciRunToProto(run, jobs))
	}
	return resp, nil
}

func (s *server) CancelRun(context.Context, *civ1.CancelRunRequest) (*civ1.CancelRunResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) Rerun(context.Context, *civ1.RerunRequest) (*civ1.StartRunResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) StreamLogs(req *civ1.StreamLogsRequest, stream civ1.CIService_StreamLogsServer) error {
	identity, err := authresolver.RequireGRPCIdentity(stream.Context(), s.st)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.GetRunId()) == "" {
		return status.Error(codes.InvalidArgument, "run_id is required")
	}
	run, err := s.st.GetCIRun(stream.Context(), req.GetRunId())
	if err != nil {
		return ciStorageError(err, "CI run not found")
	}
	if err := s.authorizeRun(stream.Context(), identity.Username, run); err != nil {
		return err
	}
	chunks, err := s.st.ListCILogChunks(stream.Context(), storage.CILogChunkListFilter{
		RunID:      strings.TrimSpace(req.GetRunId()),
		JobID:      strings.TrimSpace(req.GetJobId()),
		SinceChunk: req.GetSinceChunk(),
		Limit:      1000,
	})
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to list CI logs: %v", err))
	}
	for _, chunk := range chunks {
		if err := stream.Send(ciLogChunkToProto(req.GetRunId(), chunk)); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) ListChecks(ctx context.Context, req *civ1.ListChecksRequest) (*civ1.ListChecksResponse, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	changesetID := strings.TrimSpace(req.GetChangesetId())
	if changesetID == "" {
		return nil, status.Error(codes.InvalidArgument, "changeset_id is required")
	}
	if err := s.authorizeChangeset(ctx, identity.Username, changesetID); err != nil {
		return nil, err
	}
	checks, err := s.st.ListCIChecks(ctx, changesetID, strings.TrimSpace(req.GetChangesetVersionId()), "")
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list CI checks: %v", err))
	}
	resp := &civ1.ListChecksResponse{}
	for _, check := range checks {
		resp.Checks = append(resp.Checks, ciCheckToProto(check))
	}
	return resp, nil
}

func (s *server) ListRunnerPools(context.Context, *civ1.ListRunnerPoolsRequest) (*civ1.ListRunnerPoolsResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) ListRunners(context.Context, *civ1.ListRunnersRequest) (*civ1.ListRunnersResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) GetRunner(context.Context, *civ1.GetRunnerRequest) (*civ1.Runner, error) {
	return nil, ciNotImplemented()
}

func (s *server) CreateRunnerToken(context.Context, *civ1.CreateRunnerTokenRequest) (*civ1.CreateRunnerTokenResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) DisableRunner(context.Context, *civ1.DisableRunnerRequest) (*civ1.DisableRunnerResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) EnableRunner(context.Context, *civ1.EnableRunnerRequest) (*civ1.EnableRunnerResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) RevokeRunner(context.Context, *civ1.RevokeRunnerRequest) (*civ1.RevokeRunnerResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) ListRunnerJobs(context.Context, *civ1.ListRunnerJobsRequest) (*civ1.ListRunnerJobsResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) ListQueuedJobs(context.Context, *civ1.ListQueuedJobsRequest) (*civ1.ListQueuedJobsResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) RegisterRunner(context.Context, *civ1.RegisterRunnerRequest) (*civ1.RegisterRunnerResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) Heartbeat(context.Context, *civ1.HeartbeatRequest) (*civ1.HeartbeatResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) PollJobs(context.Context, *civ1.PollJobsRequest) (*civ1.PollJobsResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) ClaimJob(context.Context, *civ1.ClaimJobRequest) (*civ1.ClaimJobResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) GetJobPayload(context.Context, *civ1.GetJobPayloadRequest) (*civ1.JobPayload, error) {
	return nil, ciNotImplemented()
}

func (s *server) AppendLog(context.Context, *civ1.AppendLogRequest) (*civ1.AppendLogResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) CompleteStep(context.Context, *civ1.CompleteStepRequest) (*civ1.CompleteStepResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) CompleteJob(context.Context, *civ1.CompleteJobRequest) (*civ1.CompleteJobResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) UploadArtifact(context.Context, *civ1.UploadArtifactRequest) (*civ1.UploadArtifactResponse, error) {
	return nil, ciNotImplemented()
}

func ciNotImplemented() error {
	return status.Error(codes.Unimplemented, "ci service is not implemented yet")
}

func (s *server) loadChangesetVersion(ctx context.Context, changesetID string, requestedVersion string) (*models.Changeset, *models.Slice, *models.ChangesetSnapshot, error) {
	cs, err := s.st.GetChangeset(ctx, strings.TrimSpace(changesetID))
	if err != nil {
		return nil, nil, nil, ciStorageError(err, "changeset not found")
	}
	sourceSlice, err := s.st.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, nil, nil, ciStorageError(err, "source slice not found")
	}
	snapshot, err := resolveChangesetSnapshot(ctx, s.st, cs, requestedVersion)
	if err != nil {
		return nil, nil, nil, err
	}
	return cs, sourceSlice, snapshot, nil
}

func (s *server) authorizeRun(ctx context.Context, username string, run *storage.CIRun) error {
	if run == nil {
		return status.Error(codes.NotFound, "CI run not found")
	}
	if strings.TrimSpace(run.HomeID) != "" && strings.TrimSpace(run.HomeID) == strings.TrimSpace(username) {
		return nil
	}
	return s.authorizeChangeset(ctx, username, run.ChangesetID)
}

func (s *server) authorizeChangeset(ctx context.Context, username string, changesetID string) error {
	cs, err := s.st.GetChangeset(ctx, strings.TrimSpace(changesetID))
	if err != nil {
		return ciStorageError(err, "changeset not found")
	}
	sourceSlice, err := s.st.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return ciStorageError(err, "source slice not found")
	}
	if !authz.HasSliceViewAccess(sourceSlice, username) {
		return status.Error(codes.PermissionDenied, "not allowed to view CI for this changeset")
	}
	return nil
}

func resolveChangesetSnapshot(ctx context.Context, st storage.Storage, cs *models.Changeset, requested string) (*models.ChangesetSnapshot, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		snapshot, err := st.GetChangesetSnapshot(ctx, cs.ID, 0)
		if err == nil {
			return snapshot, nil
		}
		if errors.Is(err, storage.ErrChangesetNotFound) {
			return syntheticChangesetSnapshot(cs), nil
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load latest changeset snapshot: %v", err))
	}
	if version, err := strconv.ParseInt(requested, 10, 32); err == nil && version > 0 {
		snapshot, err := st.GetChangesetSnapshot(ctx, cs.ID, int32(version))
		if err != nil {
			return nil, ciStorageError(err, "changeset snapshot not found")
		}
		return snapshot, nil
	}
	snapshots, err := st.ListChangesetSnapshots(ctx, cs.ID, 1000)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list changeset snapshots: %v", err))
	}
	for _, snapshot := range snapshots {
		if snapshot.ID == requested || snapshot.Hash == requested {
			return snapshot, nil
		}
	}
	return nil, status.Error(codes.NotFound, "changeset snapshot not found")
}

func syntheticChangesetSnapshot(cs *models.Changeset) *models.ChangesetSnapshot {
	if cs == nil {
		return nil
	}
	createdAt := cs.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return &models.ChangesetSnapshot{
		ID:             common.GenerateChangesetSnapshotID(cs.ID, 1),
		ChangesetID:    cs.ID,
		Version:        1,
		Hash:           cs.Hash,
		BaseCommitHash: cs.BaseCommitHash,
		ModifiedFiles:  append([]string(nil), cs.ModifiedFiles...),
		Author:         cs.Author,
		Message:        cs.Message,
		CreatedAt:      createdAt,
	}
}

func resolveCIHomeID(sourceSlice *models.Slice, cs *models.Changeset, modifiedFiles []string) string {
	if sourceSlice != nil {
		if username := homeslice.UsernameFromSliceID(sourceSlice.ID); username != "" {
			return username
		}
	}
	if homeRoot := commonHomeRootFromFiles(modifiedFiles); homeRoot != "" {
		return homeRoot
	}
	if sourceSlice != nil {
		if createdBy := strings.TrimSpace(sourceSlice.CreatedBy); createdBy != "" {
			return createdBy
		}
		for _, owner := range sourceSlice.Owners {
			if owner = strings.TrimSpace(owner); owner != "" {
				return owner
			}
		}
	}
	if cs != nil && strings.TrimSpace(cs.SliceID) != "" {
		return strings.TrimSpace(cs.SliceID)
	}
	return "global"
}

func commonHomeRootFromFiles(files []string) string {
	var root string
	for _, raw := range files {
		cleaned := common.CleanRelativePath(raw)
		if cleaned == "" {
			continue
		}
		part, _, _ := strings.Cut(cleaned, "/")
		if part == "" {
			continue
		}
		if root == "" {
			root = part
			continue
		}
		if root != part {
			return ""
		}
	}
	return root
}

func logicalChangedPaths(homeID string, files []string) []string {
	paths := make([]string, 0, len(files))
	for _, filePath := range files {
		paths = append(paths, storagePathToLogical(homeID, filePath))
	}
	return paths
}

func storagePathToLogical(homeID string, storedPath string) string {
	cleaned := common.CleanRelativePath(storedPath)
	homeID = strings.Trim(strings.TrimSpace(homeID), "/")
	if homeID != "" {
		if cleaned == homeID {
			return "/"
		}
		if strings.HasPrefix(cleaned, homeID+"/") {
			cleaned = strings.TrimPrefix(cleaned, homeID+"/")
		}
	}
	if cleaned == "" {
		return "/"
	}
	return "/" + cleaned
}

func logicalToStoragePath(homeID string, logicalPath string) string {
	cleaned := common.CleanRelativePath(strings.TrimPrefix(strings.TrimSpace(logicalPath), "/"))
	homeID = strings.Trim(strings.TrimSpace(homeID), "/")
	if homeID != "" && cleaned != homeID && !strings.HasPrefix(cleaned, homeID+"/") {
		cleaned = path.Join(homeID, cleaned)
	}
	return cleaned
}

type changesetTreeReader struct {
	st       storage.Storage
	cs       *models.Changeset
	snapshot *models.ChangesetSnapshot
	homeID   string
}

func (r *changesetTreeReader) ReadFile(ctx context.Context, logicalPath string) ([]byte, string, error) {
	storedPath := logicalToStoragePath(r.homeID, logicalPath)
	if strings.TrimSpace(logicalPath) == ciinternal.PlatformConfigPath {
		if content, err := storage.ReadSliceFileContent(ctx, r.st, homeslice.IDForUsername(r.homeID), storedPath); err == nil {
			return content.Content, content.Hash, nil
		}
		if r.snapshot != nil && strings.TrimSpace(r.snapshot.BaseCommitHash) != "" {
			content, err := r.st.GetFileAtCommit(ctx, r.snapshot.BaseCommitHash, storedPath)
			if err == nil {
				return content.Content, content.Hash, nil
			}
			if !errors.Is(err, storage.ErrEntryNotFound) && !errors.Is(err, storage.ErrCommitNotFound) {
				return nil, "", err
			}
		}
		return nil, "", ciinternal.ErrFileNotFound
	}
	if r.snapshot != nil {
		if hash, ok := r.snapshot.FileHashes[storedPath]; ok && strings.TrimSpace(hash) != "" {
			content, err := storage.ReadVersionedFileContent(ctx, r.st, hash)
			if err != nil {
				return nil, "", err
			}
			return content.Content, strings.TrimSpace(hash), nil
		}
		if snapshotContainsPath(r.snapshot.ModifiedFiles, storedPath) {
			return nil, "", ciinternal.ErrFileNotFound
		}
		if strings.TrimSpace(r.snapshot.BaseCommitHash) != "" {
			content, err := r.st.GetFileAtCommit(ctx, r.snapshot.BaseCommitHash, storedPath)
			if err == nil {
				return content.Content, content.Hash, nil
			}
			if !errors.Is(err, storage.ErrEntryNotFound) && !errors.Is(err, storage.ErrCommitNotFound) {
				return nil, "", err
			}
		}
	}
	if r.cs != nil {
		content, err := storage.ReadSliceFileContent(ctx, r.st, r.cs.SliceID, storedPath)
		if err == nil {
			return content.Content, content.Hash, nil
		}
		if !errors.Is(err, storage.ErrEntryNotFound) {
			return nil, "", err
		}
	}
	return nil, "", ciinternal.ErrFileNotFound
}

func snapshotContainsPath(paths []string, target string) bool {
	target = common.CleanRelativePath(target)
	for _, raw := range paths {
		if common.CleanRelativePath(raw) == target {
			return true
		}
	}
	return false
}

func filterPlanJobs(jobs []ciinternal.PlanJob, manifestPath string, jobKey string) []ciinternal.PlanJob {
	manifestPath = strings.TrimSpace(manifestPath)
	jobKey = strings.TrimSpace(jobKey)
	if manifestPath != "" {
		normalized, err := ciinternal.NormalizeHomePath(manifestPath)
		if err == nil {
			manifestPath = normalized
		}
	}
	out := make([]ciinternal.PlanJob, 0, len(jobs))
	for _, job := range jobs {
		if manifestPath != "" && job.ManifestPath != manifestPath {
			continue
		}
		if jobKey != "" && job.JobKey != jobKey {
			continue
		}
		out = append(out, job)
	}
	return out
}

func (s *server) nextRunAttempt(ctx context.Context, changesetID, versionID, planHash, trigger string) (int, error) {
	runs, err := s.st.ListCIRuns(ctx, storage.CIRunListFilter{
		ChangesetID:        changesetID,
		ChangesetVersionID: versionID,
		PlanHash:           planHash,
		Limit:              1000,
	})
	if err != nil {
		return 0, err
	}
	next := 1
	for _, run := range runs {
		if run.TriggerEvent == trigger && run.Attempt >= next {
			next = run.Attempt + 1
		}
	}
	return next, nil
}

func buildStorageCIPlan(runID string, attempt int, trigger string, username string, createdAt time.Time, finishedAt *time.Time, plan *ciinternal.Plan, jobs []ciinternal.PlanJob) *storage.CIPlan {
	runStatus := "queued"
	if len(jobs) == 0 {
		runStatus = "success"
	}
	run := &storage.CIRun{
		ID:                 runID,
		HomeID:             plan.HomeID,
		SliceID:            plan.SliceID,
		ChangesetID:        plan.ChangesetID,
		ChangesetVersionID: plan.ChangesetVersionID,
		BaseCommitHash:     plan.BaseCommitHash,
		CandidateTreeHash:  plan.CandidateTreeHash,
		PlatformConfigHash: plan.PlatformConfigHash,
		PlanHash:           plan.PlanHash,
		Attempt:            attempt,
		TriggerEvent:       trigger,
		TriggeredByUserID:  username,
		Status:             runStatus,
		CreatedAt:          createdAt,
		FinishedAt:         finishedAt,
	}
	manifestIDs := make(map[string]string, len(plan.Manifests))
	storagePlan := &storage.CIPlan{Run: run}
	for _, manifest := range plan.Manifests {
		manifestID := "ci_manifest_" + strings.ReplaceAll(uuid.New().String(), "-", "")
		manifestIDs[manifest.Path] = manifestID
		storagePlan.Manifests = append(storagePlan.Manifests, &storage.CIRunManifest{
			ID:           manifestID,
			RunID:        runID,
			ManifestPath: manifest.Path,
			ManifestDir:  manifest.Dir,
			ManifestHash: manifest.Hash,
			MatchedPaths: append([]string(nil), manifest.MatchedChangedPaths...),
			ParseStatus:  "ok",
		})
	}
	jobIDs := make(map[string]string, len(jobs))
	for _, job := range jobs {
		jobID := "ci_job_" + strings.ReplaceAll(uuid.New().String(), "-", "")
		jobIDs[job.ManifestPath+"\x00"+job.JobKey] = jobID
	}
	for _, job := range jobs {
		jobID := jobIDs[job.ManifestPath+"\x00"+job.JobKey]
		dependsOn := make([]string, 0, len(job.Needs))
		for _, need := range job.Needs {
			if dependencyID := jobIDs[job.ManifestPath+"\x00"+need]; dependencyID != "" {
				dependsOn = append(dependsOn, dependencyID)
			}
		}
		storagePlan.Jobs = append(storagePlan.Jobs, &storage.CIJob{
			ID:               jobID,
			RunID:            runID,
			ManifestRunID:    manifestIDs[job.ManifestPath],
			ManifestPath:     job.ManifestPath,
			JobKey:           job.JobKey,
			CheckName:        job.CheckName,
			Required:         job.Required,
			RunnerPool:       job.RunnerPool,
			Image:            job.Image,
			WorkingDirectory: job.WorkingDirectory,
			Status:           "queued",
			DependsOnJobIDs:  dependsOn,
		})
		for idx, command := range job.Commands {
			storagePlan.Steps = append(storagePlan.Steps, &storage.CIStep{
				ID:        "ci_step_" + strings.ReplaceAll(uuid.New().String(), "-", ""),
				JobID:     jobID,
				StepIndex: idx,
				Command:   command,
				Status:    "queued",
			})
		}
		if job.Required {
			storagePlan.Checks = append(storagePlan.Checks, &storage.CICheck{
				ChangesetID:        plan.ChangesetID,
				ChangesetVersionID: plan.ChangesetVersionID,
				PlanHash:           plan.PlanHash,
				ManifestPath:       job.ManifestPath,
				JobKey:             job.JobKey,
				CheckName:          job.CheckName,
				Required:           true,
				Status:             "queued",
				RunID:              runID,
				UpdatedAt:          createdAt,
			})
		}
	}
	return storagePlan
}

func ciRunToProto(run *storage.CIRun, jobs []*storage.CIJob) *civ1.Run {
	if run == nil {
		return nil
	}
	out := &civ1.Run{
		RunId:              run.ID,
		HomeId:             run.HomeID,
		SliceId:            run.SliceID,
		ChangesetId:        run.ChangesetID,
		ChangesetVersionId: run.ChangesetVersionID,
		PlanHash:           run.PlanHash,
		TriggerEvent:       run.TriggerEvent,
		Status:             run.Status,
		Attempt:            int32(run.Attempt),
		CreatedAt:          formatTime(run.CreatedAt),
		StartedAt:          formatTimePtr(run.StartedAt),
		FinishedAt:         formatTimePtr(run.FinishedAt),
	}
	for _, job := range jobs {
		out.Jobs = append(out.Jobs, ciJobToProto(job))
	}
	return out
}

func ciJobToProto(job *storage.CIJob) *civ1.Job {
	if job == nil {
		return nil
	}
	return &civ1.Job{
		JobId:            job.ID,
		RunId:            job.RunID,
		ManifestPath:     job.ManifestPath,
		JobKey:           job.JobKey,
		CheckName:        job.CheckName,
		Required:         job.Required,
		RunnerPool:       job.RunnerPool,
		Image:            job.Image,
		WorkingDirectory: job.WorkingDirectory,
		Status:           job.Status,
		RunnerId:         job.RunnerID,
		ExitCode:         int32(job.ExitCode),
		InfraFailure:     job.InfraFailure,
		StartedAt:        formatTimePtr(job.StartedAt),
		FinishedAt:       formatTimePtr(job.FinishedAt),
	}
}

func ciCheckToProto(check *storage.CICheck) *civ1.Check {
	if check == nil {
		return nil
	}
	return &civ1.Check{
		ChangesetId:        check.ChangesetID,
		ChangesetVersionId: check.ChangesetVersionID,
		PlanHash:           check.PlanHash,
		ManifestPath:       check.ManifestPath,
		JobKey:             check.JobKey,
		CheckName:          check.CheckName,
		Required:           check.Required,
		Status:             check.Status,
		RunId:              check.RunID,
		UpdatedAt:          formatTime(check.UpdatedAt),
	}
}

func ciLogChunkToProto(runID string, chunk *storage.CILogChunk) *civ1.LogEvent {
	if chunk == nil {
		return nil
	}
	if runID == "" {
		runID = chunk.RunID
	}
	return &civ1.LogEvent{
		RunId:      runID,
		JobId:      chunk.JobID,
		ChunkIndex: chunk.ChunkIndex,
		Stream:     chunk.Stream,
		Payload:    append([]byte(nil), chunk.Payload...),
		CreatedAt:  formatTime(chunk.CreatedAt),
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatTime(*t)
}

func ciStorageError(err error, notFoundMessage string) error {
	if errors.Is(err, storage.ErrEntryNotFound) || errors.Is(err, storage.ErrChangesetNotFound) || errors.Is(err, storage.ErrSliceNotFound) {
		return status.Error(codes.NotFound, notFoundMessage)
	}
	return status.Error(codes.Internal, err.Error())
}
