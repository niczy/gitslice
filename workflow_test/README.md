# Workflow Tests

This directory contains comprehensive integration tests for the gitslice CLI based on the CLI design specification in `spec/CLI_DESIGN.md`.

## Test Structure

### Test Files

| File | Description | Test Count |
|------|-------------|------------|
| `slice_management_test.go` | Tests for slice creation, listing, and initialization | 12 |
| `changeset_workflow_test.go` | Tests for changeset CRUD operations (create, review, merge, rebase, list, abandon) | 24 |
| `conflict_resolution_test.go` | Tests for conflict detection and resolution | 19 |
| `commit_history_test.go` | Tests for log, show, and diff commands | 32 |
| `global_state_test.go` | Tests for global state and batch merge operations | 15 |
| `working_directory_test.go` | Tests for working directory management and status | 17 |
| `cache_test.go` | Tests for cache operations and performance | 27 |
| `advanced_features_test.go` | Tests for stashing, git integration, hooks, config | 48 |
| `integration_test.go` | Integration tests for service setup and teardown | 12 |

**Total: 206 tests**

## Running Tests

### Skip All Tests (Default)

By default, all tests are skipped since the implementation is not ready:

```bash
cd workflow_test
go test -v
```

### Run Integration Tests

To run integration tests with services:

```bash
# Build CLI first
cd ../
go build -o gs_cli/gs_cli ./gs_cli/

# Run integration tests
cd workflow_test
RUN_INTEGRATION_TESTS=1 go test -v
```

### Run Specific Test File

```bash
# Run only slice management tests
go test -v -run TestSlice

# Run only changeset tests
go test -v -run TestChangeset

# Run only conflict resolution tests
go test -v -run TestConflict
```

### Run Specific Test

```bash
# Run a single test
go test -v -run TestSliceCreate
```

### Run Tests with Coverage

```bash
go test -v -coverprofile=coverage.out
go tool cover -html=coverage.out
```

## Test Organization

### Categories

1. **Slice Management** (`slice_management_test.go`)
   - Create, list, and manage slices
   - Initialize working directories
   - Query slice information

2. **Changeset Workflow** (`changeset_workflow_test.go`)
   - Create changesets from modifications
   - Review changesets before merge
   - Merge changesets with conflict detection
   - Rebase changesets on slice advancement
   - List and abandon changesets

3. **Conflict Resolution** (`conflict_resolution_test.go`)
   - List conflicts by severity
   - Resolve conflicts (interactive, auto, manual)
   - Show conflict details
   - Handle different conflict types (semantic, formatting, structural)

4. **Commit History** (`commit_history_test.go`)
   - View slice commit history
   - Show detailed commit information
   - Compare commits with diff
   - Display statistics and summaries

5. **Global State** (`global_state_test.go`)
   - View global repository state
   - Trigger batch merge operations
   - Monitor global commit history
   - Track pending vs merged slices

6. **Working Directory** (`working_directory_test.go`)
   - Display directory status
   - Handle slice bindings
   - Show uncommitted changes
   - Verify directory structure

7. **Cache** (`cache_test.go`)
   - Cache statistics and management
   - Manifest and object caching
   - Performance optimization tests
   - TTL and LRU eviction

8. **Advanced Features** (`advanced_features_test.go`)
   - Stashing work
   - Git integration (import/export/sync)
   - Hooks (pre/post operations)
   - Configuration management
   - Help and documentation

9. **Integration** (`integration_test.go`)
   - Service startup/shutdown
   - Health checks
   - CLI build verification
   - Environment validation
   - Error handling
   - Concurrent access
   - Network resilience

## Test Naming Convention

All tests follow the pattern:

```go
Test<Feature><Action>(t *testing.T)
```

Examples:
- `TestSliceCreate` - Creating a slice
- `TestChangesetMerge` - Merging a changeset
- `TestConflictResolveTheirs` - Resolving conflict with incoming changes

## Test Documentation

Each test includes:
- Function name describing the test
- Comment with CLI command equivalent
- Comment with expected behavior (where applicable)
- `t.Skip("Implementation not ready yet")` for stub tests

Example:

```go
// TestChangesetCreate tests creating change list from modified files
// Command: gs changeset create --message "Add region-based tax calculation"
func TestChangesetCreate(t *testing.T) {
    t.Skip("Implementation not ready yet")
}
```

## Implementation Plan

When implementing these tests:

1. **Phase 1**: Implement basic slice operations
   - `slice_management_test.go`
   - `working_directory_test.go`

2. **Phase 2**: Implement changeset workflow
   - `changeset_workflow_test.go`

3. **Phase 3**: Implement conflict detection and resolution
   - `conflict_resolution_test.go`

4. **Phase 4**: Implement history and diff operations
   - `commit_history_test.go`

5. **Phase 5**: Implement global state management
   - `global_state_test.go`

6. **Phase 6**: Implement caching layer
   - `cache_test.go`

7. **Phase 7**: Implement advanced features
   - `advanced_features_test.go`

8. **Phase 8**: Full integration testing
   - `integration_test.go`

## Service Setup

Tests require the following services to be running:

- **Slice Service**: `localhost:50051`
- **Admin Service**: `localhost:50052`
- **CLI Binary**: `../gs_cli/gs_cli`

Services are automatically started by `TestMain` when `RUN_INTEGRATION_TESTS=1` is set.

## CI/CD Integration

GitHub Actions workflow (`.github/workflows/build.yml`) should be updated to:

1. Build CLI binary
2. Optionally run integration tests (disabled by default)
3. Report test coverage

Example workflow addition:

```yaml
- name: Run integration tests
  run: |
    cd workflow_test
    go test -v -run TestCLIBuild
```

## Adding New Tests

To add a new test:

1. Identify the appropriate test file
2. Add test function with descriptive name
3. Include command comment from CLI_DESIGN.md
4. Add expected behavior comment
5. Add `t.Skip()` if implementation not ready

Example:

```go
// TestNewFeature tests the new feature
// Command: gs feature new-thing
// Expected: Feature executes successfully
func TestNewFeature(t *testing.T) {
    if !implementationReady {
        t.Skip("Implementation not ready yet")
    }
    // Test implementation here
}
```

## Troubleshooting

### Tests Not Running

If tests don't run, check:
1. CLI binary is built: `go build -o gs_cli/gs_cli ./gs_cli/`
2. Environment variable is set: `export RUN_INTEGRATION_TESTS=1`
3. Services are accessible: `nc -zv localhost 50051`

### Services Not Starting

If services fail to start:
1. Check ports 50051 and 50052 are not in use
2. Check Go dependencies are installed: `go mod download`
3. Check proto files are generated

### Test Timeouts

If tests timeout:
1. Increase timeout in `runCLI()` function
2. Check services are responding
3. Check for deadlocks or infinite loops

## Coverage Goals

Target coverage metrics:
- Slice Management: 90%+
- Changeset Workflow: 90%+
- Conflict Resolution: 85%+ (complex logic)
- Commit History: 90%+
- Global State: 90%+
- Working Directory: 90%+
- Cache: 85%+ (performance optimization)
- Advanced Features: 80%+ (many optional features)
- Integration: 95%+ (critical paths)

**Overall Goal: 88%+ coverage**

## References

- [CLI Design Specification](../spec/CLI_DESIGN.md)
- [API Design](../spec/API_DESIGN.md)
- [Data Model](../spec/DATA_MODEL.md)
- [Algorithms](../spec/ALGORITHMS.md)
