# Local Development Guide

How to set up, build, and run Gitslice locally for development and testing.

## Prerequisites

- Go 1.24+
- PostgreSQL 16+ (for durable storage)
- `protoc` (Protocol Buffers compiler)

## Quick Start

```bash
# Install protoc plugins and build everything (generates proto code automatically)
make install
make build

# Or build individual binaries
go build -o core_server ./servers/core/
go build -o gs ./gs_cli/
```

Proto `.pb.go` files are **auto-generated** during build and are in `.gitignore`. Never edit or commit them.

## Running the Server

### In-Memory (Fastest, No Setup)

```bash
STORAGE_TYPE=memory ./core_server
```

Data is lost on restart. Good for quick iteration.

### PostgreSQL + Filesystem (Durable)

```bash
# Start PostgreSQL
pg_ctlcluster 16 main start

# Create database
psql -c "CREATE DATABASE gitslice;" postgres

# Run with postgres metadata + filesystem object store
STORAGE_TYPE=postgres \
POSTGRES_DSN="postgres://$(whoami)@/gitslice?sslmode=disable&host=/var/run/postgresql" \
OBJECT_STORE_TYPE=filesystem \
OBJECT_STORE_DIR="$PWD/.objectstore" \
./core_server
```

This stores metadata in PostgreSQL and file content in the local filesystem under `.objectstore/`.

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `STORAGE_TYPE` | `memory` | `memory` or `postgres` |
| `POSTGRES_DSN` | | PostgreSQL connection string |
| `OBJECT_STORE_TYPE` | | `filesystem` or `gcs` |
| `OBJECT_STORE_DIR` | | Path for filesystem object store |
| `GCS_BUCKET` | | GCS bucket name |
| `CORE_SERVICE_PORT` | `50051` | gRPC server port |
| `GATEWAY_PORT` | `8080` | HTTP gateway port |
| `SKIP_GIT_POPULATION` | | Set to `1` to skip auto-populating from local git checkout |

## CLI Usage Against Local Server

The CLI defaults to `api.agenttools.dev:443` (production). For local development, override the address:

```bash
# Point to local server
./gs --addr localhost:50051 --user myname root

# Import a git repo locally
./gs --addr localhost:50051 --user myname import git -repo https://github.com/org/repo.git
```

## Running Tests

```bash
# Unit tests (storage layer)
go test -v ./internal/storage/... -timeout 30s

# Service tests
(cd services/file && go test -v ./... -timeout 30s)
(cd services/slice && go test -v ./... -timeout 30s)

# Integration tests (requires running server)
RUN_INTEGRATION_TESTS=1 go test -v ./workflow_test/... -timeout 60s

# All tests via Makefile
make test
```

## Benchmarking Imports

A benchmark script exists at `benchmark/run_benchmark.sh` for testing import throughput:

```bash
# Import 10 repos with parallelism of 5
bash benchmark/run_benchmark.sh --num-repos 10 --parallelism 5 --addr localhost:50051
```

See `benchmark/RESULTS.md` for findings from benchmarking the top 100 GitHub repos.

### Import Performance Learnings

- **Clone is the bottleneck**: `git clone --bare` accounts for ~90% of import time. Commit processing is ~28ms/commit.
- **Parallelism helps but is bandwidth-bound**: 10 parallel workers give ~2.5x speedup (not 10x) due to network contention during clone.
- **Timeout**: The CLI import command has a 30-minute default timeout (`--timeout` flag). Repos over ~4GB (e.g. torvalds/linux at 5.9GB) may need a longer timeout.
- **PostgreSQL storage limits**: The snapshot-based persistence model serializes all metadata to a single row. At ~100 large repos, snapshots reach ~16MB (with content externalized to object store). The column uses `TEXT` type instead of `JSONB` to avoid PostgreSQL's 256MB JSONB limit.
- **Content externalization**: File content is written to the object store during `BulkWrite` and stripped from the PostgreSQL snapshot to keep it compact. The object store is the source of truth for file content.

## Project Structure

```
servers/core/       Core gRPC server binary (runs all services)
gs_cli/             CLI client (talks to server via gRPC)
services/admin/     Admin service (git import, management)
services/file/      File service (read-only file browsing)
services/slice/     Slice service (CRUD, checkout, changesets)
internal/storage/   Storage backends (memory, postgres+objectstore)
internal/config/    Centralized config (ports, env vars)
internal/gateway/   gRPC-Gateway HTTP proxy setup
proto/              Protobuf definitions (.proto files)
web/                Vite + React web UI
ops/                Operations (nginx, PM2, deploy scripts)
spec/               Design specifications
workflow_test/      End-to-end integration tests
benchmark/          Import benchmark scripts and results
```

## Common Issues

**`protoc` not found**: Install with `apt-get install -y protobuf-compiler` or `brew install protobuf`.

**Go module download fails**: Network-restricted environments may need `GOPROXY=direct`.

**PostgreSQL auth error**: Ensure your OS user has a matching PostgreSQL role: `psql -c "CREATE ROLE $(whoami) WITH LOGIN SUPERUSER;" postgres`.

**SSL key permissions**: If PostgreSQL fails to start, fix: `chmod 600 /etc/ssl/private/ssl-cert-snakeoil.key`.
