# CLI Design: gitslice (gs)

## Overview

The **gitslice** CLI (short: `gs`) provides a command-line interface for the slice-based version control system. It enables developers to work with slices, manage change lists, and resolve conflicts efficiently.

**Key Design Principle:**
- **One directory = one slice** - Each working directory is bound to a specific slice
- **No slice switching in place** - Create new directory for each slice
- **Clear mental model** - Directory you're in = Slice you're working on

**CLI Names:**
- Full: `gitslice`
- Short: `gs`

---

## Installation

### Setup

```bash
# Install via Homebrew (macOS)
brew install gitslice

# Install via apt (Linux)
apt install gitslice

# Install via npm (cross-platform)
npm install -g @gitslice/cli

# Verify installation
gs --version
gitslice --version
```

### Configuration

```bash
# Set API endpoint
gs config set api.endpoint https://api.gitslice.com

# Set authentication token
gs config set auth.token YOUR_API_TOKEN

# List configuration
gs config list
```

---

## Core Commands

### 1. Slice Management

#### Create Slice

```bash
# Create new slice
gs slice create my-team --files "services/my-team/**"

# Create with specific files
gs slice create billing --files services/billing/** tests/billing/**

# Create with description
gs slice create frontend-react --description "React components and hooks"
```

#### List Slices

```bash
# List all available slices
gs slice list

# List with details
gs slice list --detailed

# List slices you have access to
gs slice list --mine

# Search slices
gs slice list --search billing
```

#### Get Slice Information

```bash
# Get slice details
gs slice info my-team

# Get slice status
gs slice status my-team

# Get slice owners
gs slice owners my-team
```

#### Initialize Working Directory

```bash
# Initialize current directory for slice
# This creates .gs config binding directory to slice
gs init my-team

# Create directory in specific path
gs init my-team --path ./work/my-team

# Directory must be empty
gs init my-team ./empty-directory
```

---

### 2. Change List Workflow

### How It Works

**Change lists** provide a review workflow before changes are committed to slice history:

1. **Create** - Create change list from local modifications
2. **Review** - Review changes against slice head (diff, stats)
3. **Merge** - Merge change list into slice (creates commit)
4. **Resolve** - Resolve conflicts if detected during merge
5. **Abandon** - Discard unwanted change lists

### Create Change List

**Command:**
```bash
# Create change list from modified files
gs changeset create --message "Add region-based tax calculation"

# Create with explicit files
gs changeset create files/payment.py --message "Fix bug"

# Create with diff from specific commit
gs changeset create --base abc123 --message "Feature X"

# Create from staged files
gs changeset create --staged

# Create with reviewers
gs changeset create --reviewer alice --reviewer bob
```

**Internal Implementation:**
1. Scans working directory for modified/deleted/added files
2. Calculates file hashes (SHA-256) for each file
3. Uploads file contents to object store via presigned URLs
4. Sends change list metadata to server (slice_id, base_commit, file list)
5. Server returns unique changeset_id
6. Current working directory bound to this changeset

---

### Review Change List

**Command:**
```bash
# Review current changeset
gs changeset review

# Review specific changeset
gs changeset review cl-abc123

# Review with diff output
gs changeset review --diff

# Review with file-level details
gs changeset review --file-level

# Review in external diff tool
gs changeset review --external-diff

# Show summary only (no diff)
gs changeset review --summary
```

**Internal Implementation:**
1. Fetches current working directory's slice and changeset info
2. Gets current slice head commit from server
3. Compares working tree to base commit
4. Generates diff summary (files added/modified/deleted, lines changed)
5. Shows warnings if slice has advanced since changeset created
6. Returns review status: READY_FOR_MERGE, NEEDS_REBASE, or HAS_CONFLICTS

---

### Merge Change List

**Command:**
```bash
# Merge current changeset
gs changeset merge

# Merge specific changeset
gs changeset merge cl-abc123

# Merge with custom message
gs changeset merge --message "Merge after conflict resolution"

# Merge and update working directory
gs changeset merge --update-workdir

# Merge without conflict check (not recommended)
gs changeset merge --force
```

**Internal Implementation:**
1. Fetches changeset metadata and modified files from server
2. Runs conflict detection against other slices:
   - For each modified file, check `file:{file_id}:active_slices`
   - If slice_id NOT IN active_slices AND active_slices not empty → CONFLICT
