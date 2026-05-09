package ci

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const platformYAML = `
version: 1
triggers:
  changeset_export: true
  manual: true
defaults:
  runner_pool: default
  image: golang:1.24
  shell: bash
  timeout_seconds: 900
  working_directory: "."
runner_pools:
  default:
    labels: ["linux", "docker"]
    executor: docker
    max_parallel_jobs_per_runner: 2
    allowed_images:
      - golang:1.24
merge_policy:
  require_success: true
  missing_manifest: allow
  stale_ci: block
`

type mapTree map[string]string

func (m mapTree) ReadFile(_ context.Context, logicalPath string) ([]byte, string, error) {
	body, ok := m[logicalPath]
	if !ok {
		return nil, "", ErrFileNotFound
	}
	return []byte(body), "", nil
}

func TestParsePlatformConfig(t *testing.T) {
	cfg, err := ParsePlatformConfig([]byte(platformYAML))
	if err != nil {
		t.Fatalf("ParsePlatformConfig failed: %v", err)
	}
	if cfg.Version != 1 {
		t.Fatalf("version = %d, want 1", cfg.Version)
	}
	if got := cfg.Defaults.RunnerPool; got != "default" {
		t.Fatalf("default runner pool = %q, want default", got)
	}
	if got := cfg.RunnerPools["default"].AllowedImages[0]; got != "golang:1.24" {
		t.Fatalf("allowed image = %q, want golang:1.24", got)
	}
}

