package gscli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	accountv1 "github.com/niczy/gitslice/proto/account"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type jsonAuthStatusOutput struct {
	Authenticated         bool   `json:"authenticated"`
	Username              string `json:"username,omitempty"`
	Source                string `json:"source,omitempty"`
	CredentialStore       bool   `json:"credential_store,omitempty"`
	AuthMethod            string `json:"auth_method,omitempty"`
	AgentKeyID            string `json:"agent_key_id,omitempty"`
	KeyFingerprint        string `json:"key_fingerprint,omitempty"`
	SessionID             string `json:"session_id,omitempty"`
	AccessTokenExpiresAt  string `json:"access_token_expires_at,omitempty"`
	RefreshTokenExpiresAt string `json:"refresh_token_expires_at,omitempty"`
}

type jsonAuthLoginOutput struct {
	Status                string `json:"status"`
	Username              string `json:"username,omitempty"`
	Source                string `json:"source,omitempty"`
	AuthMethod            string `json:"auth_method,omitempty"`
	AgentKeyID            string `json:"agent_key_id,omitempty"`
	SessionID             string `json:"session_id,omitempty"`
	AccessTokenExpiresAt  string `json:"access_token_expires_at,omitempty"`
	RefreshTokenExpiresAt string `json:"refresh_token_expires_at,omitempty"`
	KeyFingerprint        string `json:"key_fingerprint,omitempty"`
}

type jsonAuthLogoutOutput struct {
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
}

type jsonAuthEnsureOutput struct {
	Status                string `json:"status"`
	Ensured               bool   `json:"ensured"`
	Authenticated         bool   `json:"authenticated"`
	Username              string `json:"username,omitempty"`
	Source                string `json:"source,omitempty"`
	CredentialStore       bool   `json:"credential_store,omitempty"`
	AuthMethod            string `json:"auth_method,omitempty"`
	AgentKeyID            string `json:"agent_key_id,omitempty"`
	KeyFingerprint        string `json:"key_fingerprint,omitempty"`
	SessionID             string `json:"session_id,omitempty"`
	AccessTokenExpiresAt  string `json:"access_token_expires_at,omitempty"`
	RefreshTokenExpiresAt string `json:"refresh_token_expires_at,omitempty"`
}

type jsonAuthKeygenOutput struct {
	Status         string `json:"status"`
	Algorithm      string `json:"algorithm"`
	Fingerprint    string `json:"fingerprint"`
	PrivateKeyPath string `json:"private_key_path"`
	PublicKeyPath  string `json:"public_key_path"`
}

type jsonAuthKeyOutput struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id,omitempty"`
	Name        string `json:"name"`
	Algorithm   string `json:"algorithm"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	LastUsedAt  string `json:"last_used_at,omitempty"`
	RevokedAt   string `json:"revoked_at,omitempty"`
	Revoked     bool   `json:"revoked"`
}

type jsonAuthKeysListOutput struct {
	Total int                 `json:"total"`
	Keys  []jsonAuthKeyOutput `json:"keys"`
}

type jsonAuthKeyRevokeOutput struct {
	KeyID  string `json:"key_id"`
	Status string `json:"status"`
}

type jsonAuthClaimTokenOutput struct {
	AccountID  string `json:"account_id"`
	ClaimToken string `json:"claim_token"`
	ClaimURL   string `json:"claim_url"`
}

type jsonChangesetCreateOutput struct {
	ChangesetID   string   `json:"changeset_id"`
	ChangesetHash string   `json:"changeset_hash"`
	Status        string   `json:"status"`
	Updated       bool     `json:"updated"`
	SliceID       string   `json:"slice_id,omitempty"`
	ModifiedFiles []string `json:"modified_files,omitempty"`
}

type jsonChangesetDiffSummary struct {
	FilesAdded    int32 `json:"files_added"`
	FilesModified int32 `json:"files_modified"`
	FilesDeleted  int32 `json:"files_deleted"`
	LinesAdded    int64 `json:"lines_added"`
	LinesRemoved  int64 `json:"lines_removed"`
}

type jsonChangesetSnapshot struct {
	Version int32  `json:"version"`
	Hash    string `json:"hash"`
}

type jsonChangesetIssue struct {
	Type               string   `json:"type"`
	FileID             string   `json:"file_id,omitempty"`
	ConflictingSliceID []string `json:"conflicting_slice_ids,omitempty"`
	Message            string   `json:"message,omitempty"`
}

type jsonChangesetChange struct {
	Path         string `json:"path"`
	OldPath      string `json:"old_path,omitempty"`
	ChangeType   string `json:"change_type"`
	LinesAdded   int32  `json:"lines_added"`
	LinesDeleted int32  `json:"lines_deleted"`
	Patch        string `json:"patch,omitempty"`
}

type jsonChangesetReviewOutput struct {
	ChangesetID  string                   `json:"changeset_id"`
	SliceID      string                   `json:"slice_id,omitempty"`
	Message      string                   `json:"message,omitempty"`
	ReviewStatus string                   `json:"review_status"`
	Snapshot     *jsonChangesetSnapshot   `json:"snapshot,omitempty"`
	Diff         jsonChangesetDiffSummary `json:"diff"`
	Warnings     []string                 `json:"warnings,omitempty"`
	Issues       []jsonChangesetIssue     `json:"issues,omitempty"`
	Changes      []jsonChangesetChange    `json:"changes,omitempty"`
}

type jsonMergeConflict struct {
	FileID              string   `json:"file_id"`
	Type                string   `json:"type,omitempty"`
	ConflictingSliceIDs []string `json:"conflicting_slice_ids,omitempty"`
	Message             string   `json:"message,omitempty"`
}

type jsonMergeOutput struct {
	ChangesetID   string              `json:"changeset_id,omitempty"`
	Status        string              `json:"status"`
	NewCommitHash string              `json:"new_commit_hash,omitempty"`
	Message       string              `json:"message,omitempty"`
	Conflicts     []jsonMergeConflict `json:"conflicts,omitempty"`
}

type jsonChangesetRebaseOutput struct {
	ChangesetID         string              `json:"changeset_id"`
	Status              string              `json:"status"`
	NewBaseCommitHash   string              `json:"new_base_commit_hash,omitempty"`
	SliceCommitsToApply []string            `json:"slice_commits_to_apply,omitempty"`
	Conflicts           []jsonMergeConflict `json:"conflicts,omitempty"`
}

type jsonChangesetCloseOutput struct {
	ChangesetID string `json:"changeset_id,omitempty"`
	Status      string `json:"status"`
}

type jsonSliceInfo struct {
	Name        string   `json:"name"`
	SliceID     string   `json:"slice_id"`
	Slug        string   `json:"slug,omitempty"`
	Description string   `json:"description,omitempty"`
	Owners      []string `json:"owners,omitempty"`
	FileCount   int32    `json:"file_count"`
	UpdatedAt   int64    `json:"updated_at,omitempty"`
}

type jsonSliceListOutput struct {
	Total  int             `json:"total"`
	Slices []jsonSliceInfo `json:"slices"`
}

type jsonSliceCreateOutput struct {
	Name        string `json:"name"`
	SliceID     string `json:"slice_id"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
}

