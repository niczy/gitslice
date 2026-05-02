package gscli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newGitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "git <command> [options]",
		Short:              "Git integration helpers",
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			if isHelpRequest(args) {
				_ = cmd.Help()
				return
			}
			runGitCommand(args)
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printGitHelp()
	})
	return cmd
}

func runGitCommand(args []string) {
	args = configureCLIBehavior(args)
	configureCLIOutputMode(args)

	if len(args) == 0 {
		printGitHelp()
		return
	}
	switch args[0] {
	case "credential":
		handleGitCredentialCommand(args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown git command: %s", args[0]), false, "gs git --help")
	}
}

func handleGitCredentialCommand(args []string) {
	if isHelpRequest(args) {
		printGitHelp()
		return
	}
	operation := "get"
	if len(args) > 0 {
		operation = strings.TrimSpace(args[0])
	}
	switch operation {
	case "", "get":
	case "store", "erase":
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown git credential operation: %s", operation), false, "gs git credential get")
	}

	input, err := readGitCredentialInput(os.Stdin)
	if err != nil {
		commandFatalf("GIT_CREDENTIAL_FAILED", false, "", "Failed to read credential request: %v", err)
	}
	_ = input

	cli, err := newCLIFromFlags()
	if err != nil {
		commandFatalf("CLI_INIT_FAILED", true, "", "Failed to initialize CLI: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	authConfig, err := resolveAuthConfig(*apiKeyFlag, *userFlag)
	if err != nil {
		commandFatalf("AUTH_RESOLUTION_FAILED", false, "", "Failed to resolve auth: %v", err)
	}
	authConfig, err = ensureCLIAuthReady(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("AUTH_REFRESH_FAILED", true, "gs auth login --key <private-key-path> or gs login", "Failed to refresh stored auth: %v", err)
	}
	if err := writeGitCredential(os.Stdout, authConfig); err != nil {
		commandFatalf("GIT_CREDENTIAL_FAILED", false, "", "Failed to write credential response: %v", err)
	}
}

func readGitCredentialInput(r io.Reader) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func writeGitCredential(w io.Writer, authConfig cliAuth) error {
	token := bearerTokenFromAuthorization(authConfig.Authorization)
	if token == "" {
		return fmt.Errorf("Git credential helper requires bearer auth; run gs login, gs auth login --key <private-key-path>, or set GS_API_KEY")
	}
	username := strings.TrimSpace(authConfig.Username)
	if username == "" {
		username = "gitslice"
	}
	if _, err := fmt.Fprintf(w, "username=%s\npassword=%s\n\n", username, token); err != nil {
		return err
	}
	return nil
}

func bearerTokenFromAuthorization(authorization string) string {
	authorization = strings.TrimSpace(authorization)
	if !strings.HasPrefix(authorization, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
}
