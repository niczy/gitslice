# Slice Visibility System Design

## Implementation Status

- Current status: `not started`
- Last updated: `2026-04-01`

---

## Goal

Add a simple visibility system for slices, folders, and files with only two states:

- `private`
- `public`

The required product rules are:

1. every slice starts private
2. every file and folder starts private
3. a file can be made public or private
4. a folder can be made public or private
5. if a slice is made public, every file and folder in that slice becomes public

This is a binary visibility model, not a full ACL system.

---

## Problem

Today Gitslice has strong ownership and collaboration semantics, but no first-class
path visibility model for public browsing/reading.

That creates several product gaps:

- there is no way to expose a slice, folder, or file publicly
- there is no way to make content private-by-default in a user-visible, explicit way
- the current older account-level share/ACL surface is more complex than what is needed here
- web/browser/file read paths do not have a simple public/private policy to enforce

The desired model is much simpler than general sharing:

- default everything to private
- optionally expose specific content publicly
- allow a whole slice to be made public in one action

---

## Why This Should Be Path-Scoped

Visibility must be attached to the **path inside a slice**, not to raw file content.

Why:

- the same content blob can appear in multiple slices
- one path may be public in one slice and private in another
- content-addressed dedupe should not imply shared visibility

So the unit of visibility is:

- `slice_id + path`

not:

- content hash
- manifest blob
- block hash

This matches the current model in [slice.go](/home/nic/workspace/gitslice/internal/models/slice.go),
where the user-visible objects are slices and directory entries, not raw blobs.

---

## Design Principles

1. **Private by default**
   - Every newly created slice, file, and folder starts private.

2. **Only two states**
   - `private`
   - `public`

3. **Path-scoped visibility**
   - Visibility lives on slice-local file and directory entries.

4. **Explicit bulk state, not dynamic inheritance-only reads**
   - If a slice becomes public, all contained paths are explicitly marked public.
   - If a folder becomes public, all descendants are explicitly marked public.

5. **Simple public-read semantics**
   - Public means anonymous read/browse is allowed.
   - Public never means anonymous write/admin.

6. **Do not turn this into ACL v2**
   - User/team/org ACLs and public visibility are separate concerns.
   - v1 should stay binary and easy to reason about.

---

## Non-Goals

- Do not implement user/team/org ACL redesign here.
- Do not add per-file secret links in v1.
- Do not make changesets, unpublished history, or admin metadata public by default.
- Do not attach visibility to deduped file blobs.
- Do not add mixed read/write public permissions.

---

## Relation To Existing Share / ACL APIs

There is already an older account-node ACL/share surface in
[account_service.proto](/home/nic/workspace/gitslice/proto/account/account_service.proto):

- `GetNodeACL`
- `UpsertNodeACL`
- `DeleteNodeACL`
- `CreateShare`
- `ListShared`
- `DeleteShare`

This new visibility system should **not** be implemented as a thin wrapper over that
surface in v1.

Reason:

- those APIs are oriented around principal-aware ACL/sharing
- this feature needs a binary public/private path model
- forcing public visibility through the older share abstraction adds unnecessary
  complexity to read paths, UI, and CLI

Recommendation:

- keep the old share/ACL APIs untouched for now
- implement visibility as a dedicated slice/filesystem concern
- consider future convergence only after the public/private model is proven

---

## User-Facing Behavior

### Defaults

- new slice: `private`
- new folder: `private`
- new file: `private`

### File visibility

Users can set a file to:

- `public`
- `private`

If a file becomes public:

- anonymous users can read it if they know the containing public route
- its ancestor directories must be publicly traversable

### Folder visibility

Users can set a folder to:

- `public`
- `private`

Folder visibility is recursive:

- making a folder public makes all descendants public
- making a folder private makes all descendants private

### Slice visibility

Users can set a slice to:

- `public`
- `private`

Making a slice public means:

- all current folders become public
- all current files become public
- new files/folders created in that slice should default public until the slice is private again

Making a slice private means:

- all current folders become private
- all current files become private
- new files/folders in that slice default private

This is intentionally bulk and explicit, matching the requested product behavior.

---

## Read Semantics

### Private

Private paths are readable only by authorized users:

- slice owners
- other allowed authenticated principals under existing auth rules

### Public

Public paths are readable by:

- anonymous users
- authenticated users

Public read includes:

- directory listing
- file content reads
- browser navigation
- search visibility

Public read does **not** include:

- write
- delete
- rename
- merge
- publish
- changeset operations
- admin operations

---

## Ancestor Rules

The model needs one explicit rule for path discoverability:

### Rule

