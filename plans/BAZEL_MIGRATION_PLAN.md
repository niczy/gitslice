# Bazel Migration Plan for Gitslice

This document outlines a phased approach to migrate the Gitslice repository from Make/Go modules to Bazel.

## Executive Summary

**Current State:**
- Go 1.24 with go.mod
- Make-based build system
- Protocol Buffers with protoc + plugins
- Vite/React web frontend with npm
- GitHub Actions CI/CD

**Target State:**
- Bazel with bzlmod for dependency management
- Hermetic builds with pinned toolchain versions
- Unified build for Go, Protos, and Web
- Remote caching and execution ready

---

## Phase 1: Workspace Setup & Dependencies

### 1.1 Install Bazel

Add `.bazelversion` file:
```
7.3.0
```

Developers install Bazelisk (manages Bazel versions automatically):
```bash
# macOS
brew install bazelisk

# Linux
curl -Lo /usr/local/bin/bazel https://github.com/bazelbuild/bazelisk/releases/latest/download/bazelisk-linux-amd64
chmod +x /usr/local/bin/bazel
```

### 1.2 Create MODULE.bazel

Create `MODULE.bazel` at repository root:

```starlark
"""Bazel module definition for Gitslice."""

module(
    name = "gitslice",
    version = "0.1.0",
)

# Go toolchain
bazel_dep(name = "rules_go", version = "0.50.1")
bazel_dep(name = "gazelle", version = "0.38.0")

# Protocol Buffers
bazel_dep(name = "rules_proto", version = "6.0.2")
bazel_dep(name = "protobuf", version = "28.2")

# gRPC
bazel_dep(name = "grpc", version = "1.66.0")
bazel_dep(name = "rules_proto_grpc", version = "5.0.0")
bazel_dep(name = "rules_proto_grpc_go", version = "1.5.0")
bazel_dep(name = "rules_proto_grpc_gateway", version = "1.5.0")

# Node.js for web frontend
bazel_dep(name = "aspect_rules_js", version = "2.0.0")
bazel_dep(name = "aspect_rules_ts", version = "3.0.0")
bazel_dep(name = "rules_nodejs", version = "6.2.0")

# Packaging and containerization (future)
bazel_dep(name = "rules_pkg", version = "1.0.1")
bazel_dep(name = "rules_oci", version = "1.8.0")

# Go toolchain configuration
go_sdk = use_extension("@rules_go//go:extensions.bzl", "go_sdk")
go_sdk.download(version = "1.24.0")

go_deps = use_extension("@gazelle//:extensions.bzl", "go_deps")
go_deps.from_file(go_mod = "//:go.mod")

# Node.js toolchain
node = use_extension("@rules_nodejs//nodejs:extensions.bzl", "node")
node.toolchain(node_version = "20.11.0")

npm = use_extension("@aspect_rules_js//npm:extensions.bzl", "npm")
npm.npm_translate_lock(
    name = "npm",
    pnpm_lock = "//web:pnpm-lock.yaml",
    verify_node_modules_ignored = "//:.bazelignore",
)
use_repo(npm, "npm")
```

### 1.3 Create BUILD.bazel at Root

Create `BUILD.bazel`:

```starlark
load("@gazelle//:def.bzl", "gazelle")

# Gazelle target for generating BUILD files
# Usage: bazel run //:gazelle
# Update deps: bazel run //:gazelle -- update-repos -from_file=go.mod -to_macro=deps.bzl%go_dependencies

gazelle(name = "gazelle")
```

### 1.4 Create .bazelrc

Create `.bazelrc`:

```
# Build settings
build --jobs=auto
build --enable_bzlmod

# Go settings
build --@rules_go//go/config:static

# Test settings
test --test_output=errors
test --test_verbose_timeout_warnings

# Remote caching (enable when ready)
# build --remote_cache=grpc://cache.example.com:9092

# Disk cache for local development
build --disk_cache=~/.cache/bazel-disk-cache

# UI settings
common --color=yes
common --curses=yes

# Performance
build --experimental_remote_merkle_tree_cache
build --experimental_remote_merkle_tree_cache_size=1000

# Proto compilation
build --proto_compiler=@com_google_protobuf//:protoc

# Platform-specific settings (macOS)
build:macos --copt=-Wno-unused-command-line-argument
build:macos --copt=-Wno-nullability-completeness
```

### 1.5 Create .bazelignore

Create `.bazelignore`:

