# Agent Session State Provenance Design

## Implementation Status

- Current status: `ongoing`
- Last updated: `2026-05-12`

## Executive Summary

Agent sessions need a durable way to explain how user input and agent work moved
a workspace from one state hash to another. The chat/tool event log is useful for
replay, but it is too verbose and too loosely structured to answer product
questions such as:

- What commit did this agent session start from?
- Which user input caused the agent to produce this new snapshot?
- Which changeset or commit did this session create?
- Which agent session created or updated this changeset?
- Which agent session produced this commit?

This design adds a queryable provenance layer on top of the existing
`agent_sessions`, `agent_session_events`, `changesets`, `changeset_snapshots`,
`slice_commits`, and `merge_events` tables.

The core model is:

```text
agent session starts at commit A
  user input event N asks for work
  agent emits verbose events
  agent produces commit B and/or changeset snapshot S
  durable transition records A -> B and links session -> artifact
```

`agent_session_events` remains the append-only conversation/tool timeline.
New provenance tables become the compact source of truth for UI queries,
auditing, and artifact navigation.

## Goals

1. Capture the starting state hash for every agent session.
2. Capture each meaningful state transition produced by user input and agent work.
3. Associate agent sessions with changesets, changeset snapshots, and slice commits.
4. Support UI navigation from session -> artifacts and artifact -> sessions.
5. Preserve existing changeset merge authority in `merge_events`.
6. Keep writes idempotent so local runners and remote runtimes can retry safely.
7. Avoid overloading `agent_session_events` with query-only concerns.

## Non-Goals

1. Replacing the existing agent event protocol.
2. Replacing `merge_events` as the authority for merged changeset -> commit facts.
3. Implementing a full DAG/branching workspace graph in v1.
4. Storing file diffs in the provenance tables. Diffs stay derived from commit and
   changeset data.
5. Exposing arbitrary public mutation endpoints for clients to forge provenance.

## Existing Model

Relevant tables today:

- `agent_sessions`: session lifecycle, runtime metadata, owning slice/user.
- `agent_session_events`: durable event log ordered by `(session_id, seq)`.
- `changesets`: pending/merged change list metadata.
- `changeset_snapshots`: exported versions of a changeset.
- `slice_commits`: per-slice commit history keyed by `(slice_id, seq)` and
  uniquely indexed by `(slice_id, commit_hash)`.
- `merge_events`: immutable accepted merge facts, including
  `changeset_id`, `source_slice_id`, and `source_commit_hash`.

Current schema already supports the authoritative merged mapping:

```text
changesets.id
  -> merge_events.changeset_id
  -> merge_events.source_slice_id + merge_events.source_commit_hash
  -> slice_commits(slice_id, commit_hash)
```

The missing piece is the session provenance that says which agent session caused
or contributed to those artifacts.

## Data Model

### Session Base And Current State

Add state hash fields to `agent_sessions`.

```sql
ALTER TABLE agent_sessions
  ADD COLUMN base_slice_id text DEFAULT '' NOT NULL,
  ADD COLUMN base_commit_hash text DEFAULT '' NOT NULL,
  ADD COLUMN current_slice_id text DEFAULT '' NOT NULL,
  ADD COLUMN current_commit_hash text DEFAULT '' NOT NULL;

CREATE INDEX idx_agent_sessions_base_commit
  ON agent_sessions (base_slice_id, base_commit_hash)
  WHERE base_commit_hash <> '';

CREATE INDEX idx_agent_sessions_current_commit
  ON agent_sessions (current_slice_id, current_commit_hash)
  WHERE current_commit_hash <> '';
```

Semantics:

- `base_*` is set once when the session starts or attaches to a workspace.
- `current_*` is updated when the session records a successful state transition.
- Empty values mean the session predates this feature or was created before the
  slice head could be resolved.

The base state should usually be the current `slice_metadata.head_commit_hash`
for `agent_sessions.slice_id` at session creation time.

### State Transitions

Add an append-only table for meaningful workspace transitions.

