package ci

import (
	"context"

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

func (s *server) StartRun(context.Context, *civ1.StartRunRequest) (*civ1.StartRunResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) GetRun(context.Context, *civ1.GetRunRequest) (*civ1.Run, error) {
	return nil, ciNotImplemented()
}

func (s *server) ListRuns(context.Context, *civ1.ListRunsRequest) (*civ1.ListRunsResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) CancelRun(context.Context, *civ1.CancelRunRequest) (*civ1.CancelRunResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) Rerun(context.Context, *civ1.RerunRequest) (*civ1.StartRunResponse, error) {
	return nil, ciNotImplemented()
}

func (s *server) StreamLogs(*civ1.StreamLogsRequest, civ1.CIService_StreamLogsServer) error {
	return ciNotImplemented()
}

func (s *server) ListChecks(context.Context, *civ1.ListChecksRequest) (*civ1.ListChecksResponse, error) {
	return nil, ciNotImplemented()
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