```
.git
node_modules
web/node_modules
bazel-*
```

---

## Phase 2: Proto Build Rules

### 2.1 Consolidate Proto Files

Current structure:
```
proto/
├── slice/
├── file/
├── admin/
└── google/  (copied from googleapis)
```

Reorganize to use Bazel's googleapis:

Create `proto/BUILD.bazel`:

```starlark
load("@rules_proto//proto:defs.bzl", "proto_library")

package(default_visibility = ["//visibility:public"])

# Slice service proto
proto_library(
    name = "slice_service_proto",
    srcs = ["slice/slice_service.proto"],
    deps = [
        "@com_google_protobuf//:timestamp_proto",
    ],
)

# File service proto  
proto_library(
    name = "file_service_proto",
    srcs = ["file/file_service.proto"],
    deps = [
        "@com_google_googleapis//google/api:annotations_proto",
        "@com_google_googleapis//google/api:field_behavior_proto",
        "@com_google_protobuf//:timestamp_proto",
    ],
)

# Admin service proto
proto_library(
    name = "admin_service_proto",
    srcs = ["admin/admin_service.proto"],
    deps = [
        "@com_google_googleapis//google/api:annotations_proto",
        "@com_google_protobuf//:timestamp_proto",
    ],
)
```

### 2.2 Go Proto Generation

Create Go-specific proto targets. Create `proto/slice/BUILD.bazel`:

```starlark
load("@rules_go//go:def.bzl", "go_library")
load("@rules_proto_grpc_go//:defs.bzl", "go_proto_library")

package(default_visibility = ["//visibility:public"])

go_proto_library(
    name = "slice_go_proto",
    importpath = "github.com/niczy/gitslice/proto/slice",
    protos = ["//proto:slice_service_proto"],
    # Generates both pb.go and _grpc.pb.go
    protoc_plugins = {
        "@com_github_golang_protobuf//protoc-gen-go": ["go"],
        "@org_golang_google_grpc//cmd/protoc-gen-go-grpc": ["go-grpc"],
    },
)

# Alias for easier imports
alias(
    name = "slice",
    actual = ":slice_go_proto",
)
```

Similar files for `proto/file/BUILD.bazel` and `proto/admin/BUILD.bazel` with grpc-gateway generation.

### 2.3 Handle google/api Protos

Add to `MODULE.bazel`:

```starlark
# Google APIs proto definitions
bazel_dep(name = "googleapis", version = "0.0.0")
archive_override(
    module_name = "googleapis",
    urls = ["https://github.com/googleapis/googleapis/archive/0d38cae77aba1a9da2b4d5f27c3eabf7e48cf0e3.tar.gz"],
    strip_prefix = "googleapis-0d38cae77aba1a9da2b4d5f27c3eabf7e48cf0e3",
)
```

Remove `third_party/googleapis` once migration is complete.

---

## Phase 3: Go Library & Binary Targets

### 3.1 Generate BUILD files with Gazelle

Run Gazelle to auto-generate BUILD files:

```bash
# Generate/update BUILD files
bazel run //:gazelle

# Update external dependencies from go.mod
bazel run //:gazelle -- update-repos -from_file=go.mod -to_macro=deps.bzl%go_dependencies
```

### 3.2 Expected Structure After Gazelle

```
internal/
├── common/BUILD.bazel
├── config/BUILD.bazel
├── models/BUILD.bazel
├── services/
│   ├── admin/BUILD.bazel
│   ├── file/BUILD.bazel
│   └── slice/BUILD.bazel
└── storage/BUILD.bazel
```

Example `internal/storage/BUILD.bazel` (auto-generated):

```starlark
load("@rules_go//go:def.bzl", "go_library", "go_test")

go_library(
    name = "storage",
    srcs = [
        "memory.go",
        "objectstore.go",
        "redis.go",
        "storage.go",
    ],
    importpath = "github.com/niczy/gitslice/internal/storage",
    visibility = ["//:__subpackages__"],
    deps = [
        "//internal/models",
        "@com_github_alicebob_miniredis_v2//:miniredis",
        "@com_github_aws_aws_sdk_go_v2_service_s3//:s3",
        "@com_github_redis_go_redis_v9//:go-redis",
    ],
)

go_test(
    name = "storage_test",
    srcs = ["storage_test.go"],
    embed = [":storage"],
    deps = [
        "//internal/models",
        "@com_github_alicebob_miniredis_v2//:miniredis",
    ],
)
```

