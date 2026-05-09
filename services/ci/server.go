package ci

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/niczy/gitslice/internal/auth"
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

func EnqueueChangesetExportRun(ctx context.Context, st storage.Storage, changesetID string, triggeredByUserID string) (*civ1.StartRunResponse, bool, error) {
	service := &server{st: st}
	cs, sourceSlice, snapshot, err := service.loadChangesetVersion(ctx, changesetID, "")
	if err != nil {
		return nil, false, err
	}
	homeID := resolveCIHomeID(sourceSlice, cs, snapshot.ModifiedFiles)
	platform, err := service.loadPlatformConfigForHome(ctx, homeID)
	if err != nil {
		return nil, false, err
	}
	if platform == nil || !platform.Triggers.ChangesetExport {
		return nil, false, nil
	}
	resp, err := service.startRun(ctx, startRunRequest{
		ChangesetID:        cs.ID,
		ChangesetVersionID: snapshot.ID,
		TriggerEvent:       "changeset_export",
		TriggeredByUserID:  strings.TrimSpace(triggeredByUserID),
	})
	if err != nil {
		return nil, true, err
	}
	return resp, true, nil
}

type MergeGateRequest struct {
	ChangesetID       string
	TriggeredByUserID string
	Force             bool
	ForceReason       string
}

type MergeGateResult struct {
	ChangesetID        string
	ChangesetVersionID string
	PlanHash           string
	RunID              string
	RunStatus          string
	Forced             bool
	ForceReason        string
	ForcedBy           string
	Message            string
}

func EnforceChangesetMergeGate(ctx context.Context, st storage.Storage, req MergeGateRequest) (*MergeGateResult, error) {
	service := &server{st: st}
	return service.enforceChangesetMergeGate(ctx, req)
}

type server struct {
	civ1.UnimplementedCIServiceServer
	civ1.UnimplementedRunnerAdminServiceServer
	civ1.UnimplementedRunnerServiceServer

	st storage.Storage
}

const maxCIArtifactPayloadBytes = 32 << 20

func (s *server) StartRun(ctx context.Context, req *civ1.StartRunRequest) (*civ1.StartRunResponse, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	return s.startRun(ctx, startRunRequest{
		ChangesetID:        req.GetChangesetId(),
		ChangesetVersionID: req.GetChangesetVersionId(),
		ManifestPath:       req.GetManifestPath(),
		JobKey:             req.GetJobKey(),
		TriggerEvent:       req.GetTriggerEvent(),
		TriggeredByUserID:  identity.Username,
		AuthorizeUsername:  identity.Username,
	})
}

type startRunRequest struct {
	ChangesetID        string
	ChangesetVersionID string
	ManifestPath       string
	JobKey             string
	TriggerEvent       string
	TriggeredByUserID  string
	AuthorizeUsername  string
}

func (s *server) startRun(ctx context.Context, req startRunRequest) (*civ1.StartRunResponse, error) {
	if strings.TrimSpace(req.ChangesetID) == "" {
		return nil, status.Error(codes.InvalidArgument, "changeset_id is required")
	}
	cs, sourceSlice, snapshot, err := s.loadChangesetVersion(ctx, req.ChangesetID, req.ChangesetVersionID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.AuthorizeUsername) != "" && !authz.HasSliceViewAccess(sourceSlice, req.AuthorizeUsername) {
		return nil, status.Error(codes.PermissionDenied, "not allowed to start CI for this changeset")
	}
	plan, _, err := s.planChangesetVersion(ctx, cs, sourceSlice, snapshot)
	if err != nil {
		if errors.Is(err, ciinternal.ErrFileNotFound) {
			return nil, status.Error(codes.FailedPrecondition, "CI platform config /.gitslice/ci.yaml was not found")
		}
		return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("failed to plan CI: %v", err))
	}
	filteredJobs := filterPlanJobs(plan.Jobs, req.ManifestPath, req.JobKey)
	if len(plan.Jobs) > 0 && len(filteredJobs) == 0 {
		return nil, status.Error(codes.NotFound, "no CI jobs matched the requested manifest/job filter")
	}
	trigger := strings.TrimSpace(req.TriggerEvent)
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
	ciPlan := buildStorageCIPlan(runID, attempt, trigger, req.TriggeredByUserID, now, finishedAt, plan, filteredJobs)
	if err := s.st.CreateCIPlan(ctx, ciPlan); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create CI run: %v", err))
	}
	if trigger == "changeset_export" {
		if err := s.supersedeOlderRuns(ctx, cs.ID, snapshot.ID, runID, now); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to supersede older CI runs: %v", err))
		}
	}
	return &civ1.StartRunResponse{RunId: runID, Status: runStatus}, nil
}

