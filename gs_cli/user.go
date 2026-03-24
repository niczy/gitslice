package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	accountv1 "github.com/niczy/gitslice/proto/account"
	"google.golang.org/grpc/metadata"
)

type cliAuth struct {
	Authorization   string
	Username        string
	Source          string
	CredentialStore bool
}

type credentialsConfig struct {
	AccessToken                string `json:"access_token,omitempty"`
	AccessTokenCamel           string `json:"accessToken,omitempty"`
	RefreshToken               string `json:"refresh_token,omitempty"`
	RefreshTokenCamel          string `json:"refreshToken,omitempty"`
	AccessTokenExpiresAt       string `json:"access_token_expires_at,omitempty"`
	AccessTokenExpiresAtCamel  string `json:"accessTokenExpiresAt,omitempty"`
	RefreshTokenExpiresAt      string `json:"refresh_token_expires_at,omitempty"`
	RefreshTokenExpiresAtCamel string `json:"refreshTokenExpiresAt,omitempty"`
	TokenType                  string `json:"token_type,omitempty"`
	TokenTypeCamel             string `json:"tokenType,omitempty"`
	Username                   string `json:"username,omitempty"`
	SessionID                  string `json:"session_id,omitempty"`
	SessionIDCamel             string `json:"sessionId,omitempty"`
	AuthMethod                 string `json:"auth_method,omitempty"`
	AgentKeyID                 string `json:"agent_key_id,omitempty"`
	KeyFingerprint             string `json:"key_fingerprint,omitempty"`
}

const accessTokenRefreshSkew = 30 * time.Second

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