type jsonSliceEnsureOutput struct {
	Created     bool   `json:"created"`
	Name        string `json:"name"`
	SliceID     string `json:"slice_id"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
}

type jsonSliceCheckoutFile struct {
	Path          string `json:"path"`
	Size          int64  `json:"size"`
	Executable    bool   `json:"executable,omitempty"`
	SymlinkTarget string `json:"symlink_target,omitempty"`
}

type jsonSliceCheckoutOutput struct {
	SliceID   string                  `json:"slice_id"`
	Commit    string                  `json:"commit"`
	Path      string                  `json:"path,omitempty"`
	FileCount int                     `json:"file_count"`
	CacheHits int64                   `json:"cache_hits"`
	Files     []jsonSliceCheckoutFile `json:"files,omitempty"`
}

type jsonSliceSyncOutput struct {
	SliceID   string `json:"slice_id"`
	Commit    string `json:"commit"`
	FileCount int    `json:"file_count"`
	Status    string `json:"status"`
	CacheHits int64  `json:"cache_hits"`
}

type jsonSliceSearchMatch struct {
	Path       string `json:"path"`
	LineNumber int    `json:"line_number"`
	Line       string `json:"line"`
}

type jsonSliceSearchOutput struct {
	SliceID string                 `json:"slice_id"`
	Query   string                 `json:"query"`
	Regex   bool                   `json:"regex"`
	Glob    string                 `json:"glob,omitempty"`
	Total   int                    `json:"total"`
	Matches []jsonSliceSearchMatch `json:"matches,omitempty"`
}

type jsonSliceTreeNode struct {
	Name     string              `json:"name"`
	Path     string              `json:"path"`
	Type     string              `json:"type"`
	Size     int64               `json:"size,omitempty"`
	Children []jsonSliceTreeNode `json:"children,omitempty"`
}

type jsonSliceTreeOutput struct {
	SliceID string              `json:"slice_id"`
	Commit  string              `json:"commit,omitempty"`
	Path    string              `json:"path"`
	Nodes   []jsonSliceTreeNode `json:"nodes,omitempty"`
}

type jsonSliceRenameOutput struct {
	SliceID string `json:"slice_id"`
	Name    string `json:"name"`
	Slug    string `json:"slug,omitempty"`
	Status  string `json:"status"`
}

type jsonFilesystemSearchMatch struct {
	Path       string `json:"path"`
	LineNumber int    `json:"line_number"`
	Line       string `json:"line"`
}

type jsonFilesystemSearchOutput struct {
	WorkspaceID string                      `json:"workspace_id"`
	Query       string                      `json:"query"`
	Regex       bool                        `json:"regex"`
	Glob        string                      `json:"glob,omitempty"`
	Total       int                         `json:"total"`
	Matches     []jsonFilesystemSearchMatch `json:"matches,omitempty"`
}

type jsonWorkingTreeSummary struct {
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Deleted  int `json:"deleted"`
}

type jsonWorkingTreePath struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type jsonSliceStatusOutput struct {
	SliceID                string                 `json:"slice_id"`
	Mode                   string                 `json:"mode"`
	CheckoutBase           string                 `json:"checkout_base,omitempty"`
	RemoteQueried          bool                   `json:"remote_queried"`
	RemoteHead             string                 `json:"remote_head,omitempty"`
	SyncStatus             string                 `json:"sync_status"`
	TrackedChangesetID     string                 `json:"tracked_changeset_id,omitempty"`
	TrackedChangesetStatus string                 `json:"tracked_changeset_status,omitempty"`
	WorkingTree            string                 `json:"working_tree"`
	Changes                jsonWorkingTreeSummary `json:"changes"`
	PathCount              int                    `json:"path_count"`
	Paths                  []jsonWorkingTreePath  `json:"paths,omitempty"`
	Truncated              bool                   `json:"truncated"`
}

type jsonSliceDeleteOutput struct {
	SliceID                string `json:"slice_id"`
	Slug                   string `json:"slug,omitempty"`
	Status                 string `json:"status"`
	RemovedCheckoutRecords int    `json:"removed_checkout_records"`
}

type jsonSlicePublishOutput struct {
	Changeset      jsonChangesetCreateOutput `json:"changeset"`
	Review         jsonChangesetReviewOutput `json:"review"`
	ReviewOnly     bool                      `json:"review_only"`
	ReusedExisting bool                      `json:"reused_existing,omitempty"`
	Merge          *jsonMergeOutput          `json:"merge,omitempty"`
}

type jsonRepoBinding struct {
	BindingID            string `json:"binding_id,omitempty"`
	Provider             string `json:"provider,omitempty"`
	RepoURL              string `json:"repo_url"`
	Branch               string `json:"branch,omitempty"`
	Path                 string `json:"path"`
	PushEnabled          bool   `json:"push_enabled"`
	LastImportedCommit   string `json:"last_imported_commit,omitempty"`
	LastPushedCommit     string `json:"last_pushed_commit,omitempty"`
	LastSeenRemoteCommit string `json:"last_seen_remote_commit,omitempty"`
	CreatedAt            string `json:"created_at,omitempty"`
	UpdatedAt            string `json:"updated_at,omitempty"`
}

type jsonRepoImportOutput struct {
	Binding      jsonRepoBinding `json:"binding"`
	CommitHash   string          `json:"commit_hash,omitempty"`
	RemoteCommit string          `json:"remote_commit,omitempty"`
	FileCount    int32           `json:"file_count"`
}

type jsonRepoListOutput struct {
	Total    int               `json:"total"`
	Bindings []jsonRepoBinding `json:"bindings"`
}

type jsonRepoStatusOutput struct {
	Found   bool             `json:"found"`
	Binding *jsonRepoBinding `json:"binding,omitempty"`
}

type jsonRepoPullOutput struct {
	Binding      jsonRepoBinding `json:"binding"`
	CommitHash   string          `json:"commit_hash,omitempty"`
	RemoteCommit string          `json:"remote_commit,omitempty"`
	FileCount    int32           `json:"file_count"`
	Updated      bool            `json:"updated"`
	Published    bool            `json:"published,omitempty"`
}

type jsonRepoPushOutput struct {
	Binding      jsonRepoBinding `json:"binding"`
	RemoteCommit string          `json:"remote_commit,omitempty"`
	Pushed       bool            `json:"pushed"`
}

type jsonRepoUnlinkOutput struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type jsonRepoEnsureOutput struct {
	Created      bool            `json:"created"`
	Updated      bool            `json:"updated"`
	Binding      jsonRepoBinding `json:"binding"`
	CommitHash   string          `json:"commit_hash,omitempty"`
	RemoteCommit string          `json:"remote_commit,omitempty"`
	FileCount    int32           `json:"file_count,omitempty"`
}

type jsonCacheCheckoutRecord struct {
	Path       string `json:"path"`
	SliceID    string `json:"slice_id"`
	CommitHash string `json:"commit_hash,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	Status     string `json:"status"`
}

