package workflow

import "encoding/json"

type changesetCreateJSON struct {
	ChangesetID string `json:"changeset_id"`
	Status      string `json:"status"`
	Updated     bool   `json:"updated"`
}

type mergeJSON struct {
	ChangesetID   string `json:"changeset_id"`
	Status        string `json:"status"`
	NewCommitHash string `json:"new_commit_hash"`
}

type changesetCloseJSON struct {
	ChangesetID string `json:"changeset_id"`
	Status      string `json:"status"`
}

type changesetRebaseJSON struct {
	ChangesetID         string   `json:"changeset_id"`
	Status              string   `json:"status"`
	NewBaseCommitHash   string   `json:"new_base_commit_hash"`
	SliceCommitsToApply []string `json:"slice_commits_to_apply"`
}

type sliceCreateJSON struct {
	SliceID string `json:"slice_id"`
	Slug    string `json:"slug"`
}

type initJSON struct {
	Status  string `json:"status"`
	SliceID string `json:"slice_id"`
}

type sliceEnsureJSON struct {
	Created bool   `json:"created"`
	SliceID string `json:"slice_id"`
	Slug    string `json:"slug"`
	Status  string `json:"status"`
}

type sliceCheckoutJSON struct {
	SliceID   string `json:"slice_id"`
	Commit    string `json:"commit"`
	Path      string `json:"path"`
	FileCount int    `json:"file_count"`
	CacheHits int64  `json:"cache_hits"`
	Files     []struct {
		Path          string `json:"path"`
		Size          int64  `json:"size"`
		Executable    bool   `json:"executable"`
		SymlinkTarget string `json:"symlink_target"`
	} `json:"files"`
}

type sliceSyncJSON struct {
	SliceID   string `json:"slice_id"`
	Commit    string `json:"commit"`
	FileCount int    `json:"file_count"`
	Status    string `json:"status"`
	CacheHits int64  `json:"cache_hits"`
}

type sliceSearchJSON struct {
	SliceID string `json:"slice_id"`
	Query   string `json:"query"`
	Regex   bool   `json:"regex"`
	Glob    string `json:"glob"`
	Total   int    `json:"total"`
	Matches []struct {
		Path       string `json:"path"`
		LineNumber int    `json:"line_number"`
		Line       string `json:"line"`
	} `json:"matches"`
}

type fsSearchJSON struct {
	WorkspaceID string `json:"workspace_id"`
	Query       string `json:"query"`
	Regex       bool   `json:"regex"`
	Glob        string `json:"glob"`
	Total       int    `json:"total"`
	Matches     []struct {
		Path       string `json:"path"`
		LineNumber int    `json:"line_number"`
		Line       string `json:"line"`
	} `json:"matches"`
}

type sliceListJSON struct {
	Total  int `json:"total"`
	Slices []struct {
		SliceID string `json:"slice_id"`
		Slug    string `json:"slug"`
	} `json:"slices"`
}

type sliceStatusJSON struct {
	SliceID            string `json:"slice_id"`
	Mode               string `json:"mode"`
	WorkingTree        string `json:"working_tree"`
	SyncStatus         string `json:"sync_status"`
	PathCount          int    `json:"path_count"`
	TrackedChangesetID string `json:"tracked_changeset_id"`
	RemoteQueried      bool   `json:"remote_queried"`
	RemoteHead         string `json:"remote_head"`
	Changes            struct {
		Added    int `json:"added"`
		Modified int `json:"modified"`
		Deleted  int `json:"deleted"`
	} `json:"changes"`
}

type sliceTreeJSON struct {
	SliceID string `json:"slice_id"`
	Path    string `json:"path"`
	Nodes   []struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Type     string `json:"type"`
		Children []struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"children"`
	} `json:"nodes"`
}

type sliceRenameJSON struct {
	SliceID string `json:"slice_id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Status  string `json:"status"`
}

