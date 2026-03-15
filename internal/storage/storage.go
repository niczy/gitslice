package storage

import (
	"context"
	"errors"
	"time"

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
	ErrRepoBindingNotFound  = errors.New("repo binding not found")
	ErrAgentSessionNotFound = errors.New("agent session not found")
	ErrAgentSessionConflict = errors.New("agent session conflict")
)

const (
	defaultSliceCommitListLimit = 100
	maxSliceCommitListLimit     = 10000
)

func normalizeSliceCommitLimit(limit int) int {
	if limit <= 0 {
		return defaultSliceCommitListLimit
	}
	if limit > maxSliceCommitListLimit {
		return maxSliceCommitListLimit
	}
	return limit
}

// Storage defines the interface for data storage operations.
// This allows us to swap implementations (in-memory, PostgreSQL, etc.).
type Storage interface {
	// Slice operations
	CreateSlice(ctx context.Context, slice *models.Slice) error
	DeleteSlice(ctx context.Context, sliceID string) error
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
	GetSliceBySlug(ctx context.Context, slug string) (*models.Slice, error)
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
	CreateChangesetSnapshot(ctx context.Context, snapshot *models.ChangesetSnapshot) error
	GetChangesetSnapshot(ctx context.Context, changesetID string, version int32) (*models.ChangesetSnapshot, error)
	ListChangesetSnapshots(ctx context.Context, changesetID string, limit int) ([]*models.ChangesetSnapshot, error)

	// Block-backed file content storage
	PutBlock(ctx context.Context, hash string, data []byte) error
	GetBlock(ctx context.Context, hash string) ([]byte, error)
	HasBlock(ctx context.Context, hash string) (bool, error)
	PutBlocks(ctx context.Context, blocks map[string][]byte) error
	PutFileManifest(ctx context.Context, sliceID, path string, manifest *models.FileManifest) error
	GetFileManifest(ctx context.Context, sliceID, path string) (*models.FileManifest, error)
	DeleteFileManifest(ctx context.Context, sliceID, path string) error
	PutVersionedFileManifest(ctx context.Context, manifest *models.FileManifest) error
	GetVersionedFileManifest(ctx context.Context, hash string) (*models.FileManifest, error)

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
	GetUserByEmail(ctx context.Context, email string) (*models.User, error)
	ListUsers(ctx context.Context, limit, offset int) ([]*models.User, error)
	CreateUser(ctx context.Context, user *models.User) error
	UpdateUser(ctx context.Context, user *models.User) error
	DeleteUser(ctx context.Context, username string) error
	PutRepoBinding(ctx context.Context, binding *models.RepoBinding) error
	GetRepoBinding(ctx context.Context, sliceID, rootPath string) (*models.RepoBinding, error)
	ListRepoBindingsByOwner(ctx context.Context, username string) ([]*models.RepoBinding, error)
	DeleteRepoBinding(ctx context.Context, sliceID, rootPath string) error
	CreateAuthSession(ctx context.Context, session *models.AuthSession) error
	GetAuthSession(ctx context.Context, sessionID string) (*models.AuthSession, error)
	GetAuthSessionByToken(ctx context.Context, token string) (*models.AuthSession, error)
	GetAuthSessionByRefreshToken(ctx context.Context, refreshToken string) (*models.AuthSession, error)
	ListAuthSessionsByUser(ctx context.Context, username string) ([]*models.AuthSession, error)
	UpdateAuthSessionTokens(ctx context.Context, sessionID, accessToken string, accessTokenExpiresAt *time.Time, refreshToken string, refreshTokenExpiresAt *time.Time) error
	TouchAuthSession(ctx context.Context, sessionID string, at time.Time) error
	RevokeAuthSession(ctx context.Context, username, sessionID string) error
	RevokeAuthSessionByToken(ctx context.Context, token string) error
	CreateDeviceAuthorization(ctx context.Context, authorization *models.DeviceAuthorization) error
	GetDeviceAuthorizationByDeviceCode(ctx context.Context, deviceCode string) (*models.DeviceAuthorization, error)
	GetDeviceAuthorizationByUserCode(ctx context.Context, userCode string) (*models.DeviceAuthorization, error)
	UpdateDeviceAuthorization(ctx context.Context, authorization *models.DeviceAuthorization) error

	CreateOrganization(ctx context.Context, org *models.Organization) error
	GetOrganization(ctx context.Context, orgSlug string) (*models.Organization, error)
	UpdateOrganization(ctx context.Context, org *models.Organization) error
	DeleteOrganization(ctx context.Context, orgSlug string) error
	AddOrganizationMember(ctx context.Context, member *models.OrganizationMember) error
	GetOrganizationMember(ctx context.Context, orgSlug, username string) (*models.OrganizationMember, error)
	ListOrganizationMembers(ctx context.Context, orgSlug string) ([]*models.OrganizationMember, error)
	UpdateOrganizationMember(ctx context.Context, member *models.OrganizationMember) error
	RemoveOrganizationMember(ctx context.Context, orgSlug, username string) error
	CreateOrganizationInvite(ctx context.Context, invite *models.OrganizationInvite) error
	GetOrganizationInvite(ctx context.Context, orgSlug, inviteID string) (*models.OrganizationInvite, error)
	UpdateOrganizationInvite(ctx context.Context, invite *models.OrganizationInvite) error
	ListOrganizationsForUser(ctx context.Context, username string) ([]*models.Organization, error)
	CreateTeam(ctx context.Context, team *models.Team) error
	GetTeam(ctx context.Context, teamID string) (*models.Team, error)
	ListTeams(ctx context.Context, orgSlug string) ([]*models.Team, error)
	UpdateTeam(ctx context.Context, team *models.Team) error
	DeleteTeam(ctx context.Context, orgSlug, teamID string) error
	AddTeamMember(ctx context.Context, member *models.TeamMember) error
	DeleteTeamMember(ctx context.Context, orgSlug, teamID, username string) error

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