```sql
CREATE TABLE agent_session_state_transitions (
    transition_id text PRIMARY KEY,
    session_id text NOT NULL,
    seq bigint NOT NULL,
    turn_id text DEFAULT '' NOT NULL,

    from_slice_id text NOT NULL,
    from_commit_hash text NOT NULL,
    to_slice_id text NOT NULL,
    to_commit_hash text NOT NULL,

    changeset_id text,
    changeset_version_id text DEFAULT '' NOT NULL,
    changeset_snapshot_hash text DEFAULT '' NOT NULL,

    trigger_event_seq bigint,
    completion_event_seq bigint,
    relationship text NOT NULL,
    summary text DEFAULT '' NOT NULL,
    metadata_json jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,

    UNIQUE (session_id, seq),
    FOREIGN KEY (session_id) REFERENCES agent_sessions(session_id) ON DELETE CASCADE,
    FOREIGN KEY (from_slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (to_slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (changeset_id) REFERENCES changesets(id) ON UPDATE CASCADE ON DELETE SET NULL,
    FOREIGN KEY (session_id, trigger_event_seq)
      REFERENCES agent_session_events(session_id, seq) ON DELETE SET NULL,
    FOREIGN KEY (session_id, completion_event_seq)
      REFERENCES agent_session_events(session_id, seq) ON DELETE SET NULL
);

CREATE INDEX idx_agent_session_transitions_session_seq
  ON agent_session_state_transitions (session_id, seq DESC);

CREATE INDEX idx_agent_session_transitions_to_commit
  ON agent_session_state_transitions (to_slice_id, to_commit_hash, created_at DESC);

CREATE INDEX idx_agent_session_transitions_changeset
  ON agent_session_state_transitions (changeset_id, created_at DESC)
  WHERE changeset_id IS NOT NULL;
```

Relationships:

- `input_applied`: a user input resulted in a workspace state update.
- `agent_commit`: the agent created a new slice commit.
- `changeset_snapshot`: the agent exported or updated a changeset snapshot.
- `merge_source`: a merged changeset used a session-produced commit.
- `manual_link`: an operator or repair job associated existing artifacts.

The transition table is intentionally not a full event log. It only records
durable state changes that should appear as milestones in the UI.

### Session To Changeset Links

Add a many-to-many provenance table.

```sql
CREATE TABLE agent_session_changesets (
    session_id text NOT NULL,
    changeset_id text NOT NULL,
    relationship text DEFAULT 'created' NOT NULL,
    source_event_seq bigint,
    transition_id text,
    metadata_json jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,

    PRIMARY KEY (session_id, changeset_id, relationship),
    FOREIGN KEY (session_id) REFERENCES agent_sessions(session_id) ON DELETE CASCADE,
    FOREIGN KEY (changeset_id) REFERENCES changesets(id) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (transition_id) REFERENCES agent_session_state_transitions(transition_id) ON DELETE SET NULL,
    FOREIGN KEY (session_id, source_event_seq)
      REFERENCES agent_session_events(session_id, seq) ON DELETE SET NULL
);

CREATE INDEX idx_agent_session_changesets_changeset
  ON agent_session_changesets (changeset_id, created_at DESC);

CREATE INDEX idx_agent_session_changesets_session_created
  ON agent_session_changesets (session_id, created_at DESC);
```

Relationship values:

- `created`: session created the changeset.
- `updated`: session changed an existing changeset.
- `reviewed`: session analyzed or commented on the changeset.
- `merged`: session initiated or completed the merge.
- `mentioned`: session referenced the changeset but did not mutate it.

### Session To Slice Commit Links

Add a many-to-many table for commit provenance. The commit identity must include
`slice_id` because slice commit uniqueness is scoped by `(slice_id, commit_hash)`.

```sql
CREATE TABLE agent_session_slice_commits (
    session_id text NOT NULL,
    slice_id text NOT NULL,
    commit_hash text NOT NULL,
    relationship text DEFAULT 'created' NOT NULL,
    changeset_id text,
    source_event_seq bigint,
    transition_id text,
    metadata_json jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,

    PRIMARY KEY (session_id, slice_id, commit_hash, relationship),
    FOREIGN KEY (session_id) REFERENCES agent_sessions(session_id) ON DELETE CASCADE,
    FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (slice_id, commit_hash) REFERENCES slice_commits(slice_id, commit_hash) ON DELETE CASCADE,
    FOREIGN KEY (changeset_id) REFERENCES changesets(id) ON UPDATE CASCADE ON DELETE SET NULL,
    FOREIGN KEY (transition_id) REFERENCES agent_session_state_transitions(transition_id) ON DELETE SET NULL,
    FOREIGN KEY (session_id, source_event_seq)
      REFERENCES agent_session_events(session_id, seq) ON DELETE SET NULL
);

CREATE INDEX idx_agent_session_slice_commits_commit
  ON agent_session_slice_commits (slice_id, commit_hash, created_at DESC);

CREATE INDEX idx_agent_session_slice_commits_changeset
  ON agent_session_slice_commits (changeset_id, created_at DESC)
  WHERE changeset_id IS NOT NULL;

CREATE INDEX idx_agent_session_slice_commits_session_created
  ON agent_session_slice_commits (session_id, created_at DESC);
```

