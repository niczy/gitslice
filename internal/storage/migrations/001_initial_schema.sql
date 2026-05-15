CREATE TABLE accounts (
    account_id text NOT NULL,
    owner_mode text DEFAULT 'agent_only'::text NOT NULL,
    claim_state text DEFAULT 'unclaimed'::text NOT NULL,
    claim_token_hash text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE agent_key_challenges (
    challenge_id text NOT NULL,
    agent_key_id text NOT NULL,
    username text NOT NULL,
    challenge bytea NOT NULL,
    device_info text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone
);

CREATE TABLE agent_keys (
    key_id text NOT NULL,
    username text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    algorithm text NOT NULL,
    public_key bytea NOT NULL,
    fingerprint text NOT NULL,
    state text DEFAULT 'active'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    last_used_at timestamp with time zone,
    revoked_at timestamp with time zone
);

CREATE TABLE agent_session_audit (
    id bigint NOT NULL,
    session_id text NOT NULL,
    actor_user_id text,
    action text NOT NULL,
    metadata_json jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE SEQUENCE agent_session_audit_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE agent_session_audit_id_seq OWNED BY agent_session_audit.id;

CREATE TABLE agent_session_events (
    session_id text NOT NULL,
    seq bigint NOT NULL,
    ts timestamp with time zone NOT NULL,
    stream text NOT NULL,
    type text NOT NULL,
    kind text DEFAULT 'event'::text NOT NULL,
    payload_json jsonb NOT NULL
);

CREATE TABLE agent_session_changesets (
    session_id text NOT NULL,
    changeset_id text NOT NULL,
    snapshot_id text NOT NULL,
    snapshot_version integer DEFAULT 0 NOT NULL,
    snapshot_hash text DEFAULT ''::text NOT NULL,
    base_commit_hash text DEFAULT ''::text NOT NULL,
    exported_from_seq bigint DEFAULT 0 NOT NULL,
    runner_id text DEFAULT ''::text NOT NULL,
    source text DEFAULT 'local_export'::text NOT NULL,
    exported_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE agent_sessions (
    session_id text NOT NULL,
    slice_id text NOT NULL,
    runner_id text DEFAULT ''::text NOT NULL,
    user_id text NOT NULL,
    state text NOT NULL,
    provider text DEFAULT 'local'::text NOT NULL,
    e2b_template_id text NOT NULL,
    e2b_sandbox_id text,
    e2b_region text,
    idle_timeout_sec integer NOT NULL,
    ttl_sec integer NOT NULL,
    runtime_endpoint text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    started_at timestamp with time zone,
    last_activity_at timestamp with time zone,
    stopped_at timestamp with time zone,
    failure_code text,
    failure_message text,
    environment_name text DEFAULT ''::text NOT NULL,
    agent_type text DEFAULT ''::text NOT NULL,
    runtime_provider text DEFAULT ''::text NOT NULL,
    runtime_session_id text DEFAULT ''::text NOT NULL,
    runtime_status text DEFAULT ''::text NOT NULL,
    runtime_error_code text DEFAULT ''::text NOT NULL
);

CREATE TABLE agent_runners (
    runner_id text NOT NULL,
    user_id text NOT NULL,
    provider text DEFAULT 'local'::text NOT NULL,
    agent_type text DEFAULT 'codex'::text NOT NULL,
    status text DEFAULT 'online'::text NOT NULL,
    host_name text DEFAULT ''::text NOT NULL,
    pid integer DEFAULT 0 NOT NULL,
    workspace_root text DEFAULT ''::text NOT NULL,
    version text DEFAULT ''::text NOT NULL,
    capabilities_json jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_heartbeat_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE auth_sessions (
    session_id text NOT NULL,
    username text NOT NULL,
    token text NOT NULL,
    device_info text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    revoked_at timestamp with time zone,
    refresh_token text,
    access_token_expires_at timestamp with time zone,
    refresh_token_expires_at timestamp with time zone,
    agent_key_id text
);

CREATE SEQUENCE changeset_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE TABLE changeset_snapshots (
    id text NOT NULL,
    changeset_id text NOT NULL,
    version integer NOT NULL,
    hash text DEFAULT ''::text NOT NULL,
    base_commit_hash text DEFAULT ''::text NOT NULL,
    modified_files jsonb DEFAULT '[]'::jsonb NOT NULL,
    file_hashes jsonb,
    base_path_versions jsonb,
    author text DEFAULT ''::text NOT NULL,
    message text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE changesets (
    id text NOT NULL,
    hash text DEFAULT ''::text NOT NULL,
    slice_id text NOT NULL,
    base_commit_hash text DEFAULT ''::text NOT NULL,
    modified_files jsonb DEFAULT '[]'::jsonb NOT NULL,
    status integer DEFAULT 0 NOT NULL,
    author text DEFAULT ''::text NOT NULL,
    message text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    merged_at timestamp with time zone
);

CREATE TABLE ci_artifacts (
    id text NOT NULL,
    job_id text NOT NULL,
    run_id text NOT NULL,
    path text DEFAULT ''::text NOT NULL,
    object_key text DEFAULT ''::text NOT NULL,
    payload bytea,
    byte_count bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE ci_checks (
    changeset_id text NOT NULL,
    changeset_version_id text DEFAULT ''::text NOT NULL,
    plan_hash text DEFAULT ''::text NOT NULL,
    manifest_path text DEFAULT ''::text NOT NULL,
    job_key text DEFAULT ''::text NOT NULL,
    check_name text DEFAULT ''::text NOT NULL,
    required boolean DEFAULT false NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    run_id text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE ci_job_dependencies (
    run_id text NOT NULL,
    job_id text NOT NULL,
    depends_on_job_id text NOT NULL
);

CREATE TABLE ci_jobs (
    id text NOT NULL,
    run_id text NOT NULL,
    manifest_run_id text,
    manifest_path text DEFAULT ''::text NOT NULL,
    job_key text DEFAULT ''::text NOT NULL,
    check_name text DEFAULT ''::text NOT NULL,
    required boolean DEFAULT false NOT NULL,
    runner_pool text DEFAULT ''::text NOT NULL,
    image text DEFAULT ''::text NOT NULL,
    working_directory text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    runner_id text,
    lease_id text DEFAULT ''::text NOT NULL,
    lease_expires_at timestamp with time zone,
    exit_code integer DEFAULT 0 NOT NULL,
    infra_failure boolean DEFAULT false NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    shell text DEFAULT ''::text NOT NULL,
    timeout_seconds integer DEFAULT 0 NOT NULL,
    env jsonb DEFAULT '{}'::jsonb NOT NULL,
    cache_paths jsonb DEFAULT '[]'::jsonb NOT NULL,
    artifacts jsonb DEFAULT '[]'::jsonb NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 2 NOT NULL
);

CREATE TABLE ci_log_chunks (
    id text NOT NULL,
    job_id text NOT NULL,
    chunk_index bigint NOT NULL,
    stream text DEFAULT 'stdout'::text NOT NULL,
    object_key text DEFAULT ''::text NOT NULL,
    payload bytea,
    byte_count bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE ci_manifest_index (
    home_id text DEFAULT ''::text NOT NULL,
    home_commit_hash text DEFAULT ''::text NOT NULL,
    manifest_path text DEFAULT ''::text NOT NULL,
    manifest_dir text DEFAULT ''::text NOT NULL,
    manifest_hash text DEFAULT ''::text NOT NULL,
    watch_globs jsonb DEFAULT '[]'::jsonb NOT NULL,
    ignore_globs jsonb DEFAULT '[]'::jsonb NOT NULL,
    applies_to_globs jsonb DEFAULT '[]'::jsonb NOT NULL,
    parse_status text DEFAULT 'ok'::text NOT NULL,
    parse_error text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE ci_run_manifests (
    id text NOT NULL,
    run_id text NOT NULL,
    manifest_path text DEFAULT ''::text NOT NULL,
    manifest_dir text DEFAULT ''::text NOT NULL,
    manifest_hash text DEFAULT ''::text NOT NULL,
    matched_paths jsonb DEFAULT '[]'::jsonb NOT NULL,
    parse_status text DEFAULT 'ok'::text NOT NULL,
    parse_error text DEFAULT ''::text NOT NULL
);

CREATE TABLE ci_runner_registration_tokens (
    token_hash text NOT NULL,
    home_id text DEFAULT ''::text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    pool text DEFAULT ''::text NOT NULL,
    labels jsonb DEFAULT '[]'::jsonb NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_by_user_id text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    used_at timestamp with time zone
);

CREATE TABLE ci_runners (
    id text NOT NULL,
    home_id text DEFAULT ''::text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    pool text DEFAULT ''::text NOT NULL,
    labels jsonb DEFAULT '[]'::jsonb NOT NULL,
    status text DEFAULT 'offline'::text NOT NULL,
    token_hash text DEFAULT ''::text NOT NULL,
    version text DEFAULT ''::text NOT NULL,
    last_seen_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    disabled_at timestamp with time zone,
    executor text DEFAULT ''::text NOT NULL
);

CREATE TABLE ci_runs (
    id text NOT NULL,
    home_id text DEFAULT ''::text NOT NULL,
    slice_id text DEFAULT ''::text NOT NULL,
    changeset_id text NOT NULL,
    changeset_version_id text DEFAULT ''::text NOT NULL,
    base_commit_hash text DEFAULT ''::text NOT NULL,
    candidate_tree_hash text DEFAULT ''::text NOT NULL,
    platform_config_hash text DEFAULT ''::text NOT NULL,
    plan_hash text DEFAULT ''::text NOT NULL,
    attempt integer DEFAULT 1 NOT NULL,
    trigger_event text DEFAULT ''::text NOT NULL,
    triggered_by_user_id text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone
);

CREATE TABLE ci_steps (
    id text NOT NULL,
    job_id text NOT NULL,
    step_index integer NOT NULL,
    command text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    exit_code integer DEFAULT 0 NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone
);

CREATE TABLE commit_snapshots (
    commit_hash text NOT NULL,
    slice_id text NOT NULL,
    files jsonb DEFAULT '{}'::jsonb NOT NULL,
    committed_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE device_authorizations (
    device_code text NOT NULL,
    user_code text NOT NULL,
    username text,
    session_id text,
    device_info text DEFAULT ''::text NOT NULL,
    status text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    approved_at timestamp with time zone,
    denied_at timestamp with time zone
);

CREATE TABLE directory_entries (
    id text NOT NULL,
    slice_id text NOT NULL,
    path text NOT NULL,
    type text DEFAULT 'file'::text NOT NULL,
    parent_id text DEFAULT ''::text NOT NULL,
    content bytea,
    size bigint DEFAULT 0 NOT NULL,
    is_executable boolean DEFAULT false NOT NULL,
    symlink_target text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE environments (
    name text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    provider text DEFAULT 'local'::text NOT NULL,
    provider_id text DEFAULT ''::text NOT NULL,
    region text DEFAULT ''::text NOT NULL,
    created_by text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    default_agent_type text DEFAULT 'codex'::text NOT NULL,
    allowed_agent_types_json jsonb DEFAULT '["codex", "claude"]'::jsonb NOT NULL,
    provider_config_json jsonb DEFAULT '{}'::jsonb NOT NULL
);

CREATE TABLE file_changes (
    id text NOT NULL,
    slice_id text NOT NULL,
    commit_hash text DEFAULT ''::text NOT NULL,
    path text NOT NULL,
    old_path text DEFAULT ''::text NOT NULL,
    change_type text DEFAULT ''::text NOT NULL,
    old_hash text DEFAULT ''::text NOT NULL,
    new_hash text DEFAULT ''::text NOT NULL,
    lines_added integer DEFAULT 0 NOT NULL,
    lines_deleted integer DEFAULT 0 NOT NULL,
    author text DEFAULT ''::text NOT NULL,
    message text DEFAULT ''::text NOT NULL,
    committed_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE file_locks (
    file_id text NOT NULL,
    owner_slice_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE file_manifests (
    slice_id text NOT NULL,
    path text NOT NULL,
    hash text DEFAULT ''::text NOT NULL,
    total_size bigint DEFAULT 0 NOT NULL,
    block_count integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE file_slice_index (
    file_id text NOT NULL,
    slice_id text NOT NULL
);

CREATE TABLE global_commits (
    seq bigint NOT NULL,
    commit_hash text NOT NULL,
    committed_at timestamp with time zone DEFAULT now() NOT NULL,
    merged_slice_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE SEQUENCE global_commits_seq_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE global_commits_seq_seq OWNED BY global_commits.seq;

CREATE TABLE global_state (
    id boolean DEFAULT true NOT NULL,
    root_id text DEFAULT ''::text,
    global_commit_hash text DEFAULT 'cmt_init_root'::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    state_json jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT global_state_id_check CHECK (id)
);

CREATE TABLE home_path_heads (
    home_id text NOT NULL,
    path text NOT NULL,
    path_version bigint DEFAULT 1 NOT NULL,
    content_hash text DEFAULT ''::text NOT NULL,
    manifest_hash text DEFAULT ''::text NOT NULL,
    source_slice_id text DEFAULT ''::text NOT NULL,
    source_commit_hash text DEFAULT ''::text NOT NULL,
    last_merge_seq bigint DEFAULT 0 NOT NULL,
    deleted boolean DEFAULT false NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT home_path_heads_last_merge_seq_check CHECK ((last_merge_seq >= 0)),
    CONSTRAINT home_path_heads_path_version_check CHECK ((path_version >= 0))
);

CREATE TABLE merge_event_shard_sequences (
    shard_id integer NOT NULL,
    next_seq bigint DEFAULT 1 NOT NULL,
    CONSTRAINT merge_event_shard_sequences_next_seq_check CHECK ((next_seq >= 1))
);

CREATE TABLE merge_events (
    home_id text NOT NULL,
    shard_id integer NOT NULL,
    merge_seq bigint NOT NULL,
    event_id text NOT NULL,
    changeset_id text NOT NULL,
    source_slice_id text NOT NULL,
    source_commit_hash text NOT NULL,
    author text NOT NULL,
    message text DEFAULT ''::text NOT NULL,
    touched_paths jsonb DEFAULT '[]'::jsonb NOT NULL,
    path_updates jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    forced boolean DEFAULT false NOT NULL,
    force_reason text DEFAULT ''::text NOT NULL,
    forced_by text DEFAULT ''::text NOT NULL
);

CREATE TABLE organization_invites (
    invite_id text NOT NULL,
    org_slug text NOT NULL,
    target_email text NOT NULL,
    role text DEFAULT 'member'::text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    created_by text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE organization_members (
    org_slug text NOT NULL,
    username text NOT NULL,
    role text DEFAULT 'member'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE organizations (
    slug text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    created_by text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    root_path text DEFAULT ''::text NOT NULL
);

CREATE TABLE path_visibility (
    path text NOT NULL,
    entry_type text DEFAULT 'file'::text NOT NULL,
    visibility text DEFAULT 'private'::text NOT NULL,
    updated_by text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE projection_offsets (
    projection_name text NOT NULL,
    shard_id integer NOT NULL,
    merge_seq bigint DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE slice_commits (
    slice_id text NOT NULL,
    seq bigint NOT NULL,
    commit_hash text NOT NULL,
    parent_hash text DEFAULT ''::text NOT NULL,
    message text DEFAULT ''::text NOT NULL,
    committed_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE SEQUENCE slice_commits_seq_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE slice_commits_seq_seq OWNED BY slice_commits.seq;

CREATE TABLE slice_locks (
    slice_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE slice_metadata (
    slice_id text NOT NULL,
    head_commit_hash text DEFAULT ''::text NOT NULL,
    modified_files jsonb DEFAULT '[]'::jsonb NOT NULL,
    last_modified timestamp with time zone DEFAULT now() NOT NULL,
    modified_files_count integer DEFAULT 0 NOT NULL
);

CREATE TABLE slices (
    id text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    slug text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    created_by text DEFAULT ''::text NOT NULL,
    parent_id text,
    is_root boolean DEFAULT false NOT NULL,
    files jsonb DEFAULT '[]'::jsonb NOT NULL,
    folder_mounts jsonb DEFAULT '[]'::jsonb NOT NULL,
    owners jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    environment text DEFAULT ''::text NOT NULL,
    visibility text DEFAULT 'private'::text NOT NULL
);

CREATE TABLE team_members (
    team_id text NOT NULL,
    username text NOT NULL,
    added_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE teams (
    team_id text NOT NULL,
    org_slug text NOT NULL,
    name text NOT NULL,
    created_by text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE users (
    username text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    primary_email text DEFAULT ''::text NOT NULL,
    password_hash text DEFAULT ''::text NOT NULL,
    root_path text DEFAULT ''::text NOT NULL,
    account_id text,
    auth_source text DEFAULT ''::text NOT NULL,
    clerk_user_id text DEFAULT ''::text NOT NULL
);

ALTER TABLE ONLY agent_session_audit ALTER COLUMN id SET DEFAULT nextval('agent_session_audit_id_seq'::regclass);

ALTER TABLE ONLY global_commits ALTER COLUMN seq SET DEFAULT nextval('global_commits_seq_seq'::regclass);

ALTER TABLE ONLY slice_commits ALTER COLUMN seq SET DEFAULT nextval('slice_commits_seq_seq'::regclass);

ALTER TABLE ONLY accounts
    ADD CONSTRAINT accounts_pkey PRIMARY KEY (account_id);

ALTER TABLE ONLY agent_key_challenges
    ADD CONSTRAINT agent_key_challenges_pkey PRIMARY KEY (challenge_id);

ALTER TABLE ONLY agent_keys
    ADD CONSTRAINT agent_keys_fingerprint_key UNIQUE (fingerprint);

ALTER TABLE ONLY agent_keys
    ADD CONSTRAINT agent_keys_pkey PRIMARY KEY (key_id);

ALTER TABLE ONLY agent_session_audit
    ADD CONSTRAINT agent_session_audit_pkey PRIMARY KEY (id);

ALTER TABLE ONLY agent_session_changesets
    ADD CONSTRAINT agent_session_changesets_pkey PRIMARY KEY (session_id, changeset_id, snapshot_id);

ALTER TABLE ONLY agent_session_events
    ADD CONSTRAINT agent_session_events_pkey PRIMARY KEY (session_id, seq);

ALTER TABLE ONLY agent_sessions
    ADD CONSTRAINT agent_sessions_pkey PRIMARY KEY (session_id);

ALTER TABLE ONLY agent_runners
    ADD CONSTRAINT agent_runners_pkey PRIMARY KEY (runner_id);

ALTER TABLE ONLY auth_sessions
    ADD CONSTRAINT auth_sessions_pkey PRIMARY KEY (session_id);

ALTER TABLE ONLY auth_sessions
    ADD CONSTRAINT auth_sessions_token_key UNIQUE (token);

ALTER TABLE ONLY changeset_snapshots
    ADD CONSTRAINT changeset_snapshots_changeset_id_version_key UNIQUE (changeset_id, version);

ALTER TABLE ONLY changeset_snapshots
    ADD CONSTRAINT changeset_snapshots_pkey PRIMARY KEY (id);

ALTER TABLE ONLY changesets
    ADD CONSTRAINT changesets_pkey PRIMARY KEY (id);

ALTER TABLE ONLY ci_artifacts
    ADD CONSTRAINT ci_artifacts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY ci_checks
    ADD CONSTRAINT ci_checks_pkey PRIMARY KEY (changeset_id, changeset_version_id, plan_hash, manifest_path, job_key);

ALTER TABLE ONLY ci_job_dependencies
    ADD CONSTRAINT ci_job_dependencies_pkey PRIMARY KEY (job_id, depends_on_job_id);

ALTER TABLE ONLY ci_jobs
    ADD CONSTRAINT ci_jobs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY ci_jobs
    ADD CONSTRAINT ci_jobs_run_id_manifest_path_job_key_key UNIQUE (run_id, manifest_path, job_key);

ALTER TABLE ONLY ci_log_chunks
    ADD CONSTRAINT ci_log_chunks_job_id_chunk_index_key UNIQUE (job_id, chunk_index);

ALTER TABLE ONLY ci_log_chunks
    ADD CONSTRAINT ci_log_chunks_pkey PRIMARY KEY (id);

ALTER TABLE ONLY ci_manifest_index
    ADD CONSTRAINT ci_manifest_index_pkey PRIMARY KEY (home_id, home_commit_hash, manifest_path);

ALTER TABLE ONLY ci_run_manifests
    ADD CONSTRAINT ci_run_manifests_pkey PRIMARY KEY (id);

ALTER TABLE ONLY ci_run_manifests
    ADD CONSTRAINT ci_run_manifests_run_id_manifest_path_key UNIQUE (run_id, manifest_path);

ALTER TABLE ONLY ci_runner_registration_tokens
    ADD CONSTRAINT ci_runner_registration_tokens_pkey PRIMARY KEY (token_hash);

ALTER TABLE ONLY ci_runners
    ADD CONSTRAINT ci_runners_pkey PRIMARY KEY (id);

ALTER TABLE ONLY ci_runs
    ADD CONSTRAINT ci_runs_changeset_id_changeset_version_id_plan_hash_trigger_key UNIQUE (changeset_id, changeset_version_id, plan_hash, trigger_event, attempt);

ALTER TABLE ONLY ci_runs
    ADD CONSTRAINT ci_runs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY ci_steps
    ADD CONSTRAINT ci_steps_job_id_step_index_key UNIQUE (job_id, step_index);

ALTER TABLE ONLY ci_steps
    ADD CONSTRAINT ci_steps_pkey PRIMARY KEY (id);

ALTER TABLE ONLY commit_snapshots
    ADD CONSTRAINT commit_snapshots_pkey PRIMARY KEY (commit_hash);

ALTER TABLE ONLY device_authorizations
    ADD CONSTRAINT device_authorizations_pkey PRIMARY KEY (device_code);

ALTER TABLE ONLY device_authorizations
    ADD CONSTRAINT device_authorizations_user_code_key UNIQUE (user_code);

ALTER TABLE ONLY directory_entries
    ADD CONSTRAINT directory_entries_pkey PRIMARY KEY (id);

ALTER TABLE ONLY environments
    ADD CONSTRAINT environments_pkey PRIMARY KEY (name);

ALTER TABLE ONLY file_changes
    ADD CONSTRAINT file_changes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY file_locks
    ADD CONSTRAINT file_locks_pkey PRIMARY KEY (file_id);

ALTER TABLE ONLY file_manifests
    ADD CONSTRAINT file_manifests_pkey PRIMARY KEY (slice_id, path);

ALTER TABLE ONLY file_slice_index
    ADD CONSTRAINT file_slice_index_pkey PRIMARY KEY (file_id, slice_id);

ALTER TABLE ONLY global_commits
    ADD CONSTRAINT global_commits_pkey PRIMARY KEY (seq);

ALTER TABLE ONLY global_state
    ADD CONSTRAINT global_state_pkey PRIMARY KEY (id);

ALTER TABLE ONLY home_path_heads
    ADD CONSTRAINT home_path_heads_pkey PRIMARY KEY (home_id, path);

ALTER TABLE ONLY merge_event_shard_sequences
    ADD CONSTRAINT merge_event_shard_sequences_pkey PRIMARY KEY (shard_id);

ALTER TABLE ONLY merge_events
    ADD CONSTRAINT merge_events_changeset_id_key UNIQUE (changeset_id);

ALTER TABLE ONLY merge_events
    ADD CONSTRAINT merge_events_pkey PRIMARY KEY (shard_id, merge_seq);

ALTER TABLE ONLY organization_invites
    ADD CONSTRAINT organization_invites_pkey PRIMARY KEY (invite_id);

ALTER TABLE ONLY organization_members
    ADD CONSTRAINT organization_members_pkey PRIMARY KEY (org_slug, username);

ALTER TABLE ONLY organizations
    ADD CONSTRAINT organizations_pkey PRIMARY KEY (slug);

ALTER TABLE ONLY path_visibility
    ADD CONSTRAINT path_visibility_pkey PRIMARY KEY (path);

ALTER TABLE ONLY projection_offsets
    ADD CONSTRAINT projection_offsets_pkey PRIMARY KEY (projection_name, shard_id);

ALTER TABLE ONLY slice_commits
    ADD CONSTRAINT slice_commits_pkey PRIMARY KEY (slice_id, seq);

ALTER TABLE ONLY slice_locks
    ADD CONSTRAINT slice_locks_pkey PRIMARY KEY (slice_id);

ALTER TABLE ONLY slice_metadata
    ADD CONSTRAINT slice_metadata_pkey PRIMARY KEY (slice_id);

ALTER TABLE ONLY slices
    ADD CONSTRAINT slices_pkey PRIMARY KEY (id);

ALTER TABLE ONLY team_members
    ADD CONSTRAINT team_members_pkey PRIMARY KEY (team_id, username);

ALTER TABLE ONLY teams
    ADD CONSTRAINT teams_pkey PRIMARY KEY (team_id);

ALTER TABLE ONLY users
    ADD CONSTRAINT users_pkey PRIMARY KEY (username);

CREATE UNIQUE INDEX idx_accounts_claim_token_hash_unique ON accounts USING btree (claim_token_hash) WHERE (claim_token_hash <> ''::text);

CREATE INDEX idx_agent_key_challenges_expires_at ON agent_key_challenges USING btree (expires_at);

CREATE INDEX idx_agent_key_challenges_key ON agent_key_challenges USING btree (agent_key_id);

CREATE INDEX idx_agent_keys_username ON agent_keys USING btree (username);

CREATE INDEX idx_agent_keys_username_active ON agent_keys USING btree (username) WHERE (revoked_at IS NULL);

CREATE INDEX idx_agent_session_audit_session_created ON agent_session_audit USING btree (session_id, created_at DESC);

CREATE INDEX idx_agent_session_changesets_changeset_exported ON agent_session_changesets USING btree (changeset_id, exported_at DESC);

CREATE INDEX idx_agent_session_changesets_session_exported ON agent_session_changesets USING btree (session_id, exported_at DESC);

CREATE INDEX idx_agent_session_changesets_snapshot ON agent_session_changesets USING btree (snapshot_id);

CREATE INDEX idx_agent_session_events_ts ON agent_session_events USING btree (session_id, ts DESC);
CREATE INDEX idx_agent_session_events_kind ON agent_session_events USING btree (session_id, kind, seq);

CREATE UNIQUE INDEX idx_agent_sessions_e2b_sandbox ON agent_sessions USING btree (e2b_sandbox_id) WHERE (e2b_sandbox_id IS NOT NULL);

CREATE INDEX idx_agent_sessions_runtime_session_id ON agent_sessions USING btree (runtime_session_id) WHERE (runtime_session_id <> ''::text);

CREATE INDEX idx_agent_sessions_runner_id ON agent_sessions USING btree (runner_id) WHERE (runner_id <> ''::text);

CREATE INDEX idx_agent_sessions_slice_created ON agent_sessions USING btree (slice_id, created_at DESC);

CREATE INDEX idx_agent_sessions_state_updated ON agent_sessions USING btree (state, updated_at DESC);

CREATE INDEX idx_agent_sessions_user_created ON agent_sessions USING btree (user_id, created_at DESC);

CREATE INDEX idx_agent_runners_user_heartbeat ON agent_runners USING btree (user_id, last_heartbeat_at DESC);

CREATE INDEX idx_auth_sessions_agent_key_id ON auth_sessions USING btree (agent_key_id) WHERE (agent_key_id IS NOT NULL);

CREATE INDEX idx_auth_sessions_refresh_token_active ON auth_sessions USING btree (refresh_token) WHERE ((refresh_token IS NOT NULL) AND (revoked_at IS NULL));

CREATE UNIQUE INDEX idx_auth_sessions_refresh_token_unique ON auth_sessions USING btree (refresh_token) WHERE (refresh_token IS NOT NULL);

CREATE INDEX idx_auth_sessions_token_active ON auth_sessions USING btree (token) WHERE (revoked_at IS NULL);

CREATE INDEX idx_auth_sessions_username ON auth_sessions USING btree (username);

CREATE INDEX idx_changeset_snapshots_changeset ON changeset_snapshots USING btree (changeset_id, version DESC);

CREATE INDEX idx_changesets_slice ON changesets USING btree (slice_id);

CREATE INDEX idx_changesets_slice_status ON changesets USING btree (slice_id, status);

CREATE INDEX idx_ci_artifacts_job_created ON ci_artifacts USING btree (job_id, created_at);

CREATE INDEX idx_ci_artifacts_run_created ON ci_artifacts USING btree (run_id, created_at);

CREATE INDEX idx_ci_checks_changeset_version ON ci_checks USING btree (changeset_id, changeset_version_id, plan_hash);

CREATE INDEX idx_ci_checks_run ON ci_checks USING btree (run_id);

CREATE INDEX idx_ci_job_dependencies_run ON ci_job_dependencies USING btree (run_id);

CREATE INDEX idx_ci_jobs_lease_expiry ON ci_jobs USING btree (status, lease_expires_at) WHERE (lease_expires_at IS NOT NULL);

CREATE INDEX idx_ci_jobs_pool_status ON ci_jobs USING btree (runner_pool, status, id);

CREATE INDEX idx_ci_jobs_run_status ON ci_jobs USING btree (run_id, status);

CREATE INDEX idx_ci_jobs_runner ON ci_jobs USING btree (runner_id, started_at DESC);

CREATE INDEX idx_ci_jobs_running_lease_expiry ON ci_jobs USING btree (lease_expires_at) WHERE ((status = 'running'::text) AND (lease_expires_at IS NOT NULL));

CREATE INDEX idx_ci_log_chunks_job_chunk ON ci_log_chunks USING btree (job_id, chunk_index);

CREATE INDEX idx_ci_manifest_index_home_commit ON ci_manifest_index USING btree (home_id, home_commit_hash);

CREATE INDEX idx_ci_run_manifests_run ON ci_run_manifests USING btree (run_id);

CREATE INDEX idx_ci_runner_registration_tokens_home ON ci_runner_registration_tokens USING btree (home_id, expires_at DESC);

CREATE INDEX idx_ci_runners_home_pool_status ON ci_runners USING btree (home_id, pool, status);

CREATE INDEX idx_ci_runners_last_seen ON ci_runners USING btree (last_seen_at DESC);

CREATE INDEX idx_ci_runs_changeset_version ON ci_runs USING btree (changeset_id, changeset_version_id);

CREATE INDEX idx_ci_runs_home_status ON ci_runs USING btree (home_id, status, created_at DESC);

CREATE INDEX idx_ci_runs_status_created ON ci_runs USING btree (status, created_at);

CREATE INDEX idx_ci_steps_job ON ci_steps USING btree (job_id, step_index);

CREATE INDEX idx_device_authorizations_expires_at ON device_authorizations USING btree (expires_at);

CREATE INDEX idx_device_authorizations_user_code ON device_authorizations USING btree (user_code);

CREATE INDEX idx_directory_entries_parent ON directory_entries USING btree (parent_id);

CREATE INDEX idx_directory_entries_slice_parent ON directory_entries USING btree (slice_id, parent_id);

CREATE UNIQUE INDEX idx_directory_entries_slice_path ON directory_entries USING btree (slice_id, path);

CREATE INDEX idx_directory_entries_slice_path_pattern ON directory_entries USING btree (slice_id, path text_pattern_ops);

CREATE INDEX idx_environments_provider ON environments USING btree (provider);

CREATE INDEX idx_file_changes_commit ON file_changes USING btree (commit_hash);

CREATE UNIQUE INDEX idx_file_changes_slice_commit_path ON file_changes USING btree (slice_id, commit_hash, path);

CREATE INDEX idx_file_changes_slice_path_time ON file_changes USING btree (slice_id, path, committed_at DESC);

CREATE INDEX idx_file_changes_slice_time ON file_changes USING btree (slice_id, committed_at DESC);

CREATE INDEX idx_file_slice_index_slice ON file_slice_index USING btree (slice_id);

CREATE UNIQUE INDEX idx_global_commits_commit_hash ON global_commits USING btree (commit_hash);

CREATE INDEX idx_global_commits_committed_at_desc ON global_commits USING btree (committed_at DESC, seq DESC);

CREATE INDEX idx_global_commits_seq_desc ON global_commits USING btree (seq DESC);

CREATE INDEX idx_org_members_username ON organization_members USING btree (username);

CREATE INDEX idx_organization_invites_org ON organization_invites USING btree (org_slug);

CREATE UNIQUE INDEX idx_organization_invites_pending_email_unique ON organization_invites USING btree (org_slug, lower(target_email)) WHERE (status = 'pending'::text);

CREATE UNIQUE INDEX idx_organizations_root_path_unique ON organizations USING btree (root_path);

CREATE INDEX idx_path_visibility_path_prefix ON path_visibility USING btree (path);

CREATE UNIQUE INDEX idx_slice_commits_hash ON slice_commits USING btree (slice_id, commit_hash);

CREATE INDEX idx_slice_commits_slice_seq_desc ON slice_commits USING btree (slice_id, seq DESC);

CREATE UNIQUE INDEX idx_slices_owner_slug ON slices USING btree (created_by, slug);

CREATE INDEX idx_slices_slug ON slices USING btree (slug);

CREATE INDEX idx_team_members_username ON team_members USING btree (username);

CREATE INDEX idx_teams_org ON teams USING btree (org_slug);

CREATE UNIQUE INDEX idx_teams_org_name_unique ON teams USING btree (org_slug, lower(name));

CREATE UNIQUE INDEX idx_users_clerk_user_id_unique ON users USING btree (clerk_user_id) WHERE (clerk_user_id <> ''::text);

CREATE UNIQUE INDEX idx_users_primary_email_unique ON users USING btree (lower(primary_email)) WHERE (primary_email <> ''::text);

CREATE UNIQUE INDEX idx_users_root_path_unique ON users USING btree (root_path);

ALTER TABLE ONLY agent_key_challenges
    ADD CONSTRAINT agent_key_challenges_agent_key_id_fkey FOREIGN KEY (agent_key_id) REFERENCES agent_keys(key_id) ON DELETE CASCADE;

ALTER TABLE ONLY auth_sessions
    ADD CONSTRAINT auth_sessions_agent_key_id_fkey FOREIGN KEY (agent_key_id) REFERENCES agent_keys(key_id) ON DELETE SET NULL;

ALTER TABLE ONLY auth_sessions
    ADD CONSTRAINT auth_sessions_username_fkey FOREIGN KEY (username) REFERENCES users(username);

ALTER TABLE ONLY agent_session_changesets
    ADD CONSTRAINT agent_session_changesets_changeset_id_fkey FOREIGN KEY (changeset_id) REFERENCES changesets(id) ON DELETE CASCADE;

ALTER TABLE ONLY agent_session_changesets
    ADD CONSTRAINT agent_session_changesets_session_id_fkey FOREIGN KEY (session_id) REFERENCES agent_sessions(session_id) ON DELETE CASCADE;

ALTER TABLE ONLY agent_session_changesets
    ADD CONSTRAINT agent_session_changesets_snapshot_id_fkey FOREIGN KEY (snapshot_id) REFERENCES changeset_snapshots(id) ON DELETE CASCADE;

ALTER TABLE ONLY changeset_snapshots
    ADD CONSTRAINT changeset_snapshots_changeset_id_fkey FOREIGN KEY (changeset_id) REFERENCES changesets(id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE ONLY changesets
    ADD CONSTRAINT changesets_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE ONLY ci_artifacts
    ADD CONSTRAINT ci_artifacts_job_id_fkey FOREIGN KEY (job_id) REFERENCES ci_jobs(id) ON DELETE CASCADE;

ALTER TABLE ONLY ci_artifacts
    ADD CONSTRAINT ci_artifacts_run_id_fkey FOREIGN KEY (run_id) REFERENCES ci_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY ci_checks
    ADD CONSTRAINT ci_checks_changeset_id_fkey FOREIGN KEY (changeset_id) REFERENCES changesets(id) ON DELETE CASCADE;

ALTER TABLE ONLY ci_checks
    ADD CONSTRAINT ci_checks_run_id_fkey FOREIGN KEY (run_id) REFERENCES ci_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY ci_job_dependencies
    ADD CONSTRAINT ci_job_dependencies_depends_on_job_id_fkey FOREIGN KEY (depends_on_job_id) REFERENCES ci_jobs(id) ON DELETE CASCADE;

ALTER TABLE ONLY ci_job_dependencies
    ADD CONSTRAINT ci_job_dependencies_job_id_fkey FOREIGN KEY (job_id) REFERENCES ci_jobs(id) ON DELETE CASCADE;

ALTER TABLE ONLY ci_job_dependencies
    ADD CONSTRAINT ci_job_dependencies_run_id_fkey FOREIGN KEY (run_id) REFERENCES ci_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY ci_jobs
    ADD CONSTRAINT ci_jobs_manifest_run_id_fkey FOREIGN KEY (manifest_run_id) REFERENCES ci_run_manifests(id) ON DELETE CASCADE;

ALTER TABLE ONLY ci_jobs
    ADD CONSTRAINT ci_jobs_run_id_fkey FOREIGN KEY (run_id) REFERENCES ci_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY ci_jobs
    ADD CONSTRAINT ci_jobs_runner_id_fkey FOREIGN KEY (runner_id) REFERENCES ci_runners(id) ON DELETE SET NULL;

ALTER TABLE ONLY ci_log_chunks
    ADD CONSTRAINT ci_log_chunks_job_id_fkey FOREIGN KEY (job_id) REFERENCES ci_jobs(id) ON DELETE CASCADE;

ALTER TABLE ONLY ci_run_manifests
    ADD CONSTRAINT ci_run_manifests_run_id_fkey FOREIGN KEY (run_id) REFERENCES ci_runs(id) ON DELETE CASCADE;

ALTER TABLE ONLY ci_runs
    ADD CONSTRAINT ci_runs_changeset_id_fkey FOREIGN KEY (changeset_id) REFERENCES changesets(id) ON DELETE CASCADE;

ALTER TABLE ONLY ci_steps
    ADD CONSTRAINT ci_steps_job_id_fkey FOREIGN KEY (job_id) REFERENCES ci_jobs(id) ON DELETE CASCADE;

ALTER TABLE ONLY file_locks
    ADD CONSTRAINT file_locks_owner_slice_id_fkey FOREIGN KEY (owner_slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE ONLY file_manifests
    ADD CONSTRAINT file_manifests_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE ONLY file_slice_index
    ADD CONSTRAINT file_slice_index_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE ONLY organization_invites
    ADD CONSTRAINT organization_invites_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(username) ON DELETE CASCADE;

ALTER TABLE ONLY organization_invites
    ADD CONSTRAINT organization_invites_org_slug_fkey FOREIGN KEY (org_slug) REFERENCES organizations(slug) ON DELETE CASCADE;

ALTER TABLE ONLY organization_members
    ADD CONSTRAINT organization_members_org_slug_fkey FOREIGN KEY (org_slug) REFERENCES organizations(slug);

ALTER TABLE ONLY organization_members
    ADD CONSTRAINT organization_members_username_fkey FOREIGN KEY (username) REFERENCES users(username);

ALTER TABLE ONLY slice_commits
    ADD CONSTRAINT slice_commits_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE ONLY slice_locks
    ADD CONSTRAINT slice_locks_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE ONLY slice_metadata
    ADD CONSTRAINT slice_metadata_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE ONLY team_members
    ADD CONSTRAINT team_members_team_id_fkey FOREIGN KEY (team_id) REFERENCES teams(team_id) ON DELETE CASCADE;

ALTER TABLE ONLY team_members
    ADD CONSTRAINT team_members_username_fkey FOREIGN KEY (username) REFERENCES users(username) ON DELETE CASCADE;

ALTER TABLE ONLY teams
    ADD CONSTRAINT teams_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(username) ON DELETE CASCADE;

ALTER TABLE ONLY teams
    ADD CONSTRAINT teams_org_slug_fkey FOREIGN KEY (org_slug) REFERENCES organizations(slug) ON DELETE CASCADE;

ALTER TABLE ONLY users
    ADD CONSTRAINT users_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(account_id) ON DELETE SET NULL;
