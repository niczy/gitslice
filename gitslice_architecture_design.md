# Gitslice Architecture Design

## 1. Overview

Gitslice, or GS, is a cloud-native, Git-compatible version control system for
large multi-tenant codebases, repository-like slices, virtual workspaces, and
changeset-based collaboration.

The central architectural idea is:

```text
Native global source graph first.
Git compatibility at the boundary.
```

Gitslice should not be implemented internally as a traditional Git server. Git
clients should see ordinary Git repositories, but the source of truth should be
a scalable native storage and metadata system.

Gitslice is designed to support:

- User and organization namespaces
- Repository-like slices with their own visibility and access rules
- A single global commit graph across all slices
- Atomic changes that can span multiple slices
- Sparse, virtualized workspaces
- Changesets as the review and submission unit
- Git clone, fetch, and push compatibility
- Agent-native code workflows
- Large file trees, large histories, and incremental indexing

---

## 2. Design Principles

### 2.1 Users and Organizations Only

The global namespace has two tenant kinds:

```text
/users/{username}/...
/orgs/{org}/...
```

There are no special top-level `/shared`, `/system`, or `/build` namespaces.
Shared libraries, build configuration, generated code, and platform-owned code
should live in a normal user or organization namespace.

### 2.2 Slices Are Repository-Like

A slice is the primary unit of access, visibility, checkout, review, and Git
compatibility.

A slice is similar to a GitHub repository:

- It has a tenant owner.
- It has a stable slug.
- It has visibility settings.
- It has members and roles.
- It has one or more included absolute paths.
- It can be cloned as a Git repository.

A slice is not an independent storage repository internally. It is a projection
over the global commit graph.

### 2.3 Absolute Paths Everywhere

Every file and directory has one canonical absolute global path.

Example:

```text
/users/nicholas/services/identity/auth.go
/orgs/acme/payment/api/handler.go
```

Slices do not remap paths to custom mount locations. A checkout of a slice
preserves the canonical path layout, minus the leading `/` required by local
filesystems.

Example slice includes:

```text
/users/nicholas/services/identity
/users/nicholas/libs/auth
```

Example checkout layout:

```text
identity/
  users/
    nicholas/
      services/
        identity/
      libs/
        auth/
```

This removes path aliasing from the core model and keeps Git projection,
authorization, diffs, review, and local workspaces easier to reason about.

### 2.4 Changesets Are The Write Model

Users and agents should not normally write commits directly.

The normal write path is:

```text
workspace diff
  -> patchset
  -> changeset
  -> review and validation
  -> tenant-defined submit queue or queues
  -> global commit or commits
  -> atomic ref update
```

Storage-level commit creation is an internal implementation detail.

### 2.5 Commits Are Storage Artifacts

Commits are immutable storage-level snapshots of the global tree.

Users interact mostly with:

- slices
- workspaces
- changesets
- patchsets
- reviews
- tenant-defined submit queues

### 2.6 Git Is A Compatibility Layer

Git should be supported for clone, fetch, push, CI, IDEs, and ecosystem tools.

Git should not define the native data model.

---

## 3. Global Namespace

The repository is one global path namespace under users and organizations.

```text
/users/nicholas/...
/users/alice/...
/orgs/acme/...
/orgs/open-source-lab/...
```

Examples:

```text
/users/nicholas/services/identity
/users/nicholas/libs/auth
/orgs/acme/payment
/orgs/acme/proto/payment
/orgs/acme/build/bazel
```

The global namespace allows:

- Atomic cross-slice changes
- Unified history
- Global indexing
- Global code search
- Cross-slice refactoring
- Consistent absolute paths for humans, agents, APIs, and Git projections

### 3.1 Tenant Identity

Tenant identity always includes tenant kind.

```text
users/nicholas
orgs/acme
```

User and organization names may overlap because the tenant kind disambiguates
them.

Examples:

```text
users/acme
orgs/acme
```

These are different tenants.

### 3.2 Path Ownership

By default:

```text
/users/{username}/... belongs to users/{username}
/orgs/{org}/... belongs to orgs/{org}
```

A slice owned by a tenant may include paths from that same tenant.

Cross-tenant changes are represented as changesets that touch multiple slices,
not as one slice silently mounting another tenant's paths.

Future explicit cross-tenant collaboration can be added, but it must be modeled
as an authorization feature, not as a special namespace.

---

## 4. Slice Model

A slice is a named, repository-like projection over one or more absolute paths
inside one tenant namespace.

### 4.1 Slice Identity

Slice identity includes tenant kind, tenant name, and slice slug.

```text
users/{username}/{slice}
orgs/{org}/{slice}
```

Examples:

```text
users/nicholas/identity
orgs/acme/payment
```

This identity is used in:

- CLI commands
- API requests
- access control
- changesets
- Git URLs
- projection cache keys
- audit logs

### 4.2 Slice Definition

A slice definition is first-class metadata, not an ordinary source file.

Example:

```yaml
id: slc_01J...
tenant: users/nicholas
slug: identity
display_name: Identity
default_branch: main
visibility: private

included_paths:
  - /users/nicholas/services/identity
  - /users/nicholas/libs/auth
  - /users/nicholas/proto/identity

roles:
  admins:
    - users/nicholas
  writers: []
  readers: []
```

