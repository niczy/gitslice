# Gitslice

Slice-based version control for massive monorepos. Define slices of the codebase your team owns, work independently, and merge seamlessly with automatic conflict detection.

## Install

```bash
# Build from source
go build -o gs ./gs_cli/
```

## Getting Started

### 1. Log in

Gitslice uses a simple username system. Set your username once:

```bash
gs login your_name
```

### 2. Browse the root slice

The root slice contains all files across every imported project:

```bash
gs root
```

### 3. Checkout a slice

Download a slice's files to your local machine:

```bash
mkdir my-project && cd my-project
gs slice checkout <slice-id>
```

### 4. Fork a slice

Create a new slice from a folder in an existing slice:

```bash
gs fork my-team-slice /path/to/folder --parent root_slice
```

### 5. Make changes with changesets

```bash
# Create a changeset from your local modifications
gs changeset create -m "Fix authentication bug"

# List changesets for the current slice
gs changeset list

# Merge a changeset
gs changeset merge <changeset-id>
```

### 6. Import a git repository

Import an external repository into Gitslice:

```bash
gs import git -repo https://github.com/org/repo.git
```

## CLI Reference

```
gs login                   Set or show your username
gs root                    Show root slice info
gs slice checkout <id>     Checkout a slice to current directory
gs slice clone <id>        Alias for checkout
gs fork <id> <path>        Create a new slice from a folder
gs changeset create        Create changeset from local changes
gs changeset list          List changesets for current slice
gs changeset review <id>   Review a changeset
gs changeset merge <id>    Merge a changeset
gs changeset rebase <id>   Rebase a changeset onto latest head
gs conflict list           List conflicts for a slice
gs conflict show <id>      Show conflict details
gs conflict resolve <id>   Resolve a conflict
gs import git              Import a git repository
gs init <slice-id>         Initialize working directory for a slice
gs status                  Show working directory status
gs log [slice-id]          Show slice commit history
```

### Global Flags

```
--addr <host:port>     Server address (default: api.agenttools.dev:443)
--tls                  Use TLS (default: true when using default addr)
--user <name>          Username (overrides GS_USERNAME env and ~/.gitslice/user)
```

### Import Flags

```
gs import git [flags]

  -repo <path-or-url>     Git repo (local path or remote URL)
  -ref <ref>              Git ref to import (default: HEAD)
  -slice <id>             Target slice (default: root_slice)
  -mount <path>           Mount path prefix (default: /o/genesis/projects/<repo-name>)
  -max-commits <n>        Limit number of commits imported (0 = all)
  -first-parent           Import first-parent linear history (default: true)
  -timeout <duration>     Timeout for the import (default: 30m)
```

## Web UI

Browse the repository at [agenttools.dev](https://agenttools.dev). The web interface provides:

- File browsing across all slices
- Diff viewer for changesets
- Slice management

## Architecture

Gitslice runs as a single server process exposing both gRPC (port 50051) and REST (port 8080) APIs. The CLI communicates via gRPC.

For detailed design docs, see the `spec/` directory:

- [Product Vision](spec/PRODUCT_VISION.md)
- [Data Model](spec/DATA_MODEL.md)
- [Architecture](spec/ARCHITECTURE.md)
- [API Design](spec/API_DESIGN.md)
- [CLI Design](spec/CLI_DESIGN.md)

## Development

See [LOCAL_DEV.md](LOCAL_DEV.md) for build instructions, running locally, and testing.

## License

[Add your license here]
