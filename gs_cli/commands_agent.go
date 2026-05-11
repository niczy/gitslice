package gscli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	agentv1 "github.com/niczy/gitslice/proto/agent"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

func newAgentCommand() *cobra.Command {
	cmd := newAuthenticatedCobraCommand("agent <command> [options]", "Start and manage local agent sessions", 24*time.Hour,
		func(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
			handleAgentCommand(ctx, cli, authConfig, args)
		})
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printAgentHelp()
	})
	return cmd
}

func handleAgentCommand(ctx context.Context, cli *CLI, authCfg cliAuth, args []string) {
	if len(args) < 1 {
		printAgentHelp()
		return
	}
	switch args[0] {
	case "start":
		handleAgentStart(ctx, cli, authCfg, args[1:])
	case "stop":
		handleAgentStop(ctx, cli, authCfg, args[1:])
	case "status":
		handleAgentStatus(ctx, cli, args[1:])
	default:
		printAgentHelp()
	}
}

func handleAgentStart(ctx context.Context, cli *CLI, authCfg cliAuth, args []string) {
	sliceRef := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--slice":
			if i+1 < len(args) {
				sliceRef = args[i+1]
				i++
			}
		}
	}
	if sliceRef == "" {
		fmt.Println("Usage: gs agent start --slice <slice-id-or-slug>")
		return
	}

	agentClient := agentv1.NewAgentServiceClient(cli.agentGRPCConn())
	resp, err := agentClient.CreateSession(ctx, &agentv1.CreateSessionRequest{
		SliceId:   sliceRef,
		AgentType: "codex",
		Provider:  "local",
	})
	if err != nil {
		commandFatal("AGENT_START_FAILED", fmt.Sprintf("Failed to create agent session: %v", err), false, "")
		return
	}

	fmt.Printf("Agent session started: %s\n", resp.SessionId)
	fmt.Printf("Slice: %s\n", resp.SliceId)
	fmt.Printf("State: %s\n", resp.State)
	if resp.Ws != nil {
		wsURL := buildAgentWSURL(resp.Ws.Url, resp.Ws.Token)
		fmt.Printf("WebSocket: %s\n", wsURL)
	}
}

func handleAgentStop(ctx context.Context, cli *CLI, authCfg cliAuth, args []string) {
	sessionID := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session":
			if i+1 < len(args) {
				sessionID = args[i+1]
				i++
			}
		}
	}
	if sessionID == "" {
		fmt.Println("Usage: gs agent stop --session <session-id>")
		return
	}

	agentClient := agentv1.NewAgentServiceClient(cli.agentGRPCConn())
	_, err := agentClient.StopSession(ctx, &agentv1.StopSessionRequest{
		SessionId: sessionID,
		Reason:    "user_requested",
	})
	if err != nil {
		commandFatal("AGENT_STOP_FAILED", fmt.Sprintf("Failed to stop agent session: %v", err), false, "")
		return
	}
	fmt.Printf("Agent session stopped: %s\n", sessionID)
}

func handleAgentStatus(ctx context.Context, cli *CLI, args []string) {
	sessionID := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session":
			if i+1 < len(args) {
				sessionID = args[i+1]
				i++
			}
		}
	}
	if sessionID == "" {
		fmt.Println("Usage: gs agent status --session <session-id>")
		return
	}

	agentClient := agentv1.NewAgentServiceClient(cli.agentGRPCConn())
	resp, err := agentClient.GetSession(ctx, &agentv1.GetSessionRequest{SessionId: sessionID})
	if err != nil {
		commandFatal("AGENT_STATUS_FAILED", fmt.Sprintf("Failed to get agent session: %v", err), false, "")
		return
	}
	out, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(out))
}

func printAgentHelp() {
	fmt.Println(strings.TrimSpace(`
Usage: gs agent <command> [options]

Commands:
  start   Start a new local agent session
  stop    Stop an agent session
  status  Get agent session status

Start options:
  --slice <id-or-slug>   Slice to run the agent in (required)

Stop/Status options:
  --session <id>         Agent session ID (required)

Examples:
  gs agent start --slice my-repo
  gs agent stop --session sess_abc123
  gs agent status --session sess_abc123
`))
}

func buildAgentWSURL(rawURL, token string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String()
}

func (cli *CLI) agentGRPCConn() *grpc.ClientConn {
	if cli.sliceConn != nil {
		return cli.sliceConn
	}
	return cli.adminConn
}
