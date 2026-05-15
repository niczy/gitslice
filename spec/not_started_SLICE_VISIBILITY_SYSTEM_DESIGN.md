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
6. the unit of visibility is the logical path
7. if a path is made public, that path is public in every slice where it exists
8. when making a slice public, the caller can optionally make all paths inside that slice globally `public` or globally `private`

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
- optionally expose specific paths publicly across slices
- allow a whole slice to be made public in one action
- optionally propagate a slice-public action into global path visibility

---

## Why This Should Be Path-Scoped

Visibility must be attached to the **logical path**, not to raw file content and not to
`slice_id + path`.

Why:

- the same content blob can appear in multiple slices
- the same logical path can appear in multiple slices
- the requirement is that making a path public affects that path in every slice
- content-addressed dedupe should not imply shared visibility

So the unit of visibility is:

- `path`

not:

- `slice_id + path`
- content hash
- manifest blob
- block hash

Slice visibility remains separate:

- `slice.visibility` controls whether the whole slice is public
- `path.visibility` controls whether that logical path is `public` or `private` across all slices

So effective public access is:

- `slice is public`
- or the path/nearest ancestor path rule resolves to `public`

This still matches the current user-visible model in
[slice.go](/home/nic/workspace/gitslice/internal/models/slice.go), where slices and
paths are the real product objects, not raw blobs.

---

## Design Principles

1. **Private by default**
   - Every newly created slice, file, and folder starts private.

2. **Only two states**
   - `private`
   - `public`

3. **Path is the visibility unit**
   - Visibility lives on global logical paths.
   - Slice visibility is a separate flag.

4. **Effective visibility is computed**
   - A slice can be public without mutating global path visibility.
   - A path can be public without making the whole slice public.
   - A path can also be explicitly private.
   - Folder visibility is inherited by descendants through path ancestry and can be overridden by deeper explicit path rules.

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

- that file path becomes public in every slice where that exact path exists
- anonymous users can read it through public routes
- its ancestor directories must be publicly traversable through the same effective rules

### Folder visibility

Users can set a folder to:

- `public`
- `private`

Folder visibility is recursive:

- making a folder public makes descendant paths effectively public in every slice
- making a folder private makes descendant paths effectively private in every non-public slice
- descendants may still be public or private if they have their own deeper explicit path rules

### Slice visibility

Users can set a slice to:

- `public`
- `private`

Making a slice public means:

- every file and folder in that slice becomes effectively public
- this can optionally mutate the global path visibility table for the paths currently in that slice
- new files/folders created in that slice are effectively public while the slice stays public

When making a slice public, support a path propagation mode:

- `unchanged`
  - leave global path visibility as-is
- `public`
  - mark every current path in the slice globally `public`
- `private`
  - mark every current path in the slice globally `private`

Making a slice private means:

- the slice returns to respecting only global path visibility plus private defaults
- new files/folders in that slice default private unless their path is already public globally

So the product behavior is:

- slice visibility is slice-local
- path visibility is global
- slice visibility can optionally bulk-write global path visibility
- effective visibility is slice override plus path rules

---

## Read Semantics

### Private

Private content is readable only by authorized users:

- slice owners
- other allowed authenticated principals under existing auth rules

### Public

Public content is readable by:

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

- making `a/b/c.txt` public makes `a/` and `a/b/` effectively public directories
- this applies in every slice where those ancestor paths exist
- siblings stay private unless:
  - the slice is public, or
  - they are under a public ancestor folder, or
  - they have their own public path rule

### Reverse case

If a child path is made private, ancestors are not automatically made private if they
still contain other public descendants or are public through slice visibility.

So ancestor cleanup should be:

- recompute when needed, or
- leave ancestors public if any public descendant still exists

Recommendation for v1:

- derive ancestor traversability from explicit path rules plus slice visibility
- avoid materializing ancestor visibility into every slice row

---

## Write Semantics

Write paths must preserve visibility expectations.

### Create

When creating a new file/folder:

- if slice is public -> new entry is effectively public
- else if the exact path already has an explicit global rule -> respect it
- else if the nearest ancestor folder has an explicit global rule -> inherit it
- else -> new entry becomes private

### Move / Rename

When moving a path:

- destination effective visibility should be recomputed from:
  - destination path rules
  - destination ancestor folder rules
  - slice visibility
- moving a file to a new path does not automatically copy the old path's global visibility rule

### Copy

Same as move:

- resulting destination path visibility is based on destination context

### Repo import / pull / sync-like materialization

