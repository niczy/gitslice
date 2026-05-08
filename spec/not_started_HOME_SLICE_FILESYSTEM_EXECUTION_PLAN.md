# Home Slice Filesystem Execution Plan

**Status:** Not Started
**Created:** 2026-03-10
**Last Updated:** 2026-03-10

---

## Executive Summary

Replace the current workspace-oriented `gs fs` UX with an implicit per-user home slice model.

- Every account gets one private home slice at account creation.
- `gs fs` operates on absolute paths like `/nic/src/main.py`.
- `gs fs` no longer asks the user to name a workspace or slice.
- The user's home slice is the authoritative working tree for `/username/**`.
- `root` remains the published shared tree and is updated asynchronously from home-slice commits in batches.
- Slices remain the internal unit of ownership, history, batching, and promotion. They are just hidden from the `gs fs` UX.

This keeps the existing storage architecture intact while removing the main user-facing complexity.

---

## Problem

The current filesystem surface leaks the implementation model:

- `gs fs` requires `workspace:path` syntax.
- Users have to understand "workspace" before they can read or write a file.
- The API and CLI expose concepts that are useful internally but unnecessary for the default single-user flow.

The common user mental model is simpler:

- "I have a home directory."
- "My files live under `/<username>/...`."
- "I read and write my files directly."
- "My changes become visible in the shared root later."

That is the model this plan implements.

---

## Goals

- Make `gs fs` default to the authenticated user's filesystem without an explicit workspace argument.
- Standardize all user-facing remote paths as absolute paths in a global namespace: `/<username>/...`.
- Create the user's home slice automatically on account creation.
- Keep the user's home slice authoritative for their subtree.
- Publish home-slice commits into `root` asynchronously using batch promotion.
- Preserve snapshot, diff, restore, upload, download, and shell workflows for the user's home slice.
- Remove `workspace:path` syntax from the CLI with no backward-compatibility layer.

## Non-Goals

- This does not create a truly global multi-writer filesystem.
- This does not remove slices from the backend data model.
- This does not make `gs fs` a generic slice-management tool.
- This does not preserve the old `workspace:path` CLI grammar.
- This does not solve cross-user collaboration under `gs fs` in the first rollout.

---

## Core Model

### Visible Namespace

The user-visible namespace is a single absolute path tree:

- `/nic/...`
- `/alice/...`
- `/bot-123/...`

The path prefix is the ownership boundary.

### Internal Storage Mapping

- `root` remains the published shared tree.
- Each user gets a dedicated home slice.
- Proposed internal home-slice ID format: `home_<username>`.

`home_<username>` is preferred over `home/<username>` because the current filesystem HTTP routes bind `workspace_id` in URL path segments, and slash-containing IDs are harder to route safely through the existing gateway surface.

### Path Storage Rule

The home slice stores full global-relative paths without the leading slash.

Examples:

- user-visible path `/nic/src/main.py`
- stored path in `home_nic`: `nic/src/main.py`
- stored path in `root`: `nic/src/main.py`

This is an important design choice. It means promotion from a home slice into `root` can copy the same path keys without path rewriting.

### Authority Model

For `/username/**`:

- `home_<username>` is the private authoritative working tree.
- `root` is the asynchronously published mirror.

The user reads and writes their home slice. `root` is not the source of truth for the user's own ongoing work.

### Ownership Model

- User `nic` can mutate only `/nic/**`.
- User `nic` cannot write `/alice/**`.
- In the initial rollout, `gs fs` is scoped to the caller's home subtree. Cross-user reads and shared-workspace semantics remain outside this UX.

This eliminates most user-facing conflict cases by construction.

---

## Why The Home Slice Must Stay A Slice

The visible path is global-looking, but the home slice is still required internally because slices currently carry:

- ownership
- commit history
- snapshot lineage
- promotion state
- merge inputs into `root`
- access control rules

The existing data model already makes slice ID the unit of metadata and history in [internal/models/slice.go](/home/nic/workspace/gitslice/internal/models/slice.go) and [internal/storage/storage.go](/home/nic/workspace/gitslice/internal/storage/storage.go).

