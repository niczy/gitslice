# Claude Development Guidelines for Gitslice

This document contains important reminders and guidelines for Claude when working on the gitslice project.

Before starting any development work in this repository, read `local_dev.md` and follow its operational notes.

## Pre-PR Checklist

### CRITICAL: Always Run Tests Before Creating PRs

Before committing and pushing code, **ALWAYS** run the following checks:

1. **Build Verification**
   ```bash
   go build -o /tmp/core_server ./servers/core/
   go build -o /tmp/gs ./gs_cli/
   ```
   - Ensure all services compile without errors
   - Check for syntax errors, duplicate declarations, undefined variables
   - Verify all imports are correct

2. **Run Tests** (when available)
   ```bash
   # Storage tests
   go test -v ./internal/storage/... -timeout 30s

   # Service tests
   (cd services/admin && go test -v ./... -timeout 30s)
   (cd services/file && go test -v ./... -timeout 30s)
   (cd services/slice && go test -v ./... -timeout 30s)

   # Core server tests
   (cd servers/core && go test -v ./... -timeout 30s)

   # Integration tests (requires RUN_INTEGRATION_TESTS=1)
   RUN_INTEGRATION_TESTS=1 go test -v ./workflow_test/... -timeout 60s
   ```

3. **Proto Files Are Auto-Generated**
   ```bash
   make build  # automatically runs 'make proto'
   ```
   - ⚠️ **IMPORTANT**: Generated `*.pb.go` files are NOT committed to git
   - Proto files are regenerated automatically by Makefile during build
   - If you modify `.proto` files, just run `make build` or `make proto`
   - Never manually edit `*.pb.go` or `*.pb.gw.go` files

4. **Lint Check** (if available)
   ```bash
   golangci-lint run
   ```

### Common Issues to Avoid

1. **Duplicate Variable Declarations**
   - ❌ `ctx := context.Background()` declared twice
   - ✅ Declare once, reuse the variable
   - Example: Lesson learned from PR #28 (duplicate ctx in servers/core/main.go)

2. **Missing Imports**
   - Always verify imports after refactoring
   - Check that all new packages are imported

3. **Hardcoded Values**
   - Use `internal/config` package for configuration
   - Environment variables should have defaults
   - **Configuration Consistency**: When services read configurable addresses, all references must use config
   - Example: If the core server uses `cfg.GetCoreServiceAddr()`, gateway must also use it (not hardcode `:50051`)

4. **Input Validation**
   - Use `internal/common/validation.go` functions
   - Validate all user inputs for security

5. **Proto Changes Without Implementation**
   - Adding RPC to proto requires implementing the handler
   - Generated files (*.pb.go) are not committed - they're auto-generated during build
   - Server must implement all RPCs to avoid Unimplemented errors
   - After modifying .proto files, verify the build succeeds with new generated code

## Project Architecture

### Key Packages

- **`internal/common/`** - Shared utilities
  - `init.go` - Root slice initialization
  - `validation.go` - Input validation (security)
  - `health.go` - Health check handlers

- **`internal/config/`** - Centralized configuration
  - All port numbers and addresses
  - Environment variable handling

- **`internal/gateway/`** - gRPC-Gateway helpers
- **`services/`** - gRPC service implementations
  - `admin/` - Admin service
  - `slice/` - Slice service
  - `file/` - File service (read-only)

- **`internal/storage/`** - Storage layer
  - `memory.go` - In-memory storage (development)
  - `postgres.go` - PostgreSQL-backed storage (durable metadata)
  - `objectstore.go` - GCS-compatible object storage backend

- **`gs_cli/`** - CLI client
  - Split into command files (not monolithic)
  - `commands_*.go` - Command implementations
  - `utils.go` - Shared utilities
  - `help.go` - Help text

### Service Ports

- Core server (gRPC + HTTP Gateway): `50051` (configurable via `CORE_SERVICE_PORT`; legacy envs still accepted)

### Important Patterns

1. **Error Handling**
   - Services should fail fast on initialization errors
   - Use `log.Fatalf()` for critical startup failures
   - Use proper gRPC status codes for RPC errors

2. **Context Usage**
   - Create once, pass down
   - Use `context.Background()` for long-lived operations
   - Use `context.WithTimeout()` for client operations

3. **Storage Initialization**
   - Always use `common.EnsureRootSliceInitialized()`
   - Don't duplicate root slice initialization logic

### Protobuf Workflow

**IMPORTANT**: Generated protobuf files are NOT committed to the repository.

**Why This Approach:**
- Prevents stale generated code from causing build failures
- Generated files are always in sync with `.proto` definitions
- Smaller git history (no binary/generated file diffs)
- Eliminates "forgot to regenerate" bugs