type jsonCacheStatsOutput struct {
	CacheRoot            string                    `json:"cache_root"`
	CachedObjects        int                       `json:"cached_objects"`
	CachedBytes          int64                     `json:"cached_bytes"`
	TrackedCheckouts     int                       `json:"tracked_checkouts"`
	UniqueSlices         int                       `json:"unique_slices"`
	StaleCheckoutRecords int                       `json:"stale_checkout_records"`
	Checkouts            []jsonCacheCheckoutRecord `json:"checkouts,omitempty"`
}

type jsonCachePruneOutput struct {
	Removed int `json:"removed"`
}

type jsonCacheClearOutput struct {
	RemovedCachedObjects        int    `json:"removed_cached_objects,omitempty"`
	RemovedCachedBytes          int64  `json:"removed_cached_bytes,omitempty"`
	RemovedStaleCheckoutRecords int    `json:"removed_stale_checkout_records,omitempty"`
	RemovedCheckoutRecords      int    `json:"removed_checkout_records,omitempty"`
	ClearedPath                 string `json:"cleared_path,omitempty"`
	ClearedPathFound            bool   `json:"cleared_path_found,omitempty"`
}

type jsonInitOutput struct {
	Status  string `json:"status"`
	SliceID string `json:"slice_id"`
}

type jsonFilesystemTransferOutput struct {
	Action         string `json:"action"`
	Status         string `json:"status,omitempty"`
	DryRun         bool   `json:"dry_run,omitempty"`
	LocalPath      string `json:"local_path"`
	RemotePath     string `json:"remote_path"`
	FileCount      int    `json:"file_count"`
	DirectoryCount int    `json:"directory_count"`
}

