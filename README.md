# Gitslice

**High-level summary:** Gitslice is a prototype slice-based version control system with gRPC services, a CLI, and a lightweight web landing page. The current implementation runs entirely in-memory while the design docs outline the long-term distributed architecture.

## Project Structure

```
.
├── gs_cli/                # CLI client implementation
│   └── main.go
├── internal/              # Storage and shared implementations
│   ├── gateway/
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
├── services/              # RPC service implementations
│   ├── admin/
│   ├── file/
│   └── slice/
├── servers/               # Binary servers
│   └── core/              # Core server (gRPC + gateway)
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

## Getting Started

### Prerequisites

- Go 1.21 or higher
- Protocol Buffers compiler (protoc)
- protoc-gen-go
- protoc-gen-go-grpc
- protoc-gen-grpc-gateway

### Go Workspace

This repository uses a Go workspace (`go.work`) to wire together the service and server modules (each service/server has its own `go.mod`). Run Go commands from the repo root to pick up the workspace configuration.

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
# Build core server (gRPC + gateway)
go build -o core_server ./servers/core/

# Build CLI
go build -o gs_cli/gs_cli ./gs_cli/
```

### Run

```bash
# Run core server (gRPC on :50051, gateway on :8080)
CORE_SERVICE_PORT=50051 GATEWAY_PORT=8080 ./core_server

# Run CLI (override addresses if needed)
./gs_cli --help
```

## Development

### Adding New Proto Definitions

1. Add or modify `.proto` files in `proto/slice/` or `proto/admin/`
2. Regenerate the golang code using protoc
3. Update the service implementations as needed
4. Run tests and ensure builds pass

### Running Tests

```bash
# Run all tests (installs dependencies first)
make test

# Run integration tests
RUN_INTEGRATION_TESTS=1 make test
```

## CI/CD

GitHub Actions workflow is configured to:
- Install Go and dependencies
- Generate proto code
- Build all services
- Test server startup
- Test CLI help command

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

## License

[Add your license here]