3. If no conflicts:
   - Builds new tree from base commit + changeset objects
   - Computes new commit hash
   - Updates slice state and active tracking on server
   - Returns SUCCESS with new_commit_hash
4. If conflicts:
   - Returns CONFLICT error with conflicting files and slice IDs
   - Client must resolve before retrying merge

---

### Rebase Change List

**Command:**
```bash
# Rebase current changeset
gs changeset rebase

# Rebase specific changeset
gs changeset rebase cl-abc123

# Rebase with auto-merge of slice changes
gs changeset rebase --auto-merge

# Rebase with conflict markers
gs changeset rebase --markers
```

**Internal Implementation:**
1. Gets current changeset's base commit
2. Gets current slice head commit from server
3. Calculates commits between old base and new head
4. Checks for file-level conflicts:
   - If slice modified same files as changeset since creation → CONFLICT
5. If no conflicts:
   - Updates changeset's base commit to current slice head
   - Returns slice commits to apply to working directory
6. Client updates working directory files and retries merge
7. If conflicts:
   - Returns CONFLICT with conflicting files and commits
   - Client must manually resolve

---

### List Change Lists

**Command:**
```bash
# List all changesets for current slice
gs changeset list

# List pending changesets
gs changeset list --status pending

# List with limit
gs changeset list --limit 20

# List with filter
gs changeset list --author alice
```

**Internal Implementation:**
1. Fetches working directory's slice
2. Queries server for all changesets bound to that slice
3. Displays with status (PENDING, IN_REVIEW, APPROVED, MERGED, ABANDONED)
4. Shows metadata: author, created_at, files count, message

---

### Abandon Change List

**Command:**
```bash
# Abandon current changeset
gs changeset abandon

# Abandon specific changeset
gs changeset abandon cl-abc123

# Abandon with reason
gs changeset abandon --reason "Superseded by cl-xyz789"
```

**Internal Implementation:**
1. Fetches changeset metadata
2. Updates status to ABANDONED on server
3. Working directory no longer bound to this changeset
4. Returns success message

---

## 3. Conflict Resolution

### Show Conflicts

**Command:**
```bash
# Show all conflicts for current working directory
gs conflict list

# Show conflicts for specific changeset
gs conflict list --changeset cl-abc123

# Show conflicts in detail
gs conflict list --detailed

# Show conflicts with severity levels
gs conflict list --severity
```

**Internal Implementation:**
1. Queries server for conflicts affecting current slice
2. Shows conflicting files with:
   - File paths and IDs
   - Conflicting slice IDs
   - Severity (HIGH, MEDIUM, LOW, CRITICAL)
   - Modification timestamps
   - Conflict type (semantic, formatting, structural)

---

### Resolve Conflicts

**Command:**
```bash
# Interactive conflict resolution
gs conflict resolve

# Auto-resolve (use incoming changes)
gs conflict resolve --theirs

# Auto-resolve (use local changes)
gs conflict resolve --ours

# Mark conflict as resolved (after manual edit)
gs conflict resolve --resolved file.py

# Show conflict details before resolving
gs conflict show file.py

# Get conflict history
gs conflict history file.py
```

**Internal Implementation:**
1. For interactive mode:
   - Lists all pending conflicts
   - User selects conflict to resolve
   - Presents resolution options (theirs, ours, manual edit)
   - Updates conflict resolution status on server
2. For auto-resolve modes:
   - Applies resolution automatically
   - Updates working directory files accordingly
   - Marks conflict as resolved on server
3. On manual resolution:
   - User edits file externally
   - User marks conflict as resolved
   - Server updates conflict status

---

## 4. Commit History

### View Log

**Command:**
```bash
# Show slice commit history
gs log

# Show current working directory's slice history
gs log

# Show specific slice history
gs log my-team

# Show N commits
gs log --limit 10

# Show commits since specific commit
gs log --since abc123

# Show detailed log
gs log --detailed

# Show with graph visualization
gs log --graph

# Show commits from specific author
gs log --author alice
```

**Internal Implementation:**
1. Fetches working directory's slice from .gs config
2. Queries server for slice commit history
3. Displays commits in reverse chronological order
4. Each commit shows:
   - Short hash
   - Commit message
   - Author and timestamp
   - Files changed count
   - Parent commit hash

---

### View Commit Details

**Command:**
```bash
# Show specific commit
gs show abc123

# Show with diff
gs show abc123 --diff

# Show with file list
gs show abc123 --files

# Show parent commits
gs show abc123 --parents

# Show full patch
gs show abc123 --patch
```