type jsonFilesystemActionOutput struct {
	Action            string                    `json:"action"`
	Status            string                    `json:"status"`
	DryRun            bool                      `json:"dry_run,omitempty"`
	Path              string                    `json:"path,omitempty"`
	SourcePath        string                    `json:"source_path,omitempty"`
	DestinationPath   string                    `json:"destination_path,omitempty"`
	LocalPath         string                    `json:"local_path,omitempty"`
	RemotePath        string                    `json:"remote_path,omitempty"`
	EntryType         string                    `json:"entry_type,omitempty"`
	CommitHash        string                    `json:"commit_hash,omitempty"`
	SnapshotID        string                    `json:"snapshot_id,omitempty"`
	CurrentSnapshotID string                    `json:"current_snapshot_id,omitempty"`
	FileCount         int                       `json:"file_count,omitempty"`
	DirectoryCount    int                       `json:"directory_count,omitempty"`
	Summary           *jsonChangesetDiffSummary `json:"summary,omitempty"`
	Message           string                    `json:"message,omitempty"`
}

type jsonJobOutput struct {
	JobID      string          `json:"job_id"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Command    []string        `json:"command,omitempty"`
	WorkingDir string          `json:"working_dir,omitempty"`
	CreatedAt  string          `json:"created_at,omitempty"`
	StartedAt  string          `json:"started_at,omitempty"`
	FinishedAt string          `json:"finished_at,omitempty"`
	PID        int             `json:"pid,omitempty"`
	ExitCode   int             `json:"exit_code,omitempty"`
	StdoutPath string          `json:"stdout_path,omitempty"`
	StderrPath string          `json:"stderr_path,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Stdout     string          `json:"stdout,omitempty"`
	Stderr     string          `json:"stderr,omitempty"`
}

type jsonJobsListOutput struct {
	Total int             `json:"total"`
	Jobs  []jsonJobOutput `json:"jobs"`
}

type jsonJobLogsOutput struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
}

type jsonDoctorAuthOutput struct {
	Source            string `json:"source,omitempty"`
	Username          string `json:"username,omitempty"`
	StoredCredentials bool   `json:"stored_credentials"`
	AuthMethod        string `json:"auth_method,omitempty"`
	AgentKeyID        string `json:"agent_key_id,omitempty"`
	KeyFingerprint    string `json:"key_fingerprint,omitempty"`
	SessionID         string `json:"session_id,omitempty"`
}

type jsonDoctorServiceStatus struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
	Username    string `json:"username,omitempty"`
	RootSliceID string `json:"root_id,omitempty"`
	Head        string `json:"head,omitempty"`
	HomeSliceID string `json:"home_slice_id,omitempty"`
}

type jsonDoctorServicesOutput struct {
	Admin       jsonDoctorServiceStatus `json:"admin"`
	Slice       jsonDoctorServiceStatus `json:"slice"`
	GlobalState jsonDoctorServiceStatus `json:"global_state"`
	Filesystem  jsonDoctorServiceStatus `json:"filesystem"`
}

type jsonDoctorCacheOutput struct {
	Error                string `json:"error,omitempty"`
	Root                 string `json:"root,omitempty"`
	ObjectCount          int    `json:"object_count,omitempty"`
	TotalBytes           int64  `json:"total_bytes,omitempty"`
	TrackedCheckouts     int    `json:"tracked_checkouts,omitempty"`
	UniqueSlices         int    `json:"unique_slices,omitempty"`
	StaleCheckoutRecords int    `json:"stale_checkout_records,omitempty"`
}

type jsonDoctorCheckoutOutput struct {
	Present              bool                   `json:"present"`
	Error                string                 `json:"error,omitempty"`
	SliceID              string                 `json:"slice_id,omitempty"`
	Mode                 string                 `json:"mode,omitempty"`
	CheckoutBase         string                 `json:"checkout_base,omitempty"`
	RemoteHead           string                 `json:"remote_head,omitempty"`
	ModifiedFiles        int                    `json:"modified_files,omitempty"`
	WorkingTree          string                 `json:"working_tree,omitempty"`
	Changes              jsonWorkingTreeSummary `json:"changes,omitempty"`
	Registered           bool                   `json:"registered"`
	RegisteredCommitHash string                 `json:"registered_commit_hash,omitempty"`
}