func removeUsernameConfig() error {
	path, err := userConfigPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
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

func writeCredentialsConfig(cfg credentialsConfig) error {
	path, err := credentialsConfigPath()
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

func removeCredentialsConfig() error {
	path, err := credentialsConfigPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (c credentialsConfig) accessToken() string {
	if token := strings.TrimSpace(c.AccessToken); token != "" {
		return token
	}
	return strings.TrimSpace(c.AccessTokenCamel)
}

func (c credentialsConfig) refreshToken() string {
	if token := strings.TrimSpace(c.RefreshToken); token != "" {
		return token
	}
	return strings.TrimSpace(c.RefreshTokenCamel)
}

func (c credentialsConfig) accessTokenExpiresAtValue() string {
	if value := strings.TrimSpace(c.AccessTokenExpiresAt); value != "" {
		return value
	}
	return strings.TrimSpace(c.AccessTokenExpiresAtCamel)
}

func (c credentialsConfig) refreshTokenExpiresAtValue() string {
	if value := strings.TrimSpace(c.RefreshTokenExpiresAt); value != "" {
		return value
	}
	return strings.TrimSpace(c.RefreshTokenExpiresAtCamel)
}

func (c credentialsConfig) parseOptionalTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("parse credentials time %q: %w", raw, err)
	}
	return &parsed, nil
}

func (c credentialsConfig) accessTokenExpiresAtTime() (*time.Time, error) {
	return c.parseOptionalTime(c.accessTokenExpiresAtValue())
}

func (c credentialsConfig) refreshTokenExpiresAtTime() (*time.Time, error) {
	return c.parseOptionalTime(c.refreshTokenExpiresAtValue())
}

func (c credentialsConfig) needsRefresh(now time.Time) (bool, error) {
	accessToken := c.accessToken()
	if accessToken == "" {
		return strings.TrimSpace(c.refreshToken()) != "", nil
	}
	expiresAt, err := c.accessTokenExpiresAtTime()
	if err != nil {
		return false, err
	}
	if expiresAt == nil {
		return false, nil
	}
	return !expiresAt.After(now.Add(accessTokenRefreshSkew)), nil
}

func (c credentialsConfig) refreshedFromAuthResponse(resp *accountv1.AuthResponse) credentialsConfig {
	if resp == nil {
		return c
	}
	refreshed := credentialsConfig{
		AccessToken:           strings.TrimSpace(resp.GetAccessToken()),
		RefreshToken:          strings.TrimSpace(resp.GetRefreshToken()),
		AccessTokenExpiresAt:  strings.TrimSpace(resp.GetAccessTokenExpiresAt()),
		RefreshTokenExpiresAt: strings.TrimSpace(resp.GetRefreshTokenExpiresAt()),
		TokenType:             strings.TrimSpace(resp.GetTokenType()),
		Username:              strings.TrimSpace(resp.GetUser().GetUsername()),
		SessionID:             strings.TrimSpace(resp.GetSession().GetId()),
		AgentKeyID:            strings.TrimSpace(resp.GetSession().GetAgentKeyId()),
	}
	if refreshed.AccessToken == "" {
		refreshed.AccessToken = c.accessToken()
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = c.refreshToken()
	}
	if refreshed.AccessTokenExpiresAt == "" {
		refreshed.AccessTokenExpiresAt = c.accessTokenExpiresAtValue()
	}
	if refreshed.RefreshTokenExpiresAt == "" {
		refreshed.RefreshTokenExpiresAt = c.refreshTokenExpiresAtValue()
	}
	if refreshed.TokenType == "" {
		refreshed.TokenType = strings.TrimSpace(c.TokenType)
		if refreshed.TokenType == "" {
			refreshed.TokenType = strings.TrimSpace(c.TokenTypeCamel)
		}
	}
	if refreshed.Username == "" {
		refreshed.Username = strings.TrimSpace(c.Username)
	}
	if refreshed.SessionID == "" {
		refreshed.SessionID = strings.TrimSpace(c.SessionID)
		if refreshed.SessionID == "" {
			refreshed.SessionID = strings.TrimSpace(c.SessionIDCamel)
		}
	}
	if refreshed.AuthMethod == "" {
		refreshed.AuthMethod = strings.TrimSpace(c.AuthMethod)
	}
	if refreshed.AgentKeyID == "" {
		refreshed.AgentKeyID = strings.TrimSpace(c.AgentKeyID)
	}
	if refreshed.KeyFingerprint == "" {
		refreshed.KeyFingerprint = strings.TrimSpace(c.KeyFingerprint)
	}
	return refreshed
}

func (c credentialsConfig) withAuthMetadata(method, agentKeyID, keyFingerprint string) credentialsConfig {
	c.AuthMethod = strings.TrimSpace(method)
	c.AgentKeyID = strings.TrimSpace(agentKeyID)
	c.KeyFingerprint = strings.TrimSpace(keyFingerprint)
	return c
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
		if token := cfg.accessToken(); token != "" || cfg.refreshToken() != "" {
			authorization := ""
			if token != "" {
				authorization = "Bearer " + token
			}
			return cliAuth{
				Authorization:   authorization,
				Username:        strings.TrimSpace(cfg.Username),
				Source:          "~/.gitslice/credentials.json",
				CredentialStore: true,
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

func ensureCLIAuthReady(ctx context.Context, cli *CLI, authConfig cliAuth) (cliAuth, error) {
	if !authConfig.CredentialStore {
		return authConfig, nil
	}

	cfg, err := readCredentialsConfig()
	if err != nil {
		return cliAuth{}, err
	}
	needsRefresh, err := cfg.needsRefresh(time.Now())
	if err != nil {
		return cliAuth{}, err
	}
	if !needsRefresh {
		token := strings.TrimSpace(cfg.accessToken())
		if token == "" {
			return cliAuth{}, errors.New("stored credentials are missing an access token")
		}
		return cliAuth{
			Authorization:   "Bearer " + token,
			Username:        strings.TrimSpace(cfg.Username),
			Source:          "~/.gitslice/credentials.json",
			CredentialStore: true,
		}, nil
	}

	refreshToken := strings.TrimSpace(cfg.refreshToken())
	if refreshToken == "" {
		return cliAuth{}, errors.New("stored login expired and cannot be refreshed; run gs login again")
	}
	refreshExpiresAt, err := cfg.refreshTokenExpiresAtTime()
	if err != nil {
		return cliAuth{}, err
	}
	if refreshExpiresAt != nil && !refreshExpiresAt.After(time.Now()) {
		return cliAuth{}, errors.New("stored login expired; run gs login again")
	}

	resp, err := cli.accountClient.RefreshAccessToken(ctx, &accountv1.RefreshAccessTokenRequest{RefreshToken: refreshToken})
	if err != nil {
		return cliAuth{}, fmt.Errorf("refresh access token: %w", err)
	}
	cfg = cfg.refreshedFromAuthResponse(resp)
	if err := writeCredentialsConfig(cfg); err != nil {
		return cliAuth{}, fmt.Errorf("write refreshed credentials: %w", err)
	}
	return cliAuth{
		Authorization:   "Bearer " + cfg.accessToken(),
		Username:        strings.TrimSpace(cfg.Username),
		Source:          "~/.gitslice/credentials.json",
		CredentialStore: true,
	}, nil
}