func (s *server) enforceChangesetMergeGate(ctx context.Context, req MergeGateRequest) (*MergeGateResult, error) {
	cs, sourceSlice, snapshot, err := s.loadChangesetVersion(ctx, req.ChangesetID, "")
	if err != nil {
		return nil, err
	}
	homeID := resolveCIHomeID(sourceSlice, cs, snapshot.ModifiedFiles)
	platform, err := s.loadPlatformConfigForHome(ctx, homeID)
	if err != nil {
		return nil, err
	}
	policy := normalizedMergePolicy(platform)
	result := &MergeGateResult{
		ChangesetID:        cs.ID,
		ChangesetVersionID: snapshot.ID,
		Forced:             req.Force,
		ForceReason:        strings.TrimSpace(req.ForceReason),
		ForcedBy:           strings.TrimSpace(req.TriggeredByUserID),
	}
	if req.Force {
		if !policy.AllowForceMerge {
			return nil, status.Error(codes.FailedPrecondition, "force merge is disabled by CI merge policy")
		}
		if result.ForceReason == "" {
			return nil, status.Error(codes.InvalidArgument, "--reason is required with --force")
		}
	}
	if !policy.RequireSuccess {
		return result, nil
	}

	plan, _, err := s.planChangesetVersion(ctx, cs, sourceSlice, snapshot)
	if err != nil {
		if req.Force {
			result.Message = fmt.Sprintf("force merge bypassed CI planning failure: %v", err)
			return result, nil
		}
		if errors.Is(err, ciinternal.ErrFileNotFound) {
			return nil, status.Error(codes.FailedPrecondition, "CI platform config /.gitslice/ci.yaml was not found")
		}
		return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("failed to plan CI for merge: %v", err))
	}
	result.PlanHash = plan.PlanHash
	if plan.MissingManifest {
		if policy.MissingManifest == "block" {
			message := "CI merge policy requires a matching manifest, but none matched this changeset"
			if req.Force {
				result.Message = "force merge bypassed CI: " + message
				return result, nil
			}
			return nil, status.Error(codes.FailedPrecondition, message)
		}
		return result, nil
	}

	gate := s.evaluateRequiredChecks(ctx, plan)
	if gate.err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to evaluate CI checks: %v", gate.err))
	}
	if gate.requiredTotal == 0 || gate.blockMessage == "" {
		return result, nil
	}
	if req.Force {
		result.Message = "force merge bypassed CI: " + gate.blockMessage
		return result, nil
	}
	if platform != nil && platform.Triggers.MergeRequested && gate.missing > 0 && gate.running == 0 && gate.queued == 0 {
		runID, runStatus, enqueueErr := s.enqueueMergeRequestedRun(ctx, cs.ID, snapshot.ID, plan.PlanHash, req.TriggeredByUserID)
		if enqueueErr != nil {
			return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("%s; also failed to enqueue CI: %v", gate.blockMessage, enqueueErr))
		}
		if runID != "" {
			return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("%s; started CI run %s", gate.blockMessage, runID))
		}
		if runStatus != "" {
			return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("%s; CI run is %s", gate.blockMessage, runStatus))
		}
	}
	return nil, status.Error(codes.FailedPrecondition, gate.blockMessage)
}

type mergePolicy struct {
	RequireSuccess  bool
	MissingManifest string
	StaleCI         string
	AllowForceMerge bool
}

func normalizedMergePolicy(platform *ciinternal.PlatformConfig) mergePolicy {
	policy := mergePolicy{MissingManifest: "allow", StaleCI: "block"}
	if platform == nil {
		return policy
	}
	policy.RequireSuccess = platform.Merge.RequireSuccess
	policy.AllowForceMerge = platform.Merge.AllowForceMerge
	if value := strings.ToLower(strings.TrimSpace(platform.Merge.MissingManifest)); value != "" {
		policy.MissingManifest = value
	}
	if value := strings.ToLower(strings.TrimSpace(platform.Merge.StaleCI)); value != "" {
		policy.StaleCI = value
	}
	return policy
}

type requiredCheckGate struct {
	requiredTotal int
	missing       int
	failed        int
	running       int
	queued        int
	blockMessage  string
	err           error
}

func (s *server) evaluateRequiredChecks(ctx context.Context, plan *ciinternal.Plan) requiredCheckGate {
	gate := requiredCheckGate{}
	if plan == nil {
		gate.blockMessage = "CI plan is missing"
		return gate
	}
	requiredJobs := make(map[string]ciinternal.PlanJob)
	for _, job := range plan.Jobs {
		if !job.Required {
			continue
		}
		key := ciCheckKey(job.ManifestPath, job.JobKey)
		requiredJobs[key] = job
	}
	gate.requiredTotal = len(requiredJobs)
	if len(requiredJobs) == 0 {
		return gate
	}
	checks, err := s.st.ListCIChecks(ctx, plan.ChangesetID, plan.ChangesetVersionID, plan.PlanHash)
	if err != nil {
		gate.err = err
		return gate
	}
	seen := make(map[string]bool, len(checks))
	for _, check := range checks {
		if check == nil || !check.Required {
			continue
		}
		key := ciCheckKey(check.ManifestPath, check.JobKey)
		if _, ok := requiredJobs[key]; !ok {
			continue
		}
		seen[key] = true
		switch check.Status {
		case "passed":
		case "queued":
			gate.queued++
		case "running":
			gate.running++
		default:
			gate.failed++
		}
	}
	for key := range requiredJobs {
		if !seen[key] {
			gate.missing++
		}
	}
	switch {
	case gate.failed > 0:
		gate.blockMessage = fmt.Sprintf("CI required checks failed for current changeset version (%d failed)", gate.failed)
	case gate.running > 0 || gate.queued > 0:
		gate.blockMessage = fmt.Sprintf("CI required checks are still running for current changeset version (%d running, %d queued)", gate.running, gate.queued)
	case gate.missing > 0:
		gate.blockMessage = fmt.Sprintf("CI required checks are missing or stale for current changeset version (%d missing)", gate.missing)
	}
	return gate
}

func ciCheckKey(manifestPath string, jobKey string) string {
	return strings.TrimSpace(manifestPath) + "\x00" + strings.TrimSpace(jobKey)
}

func (s *server) enqueueMergeRequestedRun(ctx context.Context, changesetID string, versionID string, planHash string, username string) (string, string, error) {
	runs, err := s.st.ListCIRuns(ctx, storage.CIRunListFilter{
		ChangesetID:        strings.TrimSpace(changesetID),
		ChangesetVersionID: strings.TrimSpace(versionID),
		PlanHash:           strings.TrimSpace(planHash),
		Limit:              1000,
	})
	if err != nil {
		return "", "", err
	}
	for _, run := range runs {
		if run == nil {
			continue
		}
		if run.Status == "queued" || run.Status == "running" {
			return run.ID, run.Status, nil
		}
	}
	resp, err := s.startRun(ctx, startRunRequest{
		ChangesetID:        changesetID,
		ChangesetVersionID: versionID,
		TriggerEvent:       "merge_requested",
		TriggeredByUserID:  strings.TrimSpace(username),
	})
	if err != nil {
		return "", "", err
	}
	return resp.GetRunId(), resp.GetStatus(), nil
}

