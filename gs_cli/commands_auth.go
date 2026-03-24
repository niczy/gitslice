package main

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"path/filepath"
	"strings"

	accountv1 "github.com/niczy/gitslice/proto/account"
)

const (
	authMethodAgentKey = "agent_key"
	authMethodDevice   = "device"
	authMethodUsername = "username"
)

func handleAuthCommand(ctx context.Context, cli *CLI, apiKeyFlag, userFlag string, args []string) {
	args, _ = consumeBoolFlag(args, "json")
	if len(args) == 0 {
		printAuthHelp()
		return
	}

	switch args[0] {
	case "keygen":
		handleAuthKeygen(args[1:])
	case "signup":
		handleAuthSignup(ctx, cli, args[1:])
	case "login":
		handleAuthKeyLogin(ctx, cli, args[1:])
	case "status":
		handleAuthStatus(ctx, cli, apiKeyFlag, userFlag, args[1:])
	case "logout":
		authConfig, err := resolveAuthConfig(apiKeyFlag, userFlag)
		if err != nil {
			commandFatalf("AUTH_RESOLUTION_FAILED", false, "", "Failed to resolve current auth: %v", err)
		}
		handleLogout(ctx, cli, authConfig, args[1:])
	case "keys":
		authConfig, err := resolveAuthConfig(apiKeyFlag, userFlag)
		if err != nil {
			commandFatalf("AUTH_RESOLUTION_FAILED", false, "", "Failed to resolve current auth: %v", err)
		}
		authConfig, err = ensureCLIAuthReady(ctx, cli, authConfig)
		if err != nil {
			commandFatalf("AUTH_REFRESH_FAILED", true, "gs auth login --key <private-key-path>", "Failed to refresh stored auth: %v", err)
		}
		if strings.TrimSpace(authConfig.Authorization) == "" {
			commandFatal("AUTH_REQUIRED", "Authentication required. Run gs auth login --key <private-key-path> or gs login first.", false, "gs auth login --key <private-key-path>")
		}
		handleAuthKeys(withCLIAuth(withCLIDeviceInfo(ctx), authConfig), cli, args[1:])
	default:
		printAuthHelp()
	}
}

func handleAuthKeygen(args []string) {
	fs := newCommandFlagSet("auth keygen")
	outPath := fs.String("out", "", "Path to write the private key")
	parseCommandFlags(fs, args)
	if strings.TrimSpace(*outPath) == "" {
		commandUsage("Usage: gs auth keygen --out <private-key-path>")
		return
	}

	publicKey, _, publicKeyPath, err := generateAgentPrivateKeyFile(*outPath)
	if err != nil {
		commandFatalf("AUTH_KEYGEN_FAILED", false, "", "Failed to generate agent keypair: %v", err)
	}
	fingerprint := agentKeyFingerprint(agentKeyAlgorithmEd25519, publicKey)
	if cliStructuredJSON {
		writeJSONOutput(jsonAuthKeygenOutput{
			Status:         "created",
			Algorithm:      agentKeyAlgorithmEd25519,
			Fingerprint:    fingerprint,
			PrivateKeyPath: *outPath,
			PublicKeyPath:  publicKeyPath,
		})
		return
	}
	fmt.Printf("Generated %s keypair\n", agentKeyAlgorithmEd25519)
	fmt.Printf("Private key: %s\n", *outPath)
	fmt.Printf("Public key:  %s\n", publicKeyPath)
	fmt.Printf("Fingerprint: %s\n", fingerprint)
}

