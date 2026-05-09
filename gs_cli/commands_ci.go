package gscli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	civ1 "github.com/niczy/gitslice/proto/ci"
	"github.com/spf13/cobra"
)

func newCICommand() *cobra.Command {
	cmd := newAuthenticatedCobraCommand("ci <command> [options]", "Run and inspect Gitslice CI checks", 24*time.Hour, func(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
		handleCICommand(ctx, cli, args)
	})
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printCIHelp()
	})
	return cmd
}

func newRunnerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "runner <command> [options]",
		Short:              "Manage and run CI executors",
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			if isHelpRequest(args) {
				_ = cmd.Help()
				return
			}
			handleRunnerCommand(args)
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printRunnerHelp()
	})
	return cmd
}

func handleCICommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) == 0 {
		printCIHelp()
		return
	}
	switch args[0] {
	case "run":
		handleCIRun(ctx, cli, args[1:])
	case "status":
		handleCIStatus(ctx, cli, args[1:])
	case "logs":
		handleCILogs(ctx, cli, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown CI command: %s", args[0]), false, "gs ci --help")
	}
}

func handleCIRun(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("ci run")
	changesetID := fs.String("changeset", "", "Changeset ID")
	versionID := fs.String("version", "", "Changeset version/snapshot ID or number")
	manifestPath := fs.String("manifest", "", "Restrict the run to one manifest path")
	jobKey := fs.String("job", "", "Restrict the run to one job key")
	trigger := fs.String("trigger", "manual", "Trigger event label")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() > 0 {
		commandUsage("Usage: gs ci run [--changeset <id>] [--version <id-or-number>] [--manifest </path/.gs-ci.yaml>] [--job <key>] [--json]")
		return
	}
	resolvedChangesetID, err := resolveChangesetIDForRead(*changesetID)
	if err != nil {
		commandFatalf("CHANGESET_RESOLUTION_FAILED", false, "", "Failed to resolve changeset ID: %v", err)
		return
	}
	resp, err := cli.ciClient.StartRun(ctx, &civ1.StartRunRequest{
		ChangesetId:        resolvedChangesetID,
		ChangesetVersionId: strings.TrimSpace(*versionID),
		ManifestPath:       strings.TrimSpace(*manifestPath),
		JobKey:             strings.TrimSpace(*jobKey),
		TriggerEvent:       strings.TrimSpace(*trigger),
	})
	if err != nil {
		commandFatalf("CI_RUN_FAILED", true, "", "Failed to start CI run: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(map[string]any{
			"run_id": resp.GetRunId(),
			"status": resp.GetStatus(),
		})
		return
	}
	fmt.Printf("Started CI run %s\n", resp.GetRunId())
	fmt.Printf("Status: %s\n", resp.GetStatus())
}

func handleCIStatus(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("ci status")
	runID := fs.String("run", "", "CI run ID")
	changesetID := fs.String("changeset", "", "Changeset ID")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() > 0 {
		commandUsage("Usage: gs ci status [--run <run-id>] [--changeset <id>] [--json]")
		return
	}
	if strings.TrimSpace(*runID) != "" {
		run, err := cli.ciClient.GetRun(ctx, &civ1.GetRunRequest{RunId: strings.TrimSpace(*runID)})
		if err != nil {
			commandFatalf("CI_STATUS_FAILED", true, "", "Failed to get CI run: %v", err)
		}
		if jsonEnabled {
			writeJSONOutput(run)
			return
		}
		printCIRun(run)
		return
	}

	resolvedChangesetID, err := resolveChangesetIDForRead(*changesetID)
	if err != nil {
		commandFatalf("CHANGESET_RESOLUTION_FAILED", false, "", "Failed to resolve changeset ID: %v", err)
		return
	}
	runs, err := cli.ciClient.ListRuns(ctx, &civ1.ListRunsRequest{ChangesetId: resolvedChangesetID, Limit: 20})
	if err != nil {
		commandFatalf("CI_STATUS_FAILED", true, "", "Failed to list CI runs: %v", err)
	}
	checks, err := cli.ciClient.ListChecks(ctx, &civ1.ListChecksRequest{ChangesetId: resolvedChangesetID})
	if err != nil {
		commandFatalf("CI_STATUS_FAILED", true, "", "Failed to list CI checks: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(map[string]any{
			"changeset_id": resolvedChangesetID,
			"runs":         runs.GetRuns(),
			"checks":       checks.GetChecks(),
		})
		return
	}
	fmt.Printf("Changeset: %s\n", resolvedChangesetID)
	if len(runs.GetRuns()) == 0 {
		fmt.Println("Runs: none")
	} else {
		fmt.Println("Runs:")
		for _, run := range runs.GetRuns() {
			fmt.Printf("  %s  %s  plan=%s  attempt=%d\n", run.GetRunId(), run.GetStatus(), run.GetPlanHash(), run.GetAttempt())
		}
	}
	if len(checks.GetChecks()) == 0 {
		fmt.Println("Checks: none")
	} else {
		fmt.Println("Checks:")
		for _, check := range checks.GetChecks() {
			required := "optional"
			if check.GetRequired() {
				required = "required"
			}
			fmt.Printf("  %s  %s  %s\n", check.GetCheckName(), check.GetStatus(), required)
		}
	}
}

func handleCILogs(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("ci logs")
	runID := fs.String("run", "", "CI run ID")
	jobID := fs.String("job", "", "CI job ID")
	since := fs.Int64("since", 0, "First log chunk index to return")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() > 0 || strings.TrimSpace(*runID) == "" {
		commandUsage("Usage: gs ci logs --run <run-id> [--job <job-id>] [--since <chunk>] [--json]")
		return
	}
	stream, err := cli.ciClient.StreamLogs(ctx, &civ1.StreamLogsRequest{
		RunId:      strings.TrimSpace(*runID),
		JobId:      strings.TrimSpace(*jobID),
		SinceChunk: *since,
	})
	if err != nil {
		commandFatalf("CI_LOGS_FAILED", true, "", "Failed to stream CI logs: %v", err)
	}
	events := make([]*civ1.LogEvent, 0)
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			commandFatalf("CI_LOGS_FAILED", true, "", "Failed to read CI logs: %v", err)
		}
		if jsonEnabled {
			events = append(events, event)
			continue
		}
		if event.GetStream() != "" {
			fmt.Printf("[%s] ", event.GetStream())
		}
		fmt.Print(string(event.GetPayload()))
	}
	if jsonEnabled {
		writeJSONOutput(map[string]any{"events": events})
	}
}

func printCIRun(run *civ1.Run) {
	fmt.Printf("Run: %s\n", run.GetRunId())
	fmt.Printf("Status: %s\n", run.GetStatus())
	fmt.Printf("Changeset: %s\n", run.GetChangesetId())
	fmt.Printf("Version: %s\n", run.GetChangesetVersionId())
	fmt.Printf("Plan: %s\n", run.GetPlanHash())
	if len(run.GetJobs()) == 0 {
		fmt.Println("Jobs: none")
		return
	}
	fmt.Println("Jobs:")
	for _, job := range run.GetJobs() {
		required := "optional"
		if job.GetRequired() {
			required = "required"
		}
		fmt.Printf("  %s  %s  %s  pool=%s\n", job.GetCheckName(), job.GetStatus(), required, job.GetRunnerPool())
	}
}