type jsonDoctorOutput struct {
	Auth     jsonDoctorAuthOutput     `json:"auth"`
	Services jsonDoctorServicesOutput `json:"services"`
	Cache    jsonDoctorCacheOutput    `json:"cache"`
	Checkout jsonDoctorCheckoutOutput `json:"checkout"`
}

type jsonContextCheckoutOutput struct {
	Present      bool                   `json:"present"`
	Error        string                 `json:"error,omitempty"`
	SliceID      string                 `json:"slice_id,omitempty"`
	Mode         string                 `json:"mode,omitempty"`
	CheckoutBase string                 `json:"checkout_base,omitempty"`
	RemoteHead   string                 `json:"remote_head,omitempty"`
	SyncStatus   string                 `json:"sync_status,omitempty"`
	WorkingTree  string                 `json:"working_tree,omitempty"`
	Changes      jsonWorkingTreeSummary `json:"changes,omitempty"`
}

type jsonContextChangesetOutput struct {
	Present      bool   `json:"present"`
	ChangesetID  string `json:"changeset_id,omitempty"`
	ReviewStatus string `json:"review_status,omitempty"`
	Error        string `json:"error,omitempty"`
}

type jsonContextOutput struct {
	CurrentDir    string                     `json:"current_dir"`
	Auth          jsonDoctorAuthOutput       `json:"auth"`
	HomeSliceID   string                     `json:"home_slice_id,omitempty"`
	Checkout      jsonContextCheckoutOutput  `json:"checkout"`
	TrackedChange jsonContextChangesetOutput `json:"tracked_changeset"`
	RepoBindings  []jsonRepoBinding          `json:"repo_bindings,omitempty"`
}

type jsonConflictInfo struct {
	FileID              string   `json:"file_id"`
	ConflictingSliceIDs []string `json:"conflicting_slice_ids,omitempty"`
	Severity            string   `json:"severity,omitempty"`
}

type jsonConflictListOutput struct {
	SliceID   string             `json:"slice_id,omitempty"`
	Total     int                `json:"total"`
	Conflicts []jsonConflictInfo `json:"conflicts"`
}

type jsonConflictResolveOutput struct {
	Conflict jsonConflictInfo `json:"conflict"`
}

type jsonConflictShowOutput struct {
	Found    bool              `json:"found"`
	Conflict *jsonConflictInfo `json:"conflict,omitempty"`
}

type jsonChangesetListItem struct {
	ChangesetID string `json:"changeset_id"`
	Status      string `json:"status"`
	Message     string `json:"message,omitempty"`
	CreatedAt   int64  `json:"created_at,omitempty"`
}

type jsonChangesetListOutput struct {
	SliceID    string                  `json:"slice_id"`
	Total      int                     `json:"total"`
	Changesets []jsonChangesetListItem `json:"changesets"`
}

type jsonImportGitOutput struct {
	SliceID         string   `json:"slice_id"`
	MountPath       string   `json:"mount_path"`
	ImportedCommits int      `json:"imported_commits"`
	HeadCommitHash  string   `json:"head_commit_hash"`
	Warnings        []string `json:"warnings,omitempty"`
}

func writeJSONOutput(value any) {
	if msg, ok := value.(proto.Message); ok {
		marshaler := protojson.MarshalOptions{
			Indent:          "  ",
			UseProtoNames:   true,
			EmitUnpopulated: false,
		}
		data, err := marshaler.Marshal(msg)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Failed to write JSON output: %v\n", err)
			os.Exit(cliExitGeneral)
		}
		if _, err := os.Stdout.Write(data); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Failed to write JSON output: %v\n", err)
			os.Exit(cliExitGeneral)
		}
		if len(data) == 0 || data[len(data)-1] != '\n' {
			if _, err := os.Stdout.Write([]byte("\n")); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "Failed to write JSON output: %v\n", err)
				os.Exit(cliExitGeneral)
			}
		}
		return
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Failed to write JSON output: %v\n", err)
		os.Exit(cliExitGeneral)
	}
}

func buildChangesetCreateOutput(resp *slicev1.CreateChangesetResponse, updated bool, sliceID string, modifiedFiles []string) jsonChangesetCreateOutput {
	if resp == nil {
		return jsonChangesetCreateOutput{Updated: updated, SliceID: sliceID, ModifiedFiles: append([]string(nil), modifiedFiles...)}
	}
	return jsonChangesetCreateOutput{
		ChangesetID:   resp.GetChangesetId(),
		ChangesetHash: resp.GetChangesetHash(),
		Status:        resp.GetStatus().String(),
		Updated:       updated,
		SliceID:       sliceID,
		ModifiedFiles: append([]string(nil), modifiedFiles...),
	}
}