The slice definition is versioned and auditable. Each accepted definition change
creates a new slice definition version.

```text
slice_id
slice_definition_version
slice_definition_hash
created_by
created_at
included_paths
visibility
roles
metadata
```

Definition changes are control-plane changes. They should require slice admin
permission and should be recorded with the same review/audit rigor as source
changes. For protected slices, changing `included_paths`, visibility, or roles
should go through a changeset or an equivalent reviewed administrative flow.

### 4.3 Included Paths

`included_paths` are absolute global paths.

They may point to directories or individual files.

```yaml
included_paths:
  - /orgs/acme/payment
  - /orgs/acme/proto/payment
  - /orgs/acme/README.md
```

There are no mount aliases.

The slice checkout and Git projection preserve the absolute path structure
inside the local repository root.

### 4.4 Overlapping Slices

Overlapping slices are supported.

A global path may be included by multiple slices. Each slice gets its own
repository-like projection over the same underlying global objects.

Example:

```yaml
# orgs/acme/backend
included_paths:
  - /orgs/acme/services
  - /orgs/acme/libs

# orgs/acme/payment
included_paths:
  - /orgs/acme/services/payment
  - /orgs/acme/proto/payment
```

In this example, `/orgs/acme/services/payment` is covered by both
`orgs/acme/backend` and `orgs/acme/payment`.

### 4.5 Covering Slices

A covering slice is any slice whose latest accepted `included_paths` contain a
path.

```text
covering_slices(path, definition_epoch)
  -> []slice_id
```

For existing files, coverage is resolved against the file path.

For new files, coverage is resolved against the path that would exist after the
change. A new file is valid only if at least one writable slice covers the new
path.

For renames and moves, coverage is resolved for both the old path and the new
path.

There is no single authoritative governing slice for an overlapping path. The
write policy for a path is the combined policy of all covering slices.

The slice through which a user starts work is the authoring slice. It is useful
for UI, defaults, and Git URL resolution, but it does not weaken the policy of
other covering slices.

### 4.6 Overlap Policy Rule

When a changeset touches paths covered by multiple slices, the server computes
the union of required policies.

```text
changed paths
  -> covering slices
  -> union of required roles
  -> union of required approvals
  -> union of required checks
```

The safe default is:

```text
Any covering slice may add requirements.
No covering slice may remove another covering slice's requirements.
```

If two covering slices define incompatible policies, the changeset is blocked
until a slice admin resolves the policy conflict.

Examples of compatible policy union:

```text
payment requires payment-owner approval
backend requires backend-ci

effective requirement:
  payment-owner approval
  backend-ci
```

Examples of policy conflict:

```text
slice A requires generated file X to be regenerated by tool A
slice B forbids generated file X from being changed manually

resolution:
  blocked until a shared policy or admin override is recorded
```

### 4.7 Slice Definition Overlap Changes

Adding, removing, or moving an included path can change the covering slice set
for many files.

Definition changes that affect overlap must:

- require slice admin permission
- be audited as a new slice definition version
- recompute coverage for affected paths
- revalidate open changesets touching affected paths
- invalidate affected projection caches

If a slice definition change adds a new covering slice to an open changeset,
that changeset must collect the new slice's required approvals and checks before
submission.

If a slice definition change removes a covering slice, approvals from that slice
are no longer required for future submit attempts, but the historical review log
is preserved.

### 4.8 Slice History Projection

Slice history is a projection of the global commit graph using the latest
accepted slice definition by default.

That means:

```text
slice history = global commits that touched the current included_paths
```

If the slice definition changes, the default projected history can change.

Example:

```text
definition v1 includes:
  /orgs/acme/payment

definition v2 includes:
  /orgs/acme/payment
  /orgs/acme/proto/payment
```

After v2 is accepted, the default slice history includes past global commits
that touched either path.

This is intentional. The slice answers the question:

```text
What is the history of the paths this slice currently includes?
```

For audit and debugging, the system may also support pinned historical
projection:

```text
slice_id + slice_definition_version + global_commit
```

But the normal user-facing history should use the latest definition.

This has an important consequence: slice definition changes can reshape projected
history. The global commit graph remains immutable and linear, but the projected
history for a slice can gain or lose historical commits when `included_paths`
changes.

Git clients must treat a slice definition change as a projection epoch change.
The system should expose the current projection epoch in clone/fetch metadata and
surface a clear sync/reset flow if the projected Git branch is no longer a
fast-forward update for an existing checkout.

### 4.9 Projection Cache Identity

Because slice projection depends on the slice definition, projection caches must
include the definition hash.

```text
(slice_id, slice_definition_hash, global_commit_id) -> projected_tree_id
(slice_id, slice_definition_hash, global_commit_id) -> synthetic_git_commit_id
synthetic_git_commit_id -> global_commit_id
```

When a slice definition changes, the system can invalidate or lazily rebuild
projection cache entries for that slice.

---

## 5. Visibility And Access Control

Visibility and access control are slice-level.

A slice is the unit users reason about, similar to a repository on GitHub.

### 5.1 Visibility

Recommended visibility states:

```text
private
tenant
public
```

Meaning:

