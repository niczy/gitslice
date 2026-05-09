package ci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

var ErrFileNotFound = errors.New("ci file not found")

type TreeReader interface {
	ReadFile(ctx context.Context, logicalPath string) ([]byte, string, error)
}

type Planner struct {
	Tree TreeReader
}

type PlanInput struct {
	HomeID               string
	SliceID              string
	ChangesetID          string
	ChangesetVersionID   string
	BaseCommitHash       string
	CandidateTreeHash    string
	ChangedPaths         []string
	IndexedManifestPaths []string
}

type Plan struct {
	HomeID             string
	SliceID            string
	ChangesetID        string
	ChangesetVersionID string
	BaseCommitHash     string
	CandidateTreeHash  string
	PlatformConfigPath string
	PlatformConfigHash string
	ChangedPaths       []string
	Manifests          []PlanManifest
	Jobs               []PlanJob
	PlanHash           string
	MissingManifest    bool
}

type PlanManifest struct {
	Path                string
	Dir                 string
	Name                string
	Hash                string
	MatchedChangedPaths []string
}

type PlanJob struct {
	ManifestPath        string
	ManifestDir         string
	ManifestHash        string
	JobKey              string
	CheckName           string
	Required            bool
	Needs               []string
	RunnerPool          string
	Image               string
	Shell               string
	WorkingDirectory    string
	TimeoutSeconds      int
	Commands            []string
	Env                 map[string]any
	Cache               map[string]any
	Artifacts           []string
	MatchedChangedPaths []string
}