func buildAuthStatusOutput(authConfig cliAuth, creds credentialsConfig) jsonAuthStatusOutput {
	out := jsonAuthStatusOutput{
		Authenticated:   strings.TrimSpace(authConfig.Authorization) != "",
		Username:        strings.TrimSpace(authConfig.Username),
		Source:          strings.TrimSpace(authConfig.Source),
		CredentialStore: authConfig.CredentialStore,
		AuthMethod:      strings.TrimSpace(creds.AuthMethod),
		AgentKeyID:      strings.TrimSpace(creds.AgentKeyID),
		KeyFingerprint:  strings.TrimSpace(creds.KeyFingerprint),
	}
	if !out.Authenticated {
		return out
	}
	out.SessionID = strings.TrimSpace(creds.SessionID)
	if out.SessionID == "" {
		out.SessionID = strings.TrimSpace(creds.SessionIDCamel)
	}
	out.AccessTokenExpiresAt = strings.TrimSpace(creds.AccessTokenExpiresAt)
	if out.AccessTokenExpiresAt == "" {
		out.AccessTokenExpiresAt = strings.TrimSpace(creds.AccessTokenExpiresAtCamel)
	}
	out.RefreshTokenExpiresAt = strings.TrimSpace(creds.RefreshTokenExpiresAt)
	if out.RefreshTokenExpiresAt == "" {
		out.RefreshTokenExpiresAt = strings.TrimSpace(creds.RefreshTokenExpiresAtCamel)
	}
	return out
}

func buildAuthEnsureOutput(authConfig cliAuth, creds credentialsConfig, ensured bool) jsonAuthEnsureOutput {
	status := buildAuthStatusOutput(authConfig, creds)
	return jsonAuthEnsureOutput{
		Status:                "ready",
		Ensured:               ensured,
		Authenticated:         status.Authenticated,
		Username:              status.Username,
		Source:                status.Source,
		CredentialStore:       status.CredentialStore,
		AuthMethod:            status.AuthMethod,
		AgentKeyID:            status.AgentKeyID,
		KeyFingerprint:        status.KeyFingerprint,
		SessionID:             status.SessionID,
		AccessTokenExpiresAt:  status.AccessTokenExpiresAt,
		RefreshTokenExpiresAt: status.RefreshTokenExpiresAt,
	}
}

func buildAuthLoginOutput(resp *accountv1.AuthResponse, source, authMethod string, publicKey []byte) jsonAuthLoginOutput {
	out := jsonAuthLoginOutput{
		Status:     "authenticated",
		Source:     strings.TrimSpace(source),
		AuthMethod: strings.TrimSpace(authMethod),
	}
	if resp == nil {
		return out
	}
	out.Username = strings.TrimSpace(resp.GetUser().GetUsername())
	out.SessionID = strings.TrimSpace(resp.GetSession().GetId())
	out.AgentKeyID = strings.TrimSpace(resp.GetSession().GetAgentKeyId())
	out.AccessTokenExpiresAt = strings.TrimSpace(resp.GetAccessTokenExpiresAt())
	out.RefreshTokenExpiresAt = strings.TrimSpace(resp.GetRefreshTokenExpiresAt())
	if len(publicKey) > 0 {
		out.KeyFingerprint = agentKeyFingerprint(agentKeyAlgorithmEd25519, publicKey)
	}
	return out
}

func buildDoctorAuthOutput(authConfig cliAuth, creds credentialsConfig) jsonDoctorAuthOutput {
	return jsonDoctorAuthOutput{
		Source:            strings.TrimSpace(authConfig.Source),
		Username:          strings.TrimSpace(authConfig.Username),
		StoredCredentials: authConfig.CredentialStore,
		AuthMethod:        strings.TrimSpace(creds.AuthMethod),
		AgentKeyID:        strings.TrimSpace(creds.AgentKeyID),
		KeyFingerprint:    strings.TrimSpace(creds.KeyFingerprint),
		SessionID:         firstNonEmpty(strings.TrimSpace(creds.SessionID), strings.TrimSpace(creds.SessionIDCamel)),
	}
}

func buildAuthKeyOutput(key *accountv1.AgentKey) jsonAuthKeyOutput {
	if key == nil {
		return jsonAuthKeyOutput{}
	}
	return jsonAuthKeyOutput{
		ID:          key.GetId(),
		UserID:      key.GetUserId(),
		Name:        key.GetName(),
		Algorithm:   key.GetAlgorithm(),
		Fingerprint: key.GetFingerprint(),
		CreatedAt:   key.GetCreatedAt(),
		UpdatedAt:   key.GetUpdatedAt(),
		LastUsedAt:  key.GetLastUsedAt(),
		RevokedAt:   key.GetRevokedAt(),
		Revoked:     key.GetRevoked(),
	}
}

func buildAuthKeysListOutput(resp *accountv1.ListAgentKeysResponse) jsonAuthKeysListOutput {
	if resp == nil {
		return jsonAuthKeysListOutput{}
	}
	out := jsonAuthKeysListOutput{Keys: make([]jsonAuthKeyOutput, 0, len(resp.GetKeys()))}
	for _, key := range resp.GetKeys() {
		out.Keys = append(out.Keys, buildAuthKeyOutput(key))
	}
	out.Total = len(out.Keys)
	return out
}