Relationship values:

- `created`: session created this commit.
- `amended`: session replaced or amended a previous commit.
- `based_on`: session used this commit as an input/base.
- `referenced`: session mentioned or inspected this commit.
- `merge_source`: commit became the source commit for a merged changeset.

## Go Models

Add models under `internal/models`.

```go
type AgentSessionStateTransition struct {
    TransitionID          string
    SessionID             string
    Seq                   uint64
    TurnID                string
    FromSliceID           string
    FromCommitHash        string
    ToSliceID             string
    ToCommitHash          string
    ChangesetID           string
    ChangesetVersionID    string
    ChangesetSnapshotHash string
    TriggerEventSeq       uint64
    CompletionEventSeq    uint64
    Relationship          string
    Summary               string
    Metadata              json.RawMessage
    CreatedAt             time.Time
}

type AgentSessionChangesetLink struct {
    SessionID      string
    ChangesetID    string
    Relationship   string
    SourceEventSeq uint64
    TransitionID   string
    Metadata       json.RawMessage
    CreatedAt      time.Time
}

type AgentSessionSliceCommitLink struct {
    SessionID      string
    SliceID        string
    CommitHash     string
    Relationship   string
    ChangesetID    string
    SourceEventSeq uint64
    TransitionID   string
    Metadata       json.RawMessage
    CreatedAt      time.Time
}
```

UI-oriented read models:

```go
type AgentSessionArtifacts struct {
    Changesets  []*AgentSessionChangesetArtifact
    Commits     []*AgentSessionCommitArtifact
    Transitions []*AgentSessionStateTransition
}

type AgentSessionChangesetArtifact struct {
    Link        *AgentSessionChangesetLink
    Changeset   *Changeset
    LatestSnapshot *ChangesetSnapshot
    MergeEvent *MergeEvent
}

type AgentSessionCommitArtifact struct {
    Link   *AgentSessionSliceCommitLink
    Commit *Commit
}

type AgentSessionSummaryLink struct {
    SessionID      string
    SliceID        string
    UserID         string
    AgentType      string
    State          AgentSessionState
    Relationship   string
    SourceEventSeq uint64
    TransitionID   string
    CreatedAt      time.Time
    LastActivityAt *time.Time
}
```

## Storage Interface

Expose a dedicated provenance store and embed it in `Storage` after
implementation.

```go
type AgentSessionArtifactListOptions struct {
    LimitTransitions int
    LimitChangesets  int
    LimitCommits     int
    IncludeSnapshots bool
    IncludeMergeEvents bool
}

type AgentSessionLinkListOptions struct {
    Relationship string
    Limit        int
}

type AgentSessionProvenanceStore interface {
    AppendAgentSessionStateTransition(ctx context.Context, t *models.AgentSessionStateTransition) error
    ListAgentSessionStateTransitions(ctx context.Context, sessionID string, limit int) ([]*models.AgentSessionStateTransition, error)
    GetAgentSessionStateAt(ctx context.Context, sessionID string, seq uint64) (*models.AgentSessionStateTransition, error)

    UpsertAgentSessionChangeset(ctx context.Context, link *models.AgentSessionChangesetLink) error
    UpsertAgentSessionSliceCommit(ctx context.Context, link *models.AgentSessionSliceCommitLink) error

    ListAgentSessionArtifacts(ctx context.Context, sessionID string, opts AgentSessionArtifactListOptions) (*models.AgentSessionArtifacts, error)
    ListChangesetAgentSessions(ctx context.Context, changesetID string, opts AgentSessionLinkListOptions) ([]*models.AgentSessionSummaryLink, error)
    ListSliceCommitAgentSessions(ctx context.Context, sliceID, commitHash string, opts AgentSessionLinkListOptions) ([]*models.AgentSessionSummaryLink, error)
}
```

Implementation notes:

- `AppendAgentSessionStateTransition` should update
  `agent_sessions.current_slice_id/current_commit_hash` in the same transaction
  when the transition is the newest session transition.
- `Upsert*` methods should be idempotent and use the table primary keys for
  retry safety.
- `ListAgentSessionArtifacts` should return denormalized data for session detail
  pages to avoid multiple UI round trips.
- `ListChangesetAgentSessions` and `ListSliceCommitAgentSessions` should return
  compact session summaries for chips/cards on artifact pages.

## API Surface

Implement gRPC-first APIs in `proto/agent`, with grpc-gateway HTTP bindings.

Suggested service additions:

