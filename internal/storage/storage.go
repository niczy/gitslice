package storage

import (
	"context"
	"errors"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

var (
	ErrSliceNotFound            = errors.New("slice not found")
	ErrSliceAlreadyExists       = errors.New("slice already exists")
	ErrInvalidInput             = errors.New("invalid input")
	ErrChangesetNotFound        = errors.New("changeset not found")
	ErrEntryNotFound            = errors.New("entry not found")
	ErrEntryExists              = errors.New("entry already exists")
	ErrLockHeld                 = errors.New("resource locked")
	ErrCommitNotFound           = errors.New("commit not found")
	ErrSliceFilesImmutable      = errors.New("slice files are immutable")
	ErrAgentSessionNotFound     = errors.New("agent session not found")
	ErrAgentSessionConflict     = errors.New("agent session conflict")
	ErrSearchArtifactNotReady   = errors.New("search artifact not ready")
	ErrMergeEventNotFound       = errors.New("merge event not found")
	ErrMergeEventConflict       = errors.New("merge event conflict")
	ErrHomePathHeadConflict     = errors.New("home path head conflict")
	ErrMergeFastPathUnsupported = errors.New("merge fast path unsupported")
	ErrPermissionDenied         = errors.New("permission denied")
)

const (
	defaultSliceCommitListLimit  = 100
	maxSliceCommitListLimit      = 10000
	defaultMergeEventListLimit   = 100
	maxMergeEventListLimit       = 10000
	defaultHomePathHeadListLimit = 1000
	maxHomePathHeadListLimit     = 100000
)

// ContentCommitScope identifies a backing content directory whose commits are
// part of a mounted slice's reconstructable history.
type ContentCommitScope struct {
	HomeID  string
	DirPath string
}

// MergeEventStore persists accepted merge facts and projection offsets.
type MergeEventStore interface {
	NextMergeEventSequence(ctx context.Context, shardID int32) (int64, error)
	AppendMergeEvent(ctx context.Context, event *models.MergeEvent) error
	GetMergeEventByChangeset(ctx context.Context, changesetID string) (*models.MergeEvent, error)
	GetMergeEventBySourceCommitHash(ctx context.Context, sourceCommitHash string) (*models.MergeEvent, error)
	ListMergeEvents(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*models.MergeEvent, error)
	UpdateProjectionOffset(ctx context.Context, offset *models.ProjectionOffset) error
	GetProjectionOffset(ctx context.Context, projectionName string, shardID int32) (*models.ProjectionOffset, error)
}

// MergeEventProjectionBatchProcessor claims and processes one ordered projection
// batch while preventing concurrent workers from claiming the same offset range.
type MergeEventProjectionBatchProcessor interface {
	ProcessMergeEventProjectionBatch(ctx context.Context, projectionName string, shardCount int32, limit int, fn func(context.Context, []*models.MergeEvent) error) (bool, error)
}

// MergeEventPathHeadCASStore atomically applies path-head compare-and-set
// updates and appends the accepted merge event.
type MergeEventPathHeadCASStore interface {
	AppendMergeEventWithPathHeadCAS(ctx context.Context, event *models.MergeEvent) error
}

// AcceptChangesetMergeRequest contains the already-authorized inputs required
// to accept a standard changeset merge on the storage hot path.
type AcceptChangesetMergeRequest struct {
	Changeset     *models.Changeset
	SourceSlice   *models.Slice
	ModifiedFiles []string
	HomeID        string
	ShardID       int32
	CommitHash    string
	MergedAt      time.Time
}

// AcceptChangesetMergeResult contains the durable merge fact and source-slice
// parent metadata produced by the storage hot path.
type AcceptChangesetMergeResult struct {
	Changeset   *models.Changeset
	SourceSlice *models.Slice
	Event       *models.MergeEvent
	ParentHash  string
	CommitHash  string
	MergedAt    time.Time
}

// ChangesetMergeAccepter atomically accepts a prepared changeset merge. The
// implementation should use path-head CAS as the conflict authority and append
// the accepted merge event in the same transaction.
type ChangesetMergeAccepter interface {
	AcceptChangesetMerge(ctx context.Context, req *AcceptChangesetMergeRequest) (*AcceptChangesetMergeResult, error)
}

// ChangesetMergeByIDAccepter loads, authorizes, and accepts a changeset merge
// inside one storage transaction.
type ChangesetMergeByIDAccepter interface {
	AcceptChangesetMergeByID(ctx context.Context, changesetID string, username string, commitHash string, mergedAt time.Time) (*AcceptChangesetMergeResult, error)
}

type ListChangesetsOptions struct {
	Status               *models.ChangesetStatus
	Limit                int
	IncludeModifiedFiles bool
}

type ChangesetOptionLister interface {
	ListChangesetsWithOptions(ctx context.Context, sliceID string, opts ListChangesetsOptions) ([]*models.Changeset, error)
}

type ListChangesetSnapshotsOptions struct {
	Limit                int
	IncludeModifiedFiles bool
}

type ChangesetSnapshotOptionLister interface {
	ListChangesetSnapshotsWithOptions(ctx context.Context, changesetID string, opts ListChangesetSnapshotsOptions) ([]*models.ChangesetSnapshot, error)
}

type ListSliceContentCommitsOptions struct {
	Limit             int
	FromCommitHash    string
	MaxMergeSeqByHome map[string]int64
}

type ContentCommitOptionLister interface {
	ListSliceContentCommitsWithOptions(ctx context.Context, sliceID string, scopes []ContentCommitScope, opts ListSliceContentCommitsOptions) ([]*models.Commit, error)
}

// HomePathHeadStore persists home-scoped path heads for future merge conflict authority.
type HomePathHeadStore interface {
	UpsertHomePathHeads(ctx context.Context, heads []*models.HomePathHead) error
	GetHomePathHeads(ctx context.Context, homeID string, paths []string) (map[string]*models.HomePathHead, error)
	ListHomePathHeads(ctx context.Context, homeID string, limit int) ([]*models.HomePathHead, error)
	BackfillHomePathHeads(ctx context.Context, homeID string) (*models.HomePathHeadBackfillResult, error)
	ValidateHomePathHeads(ctx context.Context, homeID string) (*models.HomePathHeadValidationResult, error)
}

// DirectoryMoveStore persists accepted directory rename facts.
type DirectoryMoveStore interface {
	CreateDirectoryMove(ctx context.Context, move *models.DirectoryMove) error
	ListDirectoryMoves(ctx context.Context, homeID string) ([]*models.DirectoryMove, error)
}

func normalizeSliceCommitLimit(limit int) int {
	if limit <= 0 {
		return defaultSliceCommitListLimit
	}
	if limit > maxSliceCommitListLimit {
		return maxSliceCommitListLimit
	}
	return limit
}

func normalizeMergeEventListLimit(limit int) int {
	if limit <= 0 {
		return defaultMergeEventListLimit
	}
	if limit > maxMergeEventListLimit {
		return maxMergeEventListLimit
	}
	return limit
}

func normalizeHomePathHeadListLimit(limit int) int {
	if limit <= 0 {
		return defaultHomePathHeadListLimit
	}
	if limit > maxHomePathHeadListLimit {
		return maxHomePathHeadListLimit
	}
	return limit
}

// Storage defines the interface for data storage operations.
// This allows us to swap implementations (in-memory, PostgreSQL, etc.).
type Storage interface {
	CIStore

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
	UpdateSliceVisibility(ctx context.Context, sliceID string, visibility models.Visibility) error
	UpdateSliceEnvironment(ctx context.Context, sliceID, environment string) error
	UpdateSliceFolderMounts(ctx context.Context, sliceID string, mounts []models.SliceFolderMount, files []string) error
	GetSliceByName(ctx context.Context, name string) (*models.Slice, error)
	GetSliceBySlug(ctx context.Context, slug string) (*models.Slice, error)
	GetSliceByOwnerAndSlug(ctx context.Context, owner, slug string) (*models.Slice, error)
	GetRootSlice(ctx context.Context) (*models.Slice, error)
	InitializeRootSlice(ctx context.Context) error
	AddSliceCommit(ctx context.Context, sliceID string, commit *models.Commit) error
	ListSliceCommits(ctx context.Context, sliceID string, limit int, fromCommitHash string) ([]*models.Commit, error)
	ListSliceContentCommits(ctx context.Context, sliceID string, scopes []ContentCommitScope, limit int, fromCommitHash string) ([]*models.Commit, error)
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
	GetChangesetSnapshotByHash(ctx context.Context, changesetID string, hash string) (*models.ChangesetSnapshot, error)
	ListChangesetSnapshots(ctx context.Context, changesetID string, limit int) ([]*models.ChangesetSnapshot, error)
	RecordAgentSessionChangeset(ctx context.Context, link *models.AgentSessionChangeset) error
	ListAgentSessionChangesets(ctx context.Context, sessionID string, limit int) ([]*models.AgentSessionChangeset, error)
	ListChangesetAgentSessions(ctx context.Context, changesetID string, limit int) ([]*models.AgentSessionChangeset, error)

	// Block-backed file content storage
	PutBlock(ctx context.Context, hash string, data []byte) error
	GetBlock(ctx context.Context, hash string) ([]byte, error)
	GetBlocks(ctx context.Context, hashes []string) (map[string][]byte, error)
	HasBlock(ctx context.Context, hash string) (bool, error)
	PutBlocks(ctx context.Context, blocks map[string][]byte) error
	PutFileManifest(ctx context.Context, sliceID, path string, manifest *models.FileManifest) error
	GetFileManifest(ctx context.Context, sliceID, path string) (*models.FileManifest, error)
	DeleteFileManifest(ctx context.Context, sliceID, path string) error
	PutVersionedFileManifest(ctx context.Context, manifest *models.FileManifest) error
	GetVersionedFileManifest(ctx context.Context, hash string) (*models.FileManifest, error)
	PutSearchIndexFileBlob(ctx context.Context, version uint32, searchContentHash string, payload []byte) error
	GetSearchIndexFileBlob(ctx context.Context, version uint32, searchContentHash string) ([]byte, error)
	PutSliceSearchArtifact(ctx context.Context, sliceID, commitHash string, version uint32, payload []byte) error
	GetSliceSearchArtifact(ctx context.Context, sliceID, commitHash string, version uint32) ([]byte, error)
	PutWorkspaceSearchArtifact(ctx context.Context, workspaceID string, version uint32, payload []byte) error
	GetWorkspaceSearchArtifact(ctx context.Context, workspaceID string, version uint32) ([]byte, error)
	DeleteWorkspaceSearchArtifact(ctx context.Context, workspaceID string, version uint32) error

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
	PingMetadata(ctx context.Context) error

	// Commit snapshot operations for versioned file access
	GetCommitSnapshot(ctx context.Context, commitHash string) (*models.CommitSnapshot, error)
	SaveCommitSnapshot(ctx context.Context, snapshot *models.CommitSnapshot) error
	GetCommitSnapshotFileHash(ctx context.Context, commitHash, path string) (string, error)
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
	CreateAccount(ctx context.Context, account *models.Account) error
	GetAccount(ctx context.Context, accountID string) (*models.Account, error)
	GetAccountByClaimTokenHash(ctx context.Context, claimTokenHash string) (*models.Account, error)
	UpdateAccount(ctx context.Context, account *models.Account) error
	EnsureUser(ctx context.Context, username string) (*models.User, error)
	GetUser(ctx context.Context, username string) (*models.User, error)
	GetUserByEmail(ctx context.Context, email string) (*models.User, error)
	GetUserByClerkUserID(ctx context.Context, clerkUserID string) (*models.User, error)
	ListUsers(ctx context.Context, limit, offset int) ([]*models.User, error)
	CreateUser(ctx context.Context, user *models.User) error
	UpdateUser(ctx context.Context, user *models.User) error
	DeleteUser(ctx context.Context, username string) error
	CreateAuthSession(ctx context.Context, session *models.AuthSession) error
	GetAuthSession(ctx context.Context, sessionID string) (*models.AuthSession, error)
	GetAuthSessionByToken(ctx context.Context, token string) (*models.AuthSession, error)
	GetAuthSessionByRefreshToken(ctx context.Context, refreshToken string) (*models.AuthSession, error)
	ListAuthSessionsByUser(ctx context.Context, username string) ([]*models.AuthSession, error)
	UpdateAuthSessionTokens(ctx context.Context, sessionID, accessToken string, accessTokenExpiresAt *time.Time, refreshToken string, refreshTokenExpiresAt *time.Time) error
	TouchAuthSession(ctx context.Context, sessionID string, at time.Time) error
	RevokeAuthSession(ctx context.Context, username, sessionID string) error
	RevokeAuthSessionByToken(ctx context.Context, token string) error
	RevokeAuthSessionsByAgentKey(ctx context.Context, username, agentKeyID string) (int, error)
	CreateAgentKey(ctx context.Context, key *models.AgentKey) error
	GetAgentKey(ctx context.Context, keyID string) (*models.AgentKey, error)
	GetAgentKeyByFingerprint(ctx context.Context, fingerprint string) (*models.AgentKey, error)
	ListAgentKeysByUser(ctx context.Context, username string) ([]*models.AgentKey, error)
	TouchAgentKey(ctx context.Context, keyID string, at time.Time) error
	RevokeAgentKey(ctx context.Context, username, keyID string, revokedAt time.Time) error
	CreateAgentKeyChallenge(ctx context.Context, challenge *models.AgentKeyChallenge) error
	GetAgentKeyChallenge(ctx context.Context, challengeID string) (*models.AgentKeyChallenge, error)
	MarkAgentKeyChallengeUsed(ctx context.Context, challengeID string, usedAt time.Time) error
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
	UpsertEnvironmentKV(ctx context.Context, entry *models.EnvironmentKVEntry) (*models.EnvironmentKVEntry, error)
	ListEnvironmentKV(ctx context.Context, filter models.EnvironmentKVFilter) ([]*models.EnvironmentKVEntry, error)
	DeleteEnvironmentKV(ctx context.Context, filter models.EnvironmentKVFilter) error
	ResolveEnvironmentKV(ctx context.Context, homeID, sliceID, profile string, class models.EnvironmentKVClass, key string) (*models.EnvironmentKVEntry, error)

	// Agent runners
	UpsertAgentRunner(ctx context.Context, runner *models.AgentRunner) error
	GetAgentRunner(ctx context.Context, runnerID string) (*models.AgentRunner, error)
	ListAgentRunnersByUser(ctx context.Context, username string, limit int) ([]*models.AgentRunner, error)
	UpdateAgentRunner(ctx context.Context, runner *models.AgentRunner) error

	// Agent sessions
	CreateAgentSession(ctx context.Context, session *models.AgentSession) error
	GetAgentSession(ctx context.Context, sessionID string) (*models.AgentSession, error)
	GetActiveAgentSessionBySlice(ctx context.Context, sliceID string) (*models.AgentSession, error)
	ListAgentSessionsBySlice(ctx context.Context, sliceID string, limit int) ([]*models.AgentSession, error)
	ListAgentSessionsByState(ctx context.Context, states []models.AgentSessionState, limit int) ([]*models.AgentSession, error)
	UpdateAgentSession(ctx context.Context, session *models.AgentSession) error
	AppendAgentSessionEvent(ctx context.Context, event *models.AgentSessionEvent) error
	ListAgentSessionEvents(ctx context.Context, sessionID string, sinceSeq uint64, limit int) ([]*models.AgentSessionEvent, error)
	ListLatestAgentSessionEvents(ctx context.Context, sessionID string, limit int) ([]*models.AgentSessionEvent, error)
	AddAgentSessionAudit(ctx context.Context, audit *models.AgentSessionAudit) error
}