func buildAuthClaimTokenOutput(resp *accountv1.CreateAccountClaimTokenResponse) jsonAuthClaimTokenOutput {
	if resp == nil {
		return jsonAuthClaimTokenOutput{}
	}
	return jsonAuthClaimTokenOutput{
		AccountID:  strings.TrimSpace(resp.GetAccountId()),
		ClaimToken: strings.TrimSpace(resp.GetClaimToken()),
		ClaimURL:   strings.TrimSpace(resp.GetClaimUrl()),
	}
}

func buildChangesetOutputFromInfo(info *slicev1.ChangesetInfo) jsonChangesetCreateOutput {
	if info == nil {
		return jsonChangesetCreateOutput{}
	}
	return jsonChangesetCreateOutput{
		ChangesetID:   info.GetChangesetId(),
		ChangesetHash: info.GetChangesetHash(),
		Status:        info.GetStatus().String(),
		Updated:       true,
		SliceID:       info.GetSliceId(),
		ModifiedFiles: append([]string(nil), info.GetModifiedFiles()...),
	}
}

func buildChangesetReviewOutput(resp *slicev1.ReviewChangesetResponse, includePatches bool) jsonChangesetReviewOutput {
	output := jsonChangesetReviewOutput{}
	if resp == nil {
		return output
	}
	output.ReviewStatus = resp.GetReviewStatus().String()
	if changeset := resp.GetChangeset(); changeset != nil {
		output.ChangesetID = changeset.GetChangesetId()
		output.SliceID = changeset.GetSliceId()
		output.Message = changeset.GetMessage()
	}
	if snapshot := resp.GetSnapshot(); snapshot != nil {
		output.Snapshot = &jsonChangesetSnapshot{
			Version: snapshot.GetVersion(),
			Hash:    snapshot.GetHash(),
		}
	}
	if diff := resp.GetDiff(); diff != nil {
		output.Diff = jsonChangesetDiffSummary{
			FilesAdded:    diff.GetFilesAdded(),
			FilesModified: diff.GetFilesModified(),
			FilesDeleted:  diff.GetFilesDeleted(),
			LinesAdded:    diff.GetLinesAdded(),
			LinesRemoved:  diff.GetLinesRemoved(),
		}
	}
	output.Warnings = append([]string(nil), resp.GetWarnings()...)
	if len(resp.GetIssues()) > 0 {
		output.Issues = make([]jsonChangesetIssue, 0, len(resp.GetIssues()))
		for _, issue := range resp.GetIssues() {
			output.Issues = append(output.Issues, jsonChangesetIssue{
				Type:               issue.GetType().String(),
				FileID:             issue.GetFileId(),
				ConflictingSliceID: append([]string(nil), issue.GetConflictingSliceIds()...),
				Message:            issue.GetMessage(),
			})
		}
	}
	if len(resp.GetChanges()) > 0 {
		output.Changes = make([]jsonChangesetChange, 0, len(resp.GetChanges()))
		for _, change := range resp.GetChanges() {
			item := jsonChangesetChange{
				Path:         change.GetPath(),
				OldPath:      change.GetOldPath(),
				ChangeType:   change.GetChangeType().String(),
				LinesAdded:   change.GetLinesAdded(),
				LinesDeleted: change.GetLinesDeleted(),
			}
			if includePatches {
				item.Patch = change.GetPatch()
			}
			output.Changes = append(output.Changes, item)
		}
	}
	return output
}

func buildMergeOutput(resp *slicev1.MergeChangesetResponse) *jsonMergeOutput {
	if resp == nil {
		return nil
	}
	output := &jsonMergeOutput{
		ChangesetID:   resp.GetChangesetId(),
		Status:        resp.GetStatus().String(),
		NewCommitHash: resp.GetNewCommitHash(),
		Message:       resp.GetMessage(),
	}
	output.Conflicts = buildMergeConflicts(resp.GetConflicts())
	return output
}

func buildChangesetCloseOutput(resp *slicev1.CloseChangesetResponse) jsonChangesetCloseOutput {
	if resp == nil {
		return jsonChangesetCloseOutput{}
	}
	return jsonChangesetCloseOutput{
		ChangesetID: resp.GetChangesetId(),
		Status:      resp.GetStatus().String(),
	}
}

func buildMergeConflicts(conflicts []*slicev1.Conflict) []jsonMergeConflict {
	if len(conflicts) == 0 {
		return nil
	}
	out := make([]jsonMergeConflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		out = append(out, jsonMergeConflict{
			FileID:              conflict.GetFileId(),
			Type:                conflict.GetType().String(),
			ConflictingSliceIDs: append([]string(nil), conflict.GetConflictingSliceIds()...),
			Message:             conflict.GetMessage(),
		})
	}
	return out
}

