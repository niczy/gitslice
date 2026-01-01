# CLI Design: gitslice (gs)

## Overview

The **gitslice** CLI (short: `gs`) provides a command-line interface for the slice-based version control system. It enables developers to work with slices, manage change lists, and resolve conflicts efficiently.

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

# Set default slice
gs config set default-slice my-team

# Set authentication token
gs config set auth.token YOUR_API_TOKEN

# List configuration
gs config list
```

---

## Core Commands

### 1. Slice Management

#### List Slices

```bash
# List all available slices
gs slice list

# List with details
gs slice list --detailed

# List slices you have access to
gs slice list --mine
```

#### Get Slice Information

```bash
# Get slice details
gs slice info my-team

# Get slice status
gs slice status my-team
```

#### Checkout Slice

```bash
# Checkout latest version
gs slice checkout my-team

# Checkout specific commit
gs slice checkout my-team --commit abc123

# Checkout specific folder
gs slice checkout my-team --path ./my-workspace

# Checkout without downloading files (metadata only)
gs slice checkout my-team --metadata-only
```

#### Show Slice Files

```bash
# List files in current slice
gs slice files

# List files recursively
gs slice files --recursive

# List with sizes
gs slice files --sizes
```

---

### 2. Change List Workflow

#### Create Change List

```bash
# Create change list from modified files
gs changeset create --message "Add region-based tax calculation"

# Create with explicit files
gs changeset create files/payment.py --message "Fix bug"

# Create with diff from specific commit
gs changeset create --base abc123 --message "Feature X"

# Create with interactive mode
gs changeset create --interactive

# Create with reviewers
gs changeset create --reviewer alice --reviewer bob
```

#### Review Change List

```bash
# Review latest change list
gs changeset review

# Review specific change list
gs changeset review cl-abc123

# Review with diff output
gs changeset review --diff

# Review with file-level details
gs changeset review --file-level

# Review in external diff tool
gs changeset review --external-diff
```

#### Merge Change List

```bash
# Merge current change list
gs changeset merge

# Merge specific change list
gs changeset merge cl-abc123

# Merge with force (skip conflict check - not recommended)
gs changeset merge --force

# Merge with custom message
gs changeset merge --message "Merge after conflict resolution"
```

#### Rebase Change List

```bash
# Rebase current change list
gs changeset rebase

# Rebase specific change list
gs changeset rebase cl-abc123

# Rebase with auto-merge
gs changeset rebase --auto-merge

# Rebase with conflict markers
gs changeset rebase --markers
```

#### List Change Lists

```bash
# List all change lists for current slice
gs changeset list

# List pending change lists
gs changeset list --status pending

# List with limit
gs changeset list --limit 20

# List for specific slice
gs changeset list --slice my-team
```

#### Abandon Change List

```bash
# Abandon current change list
gs changeset abandon

# Abandon specific change list
gs changeset abandon cl-abc123
```

---

### 3. Conflict Resolution

#### Show Conflicts

```bash
# Show all conflicts for current slice
gs conflict list

# Show conflicts for specific change list
gs conflict list --changeset cl-abc123

# Show conflicts in detail
gs conflict list --detailed
```

#### Resolve Conflicts

```bash
# Interactive conflict resolution
gs conflict resolve

# Auto-resolve (use incoming changes)
gs conflict resolve --theirs

# Auto-resolve (use local changes)
gs conflict resolve --ours

# Mark conflict as resolved
gs conflict resolve --resolved file.py
```

#### Get Conflict Details

```bash
# Get details for specific file conflict
gs conflict show file.py

# Get conflict history
gs conflict history file.py
```

---

### 4. Commit History

#### View Log

```bash
# Show slice commit history
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
```

#### View Commit Details

```bash
# Show specific commit
gs show abc123

# Show with diff
gs show abc123 --diff

# Show with file list
gs show abc123 --files

# Show parent commits
gs show abc123 --parents
```

#### Compare Commits

```bash
# Compare two commits
gs diff abc123 def456

# Compare with file filter
gs diff abc123 def456 --filter "*.py"

# Compare with summary only
gs diff abc123 def456 --summary
```

---

### 5. Global State

#### View Global Status

```bash
# View global repository state
gs global status

# Show global commit history
gs global log

# Show current global commit
gs global show
```

#### Trigger Batch Merge

```bash
# Trigger batch merge (admin only)
gs global merge

# Merge with limits
gs global merge --max-slices 100

# Merge specific slices
gs global merge --slice my-team --slice billing-service
```

---

## Workflows

### Daily Development Workflow

```bash
# 1. Start day - checkout slice
gs slice checkout my-team