```text
private: visible only to explicitly authorized users and groups
tenant: visible to members of the owning user/org tenant
public: readable without authentication
```

### 5.2 Roles

Recommended slice roles:

```text
owner
admin
writer
reader
```

Capabilities:

```text
owner:
  transfer/delete slice
  manage admins
  manage all settings

admin:
  manage visibility
  manage readers/writers
  change included paths
  approve protected changes

writer:
  create changesets
  push to changeset refs
  submit when policy allows

reader:
  clone/fetch/read slice contents
  view changesets
```

### 5.3 Included Path Authorization

Changing `included_paths` is a privileged slice administration action.

Validation rules:

- A user slice may include only paths under `/users/{username}/...` for that
  user.
- An org slice may include only paths under `/orgs/{org}/...` for that org.
- Included paths may overlap other slices in the same tenant.
- Cross-tenant included paths are not allowed in the initial design.
- Public visibility cannot expose paths that tenant policy marks as
  non-publicable.

### 5.4 Changeset Authorization

A changeset can touch one slice or many slices.

For each changed global path, the server resolves all covering slices.

```text
changed paths
  -> covering slices
  -> required slice roles
  -> required approvals
  -> required checks
```

Cross-slice changesets require policy satisfaction on every affected covering
slice.

Cross-tenant changesets require authorization in every affected tenant.

Default write authorization:

- A user may create a changeset from a slice where they have writer access.
- The user must have read access to every path they modify.
- If modified paths are covered by slices where the user lacks writer access, the
  changeset can still exist, but it cannot submit until those covering slices
  approve according to their policies.
- Submission requires every covering slice's write policy to be satisfied.

### 5.5 Overlap Read Visibility

Read access is evaluated through the slice being read.

If a public slice includes a path, that path is publicly readable through that
slice. A private overlapping slice cannot make the same underlying bytes private
again.

Effective exposure for a global path is therefore the broadest visibility of any
covering slice.

```text
private + public overlap -> path is public through the public slice
```

For that reason, changing a slice to `public` must analyze every included path
and surface any overlapping slices before the visibility change is accepted.

Changeset and review UIs should filter or redact file content per reader. A user
who can read only one affected slice may see the paths and diffs they are
authorized for, while hidden paths remain redacted unless the user can read the
other affected slices.

---

## 6. Workspace Model

A workspace is a local hydrated development environment over one or more slices.

Workspaces are sparse and virtualized. Users should not need to clone the entire
global namespace.

Example:

```bash
gs workspace init
gs slice add users/nicholas/identity
gs slice add orgs/acme/payment
```

Example workspace layout:

```text
workspace/
  users/
    nicholas/
      services/
        identity/
      libs/
        auth/
  orgs/
    acme/
      payment/
  .gs/
```

The client maintains:

```text
workspace config
slice bindings
metadata cache
hydrated file cache
overlay changes
changeset state
```

Files are hydrated on demand.

The workspace can contain multiple slices, but each file path still has one
canonical absolute global path.

---

## 7. Changeset Model

A changeset is the collaboration and submission object.

A changeset represents a proposed change to the global source graph. It may
affect one slice, multiple slices, or multiple tenants if permissions allow.

### 7.1 Changeset Structure

```text
Changeset:
  id
  author
  authoring_slice
  created_at
  updated_at
  target_ref
  base_commit
  patchsets[]
  current_patchset
  affected_paths[]
  affected_slices[]
  covering_slices_by_path[]
  expected_slice_definition_hashes[]
  required_queues[]
  expected_queue_definition_hashes[]
  required_policies
  status
  review_state
  test_state
  metadata
```

### 7.2 Patchsets

Each update to a changeset creates a new patchset.

```text
CS123
  patchset 1
  patchset 2
  patchset 3
```

A patchset stores changes using canonical global paths. It does not depend on a
mount alias or local checkout layout.

### 7.3 Changeset Lifecycle

```text
Draft
  -> Review
  -> Queued
  -> Submitting
  -> Submitted
```

Other states:

```text
Abandoned
Failed
MergeConflict
NeedsRebase
```

### 7.4 Changeset To Commit Mapping

A changeset is not necessarily equal to a single commit.

Possible mappings:

```text
1 changeset -> 1 global commit
1 changeset -> N global commits
N changesets -> 1 squashed global commit
```

The user-facing object remains the changeset.

### 7.5 No Direct User Commits

The public API should not expose a generic "create commit" operation as the
normal write path.

Allowed user write paths:

```text
create changeset
update changeset
submit changeset
abandon changeset
```

Internal services may create commits only as part of submit, import, migration,
or trusted administrative workflows.

---

## 8. Commit Model

Commits are immutable storage-level snapshots of the global tree.

```text
Commit:
  id
  parent_ids
  root_tree_id
  author
  message
  created_at
  changed_paths[]
  metadata
```

There is one global commit graph.

All slices project views from that same graph.

Advantages:

- Atomic cross-slice changes
- Unified history
- Consistent global indexing
- Cross-slice refactoring
- Deterministic slice projection

---

## 9. Ref Model

A ref is a mutable named pointer to a commit.

In Git terms, branches and tags are refs. In Gitslice, commits and trees are
immutable, but refs move as work is submitted.

Example:

```text
refs/global/main -> G123
```