type sliceVisibilityJSON struct {
	SliceID             string `json:"slice_id"`
	Visibility          string `json:"visibility"`
	PathPropagationMode string `json:"path_propagation_mode"`
}

type sliceRootJSON struct {
	SliceID    string `json:"slice_id"`
	CommitHash string `json:"commit_hash"`
}

type sliceHistoryJSON struct {
	Commits []struct {
		CommitHash string `json:"commit_hash"`
		Message    string `json:"message"`
	} `json:"commits"`
}

type changesetReviewJSON struct {
	ChangesetID  string `json:"changeset_id"`
	ReviewStatus string `json:"review_status"`
	Snapshot     *struct {
		Version int32  `json:"version"`
		Hash    string `json:"hash"`
	} `json:"snapshot"`
	Diff struct {
		FilesAdded    int32 `json:"files_added"`
		FilesModified int32 `json:"files_modified"`
		FilesDeleted  int32 `json:"files_deleted"`
	} `json:"diff"`
	Changes []struct {
		Path         string `json:"path"`
		ChangeType   string `json:"change_type"`
		LinesAdded   int32  `json:"lines_added"`
		LinesDeleted int32  `json:"lines_deleted"`
		Patch        string `json:"patch"`
	} `json:"changes"`
}

type slicePublishJSON struct {
	Changeset struct {
		ChangesetID   string   `json:"changeset_id"`
		Status        string   `json:"status"`
		ModifiedFiles []string `json:"modified_files"`
	} `json:"changeset"`
	Review struct {
		ChangesetID  string `json:"changeset_id"`
		ReviewStatus string `json:"review_status"`
		Diff         struct {
			FilesAdded    int32 `json:"files_added"`
			FilesModified int32 `json:"files_modified"`
			FilesDeleted  int32 `json:"files_deleted"`
		} `json:"diff"`
	} `json:"review"`
	ReviewOnly     bool `json:"review_only"`
	ReusedExisting bool `json:"reused_existing"`
	Merge          *struct {
		Status string `json:"status"`
	} `json:"merge"`
}

type changesetListJSON struct {
	Total      int `json:"total"`
	Changesets []struct {
		ChangesetID string `json:"changeset_id"`
		Status      string `json:"status"`
	} `json:"changesets"`
}

type sliceDeleteJSON struct {
	SliceID string `json:"slice_id"`
	Status  string `json:"status"`
}

type repoBindingJSON struct {
	Path                 string `json:"path"`
	RepoURL              string `json:"repo_url"`
	Branch               string `json:"branch"`
	PushEnabled          bool   `json:"push_enabled"`
	LastImportedCommit   string `json:"last_imported_commit"`
	LastPushedCommit     string `json:"last_pushed_commit"`
	LastSeenRemoteCommit string `json:"last_seen_remote_commit"`
}

type repoImportJSON struct {
	Binding      repoBindingJSON `json:"binding"`
	CommitHash   string          `json:"commit_hash"`
	RemoteCommit string          `json:"remote_commit"`
	FileCount    int32           `json:"file_count"`
}

type repoEnsureJSON struct {
	Created      bool            `json:"created"`
	Updated      bool            `json:"updated"`
	Binding      repoBindingJSON `json:"binding"`
	CommitHash   string          `json:"commit_hash"`
	RemoteCommit string          `json:"remote_commit"`
	FileCount    int32           `json:"file_count"`
}

type repoListJSON struct {
	Total    int               `json:"total"`
	Bindings []repoBindingJSON `json:"bindings"`
}

type repoStatusJSON struct {
	Found   bool             `json:"found"`
	Binding *repoBindingJSON `json:"binding"`
}

type repoPullJSON struct {
	Binding      repoBindingJSON `json:"binding"`
	CommitHash   string          `json:"commit_hash"`
	RemoteCommit string          `json:"remote_commit"`
	FileCount    int32           `json:"file_count"`
	Updated      bool            `json:"updated"`
	Published    bool            `json:"published"`
}