If a file or folder is public, every ancestor directory up to the slice root must also
be publicly traversable.

That means:

- making `a/b/c.txt` public must also make `a/` and `a/b/` public directories
- siblings of that file stay private unless explicitly changed

### Reverse case

If a child path is made private, ancestors are not automatically made private if they
still contain other public descendants.

So ancestor cleanup should be:

- recompute when needed, or
- leave ancestors public if any public descendant still exists

Recommendation for v1:

- on file/folder private transitions, run a subtree-aware cleanup for empty public ancestry
- correctness matters more than minimal write cost here

---

## Write Semantics

Write paths must preserve visibility expectations.

### Create

When creating a new file/folder:

- if parent folder is public -> new entry becomes public
- else if slice is public -> new entry becomes public
- else -> new entry becomes private

### Move / Rename

When moving a path:

- destination visibility should be recomputed from destination parent and slice defaults
- do not preserve old visibility blindly if the destination subtree has different visibility

### Copy

Same as move:

- resulting destination path visibility is based on destination context

### Repo import / pull / sync-like materialization

Imported or synchronized files must inherit:

- slice-level public default
- or destination-folder public state

### Slice public toggle

Changing slice visibility must bulk update:

- every directory path in the slice
- every file path in the slice

---

## Data Model

## Visibility enum

Add a shared enum:

- `VISIBILITY_PRIVATE`
- `VISIBILITY_PUBLIC`

Implementation can store:

- text (`private`, `public`)
- or integer enum

but the logical model should be the same across proto, storage, and UI.

## Slice visibility

Extend the slice model with:

- `visibility`

Conceptually:

```go
type Slice struct {
    ...
    Visibility string // "private" | "public"
}
```

## Path visibility

Visibility should live on slice-local file and directory entries.

Recommended storage shape:

- `slice_path_visibility`
  - `slice_id`
  - `path`
  - `entry_type` (`file` | `directory`)
  - `visibility`
  - `updated_by`
  - `updated_at`

Alternative implementation:

- add `visibility` directly to the existing slice-local path records if those are already
  the canonical entry rows

Both are acceptable as long as visibility remains path-scoped and slice-scoped.

## Important constraint

Do **not** add visibility to:

- block objects
- manifest blobs
- deduped file content rows

---

## Effective Visibility Model

For v1, reads should rely on explicit stored visibility for the target path.

Recommended effective lookup:

1. load slice visibility
2. load path visibility for the requested path
3. treat explicit path visibility as authoritative
4. if no explicit path visibility exists, fall back to:
   - nearest ancestor folder visibility, then
   - slice visibility

However, after any bulk update or write, storage should strive to keep descendant visibility
explicit enough that read-time fallback stays simple.

---

## Public Routes

We need first-class public read routes for:

- public slice browsing
- public file reads

Likely web/API shape:

- browser route:
  - `/public/<slice-slug>/...`
- file/content route:
  - `/v1/public/slices/{slice}/files/...`

These routes must:

- bypass normal auth requirements for public reads
- still enforce private visibility on hidden paths
- avoid exposing non-public siblings or metadata

Recommendation:

- keep public routes separate from authenticated private browsing routes
- do not overload normal private routes with surprising anonymous semantics

---

## Search Behavior

Search must respect visibility.

### Private search

Authenticated users can search private content they are allowed to read.

### Public search

Anonymous/public search should only return:

- public slices
- public folders
- public files

This applies to:

- browser search
- future public slice search
- remote indexed search paths

Any search index must treat visibility as a filter, not a separate copy of content.

---

## CLI Surface

Recommended CLI additions:

### Slice visibility

```bash
gs slice visibility get <slice>
gs slice visibility set <slice> public
gs slice visibility set <slice> private
```

### Path visibility

```bash
gs fs visibility get /<user>/path
gs fs visibility set /<user>/path public
gs fs visibility set /<user>/path private
gs fs visibility set /<user>/folder public --recursive
```

Behavior:

- default output should be human-readable
- `--json` should be supported
- `--recursive` required for folders unless implied by folder default behavior

---

## Web UX

### Browser

Add visibility UI in the repo browser:

- slice badge: `private` / `public`
- folder badge
- file badge
- action menu:
  - `Make public`
  - `Make private`

### Settings / management

For slices:

- show slice visibility in settings/list views
- allow bulk “make slice public”

### Public links

For public content:

- show a copyable public URL

For private content:

- no public URL should be exposed

---

## Security Rules

1. Private is the default for all newly created content.
2. Anonymous access must never mutate data.
3. Public routes must not reveal private siblings in a partially public tree.
4. Public file reads must not imply public history or unpublished changesets.
5. Search and directory listing must respect the same visibility decisions as file reads.
6. Content dedupe must not leak cross-slice existence via visibility shortcuts.