**Internal Implementation:**
1. Fetches commit metadata from server
2. Displays:
   - Full commit hash
   - Tree structure
   - Commit message and metadata
   - Files changed (list or detailed)
   - Parent commits
   - Optional: Unified diff output

---

### Compare Commits

**Command:**
```bash
# Compare two commits
gs diff abc123 def456

# Compare with file filter
gs diff abc123 def456 --filter "*.py"

# Compare with summary only
gs diff abc123 def456 --summary

# Compare with word diff
gs diff abc123 def456 --word-diff

# Compare with color output
gs diff abc123 def456 --color
```

**Internal Implementation:**
1. Fetches both commit objects from server
2. Compares their tree structures
3. Generates Merkle tree diff
4. Shows:
   - Files added/modified/deleted/renamed
   - Line-by-line or word-level diffs
   - Summary statistics

---

## 5. Global State

### View Global Status

**Command:**
```bash
# View global repository state
gs global status

# Show global commit history
gs global log

# Show current global commit
gs global show

# Show which slices are in global state vs pending
gs global status --pending

# Show global state statistics
gs global stats
```

**Internal Implementation:**
1. Queries global state from server
2. Displays:
   - Current global commit hash
   - Last merge timestamp
   - Slices merged into current commit
   - Slices with pending changes
   - Total files and commits in global state

---

### Trigger Batch Merge

**Command:**
```bash
# Trigger batch merge (admin only)
gs global merge

# Merge with limits
gs global merge --max-slices 100

# Merge specific slices
gs global merge --slice my-team --slice billing-service

# Dry-run merge (preview only)
gs global merge --dry-run

# Merge with specific global commit as parent
gs global merge --parent abc123
```

**Internal Implementation:**
1. Validates admin permissions
2. Triggers batch merge job on server
3. Displays merge progress:
   - Slices being merged
   - Estimated time remaining
4. Returns new global commit hash

---

## Working Directory Model

### One Directory = One Slice

**Core Principle:**
```
Each working directory is bound to exactly one slice when initialized.
Directory location = Slice identity
```

**Directory Structure:**
```bash
my-team/                    # Bound to my-team slice
  ├── .gs/                      # Directory binding
  │   ├── config                # Slice ID this directory is bound to
  │   ├── current_changeset      # Current active changeset
  │   └── state                # Working directory state
  ├── services/                  # Slice files
  ├── tests/                     # Slice files
  └── README.md

billing-service/             # Bound to billing-service slice
  ├── .gs/                      # Different slice, different binding
  ├── services/
  └── tests/
```

### Initialize Working Directory

**Command:**
```bash
# Initialize current directory for slice
gs init my-team

# Directory must be empty (or use --force)
gs init my-team --force

# Initialize with custom path
gs init my-team --path ./work/my-team

# Initialize with description
gs init my-team --description "My team's services"
```

**Internal Implementation:**
1. Validates directory is empty (unless --force)
2. Creates `.gs/` directory with:
   - `config`: Slice ID this directory belongs to
   - `current_changeset`: Active changeset ID (if any)
   - `state`: Working directory state (clean, conflicts, etc.)
3. Fetches slice manifest from server
4. Downloads slice files to directory
5. Directory is now ready for development

**Switching to Different Slice:**
```bash
# Switch slices by changing directory
cd ../billing-service   # Different directory = different slice
gs log                   # Shows billing-service history

# Or create new directory
gs init billing-service --path ./work/billing
```

---

### Working Directory Commands

**Show Directory State**

```bash
# Show current directory's slice binding
gs status

# Show working directory state
gs status --detailed

# Show conflicts in current working directory
gs status --conflicts

# Show uncommitted files
gs status --uncommitted
```

**Internal Implementation:**
1. Reads `.gs/config` to get slice ID
2. Queries server for slice status
3. Scans working directory for uncommitted changes
4. Displays:
   - Current slice ID and head commit
   - Active changeset (if any)
   - Uncommitted files count
   - Any conflicts
   - Working directory state (clean/dirty)

---

## Global Cache

### How It Works

**Global cache** is shared across all working directories to speed up slice checkout and reduce redundant downloads.

**Key Design:**
```
~/.gitslice/cache/
├── manifests/                 # Cached slice manifests (by commit hash)
├── objects/                    # Cached object contents
├── metadata/                   # Cached slice metadata
└── .lockfile                    # Cache coordination
```

