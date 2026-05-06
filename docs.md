# One versioned filesystem, two work surfaces.

Use `gs fs` for direct cloud edits in your home slice. Use custom slices when you want a fast local checkout. Plain checkout covers local status, diff, restore, sync, and publish on its own without local git metadata.

## Overview

Git Slice is a cloud versioned filesystem built for two real workflows. The first is direct remote work with `gs fs`. The second is focused local work through custom slices and fast checkouts. Both are slice-backed, both keep commit history, and both converge through the same publish model.

- Cloud reads, writes, snapshots, diffs, upload, download, and batch operations through `gs fs`.
- Repo bindings that import a GitHub repo into a home-slice path and optionally let you pull and push it later.
- Focused slice creation and fast `gs slice checkout` for editor-heavy work.
- Tracked publish and merge through `gs slice publish`, with `gs changeset` commands available for review and manual steps.
- Repo browser, history, commit diffs, and slice navigation in the web app.

## Mental model

### Understand the system before choosing a workflow

- Published tree: The shared repo state lives in the published tree. Git Slice stores that as the root slice and treats it as the default base for collaboration.
- Home slice: Each account gets a home slice. `gs fs` works there by default through absolute paths like `/$USER/project/README.md`.
- Custom slice: Create a focused slice when the job deserves a local checkout, editor workflow, tests, and a normal git-shaped working tree.
- Changeset: A changeset is the tracked publish unit. `gs slice publish` creates, updates, or reuses it and merges by default unless you stop at review.
- Blocks and manifests: File transfer is content-addressed. Uploads and checkouts exchange manifests first and only transfer blocks your machine or the server is missing.

## Quick start

### Install and log in

```sh
curl -fsSL https://raw.githubusercontent.com/niczy/gitslice/main/install-gs.sh | sh
gs auth keygen --out ~/.config/gitslice/agent_ed25519
gs auth login --key ~/.config/gitslice/agent_ed25519
```

### Cloud edit with `gs fs`

Best when you want to change remote files immediately.

```sh
gs fs mkdir /$USER/notes
printf "ship the patch\n" | gs fs write /$USER/notes/todo.md
gs fs cat /$USER/notes/todo.md
gs fs snapshot -m "notes update"
```

### Custom slice checkout

Best when the task needs a local editor or tests. Plain checkout is the local workflow, with no separate git mode to manage.

```sh
gs slice create ui-refresh apps/web
mkdir ui-refresh && cd ui-refresh
gs slice checkout <slice-id-or-slug>
gs slice status
gs slice diff
```

## Command map

### Read or patch a remote file directly

Best for notes, tiny fixes, and direct remote edits.

```sh
printf "hotfix shipped remotely\n" | gs fs write /$USER/app/NOTICE.txt
gs fs cat /$USER/app/NOTICE.txt
gs fs snapshot -m "patch notice"
```

### Create a focused local worktree

Plain checkout is the local workflow. Status, diff, restore, sync, and publish all work without local git metadata.

```sh
gs slice list
gs slice create ui-refresh apps/web
mkdir ui-refresh && cd ui-refresh
gs slice checkout <slice-id-or-slug>
gs slice diff
```

### Bind a GitHub repo into a home-slice directory

Best when you want one directory to stay connected to an upstream repo while still using normal `gs fs` edits.

```sh
gs repo import https://github.com/org/repo.git /$USER/vendor/repo --push-enabled
gs repo pull /$USER/vendor/repo
gs repo push /$USER/vendor/repo --message "sync upstream fixes"
```

### Publish local work back to the shared tree

Use this when you are ready to review and merge local slice work. Add `--review-only` if you want to stop before merge.

```sh
gs slice sync
gs slice publish --message "refresh settings page" --files src/routes/settings.tsx
gs changeset show
```

## Cloud filesystem

Use `gs fs` when remote is the fastest path. `gs fs` operates on your home slice using absolute paths. Every mutation produces versioned history, and large transfers are deduplicated through manifest-first block exchange instead of raw full-file reupload.

- Read a remote file: `gs fs cat /$USER/app/README.md`
- Write a remote file: `printf "hello\n" | gs fs write /$USER/app/README.md`
- Create a checkpoint: `gs fs snapshot -m "checkpoint"`
- Inspect changes: `gs fs diff <snapshot-or-commit>`
- Search file contents: `gs fs search live --glob '/$USER/app/**' --json`
- Set browser visibility: `gs fs visibility set /$USER/app public --recursive --json`
- Upload a directory tree: `gs fs upload ./site /$USER/site`
- Sync a directory in one command: `gs fs sync --direction push ./site /$USER/site`
- Batch several mutations: `gs fs batch -f ops.jsonl`

## Repo bindings

Bind a GitHub repo to one directory in your home slice. Use `gs repo` when one absolute path in your home slice should track an upstream repository. Import a repo into a directory, pull future remote updates into that bound path, and optionally push your edits back upstream later.

```sh
gs repo import https://github.com/org/repo.git /$USER/vendor/repo --push-enabled
gs repo status /$USER/vendor/repo
gs repo pull /$USER/vendor/repo
gs repo push /$USER/vendor/repo --message "sync upstream fixes"
gs repo unlink /$USER/vendor/repo
```

