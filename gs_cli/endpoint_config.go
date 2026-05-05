package gscli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const defaultGRPCServerAddr = "localhost:50051"

var parsedGlobalFlagNames map[string]bool

type cliEndpointConfig struct {
	Addr        string `json:"addr,omitempty"`
	AccountAddr string `json:"account_addr,omitempty"`
	SliceAddr   string `json:"slice_addr,omitempty"`
	AdminAddr   string `json:"admin_addr,omitempty"`
	FileAddr    string `json:"file_addr,omitempty"`
	FSAddr      string `json:"fs_addr,omitempty"`
	TLS         *bool  `json:"tls,omitempty"`
}

type cliEndpointSettings struct {
	ConfigPath    string
	ConfigPresent bool
	CoreAddr      string
	AccountAddr   string
	SliceAddr     string
	AdminAddr     string
	FileAddr      string
	FSAddr        string
	TLS           bool
	AddressSource string
	TLSSource     string
}

type jsonEndpointConfigOutput struct {
	Status        string `json:"status,omitempty"`
	ConfigPath    string `json:"config_path"`
	ConfigPresent bool   `json:"config_present"`
	Addr          string `json:"addr,omitempty"`
	AccountAddr   string `json:"account_addr"`
	SliceAddr     string `json:"slice_addr"`
	AdminAddr     string `json:"admin_addr"`
	FileAddr      string `json:"file_addr"`
	FSAddr        string `json:"fs_addr"`
	TLS           bool   `json:"tls"`
	AddressSource string `json:"address_source,omitempty"`
	TLSSource     string `json:"tls_source,omitempty"`
}

func endpointConfigPath() (string, error) {
	configDir, err := gitsliceConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "config.json"), nil
}

func readEndpointConfig() (cliEndpointConfig, bool, error) {
	path, err := endpointConfigPath()
	if err != nil {
		return cliEndpointConfig{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cliEndpointConfig{}, false, nil
		}
		return cliEndpointConfig{}, false, err
	}
	var cfg cliEndpointConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cliEndpointConfig{}, true, fmt.Errorf("parse endpoint config: %w", err)
	}
	cfg.normalize()
	return cfg, true, nil
}

func writeEndpointConfig(cfg cliEndpointConfig) error {
	cfg.normalize()
	path, err := endpointConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func removeEndpointConfig() error {
	path, err := endpointConfigPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (c *cliEndpointConfig) normalize() {
	if c == nil {
		return
	}
	c.Addr = strings.TrimSpace(c.Addr)
	c.AccountAddr = strings.TrimSpace(c.AccountAddr)
	c.SliceAddr = strings.TrimSpace(c.SliceAddr)
	c.AdminAddr = strings.TrimSpace(c.AdminAddr)
	c.FileAddr = strings.TrimSpace(c.FileAddr)
	c.FSAddr = strings.TrimSpace(c.FSAddr)
}

func resolveEndpointSettings() (cliEndpointSettings, error) {
	path, err := endpointConfigPath()
	if err != nil {
		return cliEndpointSettings{}, err
	}
	settings := cliEndpointSettings{
		ConfigPath:    path,
		AccountAddr:   defaultGRPCServerAddr,
		SliceAddr:     defaultGRPCServerAddr,
		AdminAddr:     defaultGRPCServerAddr,
		FileAddr:      defaultGRPCServerAddr,
		FSAddr:        defaultGRPCServerAddr,
		AddressSource: "default",
		TLSSource:     "default",
	}

	cfg, present, err := readEndpointConfig()
	if err != nil {
		return cliEndpointSettings{}, err
	}
	settings.ConfigPresent = present
	if present {
		source := "~/.gitslice/config.json"
		if cfg.Addr != "" {
			settings.applySharedAddr(cfg.Addr, source)
		}
		settings.applyServiceAddr("account", cfg.AccountAddr, source)
		settings.applyServiceAddr("slice", cfg.SliceAddr, source)
		settings.applyServiceAddr("admin", cfg.AdminAddr, source)
		settings.applyServiceAddr("file", cfg.FileAddr, source)
		settings.applyServiceAddr("fs", cfg.FSAddr, source)
		if cfg.TLS != nil {
			settings.TLS = *cfg.TLS
			settings.TLSSource = source
		}
	}

	explicit := parsedGlobalFlagNames
	if explicitEndpointFlag(explicit, "addr", *coreServerAddr, "") {
		if addr := strings.TrimSpace(*coreServerAddr); addr != "" {
			settings.applySharedAddr(addr, "--addr")
		}
	}
	if explicitEndpointFlag(explicit, "account-addr", *accountServerAddr, defaultGRPCServerAddr) {
		settings.applyServiceAddr("account", *accountServerAddr, "--account-addr")
	}
	if explicitEndpointFlag(explicit, "slice-addr", *sliceServerAddr, defaultGRPCServerAddr) {
		settings.applyServiceAddr("slice", *sliceServerAddr, "--slice-addr")
	}
	if explicitEndpointFlag(explicit, "admin-addr", *adminServerAddr, defaultGRPCServerAddr) {
		settings.applyServiceAddr("admin", *adminServerAddr, "--admin-addr")
	}
	if explicitEndpointFlag(explicit, "file-addr", *fileServerAddr, defaultGRPCServerAddr) {
		settings.applyServiceAddr("file", *fileServerAddr, "--file-addr")
	}
	if explicitEndpointFlag(explicit, "fs-addr", *fsServerAddr, defaultGRPCServerAddr) {
		settings.applyServiceAddr("fs", *fsServerAddr, "--fs-addr")
	}
	if explicit != nil {
		if explicit["tls"] {
			settings.TLS = *useTLS
			settings.TLSSource = "--tls"
		}
	} else if *useTLS {
		settings.TLS = true
		settings.TLSSource = "--tls"
	}

	return settings, nil
}

func explicitEndpointFlag(explicit map[string]bool, name, value, defaultValue string) bool {
	if explicit != nil {
		return explicit[name]
	}
	value = strings.TrimSpace(value)
	if defaultValue == "" {
		return value != ""
	}
	return value != "" && value != defaultValue
}

func (s *cliEndpointSettings) applySharedAddr(addr, source string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return
	}
	s.CoreAddr = addr
	s.AccountAddr = addr
	s.SliceAddr = addr
	s.AdminAddr = addr
	s.FileAddr = addr
	s.FSAddr = addr
	s.AddressSource = source
}

func (s *cliEndpointSettings) applyServiceAddr(service, addr, source string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return
	}
	switch service {
	case "account":
		s.AccountAddr = addr
	case "slice":
		s.SliceAddr = addr
	case "admin":
		s.AdminAddr = addr
	case "file":
		s.FileAddr = addr
	case "fs":
		s.FSAddr = addr
	default:
		return
	}
	s.CoreAddr = ""
	s.AddressSource = source
}