func TestPlannerAncestorManifestAndDefaultWorkingDirectory(t *testing.T) {
	planner := Planner{Tree: mapTree{
		PlatformConfigPath: platformYAML,
		"/api/.gs-ci.yaml": `
version: 1
name: api
watch:
  - "**/*.go"
ignore:
  - "tmp/**"
jobs:
  unit:
    required: true
    commands:
      - go test ./...
  integration:
    required: true
    needs: ["unit"]
    working_directory: "/"
    timeout_seconds: 1200
    commands:
      - make integration-api
`,
	}}
	plan, err := planner.Plan(context.Background(), PlanInput{
		HomeID:             "home-1",
		SliceID:            "slice-1",
		ChangesetID:        "cs-1",
		ChangesetVersionID: "v1",
		BaseCommitHash:     "base-1",
		CandidateTreeHash:  "tree-1",
		ChangedPaths:       []string{"/api/server/routes.go"},
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(plan.Manifests) != 1 {
		t.Fatalf("manifest count = %d, want 1", len(plan.Manifests))
	}
	if got := plan.Manifests[0].Path; got != "/api/.gs-ci.yaml" {
		t.Fatalf("manifest path = %q, want /api/.gs-ci.yaml", got)
	}
	if len(plan.Jobs) != 2 {
		t.Fatalf("job count = %d, want 2", len(plan.Jobs))
	}
	if plan.Jobs[0].JobKey != "integration" || plan.Jobs[0].WorkingDirectory != "/" {
		t.Fatalf("integration job = %#v, want root working directory", plan.Jobs[0])
	}
	if plan.Jobs[1].JobKey != "unit" || plan.Jobs[1].WorkingDirectory != "/api" {
		t.Fatalf("unit job = %#v, want /api working directory", plan.Jobs[1])
	}
	if got := plan.Jobs[1].CheckName; got != "api/unit" {
		t.Fatalf("check name = %q, want api/unit", got)
	}
	if plan.MissingManifest {
		t.Fatalf("MissingManifest = true, want false")
	}
	if !strings.HasPrefix(plan.PlanHash, "sha256:") {
		t.Fatalf("plan hash = %q, want sha256 prefix", plan.PlanHash)
	}
}

func TestPlannerAppliesToIndexedManifest(t *testing.T) {
	planner := Planner{Tree: mapTree{
		PlatformConfigPath: platformYAML,
		"/api/.gs-ci.yaml": `
version: 1
name: api
watch:
  - "**/*.go"
applies_to:
  - "/shared/proto"
jobs:
  integration:
    required: true
    commands:
      - make integration-api
`,
	}}
	plan, err := planner.Plan(context.Background(), PlanInput{
		HomeID:               "home-1",
		SliceID:              "slice-1",
		ChangesetID:          "cs-1",
		ChangesetVersionID:   "v1",
		BaseCommitHash:       "base-1",
		CandidateTreeHash:    "tree-1",
		ChangedPaths:         []string{"/shared/proto/types.proto"},
		IndexedManifestPaths: []string{"/api/.gs-ci.yaml"},
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(plan.Jobs) != 1 {
		t.Fatalf("job count = %d, want 1", len(plan.Jobs))
	}
	if got := plan.Jobs[0].MatchedChangedPaths; len(got) != 1 || got[0] != "/shared/proto/types.proto" {
		t.Fatalf("matched paths = %#v, want shared proto path", got)
	}
}

func TestPlannerIgnoreSuppressesWatchMatch(t *testing.T) {
	planner := Planner{Tree: mapTree{
		PlatformConfigPath: platformYAML,
		"/api/.gs-ci.yaml": `
version: 1
watch:
  - "**/*.go"
ignore:
  - "tmp/**"
jobs:
  unit:
    commands:
      - go test ./...
`,
	}}
	plan, err := planner.Plan(context.Background(), PlanInput{
		HomeID:             "home-1",
		SliceID:            "slice-1",
		ChangesetID:        "cs-1",
		ChangesetVersionID: "v1",
		BaseCommitHash:     "base-1",
		CandidateTreeHash:  "tree-1",
		ChangedPaths:       []string{"/api/tmp/generated.go"},
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(plan.Jobs) != 0 {
		t.Fatalf("job count = %d, want 0", len(plan.Jobs))
	}
	if !plan.MissingManifest {
		t.Fatalf("MissingManifest = false, want true")
	}
}

func TestPlannerRejectsEscapingPaths(t *testing.T) {
	if _, err := NormalizeHomePath("../outside"); err == nil {
		t.Fatalf("NormalizeHomePath should reject escaping path")
	}
	planner := Planner{Tree: mapTree{
		PlatformConfigPath: platformYAML,
		"/api/.gs-ci.yaml": `
version: 1
jobs:
  unit:
    working_directory: "../../outside"
    commands:
      - go test ./...
`,
	}}
	_, err := planner.Plan(context.Background(), PlanInput{
		ChangedPaths: []string{"/api/main.go"},
	})
	if err == nil {
		t.Fatalf("Plan should reject escaping working_directory")
	}
}

func TestPlannerRejectsDisallowedImage(t *testing.T) {
	planner := Planner{Tree: mapTree{
		PlatformConfigPath: platformYAML,
		"/api/.gs-ci.yaml": `
version: 1
jobs:
  unit:
    image: node:22
    commands:
      - npm test
`,
	}}
	_, err := planner.Plan(context.Background(), PlanInput{ChangedPaths: []string{"/api/main.go"}})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("Plan error = %v, want disallowed image", err)
	}
}

func TestPlannerNormalizesEnvCacheAndArtifacts(t *testing.T) {
	planner := Planner{Tree: mapTree{
		PlatformConfigPath: strings.Replace(platformYAML, "merge_policy:", `cache:
  enabled: true
  paths:
    - ".cache/go"
merge_policy:`, 1),
		"/api/.gs-ci.yaml": `
version: 1
jobs:
  unit:
    env:
      FOO: bar
    cache:
      paths:
        - ".cache/npm"
    artifacts:
      - "dist/**"
    commands:
      - go test ./...
`,
	}}
	plan, err := planner.Plan(context.Background(), PlanInput{ChangedPaths: []string{"/api/main.go"}})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if got := plan.Jobs[0].Env["FOO"]; got != "bar" {
		t.Fatalf("env FOO = %q, want bar", got)
	}
	if !containsString(plan.Jobs[0].CachePaths, ".cache/go") || !containsString(plan.Jobs[0].CachePaths, ".cache/npm") {
		t.Fatalf("cache paths = %#v, want platform and job paths", plan.Jobs[0].CachePaths)
	}
	if got := plan.Jobs[0].Artifacts; len(got) != 1 || got[0] != "/api/dist/**" {
		t.Fatalf("artifacts = %#v, want /api/dist/**", got)
	}
}

func TestPlannerPlanHashIsStableAndChangesWhenInputsChange(t *testing.T) {
	tree := mapTree{
		PlatformConfigPath: platformYAML,
		"/api/.gs-ci.yaml": `
version: 1
name: api
jobs:
  unit:
    commands:
      - go test ./...
`,
	}
	input := PlanInput{
		HomeID:             "home-1",
		SliceID:            "slice-1",
		ChangesetID:        "cs-1",
		ChangesetVersionID: "v1",
		BaseCommitHash:     "base-1",
		CandidateTreeHash:  "tree-1",
		ChangedPaths:       []string{"/api/b.go", "/api/a.go"},
	}
	planner := Planner{Tree: tree}
	first, err := planner.Plan(context.Background(), input)
	if err != nil {
		t.Fatalf("Plan first failed: %v", err)
	}
	input.ChangedPaths = []string{"/api/a.go", "/api/b.go"}
	second, err := planner.Plan(context.Background(), input)
	if err != nil {
		t.Fatalf("Plan second failed: %v", err)
	}
	if first.PlanHash != second.PlanHash {
		t.Fatalf("plan hash changed with changed path order: %q != %q", first.PlanHash, second.PlanHash)
	}

	input.ChangesetVersionID = "v2"
	versionChanged, err := planner.Plan(context.Background(), input)
	if err != nil {
		t.Fatalf("Plan versionChanged failed: %v", err)
	}
	if versionChanged.PlanHash == first.PlanHash {
		t.Fatalf("plan hash should change when changeset version changes")
	}

	tree["/api/.gs-ci.yaml"] = strings.Replace(tree["/api/.gs-ci.yaml"], "go test ./...", "go test ./... -run TestUnit", 1)
	manifestChanged, err := planner.Plan(context.Background(), PlanInput{
		HomeID:             "home-1",
		SliceID:            "slice-1",
		ChangesetID:        "cs-1",
		ChangesetVersionID: "v1",
		BaseCommitHash:     "base-1",
		CandidateTreeHash:  "tree-1",
		ChangedPaths:       []string{"/api/a.go", "/api/b.go"},
	})
	if err != nil {
		t.Fatalf("Plan manifestChanged failed: %v", err)
	}
	if manifestChanged.PlanHash == first.PlanHash {
		t.Fatalf("plan hash should change when manifest content changes")
	}

	tree[PlatformConfigPath] = strings.ReplaceAll(tree[PlatformConfigPath], "golang:1.24", "golang:1.25")
	platformChanged, err := planner.Plan(context.Background(), PlanInput{
		HomeID:             "home-1",
		SliceID:            "slice-1",
		ChangesetID:        "cs-1",
		ChangesetVersionID: "v1",
		BaseCommitHash:     "base-1",
		CandidateTreeHash:  "tree-1",
		ChangedPaths:       []string{"/api/a.go", "/api/b.go"},
	})
	if err != nil {
		t.Fatalf("Plan platformChanged failed: %v", err)
	}
	if platformChanged.PlanHash == manifestChanged.PlanHash || platformChanged.PlanHash == first.PlanHash {
		t.Fatalf("plan hash should change when platform config changes")
	}
}

func TestPlannerRequiresPlatformConfig(t *testing.T) {
	planner := Planner{Tree: mapTree{}}
	_, err := planner.Plan(context.Background(), PlanInput{ChangedPaths: []string{"/api/main.go"}})
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("Plan error = %v, want ErrFileNotFound", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