So the simplification here is a UX simplification, not a storage rewrite.

---

## Provisioning Rules

Home-slice provisioning happens during account lifecycle flows.

### On Account Creation

When a new user is created:

1. Create the user record.
2. Ensure `root` exists.
3. Create the home slice `home_<username>` if missing.
4. Create the top-level directory entry `<username>` in the home slice.
5. Create the top-level directory entry `<username>` in `root` if missing.
6. Initialize home-slice metadata and initial commit/snapshot just like any other slice.

### On Login / Device Authorization

Provisioning must also run idempotently on:

- username login
- bearer-backed login flows
- device approval

This backfills existing users automatically without requiring a separate hard cutover.

### Existing User Backfill

Add an admin-safe idempotent backfill path that:

- lists all existing users
- ensures `home_<username>` exists
- ensures `/username` exists in `root`
- seeds the home slice from any existing `/username/**` files already present in `root`

This backfill is necessary if any historical data already lives directly in root.

---

## Read And Write Semantics

### Home Slice Is The Read Source

`gs fs` reads from the user's home slice, not from `root`.

This is deliberate. If reads fell back to `root`, delete semantics would require tombstones to hide published files that were deleted locally but not yet promoted. Using the home slice as the only read source avoids that complexity.

### Initialization From Root

When a home slice is first created for an existing user, bootstrap it from the current `root` subtree `/username/**`.

After that:

- the home slice remains authoritative for `/username/**`
- root promotion is one-way from home slice to `root`
- no fallback read path is required

### Mutation Rules

For any `gs fs` mutation:

1. Parse and normalize the absolute path.
2. Validate the first path segment matches the authenticated username.
3. Strip the leading slash and operate on the stored path in the user's home slice.
4. Record the slice-local commit/snapshot exactly as the filesystem service already does.
5. Enqueue asynchronous promotion of the changed paths to `root`.

### Forbidden Paths

The following must be rejected:

- relative paths like `src/main.py`
- empty paths
- `/`
- any absolute path whose first segment does not equal the authenticated username

---

## Root Promotion Model

### Existing Mechanism To Reuse

The repo already has an asynchronous root-promotion queue and same-slice batching design in [finished_ROOT_PROMOTION_QUEUE_BATCHING.md](/home/nic/workspace/gitslice/spec/finished_ROOT_PROMOTION_QUEUE_BATCHING.md).

This home-slice design should reuse that model instead of inventing a second batching system.

### Promotion Flow

For each home-slice commit:

1. Filesystem mutation updates `home_<username>`.
2. Filesystem service creates or extends the slice-local commit.
3. Filesystem service enqueues root promotion for the changed files.
4. The existing promotion worker batches queued promotions.
5. The worker applies those changes to `root`.

### Expected Consistency

- Home slice: strongly consistent for the user.
- `root`: eventually consistent.

This is the intended behavior. The user's CLI should reflect home-slice state immediately, while the published root may lag briefly.

### Conflict Expectations

Normal operation should produce no user-facing conflicts because:

- each user owns exactly one subtree
- promotion only touches `/username/**`
- no other user may write that subtree through `gs fs`

If root promotion detects conflicting writes outside this invariant, treat it as a system integrity issue, not as a normal user workflow.

---

## CLI Design

### Command Surface

`gs fs` becomes a home-filesystem interface, not a workspace manager.

Keep:

- `gs fs cat /nic/file.txt`
- `gs fs write /nic/file.txt`
- `gs fs ls /nic/dir`
- `gs fs mkdir /nic/dir`
- `gs fs rm /nic/file.txt`
- `gs fs mv /nic/a /nic/b`
- `gs fs cp /nic/a /nic/b`
- `gs fs stat /nic/file.txt`
- `gs fs glob '/nic/**/*.py'`
- `gs fs search /nic 'query'`
- `gs fs snapshot -m 'message'`
- `gs fs snapshots`
- `gs fs restore <snapshot-id>`
- `gs fs diff [snapshot-id]`
- `gs fs shell`
- `gs fs upload ./local /nic/project`
- `gs fs download /nic/project ./local`