**Cache Strategy:**
- **Manifest Cache:** Full file lists per commit (precomputed from server)
- **Object Cache:** Frequently accessed objects (deduplicated across slices)
- **Metadata Cache:** Slice information, file ownership
- **Coordinated Access:** Shared lock prevents cache corruption
- **TTL-based Eviction:** Auto-expire stale cache entries

### Cache Commands

```bash
# Show cache statistics
gs cache stats

# Clear entire cache
gs cache clear

# Clear specific slice cache
gs cache clear --slice my-team

# Prefetch slice (preload cache)
gs cache prefetch my-team

# Verify cache integrity
gs cache verify
```

### Configuration

**Cache settings in config file:**

```yaml
cache:
  enabled: true
  location: ~/.gitslice/cache
  max_size_mb: 1000
  max_age_hours: 24
  compression: true
  parallel_downloads: 20
  manifest_ttl_hours: 1
  object_ttl_hours: 168  # 7 days

eviction:
  policy: lru  # Least recently used
  strategy: adaptive  # Based on access patterns
```

### Cache Hit Behavior

**On Slice Checkout:**
1. Check cache for manifest (by commit hash)
2. If cache hit:
   - Return manifest immediately
   - No server round-trip
   - Checkout: ~10-50ms
3. If cache miss:
   - Fetch manifest from server
   - Update cache for future checkouts
   - Checkout: ~100-500ms

**On Object Fetch:**
1. Check cache for object (by hash)
2. If cache hit:
   - Serve from local cache
   - No S3 download
3. If cache miss:
   - Download from S3 with presigned URL
   - Update cache
   - Save bandwidth and time

### Internal Implementation

**Cache Key Structure:**
```python
# Manifest cache
manifest:{commit_hash} -> SliceManifest
  - commit_hash: string
  - file_metadata: [FileMetadata objects]
  - created_at: timestamp
  - access_count: int
  - last_accessed: timestamp

# Object cache
object:{hash} -> CachedObject
  - hash: string
  - content: bytes
  - size: int
  - created_at: timestamp
  - last_accessed: timestamp
  - compressed: bool

# Slice metadata cache
slice:{slice_id} -> SliceMetadata
  - slice_id: string
  - manifest_cache_hits: int
  - object_cache_hits: int
  - last_prefetch: timestamp
```

**Cache Operations:**
```python
# Get from cache
def get_manifest(commit_hash):
  return cache.get(f"manifest:{commit_hash}")

# Update cache
def update_manifest(commit_hash, manifest):
  cache.set(f"manifest:{commit_hash}", manifest, ttl=3600)  # 1 hour

# Prefetch slice
def prefetch_slice(slice_id):
  slice = get_slice_metadata(slice_id)
  for commit_hash in slice.recent_commits:
    ensure_manifest_cached(commit_hash)
  update_last_prefetch(slice_id)
```

### Performance Impact

**Without cache:**
- Small slice (100 files): 100-500ms per checkout
- Medium slice (10K files): 200-500ms per checkout
- Large slice (100K files): 500-1000ms per checkout

**With cache:**
- Small slice (100 files): 10-50ms per checkout (90% faster)
- Medium slice (10K files): 20-100ms per checkout (80% faster)
- Large slice (100K files): 100-500ms per checkout (90% faster)
- Bandwidth savings: 80-95% for frequently accessed slices

**Cache Size Estimates:**
- 100K slices, avg 100 files/slice
- Avg 10KB per file, 1KB per manifest
- Cache: 1-2GB for manifests (100K * 10KB)
- Object cache: 10GB for frequently accessed objects
- Total: ~12GB for full cache

---

## Workflows

### Daily Development Workflow

```bash
# 1. Start day - initialize slice directory
gs init my-team

# 2. Make changes
vim services/payment.py

# 3. Create change list from changes
gs changeset create --message "Add refund support"
# Output: Changeset created: cl-abc123

# 4. Review changes
gs changeset review
# Output: 2 files modified, no conflicts, ready to merge

# 5. Merge to slice
gs changeset merge
# Output: Success - New commit: b2c3d4e5

# 6. Handle conflicts if any
gs changeset merge
# Output: ✗ Conflict in services/payment.py
#    Modified by: api-gateway (2 hours ago)
#    Run: gs conflict resolve
#    Then: gs changeset merge
```

### Multi-Slice Workflow

