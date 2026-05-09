package ci

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	PlatformConfigPath = "/.gitslice/ci.yaml"
	FolderManifestName = ".gs-ci.yaml"

	defaultRunnerPool     = "default"
	defaultShell          = "bash"
	defaultTimeoutSeconds = 900
	defaultWorkingDir     = "."
)

type PlatformConfig struct {
	Version     int                   `json:"version" yaml:"version"`
	Triggers    TriggerConfig         `json:"triggers,omitempty" yaml:"triggers"`
	Defaults    JobDefaults           `json:"defaults,omitempty" yaml:"defaults"`
	RunnerPools map[string]RunnerPool `json:"runner_pools,omitempty" yaml:"runner_pools"`
	Cache       CacheConfig           `json:"cache,omitempty" yaml:"cache"`
	Artifacts   ArtifactConfig        `json:"artifacts,omitempty" yaml:"artifacts"`
	Secrets     SecretConfig          `json:"secrets,omitempty" yaml:"secrets"`
	Merge       MergePolicyConfig     `json:"merge_policy,omitempty" yaml:"merge_policy"`
}

type TriggerConfig struct {
	ChangesetExport bool `json:"changeset_export,omitempty" yaml:"changeset_export"`
	MergeRequested  bool `json:"merge_requested,omitempty" yaml:"merge_requested"`
	Manual          bool `json:"manual,omitempty" yaml:"manual"`
}

type JobDefaults struct {
	RunnerPool     string `json:"runner_pool,omitempty" yaml:"runner_pool"`
	Image          string `json:"image,omitempty" yaml:"image"`
	Shell          string `json:"shell,omitempty" yaml:"shell"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" yaml:"timeout_seconds"`
	WorkingDir     string `json:"working_directory,omitempty" yaml:"working_directory"`
	Network        string `json:"network,omitempty" yaml:"network"`
}

type RunnerPool struct {
	Labels                   []string `json:"labels,omitempty" yaml:"labels"`
	Executor                 string   `json:"executor,omitempty" yaml:"executor"`
	MaxParallelJobsPerRunner int      `json:"max_parallel_jobs_per_runner,omitempty" yaml:"max_parallel_jobs_per_runner"`
	AllowedImages            []string `json:"allowed_images,omitempty" yaml:"allowed_images"`
}

type CacheConfig struct {
	Enabled       bool     `json:"enabled,omitempty" yaml:"enabled"`
	RetentionDays int      `json:"retention_days,omitempty" yaml:"retention_days"`
	Paths         []string `json:"paths,omitempty" yaml:"paths"`
}

type ArtifactConfig struct {
	RetentionDays int `json:"retention_days,omitempty" yaml:"retention_days"`
}

type SecretConfig struct {
	Allow []string `json:"allow,omitempty" yaml:"allow"`
}

type MergePolicyConfig struct {
	RequireSuccess  bool   `json:"require_success,omitempty" yaml:"require_success"`
	MissingManifest string `json:"missing_manifest,omitempty" yaml:"missing_manifest"`
	StaleCI         string `json:"stale_ci,omitempty" yaml:"stale_ci"`
	AllowForceMerge bool   `json:"allow_force_merge,omitempty" yaml:"allow_force_merge"`
}

type FolderManifest struct {
	Version   int                    `json:"version" yaml:"version"`
	Name      string                 `json:"name,omitempty" yaml:"name"`
	Watch     []string               `json:"watch,omitempty" yaml:"watch"`
	Ignore    []string               `json:"ignore,omitempty" yaml:"ignore"`
	AppliesTo []string               `json:"applies_to,omitempty" yaml:"applies_to"`
	Jobs      map[string]ManifestJob `json:"jobs,omitempty" yaml:"jobs"`
}

type ManifestJob struct {
	Required       bool           `json:"required,omitempty" yaml:"required"`
	Needs          []string       `json:"needs,omitempty" yaml:"needs"`
	RunnerPool     string         `json:"runner_pool,omitempty" yaml:"runner_pool"`
	Image          string         `json:"image,omitempty" yaml:"image"`
	Shell          string         `json:"shell,omitempty" yaml:"shell"`
	WorkingDir     string         `json:"working_directory,omitempty" yaml:"working_directory"`
	TimeoutSeconds int            `json:"timeout_seconds,omitempty" yaml:"timeout_seconds"`
	Commands       []string       `json:"commands,omitempty" yaml:"commands"`
	Env            map[string]any `json:"env,omitempty" yaml:"env"`
	Cache          map[string]any `json:"cache,omitempty" yaml:"cache"`
	Artifacts      []string       `json:"artifacts,omitempty" yaml:"artifacts"`
}

func ParsePlatformConfig(raw []byte) (*PlatformConfig, error) {
	var cfg PlatformConfig
	if err := decodeStrictYAML(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse platform config: %w", err)
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("unsupported platform config version %d", cfg.Version)
	}
	if cfg.Defaults.TimeoutSeconds < 0 {
		return nil, fmt.Errorf("defaults.timeout_seconds must be non-negative")
	}
	for name, pool := range cfg.RunnerPools {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("runner_pools contains an empty pool name")
		}
		if pool.MaxParallelJobsPerRunner < 0 {
			return nil, fmt.Errorf("runner_pools.%s.max_parallel_jobs_per_runner must be non-negative", name)
		}
	}
	return &cfg, nil
}

func ParseFolderManifest(raw []byte) (*FolderManifest, error) {
	var manifest FolderManifest
	if err := decodeStrictYAML(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse folder manifest: %w", err)
	}
	if manifest.Version != 1 {
		return nil, fmt.Errorf("unsupported folder manifest version %d", manifest.Version)
	}
	if len(manifest.Jobs) == 0 {
		return nil, fmt.Errorf("folder manifest must define at least one job")
	}
	for key, job := range manifest.Jobs {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("folder manifest contains an empty job key")
		}
		if job.TimeoutSeconds < 0 {
			return nil, fmt.Errorf("jobs.%s.timeout_seconds must be non-negative", key)
		}
		if len(job.Commands) == 0 {
			return nil, fmt.Errorf("jobs.%s.commands must contain at least one command", key)
		}
		for _, need := range job.Needs {
			if _, ok := manifest.Jobs[need]; !ok {
				return nil, fmt.Errorf("jobs.%s.needs references unknown job %q", key, need)
			}
		}
	}
	return &manifest, nil
}

func decodeStrictYAML(raw []byte, out any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("empty yaml")
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

func (d JobDefaults) withRuntimeDefaults() JobDefaults {
	if strings.TrimSpace(d.RunnerPool) == "" {
		d.RunnerPool = defaultRunnerPool
	}
	if strings.TrimSpace(d.Shell) == "" {
		d.Shell = defaultShell
	}
	if d.TimeoutSeconds == 0 {
		d.TimeoutSeconds = defaultTimeoutSeconds
	}
	if strings.TrimSpace(d.WorkingDir) == "" {
		d.WorkingDir = defaultWorkingDir
	}
	return d
}