Imported or synchronized files inherit the containing slice visibility. New slices default private.

### Slice public toggle

Changing slice visibility always updates the slice record.

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

## Important constraint

Do **not** add visibility to:

- block objects
- manifest blobs
- deduped file content rows

---

## Effective Visibility Model

For v1, reads should compute effective visibility.

Recommended effective lookup:

1. load slice visibility
2. if slice is public, allow read
3. else load explicit visibility for the requested path
4. if the path has an explicit rule, respect it
5. else walk ancestor folder paths for the nearest explicit folder rule
6. if a nearest ancestor folder rule exists, respect it
7. else deny anonymous/public read

So the effective rule is:

- `public if slice_public`
- otherwise `public/private` from exact path rule if present
- otherwise `public/private` from nearest ancestor folder rule if present
- otherwise `private`

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

Anonymous/public search should only return paths that are effectively public in the
searched slice:

- whole slice if the slice is public
- otherwise paths with an exact global `public` rule
- otherwise descendants of globally public folders
- excluding any deeper paths with explicit global `private` rules

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
3. Public routes must not reveal private siblings in a partially public tree unless the slice itself is public.
4. Public file reads must not imply public history or unpublished changesets.
5. Search and directory listing must respect the same visibility decisions as file reads.
6. Content dedupe must not leak cross-slice existence via visibility shortcuts.

---

## Migration Strategy

There is no need for a complex compatibility-preserving migration.

Recommended migration:

1. add schema/model support
2. mark every existing slice `private`
3. create no public path rules initially
4. ship read-side enforcement
5. ship write-side propagation
6. ship UI/CLI controls

Because everything defaults private, the migration is safe and unsurprising.

---

## Testing Strategy

Must cover:

### Storage / service

- new slices default private
- new files/folders inherit the containing slice visibility
- making a slice public makes all descendants effectively public
- making a slice private removes only slice-level public visibility
- move/copy preserves the containing slice visibility
- the same path can have different visibility in different slices

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
2. add storage migration for global path visibility
3. backfill existing slices to private and create no public path rows
4. add storage-layer tests

Acceptance:

1. existing slices become explicitly private
2. new slices/entries default private

### PR3 - Read-side enforcement and public routes

Goal:

- enforce visibility on reads before exposing mutation controls

Changes:

1. enforce effective visibility checks on file reads and listings
2. add public read routes for slices/files
3. keep private routes authenticated
4. add browser/server tests for public vs private reads

Acceptance:

1. anonymous public reads work
2. anonymous private reads fail
3. private siblings do not leak through public routes

### PR4 - Slice-level visibility toggles

Goal:

- make slice-level public/private transitions work

Changes:

1. add `SetSliceVisibility` / `GetSliceVisibility`
2. ensure effective reads treat the whole slice as public when enabled
3. ensure future writes inherit slice visibility correctly
4. add regression tests

Acceptance:

1. making a slice public makes every file/folder in that slice effectively public
2. making the slice private makes every file/folder in that slice effectively private
3. visibility decisions do not depend on repository paths

### PR5 - Slice visibility UI and ancestor handling

Goal:

- support visibility controls through slice visibility only

Changes:

1. remove remaining path visibility controls
2. show effective slice visibility for files and folders
3. make public read endpoints authorize through slice visibility
4. keep write operations owner/admin scoped

Acceptance:

1. file and folder visibility follows the containing slice
2. ancestors remain traversable where required

### PR6 - CLI surface

Goal:

- expose visibility controls through `gs`

Changes:

1. add `gs slice visibility ...`
2. add `gs fs visibility ...`
3. support `--json`
4. add slice-level propagation flags
5. add CLI workflow tests

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
3. making a path public makes that path public in every slice where it exists
4. making a path private makes that path private in every non-public slice where it exists
5. making a slice public makes all files/folders in that slice public
6. slice visibility and path visibility compose cleanly
7. slice-public transitions can optionally bulk-propagate `public` or `private` path rules
8. anonymous users can browse/read only effectively public content
9. private content remains hidden in file reads, listings, browser views, and search
10. new content inherits the correct visibility from its slice/path context
11. the CLI and web app both expose the feature clearly

---

## Recommended v1 Product Stance

Ship the simplest defensible model:

- binary visibility only
- global path visibility + independent slice visibility
- optional slice-to-path bulk propagation
- effective read-time visibility calculation
- public current-tree reads only
- no ACL redesign
- no public unpublished workflow objects

That is enough to make slices and paths shareable without turning this into a full
permissions project on day one.