---

## Migration Strategy

There is no need for a complex compatibility-preserving migration.

Recommended migration:

1. add schema/model support
2. mark every existing slice `private`
3. mark every existing path `private`
4. ship read-side enforcement
5. ship write-side propagation
6. ship UI/CLI controls

Because everything defaults private, the migration is safe and unsurprising.

---

## Testing Strategy

Must cover:

### Storage / service

- new slices default private
- new files/folders default private
- making a slice public marks all descendants public
- making a folder public marks all descendants public
- making a file public makes required ancestor dirs traversable
- making a slice private marks all descendants private
- move/copy into public/private destinations recalculates visibility correctly

### Auth / read enforcement

- anonymous read succeeds for public file
- anonymous read fails for private file
- anonymous directory listing only shows public entries
- anonymous public search excludes private entries

### Workflow / e2e

- CLI set/get visibility
- browser shows visibility and respects it
- public URL works for public content
- public URL fails for private content

---

## Recommended Implementation Order

### PR1 - Proto and model scaffolding

Goal:

- introduce shared visibility enum and API skeleton

Changes:

1. extend slice/filesystem protos with visibility enums and request/response shapes
2. extend `models.Slice` with slice visibility
3. add model wiring/tests without enabling behavior yet

Acceptance:

1. visibility concepts exist in proto/model form
2. no read/write behavior regression

### PR2 - Storage schema and default-private migration

Goal:

- persist visibility in storage and backfill everything to private

Changes:

1. add storage migration for slice visibility
2. add storage migration for path visibility
3. backfill existing slices and entries to private
4. add storage-layer tests

Acceptance:

1. existing content becomes explicitly private
2. new slices/entries default private

### PR3 - Read-side enforcement and public routes

Goal:

- enforce visibility on reads before exposing mutation controls

Changes:

1. enforce visibility checks on file reads and listings
2. add public read routes for slices/files
3. keep private routes authenticated
4. add browser/server tests for public vs private reads

Acceptance:

1. anonymous public reads work
2. anonymous private reads fail
3. private siblings do not leak through public routes

### PR4 - Slice-level visibility toggles

Goal:

- make whole-slice public/private transitions work

Changes:

1. add `SetSliceVisibility` / `GetSliceVisibility`
2. bulk update all paths in a slice on visibility transitions
3. ensure future writes inherit slice visibility
4. add regression tests

Acceptance:

1. making a slice public makes every file/folder public
2. making it private reverses the slice state cleanly

### PR5 - Folder/file visibility toggles and ancestor handling

Goal:

- support per-folder and per-file visibility

Changes:

1. add `SetPathVisibility` / `GetPathVisibility`
2. recursive folder propagation
3. file public -> ancestor traversal fixup
4. file/folder private -> ancestor cleanup when needed
5. write-path propagation for create/move/copy/import

Acceptance:

1. file and folder visibility behave predictably
2. ancestors remain traversable where required

### PR6 - CLI surface

Goal:

- expose visibility controls through `gs`

Changes:

1. add `gs slice visibility ...`
2. add `gs fs visibility ...`
3. support `--json`
4. add CLI workflow tests

Acceptance:

1. visibility can be inspected and changed from the CLI
2. output is agent-friendly

### PR7 - Web UI and public-link UX

Goal:

- expose visibility in the browser/settings

Changes:

1. add visibility badges and toggles
2. add public URL copy UX
3. add slice-level public controls
4. add web e2e coverage

Acceptance:

1. web users can inspect and change visibility
2. public URLs are easy to use

### PR8 - Search/index integration and hardening

Goal:

- make the rest of the system visibility-aware

Changes:

1. visibility filtering for search
2. visibility checks in public/browser index paths
3. edge-case hardening
4. audit logs or metrics if needed

Acceptance:

1. public/private semantics are consistent across read, browse, and search
2. no obvious leakage paths remain

---

## Success Criteria

The feature is complete when:

1. every slice/file/folder defaults private
2. file and folder visibility can be changed independently
3. making a slice public makes all files/folders in it public
4. anonymous users can browse/read only public content
5. private content remains hidden in file reads, listings, browser views, and search
6. new content inherits the correct visibility from its slice/folder context
7. the CLI and web app both expose the feature clearly

---

## Recommended v1 Product Stance

Ship the simplest defensible model:

- binary visibility only
- explicit subtree updates
- public current-tree reads only
- no ACL redesign
- no public unpublished workflow objects

That is enough to make slices and paths shareable without turning this into a full
permissions project on day one.