```proto
rpc ListAgentSessionArtifacts(ListAgentSessionArtifactsRequest)
    returns (ListAgentSessionArtifactsResponse) {
  option (google.api.http) = {
    get: "/v1/agent-sessions/{session_id}/artifacts"
  };
}

rpc ListChangesetAgentSessions(ListChangesetAgentSessionsRequest)
    returns (ListChangesetAgentSessionsResponse) {
  option (google.api.http) = {
    get: "/v1/changesets/{changeset_id}/agent-sessions"
  };
}

rpc ListSliceCommitAgentSessions(ListSliceCommitAgentSessionsRequest)
    returns (ListSliceCommitAgentSessionsResponse) {
  option (google.api.http) = {
    get: "/v1/slices/{slice_id}/commits/{commit_hash}/agent-sessions"
  };
}
```

Writes should initially remain internal to services and runners. If local
runners need to explicitly report artifacts, add a restricted RPC later:

```proto
rpc ReportAgentSessionArtifact(ReportAgentSessionArtifactRequest)
    returns (ReportAgentSessionArtifactResponse);
```

That RPC must require ownership of the active local runtime/session and should
validate that referenced changesets/commits belong to the session slice or a
known derived slice.

## Event Protocol

Keep relational provenance as the source of truth. Optionally append lightweight
timeline events for user-visible replay:

```text
stream=control type=state_transition
stream=control type=artifact_link
```

Example payload:

```json
{
  "transitionId": "agtst_123",
  "fromCommitHash": "cmt_A",
  "toCommitHash": "cmt_B",
  "changesetId": "chg_123",
  "relationship": "agent_commit",
  "summary": "Applied requested rename"
}
```

The event is for the chat timeline only. The storage tables remain the query
authority for UI panels and cross-object navigation.

## Lifecycle Examples

### New Session

1. User starts an agent session for `slice_id=home_nic`.
2. Service reads `slice_metadata.head_commit_hash = cmt_A`.
3. `agent_sessions.base_slice_id = home_nic`.
4. `agent_sessions.base_commit_hash = cmt_A`.
5. `agent_sessions.current_slice_id = home_nic`.
6. `agent_sessions.current_commit_hash = cmt_A`.

UI can show:

```text
Started from cmt_A
```

### User Input Produces A Commit

1. User sends input event `seq=12`: "rename foo to bar".
2. Agent edits workspace and commits `cmt_B`.
3. Storage appends:

```text
agent_session_state_transitions:
  from = home_nic@cmt_A
  to = home_nic@cmt_B
  trigger_event_seq = 12
  relationship = agent_commit

agent_session_slice_commits:
  session_id = ags_123
  slice_id = home_nic
  commit_hash = cmt_B
  relationship = created
```

4. `agent_sessions.current_commit_hash = cmt_B`.

UI can show:

```text
User asked: rename foo to bar
Agent produced commit cmt_B
```

### Commit Becomes A Changeset Snapshot

1. Agent exports or updates changeset `chg_123`.
2. Latest snapshot has hash `snap_hash_B`.
3. Storage appends:

```text
agent_session_state_transitions:
  from = home_nic@cmt_A
  to = home_nic@cmt_B
  changeset_id = chg_123
  changeset_snapshot_hash = snap_hash_B
  relationship = changeset_snapshot

agent_session_changesets:
  session_id = ags_123
  changeset_id = chg_123
  relationship = created

agent_session_slice_commits:
  session_id = ags_123
  slice_id = home_nic
  commit_hash = cmt_B
  changeset_id = chg_123
  relationship = created
```

UI can show the commit and changeset as separate artifacts while preserving the
state transition that connects them.

### Changeset Is Merged

1. Existing merge path writes `merge_events`.
2. If the changeset was linked to a session, storage or service adds:

```text
agent_session_slice_commits:
  relationship = merge_source
  slice_id = merge_events.source_slice_id
  commit_hash = merge_events.source_commit_hash
  changeset_id = merge_events.changeset_id
```

Do not duplicate merge authority in the new table. The source of truth remains:

```text
merge_events.changeset_id -> source_slice_id/source_commit_hash
```

## UI Design

### Agent Session Detail

Add panels above or beside the event timeline:

1. Start state
   - base slice
   - base commit hash
   - current commit hash
2. State transitions
   - from commit -> to commit
   - user input summary
   - produced changeset/snapshot when present
3. Artifacts
   - changesets created/updated by the session
   - commits created/referenced by the session

The verbose `agent_session_events` timeline remains available below these
structured panels.

### Changeset List And Detail

Show linked agent sessions as compact pills:

```text
Created by Codex session ags_123
Updated by Claude session ags_456
```

Clicking a pill opens the agent session detail page anchored to the relevant
transition or source event.

### Commit List And Detail