func (p *Planner) Plan(ctx context.Context, input PlanInput) (*Plan, error) {
	if p == nil || p.Tree == nil {
		return nil, fmt.Errorf("ci planner requires a tree reader")
	}
	changedPaths, err := normalizeChangedPaths(input.ChangedPaths)
	if err != nil {
		return nil, err
	}
	platformRaw, platformHash, err := p.readFile(ctx, PlatformConfigPath)
	if err != nil {
		return nil, err
	}
	platform, err := ParsePlatformConfig(platformRaw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", PlatformConfigPath, err)
	}

	manifestPaths, err := p.candidateManifestPaths(input.IndexedManifestPaths, changedPaths)
	if err != nil {
		return nil, err
	}

	plan := &Plan{
		HomeID:             strings.TrimSpace(input.HomeID),
		SliceID:            strings.TrimSpace(input.SliceID),
		ChangesetID:        strings.TrimSpace(input.ChangesetID),
		ChangesetVersionID: strings.TrimSpace(input.ChangesetVersionID),
		BaseCommitHash:     strings.TrimSpace(input.BaseCommitHash),
		CandidateTreeHash:  strings.TrimSpace(input.CandidateTreeHash),
		PlatformConfigPath: PlatformConfigPath,
		PlatformConfigHash: platformHash,
		ChangedPaths:       append([]string(nil), changedPaths...),
	}

	for _, manifestPath := range manifestPaths {
		manifestPlan, jobs, err := p.planManifest(ctx, platform, manifestPath, changedPaths)
		if err != nil {
			return nil, err
		}
		if manifestPlan == nil {
			continue
		}
		plan.Manifests = append(plan.Manifests, *manifestPlan)
		plan.Jobs = append(plan.Jobs, jobs...)
	}
	sort.Slice(plan.Manifests, func(i, j int) bool {
		return plan.Manifests[i].Path < plan.Manifests[j].Path
	})
	sort.Slice(plan.Jobs, func(i, j int) bool {
		if plan.Jobs[i].ManifestPath == plan.Jobs[j].ManifestPath {
			return plan.Jobs[i].JobKey < plan.Jobs[j].JobKey
		}
		return plan.Jobs[i].ManifestPath < plan.Jobs[j].ManifestPath
	})
	plan.MissingManifest = len(plan.Manifests) == 0
	plan.PlanHash, err = computePlanHash(plan)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func (p *Planner) candidateManifestPaths(indexedPaths []string, changedPaths []string) ([]string, error) {
	seen := make(map[string]struct{})
	for _, manifestPath := range ancestorManifestPaths(changedPaths) {
		seen[manifestPath] = struct{}{}
	}
	for _, raw := range indexedPaths {
		normalized, err := NormalizeHomePath(raw)
		if err != nil {
			return nil, fmt.Errorf("indexed manifest path %q: %w", raw, err)
		}
		if path.Base(normalized) != FolderManifestName {
			return nil, fmt.Errorf("indexed manifest path must end with %s: %s", FolderManifestName, raw)
		}
		seen[normalized] = struct{}{}
	}
	paths := make([]string, 0, len(seen))
	for manifestPath := range seen {
		paths = append(paths, manifestPath)
	}
	sort.Strings(paths)
	return paths, nil
}

func (p *Planner) planManifest(ctx context.Context, platform *PlatformConfig, manifestPath string, changedPaths []string) (*PlanManifest, []PlanJob, error) {
	raw, hash, err := p.readFile(ctx, manifestPath)
	if errors.Is(err, ErrFileNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	dir, err := manifestDir(manifestPath)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := ParseFolderManifest(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", manifestPath, err)
	}
	matches, err := matchedChangedPaths(manifest, dir, changedPaths)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", manifestPath, err)
	}
	if len(matches) == 0 {
		return nil, nil, nil
	}
	manifestName := strings.TrimSpace(manifest.Name)
	if manifestName == "" {
		manifestName = displayManifestDir(dir)
	}
	plannedManifest := &PlanManifest{
		Path:                manifestPath,
		Dir:                 dir,
		Name:                manifestName,
		Hash:                hash,
		MatchedChangedPaths: matches,
	}

	defaults := platform.Defaults.withRuntimeDefaults()
	jobKeys := make([]string, 0, len(manifest.Jobs))
	for key := range manifest.Jobs {
		jobKeys = append(jobKeys, key)
	}
	sort.Strings(jobKeys)
	jobs := make([]PlanJob, 0, len(jobKeys))
	for _, key := range jobKeys {
		job, err := planJob(defaults, manifestName, manifestPath, dir, hash, key, manifest.Jobs[key], matches)
		if err != nil {
			return nil, nil, fmt.Errorf("%s jobs.%s: %w", manifestPath, key, err)
		}
		jobs = append(jobs, job)
	}
	return plannedManifest, jobs, nil
}

func planJob(defaults JobDefaults, manifestName, manifestPath, manifestDir, manifestHash, key string, job ManifestJob, matches []string) (PlanJob, error) {
	runnerPool := firstNonEmpty(job.RunnerPool, defaults.RunnerPool, defaultRunnerPool)
	image := firstNonEmpty(job.Image, defaults.Image)
	shell := firstNonEmpty(job.Shell, defaults.Shell, defaultShell)
	timeout := job.TimeoutSeconds
	if timeout == 0 {
		timeout = defaults.TimeoutSeconds
	}
	workingDir := firstNonEmpty(job.WorkingDir, defaults.WorkingDir, defaultWorkingDir)
	normalizedWorkingDir, err := normalizeHomePattern(manifestDir, workingDir)
	if err != nil {
		return PlanJob{}, err
	}
	needs := append([]string(nil), job.Needs...)
	sort.Strings(needs)
	return PlanJob{
		ManifestPath:        manifestPath,
		ManifestDir:         manifestDir,
		ManifestHash:        manifestHash,
		JobKey:              key,
		CheckName:           manifestName + "/" + key,
		Required:            job.Required,
		Needs:               needs,
		RunnerPool:          runnerPool,
		Image:               image,
		Shell:               shell,
		WorkingDirectory:    normalizedWorkingDir,
		TimeoutSeconds:      timeout,
		Commands:            append([]string(nil), job.Commands...),
		Env:                 copyAnyMap(job.Env),
		Cache:               copyAnyMap(job.Cache),
		Artifacts:           append([]string(nil), job.Artifacts...),
		MatchedChangedPaths: append([]string(nil), matches...),
	}, nil
}

func matchedChangedPaths(manifest *FolderManifest, manifestDir string, changedPaths []string) ([]string, error) {
	matches := make([]string, 0, len(changedPaths))
	for _, changed := range changedPaths {
		ignored, err := matchManifestPatterns(manifest.Ignore, manifestDir, changed)
		if err != nil {
			return nil, err
		}
		if ignored {
			continue
		}

		watchMatch := pathWithinDir(changed, manifestDir)
		if len(manifest.Watch) > 0 {
			watchMatch, err = matchManifestPatterns(manifest.Watch, manifestDir, changed)
			if err != nil {
				return nil, err
			}
		}
		appliesMatch := false
		if len(manifest.AppliesTo) > 0 {
			appliesMatch, err = matchManifestPatterns(manifest.AppliesTo, manifestDir, changed)
			if err != nil {
				return nil, err
			}
		}
		if watchMatch || appliesMatch {
			matches = append(matches, changed)
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func (p *Planner) readFile(ctx context.Context, logicalPath string) ([]byte, string, error) {
	normalized, err := NormalizeHomePath(logicalPath)
	if err != nil {
		return nil, "", err
	}
	raw, contentHash, err := p.Tree.ReadFile(ctx, normalized)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(contentHash) == "" {
		contentHash = hashBytes(raw)
	}
	return raw, contentHash, nil
}

func computePlanHash(plan *Plan) (string, error) {
	input := struct {
		HomeID             string         `json:"home_id"`
		SliceID            string         `json:"slice_id"`
		ChangesetID        string         `json:"changeset_id"`
		ChangesetVersionID string         `json:"changeset_version_id"`
		BaseCommitHash     string         `json:"base_commit_hash"`
		CandidateTreeHash  string         `json:"candidate_tree_hash"`
		PlatformConfigHash string         `json:"platform_config_hash"`
		ChangedPaths       []string       `json:"changed_paths"`
		Manifests          []PlanManifest `json:"manifests"`
		Jobs               []PlanJob      `json:"jobs"`
	}{
		HomeID:             plan.HomeID,
		SliceID:            plan.SliceID,
		ChangesetID:        plan.ChangesetID,
		ChangesetVersionID: plan.ChangesetVersionID,
		BaseCommitHash:     plan.BaseCommitHash,
		CandidateTreeHash:  plan.CandidateTreeHash,
		PlatformConfigHash: plan.PlatformConfigHash,
		ChangedPaths:       plan.ChangedPaths,
		Manifests:          plan.Manifests,
		Jobs:               plan.Jobs,
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	return hashBytes(raw), nil
}

func hashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func displayManifestDir(dir string) string {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if dir == "" {
		return "root"
	}
	return dir
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func copyAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
