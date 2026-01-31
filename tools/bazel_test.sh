#!/bin/bash
set -e

echo "=== Running Bazel Tests ==="

# Run all tests
echo "Running all tests..."
bazel test //... "$@"

# Run only unit tests (fast)
echo ""
echo "To run only unit tests (fast):"
echo "  bazel test //... --test_tag_filters=-integration"

# Run integration tests (slow)
echo ""
echo "To run integration tests only:"
echo "  bazel test //workflow_test:integration_test --test_tag_filters=integration"
