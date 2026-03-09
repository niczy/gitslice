# Agent-Native Cloud Filesystem Evolution Plan

**Status:** Not Started
**Created:** 2026-03-09
**Last Updated:** 2026-03-09

---

## Executive Summary

Evolve Gitslice from a "slice-based version control system for massive monorepos" into an **agent-native versioned cloud filesystem** — a platform where AI agents get persistent, branching, conflict-aware file storage with a simple POSIX-like interface.

### Why Now

- AI agents (Claude Code, Codex, Devin, etc.) fundamentally operate on files
- db9.ai validates market demand for "give agents persistent cloud state"
- Multi-agent collaboration is emerging as the next frontier
- Gitslice already has the hard parts: content-addressable storage, branching (slices), conflict detection, agent session infrastructure

### Differentiation from db9.ai

| | db9.ai | Gitslice (evolved) |
|---|---|---|
| Core primitive | Serverless PostgreSQL | Versioned cloud filesystem |
| File ops | Thin layer on Postgres | First-class with POSIX semantics |
| Branching | Database branching | Slice-level isolation with merge |
| Conflict detection | None | Automatic cross-slice detection |
| Multi-agent | No built-in support | Native — each agent gets a slice |
| Version history | SQL migrations | Full file history, diffs, rollback |
| Structured data | Full SQL | Metadata via key-value (future: SQL) |

**Tagline:** "The filesystem where agents can't overwrite each other's work."

---

## Phase 1: Simplified Filesystem API Layer

**Goal:** Expose a clean, agent-friendly file API on top of existing slice/changeset primitives. Agents should be able to `read`, `write`, `mkdir`, `ls`, `rm` without understanding slices or changesets.

### 1.1 New Proto: `filesystem_service.proto`

Create `proto/filesystem/filesystem_service.proto` — a simplified facade that hides changeset mechanics.

```protobuf
syntax = "proto3";
package gitslice.filesystem.v1;

service FilesystemService {
  // Workspace lifecycle
  rpc CreateWorkspace(CreateWorkspaceRequest) returns (CreateWorkspaceResponse);
  rpc DeleteWorkspace(DeleteWorkspaceRequest) returns (DeleteWorkspaceResponse);
  rpc ListWorkspaces(ListWorkspacesRequest) returns (ListWorkspacesResponse);
  rpc GetWorkspaceInfo(GetWorkspaceInfoRequest) returns (WorkspaceInfo);

  // File operations (POSIX-like)
  rpc ReadFile(ReadFileRequest) returns (ReadFileResponse);
  rpc WriteFile(WriteFileRequest) returns (WriteFileResponse);
  rpc DeleteFile(DeleteFileRequest) returns (DeleteFileResponse);
  rpc MoveFile(MoveFileRequest) returns (MoveFileResponse);
  rpc CopyFile(CopyFileRequest) returns (CopyFileResponse);
  rpc ListDirectory(ListDirectoryRequest) returns (ListDirectoryResponse);
  rpc MakeDirectory(MakeDirectoryRequest) returns (MakeDirectoryResponse);
  rpc Stat(StatRequest) returns (StatResponse);
  rpc Exists(ExistsRequest) returns (ExistsResponse);

  // Batch operations (agent-optimized)
  rpc ReadFiles(ReadFilesRequest) returns (ReadFilesResponse);
  rpc WriteFiles(WriteFilesRequest) returns (WriteFilesResponse);
  rpc Glob(GlobRequest) returns (GlobResponse);
  rpc Search(SearchRequest) returns (SearchResponse); // grep-like

  // Version control (opt-in, for advanced agents)
  rpc Snapshot(SnapshotRequest) returns (SnapshotResponse);       // explicit commit
  rpc ListSnapshots(ListSnapshotsRequest) returns (ListSnapshotsResponse);
  rpc RestoreSnapshot(RestoreSnapshotRequest) returns (RestoreSnapshotResponse);
  rpc Diff(DiffRequest) returns (DiffResponse);

  // Collaboration
  rpc Fork(ForkRequest) returns (ForkResponse);
  rpc Merge(MergeRequest) returns (MergeResponse);
  rpc ListConflicts(ListConflictsRequest) returns (ListConflictsResponse);
  rpc ResolveConflict(ResolveConflictRequest) returns (ResolveConflictResponse);

  // Streaming (large files)
  rpc StreamRead(StreamReadRequest) returns (stream StreamReadResponse);
  rpc StreamWrite(stream StreamWriteRequest) returns (StreamWriteResponse);
}
```

