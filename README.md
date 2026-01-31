# Gitslice

**High-level summary:** Gitslice is a prototype slice-based version control system with gRPC services, a CLI, and a lightweight web landing page. The current implementation runs entirely in-memory while the design docs outline the long-term distributed architecture.

## Project Structure

```
.
├── admin_service/         # Admin service server implementation
│   └── main.go
├── gs_cli/                # CLI client implementation
│   └── main.go
├── internal/              # Storage and service implementations
│   ├── services/
│   └── storage/
├── ops/                   # Ops assets (NGINX config, etc.)
├── proto/                  # Protocol Buffer definitions and generated code
│   ├── slice/             # Slice service proto files
│   │   ├── slice_service.proto
│   │   ├── slice_service.pb.go
│   │   └── slice_service_grpc.pb.go
│   ├── file/              # File service proto files
│   │   ├── file_service.proto
│   │   ├── file_service.pb.go
│   │   ├── file_service_grpc.pb.go
│   │   └── file_service.pb.gw.go
│   └── admin/             # Admin service proto files
│       ├── admin_service.proto
│       ├── admin_service.pb.go
│       └── admin_service_grpc.pb.go
├── slice_service/         # Slice + File service server implementation
│   └── main.go
├── spec/                 # Design specifications
│   ├── PRODUCT_VISION.md
│   ├── DATA_MODEL.md
│   ├── ALGORITHMS.md
│   ├── CLI_DESIGN.md
│   ├── API_DESIGN.md
│   └── ARCHITECTURE.md
├── web/                  # Vite + React landing page
│   └── README.md
├── workflow_test/        # End-to-end integration tests
│   └── integration_test.go
└── .github/workflows/    # CI/CD workflows
    └── build.yml
```

## Building with Bazel

This project uses Bazel for hermetic, reproducible builds.

### Prerequisites

- [Bazelisk](https://bazel.build/install/bazelisk) (manages Bazel versions automatically)

```bash
# macOS
brew install bazelisk

# Linux
curl -Lo /usr/local/bin/bazel https://github.com/bazelbuild/bazelisk/releases/latest/download/bazelisk-linux-amd64
chmod +x /usr/local/bin/bazel
```

### Build

```bash
# Build all services
bazel build //...

# Build specific service
bazel build //slice_service:slice_service_server
bazel build //admin_service:admin_service_server
bazel build //gateway_service:gateway_service_server
bazel build //gs_cli:gs_cli

# Build web frontend
bazel build //web:build
```

### Run Tests

```bash
# Run all tests
bazel test //...

# Run only unit tests (fast)
bazel test //... --test_tag_filters=-integration

# Run integration tests (slow)
bazel test //workflow_test:integration_test --test_tag_filters=integration
```

### Generated Binaries

Binaries are output to `bazel-bin/`:
- `bazel-bin/slice_service/slice_service_server`
- `bazel-bin/admin_service/admin_service_server`
- `bazel-bin/gateway_service/gateway_service_server`
- `bazel-bin/gs_cli/gs_cli`

### Run

```bash
# Run slice service (SliceService on :50051)
bazel run //slice_service:slice_service_server

# Run admin service (listens on :50052)
bazel run //admin_service:admin_service_server

# Run gateway service (HTTP gRPC-Gateway on :8080)
bazel run //gateway_service:gateway_service_server

# Run CLI
bazel run //gs_cli:gs_cli -- --help
```

### Auto-generate BUILD Files

If you add new Go files, use Gazelle to update BUILD files:

```bash
bazel run //:gazelle
```

## Legacy Build (Make/Go modules)

The project can still be built using traditional Go tools and Make (deprecated):

### Prerequisites

- Go 1.24 or higher
- Protocol Buffers compiler (protoc)
- protoc-gen-go
- protoc-gen-go-grpc
- protoc-gen-grpc-gateway

### Install Dependencies

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
```

### Generate Proto Code

```bash
cd proto/slice
protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative slice_service.proto

cd ../file
protoc -I . -I .. --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative file_service.proto

cd ../admin
protoc -I . -I .. --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative admin_service.proto
```

### Build

```bash
# Build using Make
make build

# Or build manually
go build -o slice_service_server ./slice_service/
go build -o admin_service_server ./admin_service/
go build -o gateway_service_server ./gateway_service/
go build -o gs_cli ./gs_cli/
```

## Development

### Adding New Proto Definitions

1. Add or modify `.proto` files in `proto/slice/`, `proto/file/`, or `proto/admin/`
2. Update the `proto/BUILD.bazel` and respective subdirectory BUILD files if needed
3. Run `bazel build //proto/...` to regenerate Go code
4. Update the service implementations as needed
5. Run tests and ensure builds pass

### Running Tests

```bash
# Run all tests with Bazel (recommended)
bazel test //...

# Run integration tests
RUN_INTEGRATION_TESTS=1 bazel test //workflow_test:integration_test

# Legacy: Run tests with Make
make test
```

## CI/CD

GitHub Actions workflow is configured to use Bazel:
- Mount Bazel cache for faster builds
- Build all services with Bazel
- Run all tests with Bazel
- Test server startup and CLI commands

See `.github/workflows/build.yml` for details.

## SSL Certificates for NGINX

Generate a self-signed certificate and key for `agenttools.dev` and `api.agenttools.dev` with:

```bash
sudo mkdir -p /etc/ssl/private /etc/ssl/certs
sudo openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -keyout /etc/ssl/private/agenttools.dev.key \
  -out /etc/ssl/certs/agenttools.dev.crt \
  -subj "/CN=agenttools.dev" \
  -addext "subjectAltName=DNS:agenttools.dev,DNS:api.agenttools.dev"
```

## Documentation

See the `spec/` directory for detailed design specifications:
- [Product Vision](spec/PRODUCT_VISION.md)
- [Data Model](spec/DATA_MODEL.md)
- [Algorithms](spec/ALGORITHMS.md)
- [CLI Design](spec/CLI_DESIGN.md)
- [API Design](spec/API_DESIGN.md)
- [Architecture](spec/ARCHITECTURE.md)
- [Scalability Review](spec/SCALABILITY_REVIEW.md)

For the web landing page, see [web/README.md](web/README.md).

## Migration Plan

See [plans/BAZEL_MIGRATION_PLAN.md](plans/BAZEL_MIGRATION_PLAN.md) for details on the Bazel migration.

## License

[Add your license here]
