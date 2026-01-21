# Git Slice

Slice-based version control for monorepos.

## Directory Structure

This repository uses a filesystem-based slice organization:

```
/
├── u/                          # User slices
│   ├── default.toml           # Default user configuration template
│   └── <username>/            # Individual user directories
│       ├── default.toml       # User-specific configuration
│       └── slices/            # User's slices
│           └── <slice-name>/
│
├── o/                          # Organization slices
│   ├── default.toml           # Default organization configuration template
│   ├── genesis/               # Genesis org - contains gitslice source code
│   │   ├── default.toml       # Genesis configuration
│   │   ├── README.md          # Main project documentation
│   │   ├── slice_service/     # Slice service
│   │   ├── admin_service/     # Admin service
│   │   ├── gs_cli/            # CLI tool
│   │   ├── internal/          # Shared internal packages
│   │   ├── proto/             # Protocol buffer definitions
│   │   ├── web/               # Web interface
│   │   └── ...                # Other source files
│   │
│   └── <org-name>/            # Other organization directories
│       ├── default.toml       # Org-specific configuration
│       └── slices/            # Organization's slices
│           └── <slice-name>/
│
├── .gitignore                 # Git ignore rules
└── .git/                      # Git repository data
```

## Slice Paths

Slices are identified by their full filesystem paths:

- **User slices**: `/u/<username>/slices/<slice-name>`
- **Organization slices**: `/o/<org-name>/slices/<slice-name>`

Examples:
- `/u/alice/slices/payments` - Alice's payments feature slice
- `/u/andrew/slices/infra/terraform` - Andrew's infrastructure slice
- `/o/genesis/slices/core-services` - Genesis org's core services slice
- `/o/acme/slices/platform/core-api` - Acme org's platform API slice

## Getting Started

### For Gitslice Source Code Development

The gitslice source code lives in `/o/genesis/`. To work on it:

```bash
cd o/genesis
make build
```

See `/o/genesis/README.md` for full development documentation.

### For Using Gitslice

1. **Initialize a slice workspace**:
   ```bash
   gs init /u/<username>/slices/<slice-name>
   ```

2. **Create a new slice**:
   ```bash
   gs slice create /u/<username>/slices/<slice-name>
   ```

3. **Checkout a slice**:
   ```bash
   gs slice checkout /u/<username>/slices/<slice-name>
   ```

## Configuration

Each user and organization can have a `default.toml` configuration file that defines:
- What files/directories are included in their scope
- Default exclusion patterns
- Slice definitions and metadata

### Templates

- `/u/default.toml` - Template for user configurations
- `/o/default.toml` - Template for organization configurations

Copy these templates to your user/org directory and customize as needed.

## Documentation

Full documentation is available in `/o/genesis/`:
- `/o/genesis/README.md` - Main project README
- `/o/genesis/spec/` - Design specifications and architecture docs
- `/o/genesis/CLAUDE.md` - Development guidelines for Claude

## License

See `/o/genesis/README.md` for license information.