**Key design decisions:**
- A **workspace** maps to a slice internally but uses agent-friendly terminology
- Write operations auto-create changesets behind the scenes and auto-merge (single-agent mode) or batch (multi-agent mode)
- Snapshot = explicit commit checkpoint (auto-snapshots happen on configurable intervals)
- Fork/merge expose the collaboration primitives when needed

### 1.2 Server Implementation: `services/filesystem/`

New service package: `services/filesystem/server.go`

```
services/filesystem/
├── server.go           # FilesystemService gRPC handler
├── workspace.go        # Workspace lifecycle (wraps slice operations)
├── fileops.go          # POSIX file operations (wraps changeset + file service)
├── batch.go            # Batch read/write, glob, search
├── versioning.go       # Snapshot, diff, restore (wraps commit operations)
├── collaboration.go    # Fork, merge, conflict resolution
└── streaming.go        # Large file streaming
```

**Implementation approach:**
- Each workspace maintains an implicit "working changeset" per session
- Writes accumulate in the working changeset
- Auto-commit triggers: on explicit `Snapshot()`, on idle timeout, on workspace close
- Reads always reflect latest writes (read-your-writes consistency via changeset overlay)

### 1.3 Storage Layer Changes

Extend `internal/storage/storage.go` interface:

```go
// New methods for filesystem layer
type Storage interface {
    // ... existing methods ...

    // Workspace session tracking
    CreateWorkspaceSession(ctx context.Context, workspaceID, sessionID, userID string) error
    GetWorkspaceSession(ctx context.Context, sessionID string) (*models.WorkspaceSession, error)
    CloseWorkspaceSession(ctx context.Context, sessionID string) error
    ListWorkspaceSessions(ctx context.Context, workspaceID string) ([]*models.WorkspaceSession, error)

    // Fast path: direct file read/write without changeset ceremony
    DirectWriteFile(ctx context.Context, workspaceID, path string, content []byte) error
    DirectReadFile(ctx context.Context, workspaceID, path string) ([]byte, error)
    DirectDeleteFile(ctx context.Context, workspaceID, path string) error
    DirectListDir(ctx context.Context, workspaceID, path string) ([]*models.DirectoryEntry, error)

    // Search/glob
    GlobFiles(ctx context.Context, workspaceID, pattern string) ([]string, error)
    SearchFileContents(ctx context.Context, workspaceID, query string, opts SearchOpts) ([]*models.SearchResult, error)
}
```

New models in `internal/models/`:

```go
// models/workspace.go
type WorkspaceSession struct {
    SessionID       string
    WorkspaceID     string    // maps to slice_id
    UserID          string
    ActiveChangeset string    // implicit working changeset
    CreatedAt       time.Time
    LastActivityAt  time.Time
    Mode            string    // "single" or "collaborative"
}

type SearchResult struct {
    Path       string
    LineNumber int
    Line       string
    MatchStart int
    MatchEnd   int
}
```

### 1.4 Register in Core Server

Update `servers/core/main.go`:
- Register `FilesystemService` alongside existing services
- Add HTTP gateway routes under `/v1/fs/` prefix
- Filesystem endpoints:
  - `POST /v1/fs/workspaces` — create workspace
  - `GET /v1/fs/workspaces/{id}/files/{path}` — read file
  - `PUT /v1/fs/workspaces/{id}/files/{path}` — write file
  - `DELETE /v1/fs/workspaces/{id}/files/{path}` — delete file
  - `GET /v1/fs/workspaces/{id}/ls/{path}` — list directory
  - `POST /v1/fs/workspaces/{id}/snapshot` — create snapshot
  - `POST /v1/fs/workspaces/{id}/fork` — fork workspace
  - `POST /v1/fs/workspaces/{id}/merge` — merge workspace