func (s cliEndpointSettings) sharedAddr() string {
	if s.AccountAddr == "" {
		return ""
	}
	if s.AccountAddr == s.SliceAddr && s.AccountAddr == s.AdminAddr && s.AccountAddr == s.FileAddr && s.AccountAddr == s.FSAddr {
		return s.AccountAddr
	}
	return ""
}

func buildEndpointConfigOutput(status string, settings cliEndpointSettings) jsonEndpointConfigOutput {
	out := jsonEndpointConfigOutput{
		Status:        status,
		ConfigPath:    settings.ConfigPath,
		ConfigPresent: settings.ConfigPresent,
		AccountAddr:   settings.AccountAddr,
		SliceAddr:     settings.SliceAddr,
		AdminAddr:     settings.AdminAddr,
		FileAddr:      settings.FileAddr,
		FSAddr:        settings.FSAddr,
		TLS:           settings.TLS,
		AddressSource: settings.AddressSource,
		TLSSource:     settings.TLSSource,
	}
	if shared := settings.sharedAddr(); shared != "" {
		out.Addr = shared
	}
	return out
}

func collectGlobalFlagNames(args []string) map[string]bool {
	out := make(map[string]bool)
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" || arg == "--" || arg == "-" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			break
		}
		nameValue := strings.TrimLeft(arg, "-")
		name, _, hasValue := strings.Cut(nameValue, "=")
		name = strings.TrimSpace(name)
		if name == "" {
			break
		}
		out[name] = true
		if !hasValue && globalFlagConsumesNextArg(name) && i+1 < len(args) {
			i++
		}
	}
	return out
}

func globalFlagConsumesNextArg(name string) bool {
	switch name {
	case "addr", "account-addr", "slice-addr", "admin-addr", "file-addr", "fs-addr", "api-key", "user":
		return true
	default:
		return false
	}
}

func consumeOptionalBoolFlag(args []string, name string) ([]string, bool, bool, error) {
	flagName := "--" + strings.TrimSpace(name)
	if flagName == "--" {
		return append([]string(nil), args...), false, false, nil
	}

	remaining := make([]string, 0, len(args))
	value := false
	found := false
	for _, arg := range args {
		if arg == flagName {
			value = true
			found = true
			continue
		}
		if strings.HasPrefix(arg, flagName+"=") {
			parsed, err := strconv.ParseBool(strings.TrimPrefix(arg, flagName+"="))
			if err != nil {
				return nil, false, false, fmt.Errorf("invalid value for %s: %s", flagName, strings.TrimPrefix(arg, flagName+"="))
			}
			value = parsed
			found = true
			continue
		}
		remaining = append(remaining, arg)
	}
	return remaining, value, found, nil
}
