package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	accountv1 "github.com/niczy/gitslice/proto/account"
	"google.golang.org/grpc/metadata"
)

func handleLogin(ctx context.Context, cli *CLI, currentAuth cliAuth, args []string) {
	if len(args) == 1 && strings.TrimSpace(args[0]) == "status" {
		showCurrentAuth(currentAuth)
		return
	}
	if len(args) == 0 {
		if strings.TrimSpace(currentAuth.Authorization) != "" || currentAuth.CredentialStore {
			authConfig, err := ensureCLIAuthReady(ctx, cli, currentAuth)
			if err != nil {
				log.Fatalf("Failed to resolve current login: %v", err)
			}
			showCurrentAuth(authConfig)
			return
		}
		startDeviceLogin(ctx, cli)
		return
	}
	if len(args) != 1 {
		log.Fatal("Usage: gs login [status|<username>]")
	}

	username := strings.TrimSpace(args[0])
	if username == "" {
		log.Fatal("Usage: gs login [status|<username>]")
	}

	resp, err := cli.accountClient.Login(withCLIDeviceInfo(ctx), &accountv1.LoginRequest{Username: username})
	if err != nil {
		log.Fatalf("Login failed: %v", err)
	}

	if err := writeCredentialsConfig((credentialsConfig{}).refreshedFromAuthResponse(resp)); err != nil {
		log.Fatalf("Failed to save login: %v", err)
	}

	fmt.Printf("Logged in as: %s\n", strings.TrimSpace(resp.GetUser().GetUsername()))
}

func handleLogout(ctx context.Context, cli *CLI, currentAuth cliAuth, args []string) {
	if len(args) != 0 {
		log.Fatal("Usage: gs logout")
	}
	if strings.TrimSpace(currentAuth.Authorization) == "" && !currentAuth.CredentialStore {
		if cliStructuredJSON {
			writeJSONOutput(jsonAuthLogoutOutput{Status: "not_logged_in"})
			return
		}
		fmt.Println("Not logged in.")
		return
	}

	switch currentAuth.Source {
	case "~/.gitslice/credentials.json":
		authConfig, err := ensureCLIAuthReady(ctx, cli, currentAuth)
		if err != nil {
			log.Printf("Warning: failed to refresh stored login before logout: %v", err)
			authConfig = currentAuth
		}
		logoutCtx := withCLIDeviceInfo(withCLIAuth(ctx, authConfig))
		if _, err := cli.accountClient.Logout(logoutCtx, &accountv1.LogoutRequest{}); err != nil {
			log.Printf("Warning: failed to revoke session: %v", err)
		}
		if err := removeCredentialsConfig(); err != nil {
			log.Fatalf("Failed to remove stored credentials: %v", err)
		}
		if err := removeUsernameConfig(); err != nil {
			log.Fatalf("Failed to clear legacy username config: %v", err)
		}
		if cliStructuredJSON {
			writeJSONOutput(jsonAuthLogoutOutput{Status: "logged_out"})
			return
		}
		fmt.Println("Logged out.")
	case "legacy username":
		if err := removeUsernameConfig(); err != nil {
			log.Fatalf("Failed to clear legacy username config: %v", err)
		}
		if cliStructuredJSON {
			writeJSONOutput(jsonAuthLogoutOutput{Status: "logged_out"})
			return
		}
		fmt.Println("Logged out.")
	default:
		if cliStructuredJSON {
			writeJSONOutput(jsonAuthLogoutOutput{Status: "external_auth", Source: currentAuth.Source})
			return
		}
		fmt.Printf("Auth comes from %s; clear it manually.\n", currentAuth.Source)
	}
}

func showCurrentAuth(currentAuth cliAuth) {
	if strings.TrimSpace(currentAuth.Authorization) == "" {
		if cliStructuredJSON {
			writeJSONOutput(buildAuthStatusOutput(currentAuth, credentialsConfig{}))
			return
		}
		fmt.Println("Not logged in.")
		return
	}
	cfg := credentialsConfig{}
	if currentAuth.CredentialStore {
		if loaded, err := readCredentialsConfig(); err == nil {
			cfg = loaded
		}
	}
	if cliStructuredJSON {
		writeJSONOutput(buildAuthStatusOutput(currentAuth, cfg))
		return
	}
	if strings.TrimSpace(currentAuth.Username) != "" {
		fmt.Printf("Logged in as: %s\n", strings.TrimSpace(currentAuth.Username))
		return
	}
	fmt.Printf("Authenticated via %s\n", strings.TrimSpace(currentAuth.Source))
}

func startDeviceLogin(ctx context.Context, cli *CLI) {
	startResp, err := cli.accountClient.StartDeviceAuthorization(withCLIDeviceInfo(ctx), &accountv1.StartDeviceAuthorizationRequest{})
	if err != nil {
		log.Fatalf("Device login failed: %v", err)
	}

	verificationURI := strings.TrimSpace(startResp.GetVerificationUri())
	verificationURIComplete := strings.TrimSpace(startResp.GetVerificationUriComplete())
	if verificationURIComplete == "" {
		verificationURIComplete = verificationURI
	}
	if tryOpenBrowser(verificationURIComplete) {
		fmt.Printf("Opening browser to %s\n", verificationURI)
	} else {
		fmt.Printf("Visit: %s\n", verificationURIComplete)
	}
	fmt.Printf("If browser doesn't open, visit: %s\n", verificationURI)
	fmt.Printf("Enter code: %s\n", strings.TrimSpace(startResp.GetUserCode()))
	fmt.Print("Waiting for authorization...")

	pollInterval := time.Duration(startResp.GetPollIntervalSeconds()) * time.Second
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	deadline := time.Now().Add(10 * time.Minute)
	if expiresAtRaw := strings.TrimSpace(startResp.GetExpiresAt()); expiresAtRaw != "" {
		if expiresAt, err := time.Parse(time.RFC3339, expiresAtRaw); err == nil {
			deadline = expiresAt
		}
	}

	for {
		pollResp, err := cli.accountClient.PollDeviceAuthorization(withCLIDeviceInfo(ctx), &accountv1.PollDeviceAuthorizationRequest{
			DeviceCode: startResp.GetDeviceCode(),
		})
		if err != nil {
			log.Fatalf("\nDevice login failed while polling: %v", err)
		}
		switch pollResp.GetStatus() {
		case accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_APPROVED:
			fmt.Println(" done")
			cfg := (credentialsConfig{}).refreshedFromAuthResponse(pollResp.GetAuth())
			if err := writeCredentialsConfig(cfg); err != nil {
				log.Fatalf("Failed to save login: %v", err)
			}
			fmt.Printf("Logged in as %s (stored in ~/.gitslice/credentials.json)\n", strings.TrimSpace(cfg.Username))
			return
		case accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_DENIED:
			log.Fatal("\nDevice login was denied")
		case accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_EXPIRED:
			log.Fatal("\nDevice login expired")
		}
		if time.Now().After(deadline) {
			log.Fatal("\nDevice login expired")
		}
		time.Sleep(pollInterval)
	}
}

func withCLIDeviceInfo(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "x-device-info", fmt.Sprintf("gs-cli (%s/%s)", runtime.GOOS, runtime.GOARCH))
}

func tryOpenBrowser(target string) bool {
	if strings.TrimSpace(target) == "" || strings.TrimSpace(os.Getenv("GS_NO_BROWSER")) != "" {
		return false
	}
	if customCommand := strings.TrimSpace(os.Getenv("GS_BROWSER_COMMAND")); customCommand != "" {
		return exec.Command(customCommand, target).Start() == nil
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start() == nil
}
