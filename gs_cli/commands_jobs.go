package gscli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newJobsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs <command> [options]",
		Short: "Inspect detached CLI jobs for long-running commands",
		Run: func(cmd *cobra.Command, args []string) {
			printJobsHelp()
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printJobsHelp()
	})
	cmd.AddCommand(
		&cobra.Command{
			Use:                "list [options]",
			Short:              "List detached CLI jobs",
			DisableFlagParsing: true,
			Run: func(cmd *cobra.Command, args []string) {
				handleJobsList(args)
			},
		},
		&cobra.Command{
			Use:                "get <job-id> [options]",
			Short:              "Show a detached job",
			DisableFlagParsing: true,
			Run: func(cmd *cobra.Command, args []string) {
				handleJobsGet(args)
			},
		},
		&cobra.Command{
			Use:                "wait <job-id> [options]",
			Short:              "Wait for a detached job to finish",
			DisableFlagParsing: true,
			Run: func(cmd *cobra.Command, args []string) {
				handleJobsWait(args)
			},
		},
		&cobra.Command{
			Use:                "logs <job-id> [options]",
			Short:              "Print stdout/stderr captured for a detached job",
			DisableFlagParsing: true,
			Run: func(cmd *cobra.Command, args []string) {
				handleJobsLogs(args)
			},
		},
	)
	return cmd
}

func handleJobsCommand(args []string) {
	if len(args) == 0 {
		printJobsHelp()
		return
	}

	switch args[0] {
	case "list":
		handleJobsList(args[1:])
	case "get":
		handleJobsGet(args[1:])
	case "wait":
		handleJobsWait(args[1:])
	case "logs":
		handleJobsLogs(args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown jobs command: %s", args[0]), false, "gs jobs --help")
	}
}

func handleJobsList(args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("jobs list")
	limit := fs.Int("limit", 20, "Maximum jobs to return")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 0 {
		commandUsage("Usage: gs jobs list [--limit <n>] [--json]")
		return
	}

	records, err := listLocalCLIJobs()
	if err != nil {
		commandFatalf("JOBS_LIST_FAILED", false, "", "Failed to list jobs: %v", err)
	}
	if *limit >= 0 && len(records) > *limit {
		records = records[:*limit]
	}

	if jsonEnabled {
		out := jsonJobsListOutput{
			Total: len(records),
			Jobs:  make([]jsonJobOutput, 0, len(records)),
		}
		for _, record := range records {
			out.Jobs = append(out.Jobs, buildJSONJobOutput(&record, false, false))
		}
		writeJSONOutput(out)
		return
	}

	if len(records) == 0 {
		fmt.Println("No local jobs.")
		return
	}
	for _, record := range records {
		fmt.Printf("%s\t%s\t%s\t%s\n", record.ID, record.Status, record.Kind, record.CreatedAt)
	}
}

func handleJobsGet(args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("jobs get")
	includeLogs := fs.Bool("logs", false, "Include stdout/stderr log content")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs jobs get <job-id> [--logs] [--json]")
		return
	}

	record, err := loadLocalCLIJob(strings.TrimSpace(fs.Arg(0)))
	if err != nil {
		commandFatalf("JOB_NOT_FOUND", false, "gs jobs list --json", "Failed to load job: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(buildJSONJobOutput(record, *includeLogs, true))
		return
	}
	printLocalCLIJob(record, *includeLogs)
}

func handleJobsWait(args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("jobs wait")
	timeout := fs.Duration("timeout", 10*time.Minute, "Maximum time to wait")
	includeLogs := fs.Bool("logs", false, "Include stdout/stderr log content")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs jobs wait <job-id> [--timeout <duration>] [--logs] [--json]")
		return
	}

	record, err := waitForLocalCLIJob(strings.TrimSpace(fs.Arg(0)), *timeout)
	if err != nil {
		commandFatalf("JOB_WAIT_FAILED", true, "", "Failed while waiting for job: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(buildJSONJobOutput(record, *includeLogs, true))
		if record.Status == jobStatusFailed {
			os.Exit(max(record.ExitCode, cliExitGeneral))
		}
		return
	}
	printLocalCLIJob(record, *includeLogs)
	if record.Status == jobStatusFailed {
		os.Exit(max(record.ExitCode, cliExitGeneral))
	}
}

func handleJobsLogs(args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("jobs logs")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs jobs logs <job-id> [--json]")
		return
	}

	record, err := loadLocalCLIJob(strings.TrimSpace(fs.Arg(0)))
	if err != nil {
		commandFatalf("JOB_NOT_FOUND", false, "gs jobs list --json", "Failed to load job: %v", err)
	}
	stdout, stderr, err := readLocalCLIJobLogs(record)
	if err != nil {
		commandFatalf("JOB_LOGS_FAILED", false, "", "Failed to read job logs: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(jsonJobLogsOutput{
			JobID:  record.ID,
			Status: record.Status,
			Stdout: stdout,
			Stderr: stderr,
		})
		return
	}
	if stdout != "" {
		fmt.Print(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			fmt.Println()
		}
	}
	if stderr != "" {
		if stdout != "" {
			fmt.Println("--- stderr ---")
		}
		fmt.Print(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			fmt.Println()
		}
	}
}