---

## Phase 2: Agent SDKs

**Goal:** Ship Python and TypeScript SDKs that agent frameworks can integrate in minutes.

### 2.1 Python SDK (`sdk/python/`)

```
sdk/python/
├── gitslice/
│   ├── __init__.py
│   ├── client.py          # Main client class
│   ├── workspace.py       # Workspace object with file ops
│   ├── types.py           # Pydantic models
│   └── exceptions.py
├── pyproject.toml
└── README.md
```

**Target API:**

```python
from gitslice import GitsliceClient

# Connect
client = GitsliceClient(api_key="gs_...")

# Create workspace (or connect to existing)
ws = client.workspace("my-agent-workspace")

# POSIX-like file ops
ws.write("src/main.py", "print('hello')")
content = ws.read("src/main.py")
entries = ws.ls("src/")
ws.mkdir("src/utils")
ws.rm("old_file.txt")
ws.mv("src/old.py", "src/new.py")
ws.cp("template.py", "src/new_module.py")
exists = ws.exists("src/main.py")
info = ws.stat("src/main.py")

# Batch ops (agent-optimized)
files = ws.read_many(["src/a.py", "src/b.py", "src/c.py"])
ws.write_many({"src/a.py": "...", "src/b.py": "..."})
matches = ws.glob("**/*.py")
results = ws.search("def main", glob="**/*.py")

# Version control (opt-in)
snap = ws.snapshot("implemented auth module")
history = ws.snapshots(limit=10)
ws.restore(snap.id)
diff = ws.diff(snap.id)

# Collaboration
fork = ws.fork("experiment-branch")
fork.write("src/experiment.py", "...")
fork.snapshot("try new approach")
ws.merge(fork)  # auto-detects conflicts

# Context manager
with client.workspace("task-123") as ws:
    ws.write("output.txt", result)
    ws.snapshot("task complete")
# auto-cleanup on exit
```

**Framework integrations (separate packages):**

```python
# LangChain tool
from gitslice.integrations.langchain import GitsliceTool
tools = [GitsliceTool(workspace="my-ws")]

# CrewAI tool
from gitslice.integrations.crewai import GitsliceFileTool

# Claude Agent SDK tool
from gitslice.integrations.claude import GitsliceToolProvider
```

### 2.2 TypeScript SDK (`sdk/typescript/`)

```
sdk/typescript/
├── src/
│   ├── index.ts
│   ├── client.ts
│   ├── workspace.ts
│   └── types.ts
├── package.json          # @gitslice/sdk
└── tsconfig.json
```

```typescript
import { GitsliceClient } from '@gitslice/sdk';

const client = new GitsliceClient({ apiKey: 'gs_...' });
const ws = await client.workspace('my-workspace');

await ws.write('src/index.ts', 'console.log("hello")');
const content = await ws.read('src/index.ts');
const entries = await ws.ls('src/');

// MCP server integration
import { GitsliceMCPServer } from '@gitslice/mcp';
const mcp = new GitsliceMCPServer({ workspace: 'my-workspace' });
```

### 2.3 MCP Server

Ship a Model Context Protocol server so any MCP-compatible agent (Claude, etc.) can use Gitslice as a filesystem tool provider.

```
sdk/mcp/
├── src/
│   ├── index.ts
│   ├── tools.ts          # read, write, ls, mkdir, rm, search, snapshot, etc.
│   └── resources.ts      # workspace file tree as MCP resources
├── package.json          # @gitslice/mcp
└── README.md
```

