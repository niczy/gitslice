package sliceconfig

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

const ConfigFilePath = ".gitslice/config.yaml"

type SliceConfigFile struct {
	Environments map[string]EnvEntry   `yaml:"environments"`
	Slices       map[string]SliceEntry `yaml:"slices"`
	Defaults     DefaultsEntry         `yaml:"defaults"`
}

type EnvEntry struct {
	DisplayName string `yaml:"display_name"`
}

type SliceEntry struct {
	Environment string `yaml:"environment"`
}

type DefaultsEntry struct {
	Environment string `yaml:"environment"`
}

func ParseConfig(raw []byte) (*SliceConfigFile, error) {
	cfg := &SliceConfigFile{
		Environments: map[string]EnvEntry{},
		Slices:       map[string]SliceEntry{},
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return cfg, nil
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	if cfg.Environments == nil {
		cfg.Environments = map[string]EnvEntry{}
	}
	if cfg.Slices == nil {
		cfg.Slices = map[string]SliceEntry{}
	}
	return cfg, nil
}