func buildJSONJobOutput(record *localCLIJobRecord, includeLogs bool, includeResult bool) jsonJobOutput {
	out := jsonJobOutput{}
	if record == nil {
		return out
	}
	out.JobID = record.ID
	out.Kind = record.Kind
	out.Status = record.Status
	out.Command = append([]string(nil), record.Command...)
	out.WorkingDir = record.WorkingDir
	out.CreatedAt = record.CreatedAt
	out.StartedAt = record.StartedAt
	out.FinishedAt = record.FinishedAt
	out.PID = record.PID
	out.ExitCode = record.ExitCode
	out.StdoutPath = record.StdoutPath
	out.StderrPath = record.StderrPath
	if includeResult && len(record.Result) > 0 {
		out.Result = append(json.RawMessage(nil), record.Result...)
	}
	if !includeLogs {
		return out
	}
	stdout, stderr, err := readLocalCLIJobLogs(record)
	if err == nil {
		out.Stdout = stdout
		out.Stderr = stderr
	}
	if len(out.Result) == 0 && len(record.Result) > 0 {
		out.Result = append(json.RawMessage(nil), record.Result...)
	}
	return out
}

func printLocalCLIJob(record *localCLIJobRecord, includeLogs bool) {
	fmt.Printf("Job: %s\n", record.ID)
	fmt.Printf("Kind: %s\n", record.Kind)
	fmt.Printf("Status: %s\n", record.Status)
	if len(record.Command) > 0 {
		fmt.Printf("Command: %s\n", strings.Join(record.Command, " "))
	}
	if record.WorkingDir != "" {
		fmt.Printf("Working dir: %s\n", record.WorkingDir)
	}
	fmt.Printf("Created: %s\n", record.CreatedAt)
	if record.StartedAt != "" {
		fmt.Printf("Started: %s\n", record.StartedAt)
	}
	if record.FinishedAt != "" {
		fmt.Printf("Finished: %s\n", record.FinishedAt)
	}
	if record.PID > 0 {
		fmt.Printf("PID: %d\n", record.PID)
	}
	if record.ExitCode != 0 || record.Status == jobStatusFailed {
		fmt.Printf("Exit code: %d\n", record.ExitCode)
	}
	if len(record.Result) > 0 {
		fmt.Println("Result:")
		fmt.Println(string(record.Result))
	}
	if includeLogs {
		stdout, stderr, err := readLocalCLIJobLogs(record)
		if err == nil {
			if stdout != "" {
				fmt.Println("Stdout:")
				fmt.Print(stdout)
				if !strings.HasSuffix(stdout, "\n") {
					fmt.Println()
				}
			}
			if stderr != "" {
				fmt.Println("Stderr:")
				fmt.Print(stderr)
				if !strings.HasSuffix(stderr, "\n") {
					fmt.Println()
				}
			}
		}
	}
}

func emitDetachedJobStarted(record *localCLIJobRecord, jsonEnabled bool) {
	if jsonEnabled {
		writeJSONOutput(buildJSONJobOutput(record, false, false))
		return
	}
	fmt.Printf("Started detached job: %s\n", record.ID)
	fmt.Printf("Kind: %s\n", record.Kind)
	fmt.Printf("Status: %s\n", record.Status)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
