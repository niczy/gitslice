.PHONY: install proto build build-slice build-admin build-cli start-servers test clean install_gs web-install web-build web-test-e2e setup-googleapis

GOPATH := $(shell go env GOPATH)
GOBIN := $(GOPATH)/bin
GOOGLEAPIS_DIR := third_party/googleapis
# Pin to specific googleapis commit for reproducible builds
# This commit is from 2024-05-13, matching our genproto dependency date
GOOGLEAPIS_COMMIT := 0d38cae77aba1a9da2b4d5f27c3eabf7e48cf0e3

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
	PATH=$(GOBIN):$(PATH) sh -c 'cd proto/slice && protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative slice_service.proto'
	PATH=$(GOBIN):$(PATH) sh -c 'cd proto/admin && protoc -I . -I .. -I ../../$(GOOGLEAPIS_DIR) --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative admin_service.proto'
	PATH=$(GOBIN):$(PATH) sh -c 'cd proto/file && protoc -I . -I .. -I ../../$(GOOGLEAPIS_DIR) --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative file_service.proto'

build: proto
	go build -o slice_service_server ./slice_service/
	go build -o admin_service_server ./admin_service/
	go build -o gs_cli/gs_cli ./gs_cli/

build-slice: proto
	go build -o slice_service_server ./slice_service/

build-admin: proto
	go build -o admin_service_server ./admin_service/

build-cli: proto
	go build -o gs_cli/gs_cli ./gs_cli/

start-servers: build
	./slice_service_server &
	./admin_service_server &
	@echo "Services started. Press Ctrl+C to stop."

test: install proto
	go test ./...

clean:
	rm -f slice_service_server admin_service_server gs_cli/gs_cli
	find proto -name "*.pb.go" -delete
	find proto -name "*.pb.gw.go" -delete

install_gs: build-cli
	cp gs_cli/gs_cli $(GOPATH)/bin/gs

web-install:
	cd web && npm ci

web-build: web-install
	cd web && npm run build

web-test-e2e: web-install
	cd web && npm run build
	cd web && npx playwright install --with-deps
	cd web && npm run test:e2e