func (s *server) planChangesetVersion(ctx context.Context, cs *models.Changeset, sourceSlice *models.Slice, snapshot *models.ChangesetSnapshot) (*ciinternal.Plan, string, error) {
	if cs == nil || snapshot == nil {
		return nil, "", status.Error(codes.InvalidArgument, "changeset and snapshot are required")
	}
	homeID := resolveCIHomeID(sourceSlice, cs, snapshot.ModifiedFiles)
	changedPaths := logicalChangedPaths(homeID, snapshot.ModifiedFiles)
	indexedManifestPaths, err := s.indexedManifestPathsForChangedPaths(ctx, homeID, changedPaths)
	if err != nil {
		return nil, "", err
	}
	tree := &changesetTreeReader{
		st:       s.st,
		cs:       cs,
		snapshot: snapshot,
		homeID:   homeID,
	}
	plan, err := (&ciinternal.Planner{Tree: tree}).Plan(ctx, ciinternal.PlanInput{
		HomeID:               homeID,
		SliceID:              cs.SliceID,
		ChangesetID:          cs.ID,
		ChangesetVersionID:   snapshot.ID,
		BaseCommitHash:       snapshot.BaseCommitHash,
		CandidateTreeHash:    snapshot.Hash,
		ChangedPaths:         changedPaths,
		IndexedManifestPaths: indexedManifestPaths,
	})
	return plan, homeID, err
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

func (s *server) ListRunnerPools(ctx context.Context, req *civ1.ListRunnerPoolsRequest) (*civ1.ListRunnerPoolsResponse, error) {
	_ = req
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	platform, err := s.loadPlatformConfigForHome(ctx, identity.Username)
	if err != nil {
		return nil, err
	}
	runners, err := s.st.ListCIRunners(ctx, storage.CIRunnerListFilter{HomeID: identity.Username, Limit: 1000})
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list CI runners: %v", err))
	}
	queued, err := s.st.ListCIJobs(ctx, storage.CIJobListFilter{Status: "queued", Limit: 1000})
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list queued CI jobs: %v", err))
	}
	resp := &civ1.ListRunnerPoolsResponse{}
	for name, pool := range normalizedRunnerPools(platform) {
		out := &civ1.RunnerPool{
			Name:                     name,
			Executor:                 defaultString(pool.Executor, "shell"),
			Labels:                   append([]string(nil), pool.Labels...),
			AllowedImages:            append([]string(nil), pool.AllowedImages...),
			MaxParallelJobsPerRunner: int32(pool.MaxParallelJobsPerRunner),
		}
		for _, runner := range runners {
			if runner.Pool != name {
				continue
			}
			switch runner.Status {
			case "busy":
				out.BusyRunners++
				out.OnlineRunners++
			case "idle":
				out.OnlineRunners++
			}
		}
		for _, job := range queued {
			if job.RunnerPool == name {
				out.QueuedJobs++
			}
		}
		resp.Pools = append(resp.Pools, out)
	}
	sort.Slice(resp.Pools, func(i, j int) bool { return resp.Pools[i].Name < resp.Pools[j].Name })
	return resp, nil
}

func (s *server) ListRunners(ctx context.Context, req *civ1.ListRunnersRequest) (*civ1.ListRunnersResponse, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	runners, err := s.st.ListCIRunners(ctx, storage.CIRunnerListFilter{
		HomeID: identity.Username,
		Pool:   strings.TrimSpace(req.GetPool()),
		Status: strings.TrimSpace(req.GetStatus()),
		Limit:  int(req.GetLimit()),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list CI runners: %v", err))
	}
	resp := &civ1.ListRunnersResponse{}
	for _, runner := range runners {
		resp.Runners = append(resp.Runners, ciRunnerToProto(runner))
	}
	return resp, nil
}

func (s *server) GetRunner(ctx context.Context, req *civ1.GetRunnerRequest) (*civ1.Runner, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	runner, err := s.loadAuthorizedRunner(ctx, identity.Username, req.GetRunnerId())
	if err != nil {
		return nil, err
	}
	return ciRunnerToProto(runner), nil
}

func (s *server) CreateRunnerToken(ctx context.Context, req *civ1.CreateRunnerTokenRequest) (*civ1.CreateRunnerTokenResponse, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "runner name is required")
	}
	pool := strings.TrimSpace(req.GetPool())
	if pool == "" {
		pool = "default"
	}
	platform, err := s.loadPlatformConfigForHome(ctx, identity.Username)
	if err != nil {
		return nil, err
	}
	if _, ok := normalizedRunnerPools(platform)[pool]; !ok {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("runner pool %q is not defined", pool))
	}
	ttl := 30 * time.Minute
	if raw := strings.TrimSpace(req.GetTtl()); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return nil, status.Error(codes.InvalidArgument, "ttl must be a positive Go duration, for example 30m")
		}
		if parsed > 24*time.Hour {
			return nil, status.Error(codes.InvalidArgument, "ttl must be 24h or less")
		}
		ttl = parsed
	}
	rawToken, err := randomToken("gsrt")
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to generate runner token: %v", err))
	}
	now := time.Now()
	expiresAt := now.Add(ttl)
	if err := s.st.CreateCIRunnerRegistrationToken(ctx, &storage.CIRunnerRegistrationToken{
		TokenHash:       hashRunnerToken(rawToken),
		HomeID:          identity.Username,
		Name:            name,
		Pool:            pool,
		Labels:          append([]string(nil), req.GetLabels()...),
		ExpiresAt:       expiresAt,
		CreatedByUserID: identity.Username,
		CreatedAt:       now,
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create runner registration token: %v", err))
	}
	return &civ1.CreateRunnerTokenResponse{Token: rawToken, ExpiresAt: formatTime(expiresAt)}, nil
}

