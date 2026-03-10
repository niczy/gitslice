package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niczy/gitslice/internal/auth"
	"google.golang.org/grpc/metadata"
)

type cliAuth struct {
	Authorization string
	Username      string
	Source        string
}

type credentialsConfig struct {
	AccessToken      string `json:"access_token"`
	AccessTokenCamel string `json:"accessToken"`
}

func gitsliceConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gitslice"), nil
}

func userConfigPath() (string, error) {
	configDir, err := gitsliceConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "user"), nil
}

func credentialsConfigPath() (string, error) {
	configDir, err := gitsliceConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "credentials.json"), nil
}

func readUsernameConfig() (string, error) {
	path, err := userConfigPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func writeUsernameConfig(username string) error {
	path, err := userConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(username+"\n"), 0o600)
}

func readCredentialsConfig() (credentialsConfig, error) {
	path, err := credentialsConfigPath()
	if err != nil {
		return credentialsConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return credentialsConfig{}, err
	}

	var cfg credentialsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return credentialsConfig{}, fmt.Errorf("parse credentials config: %w", err)
	}
	return cfg, nil
}

func (c credentialsConfig) accessToken() string {
	if token := strings.TrimSpace(c.AccessToken); token != "" {
		return token
	}
	return strings.TrimSpace(c.AccessTokenCamel)
}

func resolveUsername(flagValue string) string {
	if u := strings.TrimSpace(flagValue); u != "" {
		return u
	}
	if u := strings.TrimSpace(os.Getenv("GS_USERNAME")); u != "" {
		return u
	}
	if u, err := readUsernameConfig(); err == nil && strings.TrimSpace(u) != "" {
		return strings.TrimSpace(u)
	}
	return ""
}

func resolveAuthConfig(apiKeyFlag, userFlag string) (cliAuth, error) {
	if token := strings.TrimSpace(apiKeyFlag); token != "" {
		return cliAuth{
			Authorization: "Bearer " + token,
			Source:        "--api-key",
		}, nil
	}
	if token := strings.TrimSpace(os.Getenv("GS_API_KEY")); token != "" {
		return cliAuth{
			Authorization: "Bearer " + token,
			Source:        "GS_API_KEY",
		}, nil
	}
	if cfg, err := readCredentialsConfig(); err == nil {
		if token := cfg.accessToken(); token != "" {
			return cliAuth{
				Authorization: "Bearer " + token,
				Source:        "~/.gitslice/credentials.json",
			}, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return cliAuth{}, err
	}

	username := resolveUsername(userFlag)
	username = strings.TrimSpace(username)
	if username == "" {
		return cliAuth{}, nil
	}
	if !auth.ValidateUsername(username) {
		return cliAuth{}, fmt.Errorf("invalid username %q", username)
	}
	return cliAuth{
		Authorization: "User " + username,
		Username:      username,
		Source:        "legacy username",
	}, nil
}

func withCLIAuth(ctx context.Context, authConfig cliAuth) context.Context {
	if strings.TrimSpace(authConfig.Authorization) == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", authConfig.Authorization)
}