type repoPushJSON struct {
	Binding      repoBindingJSON `json:"binding"`
	RemoteCommit string          `json:"remote_commit"`
	Pushed       bool            `json:"pushed"`
}

type repoUnlinkJSON struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type jobJSON struct {
	JobID      string          `json:"job_id"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Command    []string        `json:"command"`
	WorkingDir string          `json:"working_dir"`
	CreatedAt  string          `json:"created_at"`
	StartedAt  string          `json:"started_at"`
	FinishedAt string          `json:"finished_at"`
	PID        int             `json:"pid"`
	ExitCode   int             `json:"exit_code"`
	StdoutPath string          `json:"stdout_path"`
	StderrPath string          `json:"stderr_path"`
	Result     json.RawMessage `json:"result"`
	Stdout     string          `json:"stdout"`
	Stderr     string          `json:"stderr"`
}

type jobsListJSON struct {
	Total int       `json:"total"`
	Jobs  []jobJSON `json:"jobs"`
}

type jobLogsJSON struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

type authKeygenJSON struct {
	Status         string `json:"status"`
	Algorithm      string `json:"algorithm"`
	Fingerprint    string `json:"fingerprint"`
	PrivateKeyPath string `json:"private_key_path"`
	PublicKeyPath  string `json:"public_key_path"`
}

type authLoginJSON struct {
	Status                string `json:"status"`
	Username              string `json:"username"`
	Source                string `json:"source"`
	AuthMethod            string `json:"auth_method"`
	AgentKeyID            string `json:"agent_key_id"`
	SessionID             string `json:"session_id"`
	AccessTokenExpiresAt  string `json:"access_token_expires_at"`
	RefreshTokenExpiresAt string `json:"refresh_token_expires_at"`
	KeyFingerprint        string `json:"key_fingerprint"`
}

type authStatusJSON struct {
	Authenticated         bool   `json:"authenticated"`
	Username              string `json:"username"`
	Source                string `json:"source"`
	CredentialStore       bool   `json:"credential_store"`
	AuthMethod            string `json:"auth_method"`
	AgentKeyID            string `json:"agent_key_id"`
	KeyFingerprint        string `json:"key_fingerprint"`
	SessionID             string `json:"session_id"`
	AccessTokenExpiresAt  string `json:"access_token_expires_at"`
	RefreshTokenExpiresAt string `json:"refresh_token_expires_at"`
}

type authEnsureJSON struct {
	Status                string `json:"status"`
	Ensured               bool   `json:"ensured"`
	Authenticated         bool   `json:"authenticated"`
	Username              string `json:"username"`
	Source                string `json:"source"`
	CredentialStore       bool   `json:"credential_store"`
	AuthMethod            string `json:"auth_method"`
	AgentKeyID            string `json:"agent_key_id"`
	KeyFingerprint        string `json:"key_fingerprint"`
	SessionID             string `json:"session_id"`
	AccessTokenExpiresAt  string `json:"access_token_expires_at"`
	RefreshTokenExpiresAt string `json:"refresh_token_expires_at"`
}

type authKeyJSON struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Name        string `json:"name"`
	Algorithm   string `json:"algorithm"`
	Fingerprint string `json:"fingerprint"`
	Revoked     bool   `json:"revoked"`
}

type authKeysListJSON struct {
	Total int           `json:"total"`
	Keys  []authKeyJSON `json:"keys"`
}

type authClaimTokenJSON struct {
	AccountID  string `json:"account_id"`
	ClaimToken string `json:"claim_token"`
	ClaimURL   string `json:"claim_url"`
}

type authKeyRevokeJSON struct {
	KeyID  string `json:"key_id"`
	Status string `json:"status"`
}

type cacheStatsJSON struct {
	CacheRoot            string `json:"cache_root"`
	CachedObjects        int    `json:"cached_objects"`
	CachedBytes          int64  `json:"cached_bytes"`
	TrackedCheckouts     int    `json:"tracked_checkouts"`
	UniqueSlices         int    `json:"unique_slices"`
	StaleCheckoutRecords int    `json:"stale_checkout_records"`
	Checkouts            []struct {
		Path       string `json:"path"`
		SliceID    string `json:"slice_id"`
		CommitHash string `json:"commit_hash"`
		Status     string `json:"status"`
	} `json:"checkouts"`
}

type cachePruneJSON struct {
	Removed int `json:"removed"`
}

type cacheClearJSON struct {
	RemovedCachedObjects        int    `json:"removed_cached_objects"`
	RemovedCachedBytes          int64  `json:"removed_cached_bytes"`
	RemovedStaleCheckoutRecords int    `json:"removed_stale_checkout_records"`
	RemovedCheckoutRecords      int    `json:"removed_checkout_records"`
	ClearedPath                 string `json:"cleared_path"`
	ClearedPathFound            bool   `json:"cleared_path_found"`
}

type doctorJSON struct {
	Auth struct {
		Source            string `json:"source"`
		Username          string `json:"username"`
		StoredCredentials bool   `json:"stored_credentials"`
		AuthMethod        string `json:"auth_method"`
		AgentKeyID        string `json:"agent_key_id"`
		KeyFingerprint    string `json:"key_fingerprint"`
		SessionID         string `json:"session_id"`
	} `json:"auth"`
	Services struct {
		Admin struct {
			OK       bool   `json:"ok"`
			Username string `json:"username"`
			Error    string `json:"error"`
		} `json:"admin"`
		Slice struct {
			OK          bool   `json:"ok"`
			RootSliceID string `json:"root_slice_id"`
			Head        string `json:"head"`
			Error       string `json:"error"`
		} `json:"slice"`
		GlobalState struct {
			OK    bool   `json:"ok"`
			Head  string `json:"head"`
			Error string `json:"error"`
		} `json:"global_state"`
		Filesystem struct {
			OK          bool   `json:"ok"`
			HomeSliceID string `json:"home_slice_id"`
			Error       string `json:"error"`
		} `json:"filesystem"`
	} `json:"services"`
	Cache struct {
		Root                 string `json:"root"`
		ObjectCount          int    `json:"object_count"`
		TotalBytes           int64  `json:"total_bytes"`
		TrackedCheckouts     int    `json:"tracked_checkouts"`
		UniqueSlices         int    `json:"unique_slices"`
		StaleCheckoutRecords int    `json:"stale_checkout_records"`
		Error                string `json:"error"`
	} `json:"cache"`
	Checkout struct {
		Present              bool   `json:"present"`
		Error                string `json:"error"`
		SliceID              string `json:"slice_id"`
		Mode                 string `json:"mode"`
		CheckoutBase         string `json:"checkout_base"`
		RemoteHead           string `json:"remote_head"`
		ModifiedFiles        int    `json:"modified_files"`
		WorkingTree          string `json:"working_tree"`
		Registered           bool   `json:"registered"`
		RegisteredCommitHash string `json:"registered_commit_hash"`
		Changes              struct {
			Added    int `json:"added"`
			Modified int `json:"modified"`
			Deleted  int `json:"deleted"`
		} `json:"changes"`
	} `json:"checkout"`
}

type conflictListJSON struct {
	SliceID   string `json:"slice_id"`
	Total     int    `json:"total"`
	Conflicts []struct {
		FileID              string   `json:"file_id"`
		ConflictingSliceIDs []string `json:"conflicting_slice_ids"`
		Severity            string   `json:"severity"`
	} `json:"conflicts"`
}

type conflictResolveJSON struct {
	Conflict struct {
		FileID              string   `json:"file_id"`
		ConflictingSliceIDs []string `json:"conflicting_slice_ids"`
		Severity            string   `json:"severity"`
	} `json:"conflict"`
}

type conflictShowJSON struct {
	Found    bool `json:"found"`
	Conflict *struct {
		FileID              string   `json:"file_id"`
		ConflictingSliceIDs []string `json:"conflicting_slice_ids"`
		Severity            string   `json:"severity"`
	} `json:"conflict"`
}

type cliErrorJSON struct {
	ErrorCode       string `json:"error_code"`
	ExitCode        int    `json:"exit_code"`
	Message         string `json:"message"`
	Retryable       bool   `json:"retryable"`
	SuggestedAction string `json:"suggested_action"`
}

type filesystemActionJSON struct {
	Action            string `json:"action"`
	Status            string `json:"status"`
	DryRun            bool   `json:"dry_run"`
	Path              string `json:"path"`
	SourcePath        string `json:"source_path"`
	DestinationPath   string `json:"destination_path"`
	LocalPath         string `json:"local_path"`
	RemotePath        string `json:"remote_path"`
	EntryType         string `json:"entry_type"`
	CommitHash        string `json:"commit_hash"`
	SnapshotID        string `json:"snapshot_id"`
	CurrentSnapshotID string `json:"current_snapshot_id"`
	FileCount         int    `json:"file_count"`
	DirectoryCount    int    `json:"directory_count"`
	Summary           *struct {
		FilesAdded    int32 `json:"files_added"`
		FilesModified int32 `json:"files_modified"`
		FilesDeleted  int32 `json:"files_deleted"`
		LinesAdded    int64 `json:"lines_added"`
		LinesRemoved  int64 `json:"lines_removed"`
	} `json:"summary"`
	Message string `json:"message"`
}

type filesystemStatJSON struct {
	Exists bool `json:"exists"`
	Entry  struct {
		Path string `json:"path"`
		Type string `json:"type"`
		Size int64  `json:"size,string"`
		Hash string `json:"hash"`
	} `json:"entry"`
}

type pathVisibilityJSON struct {
	WorkspaceID         string `json:"workspace_id"`
	Path                string `json:"path"`
	Visibility          string `json:"visibility"`
	ExplicitRule        bool   `json:"explicit_rule"`
	ResolvedFromPath    string `json:"resolved_from_path"`
	EffectiveVisibility string `json:"effective_visibility"`
	Recursive           bool   `json:"recursive"`
}

type contextJSON struct {
	CurrentDir  string `json:"current_dir"`
	HomeSliceID string `json:"home_slice_id"`
	Auth        struct {
		Source            string `json:"source"`
		Username          string `json:"username"`
		StoredCredentials bool   `json:"stored_credentials"`
		AuthMethod        string `json:"auth_method"`
		AgentKeyID        string `json:"agent_key_id"`
		KeyFingerprint    string `json:"key_fingerprint"`
		SessionID         string `json:"session_id"`
	} `json:"auth"`
	Checkout struct {
		Present      bool   `json:"present"`
		Error        string `json:"error"`
		SliceID      string `json:"slice_id"`
		Mode         string `json:"mode"`
		CheckoutBase string `json:"checkout_base"`
		RemoteHead   string `json:"remote_head"`
		SyncStatus   string `json:"sync_status"`
		WorkingTree  string `json:"working_tree"`
		Changes      struct {
			Added    int `json:"added"`
			Modified int `json:"modified"`
			Deleted  int `json:"deleted"`
		} `json:"changes"`
	} `json:"checkout"`
	TrackedChange struct {
		Present      bool   `json:"present"`
		ChangesetID  string `json:"changeset_id"`
		ReviewStatus string `json:"review_status"`
		Error        string `json:"error"`
	} `json:"tracked_changeset"`
	RepoBindings []struct {
		Path        string `json:"path"`
		RepoURL     string `json:"repo_url"`
		PushEnabled bool   `json:"push_enabled"`
	} `json:"repo_bindings"`
}