func (s *server) DisableRunner(ctx context.Context, req *civ1.DisableRunnerRequest) (*civ1.DisableRunnerResponse, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	runner, err := s.loadAuthorizedRunner(ctx, identity.Username, req.GetRunnerId())
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if err := s.st.UpdateCIRunnerStatus(ctx, runner.ID, "disabled", &now); err != nil {
		return nil, ciStorageError(err, "runner not found")
	}
	return &civ1.DisableRunnerResponse{RunnerId: runner.ID, Status: "disabled"}, nil
}

func (s *server) EnableRunner(ctx context.Context, req *civ1.EnableRunnerRequest) (*civ1.EnableRunnerResponse, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	runner, err := s.loadAuthorizedRunner(ctx, identity.Username, req.GetRunnerId())
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if err := s.st.UpdateCIRunnerStatus(ctx, runner.ID, "idle", &now); err != nil {
		return nil, ciStorageError(err, "runner not found")
	}
	return &civ1.EnableRunnerResponse{RunnerId: runner.ID, Status: "idle"}, nil
}

func (s *server) RevokeRunner(ctx context.Context, req *civ1.RevokeRunnerRequest) (*civ1.RevokeRunnerResponse, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	runner, err := s.loadAuthorizedRunner(ctx, identity.Username, req.GetRunnerId())
	if err != nil {
		return nil, err
	}
	if err := s.st.RevokeCIRunner(ctx, runner.ID, time.Now()); err != nil {
		return nil, ciStorageError(err, "runner not found")
	}
	return &civ1.RevokeRunnerResponse{RunnerId: runner.ID, Status: "revoked"}, nil
}

func (s *server) ListRunnerJobs(ctx context.Context, req *civ1.ListRunnerJobsRequest) (*civ1.ListRunnerJobsResponse, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	runner, err := s.loadAuthorizedRunner(ctx, identity.Username, req.GetRunnerId())
	if err != nil {
		return nil, err
	}
	jobs, err := s.st.ListCIJobs(ctx, storage.CIJobListFilter{RunnerID: runner.ID, Limit: int(req.GetLimit())})
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list runner jobs: %v", err))
	}
	resp := &civ1.ListRunnerJobsResponse{}
	for _, job := range jobs {
		resp.Jobs = append(resp.Jobs, ciJobToProto(job))
	}
	return resp, nil
}

func (s *server) ListQueuedJobs(ctx context.Context, req *civ1.ListQueuedJobsRequest) (*civ1.ListQueuedJobsResponse, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	jobs, err := s.st.ListCIJobs(ctx, storage.CIJobListFilter{Pool: strings.TrimSpace(req.GetPool()), Status: "queued", Limit: int(req.GetLimit())})
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list queued jobs: %v", err))
	}
	resp := &civ1.ListQueuedJobsResponse{}
	for _, job := range jobs {
		run, err := s.st.GetCIRun(ctx, job.RunID)
		if err != nil || run.HomeID != identity.Username {
			continue
		}
		resp.Jobs = append(resp.Jobs, ciJobToProto(job))
	}
	return resp, nil
}

func (s *server) RegisterRunner(ctx context.Context, req *civ1.RegisterRunnerRequest) (*civ1.RegisterRunnerResponse, error) {
	regToken := strings.TrimSpace(req.GetRegistrationToken())
	if regToken == "" {
		return nil, status.Error(codes.InvalidArgument, "registration_token is required")
	}
	now := time.Now()
	consumed, err := s.st.ConsumeCIRunnerRegistrationToken(ctx, hashRunnerToken(regToken), now)
	if err != nil {
		return nil, ciStorageError(err, "registration token not found or expired")
	}
	runnerToken, err := randomToken("gsrunner")
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to generate runner credential: %v", err))
	}
	runnerID := "ci_runner_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	executor := strings.TrimSpace(req.GetExecutor())
	if executor == "" {
		executor = "shell"
	}
	labels := append([]string(nil), consumed.Labels...)
	if len(req.GetLabels()) > 0 {
		labels = append(labels, req.GetLabels()...)
	}
	if err := s.st.CreateCIRunner(ctx, &storage.CIRunner{
		ID:         runnerID,
		HomeID:     consumed.HomeID,
		Name:       consumed.Name,
		Pool:       consumed.Pool,
		Labels:     uniqueStrings(labels),
		Executor:   executor,
		Status:     "idle",
		TokenHash:  hashRunnerToken(runnerToken),
		Version:    strings.TrimSpace(req.GetVersion()),
		LastSeenAt: &now,
		CreatedAt:  now,
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to register runner: %v", err))
	}
	return &civ1.RegisterRunnerResponse{RunnerId: runnerID, RunnerToken: runnerToken, Pool: consumed.Pool}, nil
}

func (s *server) Heartbeat(ctx context.Context, req *civ1.HeartbeatRequest) (*civ1.HeartbeatResponse, error) {
	runner, err := s.requireRunner(ctx, req.GetRunnerId())
	if err != nil {
		return nil, err
	}
	if runner.Status == "disabled" || runner.Status == "revoked" {
		return &civ1.HeartbeatResponse{Status: runner.Status, PollAfterSeconds: 30}, nil
	}
	statusValue := strings.TrimSpace(req.GetStatus())
	if statusValue == "" {
		statusValue = runner.Status
	}
	now := time.Now()
	if err := s.st.UpdateCIRunnerStatus(ctx, runner.ID, statusValue, &now); err != nil {
		return nil, ciStorageError(err, "runner not found")
	}
	return &civ1.HeartbeatResponse{Status: statusValue, PollAfterSeconds: 5}, nil
}

