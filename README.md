# Git Slice

Slice-based version control for monorepos.

## Overview

Git Slice lets teams carve out focused slices of work, run them end-to-end, and merge back with clarity. No more sprawling branches—just fast, predictable delivery.

## Slice Organization

Slices are organized using a filesystem-based approach with full paths:

### Slice Paths

- **User slices**: `/u/<username>/slices/<slice-name>`
- **Organization slices**: `/o/<org-name>/slices/<slice-name>`

### Examples

```
/u/alice/slices/payments              # Alice's payments feature slice
/u/andrew/slices/infra/terraform      # Andrew's infrastructure slice
/o/genesis/slices/core-services       # Genesis org's core services slice
/o/acme/slices/platform/core-api      # Acme org's platform API slice
```

## Directory Structure

The repository uses `/u/` and `/o/` directories to organize user and organization slices at runtime:

```
/
├── u/                          # User slice workspaces
│   ├── .gitkeep               # Keep directory in git
│   ├── default.toml           # Default user configuration template
│   └── <username>/            # Individual user directories (created at runtime)
│       ├── default.toml       # User-specific configuration
│       └── slices/            # User's slices
│
├── o/                          # Organization slice workspaces
│   ├── .gitkeep               # Keep directory in git
│   ├── default.toml           # Default organization configuration template
│   └── <org-name>/            # Organization directories (created at runtime)
│       ├── default.toml       # Org-specific configuration
│       └── slices/            # Organization's slices
│
├── slice_service/              # Slice service source code
├── admin_service/              # Admin service source code
├── gs_cli/                     # CLI tool source code
├── internal/                   # Shared internal packages
├── proto/                      # Protocol buffer definitions
├── web/                        # Web interface
└── ...                         # Other source files
```

## Configuration

Each user and organization can have a `default.toml` configuration file that defines:
- What files/directories are included in their scope
- Default exclusion patterns
- Slice definitions and metadata

Templates are provided in:
- `/u/default.toml` - Template for user configurations
- `/o/default.toml` - Template for organization configurations

Copy these templates to your user/org directory and customize as needed.

## Getting Started

### Installation

```bash
# Build the CLI
make build-cli

# Install to your PATH
make install_gs
```

### Basic Usage

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

### Development

```bash
# Install dependencies
make install

# Generate protobuf files
make proto

# Build all services
make build

# Run tests
make test

# Start services
make start-servers
```

### Web Interface

```bash
# Install web dependencies
make web-install

# Build web app
make web-build

# Run E2E tests
make web-test-e2e
```

## Documentation

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