When a changeset lands, Gitslice creates a new commit and atomically moves the
target ref:

```text
refs/global/main: G123 -> G124
```

Refs are needed because immutable commits alone do not say which commit is the
current accepted state.

### 9.1 Target Refs

A target ref is the ref a queue updates when a changeset lands.

The initial system may use one accepted global tree ref:

```text
refs/global/main
```

But this must not imply one global submit queue. Many user/org queues can target
the same ref. They serialize only the work assigned to those queues, and the
final ref update is still protected by CAS.

Future branch support can add target refs such as:

```text
refs/global/branches/{branch}
refs/users/{username}/branches/{branch}
refs/orgs/{org}/branches/{branch}
```

### 9.2 Changeset Refs

Changeset patchsets can be addressed with refs:

```text
refs/changes/{changeset_id}/{patchset_number}
```

These refs make it possible to integrate with Git tooling, CI systems, and
review systems without making changesets ordinary branches.

`refs/changes/new` can be supported as a Git push alias that asks the server to
allocate a new changeset id.

### 9.3 Projected Git Refs

When a slice is exposed as a Git repository, the Git gateway projects native
refs into Git refs.

Example:

```text
native target ref: refs/global/main
git ref:           refs/heads/main
```

Projected Git refs are compatibility views. The native source of truth remains
the global ref.

### 9.4 Atomic Ref Updates

Refs use compare-and-swap semantics.

```text
update_ref(ref, expected_old_commit, new_commit)
```

The update succeeds only if:

```text
current_commit == expected_old_commit
```

Otherwise the changeset must be rebased and retried through its required queue
or queues.

### 9.5 Queue Definitions As Versioned Files

Each user or organization defines its own submit queues using versioned files in
its namespace.

```text
/users/{username}/.gitslice/queues/{queue}.yaml
/orgs/{org}/.gitslice/queues/{queue}.yaml
```

Example:

```yaml
version: 1
name: main
target_ref: refs/global/main

scope:
  paths:
    - /orgs/acme/**
  slices:
    - orgs/acme/*

ordering: fifo

submit:
  required_roles:
    - writer
  required_approvals:
    - team: acme-maintainers
  required_checks:
    - acme-ci

concurrency:
  max_active: 1
  allow_disjoint_paths: false

overrides:
  admin_override: true
```

Queue files are ordinary versioned source graph files. Updating a queue file is a
control-plane change and should itself go through a changeset. The effective
queue configuration for submission is resolved from the latest accepted target
ref at the time the changeset is validated.

If a tenant has no queue file yet, the system provides a bootstrap default queue:

```text
/users/{username}/.gitslice/queues/default.yaml
/orgs/{org}/.gitslice/queues/default.yaml
```

The bootstrap queue exists as system behavior until the tenant commits an
explicit queue file.

---

## 10. Git Compatibility

Git compatibility is implemented as a projection layer.

Each slice can be exposed as a Git repository.

Canonical Git URL format:

```text
https://gitslice.io/git/users/{username}/{slice}.git
https://gitslice.io/git/orgs/{org}/{slice}.git
```

Examples:

```bash
git clone https://gitslice.io/git/users/nicholas/identity.git
git clone https://gitslice.io/git/orgs/acme/payment.git
```

### 10.1 Supported Git Operations

Initial supported operations:

- `git clone`
- `git fetch`
- `git push`
- partial clone
- sparse checkout
- Git refs
- Git branches projected from native refs
- Git commits projected from global commits

### 10.2 Synthetic Git Commits

Git commits exposed to clients are synthetic projections.

Mapping:

```text
GitCommit(slice=users/nicholas/identity, hash=A)
  -> GlobalCommit(G123)
  -> SliceDefinitionHash(D456)
```

One global commit may map to many synthetic Git commits because each slice sees
a different projected tree.

```text
GlobalCommit(G123)
  -> users/nicholas/identity Git commit A
  -> orgs/acme/payment Git commit B
```

Synthetic Git commit IDs must be stable for the same projection inputs:

```text
slice_id
slice_definition_hash
global_commit_id
projected_parent_git_commit_ids
projected_tree_id
author
message
timestamp policy
```

### 10.3 Git Push

Protected targets should not allow ordinary Git pushes to write directly to the
accepted global ref.

Instead:

```text
git push origin HEAD:refs/changes/new
```

or an equivalent server-supported push target should create or update a
changeset.

Server behavior:

```text
Git push
  -> authenticate user
  -> resolve slice from Git URL
  -> convert Git diff to global absolute paths
  -> create or update changeset
  -> create patchset
  -> run validation
```

Direct push to a protected branch should either be rejected or translated into a
changeset according to slice policy.

---

## 11. Storage Architecture

Gitslice storage should be built around:

- Content-addressed immutable blobs
- Immutable tree nodes
- Immutable commits
- Transactional metadata
- Atomic refs
- Separate blob and metadata stores

### 11.1 Recommended Storage Stack

Blob content:

```text
S3-compatible object storage, GCS, R2, or equivalent
```

Metadata:

```text
transactional database or ordered key-value store
```

The metadata store must support:

```text
point lookup
range scan
transactional writes
compare-and-swap ref updates
consistent reads for submit validation
```