func (s *server) PollJobs(ctx context.Context, req *civ1.PollJobsRequest) (*civ1.PollJobsResponse, error) {
	runner, err := s.requireRunner(ctx, req.GetRunnerId())
	if err != nil {
		return nil, err
	}
	if runner.Status == "disabled" || runner.Status == "revoked" {
		return &civ1.PollJobsResponse{}, nil
	}
	limit := int(req.GetMaxJobs())
	if limit <= 0 {
		limit = 1
	}
	if limit > 10 {
		limit = 10
	}
	now := time.Now()
	_ = s.st.UpdateCIRunnerStatus(ctx, runner.ID, "idle", &now)
	jobs, err := s.st.ListCIJobs(ctx, storage.CIJobListFilter{Pool: runner.Pool, Status: "queued", Limit: 1000})
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to poll queued jobs: %v", err))
	}
	resp := &civ1.PollJobsResponse{}
	for _, job := range jobs {
		run, err := s.st.GetCIRun(ctx, job.RunID)
		if err != nil || run.HomeID != runner.HomeID {
			continue
		}
		if !s.runnerCanRunJob(ctx, runner, job, run) {
			continue
		}
		if !s.jobDependenciesPassed(ctx, job) {
			continue
		}
		resp.Jobs = append(resp.Jobs, ciJobToProto(job))
		if len(resp.Jobs) >= limit {
			break
		}
	}
	return resp, nil
}

func (s *server) ClaimJob(ctx context.Context, req *civ1.ClaimJobRequest) (*civ1.ClaimJobResponse, error) {
	runner, err := s.requireRunner(ctx, req.GetRunnerId())
	if err != nil {
		return nil, err
	}
	job, err := s.st.GetCIJob(ctx, req.GetJobId())
	if err != nil {
		return nil, ciStorageError(err, "job not found")
	}
	run, err := s.st.GetCIRun(ctx, job.RunID)
	if err != nil {
		return nil, ciStorageError(err, "CI run not found")
	}
	if run.HomeID != runner.HomeID || job.RunnerPool != runner.Pool {
		return nil, status.Error(codes.PermissionDenied, "runner is not allowed to claim this job")
	}
	if !s.runnerCanRunJob(ctx, runner, job, run) {
		return nil, status.Error(codes.FailedPrecondition, "runner executor is not compatible with this job")
	}
	if !s.jobDependenciesPassed(ctx, job) {
		return nil, status.Error(codes.FailedPrecondition, "job dependencies have not passed")
	}
	leaseID, err := randomToken("lease")
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to generate lease: %v", err))
	}
	now := time.Now()
	expiresAt := now.Add(10 * time.Minute)
	if _, err := s.st.ClaimCIJob(ctx, job.ID, runner.ID, leaseID, expiresAt, now); err != nil {
		return nil, ciStorageError(err, "job not found or already claimed")
	}
	return &civ1.ClaimJobResponse{JobId: job.ID, LeaseId: leaseID, LeaseExpiresAt: formatTime(expiresAt)}, nil
}

func (s *server) GetJobPayload(ctx context.Context, req *civ1.GetJobPayloadRequest) (*civ1.JobPayload, error) {
	runner, job, err := s.requireRunnerJobLease(ctx, req.GetJobId(), req.GetLeaseId())
	if err != nil {
		return nil, err
	}
	_ = runner
	run, err := s.st.GetCIRun(ctx, job.RunID)
	if err != nil {
		return nil, ciStorageError(err, "CI run not found")
	}
	steps, err := s.st.ListCISteps(ctx, job.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list job steps: %v", err))
	}
	commands := make([]string, 0, len(steps))
	for _, step := range steps {
		commands = append(commands, step.Command)
	}
	manifestDir := path.Dir(job.ManifestPath)
	if manifests, err := s.st.ListCIRunManifests(ctx, run.ID); err == nil {
		for _, manifest := range manifests {
			if manifest.ID == job.ManifestRunID || manifest.ManifestPath == job.ManifestPath {
				manifestDir = manifest.ManifestDir
				break
			}
		}
	}
	files, changedFiles, err := s.workspaceFilesForRun(ctx, run)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to materialize candidate tree: %v", err))
	}
	shell := strings.TrimSpace(job.Shell)
	if shell == "" {
		shell = "bash"
	}
	return &civ1.JobPayload{
		JobId:              job.ID,
		RunId:              run.ID,
		HomeId:             run.HomeID,
		CandidateTreeHash:  run.CandidateTreeHash,
		ManifestPath:       job.ManifestPath,
		ManifestDir:        manifestDir,
		WorkingDirectory:   job.WorkingDirectory,
		Image:              job.Image,
		Shell:              shell,
		Commands:           commands,
		ChangedFiles:       changedFiles,
		Files:              files,
		Env:                cloneStringMap(job.Env),
		CachePaths:         append([]string(nil), job.CachePaths...),
		Artifacts:          append([]string(nil), job.Artifacts...),
		TimeoutSeconds:     int32(job.TimeoutSeconds),
		ChangesetId:        run.ChangesetID,
		ChangesetVersionId: run.ChangesetVersionID,
	}, nil
}

func (s *server) AppendLog(ctx context.Context, req *civ1.AppendLogRequest) (*civ1.AppendLogResponse, error) {
	if _, _, err := s.requireRunnerJobLease(ctx, req.GetJobId(), req.GetLeaseId()); err != nil {
		return nil, err
	}
	streamName := strings.TrimSpace(req.GetStream())
	if streamName == "" {
		streamName = "stdout"
	}
	job, _ := s.st.GetCIJob(ctx, req.GetJobId())
	chunk := &storage.CILogChunk{
		ID:         strings.TrimSpace(req.GetJobId()) + ":" + strconv.FormatInt(req.GetChunkIndex(), 10),
		JobID:      strings.TrimSpace(req.GetJobId()),
		ChunkIndex: req.GetChunkIndex(),
		Stream:     streamName,
		Payload:    append([]byte(nil), req.GetPayload()...),
		ByteCount:  int64(len(req.GetPayload())),
		CreatedAt:  time.Now(),
	}
	if job != nil {
		chunk.RunID = job.RunID
	}
	if err := s.st.AppendCILogChunk(ctx, chunk); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to append log chunk: %v", err))
	}
	return &civ1.AppendLogResponse{AcknowledgedChunkIndex: req.GetChunkIndex()}, nil
}