**MCP Tools exposed:**
- `gitslice_read` — Read file content
- `gitslice_write` — Write file content
- `gitslice_ls` — List directory
- `gitslice_mkdir` — Create directory
- `gitslice_rm` — Delete file/directory
- `gitslice_mv` — Move/rename
- `gitslice_search` — Search file contents
- `gitslice_glob` — Find files by pattern
- `gitslice_snapshot` — Create version snapshot
- `gitslice_diff` — Show changes since last snapshot
- `gitslice_fork` — Create isolated branch
- `gitslice_merge` — Merge branch back

---

## Phase 3: CLI Evolution

**Goal:** Add `gs fs` commands for direct filesystem interaction and an interactive shell mode.

### 3.1 New CLI Commands

Add `gs_cli/commands_fs.go`:

```bash
# Workspace management
gs fs create <name> [--from <workspace>]    # Create new workspace (or fork)
gs fs list                                   # List workspaces
gs fs delete <name>                          # Delete workspace
gs fs info <name>                            # Show workspace details

# File operations (workspace-scoped)
gs fs cat <workspace>:<path>                 # Read file
gs fs write <workspace>:<path> < input       # Write file from stdin
gs fs ls <workspace>:[path]                  # List directory
gs fs mkdir <workspace>:<path>               # Create directory
gs fs rm <workspace>:<path>                  # Delete file
gs fs mv <workspace>:<src> <workspace>:<dst> # Move file
gs fs cp <workspace>:<src> <workspace>:<dst> # Copy file
gs fs glob <workspace> <pattern>             # Find files
gs fs search <workspace> <query>             # Search content

# Version control
gs fs snapshot <workspace> -m "message"      # Create snapshot
gs fs snapshots <workspace> [--limit N]      # List snapshots
gs fs restore <workspace> <snapshot-id>      # Restore to snapshot
gs fs diff <workspace> [snapshot-id]         # Show changes

# Collaboration
gs fs fork <workspace> <new-name>            # Fork workspace
gs fs merge <source> <target>                # Merge workspaces

# Upload/download (bulk)
gs fs push <local-dir> <workspace>:[path]    # Upload directory tree
gs fs pull <workspace>:[path] <local-dir>    # Download directory tree

# Sync (bidirectional, FUSE-like)
gs fs mount <workspace> <local-dir>          # Mount workspace locally (FUSE)
gs fs sync <workspace> <local-dir>           # Two-way sync
```

### 3.2 Interactive Shell

Add `gs_cli/commands_shell.go`:

```bash
$ gs fs shell my-workspace
gitslice:my-workspace:/> ls
src/  tests/  README.md
gitslice:my-workspace:/> cd src
gitslice:my-workspace:/src> cat main.py
print("hello world")
gitslice:my-workspace:/src> echo "import os" > utils.py
gitslice:my-workspace:/src> ls
main.py  utils.py
gitslice:my-workspace:/src> snapshot "added utils"
Snapshot created: snap_abc123
gitslice:my-workspace:/src> history
snap_abc123  2026-03-09 14:30  "added utils"
snap_def456  2026-03-09 14:00  "initial"
gitslice:my-workspace:/src> exit
```

This provides the same interactive UX as db9.ai's filesystem shell but with version control built in.

### 3.3 Backward Compatibility

- Existing `gs slice`, `gs changeset`, `gs file` commands remain unchanged
- `gs fs` is a new top-level command group
- Internally, `gs fs` commands call the new `FilesystemService` gRPC API
- Migration path: `gs slice checkout` -> `gs fs pull`, `gs changeset create` -> `gs fs snapshot`

---

## Phase 4: Website Evolution

**Goal:** Transform the web UI from a landing page + repo browser into a full workspace management dashboard with real-time file editing.

### 4.1 Information Architecture

```
/                           # Landing page (new messaging)
/docs                       # Documentation (SDK guides, API reference)
/login                      # Auth (existing)
/dashboard                  # Workspace dashboard (replaces overview)
/dashboard/workspaces       # List all workspaces
/dashboard/workspaces/:id   # Single workspace view
  /files                    # File browser (enhanced RepoBrowser)
  /editor                   # In-browser file editor
  /history                  # Snapshot timeline
  /settings                 # Workspace config
  /collaborate              # Fork/merge/conflicts
/dashboard/sessions         # Agent sessions (existing)
/dashboard/settings         # Account settings (existing)
/playground                 # Try it now (no signup)
```

