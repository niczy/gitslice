# Claude Development Guidelines for Gitslice

This document contains important reminders and guidelines for Claude when working on the gitslice project.

## Pre-PR Checklist

### CRITICAL: Always Run Tests Before Creating PRs

Before committing and pushing code, **ALWAYS** run the following checks:

1. **Build Verification**
   ```bash
   go build -o /tmp/slice_service ./slice_service/
   go build -o /tmp/admin_service ./admin_service/
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
   go test -v ./internal/services/... -timeout 30s

   # Integration tests (requires RUN_INTEGRATION_TESTS=1)
   RUN_INTEGRATION_TESTS=1 go test -v ./workflow_test/... -timeout 60s
   ```

3. **Proto Regeneration** (if proto files changed)
   ```bash
   make proto
   ```
   - Only needed if `.proto` files were modified
   - Verify generated code compiles

4. **Lint Check** (if available)
   ```bash
   golangci-lint run
   ```

### Common Issues to Avoid

1. **Duplicate Variable Declarations**
   - ❌ `ctx := context.Background()` declared twice
   - ✅ Declare once, reuse the variable
   - Example: Lesson learned from PR #28 (duplicate ctx in slice_service/main.go)

2. **Missing Imports**
   - Always verify imports after refactoring
   - Check that all new packages are imported

3. **Hardcoded Values**
   - Use `internal/config` package for configuration
   - Environment variables should have defaults
   - **Configuration Consistency**: When services read configurable addresses, all references must use config
   - Example: If admin service uses `cfg.GetAdminServiceAddr()`, gateway must also use it (not hardcode `:50052`)

4. **Input Validation**
   - Use `internal/common/validation.go` functions
   - Validate all user inputs for security

5. **Proto Changes Without Implementation**
   - Adding RPC to proto requires implementing the handler
   - Proto regeneration alone is not enough
   - Server must implement all RPCs to avoid Unimplemented errors

## Project Architecture

### Key Packages

- **`internal/common/`** - Shared utilities
  - `init.go` - Root slice initialization
  - `validation.go` - Input validation (security)
  - `health.go` - Health check handlers

- **`internal/config/`** - Centralized configuration
  - All port numbers and addresses
  - Environment variable handling

- **`internal/services/`** - gRPC service implementations
  - `admin/` - Admin service
  - `slice/` - Slice service
  - `file/` - File service (read-only)

- **`internal/storage/`** - Storage layer
  - `memory.go` - In-memory storage (development)
  - `redis.go` - Redis-backed storage (production)

- **`gs_cli/`** - CLI client
  - Split into command files (not monolithic)
  - `commands_*.go` - Command implementations
  - `utils.go` - Shared utilities
  - `help.go` - Help text

### Service Ports

- Slice Service (gRPC): `50051` (configurable via `SLICE_SERVICE_PORT`)
- Admin Service (gRPC): `50052` (configurable via `ADMIN_SERVICE_PORT`)
- HTTP Gateway: `8080` (configurable via `GATEWAY_PORT`)

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
fix: remove duplicate ctx declaration in slice_service

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
   - `GetSlice` RPC defined but not implemented in admin service
   - Proto regeneration requires `protoc` and plugins

3. **Network Issues**
   - CI/local environments may have network restrictions
   - Tests may fail if external dependencies unreachable

## Quick Reference

### Environment Variables

```bash
# Service Ports
SLICE_SERVICE_PORT=50051
ADMIN_SERVICE_PORT=50052
GATEWAY_PORT=8080

# Storage
STORAGE_TYPE=memory  # or "redis"
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=
REDIS_DB=0

# Object Store (S3)
S3_ENDPOINT=
S3_ACCESS_KEY_ID=
S3_SECRET_ACCESS_KEY=
S3_BUCKET=gitslice-objects
S3_REGION=us-east-1
```

### Useful Commands

```bash
# Start services
./ops/restart_all.sh

# Build all
go build -o slice_service_server ./slice_service/
go build -o admin_service_server ./admin_service/
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