func handleAuthSignup(ctx context.Context, cli *CLI, args []string) {
	fs := newCommandFlagSet("auth signup")
	username := fs.String("username", "", "Username")
	email := fs.String("email", "", "Primary email")
	displayName := fs.String("name", "", "Display name")
	keyPath := fs.String("key", "", "Path to the agent private key PEM")
	keyName := fs.String("key-name", "", "Agent key display name")
	parseCommandFlags(fs, args)
	if strings.TrimSpace(*username) == "" || strings.TrimSpace(*email) == "" || strings.TrimSpace(*keyPath) == "" {
		commandUsage("Usage: gs auth signup --username <username> --email <email> --name <display-name> --key <private-key-path> [--key-name <name>] [--json]")
		return
	}

	_, publicKey, err := loadAgentPrivateKey(*keyPath)
	if err != nil {
		commandFatalf("AUTH_INVALID_KEY", false, "", "Failed to load agent private key: %v", err)
	}
	resolvedKeyName := strings.TrimSpace(*keyName)
	if resolvedKeyName == "" {
		resolvedKeyName = filepath.Base(strings.TrimSpace(*keyPath))
	}

	resp, err := cli.accountClient.SignupWithAgentKey(withCLIDeviceInfo(ctx), &accountv1.SignupWithAgentKeyRequest{
		Username:  strings.TrimSpace(*username),
		Email:     strings.TrimSpace(*email),
		Name:      strings.TrimSpace(*displayName),
		Algorithm: agentKeyAlgorithmEd25519,
		PublicKey: append([]byte(nil), publicKey...),
		KeyName:   resolvedKeyName,
	})
	if err != nil {
		commandFatalf("AUTH_SIGNUP_FAILED", false, "", "Agent key signup failed: %v", err)
	}
	cfg := (credentialsConfig{}).
		refreshedFromAuthResponse(resp).
		withAuthMetadata(authMethodAgentKey, resp.GetSession().GetAgentKeyId(), agentKeyFingerprint(agentKeyAlgorithmEd25519, publicKey))
	if err := writeCredentialsConfig(cfg); err != nil {
		commandFatalf("AUTH_SAVE_FAILED", false, "", "Failed to save credentials: %v", err)
	}
	if cliStructuredJSON {
		writeJSONOutput(buildAuthLoginOutput(resp, "~/.gitslice/credentials.json", authMethodAgentKey, publicKey))
		return
	}
	fmt.Printf("Signed up and logged in as: %s\n", strings.TrimSpace(resp.GetUser().GetUsername()))
}

func handleAuthKeyLogin(ctx context.Context, cli *CLI, args []string) {
	fs := newCommandFlagSet("auth login")
	keyPath := fs.String("key", "", "Path to the agent private key PEM")
	parseCommandFlags(fs, args)
	if strings.TrimSpace(*keyPath) == "" {
		commandUsage("Usage: gs auth login --key <private-key-path> [--json]")
		return
	}

	privateKey, publicKey, err := loadAgentPrivateKey(*keyPath)
	if err != nil {
		commandFatalf("AUTH_INVALID_KEY", false, "", "Failed to load agent private key: %v", err)
	}
	startResp, err := cli.accountClient.StartAgentKeyLogin(withCLIDeviceInfo(ctx), &accountv1.StartAgentKeyLoginRequest{
		PublicKey: append([]byte(nil), publicKey...),
	})
	if err != nil {
		commandFatalf("AUTH_LOGIN_FAILED", true, "", "Failed to start agent key login: %v", err)
	}
	signature := ed25519.Sign(privateKey, startResp.GetChallenge())
	authResp, err := cli.accountClient.CompleteAgentKeyLogin(withCLIDeviceInfo(ctx), &accountv1.CompleteAgentKeyLoginRequest{
		ChallengeId: startResp.GetChallengeId(),
		Signature:   signature,
	})
	if err != nil {
		commandFatalf("AUTH_LOGIN_FAILED", true, "", "Failed to complete agent key login: %v", err)
	}
	cfg := (credentialsConfig{}).
		refreshedFromAuthResponse(authResp).
		withAuthMetadata(authMethodAgentKey, authResp.GetSession().GetAgentKeyId(), agentKeyFingerprint(agentKeyAlgorithmEd25519, publicKey))
	if err := writeCredentialsConfig(cfg); err != nil {
		commandFatalf("AUTH_SAVE_FAILED", false, "", "Failed to save credentials: %v", err)
	}
	if cliStructuredJSON {
		writeJSONOutput(buildAuthLoginOutput(authResp, "~/.gitslice/credentials.json", authMethodAgentKey, publicKey))
		return
	}
	fmt.Printf("Logged in as: %s\n", strings.TrimSpace(authResp.GetUser().GetUsername()))
}