### 4.2 New/Modified Components

#### Landing Page Redesign (`web/src/components/LandingPage.jsx`)

Replace current `OverviewPage.jsx` with agent-filesystem-focused messaging:

- **Hero:** "The cloud filesystem built for AI agents"
- **Value props:** Version control, multi-agent safety, POSIX interface
- **Code snippets:** Show Python/TS SDK in action
- **Comparison table:** vs db9.ai, vs plain S3, vs local filesystem
- **Interactive demo:** embedded playground (write file, fork, merge)

#### Workspace Dashboard (`web/src/components/WorkspaceDashboard.jsx`)

New component replacing `ProjectsPage.jsx`:

- List workspaces with status (active sessions, last modified, snapshot count)
- Create workspace button (with fork option)
- Quick actions: open shell, view files, create session
- Usage metrics: storage used, API calls, active agents

#### Enhanced File Browser (`web/src/components/FileBrowser.jsx`)

Evolve existing `RepoBrowser.jsx`:

- **File editing:** Inline editor with syntax highlighting (Monaco/CodeMirror)
- **Save = auto-snapshot** with optional message
- **Diff view:** Side-by-side comparison between snapshots
- **Multi-file operations:** Drag-and-drop upload, bulk delete
- **Real-time updates:** WebSocket push when agents modify files
- **Search:** Full-text search across workspace files

#### Snapshot Timeline (`web/src/components/SnapshotTimeline.jsx`)

New component:

- Visual timeline of all snapshots (like git log but visual)
- Click to browse files at any snapshot
- Diff between any two snapshots
- Restore button per snapshot
- Fork from any snapshot

#### Collaboration View (`web/src/components/CollaborationView.jsx`)

New component:

- Visual branch/fork tree
- Merge status and conflict indicators
- Inline conflict resolution (3-way merge UI)
- Activity feed: which agents are writing to which workspaces

#### Playground (`web/src/components/Playground.jsx`)

New component — zero-signup trial:

- Temporary workspace (auto-deleted after 1 hour)
- Embedded terminal (shell mode)
- SDK code generator (shows equivalent Python/TS code)
- "Sign up to save" CTA

### 4.3 API Client Updates

Update `web/src/utils/api.js`:

```javascript
// New filesystem API methods
export const fsApi = {
  createWorkspace: (name, opts) => post('/v1/fs/workspaces', { name, ...opts }),
  listWorkspaces: () => get('/v1/fs/workspaces'),
  deleteWorkspace: (id) => del(`/v1/fs/workspaces/${id}`),

  readFile: (wsId, path) => get(`/v1/fs/workspaces/${wsId}/files/${path}`),
  writeFile: (wsId, path, content) => put(`/v1/fs/workspaces/${wsId}/files/${path}`, { content }),
  deleteFile: (wsId, path) => del(`/v1/fs/workspaces/${wsId}/files/${path}`),
  listDir: (wsId, path) => get(`/v1/fs/workspaces/${wsId}/ls/${path || ''}`),

  createSnapshot: (wsId, message) => post(`/v1/fs/workspaces/${wsId}/snapshot`, { message }),
  listSnapshots: (wsId) => get(`/v1/fs/workspaces/${wsId}/snapshots`),
  restoreSnapshot: (wsId, snapId) => post(`/v1/fs/workspaces/${wsId}/restore/${snapId}`),
  diff: (wsId, snapId) => get(`/v1/fs/workspaces/${wsId}/diff/${snapId || ''}`),

  fork: (wsId, name) => post(`/v1/fs/workspaces/${wsId}/fork`, { name }),
  merge: (sourceWsId, targetWsId) => post(`/v1/fs/workspaces/${sourceWsId}/merge`, { target: targetWsId }),
  listConflicts: (wsId) => get(`/v1/fs/workspaces/${wsId}/conflicts`),

  // WebSocket for real-time file updates
  watchWorkspace: (wsId) => ws(`/ws/fs/workspaces/${wsId}/watch`),
};
```

