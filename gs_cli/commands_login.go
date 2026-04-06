package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	accountv1 "github.com/niczy/gitslice/proto/account"
	"google.golang.org/grpc/metadata"
)

func handleLogin(ctx context.Context, cli *CLI, currentAuth cliAuth, args []string) {
	args, _ = consumeBoolFlag(args, "json")
	if len(args) == 1 && strings.TrimSpace(args[0]) == "status" {
		showCurrentAuth(currentAuth)
		return
	}
	if len(args) == 0 {
		if strings.TrimSpace(currentAuth.Authorization) != "" || currentAuth.CredentialStore {
			authConfig, err := ensureCLIAuthReady(ctx, cli, currentAuth)
			if err != nil {
				commandFatalf("AUTH_REFRESH_FAILED", true, "", "Failed to resolve current login: %v", err)
			}
			showCurrentAuth(authConfig)
			return
		}
		if cliStructuredJSON || cliNonInteractive {
			commandFatal("INTERACTIVE_REQUIRED", "Device login uses an interactive browser sign-in. Run gs auth login --key <private-key-path> for agent-friendly auth.", false, "gs auth login --key <private-key-path>")
		}
		startDeviceLogin(ctx, cli)
		return
	}
	if len(args) != 1 {
		commandFatal("INVALID_ARGUMENT", "Usage: gs login [status|<username>]", false, "")
	}

	username := strings.TrimSpace(args[0])
	if username == "" {
		commandFatal("INVALID_ARGUMENT", "Usage: gs login [status|<username>]", false, "")
	}

	resp, err := cli.accountClient.Login(withCLIDeviceInfo(ctx), &accountv1.LoginRequest{Username: username})
	if err != nil {
		commandFatalf("AUTH_LOGIN_FAILED", true, "", "Login failed: %v", err)
	}

	cfg := (credentialsConfig{}).
		refreshedFromAuthResponse(resp).
		withAuthMetadata(authMethodUsername, "", "")
	if err := writeCredentialsConfig(cfg); err != nil {
		commandFatalf("AUTH_SAVE_FAILED", false, "", "Failed to save login: %v", err)
	}
	if cliStructuredJSON {
		writeJSONOutput(buildAuthLoginOutput(resp, "~/.gitslice/credentials.json", authMethodUsername, nil))
		return
	}

	fmt.Printf("Logged in as: %s\n", strings.TrimSpace(resp.GetUser().GetUsername()))
}

func handleLogout(ctx context.Context, cli *CLI, currentAuth cliAuth, args []string) {
	args, _ = consumeBoolFlag(args, "json")
	if len(args) != 0 {
		commandFatal("INVALID_ARGUMENT", "Usage: gs logout", false, "")
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
			fmt.Fprintf(os.Stderr, "Warning: failed to refresh stored login before logout: %v\n", err)
			authConfig = currentAuth
		}
		logoutCtx := withCLIDeviceInfo(withCLIAuth(ctx, authConfig))
		if _, err := cli.accountClient.Logout(logoutCtx, &accountv1.LogoutRequest{}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to revoke session: %v\n", err)
		}
		if err := removeCredentialsConfig(); err != nil {
			commandFatalf("AUTH_LOGOUT_FAILED", false, "", "Failed to remove stored credentials: %v", err)
		}
		if err := removeUsernameConfig(); err != nil {
			commandFatalf("AUTH_LOGOUT_FAILED", false, "", "Failed to clear legacy username config: %v", err)
		}
		if cliStructuredJSON {
			writeJSONOutput(jsonAuthLogoutOutput{Status: "logged_out"})
			return
		}
		fmt.Println("Logged out.")
	case "legacy username":
		if err := removeUsernameConfig(); err != nil {
			commandFatalf("AUTH_LOGOUT_FAILED", false, "", "Failed to clear legacy username config: %v", err)
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
		if method := strings.TrimSpace(cfg.AuthMethod); method != "" {
			fmt.Printf("Auth method: %s\n", method)
		}
		if agentKeyID := strings.TrimSpace(cfg.AgentKeyID); agentKeyID != "" {
			fmt.Printf("Agent key: %s\n", agentKeyID)
		}
		return
	}
	fmt.Printf("Authenticated via %s\n", strings.TrimSpace(currentAuth.Source))
}

func startDeviceLogin(ctx context.Context, cli *CLI) {
	if cliNonInteractive {
		commandFatal("INTERACTIVE_REQUIRED", "Device login is interactive. Run gs auth login --key <private-key-path> for agent-friendly auth.", false, "gs auth login --key <private-key-path>")
	}
	startResp, err := cli.accountClient.StartDeviceAuthorization(withCLIDeviceInfo(ctx), &accountv1.StartDeviceAuthorizationRequest{})
	if err != nil {
		commandFatalf("AUTH_LOGIN_FAILED", true, "", "Device login failed: %v", err)
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
	fmt.Print("Waiting for browser approval...")

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
			commandFatalf("AUTH_LOGIN_FAILED", true, "", "Device login failed while polling: %v", err)
		}
		switch pollResp.GetStatus() {
		case accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_APPROVED:
			fmt.Println(" done")
			cfg := (credentialsConfig{}).
				refreshedFromAuthResponse(pollResp.GetAuth()).
				withAuthMetadata(authMethodDevice, "", "")
			if err := writeCredentialsConfig(cfg); err != nil {
				commandFatalf("AUTH_SAVE_FAILED", false, "", "Failed to save login: %v", err)
			}
			fmt.Printf("Logged in as %s (stored in ~/.gitslice/credentials.json)\n", strings.TrimSpace(cfg.Username))
			return
		case accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_DENIED:
			commandFatal("AUTH_LOGIN_FAILED", "Device login was denied.", false, "")
		case accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_EXPIRED:
			commandFatal("AUTH_LOGIN_FAILED", "Device login expired.", false, "")
		}
		if time.Now().After(deadline) {
			commandFatal("AUTH_LOGIN_FAILED", "Device login expired.", false, "")
		}
		time.Sleep(pollInterval)
	}
}

func withCLIDeviceInfo(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "x-device-info", fmt.Sprintf("gs-cli (%s/%s)", runtime.GOOS, runtime.GOARCH))
}

func tryOpenBrowser(target string) bool {
	if cliNonInteractive || strings.TrimSpace(target) == "" || strings.TrimSpace(os.Getenv("GS_NO_BROWSER")) != "" {
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