### 3.3 Binary Targets

Create `slice_service/BUILD.bazel`:

```starlark
load("@rules_go//go:def.bzl", "go_binary", "go_library")

go_library(
    name = "slice_service_lib",
    srcs = ["main.go"],
    importpath = "github.com/niczy/gitslice/slice_service",
    visibility = ["//visibility:private"],
    deps = [
        "//internal/common",
        "//internal/config",
        "//internal/services/file",
        "//internal/services/slice",
        "//internal/storage",
        "//proto/file",
    ],
)

go_binary(
    name = "slice_service_server",
    embed = [":slice_service_lib"],
    visibility = ["//visibility:public"],
    # Static linking for deployment
    static = "on",
    # Strip debug symbols for smaller binary
    gc_linkopts = ["-s", "-w"],
)
```

Similar files for:
- `admin_service/BUILD.bazel`
- `gateway_service/BUILD.bazel`
- `gs_cli/BUILD.bazel`

### 3.4 Convenience Build Targets

Create root `BUILD.bazel` with convenience targets:

```starlark
# Build all services
filegroup(
    name = "all_services",
    srcs = [
        "//admin_service:admin_service_server",
        "//gateway_service:gateway_service_server",
        "//slice_service:slice_service_server",
        "//gs_cli:gs_cli",
    ],
    visibility = ["//visibility:public"],
)

# Run all tests
test_suite(
    name = "all_tests",
    tests = [
        "//internal/storage:storage_test",
        "//workflow_test:integration_test",
    ],
)
```

---

## Phase 4: Web Frontend Bazel Build

### 4.1 Setup rules_js for Web

Create `web/BUILD.bazel`:

```starlark
load("@aspect_rules_js//js:defs.bzl", "js_library")
load("@aspect_rules_ts//ts:defs.bzl", "ts_config")
load("@npm//:defs.bzl", "npm_link_all_packages")

npm_link_all_packages(name = "node_modules")

# TypeScript configuration
ts_config(
    name = "tsconfig",
    src = "tsconfig.json",
    visibility = ["//visibility:public"],
)

# Build the web app
load("@npm//:vite/package_json.bzl", vite_bin = "bin")

vite_bin.vite(
    name = "build",
    srcs = [
        "vite.config.ts",
        "tsconfig.json",
        "index.html",
        "//web/src:all_srcs",
        "//web/public:all_assets",
        ":node_modules",
    ],
    outs = ["dist"],
    args = ["build"],
    chdir = "$(RULEDIR)",
    env = {
        "NODE_ENV": "production",
    },
)

# Serve for development (future enhancement)
# Could create a custom rule for vite dev server
```

### 4.2 Source File Organization

Create `web/src/BUILD.bazel`:

```starlark
filegroup(
    name = "all_srcs",
    srcs = glob([
        "**/*.ts",
        "**/*.tsx",
        "**/*.css",
    ]),
    visibility = ["//web:__pkg__"],
)
```

Create `web/public/BUILD.bazel`:

```starlark
filegroup(
    name = "all_assets",
    srcs = glob(["**/*"]),
    visibility = ["//web:__pkg__"],
)
```

---

## Phase 5: Testing Infrastructure

### 5.1 Go Unit Tests

Gazelle will generate `go_test` rules automatically. Key considerations:

1. **Redis Tests** - Tests using miniredis should work hermetically
2. **Integration Tests** - Need special handling for service startup

Update `workflow_test/BUILD.bazel`:

```starlark
load("@rules_go//go:def.bzl", "go_test")

go_test(
    name = "integration_test",
    srcs = ["integration_test.go"],
    data = [
        "//admin_service:admin_service_server",
        "//slice_service:slice_service_server",
        "//gs_cli:gs_cli",
    ],
    env = {
        "RUN_INTEGRATION_TESTS": "1",
    },
    tags = ["integration"],
    deps = [
        "//internal/config",
    ],
)
```

### 5.2 Test Scripts

Create test convenience script in `tools/bazel_test.sh`:

```bash
#!/bin/bash
# Run all tests
bazel test //...

# Run only unit tests (fast)
bazel test //... --test_tag_filters=-integration

# Run integration tests (slow)
bazel test //workflow_test:integration_test --test_tag_filters=integration
```

---

## Phase 6: CI/CD Migration

### 6.1 Update GitHub Actions

Update `.github/workflows/build.yml`:

