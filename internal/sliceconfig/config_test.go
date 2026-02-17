package sliceconfig

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func TestParseConfig(t *testing.T) {
	valid := []byte(`environments:
  node20:
    display_name: "Node.js 20"
slices:
  auth-service:
    environment: node20
defaults:
  environment: node20
`)
	cfg, err := ParseConfig(valid)
	if err != nil {
		t.Fatalf("ParseConfig valid failed: %v", err)
	}
	if cfg.Environments["node20"].DisplayName != "Node.js 20" {
		t.Fatalf("unexpected env display name: %#v", cfg.Environments["node20"])
	}
	if cfg.Slices["auth-service"].Environment != "node20" {
		t.Fatalf("unexpected slice env: %#v", cfg.Slices["auth-service"])
	}
	if cfg.Defaults.Environment != "node20" {
		t.Fatalf("unexpected defaults env: %#v", cfg.Defaults)
	}

	partial := []byte(`slices:
  a:
    environment: node20
`)
	cfg, err = ParseConfig(partial)
	if err != nil {
		t.Fatalf("ParseConfig partial failed: %v", err)
	}
	if len(cfg.Environments) != 0 {
		t.Fatalf("expected empty environments for partial config, got %#v", cfg.Environments)
	}
	if cfg.Slices["a"].Environment != "node20" {
		t.Fatalf("unexpected partial slice env: %#v", cfg.Slices["a"])
	}

	if _, err := ParseConfig([]byte(`environments: [1,`)); err == nil {
		t.Fatalf("expected parse error for invalid yaml")
	}
}

func TestApplyConfig(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{ID: "s1", Name: "auth-service", Owners: []string{"alice"}, CreatedBy: "alice"}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.CreateEnvironment(ctx, &models.Environment{
		Name:        "node20",
		DisplayName: "Node",
		Provider:    "e2b",
		ProviderID:  "tmpl-node20",
		Region:      "us-west-2",
		CreatedBy:   "alice",
	}); err != nil {
		t.Fatalf("CreateEnvironment failed: %v", err)
	}

	cfg := &SliceConfigFile{
		Environments: map[string]EnvEntry{
			"node20": {DisplayName: "Node.js 20"},
		},
		Slices: map[string]SliceEntry{
			"auth-service":  {Environment: "node20"},
			"missing-slice": {Environment: "node20"},
		},
	}
	if err := ApplyConfig(ctx, st, cfg); err != nil {
		t.Fatalf("ApplyConfig failed: %v", err)
	}

	slice, err := st.GetSlice(ctx, "s1")
	if err != nil {
		t.Fatalf("GetSlice failed: %v", err)
	}
	if slice.Environment != "node20" {
		t.Fatalf("expected slice environment node20, got %q", slice.Environment)
	}
	env, err := st.GetEnvironment(ctx, "node20")
	if err != nil {
		t.Fatalf("GetEnvironment failed: %v", err)
	}
	if env.DisplayName != "Node.js 20" {
		t.Fatalf("expected updated display name, got %q", env.DisplayName)
	}
}

func TestApplyConfigWarnsForUnregisteredEnvironment(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	cfg := &SliceConfigFile{
		Environments: map[string]EnvEntry{
			"missing": {DisplayName: "Missing Env"},
		},
	}

	oldWriter := log.Writer()
	defer log.SetOutput(oldWriter)
	var buf bytes.Buffer
	log.SetOutput(&buf)

	if err := ApplyConfig(ctx, st, cfg); err != nil {
		t.Fatalf("ApplyConfig failed: %v", err)
	}
	if !strings.Contains(buf.String(), `sliceconfig: environment "missing" declared in config but not registered`) {
		t.Fatalf("expected warning log, got %q", buf.String())
	}
}

func TestApplyFromFileTree(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}
	if err := st.CreateSlice(ctx, &models.Slice{ID: "s2", Name: "auth-service", Owners: []string{"alice"}, CreatedBy: "alice"}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.CreateEnvironment(ctx, &models.Environment{
		Name:        "node20",
		DisplayName: "Node",
		Provider:    "e2b",
		ProviderID:  "tmpl-node20",
		Region:      "us-west-2",
		CreatedBy:   "alice",
	}); err != nil {
		t.Fatalf("CreateEnvironment failed: %v", err)
	}

	if err := ApplyFromFileTree(ctx, st); err != nil {
		t.Fatalf("ApplyFromFileTree without config should not fail: %v", err)
	}

	root, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	content := []byte("slices:\n  auth-service:\n    environment: node20\n")
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       root.ID + ":" + ConfigFilePath,
		Path:     ConfigFilePath,
		Type:     "file",
		ParentID: root.ID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := st.AddFileContent(ctx, &models.FileContent{
		FileID:  ConfigFilePath,
		Path:    ConfigFilePath,
		Content: content,
		Size:    int64(len(content)),
	}); err != nil {
		t.Fatalf("AddFileContent failed: %v", err)
	}

	if err := ApplyFromFileTree(ctx, st); err != nil {
		t.Fatalf("ApplyFromFileTree failed: %v", err)
	}
	slice, err := st.GetSlice(ctx, "s2")
	if err != nil {
		t.Fatalf("GetSlice failed: %v", err)
	}
	if slice.Environment != "node20" {
		t.Fatalf("expected slice environment from config, got %q", slice.Environment)
	}

	badContent := []byte("slices: [")
	if err := st.AddFileContent(ctx, &models.FileContent{
		FileID:  ConfigFilePath,
		Path:    ConfigFilePath,
		Content: badContent,
		Size:    int64(len(badContent)),
	}); err != nil {
		t.Fatalf("AddFileContent bad failed: %v", err)
	}
	if err := ApplyFromFileTree(ctx, st); err == nil {
		t.Fatalf("expected parse error for malformed config")
	}
}
