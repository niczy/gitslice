package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niczy/gitslice/internal/auth"
	"google.golang.org/grpc/metadata"
)

func userConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gitslice", "user"), nil
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

func withUserAuth(ctx context.Context, username string) (context.Context, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return ctx, nil
	}
	if !auth.ValidateUsername(username) {
		return nil, fmt.Errorf("invalid username %q", username)
	}
	// Auth is "fake": the server trusts Authorization: User <username>.
	return metadata.AppendToOutgoingContext(ctx, "authorization", "User "+username), nil
}
