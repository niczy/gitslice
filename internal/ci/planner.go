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
	Env                 map[string]string
	CachePaths          []string
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
		job, err := planJob(platform, defaults, manifestName, manifestPath, dir, hash, key, manifest.Jobs[key], matches)
		if err != nil {
			return nil, nil, fmt.Errorf("%s jobs.%s: %w", manifestPath, key, err)
		}
		jobs = append(jobs, job)
	}
	return plannedManifest, jobs, nil
}

func planJob(platform *PlatformConfig, defaults JobDefaults, manifestName, manifestPath, manifestDir, manifestHash, key string, job ManifestJob, matches []string) (PlanJob, error) {
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
	env, err := normalizeJobEnv(job.Env)
	if err != nil {
		return PlanJob{}, err
	}
	cachePaths, err := normalizeCachePaths(platform, job.Cache)
	if err != nil {
		return PlanJob{}, err
	}
	artifacts, err := normalizeArtifactPatterns(manifestDir, job.Artifacts)
	if err != nil {
		return PlanJob{}, err
	}
	if err := validateJobRuntime(platform, runnerPool, image); err != nil {
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
		Env:                 env,
		CachePaths:          cachePaths,
		Artifacts:           artifacts,
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

func normalizeJobEnv(src map[string]any) (map[string]string, error) {
	if len(src) == 0 {
		return nil, nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		key = strings.TrimSpace(key)
		if !validEnvKey(key) {
			return nil, fmt.Errorf("env key %q is invalid", key)
		}
		switch v := value.(type) {
		case string:
			if strings.Contains(v, "\x00") {
				return nil, fmt.Errorf("env.%s contains null byte", key)
			}
			dst[key] = v
		default:
			return nil, fmt.Errorf("env.%s must be a string", key)
		}
	}
	return dst, nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	first := key[0]
	return first == '_' || (first >= 'A' && first <= 'Z') || (first >= 'a' && first <= 'z')
}

func normalizeArtifactPatterns(manifestDir string, raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(raw))
	for _, artifact := range raw {
		normalized, err := normalizeHomePattern(manifestDir, artifact)
		if err != nil {
			return nil, fmt.Errorf("artifacts path %q: %w", artifact, err)
		}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out, nil
}

func normalizeCachePaths(platform *PlatformConfig, jobCache map[string]any) ([]string, error) {
	var paths []string
	if platform != nil && platform.Cache.Enabled {
		paths = append(paths, platform.Cache.Paths...)
	}
	if raw, ok := jobCache["paths"]; ok {
		jobPaths, err := stringListFromYAML(raw)
		if err != nil {
			return nil, fmt.Errorf("cache.paths must be a list of strings")
		}
		paths = append(paths, jobPaths...)
	}
	if len(paths) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		normalized, err := normalizeCachePath(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out, nil
}

func stringListFromYAML(raw any) ([]string, error) {
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...), nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			str, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("not a string")
			}
			out = append(out, str)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("not a string list")
	}
}

func normalizeCachePath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("cache path is required")
	}
	if strings.Contains(trimmed, "\x00") {
		return "", fmt.Errorf("cache path contains null byte")
	}
	if strings.Contains(trimmed, "\\") {
		return "", fmt.Errorf("cache path must use '/' separators")
	}
	if strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("cache path must be relative or use ~/")
	}
	if strings.HasPrefix(trimmed, "~/") {
		rest := strings.TrimPrefix(trimmed, "~/")
		if rest == "" || strings.HasPrefix(path.Clean(rest), "..") {
			return "", fmt.Errorf("cache path escapes home: %s", raw)
		}
		return "~/" + path.Clean(rest), nil
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("cache path escapes workspace: %s", raw)
	}
	return cleaned, nil
}

func validateJobRuntime(platform *PlatformConfig, runnerPool, image string) error {
	pools := normalizedPools(platform)
	pool, ok := pools[runnerPool]
	if !ok {
		return fmt.Errorf("runner_pool %q is not defined", runnerPool)
	}
	executor := strings.TrimSpace(pool.Executor)
	if executor == "" {
		executor = "shell"
	}
	if executor == "docker" && strings.TrimSpace(image) == "" {
		return fmt.Errorf("image is required for docker runner_pool %q", runnerPool)
	}
	if len(pool.AllowedImages) > 0 && strings.TrimSpace(image) != "" {
		for _, allowed := range pool.AllowedImages {
			if strings.TrimSpace(allowed) == strings.TrimSpace(image) {
				return nil
			}
		}
		return fmt.Errorf("image %q is not allowed by runner_pool %q", image, runnerPool)
	}
	return nil
}

func normalizedPools(platform *PlatformConfig) map[string]RunnerPool {
	pools := make(map[string]RunnerPool)
	if platform != nil {
		for name, pool := range platform.RunnerPools {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			pools[name] = pool
		}
	}
	if len(pools) == 0 {
		pools[defaultRunnerPool] = RunnerPool{Executor: "shell"}
	}
	return pools
}
