.PHONY: build build-slice build-admin build-gateway build-cli start-servers stop-servers test clean install_gs web-install web-build web-test-e2e

GOPATH := $(shell go env GOPATH)
GATEWAY_PORT ?= 8080
SLICE_SERVICE_PORT ?= 50051
ADMIN_SERVICE_PORT ?= 50052
WEB_PORTS ?= 5173 4173 5174
STOP_SERVER_PORTS := $(SLICE_SERVICE_PORT) $(ADMIN_SERVICE_PORT) $(GATEWAY_PORT) $(WEB_PORTS)
VITE_FILE_API_PROXY_TARGET ?= http://localhost:$(GATEWAY_PORT)

build:
	bazel build //...

build-slice:
	bazel build //slice_service:slice_service_server

build-admin:
	bazel build //admin_service:admin_service_server

build-gateway:
	bazel build //gateway_service:gateway_service_server

build-cli:
	bazel build //gs_cli:gs_cli

start-servers: build
	@$(MAKE) stop-servers
	GATEWAY_PORT=$(GATEWAY_PORT) ./bazel-bin/slice_service/slice_service_server_/slice_service_server &
	./bazel-bin/admin_service/admin_service_server_/admin_service_server &
	GATEWAY_PORT=$(GATEWAY_PORT) ./bazel-bin/gateway_service/gateway_service_server_/gateway_service_server &
	cd web && VITE_FILE_API_PROXY_TARGET=$(VITE_FILE_API_PROXY_TARGET) npm run dev &
	@echo "Services started (slice, admin, gateway on :$(GATEWAY_PORT), web). Press Ctrl+C to stop."

stop-servers:
	@tool=""; \
	if command -v lsof >/dev/null 2>&1; then \
		tool="lsof"; \
	elif command -v ss >/dev/null 2>&1; then \
		tool="ss"; \
	elif command -v netstat >/dev/null 2>&1; then \
		tool="netstat"; \
	elif command -v fuser >/dev/null 2>&1; then \
		tool="fuser"; \
	else \
		echo "Warning: no supported port-inspection tool found (lsof/ss/netstat/fuser). Skipping stop-servers."; \
		exit 0; \
	fi; \
	for port in $(STOP_SERVER_PORTS); do \
		pids=""; \
		case "$$tool" in \
			lsof) pids=$$(lsof -tiTCP:$$port -sTCP:LISTEN 2>/dev/null || true) ;; \
			ss) pids=$$(ss -ltnp "sport = :$$port" 2>/dev/null | awk -F'pid=' 'NF>1 {for (i=2;i<=NF;i++){split($$i,a,\","); if (a[1] ~ /^[0-9]+$$/) print a[1]}}' | sort -u | tr '\n' ' ') ;; \
			netstat) pids=$$(netstat -ltnp 2>/dev/null | awk -v p=":$$port" '$$4 ~ p {split($$7,a,"/"); if (a[1] ~ /^[0-9]+$$/) print a[1]}' | sort -u | tr '\n' ' ') ;; \
			fuser) pids=$$(fuser -n tcp $$port 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+$$' | sort -u | tr '\n' ' ') ;; \
		esac; \
		if [ -n "$$pids" ]; then \
			echo "Stopping processes on port $$port: $$pids"; \
			kill $$pids 2>/dev/null || true; \
		fi; \
	done

test:
	bazel test //...

clean:
	bazel clean

install_gs: build-cli
	cp bazel-bin/gs_cli/gs_cli_/gs_cli $(GOPATH)/bin/gs

web-install:
	cd web && npm ci

web-build: web-install
	cd web && npm run build

web-test-e2e: build web-install
	cd web && npx playwright install --with-deps
	cd web && npm run test:e2e