**How It Works:**
1. `.proto` files in `proto/` directories are committed to git
2. `*.pb.go` and `*.pb.gw.go` files are in `.gitignore`
3. `make build`, `make test`, and individual build targets depend on `make proto`
4. Proto files are auto-generated during every build

**When Modifying Proto Files:**
```bash
# 1. Edit the .proto file
vim proto/admin/admin_service.proto

# 2. Build (this regenerates proto files automatically)
make build

# 3. Implement any new RPC handlers in the service
vim services/admin/server.go

# 4. Verify it builds and works
make test
```

**First Time Setup:**
```bash
# Install protoc and Go plugins
make install

# This installs:
# - protoc-gen-go (protobuf Go code generator)
# - protoc-gen-go-grpc (gRPC Go code generator)
# - protoc-gen-grpc-gateway (HTTP gateway generator)
```

## Security Considerations

### Input Validation

Always validate user inputs:
```go
import "github.com/niczy/gitslice/internal/common"

// Validate slice IDs
if err := common.ValidateSliceID(sliceID); err != nil {
    return nil, status.Error(codes.InvalidArgument, err.Error())
}

// Validate file paths (prevent directory traversal)
if err := common.ValidateFilePath(filePath); err != nil {
    return nil, status.Error(codes.InvalidArgument, err.Error())
}
```

### Known Security Gaps (TODO)

- ⚠️ No authentication/authorization implemented yet
- ⚠️ gRPC connections use insecure credentials
- ⚠️ CORS set to allow all origins (`*`)
- ⚠️ No rate limiting or resource quotas

## Code Style

### Refactoring Guidelines

1. **CLI Commands**
   - Keep command files focused (one concern per file)
   - Use `commands_*.go` pattern
   - Extract common utilities to `utils.go`

2. **Large Functions**
   - Break down functions > 100 lines
   - Extract helper functions
   - Consider creating packages for complex logic

3. **Comments**
   - Explain "why", not "what"
   - Document security considerations
   - Note missing features with descriptive comments (not just TODO)

### Git Commit Messages

Follow conventional commits:
- `feat:` - New feature
- `fix:` - Bug fix
- `refactor:` - Code refactoring
- `docs:` - Documentation changes
- `test:` - Test additions/changes
- `build:` - Build system changes

Example:
```
fix: remove duplicate ctx declaration in core server

Removed duplicate 'ctx := context.Background()' declaration on line 53
that was causing compilation error. Reusing the ctx variable declared
at line 30 instead.

Fixes CI error: 'no new variables on left side of :='
```

## Testing

### Integration Tests

Located in `workflow_test/`:
- `integration_test.go` - Basic workflow
- `changeset_workflow_test.go` - Changeset operations
- `conflict_resolution_test.go` - Conflict handling
- `checkout_test.go` - Checkout operations

Run with:
```bash
RUN_INTEGRATION_TESTS=1 go test -v ./workflow_test/...
```

### Test Requirements

- Tests require services to be running (or use in-memory storage)
- Integration tests are skipped without `RUN_INTEGRATION_TESTS=1`
- Playwright tests in `web/tests/` require web server

## Known Limitations

1. **Prototype Status**
   - In-memory storage by default
   - No distributed architecture (yet)
   - No authentication system

2. **Proto Files**
   - Generated `*.pb.go` files are NOT committed to git (auto-generated during build)
   - Proto regeneration requires `protoc` and plugins (installed via `make install`)
   - All builds automatically regenerate proto files from `.proto` sources

3. **Network Issues**
   - CI/local environments may have network restrictions
   - Tests may fail if external dependencies unreachable

## Quick Reference

### Environment Variables

```bash
# Service Ports
CORE_SERVICE_PORT=50051

# Storage
STORAGE_TYPE=memory  # or "postgres"
POSTGRES_DSN=postgres://user:pass@localhost:5432/gitslice?sslmode=disable

# Object Store (GCS)
GCS_BUCKET=gitslice-objects
GCS_ENDPOINT=
GCS_CREDENTIALS_FILE=
GCS_CREDENTIALS_JSON=
GCS_DISABLE_AUTH=false
```

### Useful Commands

```bash
# Start services
./ops/restart_all.sh

# Build all
go build -o core_server ./servers/core/
go build -o gs ./gs_cli/

# Run specific tests
go test -v ./internal/common/...

# Check formatting
gofmt -l .
```

## Last Updated

This document was created: 2026-01-20
Last updated: 2026-01-20

---

**Remember**: When in doubt, run the build and tests before pushing! 🧪