func (s *server) CompleteStep(ctx context.Context, req *civ1.CompleteStepRequest) (*civ1.CompleteStepResponse, error) {
	if _, _, err := s.requireRunnerJobLease(ctx, req.GetJobId(), req.GetLeaseId()); err != nil {
		return nil, err
	}
	now := time.Now()
	stepIndex := int(req.GetStepIndex())
	steps, _ := s.st.ListCISteps(ctx, req.GetJobId())
	var startedAt *time.Time
	for _, step := range steps {
		if step.StepIndex == stepIndex {
			startedAt = step.StartedAt
			break
		}
	}
	if startedAt == nil {
		startedAt = &now
	}
	statusValue := strings.TrimSpace(req.GetStatus())
	if statusValue == "" {
		statusValue = "passed"
	}
	if err := s.st.UpdateCIStepStatus(ctx, req.GetJobId(), stepIndex, statusValue, int(req.GetExitCode()), startedAt, &now); err != nil {
		return nil, ciStorageError(err, "step not found")
	}
	return &civ1.CompleteStepResponse{Status: statusValue}, nil
}

func (s *server) CompleteJob(ctx context.Context, req *civ1.CompleteJobRequest) (*civ1.CompleteJobResponse, error) {
	_, _, err := s.requireRunnerJobLease(ctx, req.GetJobId(), req.GetLeaseId())
	if err != nil {
		return nil, err
	}
	statusValue := strings.TrimSpace(req.GetStatus())
	if statusValue == "" {
		statusValue = "passed"
	}
	if statusValue != "passed" && statusValue != "failed" && statusValue != "cancelled" {
		return nil, status.Error(codes.InvalidArgument, "job status must be passed, failed, or cancelled")
	}
	finishedAt := time.Now()
	job, err := s.st.CompleteCIJob(ctx, req.GetJobId(), req.GetLeaseId(), statusValue, int(req.GetExitCode()), req.GetInfraFailure(), finishedAt)
	if err != nil {
		return nil, ciStorageError(err, "job not found")
	}
	if err := s.updateCheckAndRunAfterJob(ctx, job, finishedAt); err != nil {
		return nil, err
	}
	return &civ1.CompleteJobResponse{Status: statusValue}, nil
}

func (s *server) UploadArtifact(ctx context.Context, req *civ1.UploadArtifactRequest) (*civ1.UploadArtifactResponse, error) {
	_, job, err := s.requireRunnerJobLease(ctx, req.GetJobId(), req.GetLeaseId())
	if err != nil {
		return nil, err
	}
	if len(req.GetPayload()) > maxCIArtifactPayloadBytes {
		return nil, status.Errorf(codes.InvalidArgument, "artifact payload exceeds %d bytes", maxCIArtifactPayloadBytes)
	}
	artifactPath, err := validateCIArtifactPath(job, req.GetPath())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	artifactID := "ci_artifact_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	objectKey := strings.Join([]string{"ci_artifacts", job.RunID, job.ID, artifactID, strings.TrimPrefix(artifactPath, "/")}, "/")
	artifact := &storage.CIArtifact{
		ID:        artifactID,
		JobID:     job.ID,
		RunID:     job.RunID,
		Path:      artifactPath,
		ObjectKey: objectKey,
		Payload:   append([]byte(nil), req.GetPayload()...),
		ByteCount: int64(len(req.GetPayload())),
		CreatedAt: time.Now(),
	}
	if err := s.st.CreateCIArtifact(ctx, artifact); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to store artifact: %v", err))
	}
	return &civ1.UploadArtifactResponse{ArtifactId: artifactID, ObjectKey: objectKey}, nil
}

func validateCIArtifactPath(job *storage.CIJob, rawPath string) (string, error) {
	if job == nil {
		return "", fmt.Errorf("job is required")
	}
	if len(job.Artifacts) == 0 {
		return "", fmt.Errorf("job does not declare artifacts")
	}
	artifactPath, err := ciinternal.NormalizeHomePath(rawPath)
	if err != nil {
		return "", err
	}
	if artifactPath == "/" {
		return "", fmt.Errorf("artifact path must not be the home root")
	}
	for _, pattern := range job.Artifacts {
		if ciinternal.MatchHomePattern(pattern, artifactPath) {
			return artifactPath, nil
		}
	}
	return "", fmt.Errorf("artifact path %s is not declared by this job", artifactPath)
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (s *server) loadAuthorizedRunner(ctx context.Context, username string, runnerID string) (*storage.CIRunner, error) {
	if strings.TrimSpace(runnerID) == "" {
		return nil, status.Error(codes.InvalidArgument, "runner_id is required")
	}
	runner, err := s.st.GetCIRunner(ctx, strings.TrimSpace(runnerID))
	if err != nil {
		return nil, ciStorageError(err, "runner not found")
	}
	if runner.HomeID != strings.TrimSpace(username) {
		return nil, status.Error(codes.PermissionDenied, "not allowed to manage this runner")
	}
	return runner, nil
}

func (s *server) requireRunner(ctx context.Context, runnerID string) (*storage.CIRunner, error) {
	token := auth.TokenFromGRPCContext(ctx)
	if strings.TrimSpace(token) == "" {
		return nil, status.Error(codes.Unauthenticated, "runner token required")
	}
	runner, err := s.st.GetCIRunnerByTokenHash(ctx, hashRunnerToken(token))
	if err != nil {
		return nil, ciStorageError(err, "runner token is invalid")
	}
	if strings.TrimSpace(runnerID) != "" && runner.ID != strings.TrimSpace(runnerID) {
		return nil, status.Error(codes.PermissionDenied, "runner token does not match runner_id")
	}
	if runner.Status == "revoked" || strings.TrimSpace(runner.TokenHash) == "" {
		return nil, status.Error(codes.PermissionDenied, "runner credential is revoked")
	}
	return runner, nil
}

func (s *server) requireRunnerJobLease(ctx context.Context, jobID string, leaseID string) (*storage.CIRunner, *storage.CIJob, error) {
	job, err := s.st.GetCIJob(ctx, strings.TrimSpace(jobID))
	if err != nil {
		return nil, nil, ciStorageError(err, "job not found")
	}
	runner, err := s.requireRunner(ctx, job.RunnerID)
	if err != nil {
		return nil, nil, err
	}
	if job.RunnerID != runner.ID {
		return nil, nil, status.Error(codes.PermissionDenied, "runner does not own this job")
	}
	if strings.TrimSpace(leaseID) == "" || job.LeaseID != strings.TrimSpace(leaseID) {
		return nil, nil, status.Error(codes.PermissionDenied, "job lease is invalid")
	}
	if job.Status != "running" {
		return nil, nil, status.Error(codes.FailedPrecondition, "job is not running")
	}
	if job.LeaseExpiresAt != nil && job.LeaseExpiresAt.Before(time.Now()) {
		return nil, nil, status.Error(codes.FailedPrecondition, "job lease expired")
	}
	return runner, job, nil
}

func (s *server) loadPlatformConfigForHome(ctx context.Context, homeID string) (*ciinternal.PlatformConfig, error) {
	storedPath := logicalToStoragePath(homeID, ciinternal.PlatformConfigPath)
	content, err := storage.ReadSliceFileContent(ctx, s.st, homeslice.IDForUsername(homeID), storedPath)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) || errors.Is(err, storage.ErrSliceNotFound) {
			return defaultPlatformConfig(), nil
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to read CI platform config: %v", err))
	}
	cfg, err := ciinternal.ParsePlatformConfig(content.Content)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("invalid CI platform config: %v", err))
	}
	return cfg, nil
}

