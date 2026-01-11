# Project Status & Production Readiness TODOs

## Current Status (as implemented)

- **Core services:** gRPC-based slice and admin services are implemented and start with in-memory storage by default (`slice_service/main.go`, `admin_service/main.go`).
- **Storage layer:** Storage interface supports slices, changesets, conflicts, directory entries, and global state with in-memory and Redis-backed implementations.
- **CLI:** The `gs_cli` tool supports workflows for slices, changesets, status, init, conflict handling, and checkout/log/root/fork operations.
- **Protobuf APIs:** Admin and slice service APIs are defined under `proto/` with generated Go stubs committed.
- **Testing:** Unit tests exist for storage and integration tests exercise the CLI + gRPC services with Redis via `RUN_INTEGRATION_TESTS=1`.
- **Documentation:** Design specs live in `spec/` and the README documents build/run steps plus proto generation.

## Production Readiness TODOs

### Reliability & Persistence
- **Adopt durable storage in production defaults.** Wire the services to use Redis-backed storage + object store rather than in-memory storage.
- **Backups and restores.** Add backup/restore workflows for Redis/object store data.
- **Data migrations.** Introduce versioned schemas and migration tooling for durable state.

### Security
- **Authentication & authorization.** Add identity-aware requests and server-side authz checks (CLI currently uses placeholder owners/created-by values).
- **Transport security.** Enable TLS for gRPC and switch the CLI from insecure credentials.
- **Secrets management.** Centralize secrets/config (e.g., Redis credentials, TLS certs) and avoid hard-coded values.

### Operational Readiness
- **Configuration management.** Replace hard-coded ports and storage selection with config files/env vars.
- **Observability.** Add structured logging, request tracing, and metrics (e.g., Prometheus) for services.
- **Health checks.** Provide health/readiness endpoints for service monitoring.
- **Resource limits & timeouts.** Enforce service timeouts and request limits for large slices/files.

### Product Completeness
- **User and org models.** Implement users, teams, and ownership metadata rather than placeholder values.
- **Conflict resolution workflows.** Expand conflict data beyond file-level ownership and add explicit resolution paths.
- **CLI ergonomics.** Improve UX (help, error messaging, and output formatting) and add config support.

### Release Engineering
- **Deployment artifacts.** Add Docker images and deployment manifests (systemd/Kubernetes) for services.
- **CI/CD hardening.** Expand CI to run integration tests and verify proto generation in pipelines.
- **Performance testing.** Create benchmarks for large repos and high-concurrency usage.
