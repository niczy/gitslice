package main

import (
	"testing"

	"github.com/niczy/gitslice/internal/storage"
)

func TestResolvePruneEffectiveCommit(t *testing.T) {
	info := map[string]pruneSliceInfo{
		"root_slice":     {headCommit: "root-head"},
		"home.alice":     {parentID: "root_slice", headCommit: "fs-home"},
		"slice.child":    {parentID: "home.alice", headCommit: "init-slice"},
		"slice.grand":    {parentID: "slice.child", headCommit: ""},
		"slice.detached": {headCommit: "custom-head"},
	}

	cache := map[string]string{}
	if got, want := resolvePruneEffectiveCommit("home.alice", info, cache, map[string]bool{}), "fs-home"; got != want {
		t.Fatalf("home effective commit mismatch: got %q want %q", got, want)
	}
	if got, want := resolvePruneEffectiveCommit("slice.child", info, cache, map[string]bool{}), "fs-home"; got != want {
		t.Fatalf("child effective commit mismatch: got %q want %q", got, want)
	}
	if got, want := resolvePruneEffectiveCommit("slice.grand", info, cache, map[string]bool{}), "fs-home"; got != want {
		t.Fatalf("grandchild effective commit mismatch: got %q want %q", got, want)
	}
	if got, want := resolvePruneEffectiveCommit("slice.detached", info, cache, map[string]bool{}), "custom-head"; got != want {
		t.Fatalf("detached effective commit mismatch: got %q want %q", got, want)
	}
}

func TestCountAffectedPruneSlices(t *testing.T) {
	entries := []pruneCandidate{
		{sliceID: "root_slice"},
		{sliceID: "home.alice"},
		{sliceID: "home.alice"},
		{sliceID: "slice.child"},
	}
	if got, want := countAffectedPruneSlices(entries), 3; got != want {
		t.Fatalf("affected slice count mismatch: got %d want %d", got, want)
	}
}

func TestValidateObjectStoreMigrationConfigRejectsMismatchedR2Namespace(t *testing.T) {
	cfg := storage.ObjectStoreConfig{
		Type:     "r2",
		R2Bucket: "bucket",
		R2Prefix: "staging",
	}
	if err := validateObjectStoreMigrationConfig("target", cfg, "production"); err == nil {
		t.Fatalf("expected mismatched R2 namespace validation failure")
	}
}

func TestValidateObjectStoreMigrationConfigAllowsMatchingR2Namespace(t *testing.T) {
	cfg := storage.ObjectStoreConfig{
		Type:     "r2",
		R2Bucket: "bucket",
		R2Prefix: "production/core",
	}
	if err := validateObjectStoreMigrationConfig("target", cfg, "production"); err != nil {
		t.Fatalf("expected matching R2 namespace to validate, got %v", err)
	}
}

func TestValidateObjectStoreMigrationConfigRequiresEnvForR2(t *testing.T) {
	cfg := storage.ObjectStoreConfig{
		Type:     "r2",
		R2Bucket: "bucket",
		R2Prefix: "production",
	}
	if err := validateObjectStoreMigrationConfig("source", cfg, ""); err == nil {
		t.Fatalf("expected missing R2 env validation failure")
	}
}

func TestSameObjectStoreConfig(t *testing.T) {
	left := storage.ObjectStoreConfig{
		Type:       "r2",
		R2Bucket:   "bucket",
		R2Prefix:   "production",
		R2Endpoint: "https://account.r2.cloudflarestorage.com",
	}
	right := storage.ObjectStoreConfig{
		Type:       "r2",
		R2Bucket:   "bucket",
		R2Prefix:   "production/",
		R2Endpoint: "https://account.r2.cloudflarestorage.com",
	}
	if !sameObjectStoreConfig(left, right) {
		t.Fatalf("expected matching object-store configs to compare equal")
	}
}