```bash
# 1. Work on slice A (in its directory)
cd ./my-team-frontend
gs changeset create --message "Update button styles"
gs changeset merge

# 2. Work on slice B (in its directory)
cd ./my-team-backend
gs changeset create --message "Update API routes"
gs changeset merge

# 3. Check global state (any directory)
gs global status
# Shows both slices' status in global state
```

### Conflict Resolution Workflow

```bash
# 1. Detect conflict on merge
gs changeset merge
# ✗ Conflict detected in file.py

# 2. Show conflicts
gs conflict list

# 3. Show specific conflict
gs conflict show file.py

# 4. Resolve conflict
gs conflict resolve --theirs
# Output: Applied incoming changes

# 5. Retry merge
gs changeset merge
# ✓ Success - New commit: y2z3w4x5
```

### Historical Investigation Workflow

```bash
# 1. View commit history
gs log --limit 20

# 2. Checkout specific historical commit
# Since directories are bound to slices, use `gs show` instead
gs show b2c3d4e5

# 3. View file at that commit
gs show b2c3d4e5 --files --path services/payment.py

# 4. Compare commits
gs diff abc123 HEAD
```

---

## Advanced Features

### Stashing

```bash
# Stash current work
gs stash save "WIP"

# List stashes
gs stash list

# Apply stash
gs stash apply stash-0

# Drop stash
gs stash drop stash-0
```

### Git Integration

```bash
# Import git repository into gitslice
gs import from-git ./my-git-repo

# Export gitslice to git
gs export to-git

# Sync with git repository
gs sync with-git

# Create git branch from slice
gs export my-team --branch feature-x
```

### Hooks

```bash
# Add pre-changeset-merge hook
gs hook add pre-merge --command "npm test"

# Add post-changeset-merge hook
gs hook add post-merge --command "npm run deploy"

# List hooks
gs hook list

# Remove hook
gs hook remove pre-merge
```

### Caching

```bash
# Clear cache
gs cache clear

# Show cache stats
gs cache stats

# Prefetch slice for faster checkouts
gs cache prefetch my-team
```

---

## Configuration Options

### Environment Variables

```bash
# API endpoint
export GITSLICE_API_ENDPOINT=https://api.gitslice.com

# Authentication token
export GITSLICE_AUTH_TOKEN=your-token

# Default editor for conflict resolution
export GITSLICE_EDITOR=vim

# Diff tool preference
export GITSLICE_DIFF_TOOL=vscode

# Number of parallel file downloads
export GITSLICE_PARALLEL_DOWNLOADS=10
```

### Config File

Located at `~/.gitslice/config.yaml`:

```yaml
api:
  endpoint: https://api.gitslice.com
  timeout: 30s
  retries: 3

auth:
  method: token
  token_file: ~/.gitslice/token

defaults:
  editor: vim
  diff-tool: diff

performance:
  parallel_downloads: 10
  compression: true
  cache_size: 1GB

colors:
  enabled: true
  theme: dark

workflows:
  auto-commit: false
  stash_on_switch: false
  create_changeset_on_init: false
```

---

## Output Formats

### JSON Output

```bash
# Enable JSON for scripting
gs slice list --json

# Example output:
{
  "slices": [
    {
      "id": "my-team",
      "name": "My Team",
      "files": 245,
      "status": "active"
    },
    {
      "id": "billing-service",
      "name": "Billing Service",
      "files": 189,
      "status": "active"
    }
  ]
}
```

### Table Output

```bash
# Tabular output (default)
gs changeset list --format table

# Example output:
+-----------+-----------+--------+----------------+----------------+
| CHANGESET | SLICE     | STATUS | AUTHOR         | CREATED AT      |
+-----------+-----------+--------+----------------+----------------+
| cl-abc123 | my-team   | READY  | alice          | 2024-01-15 10:30 |
| cl-def456 | billing   | PENDING| bob            | 2024-01-15 09:15 |
+-----------+-----------+--------+----------------+----------------+
```

---

## Common Use Cases

### Quick Start

```bash
# First-time setup
gs config set api.endpoint https://api.gitslice.com
gs config set auth.token YOUR_TOKEN

# Start working on a slice
mkdir my-team && cd my-team
gs init my-team

# Make your first change
vim services/payment.py

# Create and merge changeset
gs changeset create --message "Initial commit"
gs changeset merge
```

### Feature Development