Implementation choices can evolve from a transactional SQL database to an
ordered distributed KV store as scale requires. The architecture depends on the
capabilities, not on a specific vendor.

Search and derived indexes:

```text
OpenSearch, Elasticsearch, Zoekt, custom trigram index, or purpose-built index
workers
```

Hot metadata cache:

```text
process-local cache
distributed cache where needed
```

### 11.2 Blob And Metadata Transaction Semantics

Object storage systems are not part of the metadata transaction.

The write protocol must account for that.

Recommended staged write flow:

```text
1. Client uploads missing blob content by hash.
2. Server verifies hash and size.
3. Server marks blob records as staged or available.
4. Submit transaction writes tree nodes, commit metadata, and ref update.
5. After commit succeeds, referenced blobs are considered live.
6. Background GC removes unreferenced staged blobs after a grace period.
```

The metadata transaction must never point at a blob that has not been verified.

Blob upload can happen before submit. Commit publication happens only through
the metadata transaction and atomic ref update.

### 11.3 Storage Invariant

The core storage invariant is:

```text
Ref -> Commit -> RootTree -> TreeEntries -> Blobs
```

Everything except refs is immutable.

---

## 12. Object Model

### 12.1 Blob

```text
Blob:
  id
  hash
  size
  compression
  storage_location
  state
```

Blobs are immutable and content-addressed.

### 12.2 Tree

```text
Tree:
  id
  hash
  entries_or_chunks[]
```

Trees are immutable.

### 12.3 Tree Entry

```text
TreeEntry:
  name
  kind
  mode
  tree_id
  blob_id
  symlink_target
  size
  content_hash
```

Supported entry kinds:

```text
file
directory
symlink
```

### 12.4 Commit

```text
Commit:
  id
  parent_ids
  root_tree_id
  author
  message
  created_at
  changed_paths[]
```

### 12.5 Ref

```text
Ref:
  name
  commit_id
  updated_at
  updated_by
```

---

## 13. Canonical Paths And Tree Hashing

The tree model must be deterministic across clients, operating systems, and
regions.

### 13.1 Path Rules

Canonical paths:

- are absolute
- start with `/users/` or `/orgs/`
- use `/` as the only separator
- are valid UTF-8
- are normalized to Unicode NFC
- do not contain empty segments
- do not contain `.` or `..` segments
- do not contain NUL
- are case-sensitive

Examples:

```text
valid:   /users/nicholas/app/README.md
invalid: users/nicholas/app/README.md
invalid: /users/nicholas/app/../secret.txt
invalid: /shared/lib
```

### 13.2 Entry Ordering

Directory entries are sorted by the byte order of their canonical UTF-8 names
after NFC normalization.

This ordering is used for:

- tree hashing
- directory pagination
- deterministic projection
- Git tree generation

### 13.3 Tree Hash Inputs

A tree entry hash includes:

```text
entry kind
entry name
mode
content hash or child tree hash
size where applicable
symlink target where applicable
```

Directory tree hashes are computed from ordered child entries.

The directory name itself is stored in the parent entry, not inside the child
tree. This allows a directory rename to reuse the child subtree hash.

### 13.4 File Modes

The initial mode model should support:

```text
regular file
executable file
directory
symlink
```

Additional platform-specific mode bits should not affect the canonical source
tree unless explicitly added to the model.

### 13.5 Huge Directory Handling

Very large directories must be chunked deterministically.

Recommended approach:

```text
DirectoryRoot
  -> DirectoryChunk[]
  -> ordered TreeEntry records
```

Chunking rules:

- chunks cover non-overlapping name ranges
- entries are ordered by canonical name
- chunk boundaries are deterministic for the same entry set
- chunk hashes feed into the directory root hash
- listing supports cursor-based pagination by entry name

This prevents one huge directory from becoming one massive metadata object while
keeping tree hashes deterministic.

### 13.6 Rename Behavior

Renaming a file changes the parent directory entries.

Renaming a directory changes the parent directory entry, but can reuse the
renamed directory's child tree because the child tree hash is independent of the
directory's name.

Path-based indexes still need to update affected path records after a rename.

---

## 14. Repository APIs

Native APIs should be gRPC-first. HTTP endpoints should be exposed through
grpc-gateway bindings where needed.

### 14.1 Reader API

High-level read API:

```go
type RepositoryReader interface {
    ResolvePath(ctx context.Context, commit CommitID, path string) (TreeEntry, error)
    ListDir(ctx context.Context, commit CommitID, path string, cursor string, limit int) ([]TreeEntry, string, error)
    ReadFile(ctx context.Context, commit CommitID, path string, offset, length int64) (io.ReadCloser, error)
}
```

### 14.2 Changeset API

High-level write API should be changeset-oriented:

```go
type ChangesetService interface {
    CreateChangeset(ctx context.Context, req CreateChangesetRequest) (Changeset, error)
    UpdateChangeset(ctx context.Context, req UpdateChangesetRequest) (Patchset, error)
    SubmitChangeset(ctx context.Context, req SubmitChangesetRequest) (SubmitResult, error)
    AbandonChangeset(ctx context.Context, req AbandonChangesetRequest) error
}
```

### 14.3 Internal Commit API

Internal services can expose commit creation behind trusted boundaries:

```go
type InternalCommitter interface {
    CreateCommitFromPatchset(ctx context.Context, req CommitPatchsetRequest) (CommitID, error)
}
```

This API should not bypass validation for normal users.

---

## 15. Conflict Prevention

Gitslice should use optimistic concurrency control by default.

Every changeset is based on a specific global commit.

```text
Changeset:
  base_commit = G100
```

Before submission, the server validates:

```text
Can the patch apply cleanly to current head?
Do affected paths still have the expected covering slices?
Does the author still have the required slice roles?
Do all covering slice policies pass?
Do required checks pass on the latest head?
```

### 15.1 Conflict Types

File content conflict:

```text
Two changes edit the same lines.
```

Path conflict:

```text
One changeset deletes or renames a file while another edits it.
```

Slice coverage conflict:

```text
The covering slice set or included path set changed while the changeset was open.
```

Overlap policy conflict:

```text
Two covering slices impose incompatible requirements on the same path.
```

Semantic conflict:

```text
Two changes touch different files but break behavior together.
```

Semantic conflicts are handled by tests and the required queue or queues.

### 15.2 Overlap Conflict Resolution Process

Overlapping slices are resolved by recomputing coverage and applying the union of
all covering slice policies at every important transition.

Process:

```text
1. Create or update patchset.
2. Normalize changed paths to canonical absolute paths.
3. Resolve covering slices for each changed path using latest slice definitions.
4. Store covering_slices_by_path and slice definition hashes on the patchset.
5. Compute required approvals, roles, locks, and checks from all covering slices.
6. Notify reviewers for every affected covering slice.
7. Collect approvals per slice policy.
8. Before submit, recompute coverage and policies against latest definitions.
9. If coverage or policies changed, refresh requirements before continuing.
10. Reapply patch to latest target ref.
11. Run required checks.
12. Publish commit and update target ref with CAS.
```

Coverage refresh outcomes:

```text
unchanged:
  keep current requirements and continue

covering slice added:
  require new slice approvals/checks before submit

covering slice removed:
  remove future requirements from that slice but preserve historical review log

policy changed:
  recompute requirements; stale approvals may need renewal if policy requires it

included path moved:
  mark NeedsRebase or NeedsPolicyRefresh depending on whether the patch still applies
```

The changeset should show coverage explicitly.

Example:

```text
/orgs/acme/services/payment/handler.go
  covering slices:
    orgs/acme/backend
    orgs/acme/payment
  required:
    backend-ci
    payment-owner approval
```

### 15.3 Concurrent Overlap Changes

Two changesets from different authoring slices can edit the same overlapping
path.

They do not merge independently per slice. Queue selection resolves every
covering slice, then places both changesets into the required tenant queues. If
they share any required queue, that queue serializes them.

If the first changeset lands, the second changeset must reapply to the new head.
If the patch no longer applies cleanly, it becomes `NeedsRebase` or
`MergeConflict`.

### 15.4 Approval Semantics

Approvals are recorded against both:

```text
slice_id
slice_definition_hash
```

An approval remains valid only while the relevant slice definition and policy
remain valid for the affected paths, unless the policy explicitly allows stale
approvals.

If a new covering slice appears, that slice has not approved the change yet.

If a covering slice disappears, its approval is retained in the audit log but is
not required for the next submit attempt.

### 15.5 Incompatible Policy Resolution

Most policies compose by union. Some policies can conflict.

When policies conflict, the changeset cannot submit automatically.

Resolution options:

```text
1. Update one or both slice policies.
2. Split the changeset so conflicting paths are reviewed separately.
3. Apply an explicit admin override if the tenant allows overrides.
4. Abandon the changeset.
```

Admin overrides must be audited and should name the conflicting policies they
override.

---

## 16. Versioned Submit Queues

Gitslice does not have one global submit queue.

Each user or organization owns versioned queue definitions under its namespace.
Queue definitions decide how changes touching that tenant's slices are ordered,
validated, and submitted.

### 16.1 Queue Selection

Queue selection happens after changed paths and covering slices are resolved.

```text
changed paths
  -> covering slices
  -> covering tenants
  -> queue rules from /users/{username}/.gitslice/queues/*.yaml
  -> queue rules from /orgs/{org}/.gitslice/queues/*.yaml
  -> required queues
```

If more than one queue matches, the changeset must satisfy all of them.

For the initial design, all required queues for one changeset must agree on the
same `target_ref`. If they do not, the changeset is a queue conflict and cannot
submit until it is split, retargeted, or the queue files are changed.

Examples:

```text
/orgs/acme/services/payment/handler.go
  covering slices:
    orgs/acme/backend
    orgs/acme/payment
  required queues:
    orgs/acme/.gitslice/queues/backend.yaml
    orgs/acme/.gitslice/queues/payments.yaml
```

Queue selection records:

```text
queue_id
queue_definition_hash
target_ref
matched_paths
matched_slices
required_checks
required_approvals
```

### 16.2 Single-Queue Submit

For a changeset assigned to one queue:

```text
1. Wait until the changeset is runnable in that queue.
2. Lease the queue item.
3. Load latest queue definition from the target ref.
4. Recompute changed paths, covering slices, and queue selection.
5. Refresh approvals, roles, locks, and checks.
6. Rebase or reapply onto latest target ref.
7. Run required checks.
8. Create final commit or commits.
9. Atomically update target ref with CAS.
10. Emit indexing events for every affected covering slice.
```

If CAS fails because another queue moved the same target ref first, the worker
reloads the new head, reapplies the patch, and retries while preserving the
changeset's queue position.

### 16.3 Multi-Queue Submit

A changeset can require multiple queues when it touches overlapping slices or
multiple tenants.

Multi-queue submit uses deterministic queue leases.

```text
1. Compute required queue set.
2. Sort queue ids lexicographically.
3. Wait until the changeset is runnable in every required queue.
4. Acquire leases in sorted order.
5. Revalidate queue definitions and covering slices.
6. Reapply patch to latest target ref.
7. Run union of required checks.
8. Commit and CAS-update target ref.
9. Release all leases.
```

Sorted lease acquisition prevents deadlocks.

For the MVP, a multi-queue changeset should be runnable only when it is at the
head of every required queue. This is conservative but easy to reason about.
Later, queues can allow disjoint-path concurrency when their queue files opt in.

### 16.4 Queue Definition Changes

Queue files are versioned. When a queue file changes:

- new changesets use the new queue definition
- open changesets recompute queue selection before submit
- approvals tied to the old queue definition may need renewal
- queued items whose required queue set changed move to `NeedsQueueRefresh`

Queue config changes should not mutate already-submitted history. They affect
future validation and future submit attempts.

### 16.5 Queue Conflicts

Queue conflicts happen when queue definitions disagree.

Examples:

```text
queue A requires check acme-ci
queue B forbids external CI for the same path

queue A targets refs/global/main
queue B targets refs/orgs/acme/release
```

Resolution options:

```text
1. Update one or more queue files.
2. Split the changeset.
3. Retarget the changeset if policy allows.
4. Apply an audited admin override.
5. Abandon the changeset.
```

### 16.6 Why Queues Still Need CAS

Tenant queues remove the global queue bottleneck, but they do not remove the
need for atomic ref updates.

Two independent queues can land disjoint changes against the same target ref at
roughly the same time. CAS ensures only one wins the exact head it validated
against. The losing submitter rebases onto the new head and reruns any required
validation before trying again.

This gives the system both:

- tenant-defined queue policy
- global commit/ref correctness

---

## 17. Optional Path Locks

Gitslice should avoid locks for normal source development.

Explicit locks may still be useful for rare high-risk paths.

Examples:

```bash
gs lock /orgs/acme/infra/prod
gs lock /orgs/acme/releases/2026-Q2.yaml
```

Use locks for:

- large binary files
- critical infrastructure config
- generated snapshots
- schema migrations
- release manifests

Path locks do not replace changesets, review, or submit validation.

---

## 18. Indexing System

Indexes should be incremental and event-driven.

Required indexes:

- code search
- symbol search
- path history
- slice coverage
- build graph
- test graph
- slice projection index
- changed paths index

### 18.1 Event Pipeline

Each submitted commit emits events.

```text
CommitCreated
FileChanged
DirectoryChanged
SliceProjectionInvalidated
SymbolIndexNeeded
BuildGraphInvalidated
```

Async workers update derived indexes.

The source of truth remains:

```text
Ref -> Commit -> Tree -> Blob
```

### 18.2 Index Consistency

Indexes are derived data.

If an index is missing or stale, the system should be able to rebuild it from
the commit graph and slice definitions.

User-facing APIs should expose whether index-backed results are fresh,
stale-but-usable, or unavailable.

---

## 19. Build And CI Integration

Gitslice should integrate with scalable build and CI systems.

Recommended systems:

- Bazel
- Buck2
- Pants
- ordinary CI runners for smaller slices

Required capabilities:

- affected target calculation
- remote execution where available
- remote caching where available
- test impact analysis
- hermetic builds where practical
- build graph indexing

Submission policies should be able to reference required checks.

Example:

```yaml
submit:
  required_owners:
    - identity-team

  checks:
    - //users/nicholas/services/identity/...
    - //users/nicholas/proto/identity/...
```

---

## 20. Service Architecture

Core services:

```text
Object Store
Metadata Service
Slice Service
Workspace Service
Git Gateway
GS API Gateway
Changeset Service
Submit Queue Service
Index Service
Build/CI Service
Auth Service
Replication Service
```

### 20.1 Object Store

Stores file contents and large binary objects.

### 20.2 Metadata Service

Stores trees, commits, refs, slice definitions, changesets, and object metadata.

### 20.3 Slice Service

Manages slice definitions, slice resolution, visibility, roles, included paths,
and projections.

### 20.4 Workspace Service

Manages workspace metadata, sparse hydration, local state synchronization, and
agent workspace operations.

### 20.5 Git Gateway

Implements Git smart HTTP and translates between Git objects and native objects.

### 20.6 GS API Gateway

Implements the native GS protocol used by the CLI, web app, SDKs, and agents.

### 20.7 Changeset Service

Manages changesets, patchsets, review state, and workflow state.

### 20.8 Submit Queue Service

Evaluates versioned tenant queue definitions, manages queue membership and
leases, coordinates multi-queue submissions, and performs final validation before
CAS ref updates.

