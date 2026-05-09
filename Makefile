.PHONY: install proto build build-core build-cli start-servers start-servers-memory start-servers-postgres start-servers-postgres-file start-servers-postgres-r2 restart-servers restart-servers-memory restart-servers-postgres restart-servers-postgres-file restart-servers-postgres-r2 stop-servers dev-status test test-benchmark benchmark-postgres-matrix clean install_gs web-install web-build web-test-e2e setup-googleapis

GOPATH := $(shell go env GOPATH)
GOBIN := $(GOPATH)/bin
ROOT_TEST_PACKAGES = $$(go list ./... | grep -v '/benchmark_suite$$')
CLI_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
CLI_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
CLI_BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
CLI_LDFLAGS := -X github.com/niczy/gitslice/gs_cli.version=$(CLI_VERSION) -X github.com/niczy/gitslice/gs_cli.commit=$(CLI_COMMIT) -X github.com/niczy/gitslice/gs_cli.buildDate=$(CLI_BUILD_DATE)
GOOGLEAPIS_DIR := third_party/googleapis
# Pin to specific googleapis commit for reproducible builds
# This commit is from 2024-05-13, matching our genproto dependency date
GOOGLEAPIS_COMMIT := 0d38cae77aba1a9da2b4d5f27c3eabf7e48cf0e3
CORE_SERVICE_PORT ?= 50051
WEB_PORT ?= 5173
VITE_FILE_API_PROXY_TARGET ?= http://localhost:$(CORE_SERVICE_PORT)

install:
	go mod download
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0
	go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest

setup-googleapis:
	@if [ ! -d "$(GOOGLEAPIS_DIR)" ]; then \
		echo "Downloading googleapis proto files (commit: $(GOOGLEAPIS_COMMIT))..."; \
		mkdir -p third_party; \
		git clone --depth 1 --no-checkout https://github.com/googleapis/googleapis.git $(GOOGLEAPIS_DIR); \
		git -C $(GOOGLEAPIS_DIR) sparse-checkout init --cone; \
		git -C $(GOOGLEAPIS_DIR) sparse-checkout set google/api; \
		git -C $(GOOGLEAPIS_DIR) checkout master; \
		echo "googleapis downloaded successfully"; \
	fi

proto: setup-googleapis
	@if ! command -v protoc >/dev/null 2>&1; then \
		echo "Error: protoc not found. Please install protobuf compiler."; \
		echo "  Ubuntu/Debian: apt-get install -y protobuf-compiler"; \
		echo "  macOS: brew install protobuf"; \
		exit 1; \
	fi
	@if ! PATH=$(GOBIN):$(PATH) command -v protoc-gen-go >/dev/null 2>&1; then \
		echo "Error: protoc-gen-go not found. Run 'make install' first."; \
		exit 1; \
	fi
	@if ! PATH=$(GOBIN):$(PATH) command -v protoc-gen-grpc-gateway >/dev/null 2>&1; then \
		echo "Error: protoc-gen-grpc-gateway not found. Run 'make install' first."; \
		exit 1; \
	fi
	PATH=$(GOBIN):$(PATH) sh -c 'cd proto/common && protoc -I . -I .. -I ../../$(GOOGLEAPIS_DIR) --go_out=. --go_opt=paths=source_relative visibility.proto'
	PATH=$(GOBIN):$(PATH) sh -c 'cd proto/slice && protoc -I . -I .. -I ../../$(GOOGLEAPIS_DIR) --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative slice_service.proto'
	PATH=$(GOBIN):$(PATH) sh -c 'cd proto/admin && protoc -I . -I .. -I ../../$(GOOGLEAPIS_DIR) --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative admin_service.proto'
	PATH=$(GOBIN):$(PATH) sh -c 'cd proto/account && protoc -I . -I .. -I ../../$(GOOGLEAPIS_DIR) --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative account_service.proto'
	PATH=$(GOBIN):$(PATH) sh -c 'cd proto/file && protoc -I . -I .. -I ../../$(GOOGLEAPIS_DIR) --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative file_service.proto'
	PATH=$(GOBIN):$(PATH) sh -c 'cd proto/filesystem && protoc -I . -I .. -I ../../$(GOOGLEAPIS_DIR) --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative filesystem_service.proto'
	PATH=$(GOBIN):$(PATH) sh -c 'cd proto/agent && protoc -I . -I .. -I ../../$(GOOGLEAPIS_DIR) --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative agent_service.proto'
	PATH=$(GOBIN):$(PATH) sh -c 'cd proto/ci && protoc -I . -I .. -I ../../$(GOOGLEAPIS_DIR) --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative ci_service.proto'

