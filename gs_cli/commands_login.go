package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	accountv1 "github.com/niczy/gitslice/proto/account"
)

func handleLogin(ctx context.Context, cli *CLI, currentAuth cliAuth, args []string) {
	if len(args) == 0 {
		if strings.TrimSpace(currentAuth.Authorization) == "" {
			fmt.Println("Not logged in. Usage: gs login <username>")
			return
		}
		if strings.TrimSpace(currentAuth.Username) != "" {
			fmt.Printf("Logged in as: %s\n", strings.TrimSpace(currentAuth.Username))
			return
		}
		fmt.Printf("Authenticated via %s\n", strings.TrimSpace(currentAuth.Source))
		return
	}

	username := strings.TrimSpace(args[0])
	if username == "" {
		log.Fatal("Usage: gs login <username>")
	}

	resp, err := cli.accountClient.Login(ctx, &accountv1.LoginRequest{Username: username})
	if err != nil {
		log.Fatalf("Login failed: %v", err)
	}

	if err := writeCredentialsConfig(credentialsConfig{
		AccessToken: resp.GetAccessToken(),
		Username:    resp.GetUser().GetUsername(),
		SessionID:   resp.GetSession().GetId(),
	}); err != nil {
		log.Fatalf("Failed to save login: %v", err)
	}

	fmt.Printf("Logged in as: %s\n", strings.TrimSpace(resp.GetUser().GetUsername()))
}

func handleLogout(ctx context.Context, cli *CLI, currentAuth cliAuth, args []string) {
	if len(args) != 0 {
		log.Fatal("Usage: gs logout")
	}
	if strings.TrimSpace(currentAuth.Authorization) == "" {
		fmt.Println("Not logged in.")
		return
	}

	switch currentAuth.Source {
	case "~/.gitslice/credentials.json":
		logoutCtx := withCLIAuth(ctx, currentAuth)
		if _, err := cli.accountClient.Logout(logoutCtx, &accountv1.LogoutRequest{}); err != nil {
			log.Printf("Warning: failed to revoke session: %v", err)
		}
		if err := removeCredentialsConfig(); err != nil {
			log.Fatalf("Failed to remove stored credentials: %v", err)
		}
		if err := removeUsernameConfig(); err != nil {
			log.Fatalf("Failed to clear legacy username config: %v", err)
		}
		fmt.Println("Logged out.")
	case "legacy username":
		if err := removeUsernameConfig(); err != nil {
			log.Fatalf("Failed to clear legacy username config: %v", err)
		}
		fmt.Println("Logged out.")
	default:
		fmt.Printf("Auth comes from %s; clear it manually.\n", currentAuth.Source)
	}
}
