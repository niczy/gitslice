# Git Slice

Slice-based version control for monorepos.

## Overview

Git Slice lets teams carve out focused slices of work, run them end-to-end, and merge back with clarity. No more sprawling branches—just fast, predictable delivery.

## Repository Structure

### Git Repository Structure (at rest)

In the git repository, the source code lives at the root:

```
/
├── admin_service/         # Admin service source code
├── slice_service/         # Slice service source code
├── gs_cli/                # CLI tool source code
├── internal/              # Shared internal packages
├── proto/                 # Protocol buffer definitions
├── web/                   # Web interface
├── ops/                   # Operations scripts
├── spec/                  # Design specifications
├── workflow_test/         # Integration tests
├── Makefile              # Build configuration
├── go.mod                # Go module definition
├── CLAUDE.md             # Development guidelines
├── AGENTS.md             # Agent documentation
│
├── u/                     # User slice workspaces
│   ├── .gitkeep          # Keep directory in git
│   └── default.toml      # User configuration template
│
├── o/                     # Organization slice workspaces
│   ├── .gitkeep          # Keep directory in git
│   ├── default.toml      # Org configuration template
│   └── genesis/          # Genesis organization config
│       ├── default.toml  # Genesis org configuration
│       └── slices/       # Genesis org slices
│           └── default.toml  # Default slice config
│
├── .gitignore            # Git ignore rules
└── README.md             # This file
```

### Runtime Structure (when server initializes)

When the gitslice server starts, it mounts the repository at `/o/genesis/project/gitslice/`:

```
/
├── u/                                      # User slice workspaces
│   ├── default.toml                       # User configuration template
│   └── <username>/                        # User directories (created at runtime)
│       ├── default.toml                   # User-specific configuration
│       └── slices/                        # User's slices
│           └── <slice-name>/
│
├── o/                                      # Organization slice workspaces
│   ├── default.toml                       # Org configuration template
│   └── genesis/                           # Genesis organization
│       ├── default.toml                   # Genesis org configuration
│       ├── slices/                        # Genesis org slices
│       │   └── default.toml               # Default slice configuration
│       └── project/                       # Genesis projects (runtime mount)
│           └── gitslice/                  # GITSLICE REPO MOUNTED HERE
│               ├── admin_service/
│               ├── slice_service/
│               ├── gs_cli/
│               ├── internal/
│               ├── proto/
│               ├── web/
│               └── ...
```

The mounting is configured in `/o/genesis/default.toml`:
```toml
[runtime]
mount_point = "/o/genesis/project/gitslice"
```

## Slice Paths

Slices are identified by their full filesystem paths:

- **User slices**: `/u/<username>/slices/<slice-name>`
- **Organization slices**: `/o/<org-name>/slices/<slice-name>`

### Examples

```
/u/alice/slices/payments              # Alice's payments feature slice
/u/andrew/slices/infra/terraform      # Andrew's infrastructure slice
/o/genesis/slices/core-services       # Genesis org's core services slice
/o/acme/slices/platform/core-api      # Acme org's platform API slice
```

## Getting Started

### Development Setup

For development, work directly in the repository root:

```bash
# Install dependencies
make install

# Generate protobuf files
make proto

# Build all services
make build

# Run tests
make test
```

### Building the CLI

```bash
# Build the CLI
make build-cli

# Install to your PATH
make install_gs
```

### Using Gitslice

Once the CLI is installed:

```bash
# Initialize a slice workspace
gs init /u/<username>/slices/<slice-name>

# Create a new slice
gs slice create /u/<username>/slices/<slice-name>

# Checkout a slice
gs slice checkout /u/<username>/slices/<slice-name>

# List all slices
gs slice list
```

## Configuration

Configuration files define what's included in each scope:

- **`/u/default.toml`** - Template for user configurations
- **`/o/default.toml`** - Template for organization configurations
- **`/o/genesis/default.toml`** - Genesis organization configuration (includes runtime mount_point)
- **`/o/genesis/slices/default.toml`** - Default slice configuration for genesis

## Runtime Mounting

When the server initializes:

1. The gitslice repository (currently at `/`) is mounted to `/o/genesis/project/gitslice/`
2. This is configured via the `runtime.mount_point` setting in `/o/genesis/default.toml`
3. User and org workspaces are created under `/u/` and `/o/` as needed

This separation allows:
- Clean git repository structure (source at root)
- Proper runtime organization (source under `/o/genesis/project/gitslice/`)
- Demonstration of the slice organization model

## Web Interface

```bash
# Install web dependencies
make web-install

# Build web app
make web-build

# Run E2E tests
make web-test-e2e
```

## Documentation

Full documentation is available in the `spec/` directory:

- [Product Vision](spec/PRODUCT_VISION.md) - Overview and goals
- [Architecture](spec/ARCHITECTURE.md) - System architecture
- [API Design](spec/API_DESIGN.md) - gRPC API documentation
- [CLI Design](spec/CLI_DESIGN.md) - CLI commands and workflows
- [Data Model](spec/DATA_MODEL.md) - Data structures
- [Algorithms](spec/ALGORITHMS.md) - Core algorithms

## Contributing

See [CLAUDE.md](CLAUDE.md) for development guidelines when working with Claude Code.

## License

[License information to be added]