### 4.4 SEO & Marketing Pages

Add static pages under `web/src/pages/` or as separate Markdown-rendered routes:

- `/docs/quickstart` — 5-minute getting started
- `/docs/python-sdk` — Python SDK reference
- `/docs/typescript-sdk` — TypeScript SDK reference
- `/docs/mcp` — MCP server setup
- `/docs/cli` — CLI reference
- `/docs/concepts` — Workspaces, snapshots, collaboration
- `/docs/api` — REST API reference
- `/pricing` — Free tier + paid plans
- `/blog` — Use cases, benchmarks, comparisons

---

## Phase 5: Authentication & Multi-Tenancy

**Goal:** Production-ready auth system for SDK/API access.

### 5.1 API Keys

- Generate `gs_live_...` and `gs_test_...` API keys per user/org
- Key management in dashboard settings
- Rate limiting per key
- Usage tracking per key

### 5.2 Auth Flow

```
SDK/CLI → API Key in header → Gateway validates → Forward to service
Browser → OAuth (Google/GitHub) → Session cookie → Gateway validates
Agent → API Key or short-lived token (from MCP handshake)
```

### 5.3 Implementation

- Extend `internal/auth/` with API key validation
- Add middleware in `internal/gateway/` for key extraction
- Store keys in PostgreSQL (`api_keys` table)
- Add `Authorization: Bearer gs_...` header support
- Scoped permissions: read-only, read-write, admin per workspace

### 5.4 Multi-Tenancy

- Workspaces belong to organizations
- Org members can share workspaces
- Role-based access: owner, editor, viewer
- Workspace-level API keys for agent access

---

## Phase 6: Performance & Scale

**Goal:** Handle production agent workloads — thousands of concurrent agent sessions, millions of files.

### 6.1 Caching Layer

- Add Redis/Dragonfly for hot file caching
- Cache workspace directory trees (invalidate on write)
- Cache file content by hash (immutable, cache forever)
- SDK-side LRU cache for repeated reads

### 6.2 Object Store Optimization

- Deduplicate file content across workspaces (content-addressable, already designed)
- Pack small files into larger objects (reduce S3/GCS request count)
- Lazy loading: workspace metadata loaded first, file content on demand
- Streaming support for files > 10MB

### 6.3 Write Path Optimization

- Batch writes: accumulate in-memory, flush periodically
- Write-ahead log for durability before flush
- Background snapshot: don't block writes for commit creation
- Configurable consistency: "eventual" (fast) vs "strong" (safe)

### 6.4 Connection Management

- Connection pooling for PostgreSQL
- gRPC connection multiplexing
- HTTP/2 for SDK connections
- WebSocket heartbeat and reconnection

---

## Phase 7: Advanced Features

### 7.1 FUSE Mount (Linux/macOS)

Allow mounting a workspace as a local directory:

```bash
gs fs mount my-workspace ~/mnt/workspace
# Now any tool can read/write files normally
vim ~/mnt/workspace/src/main.py
# Changes sync to cloud automatically
```

Implementation: Go FUSE library (`bazil.org/fuse` or `hanwen/go-fuse`), filesystem operations proxy to gRPC.

### 7.2 Webhooks & Events

```
POST /v1/fs/workspaces/{id}/webhooks
{
  "url": "https://my-app.com/hook",
  "events": ["file.written", "snapshot.created", "conflict.detected"]
}
```

Agent orchestrators can react to file changes in real-time.

### 7.3 Templates

Pre-populated workspace templates:

```python
ws = client.workspace("my-project", template="python-fastapi")
# Workspace starts with pyproject.toml, src/, tests/, etc.
```

### 7.4 Workspace Policies

Configurable rules per workspace:

- Auto-snapshot interval (e.g., every 5 minutes)
- Max file size
- Allowed file types
- Retention policy (delete snapshots older than N days)
- Conflict resolution strategy (last-write-wins vs. manual)