Remove from `gs fs`:

- `create`
- `list`
- `delete`
- `info`
- `fork`
- `merge`
- `conflicts`

Those are no longer part of the simplified home-filesystem UX. Advanced slice management stays in lower-level APIs and `gs slice`.

### Shell UX

- `gs fs shell` opens directly in `/<username>`
- `gs fs shell /nic/project` opens in that subdirectory
- prompt format becomes `gitslice:/nic/project>`

The shell should never ask for or display a workspace ID.

### Snapshot UX

Snapshots are still slice-local, but the CLI hides that fact:

- `gs fs snapshot -m "checkpoint"` snapshots the caller's home slice
- `gs fs snapshots` lists snapshots for the caller's home slice
- `gs fs restore <id>` restores the caller's home slice

---

## API Strategy

The initial rollout should minimize wire-level churn.

### Keep For Now

- existing `FilesystemService` proto
- current `workspace_id`-scoped backend handlers
- existing HTTP routes under `/v1/fs/workspaces/...`

### Change In Behavior

- `gs fs` and SDKs resolve `workspace_id` internally as `home_<username>`
- filesystem service enforces that home-slice operations only touch `/username/**`
- workspace lifecycle endpoints are no longer part of the primary user workflow

This avoids a destabilizing API rewrite while still delivering the simpler product model.

---

## Data And Invariants

### Required Invariants

1. Every user has at most one home slice.
2. Home slice ID is deterministic from username.
3. Home slice stores only paths under its owner's top-level prefix.
4. `root` may contain `/username/**`, but that subtree is promoted only from `home_<username>`.
5. `gs fs` mutating commands never bypass the home slice and write directly to `root`.

### Recommended Helper Layer

Add a shared internal helper package for home-slice resolution:

- derive home-slice ID from username
- validate path ownership
- convert absolute visible path to stored slice path
- ensure home slice exists

This logic should not be duplicated across account service, filesystem service, CLI, and future SDKs.

---

## Migration And Rollout Notes

### No CLI Backward Compatibility

Do not preserve `workspace:path`.

The CLI should fail fast on the old syntax and direct users to the new absolute-path format.

### Existing Workspace Data

Existing non-home workspaces remain valid backend slices. This plan does not delete them. It only changes what `gs fs` exposes by default.

### Existing Users

Existing users must be backfilled safely. The migration path is:

1. deploy idempotent provisioning in auth/account flows
2. run explicit backfill for all current users
3. switch CLI to the new syntax
4. update docs and examples

---

## Risks

### Promotion Lag

Root will lag the home slice. This is expected, but it must be visible in logs and metrics.

### Historical Root Data

If older systems wrote directly to `root` under `/username/**`, the initial bootstrap must copy that subtree into the user's home slice or those files will disappear from `gs fs`.

### Hidden Direct API Use

Some internal callers may still use explicit workspace IDs. The rollout should not assume the CLI is the only client.

### Command Surface Shrink

Removing workspace lifecycle commands from `gs fs` is intentionally breaking. Release notes and help text must be explicit.

---

## PR-By-PR Execution Plan

### PR1: Home Slice Helper And Provisioning

Scope:

- add shared home-slice helper package
- define internal ID format `home_<username>`
- add idempotent `EnsureHomeSlice(...)` logic
- call it from account creation, login, and device approval flows
- reserve `/username` directory in both home slice and `root`

Files likely touched:

- `services/account/server.go`
- new shared helper under `internal/`
- filesystem bootstrap helpers if extraction is needed

Verification:

- account-service unit tests
- provisioning tests for signup, login, and device approval
- migration-safe tests proving repeated ensure calls are no-ops

Deploy:

- yes

### PR2: Bootstrap Existing User Data

Scope:

- add admin/backfill path for all existing users
- copy existing `/username/**` subtree from `root` into a missing home slice
- keep operation idempotent and resumable

