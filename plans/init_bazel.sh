#!/bin/bash
# Initialize Bazel for the Gitslice repository
# This script implements Phase 1 of the Bazel migration plan

set -e

echo "=== Initializing Bazel for Gitslice ==="

# Check if Bazelisk is installed
if ! command -v bazel &> /dev/null; then
    echo "❌ Bazel not found. Please install Bazelisk:"
    echo "   macOS: brew install bazelisk"
    echo "   Linux: See https://bazel.build/install/bazelisk"
    exit 1
fi

echo "✓ Bazel found: $(bazel --version)"

# Create .bazelversion
echo "7.3.0" > .bazelversion
echo "✓ Created .bazelversion"

# Create MODULE.bazel
cat > MODULE.bazel << 'EOF'
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

# Node.js for web frontend (optional for Phase 1)
bazel_dep(name = "rules_nodejs", version = "6.2.0")

# Go toolchain configuration
go_sdk = use_extension("@rules_go//go:extensions.bzl", "go_sdk")
go_sdk.download(version = "1.24.0")

go_deps = use_extension("@gazelle//:extensions.bzl", "go_deps")
go_deps.from_file(go_mod = "//:go.mod")
EOF

echo "✓ Created MODULE.bazel"

# Create root BUILD.bazel
cat > BUILD.bazel << 'EOF'
load("@gazelle//:def.bzl", "gazelle")

# Gazelle target for generating BUILD files
# Usage: bazel run //:gazelle
gazelle(name = "gazelle")

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
EOF

echo "✓ Created BUILD.bazel"

# Create .bazelrc
cat > .bazelrc << 'EOF'
# Build settings
build --jobs=auto
build --enable_bzlmod

# Go settings
build --@rules_go//go/config:static

# Test settings
test --test_output=errors
test --test_verbose_timeout_warnings

# Disk cache for local development
build --disk_cache=~/.cache/bazel-disk-cache

# UI settings
common --color=yes
common --curses=yes

# Proto compilation
build --proto_compiler=@com_google_protobuf//:protoc
EOF

echo "✓ Created .bazelrc"

# Create .bazelignore
cat > .bazelignore << 'EOF'
.git
node_modules
web/node_modules
bazel-*
EOF

echo "✓ Created .bazelignore"

# Update .gitignore if needed
if ! grep -q "bazel-" .gitignore 2>/dev/null; then
    cat >> .gitignore << 'EOF'

# Bazel
/bazel-*
.bazelrc.user
user.bazelrc
EOF
    echo "✓ Updated .gitignore"
fi

echo ""
echo "=== Phase 1 Complete ==="
echo ""
echo "Next steps:"
echo "1. Fetch Bazel dependencies:"
echo "   bazel fetch //..."
echo ""
echo "2. Run Gazelle to generate BUILD files:"
echo "   bazel run //:gazelle"
echo ""
echo "3. Update Go dependencies:"
echo "   bazel run //:gazelle -- update-repos -from_file=go.mod -to_macro=deps.bzl%go_dependencies"
echo ""
echo "4. Build all services:"
echo "   bazel build //..."
echo ""
