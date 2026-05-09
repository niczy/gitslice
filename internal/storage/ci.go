package storage

import (
	"context"
	"time"
)

// CIStore is the storage contract for path-scoped CI. Implementations are added
// incrementally after the schema and API surface are in place.
type CIStore interface {
	CreateCIRun(ctx context.Context, run *CIRun) error
	GetCIRun(ctx context.Context, runID string) (*CIRun, error)
	ListCIRuns(ctx context.Context, filter CIRunListFilter) ([]*CIRun, error)
	UpdateCIRunStatus(ctx context.Context, runID string, status string, finishedAt *time.Time) error

	UpsertCICheck(ctx context.Context, check *CICheck) error
	ListCIChecks(ctx context.Context, changesetID string, changesetVersionID string, planHash string) ([]*CICheck, error)

	CreateCIRunner(ctx context.Context, runner *CIRunner) error
	GetCIRunner(ctx context.Context, runnerID string) (*CIRunner, error)
	ListCIRunners(ctx context.Context, filter CIRunnerListFilter) ([]*CIRunner, error)
	UpdateCIRunnerStatus(ctx context.Context, runnerID string, status string, lastSeenAt *time.Time) error
	RevokeCIRunner(ctx context.Context, runnerID string, revokedAt time.Time) error
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
	Status             string
	Limit              int
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

type CIRunner struct {
	ID         string
	HomeID     string
	Name       string
	Pool       string
	Labels     []string
	Status     string
	TokenHash  string
	Version    string
	LastSeenAt *time.Time
	CreatedAt  time.Time
	DisabledAt *time.Time
}

type CIRunnerListFilter struct {
	HomeID string
	Pool   string
	Status string
	Limit  int
}
