package storage

import (
	"context"
	"errors"

	"github.com/niczy/gitslice/internal/models"
)

var (
	ErrSliceNotFound        = errors.New("slice not found")
	ErrSliceAlreadyExists   = errors.New("slice already exists")
	ErrInvalidInput         = errors.New("invalid input")
	ErrChangesetNotFound    = errors.New("changeset not found")
	ErrEntryNotFound        = errors.New("entry not found")
	ErrEntryExists          = errors.New("entry already exists")
	ErrLockHeld             = errors.New("resource locked")
	ErrCommitNotFound       = errors.New("commit not found")
	ErrSliceFilesImmutable  = errors.New("slice files are immutable")
	ErrAgentSessionNotFound = errors.New("agent session not found")
	ErrAgentSessionConflict = errors.New("agent session conflict")
)

// Storage defines the interface for data storage operations.
// This allows us to swap implementations (in-memory, PostgreSQL, etc.).
type Storage interface {
	// Slice operations
	CreateSlice(ctx context.Context, slice *models.Slice) error
	GetSlice(ctx context.Context, sliceID string) (*models.Slice, error)
	ListSlices(ctx context.Context, limit, offset int) ([]*models.Slice, error)
	CountSlices(ctx context.Context) (int, error)
	ListSlicesByOwner(ctx context.Context, owner string, limit, offset int) ([]*models.Slice, error)
	SearchSlices(ctx context.Context, query string, limit, offset int) ([]*models.Slice, error)
	GetSliceMetadata(ctx context.Context, sliceID string) (*models.SliceMetadata, error)
	UpdateSliceMetadata(ctx context.Context, sliceID string, metadata *models.SliceMetadata) error
	SetSliceFiles(ctx context.Context, sliceID string, files []string) error
	UpdateSliceName(ctx context.Context, sliceID, newName string) error
	UpdateSliceEnvironment(ctx context.Context, sliceID, environment string) error
	GetSliceByName(ctx context.Context, name string) (*models.Slice, error)
	GetRootSlice(ctx context.Context) (*models.Slice, error)
	InitializeRootSlice(ctx context.Context) error
	AddSliceCommit(ctx context.Context, sliceID string, commit *models.Commit) error
	ListSliceCommits(ctx context.Context, sliceID string, limit int, fromCommitHash string) ([]*models.Commit, error)
	GetCommitByHash(ctx context.Context, sliceID, commitHash string) (*models.Commit, error)

	// File indexing
	AddFileToSlice(ctx context.Context, fileID, sliceID string) error
	GetActiveSlicesForFile(ctx context.Context, fileID string) ([]string, error)
	RemoveFileFromSlice(ctx context.Context, fileID, sliceID string) error
	ListConflicts(ctx context.Context) ([]*models.FileConflict, error)
	ResolveConflict(ctx context.Context, fileID, preferredSliceID string) (*models.FileConflict, error)
	LockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) error
	UnlockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string)

	// Index maintenance
	RebuildIndexes(ctx context.Context) error

	// Changesets
	CreateChangeset(ctx context.Context, changeset *models.Changeset) error
	GetChangeset(ctx context.Context, changesetID string) (*models.Changeset, error)
	ListChangesets(ctx context.Context, sliceID string, status *models.ChangesetStatus, limit int) ([]*models.Changeset, error)
	UpdateChangeset(ctx context.Context, changeset *models.Changeset) error

	// File content for checkout
	GetSliceFiles(ctx context.Context, sliceID string) ([]*models.FileContent, error)
	GetSliceFileByPath(ctx context.Context, sliceID, path string) (*models.FileContent, error)
	AddFileContent(ctx context.Context, content *models.FileContent) error

	// Directory entries
	AddEntry(ctx context.Context, entry *models.DirectoryEntry) error
	GetEntry(ctx context.Context, entryID string) (*models.DirectoryEntry, error)
	GetEntryByPath(ctx context.Context, sliceID, path string) (*models.DirectoryEntry, error)
	ListEntries(ctx context.Context, sliceID, parentID string) ([]*models.DirectoryEntry, error)
	UpdateEntry(ctx context.Context, entry *models.DirectoryEntry) error
	DeleteEntry(ctx context.Context, entryID string) error

	// Global state
	GetGlobalState(ctx context.Context) (*models.GlobalState, error)
	UpdateGlobalState(ctx context.Context, state *models.GlobalState) error

	// Health check
	Ping(ctx context.Context) error

	// Commit snapshot operations for versioned file access
	GetCommitSnapshot(ctx context.Context, commitHash string) (*models.CommitSnapshot, error)
	SaveCommitSnapshot(ctx context.Context, snapshot *models.CommitSnapshot) error
	GetFileContentByHash(ctx context.Context, contentHash string) (*models.FileContent, error)
	GetFileAtCommit(ctx context.Context, commitHash, path string) (*models.FileContent, error)
	ListFilesAtCommit(ctx context.Context, commitHash, pathPrefix string) ([]string, error)

	// File change history operations
	// AddFileChange records a file change associated with a commit
	AddFileChange(ctx context.Context, change *models.FileChangeRecord) error

	// AddFileChanges records multiple file changes in a batch (for efficiency)
	AddFileChanges(ctx context.Context, changes []*models.FileChangeRecord) error

	// GetFileHistory retrieves the change history for a specific file path
	GetFileHistory(ctx context.Context, sliceID, path string, limit int, fromCommit string) ([]*models.FileChangeRecord, error)

	// GetDirectoryHistory retrieves change history for all files under a directory
	GetDirectoryHistory(ctx context.Context, sliceID, pathPrefix string, limit int, fromCommit string) ([]*models.FileChangeRecord, error)

	// GetCommitChanges retrieves all file changes made in a specific commit
	GetCommitChanges(ctx context.Context, commitHash string) ([]*models.FileChangeRecord, error)

	// QueryFileHistory performs a flexible query on file change history
	QueryFileHistory(ctx context.Context, query *models.FileHistoryQuery) (*models.FileHistoryResult, error)

	// GetDirectorySummary gets an aggregated summary of changes for a directory
	GetDirectorySummary(ctx context.Context, sliceID, pathPrefix string) (*models.DirectoryChangeSummary, error)

	// Accounts / Organizations (fake auth: identity is a username).
	EnsureUser(ctx context.Context, username string) (*models.User, error)
	GetUser(ctx context.Context, username string) (*models.User, error)

	CreateOrganization(ctx context.Context, org *models.Organization) error
	GetOrganization(ctx context.Context, orgSlug string) (*models.Organization, error)
	AddOrganizationMember(ctx context.Context, member *models.OrganizationMember) error
	ListOrganizationsForUser(ctx context.Context, username string) ([]*models.Organization, error)

	// Environment registry
	CreateEnvironment(ctx context.Context, env *models.Environment) error
	GetEnvironment(ctx context.Context, name string) (*models.Environment, error)
	ListEnvironments(ctx context.Context, limit, offset int) ([]*models.Environment, error)
	UpdateEnvironment(ctx context.Context, env *models.Environment) error
	DeleteEnvironment(ctx context.Context, name string) error

	// Agent sessions
	CreateAgentSession(ctx context.Context, session *models.AgentSession) error
	GetAgentSession(ctx context.Context, sessionID string) (*models.AgentSession, error)
	GetActiveAgentSessionBySlice(ctx context.Context, sliceID string) (*models.AgentSession, error)
	ListAgentSessionsByState(ctx context.Context, states []models.AgentSessionState, limit int) ([]*models.AgentSession, error)
	UpdateAgentSession(ctx context.Context, session *models.AgentSession) error
	AppendAgentSessionEvent(ctx context.Context, event *models.AgentSessionEvent) error
	ListAgentSessionEvents(ctx context.Context, sessionID string, sinceSeq uint64, limit int) ([]*models.AgentSessionEvent, error)
	AddAgentSessionAudit(ctx context.Context, audit *models.AgentSessionAudit) error
}