Show linked agent sessions on commit rows:

```text
Produced by Codex session ags_123
```

Commit detail should link back to the session transition that produced it.

## Query Patterns

Session page:

```sql
SELECT * FROM agent_session_state_transitions
WHERE session_id = $1
ORDER BY seq DESC
LIMIT $2;

SELECT l.*, c.*
FROM agent_session_changesets l
JOIN changesets c ON c.id = l.changeset_id
WHERE l.session_id = $1
ORDER BY l.created_at DESC
LIMIT $2;

SELECT l.*, sc.*
FROM agent_session_slice_commits l
JOIN slice_commits sc
  ON sc.slice_id = l.slice_id AND sc.commit_hash = l.commit_hash
WHERE l.session_id = $1
ORDER BY l.created_at DESC
LIMIT $2;
```

Changeset page:

```sql
SELECT l.*, s.session_id, s.slice_id, s.user_id, s.agent_type, s.state,
       s.created_at, s.last_activity_at
FROM agent_session_changesets l
JOIN agent_sessions s ON s.session_id = l.session_id
WHERE l.changeset_id = $1
ORDER BY l.created_at DESC
LIMIT $2;
```

Commit page:

```sql
SELECT l.*, s.session_id, s.slice_id, s.user_id, s.agent_type, s.state,
       s.created_at, s.last_activity_at
FROM agent_session_slice_commits l
JOIN agent_sessions s ON s.session_id = l.session_id
WHERE l.slice_id = $1 AND l.commit_hash = $2
ORDER BY l.created_at DESC
LIMIT $3;
```

## Write Ownership

Preferred write paths:

1. Session creation service sets base/current commit fields.
2. Commit creation/export service records transitions and commit links.
3. Changeset creation/update service records changeset links.
4. Merge service records optional `merge_source` links after merge event append.
5. Local runner may report candidate artifacts only through an authenticated
   restricted API, and the server validates them before writing.

Avoid letting the browser directly write provenance. The browser should display
facts produced by trusted server-side workflows.

## Transaction Rules

1. If a service creates a commit and links it to a session, write the
   `slice_commits`, transition row, commit link row, and session current state in
   one transaction.
2. If a service creates a changeset snapshot and links it to a session, write the
   snapshot, transition row, and changeset link row in one transaction.
3. Merge links should be written in the same transaction as `merge_events` when
   the changeset already has session links.
4. Retry behavior must be idempotent. Use deterministic or caller-provided
   `transition_id` values and `ON CONFLICT DO UPDATE/NOTHING` for link tables.

## Backfill Strategy

Historical data can be partially backfilled:

1. For each `agent_sessions` row with empty base/current fields, set both to the
   earliest known slice head if available, otherwise leave empty.
2. For each `changesets.author` that matches an agent/session user convention,
   optionally create `mentioned` links only when confidence is high.
3. For each `merge_events` row, do not infer agent links unless the changeset is
   already linked.

It is better to leave historical provenance unknown than to create false links.

## Migration Plan

1. Add SQL migration for session state columns and provenance tables.
2. Add Go models and storage interface.
3. Implement memory storage for tests.
4. Implement Postgres storage with transaction helpers.
5. Add service-layer reads for UI endpoints.
6. Add internal write hooks in session creation, changeset creation/export, and
   commit creation.
7. Add UI panels/chips after read APIs are stable.

## Testing Plan

Storage tests:

- session base/current state is set on creation
- append transition updates current state
- transition rows preserve event seq references
- changeset links are idempotent
- commit links are idempotent
- list artifacts returns denormalized changeset/commit data
- reverse lookups return compact agent session summaries
- deleting a session cascades provenance rows
- deleting a changeset cascades changeset links and nulls optional commit links

Service tests:

- creating a session captures the current slice head
- creating a changeset from an agent session writes link rows
- committing from an agent session writes transition and commit rows
- merging a linked changeset records `merge_source`
- read APIs enforce slice/session authorization

UI tests:

- session detail renders base/current state
- session detail renders changeset and commit artifacts
- changeset detail renders linked sessions
- commit detail renders linked sessions

## Open Questions

1. Should `turn_id` be generated by the server for every user input, or derived
   from local runtime IDs when available?
2. Should changeset snapshots get their own explicit link table, or is
   `changeset_id + changeset_version_id + changeset_snapshot_hash` on
   transitions sufficient for v1?
3. Should session base/current state use slice commits only, or should global
   commits also be supported for agents that operate on merged home state?
4. Should a session be allowed to link artifacts from a different slice when the
   runtime operates across mounted folders?
5. What should be the retention policy for verbose events versus compact
   provenance rows?