Files likely touched:

- `services/admin/`
- `services/account/`
- storage helpers if subtree copy utilities are missing

Verification:

- integration test for backfilling a user with existing root data
- dry-run or no-op mode if useful

Deploy:

- yes

### PR3: Filesystem Service Home-Slice Enforcement

Scope:

- make filesystem mutations validate absolute paths
- map authenticated user to `home_<username>`
- reject paths outside `/username/**`
- keep backend writes on the home slice only
- stop exposing workspace lifecycle as part of the primary filesystem path

Files likely touched:

- `services/filesystem/server.go`
- `proto/filesystem/filesystem_service.proto` only if small cleanup is needed
- gateway tests

Verification:

- unit tests for path parsing and permission enforcement
- filesystem service tests for read/write/mkdir/rm/mv/cp/stat/exists
- integration tests proving `/other-user/...` is rejected

Deploy:

- yes

### PR4: Shared Root Promotion For Filesystem Commits

Scope:

- extract or reuse the existing root-promotion queue so filesystem commits can enqueue promotions too
- enqueue home-slice mutations for asynchronous publish into `root`
- preserve batching semantics and eventual consistency

Files likely touched:

- `services/slice/server.go`
- `services/filesystem/server.go`
- shared promotion helper if extracted

Verification:

- queue batching tests
- same-slice burst tests
- end-to-end test showing home-slice write becomes visible in root after queue drain

Deploy:

- yes

### PR5: CLI Absolute-Path Cutover

Scope:

- remove `workspace:path` parsing
- require absolute remote paths
- resolve implicit home slice from auth identity
- update `cat`, `write`, `ls`, `mkdir`, `rm`, `mv`, `cp`, `glob`, `search`, `stat`

Files likely touched:

- `gs_cli/commands_fs.go`
- `gs_cli/help.go`
- `gs_cli/commands_fs_test.go`

Verification:

- CLI unit tests for absolute-path parsing
- workflow tests for full `gs fs` read/write flow

Deploy:

- no

### PR6: Shell And Transfer UX Cutover

Scope:

- make `gs fs shell` default to `/<username>`
- support optional absolute-path shell start target
- update `upload` and `download` to use absolute remote paths
- update shell prompt and help text

Files likely touched:

- `gs_cli/commands_shell.go`
- `gs_cli/commands_fs_transfer.go`
- workflow integration tests

Verification:

- shell workflow end-to-end test
- transfer workflow end-to-end test

Deploy:

- no

### PR7: Snapshot And History UX Simplification

Scope:

- make `snapshot`, `snapshots`, `restore`, and `diff` operate on the implicit home slice
- remove workspace arguments from these commands
- update examples and docs

Files likely touched:

- `gs_cli/commands_fs.go`
- `workflow_test/integration_test.go`
- `README.md`

Verification:

- end-to-end snapshot/restore/diff tests on the home slice

Deploy:

- no

### PR8: Remove Workspace Management From `gs fs`

Scope:

- remove `create`, `list`, `delete`, `info`, `fork`, `merge`, `conflicts` from `gs fs`
- keep advanced slice/workspace operations in `gs slice` or internal APIs
- update help text and docs accordingly

Files likely touched:

- `gs_cli/help.go`
- `gs_cli/main.go`
- `README.md`

Verification:

- CLI help snapshot tests if added
- workflow tests updated to new command surface

Deploy:

- no

### PR9: SDK And Web Follow-Up

Scope:

- update Python and TypeScript SDKs to use absolute paths and implicit home slice
- update any web/dashboard filesystem entry points that still surface explicit workspaces for the default user flow

Verification:

- SDK tests
- targeted web/API smoke tests if the web UI is changed

Deploy:

- maybe, depending on web changes

---

## Recommended Rollout Order

1. land provisioning first
2. backfill existing users
3. land backend home-slice enforcement
4. land async promotion wiring
5. cut the CLI to absolute paths
6. remove stale workspace UX

That order ensures the new CLI never reaches a server that lacks home slices or promotion behavior.