```yaml
name: Build and Test

on:
  push:
    branches: [main, develop]
  pull_request:
    branches: [main, develop]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Mount Bazel Cache
        uses: actions/cache@v4
        with:
          path: |
            ~/.cache/bazel
            ~/.cache/bazel-disk-cache
          key: bazel-${{ runner.os }}-${{ hashFiles('MODULE.bazel', 'go.mod', 'web/pnpm-lock.yaml') }}
          restore-keys: |
            bazel-${{ runner.os }}-

      - name: Setup Bazel
        uses: bazelbuild/setup-bazelisk@v3

      - name: Build All
        run: bazel build //...

      - name: Run Tests
        run: bazel test //...

      - name: Upload Test Logs
        if: failure()
        uses: actions/upload-artifact@v4
        with:
          name: test-logs
          path: bazel-testlogs/
```

### 6.2 Hermetic Testing

Ensure all tests are hermetic:
- No network access during tests (unless explicitly tagged)
- No reliance on external services
- Deterministic outputs

---

## Phase 7: Documentation & Cleanup

### 7.1 Update Documentation

Update `README.md` with Bazel instructions:

```markdown
## Building with Bazel

### Prerequisites
- Install Bazelisk: `brew install bazelisk` or see [docs](https://bazel.build/install/bazelisk)

### Build
```bash
# Build all services
bazel build //...

# Build specific service
bazel build //slice_service:slice_service_server

# Run all tests
bazel test //...

# Run specific test
bazel test //internal/storage:storage_test

# Auto-generate BUILD files
bazel run //:gazelle
```

### Generated Binaries
Binaries are output to `bazel-bin/`:
- `bazel-bin/slice_service/slice_service_server`
- `bazel-bin/admin_service/admin_service_server`
- `bazel-bin/gateway_service/gateway_service_server`
- `bazel-bin/gs_cli/gs_cli`
```

### 7.2 Cleanup Legacy Files

After migration is complete:

1. Remove `Makefile` (or keep for backward compatibility calling bazel)
2. Remove manual protoc scripts
3. Clean up `third_party/googleapis` (use Bazel's instead)
4. Update `.gitignore` for Bazel artifacts

### 7.3 Update .gitignore

Add to `.gitignore`:
```
# Bazel
/bazel-*
.bazelrc.user
user.bazelrc
```

---

## Migration Timeline

| Phase | Duration | Key Deliverables |
|-------|----------|------------------|
| Phase 1 | 1-2 days | MODULE.bazel, .bazelrc, WORKSPACE (if needed) |
| Phase 2 | 2-3 days | Proto BUILD files, working proto generation |
| Phase 3 | 2-3 days | Go BUILD files, all binaries building |
| Phase 4 | 2-3 days | Web frontend building with Bazel |
| Phase 5 | 1-2 days | All tests passing under Bazel |
| Phase 6 | 1-2 days | CI/CD migrated to Bazel |
| Phase 7 | 1 day | Documentation, cleanup |

**Total Estimated Time: 10-16 days**

---

## Risk Mitigation

### Parallel Build Systems
During migration, keep both Make and Bazel working:

```makefile
# In Makefile, add Bazel targets
.PHONY: bazel-build bazel-test

bazel-build:
	bazel build //...

bazel-test:
	bazel test //...
```

### Rollback Plan
If issues arise:
1. Revert CI to use Make
2. Continue using Bazel locally
3. Fix issues incrementally

### Common Issues & Solutions

| Issue | Solution |
|-------|----------|
| Proto generation differs | Compare outputs, adjust protoc plugins |
| Go module version mismatch | Update `go_deps` in MODULE.bazel |
| Test timeouts | Add `size = "large"` or `timeout = "long"` |
| Network access in tests | Use `tags = ["requires-network"]` or mock |

---

## Future Enhancements

### Container Images
Use `rules_oci` to build container images:

```starlark
oci_image(
    name = "slice_service_image",
    base = "@distroless_base",
    entrypoint = ["/slice_service_server"],
    tars = [":slice_service_layer"],
)
```

### Remote Caching
Enable remote caching for faster builds:
```bash
# .bazelrc
build:remote --remote_cache=grpc://cache.internal:9092
build:remote --remote_upload_local_results=true
```

### Cross Compilation
Build for multiple platforms:
```bash
bazel build //slice_service:slice_service_server --platforms=@rules_go//go/toolchain:linux_amd64
bazel build //slice_service:slice_service_server --platforms=@rules_go//go/toolchain:darwin_arm64
```