build: proto build-core build-cli

build-core: proto
	go build -o core_server ./servers/core

build-cli: proto
	go build -ldflags "$(CLI_LDFLAGS)" -o bin/gs ./gs/

start-servers:
	CORE_SERVICE_PORT=$(CORE_SERVICE_PORT) WEB_PORT=$(WEB_PORT) VITE_FILE_API_PROXY_TARGET=$(VITE_FILE_API_PROXY_TARGET) dev/start-servers.sh --storage postgres --object-store filesystem

start-servers-memory:
	CORE_SERVICE_PORT=$(CORE_SERVICE_PORT) WEB_PORT=$(WEB_PORT) VITE_FILE_API_PROXY_TARGET=$(VITE_FILE_API_PROXY_TARGET) dev/start-servers.sh --storage memory

start-servers-postgres start-servers-postgres-file:
	CORE_SERVICE_PORT=$(CORE_SERVICE_PORT) WEB_PORT=$(WEB_PORT) VITE_FILE_API_PROXY_TARGET=$(VITE_FILE_API_PROXY_TARGET) dev/start-servers.sh --storage postgres --object-store filesystem

start-servers-postgres-r2:
	CORE_SERVICE_PORT=$(CORE_SERVICE_PORT) WEB_PORT=$(WEB_PORT) VITE_FILE_API_PROXY_TARGET=$(VITE_FILE_API_PROXY_TARGET) dev/start-servers.sh --storage postgres --object-store r2

restart-servers:
	CORE_SERVICE_PORT=$(CORE_SERVICE_PORT) WEB_PORT=$(WEB_PORT) VITE_FILE_API_PROXY_TARGET=$(VITE_FILE_API_PROXY_TARGET) dev/restart-servers.sh --storage postgres --object-store filesystem

restart-servers-memory:
	CORE_SERVICE_PORT=$(CORE_SERVICE_PORT) WEB_PORT=$(WEB_PORT) VITE_FILE_API_PROXY_TARGET=$(VITE_FILE_API_PROXY_TARGET) dev/restart-servers.sh --storage memory

restart-servers-postgres restart-servers-postgres-file:
	CORE_SERVICE_PORT=$(CORE_SERVICE_PORT) WEB_PORT=$(WEB_PORT) VITE_FILE_API_PROXY_TARGET=$(VITE_FILE_API_PROXY_TARGET) dev/restart-servers.sh --storage postgres --object-store filesystem

restart-servers-postgres-r2:
	CORE_SERVICE_PORT=$(CORE_SERVICE_PORT) WEB_PORT=$(WEB_PORT) VITE_FILE_API_PROXY_TARGET=$(VITE_FILE_API_PROXY_TARGET) dev/restart-servers.sh --storage postgres --object-store r2

stop-servers:
	CORE_SERVICE_PORT=$(CORE_SERVICE_PORT) WEB_PORT=$(WEB_PORT) dev/stop-servers.sh

dev-status:
	CORE_SERVICE_PORT=$(CORE_SERVICE_PORT) WEB_PORT=$(WEB_PORT) dev/dev-servers.sh status

test: install proto
	go test $(ROOT_TEST_PACKAGES)
	cd services/admin && go test ./...
	cd services/file && go test ./...
	cd services/slice && go test ./...
	cd servers/core && go test ./...

test-benchmark: install proto
	go test ./benchmark_suite

benchmark-postgres-matrix:
	./benchmark_suite/run_postgres_matrix.sh

clean:
	rm -f core_server slice_service_server admin_service_server gateway_service_server bin/gs
	find proto -name "*.pb.go" -delete
	find proto -name "*.pb.gw.go" -delete

install_gs: build-cli
	cp bin/gs $(GOPATH)/bin/gs

web-install:
	cd web && npm ci

web-build: web-install
	cd web && npm run build

web-test-e2e: build web-install
	cd web && npx playwright install --with-deps
	cd web && npm run test:e2e