func defaultPlatformConfig() *ciinternal.PlatformConfig {
	return &ciinternal.PlatformConfig{
		Version: 1,
		Defaults: ciinternal.JobDefaults{
			RunnerPool: "default",
			Shell:      "bash",
		},
		RunnerPools: map[string]ciinternal.RunnerPool{
			"default": {Executor: "shell", MaxParallelJobsPerRunner: 1},
		},
	}
}

func normalizedRunnerPools(platform *ciinternal.PlatformConfig) map[string]ciinternal.RunnerPool {
	pools := make(map[string]ciinternal.RunnerPool)
	if platform != nil {
		for name, pool := range platform.RunnerPools {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if strings.TrimSpace(pool.Executor) == "" {
				pool.Executor = "shell"
			}
			if pool.MaxParallelJobsPerRunner <= 0 {
				pool.MaxParallelJobsPerRunner = 1
			}
			pools[name] = pool
		}
	}
	if len(pools) == 0 {
		pools["default"] = ciinternal.RunnerPool{Executor: "shell", MaxParallelJobsPerRunner: 1}
	}
	return pools
}

func (s *server) runnerCanRunJob(ctx context.Context, runner *storage.CIRunner, job *storage.CIJob, run *storage.CIRun) bool {
	if runner == nil || job == nil || run == nil {
		return false
	}
	platform, err := s.loadPlatformConfigForHome(ctx, run.HomeID)
	if err != nil {
		return false
	}
	pool, ok := normalizedRunnerPools(platform)[job.RunnerPool]
	if !ok {
		return false
	}
	expectedExecutor := strings.TrimSpace(pool.Executor)
	if expectedExecutor == "" {
		expectedExecutor = "shell"
	}
	actualExecutor := strings.TrimSpace(runner.Executor)
	if actualExecutor == "" {
		actualExecutor = "shell"
	}
	return actualExecutor == expectedExecutor
}

func (s *server) jobDependenciesPassed(ctx context.Context, job *storage.CIJob) bool {
	for _, dependencyID := range job.DependsOnJobIDs {
		dependency, err := s.st.GetCIJob(ctx, dependencyID)
		if err != nil || dependency.Status != "passed" {
			return false
		}
	}
	return true
}

func (s *server) updateCheckAndRunAfterJob(ctx context.Context, job *storage.CIJob, finishedAt time.Time) error {
	run, err := s.st.GetCIRun(ctx, job.RunID)
	if err != nil {
		return ciStorageError(err, "CI run not found")
	}
	if job.Required {
		if err := s.st.UpsertCICheck(ctx, &storage.CICheck{
			ChangesetID:        run.ChangesetID,
			ChangesetVersionID: run.ChangesetVersionID,
			PlanHash:           run.PlanHash,
			ManifestPath:       job.ManifestPath,
			JobKey:             job.JobKey,
			CheckName:          job.CheckName,
			Required:           true,
			Status:             job.Status,
			RunID:              run.ID,
			UpdatedAt:          finishedAt,
		}); err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("failed to update CI check: %v", err))
		}
	}
	if run.Status == "cancelled" || run.Status == "superseded" {
		return nil
	}
	if job.Status != "passed" {
		if err := s.skipJobsBlockedByFailure(ctx, run, finishedAt); err != nil {
			return err
		}
	}
	jobs, err := s.st.ListCIJobs(ctx, storage.CIJobListFilter{RunID: run.ID, Limit: 1000})
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to list CI jobs: %v", err))
	}
	allTerminal := true
	anyFailed := false
	for _, candidate := range jobs {
		if !terminalCIJobStatus(candidate.Status) {
			allTerminal = false
			break
		}
		if candidate.Status != "passed" {
			anyFailed = true
		}
	}
	if allTerminal {
		runStatus := "passed"
		if anyFailed {
			runStatus = "failed"
		}
		if err := s.st.UpdateCIRunStatus(ctx, run.ID, runStatus, &finishedAt); err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("failed to update CI run: %v", err))
		}
	}
	return nil
}