func handleAuthStatus(ctx context.Context, cli *CLI, apiKeyFlag, userFlag string, args []string) {
	if len(args) != 0 {
		commandUsage("Usage: gs auth status [--json]")
		return
	}

	authConfig, err := resolveAuthConfig(apiKeyFlag, userFlag)
	if err != nil {
		commandFatalf("AUTH_RESOLUTION_FAILED", false, "", "Failed to resolve current auth: %v", err)
	}
	if strings.TrimSpace(authConfig.Authorization) == "" && !authConfig.CredentialStore {
		showCurrentAuth(authConfig)
		return
	}
	authConfig, err = ensureCLIAuthReady(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("AUTH_REFRESH_FAILED", true, "gs auth login --key <private-key-path>", "Failed to resolve current login: %v", err)
	}
	showCurrentAuth(authConfig)
}

func handleAuthKeys(ctx context.Context, cli *CLI, args []string) {
	if len(args) == 0 {
		printAuthKeysHelp()
		return
	}
	switch args[0] {
	case "list":
		handleAuthKeysList(ctx, cli, args[1:])
	case "add":
		handleAuthKeysAdd(ctx, cli, args[1:])
	case "revoke":
		handleAuthKeysRevoke(ctx, cli, args[1:])
	default:
		printAuthKeysHelp()
	}
}

func handleAuthKeysList(ctx context.Context, cli *CLI, args []string) {
	if len(args) != 0 {
		commandUsage("Usage: gs auth keys list [--json]")
		return
	}
	resp, err := cli.accountClient.ListAgentKeys(ctx, &accountv1.ListAgentKeysRequest{})
	if err != nil {
		commandFatalf("AUTH_KEYS_LIST_FAILED", true, "", "Failed to list agent keys: %v", err)
	}
	if cliStructuredJSON {
		writeJSONOutput(buildAuthKeysListOutput(resp))
		return
	}
	if len(resp.GetKeys()) == 0 {
		fmt.Println("No agent keys.")
		return
	}
	for _, key := range resp.GetKeys() {
		state := "active"
		if key.GetRevoked() {
			state = "revoked"
		}
		fmt.Printf("%s  %s  %s  %s\n", key.GetId(), key.GetName(), key.GetFingerprint(), state)
	}
}

func handleAuthKeysAdd(ctx context.Context, cli *CLI, args []string) {
	fs := newCommandFlagSet("auth keys add")
	name := fs.String("name", "", "Display name for the key")
	publicKeyPath := fs.String("public-key", "", "Path to the public key PEM")
	parseCommandFlags(fs, args)
	if strings.TrimSpace(*name) == "" || strings.TrimSpace(*publicKeyPath) == "" {
		commandUsage("Usage: gs auth keys add --name <name> --public-key <path> [--json]")
		return
	}

	publicKey, err := loadAgentPublicKey(*publicKeyPath)
	if err != nil {
		commandFatalf("AUTH_INVALID_KEY", false, "", "Failed to load agent public key: %v", err)
	}
	key, err := cli.accountClient.CreateAgentKey(ctx, &accountv1.CreateAgentKeyRequest{
		Name:      strings.TrimSpace(*name),
		Algorithm: agentKeyAlgorithmEd25519,
		PublicKey: append([]byte(nil), publicKey...),
	})
	if err != nil {
		commandFatalf("AUTH_KEYS_ADD_FAILED", false, "", "Failed to add agent key: %v", err)
	}
	if cliStructuredJSON {
		writeJSONOutput(buildAuthKeyOutput(key))
		return
	}
	fmt.Printf("Added agent key: %s (%s)\n", key.GetName(), key.GetId())
}

func handleAuthKeysRevoke(ctx context.Context, cli *CLI, args []string) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		commandUsage("Usage: gs auth keys revoke <key-id> [--json]")
		return
	}
	keyID := strings.TrimSpace(args[0])
	if _, err := cli.accountClient.DeleteAgentKey(ctx, &accountv1.DeleteAgentKeyRequest{KeyId: keyID}); err != nil {
		commandFatalf("AUTH_KEYS_REVOKE_FAILED", false, "", "Failed to revoke agent key: %v", err)
	}
	if cliStructuredJSON {
		writeJSONOutput(jsonAuthKeyRevokeOutput{KeyID: keyID, Status: "revoked"})
		return
	}
	fmt.Printf("Revoked agent key: %s\n", keyID)
}