```bash
# Feature branch equivalent
gs changeset create --message "Feature: new payment flow"
# Work on it...
gs changeset review
gs changeset merge
```

### Bug Fix

```bash
# Fix bug from historical commit
gs log --grep "bug"
gs show abc123 --files --path services/payment.py
vim services/payment.py
gs changeset create --message "Fix: payment calculation"
gs changeset merge
```

### Team Collaboration

```bash
# Review teammate's changeset
gs changeset review cl-abc123 --diff

# Add comment
gs changeset comment cl-abc123 --message "LGTM, minor nit"
```

### Deployment

```bash
# Check global state
gs global status

# Trigger batch merge
gs global merge

# Deploy specific global commit
deploy g1h2i3j4 --env production
```

---

## Error Handling

### Common Errors

```bash
# Conflict on merge
Error: Conflict detected in services/payment.py
Solution: gs conflict resolve

# Directory not initialized
Error: Working directory not initialized. Run: gs init <slice_id>
Solution: gs init my-team

# Changeset outdated
Error: Changeset base commit is outdated. Slice has advanced
Solution: gs changeset rebase

# Authentication failed
Error: Invalid authentication token
Solution: gs config set auth.token YOUR_TOKEN
```

### Error Codes

```bash
# General errors
1 - Unknown error
2 - Network error
3 - Authentication error
4 - Permission denied
5 - Rate limited

# Slice errors
10 - Slice not found
11 - Not authorized for slice
12 - Slice is locked

# Changeset errors
20 - Changeset not found
21 - Changeset already merged
22 - Changeset conflicts with slice head
23 - Working directory not initialized for slice
24 - Changeset bound to different slice

# Conflict errors
30 - Conflict detected
31 - Unresolvable conflict
32 - Merge conflict
33 - File-level conflict
```

---

## Performance Tips

### Faster Operations

```bash
# Use parallel file downloads
gs config set performance.parallel_downloads 20

# Enable compression
gs config set performance.compression true

# Cache frequently accessed slices
gs cache prefetch my-team

# Use JSON output for scripting
gs changeset list --json --quiet | jq '.[] | select(.status=="pending")'
```

---

## Integration with Other Tools

### Editor Integration

```bash
# VS Code extension
code --install-extension gitslice.vscode

# Vim plugin
PlugInstall gitslice.vim

# Emacs mode
(add-hook 'before-save-hook 'gs status')
```

### CI/CD Integration

```bash
# GitHub Actions
- name: Test changeset
  run: |
    gs changeset review ${{ github.event.changeset.id }}
    gs changeset merge ${{ github.event.changeset.id }}

# Jenkins Pipeline
pipeline {
  agent any
  stages {
    stage('Test') {
      steps {
        sh 'gs changeset review ${CHANGESET_ID}'
        sh 'gs changeset merge ${CHANGESET_ID}'
      }
    }
  }
}
```

---

## Help and Documentation

### Getting Help

```bash
# General help
gs --help
gs help

# Command-specific help
gs slice init --help
gs changeset merge --help

# Show examples
gs help --examples
gs changeset create --examples
```

### Documentation Links

```bash
# Open documentation in browser
gs docs

# Show API docs
gs docs api

# Show troubleshooting guide
gs docs troubleshooting
```

---

## Version Management

### Check Updates

```bash
# Check for updates
gs update check

# Update to latest version
gs update install

# Check installed version
gs --version

# Show changelog
gs changelog
```

---

## Comparison with Git

| Aspect | Traditional Git | gitslice |
|--------|----------------|-----------|
| Working Model | Switch branches in same directory | Different directory = different slice |
| Mental Model | Current branch = where you are | Current directory = which slice you're on |
| Conflict Timing | On merge (too late) | On changeset merge (proactive) |
| Review Process | PR review then merge | Changeset review + conflict check |
| Scope | Entire repository | Single slice |
| Checkout Time | Full repo | Single slice |
| Parallel Work | Switch branches | Multiple directories |

---

## Conclusion

The **gitslice** CLI provides a comprehensive interface for slice-based version control system:

✅ **Slice management** - Create, list, info, init working directories
✅ **Change list workflow** - Create, review, merge, rebase, list, abandon
✅ **Conflict resolution** - Interactive and automated
✅ **Commit history** - Log, show, diff
✅ **Global operations** - Status, merge, deploy
✅ **Working directory model** - One directory = one slice (simple & clear)

Commands follow consistent naming patterns and include helpful flags for customization.