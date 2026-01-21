# Git Slice

Slice-based version control for monorepos.

## Overview

Git Slice lets teams carve out focused slices of work, run them end-to-end, and merge back with clarity. No more sprawling branches—just fast, predictable delivery.

## Repository Structure

This repository is organized using the slice-based directory structure:

```
/
├── u/                                      # User slice workspaces
│   ├── .gitkeep                           # Keep directory in git
│   ├── default.toml                       # User configuration template
│   └── <username>/                        # User directories (created at runtime)
│       ├── default.toml                   # User-specific configuration
│       └── slices/                        # User's slices
│
├── o/                                      # Organization slice workspaces
│   ├── .gitkeep                           # Keep directory in git
│   ├── default.toml                       # Organization configuration template
│   └── genesis/                           # Genesis organization (tracked in git)
│       ├── default.toml                   # Genesis org configuration
│       ├── slices/                        # Genesis org slices
│       │   └── default.toml               # Default slice configuration
│       └── project/                       # Genesis org projects
│           └── gitslice/                  # THE GITSLICE SOURCE CODE
│               ├── slice_service/         # Slice service
│               ├── admin_service/         # Admin service
│               ├── gs_cli/                # CLI tool
│               ├── internal/              # Shared packages
│               ├── proto/                 # Protocol buffers
│               ├── web/                   # Web interface
│               ├── ops/                   # Operations scripts
│               ├── spec/                  # Design specifications
│               ├── workflow_test/         # Integration tests
│               ├── Makefile              # Build configuration
│               ├── go.mod                # Go module definition
│               ├── CLAUDE.md             # Development guidelines
│               └── AGENTS.md             # Agent documentation
│
├── .gitignore                             # Git ignore rules
└── README.md                              # This file
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

The gitslice source code lives at `/o/genesis/project/gitslice/`.

```bash
# Navigate to the source code
cd o/genesis/project/gitslice

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
cd o/genesis/project/gitslice

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
- **`/o/genesis/default.toml`** - Genesis organization configuration
- **`/o/genesis/slices/default.toml`** - Default slice configuration for genesis

Copy templates to your user/org directory and customize as needed.

## Web Interface

```bash
cd o/genesis/project/gitslice

# Install web dependencies
make web-install

# Build web app
make web-build

# Run E2E tests
make web-test-e2e
```

## Documentation

Full documentation is available in `/o/genesis/project/gitslice/spec/`:

- [Product Vision](o/genesis/project/gitslice/spec/PRODUCT_VISION.md) - Overview and goals
- [Architecture](o/genesis/project/gitslice/spec/ARCHITECTURE.md) - System architecture
- [API Design](o/genesis/project/gitslice/spec/API_DESIGN.md) - gRPC API documentation
- [CLI Design](o/genesis/project/gitslice/spec/CLI_DESIGN.md) - CLI commands and workflows
- [Data Model](o/genesis/project/gitslice/spec/DATA_MODEL.md) - Data structures
- [Algorithms](o/genesis/project/gitslice/spec/ALGORITHMS.md) - Core algorithms

## Contributing

See [CLAUDE.md](o/genesis/project/gitslice/CLAUDE.md) for development guidelines when working with Claude Code.

## Project Structure

The gitslice project itself is organized under `/o/genesis/project/gitslice/`:

- **Services**: `slice_service/`, `admin_service/`
- **CLI**: `gs_cli/`
- **Shared Code**: `internal/`
- **API Definitions**: `proto/`
- **Web Interface**: `web/`
- **Tests**: `workflow_test/`
- **Documentation**: `spec/`, `CLAUDE.md`, `AGENTS.md`
- **Operations**: `ops/` (deployment scripts)

## License

[License information to be added]