func buildSliceInfoOutput(slice *slicev1.SliceInfo) jsonSliceInfo {
	if slice == nil {
		return jsonSliceInfo{}
	}
	return jsonSliceInfo{
		Name:        slice.GetName(),
		SliceID:     slice.GetSliceId(),
		Slug:        slice.GetSlug(),
		Description: slice.GetDescription(),
		Owners:      append([]string(nil), slice.GetOwners()...),
		FileCount:   slice.GetFileCount(),
		UpdatedAt:   slice.GetUpdatedAt(),
	}
}

func buildSliceCheckoutOutput(sliceID, checkoutPath string, result *checkoutFetchResult, includeFiles bool) jsonSliceCheckoutOutput {
	output := jsonSliceCheckoutOutput{
		SliceID: sliceID,
		Path:    checkoutPath,
	}
	if result == nil {
		return output
	}
	if result.Manifest != nil {
		output.Commit = result.Manifest.GetCommitHash()
		output.FileCount = len(result.Manifest.GetFileMetadata())
		if includeFiles && len(result.Manifest.GetFileMetadata()) > 0 {
			output.Files = make([]jsonSliceCheckoutFile, 0, len(result.Manifest.GetFileMetadata()))
			for _, file := range result.Manifest.GetFileMetadata() {
				output.Files = append(output.Files, jsonSliceCheckoutFile{
					Path:          file.GetPath(),
					Size:          file.GetSize(),
					Executable:    file.GetExecutable(),
					SymlinkTarget: file.GetSymlinkTarget(),
				})
			}
		}
	}
	output.CacheHits = result.Materialized.CacheHits
	return output
}

func buildSliceSyncOutput(sliceID, syncStatus string, result *checkoutFetchResult) jsonSliceSyncOutput {
	output := jsonSliceSyncOutput{
		SliceID: sliceID,
		Status:  syncStatus,
	}
	if result == nil {
		return output
	}
	if result.Manifest != nil {
		output.Commit = result.Manifest.GetCommitHash()
		output.FileCount = len(result.Manifest.GetFileMetadata())
	}
	output.CacheHits = result.Materialized.CacheHits
	return output
}

func buildSliceSearchOutput(sliceID, query string, regex bool, glob string, matches []sliceSearchMatch) jsonSliceSearchOutput {
	output := jsonSliceSearchOutput{
		SliceID: sliceID,
		Query:   query,
		Regex:   regex,
		Glob:    strings.TrimSpace(glob),
		Total:   len(matches),
	}
	if len(matches) == 0 {
		return output
	}
	output.Matches = make([]jsonSliceSearchMatch, 0, len(matches))
	for _, match := range matches {
		output.Matches = append(output.Matches, jsonSliceSearchMatch{
			Path:       match.Path,
			LineNumber: match.LineNumber,
			Line:       match.Line,
		})
	}
	return output
}

func buildFilesystemSearchOutput(workspaceID, query string, regex bool, glob string, matches []*filesystemv1.SearchMatch) jsonFilesystemSearchOutput {
	output := jsonFilesystemSearchOutput{
		WorkspaceID: workspaceID,
		Query:       query,
		Regex:       regex,
		Glob:        strings.TrimSpace(glob),
		Total:       len(matches),
	}
	if len(matches) == 0 {
		return output
	}
	output.Matches = make([]jsonFilesystemSearchMatch, 0, len(matches))
	for _, match := range matches {
		if match == nil {
			continue
		}
		output.Matches = append(output.Matches, jsonFilesystemSearchMatch{
			Path:       match.GetPath(),
			LineNumber: int(match.GetLineNumber()),
			Line:       match.GetLine(),
		})
	}
	output.Total = len(output.Matches)
	return output
}

func buildRepoBindingOutput(binding *filesystemv1.RepoBinding) jsonRepoBinding {
	if binding == nil {
		return jsonRepoBinding{}
	}
	return jsonRepoBinding{
		BindingID:            binding.GetBindingId(),
		Provider:             binding.GetProvider(),
		RepoURL:              binding.GetRepoUrl(),
		Branch:               binding.GetBranch(),
		Path:                 binding.GetPath(),
		PushEnabled:          binding.GetPushEnabled(),
		LastImportedCommit:   binding.GetLastImportedCommit(),
		LastPushedCommit:     binding.GetLastPushedCommit(),
		LastSeenRemoteCommit: binding.GetLastSeenRemoteCommit(),
		CreatedAt:            binding.GetCreatedAt(),
		UpdatedAt:            binding.GetUpdatedAt(),
	}
}

func buildConflictOutput(conflict *slicev1.Conflict) jsonConflictInfo {
	if conflict == nil {
		return jsonConflictInfo{}
	}
	return jsonConflictInfo{
		FileID:              conflict.GetFileId(),
		ConflictingSliceIDs: append([]string(nil), conflict.GetConflictingSliceIds()...),
		Severity:            conflictSeverityLabel(conflict),
	}
}

func conflictSeverityLabel(conflict *slicev1.Conflict) string {
	if conflict == nil {
		return ""
	}
	switch len(conflict.GetConflictingSliceIds()) {
	case 0, 1:
		return "LOW"
	case 2:
		return "MEDIUM"
	default:
		return "HIGH"
	}
}