- `gs repo import` snapshots the remote repo into one bound directory and records the binding.
- `gs repo pull` refreshes the bound directory from upstream and records a normal home-slice commit.
- `gs repo push` exports the bound directory back to the remote repo when push is enabled.
- The Settings page lists your current bindings so you can see path, branch, push mode, and last sync state.

## Custom slices

Check out a focused slice instead of dragging a whole tree everywhere. A custom slice is the local-work path. Create one around the folder or surface you care about, then check it out. Plain `gs slice checkout` is now the primary local path. It skips git metadata for speed, keeps a local index under `.gs`, and supports local status, diff, restore, sync, and publish directly. The client asks for manifests first and downloads only blocks missing from local cache, so repeat checkouts stay fast.

```sh
gs slice list
gs slice create ui-refresh apps/web
mkdir ui-refresh && cd ui-refresh
gs slice checkout <slice-id-or-slug>
gs slice tree
gs slice diff
gs slice restore
gs slice sync
gs slice publish --message "refresh settings page" --files src/routes/settings.tsx
gs changeset show
```

## Changesets

Publish local work through a tracked changeset. Changesets are the publish unit for checked-out slices. `gs slice publish` creates, updates, or reuses the checkout's tracked changeset and merges it back into the published tree by default. Pass `--review-only` or `--no-merge` when you want to inspect or hand off the changeset before merging.

```sh
$EDITOR src/routes/settings.tsx
gs slice publish --message "refresh settings page" --files src/routes/settings.tsx
gs changeset show
gs slice publish --review-only --message "stage for review" --files src/routes/settings.tsx
gs changeset list --status merged
```

Remote `gs fs` mutations also end up on the same publish model. They create slice history immediately, and publication flows through the same merge logic instead of a separate ad hoc sync path.

## Local cache

Track checked-out slices globally and clean local cache state. Git Slice keeps a global local registry of checked-out slices and their paths under your `~/.gitslice` state. That makes it possible to answer two practical questions quickly: which slices are checked out on this machine, and how much cached object data is still taking space.

```sh
gs slice checkouts
gs slice checkouts --slice home.$USER
gs cache stats --checkouts
gs cache prune
gs cache clear --objects
```

- `gs slice checkouts` reports how many checkouts exist globally and where they live.
- `gs cache stats` shows cached object count, cached bytes, tracked checkouts, and stale records.
- `gs cache prune` removes registry entries for deleted or invalid local worktrees.
- `gs cache clear --objects` wipes cached objects so you can reclaim disk when needed.
- `gs doctor` checks auth, current slice binding, global state, cache stats, and checkout health in one command.

## Web app

Use the browser when you want visibility, not just commands.

- The repo browser shows slices, folders, file previews, and commit history.
- Signed-in home and custom slices support indexed file search in the browser, including regex queries and glob filters.
- Search artifacts are keyed by slice and commit head, so updated slice content gets a fresh index instead of reusing a stale one.
- Raw file URLs use `/raw/<path>` for the published root slice or `/raw/slices/<slice-id>/<path>` for a specific slice, returning bytes instead of base64 JSON.
- Diff pages let you inspect commit patches and changeset patches in the browser.
- The web app defaults signed-in users to their home slice and keeps custom slices available for inspection.
- Slice detail URLs track the selected directory or file, so browser Back and Forward restore navigation state.
- The docs page is rendered from `/docs.md`, so the markdown file stays the source of truth for agent instructions.

## Auth

Authenticate once, then use the same identity everywhere. Human CLI use can start with the device flow. Agent workflows should use an enrolled `ed25519` keypair so login stays non-interactive and machine-readable.

```sh
gs auth keygen --out ~/.config/gitslice/agent_ed25519
gs auth signup --username my-agent --email my-agent@example.com --name "My Agent" --key ~/.config/gitslice/agent_ed25519
gs auth login --key ~/.config/gitslice/agent_ed25519
gs auth claim-token --json
gs auth status --json
gs doctor --json
gs context --json
```

- Use `gs login` or `gs auth login --device` to start browser-approved human CLI auth.
- Use `gs auth signup` and `gs auth login --key` for non-interactive agent auth.
- Use `gs auth claim-token` to create a one-time URL that lets a human attach WorkOS sign-in to an agent-created account.
- `gs auth status --json`, `gs doctor --json`, and `gs context --json` expose stored auth metadata including the session and enrolled agent key ID.
- The web app uses hosted browser auth through the configured provider. Clerk and WorkOS are both supported; username sign-in remains an explicit local/dev fallback.
- The Settings page shows enrolled agent keys, their fingerprints, last-used timestamps, and revoke controls.
- Your account owns a home slice, which is why `gs fs` can work from absolute paths immediately.

## FAQ

### When should I use `gs fs` instead of a checkout?

Use `gs fs` when the change is smaller than the setup cost of a local worktree. If you need an editor session, test loop, or lots of files, use a custom slice checkout.

### What does a slice represent?

A slice is a versioned tree with its own history. Your home slice is your personal default surface. Custom slices are focused branches created for a task or folder.

### Are large transfers deduplicated?

Yes. Uploads and checkouts exchange manifests first and then transfer only missing blocks. Repeated uploads and repeated checkouts get cheaper as cache coverage grows.

### Does `gs fs` bypass the merge model?

No. Remote filesystem mutations create home-slice commits, and publication goes through the same changeset merge model used by normal slice workflows.