func (s *server) supersedeOlderRuns(ctx context.Context, changesetID string, currentVersionID string, currentRunID string, now time.Time) error {
	for _, statusValue := range []string{"queued", "running"} {
		runs, err := s.st.ListCIRuns(ctx, storage.CIRunListFilter{ChangesetID: changesetID, Status: statusValue, Limit: 1000})
		if err != nil {
			return err
		}
		for _, run := range runs {
			if run == nil || run.ID == currentRunID || run.ChangesetVersionID == currentVersionID {
				continue
			}
			nextStatus := "cancelled"
			if run.Status == "running" {
				nextStatus = "superseded"
			}
			if err := s.st.UpdateCIRunStatus(ctx, run.ID, nextStatus, &now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *server) skipJobsBlockedByFailure(ctx context.Context, run *storage.CIRun, finishedAt time.Time) error {
	for {
		jobs, err := s.st.ListCIJobs(ctx, storage.CIJobListFilter{RunID: run.ID, Limit: 1000})
		if err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("failed to list CI jobs: %v", err))
		}
		byID := make(map[string]*storage.CIJob, len(jobs))
		for _, job := range jobs {
			byID[job.ID] = job
		}
		changed := false
		for _, job := range jobs {
			if job.Status != "queued" {
				continue
			}
			for _, dependencyID := range job.DependsOnJobIDs {
				dependency := byID[dependencyID]
				if dependency == nil || dependency.Status == "passed" || !terminalCIJobStatus(dependency.Status) {
					continue
				}
				skipped, err := s.st.CompleteCIJob(ctx, job.ID, "", "skipped", 0, false, finishedAt)
				if err != nil {
					return status.Error(codes.Internal, fmt.Sprintf("failed to skip blocked CI job: %v", err))
				}
				if skipped.Required {
					if err := s.st.UpsertCICheck(ctx, &storage.CICheck{
						ChangesetID:        run.ChangesetID,
						ChangesetVersionID: run.ChangesetVersionID,
						PlanHash:           run.PlanHash,
						ManifestPath:       skipped.ManifestPath,
						JobKey:             skipped.JobKey,
						CheckName:          skipped.CheckName,
						Required:           true,
						Status:             skipped.Status,
						RunID:              run.ID,
						UpdatedAt:          finishedAt,
					}); err != nil {
						return status.Error(codes.Internal, fmt.Sprintf("failed to update skipped CI check: %v", err))
					}
				}
				changed = true
				break
			}
		}
		if !changed {
			return nil
		}
	}
}

func terminalCIJobStatus(statusValue string) bool {
	switch statusValue {
	case "passed", "failed", "cancelled", "skipped":
		return true
	default:
		return false
	}
}

func (s *server) workspaceFilesForRun(ctx context.Context, run *storage.CIRun) ([]*civ1.WorkspaceFile, []string, error) {
	cs, err := s.st.GetChangeset(ctx, run.ChangesetID)
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := resolveChangesetSnapshot(ctx, s.st, cs, run.ChangesetVersionID)
	if err != nil {
		return nil, nil, err
	}
	byPath := make(map[string]*civ1.WorkspaceFile)
	prefix := strings.Trim(strings.TrimSpace(run.HomeID), "/")
	if prefix != "" {
		prefix += "/"
	}
	if strings.TrimSpace(run.BaseCommitHash) != "" {
		basePaths, err := s.st.ListFilesAtCommit(ctx, run.BaseCommitHash, prefix)
		if err != nil && !errors.Is(err, storage.ErrCommitNotFound) {
			return nil, nil, err
		}
		for _, storedPath := range basePaths {
			content, err := s.st.GetFileAtCommit(ctx, run.BaseCommitHash, storedPath)
			if err != nil {
				return nil, nil, err
			}
			logical := storagePathToLogical(run.HomeID, storedPath)
			byPath[logical] = &civ1.WorkspaceFile{Path: logical, Content: append([]byte(nil), content.Content...)}
		}
	}
	for _, rawPath := range snapshot.ModifiedFiles {
		storedPath := common.CleanRelativePath(rawPath)
		logical := storagePathToLogical(run.HomeID, storedPath)
		hash := snapshot.FileHashes[storedPath]
		if hash == "" {
			hash = snapshot.FileHashes[logical]
		}
		if strings.TrimSpace(hash) == "" {
			delete(byPath, logical)
			continue
		}
		content, err := storage.ReadVersionedFileContent(ctx, s.st, hash)
		if err != nil {
			return nil, nil, err
		}
		byPath[logical] = &civ1.WorkspaceFile{Path: logical, Content: append([]byte(nil), content.Content...)}
	}
	paths := make([]string, 0, len(byPath))
	for filePath := range byPath {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	files := make([]*civ1.WorkspaceFile, 0, len(paths))
	for _, filePath := range paths {
		files = append(files, byPath[filePath])
	}
	return files, logicalChangedPaths(run.HomeID, snapshot.ModifiedFiles), nil
}

func ciRunnerToProto(runner *storage.CIRunner) *civ1.Runner {
	if runner == nil {
		return nil
	}
	return &civ1.Runner{
		RunnerId:   runner.ID,
		HomeId:     runner.HomeID,
		Name:       runner.Name,
		Pool:       runner.Pool,
		Labels:     append([]string(nil), runner.Labels...),
		Executor:   runner.Executor,
		Version:    runner.Version,
		Status:     runner.Status,
		LastSeenAt: formatTimePtr(runner.LastSeenAt),
	}
}

func randomToken(prefix string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func hashRunnerToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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
			Shell:            job.Shell,
			WorkingDirectory: job.WorkingDirectory,
			TimeoutSeconds:   job.TimeoutSeconds,
			Env:              cloneStringMap(job.Env),
			CachePaths:       append([]string(nil), job.CachePaths...),
			Artifacts:        append([]string(nil), job.Artifacts...),
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
	if errors.Is(err, storage.ErrPermissionDenied) {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	if errors.Is(err, storage.ErrInvalidInput) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}