# 2. Make changes
vim services/payment.py

# 3. Create change list
gs changeset create --message "Add refund support"

# 4. Review changes
gs changeset review --diff

# 5. Merge to slice (if ready)
gs changeset merge

# 6. Handle conflicts if any
gs conflict resolve --interactive
gs changeset merge
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
vim file.py  # Edit file manually

# 5. Mark as resolved
gs conflict resolve --resolved file.py

# 6. Retry merge
gs changeset merge
```

### Historical Investigation Workflow

```bash
# 1. View commit history
gs log --limit 20

# 2. Checkout specific commit
gs slice checkout my-team --commit abc123

# 3. Compare with current
gs diff abc123 HEAD

# 4. View specific changes
gs show abc123 --files
```

### Multi-Slice Workflow

```bash
# 1. Work on slice A
gs slice checkout frontend-react
gs changeset create --message "Update button styles"
gs changeset merge

# 2. Switch to slice B
gs slice checkout backend-api

# 3. Make compatible changes
vim api/routes.ts

# 4. Create and merge change list
gs changeset create --message "Update for new button"
gs changeset merge

# 5. Check global state
gs global status
```

---

## Commands Reference

### Global Flags

```bash
--verbose, -v           # Verbose output
--quiet, -q             # Quiet mode
--json                   # JSON output format
--no-color              # Disable colored output
--config <path>          # Use specific config file
--help, -h              # Show help
--version                # Show version
```

### Command Aliases

```bash
# Short forms for quick access
gs co                   # alias for slice checkout
gs cs                   # alias for changeset
gs ci                   # alias for changeset info
gs ca                   # alias for changeset create
gs cm                   # alias for changeset merge
gs cr                   # alias for changeset review
```

---

## Configuration Options

### Environment Variables

```bash
# API endpoint
export GITSLICE_API_ENDPOINT=https://api.gitslice.com

# Authentication token
export GITSLICE_AUTH_TOKEN=your-token

# Default slice
export GITSLICE_DEFAULT_SLICE=my-team

# Editor for conflict resolution
export GITSLICE_EDITOR=vim

# Diff tool
export GITSLICE_DIFF_TOOL=vscode
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
  slice: my-team
  review-tool: vim
  diff-tool: diff

performance:
  parallel-downloads: 10
  cache-size: 1GB
  compression: true

colors:
  enabled: true
  theme: dark
```

---

## Interactive Mode

### Interactive Changeset Creation

```bash
$ gs changeset create --interactive

Modified files:
  ✓  services/payment.py (modified)
  ✓  tests/payment_test.py (modified)
  ✗  docs/api.md (not modified)

Select files to include in changeset:
[1] All modified files
[2] Select specific files
> 1

Enter commit message:
> Add region-based tax calculation

Add reviewers (comma-separated, optional):
> alice, bob

Enter tags (comma-separated, optional):
> feature, payment

✓ Change list created: cl-abc123
```

### Interactive Conflict Resolution

```bash
$ gs conflict resolve

Conflicts:
  [1] services/payment.py
  [2] tests/payment_test.py

Select conflict to resolve:
> 1

<<<<<<< HEAD (your changes)
amount = calculate_tax(amount)
=======
>>>>>>> incoming (slice my-team)
amount = calculate_tax(amount, region)

Select resolution:
[1] Use your changes (ours)
[2] Use incoming changes (theirs)
[3] Edit manually
[4] Open in external editor
> 3

Opening in vim...
```

---

## Advanced Features

### Batch Operations

```bash
# Batch create change lists
gs changeset batch-create changeset-list.yaml

# Batch merge change lists
gs changeset batch-merge changeset-list.yaml

# Batch checkout multiple slices
gs slice batch-checkout slice-list.yaml
```

### Hooks

```bash
# Pre-changeset-merge hook
gs hook add pre-merge --command "npm test"

# Post-changeset-merge hook
gs hook add post-merge --command "git push origin main"

# List hooks
gs hook list

# Remove hook
gs hook remove pre-merge
```

### Workspaces

```bash
# List workspaces
gs workspace list

# Create workspace
gs workspace create ./my-project

# Switch workspace
gs workspace use my-project

# Remove workspace
gs workspace remove my-project
```

### Caching

```bash
# Clear cache
gs cache clear

# Show cache stats
gs cache stats

# Pre-fetch slice
gs cache prefetch my-team
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
gs init
gs auth login

