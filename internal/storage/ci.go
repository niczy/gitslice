package storage

import (
	"context"
	"time"
)

// CIStore is the storage contract for path-scoped CI. Implementations are added
// incrementally after the schema and API surface are in place.
type CIStore interface {
	CreateCIPlan(ctx context.Context, plan *CIPlan) error
	GetCIRun(ctx context.Context, runID string) (*CIRun, error)
	ListCIRuns(ctx context.Context, filter CIRunListFilter) ([]*CIRun, error)
	UpdateCIRunStatus(ctx context.Context, runID string, status string, finishedAt *time.Time) error
	ListCIRunManifests(ctx context.Context, runID string) ([]*CIRunManifest, error)
	GetCIJob(ctx context.Context, jobID string) (*CIJob, error)
	ListCIJobs(ctx context.Context, filter CIJobListFilter) ([]*CIJob, error)
	ListCISteps(ctx context.Context, jobID string) ([]*CIStep, error)
	ClaimCIJob(ctx context.Context, jobID string, runnerID string, leaseID string, leaseExpiresAt time.Time, startedAt time.Time) (*CIJob, error)
	UpdateCIStepStatus(ctx context.Context, jobID string, stepIndex int, status string, exitCode int, startedAt *time.Time, finishedAt *time.Time) error
	AppendCILogChunk(ctx context.Context, chunk *CILogChunk) error
	CreateCIArtifact(ctx context.Context, artifact *CIArtifact) error
	ListCIArtifacts(ctx context.Context, filter CIArtifactListFilter) ([]*CIArtifact, error)
	CompleteCIJob(ctx context.Context, jobID string, leaseID string, status string, exitCode int, infraFailure bool, finishedAt time.Time) (*CIJob, error)

	UpsertCICheck(ctx context.Context, check *CICheck) error
	ListCIChecks(ctx context.Context, changesetID string, changesetVersionID string, planHash string) ([]*CICheck, error)
	ListCILogChunks(ctx context.Context, filter CILogChunkListFilter) ([]*CILogChunk, error)

	CreateCIRunnerRegistrationToken(ctx context.Context, token *CIRunnerRegistrationToken) error
	ConsumeCIRunnerRegistrationToken(ctx context.Context, tokenHash string, usedAt time.Time) (*CIRunnerRegistrationToken, error)
	CreateCIRunner(ctx context.Context, runner *CIRunner) error
	GetCIRunner(ctx context.Context, runnerID string) (*CIRunner, error)
	GetCIRunnerByTokenHash(ctx context.Context, tokenHash string) (*CIRunner, error)
	ListCIRunners(ctx context.Context, filter CIRunnerListFilter) ([]*CIRunner, error)
	UpdateCIRunnerStatus(ctx context.Context, runnerID string, status string, lastSeenAt *time.Time) error
	RevokeCIRunner(ctx context.Context, runnerID string, revokedAt time.Time) error
}

type CIPlan struct {
	Run       *CIRun
	Manifests []*CIRunManifest
	Jobs      []*CIJob
	Steps     []*CIStep
	Checks    []*CICheck
}

type CIRun struct {
	ID                 string
	HomeID             string
	SliceID            string
	ChangesetID        string
	ChangesetVersionID string
	BaseCommitHash     string
	CandidateTreeHash  string
	PlatformConfigHash string
	PlanHash           string
	Attempt            int
	TriggerEvent       string
	TriggeredByUserID  string
	Status             string
	CreatedAt          time.Time
	StartedAt          *time.Time
	FinishedAt         *time.Time
}

type CIRunListFilter struct {
	HomeID             string
	ChangesetID        string
	ChangesetVersionID string
	PlanHash           string
	Status             string
	Limit              int
}

type CIRunManifest struct {
	ID           string
	RunID        string
	ManifestPath string
	ManifestDir  string
	ManifestHash string
	MatchedPaths []string
	ParseStatus  string
	ParseError   string
}

type CIJob struct {
	ID               string
	RunID            string
	ManifestRunID    string
	ManifestPath     string
	JobKey           string
	CheckName        string
	Required         bool
	RunnerPool       string
	Image            string
	Shell            string
	WorkingDirectory string
	TimeoutSeconds   int
	Env              map[string]string
	CachePaths       []string
	Artifacts        []string
	Status           string
	RunnerID         string
	LeaseID          string
	LeaseExpiresAt   *time.Time
	ExitCode         int
	InfraFailure     bool
	StartedAt        *time.Time
	FinishedAt       *time.Time
	DependsOnJobIDs  []string
}

type CIJobListFilter struct {
	RunID    string
	RunnerID string
	Pool     string
	Status   string
	Limit    int
}

type CIStep struct {
	ID         string
	JobID      string
	StepIndex  int
	Command    string
	Status     string
	ExitCode   int
	StartedAt  *time.Time
	FinishedAt *time.Time
}

type CICheck struct {
	ChangesetID        string
	ChangesetVersionID string
	PlanHash           string
	ManifestPath       string
	JobKey             string
	CheckName          string
	Required           bool
	Status             string
	RunID              string
	UpdatedAt          time.Time
}

type CILogChunk struct {
	ID         string
	JobID      string
	RunID      string
	ChunkIndex int64
	Stream     string
	ObjectKey  string
	Payload    []byte
	ByteCount  int64
	CreatedAt  time.Time
}

type CILogChunkListFilter struct {
	RunID      string
	JobID      string
	SinceChunk int64
	Limit      int
}

type CIArtifact struct {
	ID        string
	JobID     string
	RunID     string
	Path      string
	ObjectKey string
	Payload   []byte
	ByteCount int64
	CreatedAt time.Time
}

type CIArtifactListFilter struct {
	RunID string
	JobID string
	Limit int
}

type CIRunner struct {
	ID         string
	HomeID     string
	Name       string
	Pool       string
	Labels     []string
	Executor   string
	Status     string
	TokenHash  string
	Version    string
	LastSeenAt *time.Time
	CreatedAt  time.Time
	DisabledAt *time.Time
}

type CIRunnerRegistrationToken struct {
	TokenHash       string
	HomeID          string
	Name            string
	Pool            string
	Labels          []string
	ExpiresAt       time.Time
	CreatedByUserID string
	CreatedAt       time.Time
	UsedAt          *time.Time
}

type CIRunnerListFilter struct {
	HomeID string
	Pool   string
	Status string
	Limit  int
}