### 7.5 Structured Metadata Store

Key-value metadata attached to workspaces and files:

```python
ws.set_metadata("status", "in_progress")
ws.file_metadata("src/main.py").set("reviewed", "true")
results = ws.query_metadata({"status": "in_progress"})
```

This provides the structured data capability that db9.ai offers via SQL, without requiring a full database interface.

---

## Implementation Priority & Sequencing

```
Phase 1: Filesystem API (4-6 weeks)
  ├── Proto definition + service implementation
  ├── Storage layer extensions
  ├── HTTP gateway routes
  └── Integration tests

Phase 2: SDKs (3-4 weeks, parallel with Phase 1 backend)
  ├── Python SDK + PyPI publish
  ├── TypeScript SDK + npm publish
  └── MCP server

Phase 3: CLI Evolution (2-3 weeks)
  ├── gs fs commands
  ├── Interactive shell
  └── push/pull bulk operations

Phase 4: Website (4-6 weeks, parallel with Phase 2-3)
  ├── Landing page redesign
  ├── Workspace dashboard
  ├── Enhanced file browser + editor
  ├── Playground
  └── Documentation site

Phase 5: Auth & Multi-Tenancy (3-4 weeks)
  ├── API key system
  ├── Gateway middleware
  └── Org-level workspace sharing

Phase 6: Performance (ongoing)
  ├── Caching layer
  ├── Write path optimization
  └── Benchmarking

Phase 7: Advanced (ongoing, feature-driven)
  ├── FUSE mount
  ├── Webhooks
  ├── Templates
  └── Metadata store
```

**Total estimated timeline to MVP (Phases 1-3):** 8-10 weeks
**Full product (Phases 1-5):** 16-20 weeks

---

## Migration Strategy

### Existing Users / Concepts

| Old Concept | New Concept | Internal Mapping |
|---|---|---|
| Slice | Workspace | 1:1, workspace_id = slice_id |
| Changeset | (hidden) | Auto-managed per session |
| Commit | Snapshot | 1:1, snapshot_id = commit_hash |
| Root slice | Default workspace | Auto-created per org |
| Slice checkout | `gs fs pull` | Same underlying operation |
| Changeset merge | `ws.snapshot()` | Auto-changeset + merge |
| Fork slice | `ws.fork()` | Same underlying operation |

### Backward Compatibility

- All existing gRPC services (`SliceService`, `AdminService`, `FileService`) remain operational
- `FilesystemService` is additive — new service alongside existing ones
- Existing CLI commands unchanged; `gs fs` is a new command group
- Existing web routes unchanged; new dashboard is at `/dashboard`
- Gradual deprecation of old terminology in docs (slice -> workspace)

---

## Success Metrics

### Phase 1 (MVP)
- Filesystem API handles 100 req/s per workspace
- < 50ms p99 latency for file reads (cached)
- < 200ms p99 latency for file writes

### Phase 2 (SDKs)
- Python SDK published on PyPI
- TypeScript SDK published on npm
- At least one framework integration (LangChain or Claude Agent SDK)
- 10+ GitHub stars on SDK repos

### Phase 4 (Website)
- Playground conversion rate > 5%
- Documentation covers all SDK methods
- < 3 clicks from landing page to first file write

### Phase 5 (Production)
- 99.9% uptime
- Support 1000+ concurrent agent sessions
- < 1s workspace creation time

---

## Open Questions

1. **Pricing model:** Per-workspace? Per-storage? Per-API-call? Free tier limits?
2. **Conflict resolution default:** Should multi-agent workspaces default to last-write-wins or manual resolution?
3. **Sandbox integration:** Should `gs fs` workspaces auto-provision an E2B/CFC sandbox, or remain storage-only?
4. **SQL layer:** Should we add a structured query interface (like db9.ai) or stay focused on files?
5. **Real-time collaboration:** Should multiple agents see each other's writes in real-time (CRDT-style), or only at snapshot boundaries?
6. **Domain name:** Keep `agenttools.dev` or acquire a filesystem-specific domain?