# Start working
gs slice checkout my-team
vim file.py
gs changeset create --message "Fix bug"
gs changeset merge
```

### Feature Branch Equivalent

```bash
# Create feature branch equivalent
gs changeset create --message "Feature: new payment flow"
# Work on it...
gs changeset review
gs changeset merge
```

### Bug Fix Equivalent

```bash
# Fix bug in historical commit
gs log --grep "bug"
gs slice checkout --commit abc123
vim file.py
gs changeset create --message "Fix: payment calculation"
gs changeset merge
```

### Review Team's Changes

```bash
# Review someone else's changeset
gs changeset review cl-abc123 --diff

# Add comment
gs changeset comment cl-abc123 --message "LGTM, minor nit"
```

### Deploy to Production

```bash
# Check global state
gs global status

# Trigger batch merge
gs global merge

# Deploy specific global commit
gs deploy g1h2i3j4 --env production
```

---

## Error Handling

### Common Errors

```bash
# Conflict on merge
Error: Conflict detected in services/payment.py
Solution: gs conflict resolve

# Stale changeset
Error: Changeset base commit is outdated
Solution: gs changeset rebase

# Authentication failed
Error: Invalid authentication token
Solution: gs auth login

# Network error
Error: Connection timeout
Solution: gs config set api.timeout 60s
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

# Conflict errors
30 - Conflict detected
31 - Unresolvable conflict
32 - Merge conflict
```

---

## Performance Tips

### Faster Operations

```bash
# Use parallel downloads
gs config set performance.parallel-downloads 20

# Enable compression
gs config set performance.compression true

# Cache frequently accessed slices
gs cache prefetch my-team billing-service

# Use JSON output for scripting
gs changeset list --json --quiet | jq '.[] | select(.status=="pending")'
```

### Network Optimization

```bash
# Set lower timeout for fast networks
gs config set api.timeout 10s

# Use compression for slow networks
gs config set performance.compression true

# Disable progress bars for scripts
gs changeset merge --quiet
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
(add-hook 'before-save-hook 'gs changeset status')

# JetBrains IDEA
Preferences → Plugins → Install gitslice
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
      }
    }
  }
}

# GitLab CI
test:
  script:
    - gs changeset review $CI_MERGE_REQUEST_ID
    - gs changeset merge $CI_MERGE_REQUEST_ID
```

### Git Integration

```bash
# Convert git repo to gitslice
gs import from-git

# Sync gitslice with git
gs sync with-git

# Convert gitslice commit to git
gs export to-git
```

---

## Troubleshooting

### Debug Mode

```bash
# Enable debug logging
gs config set debug true

# Show verbose output
gs changeset merge --verbose --verbose

# Check connection
gs health check
```

### Cache Issues

```bash
# Clear cache
gs cache clear

# Reset configuration
gs config reset

# Verify installation
gs doctor
```

### Performance Issues

```bash
# Check performance stats
gs perf stats

# Benchmark operations
gs perf benchmark checkout
gs perf benchmark merge

# Show profile
gs perf profile changeset merge
```

---

## Help and Documentation

### Getting Help

```bash
# General help
gs --help
gs help

# Command-specific help
gs slice checkout --help
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

## Alias Reference

### Common Aliases

```bash
# Shorten common commands
alias gsc="gs changeset create"
alias gsm="gs changeset merge"
alias gsr="gs changeset review"
alias gss="gs slice status"

# Quick checkout
alias gco="gs slice checkout"
alias gl="gs log"

# Quick conflict resolution
alias gcr="gs conflict resolve --theirs"
alias gco="gs conflict resolve --ours"
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

## Migration Guide

### From Git to gitslice

```bash
# Initialize gitslice in git repo
gs init

# Detect git branches as slices
gs detect slices

# Convert git history to gitslice
gs migrate --from-git

# Push to gitslice
gs push --all
```

### From gitslice to Git

```bash
# Export to git
gs export --to-git

# Clone as git repo
gs clone --as-git my-team

# Create git branch from slice
gs export slice my-team --branch feature-x
```

---

## Conclusion

The `gitslice` CLI provides a comprehensive interface for the slice-based version control system, with intuitive commands for all major workflows:

✅ **Slice management** - Checkout, list, info
✅ **Change list workflow** - Create, review, merge, rebase
✅ **Conflict resolution** - Interactive and automated
✅ **History management** - Log, show, diff
✅ **Global operations** - Status, merge, deploy
✅ **Advanced features** - Hooks, workspaces, caching
✅ **Integration** - Editors, CI/CD, Git compatibility

Commands follow consistent naming patterns and include helpful flags for customization.