### 20.9 Index Service

Maintains search, symbol, path history, slice coverage, build, and projection
indexes.

---

## 21. Replication Architecture

Use regional read replicas and controlled write coordination.

Example:

```text
US primary
EU replica
Asia replica
```

Reads should be served locally when possible.

Writes should be coordinated through the region that owns the target ref and the
required tenant queue leases.

Blob replication can be lazy and demand-driven.

Metadata replication must preserve commit/ref consistency.

Ref updates must remain linearizable for queue target refs.

---

## 22. System Invariants

These invariants must not be violated.

```text
1. A committed tree is immutable.
2. A committed blob is immutable and content-addressed.
3. A commit points to exactly one root tree.
4. A ref update is atomic and conditional.
5. A changeset submit either publishes all final commits and moves the target ref, or publishes none.
6. Queue definitions are versioned files under user/org namespaces.
7. A changeset must submit through every queue selected by its affected paths and covering slices.
8. Multi-queue submit must acquire queue leases in deterministic order.
9. A slice projection is deterministic for a given slice id, slice definition hash, and global commit.
10. Default slice history uses the latest accepted slice definition.
11. Slice visibility and roles govern access to all paths included by the slice.
12. A global path may be covered by multiple slices.
13. Writes to overlapping paths must satisfy every covering slice's policy at submit time.
14. Effective read exposure for a path is the broadest visibility of any covering slice.
15. Git synthetic commit IDs are stable for the same projection inputs.
16. Metadata must never reference an unverified blob.
17. Derived indexes can be rebuilt from commits, trees, blobs, slice definitions, and queue definitions.
```

---

## 23. MVP Plan

### Phase 1: Native Object Model

- Content-addressed blob store
- Immutable tree metadata
- Canonical path rules
- Global commit graph
- Atomic refs
- Staged blob upload protocol

### Phase 2: Slice Definitions And Projection

- User and organization tenants
- Slice identity
- Slice definitions as versioned metadata
- Absolute included paths
- Slice-level visibility and roles
- Overlapping slice coverage
- Overlap policy union
- Deterministic projection by latest definition

### Phase 3: Workspace And Native CLI

- Sparse workspace metadata
- On-demand hydration
- Slice add/remove in workspace
- Local status/diff
- Native `gs` workflows

### Phase 4: Changesets And Versioned Queues

- Changeset creation
- Patchsets
- Review state
- Conflict detection
- Covering-slice policy refresh
- Versioned queue definition files
- Queue selection
- Queue leases
- Multi-queue submit coordination
- Atomic ref update

### Phase 5: Git Read Compatibility

- Git smart HTTP endpoint
- Clone from slice URL
- Synthetic Git history
- Fetch
- Partial clone support

### Phase 6: Git Push Into Changesets

- Convert Git diff to global path patchset
- Push to changeset refs
- Changeset creation/update from Git
- Protected branch push policy

### Phase 7: Indexing, CI, And Scale

- Changed path index
- Code search
- Slice coverage index
- Build/test integration
- Regional reads
- Projection cache
- Advanced replication

---

## 24. Example Native Workflow

### 24.1 Create Workspace

```bash
gs workspace init
gs slice add users/nicholas/identity
```

### 24.2 Edit Code

```bash
vim users/nicholas/services/identity/auth.go
```

### 24.3 Create Changeset

```bash
gs cs create
```

### 24.4 Update Changeset

```bash
gs cs update
```

### 24.5 Submit

```bash
gs cs submit
```

Server behavior:

```text
1. Resolve changed absolute paths.
2. Resolve covering slices.
3. Select required tenant queues.
4. Refresh overlap and queue policy requirements.
5. Acquire required queue leases.
6. Check slice roles and approvals.
7. Rebase onto latest target ref.
8. Run required checks.
9. Create commit or commits.
10. Update ref with CAS.
11. Emit indexing events.
```

---

## 25. Example Git Workflow

```bash
git clone https://gitslice.io/git/users/nicholas/identity.git
cd identity
git checkout -b my-change
# edit files
git commit -am "Update auth flow"
git push origin HEAD:refs/changes/new
```

Server behavior:

```text
1. Resolve slice from URL.
2. Authenticate and authorize user.
3. Convert Git diff to global absolute paths.
4. Resolve covering slices.
5. Select required tenant queues.
6. Create changeset.
7. Create patchset.
8. Run validation.
```

---

## 26. Non-Goals For The Initial Design

The initial design should not include:

- special `/shared` or `/system` namespaces
- custom mount aliases inside slices
- direct user-facing commit creation
- single-owner path model
- object-store participation in metadata transactions
- path-level ACLs as the primary access model
- Git-native storage internals

These can be revisited only if a concrete product requirement justifies the
additional complexity.

---

## 27. Long-Term Direction

Gitslice should become a source graph platform with:

- Git-compatible slice repositories
- repository-like access control
- global-scale history and indexing
- sparse workspaces for humans and agents
- changeset-centered collaboration
- atomic multi-slice submission
- native cloud storage and metadata architecture

The architecture should stay simple at the conceptual boundary:

```text
global paths
slice coverage
changesets
immutable commits
atomic refs
Git projection
```
