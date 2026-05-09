package gscli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ciinternal "github.com/niczy/gitslice/internal/ci"
	civ1 "github.com/niczy/gitslice/proto/ci"
	"google.golang.org/grpc/metadata"
)

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value != "" {
		*f = append(*f, value)
	}
	return nil
}

type runnerLocalConfig struct {
	RunnerID    string `json:"runner_id"`
	RunnerToken string `json:"runner_token"`
	Pool        string `json:"pool"`
	Executor    string `json:"executor"`
	EnrolledAt  string `json:"enrolled_at"`
}

const runnerLogChunkSize = 64 << 10

func handleRunnerCommand(args []string) {
	if len(args) == 0 {
		printRunnerHelp()
		return
	}
	switch args[0] {
	case "token":
		if len(args) >= 2 && args[1] == "create" {
			runAuthenticatedRunnerCommand(args[2:], handleRunnerTokenCreate)
			return
		}
	case "pool":
		if len(args) >= 2 && args[1] == "list" {
			runAuthenticatedRunnerCommand(args[2:], handleRunnerPoolList)
			return
		}
	case "list":
		runAuthenticatedRunnerCommand(args[1:], handleRunnerList)
		return
	case "show":
		runAuthenticatedRunnerCommand(args[1:], handleRunnerShow)
		return
	case "disable":
		runAuthenticatedRunnerCommand(args[1:], handleRunnerDisable)
		return
	case "enable":
		runAuthenticatedRunnerCommand(args[1:], handleRunnerEnable)
		return
	case "revoke":
		runAuthenticatedRunnerCommand(args[1:], handleRunnerRevoke)
		return
	case "jobs":
		runAuthenticatedRunnerCommand(args[1:], handleRunnerJobs)
		return
	case "queue":
		if len(args) >= 2 && args[1] == "list" {
			runAuthenticatedRunnerCommand(args[2:], handleRunnerQueueList)
			return
		}
	case "enroll", "register":
		handleRunnerEnroll(args[1:])
		return
	case "start":
		handleRunnerStart(args[1:])
		return
	case "status":
		handleRunnerHostStatus(args[1:])
		return
	case "doctor":
		handleRunnerDoctor(args[1:])
		return
	case "unenroll":
		handleRunnerUnenroll(args[1:])
		return
	}
	commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown runner command: %s", strings.Join(args, " ")), false, "gs runner --help")
}

func runAuthenticatedRunnerCommand(args []string, handler func(context.Context, *CLI, []string)) {
	args = configureCLIBehavior(args)
	configureCLIOutputMode(args)
	cli, err := newCLIFromFlags()
	if err != nil {
		commandFatalf("CLI_INIT_FAILED", true, "", "Failed to initialize CLI: %v", err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	authConfig, err := resolveAuthConfig(*apiKeyFlag, *userFlag)
	if err != nil {
		commandFatalf("AUTH_RESOLUTION_FAILED", false, "", "Failed to resolve auth: %v", err)
	}
	authConfig, err = ensureCLIAuthReady(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("AUTH_REFRESH_FAILED", true, "", "Failed to refresh stored auth: %v", err)
	}
	handler(withCLIAuth(ctx, authConfig), cli, args)
}

func handleRunnerTokenCreate(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("runner token create")
	name := fs.String("name", "", "Runner name")
	pool := fs.String("pool", "default", "Runner pool")
	ttl := fs.String("ttl", "30m", "Registration token TTL")
	var labels stringListFlag
	fs.Var(&labels, "label", "Runner label; may be repeated")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() > 0 {
		commandUsage("Usage: gs runner token create --name <name> [--pool default] [--label linux] [--ttl 30m] [--json]")
		return
	}
	resp, err := cli.runnerAdminClient.CreateRunnerToken(ctx, &civ1.CreateRunnerTokenRequest{
		Name:   strings.TrimSpace(*name),
		Pool:   strings.TrimSpace(*pool),
		Labels: []string(labels),
		Ttl:    strings.TrimSpace(*ttl),
	})
	if err != nil {
		commandFatalf("RUNNER_TOKEN_FAILED", true, "", "Failed to create runner token: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}
	fmt.Println(resp.GetToken())
	fmt.Printf("Expires: %s\n", resp.GetExpiresAt())
}

func handleRunnerPoolList(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("runner pool list")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	if fs.NArg() > 0 {
		commandUsage("Usage: gs runner pool list [--json]")
		return
	}
	resp, err := cli.runnerAdminClient.ListRunnerPools(ctx, &civ1.ListRunnerPoolsRequest{})
	if err != nil {
		commandFatalf("RUNNER_POOL_LIST_FAILED", true, "", "Failed to list runner pools: %v", err)
	}
	if jsonRequested || *jsonOutput {
		writeJSONOutput(resp)
		return
	}
	for _, pool := range resp.GetPools() {
		fmt.Printf("%s  executor=%s  online=%d  busy=%d  queued=%d\n", pool.GetName(), pool.GetExecutor(), pool.GetOnlineRunners(), pool.GetBusyRunners(), pool.GetQueuedJobs())
	}
}

func handleRunnerList(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("runner list")
	pool := fs.String("pool", "", "Runner pool")
	statusValue := fs.String("status", "", "Runner status")
	limit := fs.Int("limit", 100, "Maximum runners")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	if fs.NArg() > 0 {
		commandUsage("Usage: gs runner list [--pool default] [--status idle] [--limit 100] [--json]")
		return
	}
	resp, err := cli.runnerAdminClient.ListRunners(ctx, &civ1.ListRunnersRequest{Pool: strings.TrimSpace(*pool), Status: strings.TrimSpace(*statusValue), Limit: int32(*limit)})
	if err != nil {
		commandFatalf("RUNNER_LIST_FAILED", true, "", "Failed to list runners: %v", err)
	}
	if jsonRequested || *jsonOutput {
		writeJSONOutput(resp)
		return
	}
	for _, runner := range resp.GetRunners() {
		fmt.Printf("%s  %s  pool=%s  executor=%s  last_seen=%s\n", runner.GetRunnerId(), runner.GetStatus(), runner.GetPool(), runner.GetExecutor(), runner.GetLastSeenAt())
	}
}

func handleRunnerShow(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("runner show")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	if fs.NArg() != 1 {
		commandUsage("Usage: gs runner show <runner-id> [--json]")
		return
	}
	runner, err := cli.runnerAdminClient.GetRunner(ctx, &civ1.GetRunnerRequest{RunnerId: strings.TrimSpace(fs.Arg(0))})
	if err != nil {
		commandFatalf("RUNNER_SHOW_FAILED", true, "", "Failed to show runner: %v", err)
	}
	if jsonRequested || *jsonOutput {
		writeJSONOutput(runner)
		return
	}
	fmt.Printf("Runner: %s\nStatus: %s\nPool: %s\nExecutor: %s\nLabels: %s\n", runner.GetRunnerId(), runner.GetStatus(), runner.GetPool(), runner.GetExecutor(), strings.Join(runner.GetLabels(), ","))
}

func handleRunnerDisable(ctx context.Context, cli *CLI, args []string) {
	fs := newCommandFlagSet("runner disable")
	reason := fs.String("reason", "", "Reason for disabling the runner")
	parseCommandFlags(fs, args)
	if fs.NArg() != 1 {
		commandUsage("Usage: gs runner disable <runner-id> [--reason <text>]")
		return
	}
	resp, err := cli.runnerAdminClient.DisableRunner(ctx, &civ1.DisableRunnerRequest{RunnerId: strings.TrimSpace(fs.Arg(0)), Reason: strings.TrimSpace(*reason)})
	if err != nil {
		commandFatalf("RUNNER_DISABLE_FAILED", true, "", "Failed to disable runner: %v", err)
	}
	fmt.Printf("%s %s\n", resp.GetRunnerId(), resp.GetStatus())
}

func handleRunnerEnable(ctx context.Context, cli *CLI, args []string) {
	runnerID := singleRunnerIDArg("runner enable", args)
	resp, err := cli.runnerAdminClient.EnableRunner(ctx, &civ1.EnableRunnerRequest{RunnerId: runnerID})
	if err != nil {
		commandFatalf("RUNNER_ENABLE_FAILED", true, "", "Failed to enable runner: %v", err)
	}
	fmt.Printf("%s %s\n", resp.GetRunnerId(), resp.GetStatus())
}

func handleRunnerRevoke(ctx context.Context, cli *CLI, args []string) {
	fs := newCommandFlagSet("runner revoke")
	reason := fs.String("reason", "", "Reason for revoking the runner credential")
	requeueLeased := fs.Bool("requeue-leased", false, "Requeue jobs currently leased by this runner")
	cancelLeased := fs.Bool("cancel-leased", false, "Cancel jobs currently leased by this runner")
	parseCommandFlags(fs, args)
	if fs.NArg() != 1 {
		commandUsage("Usage: gs runner revoke <runner-id> [--requeue-leased|--cancel-leased] [--reason <text>]")
		return
	}
	if *requeueLeased && *cancelLeased {
		commandFatal("INVALID_ARGUMENT", "Use either --requeue-leased or --cancel-leased, not both.", false, "")
	}
	resp, err := cli.runnerAdminClient.RevokeRunner(ctx, &civ1.RevokeRunnerRequest{
		RunnerId:      strings.TrimSpace(fs.Arg(0)),
		Reason:        strings.TrimSpace(*reason),
		RequeueLeased: *requeueLeased,
		CancelLeased:  *cancelLeased,
	})
	if err != nil {
		commandFatalf("RUNNER_REVOKE_FAILED", true, "", "Failed to revoke runner: %v", err)
	}
	fmt.Printf("%s %s\n", resp.GetRunnerId(), resp.GetStatus())
}

func handleRunnerJobs(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("runner jobs")
	limit := fs.Int("limit", 20, "Maximum jobs")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	if fs.NArg() != 1 {
		commandUsage("Usage: gs runner jobs <runner-id> [--limit 20] [--json]")
		return
	}
	resp, err := cli.runnerAdminClient.ListRunnerJobs(ctx, &civ1.ListRunnerJobsRequest{RunnerId: strings.TrimSpace(fs.Arg(0)), Limit: int32(*limit)})
	if err != nil {
		commandFatalf("RUNNER_JOBS_FAILED", true, "", "Failed to list runner jobs: %v", err)
	}
	if jsonRequested || *jsonOutput {
		writeJSONOutput(resp)
		return
	}
	for _, job := range resp.GetJobs() {
		fmt.Printf("%s  %s  %s\n", job.GetJobId(), job.GetStatus(), job.GetCheckName())
	}
}

func handleRunnerQueueList(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("runner queue list")
	pool := fs.String("pool", "", "Runner pool")
	limit := fs.Int("limit", 50, "Maximum queued jobs")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	if fs.NArg() > 0 {
		commandUsage("Usage: gs runner queue list [--pool default] [--limit 50] [--json]")
		return
	}
	resp, err := cli.runnerAdminClient.ListQueuedJobs(ctx, &civ1.ListQueuedJobsRequest{Pool: strings.TrimSpace(*pool), Limit: int32(*limit)})
	if err != nil {
		commandFatalf("RUNNER_QUEUE_FAILED", true, "", "Failed to list queued jobs: %v", err)
	}
	if jsonRequested || *jsonOutput {
		writeJSONOutput(resp)
		return
	}
	for _, job := range resp.GetJobs() {
		fmt.Printf("%s  pool=%s  %s\n", job.GetJobId(), job.GetRunnerPool(), job.GetCheckName())
	}
}

func handleRunnerEnroll(args []string) {
	args = configureCLIBehavior(args)
	configureCLIOutputMode(args)
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("runner enroll")
	token := fs.String("token", "", "Runner registration token")
	executorMode := fs.String("executor", "shell", "Executor mode")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	if fs.NArg() > 0 || strings.TrimSpace(*token) == "" {
		commandUsage("Usage: gs runner enroll --token <runner-registration-token> [--executor shell|docker] [--json]")
		return
	}
	cli, err := newCLIFromFlags()
	if err != nil {
		commandFatalf("CLI_INIT_FAILED", true, "", "Failed to initialize CLI: %v", err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := cli.runnerClient.RegisterRunner(ctx, &civ1.RegisterRunnerRequest{
		RegistrationToken: strings.TrimSpace(*token),
		Version:           versionString(),
		Executor:          strings.TrimSpace(*executorMode),
	})
	if err != nil {
		commandFatalf("RUNNER_ENROLL_FAILED", true, "", "Failed to enroll runner: %v", err)
	}
	cfg := runnerLocalConfig{
		RunnerID:    resp.GetRunnerId(),
		RunnerToken: resp.GetRunnerToken(),
		Pool:        resp.GetPool(),
		Executor:    strings.TrimSpace(*executorMode),
		EnrolledAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeRunnerLocalConfig(cfg); err != nil {
		commandFatalf("RUNNER_CONFIG_FAILED", false, "", "Failed to write runner config: %v", err)
	}
	if jsonRequested || *jsonOutput {
		writeJSONOutput(map[string]any{"runner_id": cfg.RunnerID, "pool": cfg.Pool, "executor": cfg.Executor})
		return
	}
	fmt.Printf("Enrolled runner %s in pool %s\n", cfg.RunnerID, cfg.Pool)
}

func handleRunnerStart(args []string) {
	args = configureCLIBehavior(args)
	configureCLIOutputMode(args)
	args, jsonRequested := consumeBoolFlag(args, "json")
	args, once := consumeBoolFlag(args, "once")
	fs := newCommandFlagSet("runner start")
	executorMode := fs.String("executor", "", "Executor mode")
	workDir := fs.String("workdir", "", "Workspace parent directory")
	pollInterval := fs.Duration("poll-interval", 5*time.Second, "Poll interval")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	if fs.NArg() > 0 {
		commandUsage("Usage: gs runner start [--executor shell|docker] [--once] [--workdir <dir>] [--json]")
		return
	}
	cfg, err := readRunnerLocalConfig()
	if err != nil {
		commandFatalf("RUNNER_CONFIG_MISSING", false, "gs runner enroll --token <token>", "Failed to read runner config: %v", err)
	}
	if strings.TrimSpace(*executorMode) != "" {
		cfg.Executor = strings.TrimSpace(*executorMode)
	}
	if cfg.Executor == "" {
		cfg.Executor = "shell"
	}
	if cfg.Executor != "shell" && cfg.Executor != "docker" {
		commandFatal("INVALID_ARGUMENT", "Executor must be shell or docker", false, "")
	}
	cli, err := newCLIFromFlags()
	if err != nil {
		commandFatalf("CLI_INIT_FAILED", true, "", "Failed to initialize CLI: %v", err)
	}
	defer cli.Close()
	ctx := withRunnerAuth(context.Background(), cfg.RunnerToken)
	started := time.Now()
	completedJobs := 0
	for {
		pollCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		resp, err := cli.runnerClient.PollJobs(pollCtx, &civ1.PollJobsRequest{RunnerId: cfg.RunnerID, Pool: cfg.Pool, MaxJobs: 1})
		cancel()
		if err != nil {
			commandFatalf("RUNNER_POLL_FAILED", true, "", "Failed to poll runner jobs: %v", err)
		}
		if len(resp.GetJobs()) == 0 {
			if once {
				break
			}
			time.Sleep(*pollInterval)
			continue
		}
		for _, job := range resp.GetJobs() {
			if err := executeRunnerJob(ctx, cli, cfg, job, strings.TrimSpace(*workDir)); err != nil {
				commandFatalf("RUNNER_JOB_FAILED", true, "", "Failed to execute CI job: %v", err)
			}
			completedJobs++
		}
		if once {
			break
		}
	}
	if jsonRequested || *jsonOutput {
		writeJSONOutput(map[string]any{"runner_id": cfg.RunnerID, "completed_jobs": completedJobs, "duration_ms": time.Since(started).Milliseconds()})
		return
	}
	fmt.Printf("Runner %s completed %d job(s)\n", cfg.RunnerID, completedJobs)
}

func executeRunnerJob(ctx context.Context, cli *CLI, cfg runnerLocalConfig, job *civ1.Job, workspaceParent string) error {
	claim, err := cli.runnerClient.ClaimJob(ctx, &civ1.ClaimJobRequest{RunnerId: cfg.RunnerID, JobId: job.GetJobId()})
	if err != nil {
		return err
	}
	payload, err := cli.runnerClient.GetJobPayload(ctx, &civ1.GetJobPayloadRequest{JobId: job.GetJobId(), LeaseId: claim.GetLeaseId()})
	if err != nil {
		return err
	}
	workspace, cleanup, err := materializeRunnerWorkspace(payload, workspaceParent)
	if err != nil {
		_, _ = cli.runnerClient.CompleteJob(ctx, &civ1.CompleteJobRequest{JobId: job.GetJobId(), LeaseId: claim.GetLeaseId(), Status: "failed", InfraFailure: true, FailureMessage: err.Error()})
		return err
	}
	defer cleanup()
	timeoutSeconds := payload.GetTimeoutSeconds()
	if timeoutSeconds <= 0 {
		timeoutSeconds = 900
	}
	jobCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	cacheDir, err := runnerCacheDir()
	if err != nil {
		_, _ = cli.runnerClient.CompleteJob(ctx, &civ1.CompleteJobRequest{JobId: payload.GetJobId(), LeaseId: claim.GetLeaseId(), Status: "failed", InfraFailure: true, FailureMessage: err.Error()})
		return err
	}
	executor := strings.TrimSpace(cfg.Executor)
	if executor == "" {
		executor = "shell"
	}
	if executor == "docker" && strings.TrimSpace(payload.GetImage()) == "" {
		err := errors.New("docker executor requires a job image")
		_, _ = cli.runnerClient.CompleteJob(ctx, &civ1.CompleteJobRequest{JobId: payload.GetJobId(), LeaseId: claim.GetLeaseId(), Status: "failed", InfraFailure: true, FailureMessage: err.Error()})
		return err
	}
	shellEnv := runnerJobEnv(payload, workspace, cacheDir, true)
	dockerEnv := runnerJobEnv(payload, "/workspace", "/gitslice-cache", false)
	chunkIndex := int64(0)
	for idx, command := range payload.GetCommands() {
		appendRunnerLog(ctx, cli, payload.GetJobId(), claim.GetLeaseId(), &chunkIndex, "system", []byte("$ "+command+"\n"))
		commandDir, err := safeWorkspacePath(workspace, payload.GetWorkingDirectory())
		if err != nil {
			_, _ = cli.runnerClient.CompleteJob(ctx, &civ1.CompleteJobRequest{JobId: payload.GetJobId(), LeaseId: claim.GetLeaseId(), Status: "failed", InfraFailure: true, FailureMessage: err.Error()})
			return err
		}
		exitCode, output := runRunnerCommand(jobCtx, executor, payload, workspace, commandDir, cacheDir, command, shellEnv, dockerEnv)
		if len(output) > 0 {
			appendRunnerLog(ctx, cli, payload.GetJobId(), claim.GetLeaseId(), &chunkIndex, "stdout", output)
		}
		statusValue := "passed"
		if exitCode != 0 {
			statusValue = "failed"
		}
		_, err = cli.runnerClient.CompleteStep(ctx, &civ1.CompleteStepRequest{JobId: payload.GetJobId(), LeaseId: claim.GetLeaseId(), StepIndex: int32(idx), Status: statusValue, ExitCode: int32(exitCode)})
		if err != nil {
			return err
		}
		if exitCode != 0 {
			if uploadErr := uploadRunnerArtifacts(ctx, cli, payload, claim.GetLeaseId(), workspace, &chunkIndex); uploadErr != nil {
				appendRunnerLog(ctx, cli, payload.GetJobId(), claim.GetLeaseId(), &chunkIndex, "system", []byte("artifact upload failed: "+uploadErr.Error()+"\n"))
			}
			_, err = cli.runnerClient.CompleteJob(ctx, &civ1.CompleteJobRequest{JobId: payload.GetJobId(), LeaseId: claim.GetLeaseId(), Status: "failed", ExitCode: int32(exitCode)})
			return err
		}
	}
	if err := uploadRunnerArtifacts(ctx, cli, payload, claim.GetLeaseId(), workspace, &chunkIndex); err != nil {
		_, _ = cli.runnerClient.CompleteJob(ctx, &civ1.CompleteJobRequest{JobId: payload.GetJobId(), LeaseId: claim.GetLeaseId(), Status: "failed", InfraFailure: true, FailureMessage: err.Error()})
		return err
	}
	_, err = cli.runnerClient.CompleteJob(ctx, &civ1.CompleteJobRequest{JobId: payload.GetJobId(), LeaseId: claim.GetLeaseId(), Status: "passed"})
	return err
}

func appendRunnerLog(ctx context.Context, cli *CLI, jobID, leaseID string, chunkIndex *int64, stream string, payload []byte) {
	if len(payload) == 0 {
		return
	}
	for len(payload) > 0 {
		n := len(payload)
		if n > runnerLogChunkSize {
			n = runnerLogChunkSize
		}
		_, _ = cli.runnerClient.AppendLog(ctx, &civ1.AppendLogRequest{
			JobId:      jobID,
			LeaseId:    leaseID,
			ChunkIndex: *chunkIndex,
			Stream:     stream,
			Payload:    payload[:n],
		})
		*chunkIndex = *chunkIndex + 1
		payload = payload[n:]
	}
}

func runnerJobEnv(payload *civ1.JobPayload, workspace, cacheDir string, inheritHost bool) []string {
	env := make([]string, 0, len(payload.GetEnv())+16)
	if inheritHost {
		env = append(env, os.Environ()...)
	}
	keys := make([]string, 0, len(payload.GetEnv()))
	for key := range payload.GetEnv() {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+payload.GetEnv()[key])
	}
	env = append(env,
		"GS_HOME_ROOT="+workspace,
		"GS_MANIFEST_PATH="+payload.GetManifestPath(),
		"GS_MANIFEST_DIR="+filepath.Join(workspace, strings.TrimPrefix(payload.GetManifestDir(), "/")),
		"GS_CHANGED_FILES="+strings.Join(payload.GetChangedFiles(), "\n"),
		"GS_CHANGESET_ID="+payload.GetChangesetId(),
		"GS_CHANGESET_VERSION="+payload.GetChangesetVersionId(),
		"GS_RUN_ID="+payload.GetRunId(),
		"GS_JOB_ID="+payload.GetJobId(),
		"GS_CACHE_ROOT="+cacheDir,
		"GS_CACHE_PATHS="+strings.Join(payload.GetCachePaths(), "\n"),
	)
	return env
}

func runRunnerCommand(ctx context.Context, executor string, payload *civ1.JobPayload, workspace, workDir, cacheDir, command string, shellEnv, dockerEnv []string) (int, []byte) {
	switch strings.TrimSpace(executor) {
	case "docker":
		return runDockerCommand(ctx, payload, workspace, workDir, cacheDir, command, dockerEnv)
	default:
		return runShellCommand(ctx, payload.GetShell(), workDir, command, shellEnv)
	}
}

func runShellCommand(ctx context.Context, shellName, workDir, command string, env []string) (int, []byte) {
	shellName = strings.TrimSpace(shellName)
	if shellName == "" {
		shellName = "bash"
	}
	cmd := exec.CommandContext(ctx, shellName, "-lc", command)
	cmd.Dir = workDir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err == nil {
		return 0, output
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), output
	}
	return 127, append(output, []byte(err.Error()+"\n")...)
}

func runDockerCommand(ctx context.Context, payload *civ1.JobPayload, workspace, workDir, cacheDir, command string, env []string) (int, []byte) {
	if _, err := exec.LookPath("docker"); err != nil {
		return 127, []byte("docker executable not found\n")
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return 127, []byte(err.Error() + "\n")
	}
	relWorkDir, err := filepath.Rel(workspace, workDir)
	if err != nil || strings.HasPrefix(relWorkDir, "..") {
		return 127, []byte("working directory escapes workspace\n")
	}
	containerWorkDir := "/workspace"
	if relWorkDir != "." {
		containerWorkDir = "/workspace/" + filepath.ToSlash(relWorkDir)
	}
	shellName := strings.TrimSpace(payload.GetShell())
	if shellName == "" {
		shellName = "bash"
	}
	args := []string{
		"run", "--rm",
		"--pull=missing",
		"--network", "none",
		"--volume", workspace + ":/workspace:rw",
		"--volume", cacheDir + ":/gitslice-cache:rw",
		"--workdir", containerWorkDir,
	}
	for _, entry := range env {
		if key := envKey(entry); key != "" {
			args = append(args, "--env", entry)
		}
	}
	args = append(args, payload.GetImage(), shellName, "-lc", command)
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return 0, output
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), output
	}
	return 127, append(output, []byte(err.Error()+"\n")...)
}

func envKey(entry string) string {
	key, _, ok := strings.Cut(entry, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return ""
	}
	return key
}

func runnerCacheDir() (string, error) {
	configDir, err := gitsliceConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(configDir, "runner", "cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func uploadRunnerArtifacts(ctx context.Context, cli *CLI, payload *civ1.JobPayload, leaseID string, workspace string, chunkIndex *int64) error {
	if len(payload.GetArtifacts()) == 0 {
		return nil
	}
	matched := make(map[string]struct{})
	err := filepath.WalkDir(workspace, func(localPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(workspace, localPath)
		if err != nil {
			return err
		}
		logical := "/" + filepath.ToSlash(rel)
		if !artifactMatches(payload.GetArtifacts(), logical) {
			return nil
		}
		if _, ok := matched[logical]; ok {
			return nil
		}
		matched[logical] = struct{}{}
		raw, err := os.ReadFile(localPath)
		if err != nil {
			return err
		}
		_, err = cli.runnerClient.UploadArtifact(ctx, &civ1.UploadArtifactRequest{
			JobId:   payload.GetJobId(),
			LeaseId: leaseID,
			Path:    logical,
			Payload: raw,
		})
		if err == nil {
			appendRunnerLog(ctx, cli, payload.GetJobId(), leaseID, chunkIndex, "system", []byte("uploaded artifact "+logical+"\n"))
		}
		return err
	})
	return err
}

func artifactMatches(patterns []string, logicalPath string) bool {
	for _, pattern := range patterns {
		if ciinternal.MatchHomePattern(pattern, logicalPath) {
			return true
		}
	}
	return false
}

func materializeRunnerWorkspace(payload *civ1.JobPayload, parent string) (string, func(), error) {
	if strings.TrimSpace(parent) == "" {
		parent = os.TempDir()
	}
	root, err := os.MkdirTemp(parent, "gitslice-ci-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	for _, file := range payload.GetFiles() {
		target, err := safeWorkspacePath(root, file.GetPath())
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			cleanup()
			return "", func() {}, err
		}
		mode := os.FileMode(0o644)
		if file.GetExecutable() {
			mode = 0o755
		}
		if err := os.WriteFile(target, file.GetContent(), mode); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	wd, err := safeWorkspacePath(root, payload.GetWorkingDirectory())
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := os.MkdirAll(wd, 0o755); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return root, cleanup, nil
}

func safeWorkspacePath(root, logicalPath string) (string, error) {
	cleaned := strings.TrimPrefix(strings.TrimSpace(logicalPath), "/")
	if cleaned == "" || cleaned == "." {
		return root, nil
	}
	cleaned = filepath.Clean(cleaned)
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("workspace path escapes root: %s", logicalPath)
	}
	return filepath.Join(root, cleaned), nil
}

func handleRunnerHostStatus(args []string) {
	args = configureCLIBehavior(args)
	configureCLIOutputMode(args)
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("runner status")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	cfg, err := readRunnerLocalConfig()
	if err != nil {
		commandFatalf("RUNNER_CONFIG_MISSING", false, "gs runner enroll --token <token>", "Failed to read runner config: %v", err)
	}
	if jsonRequested || *jsonOutput {
		writeJSONOutput(cfg)
		return
	}
	fmt.Printf("Runner: %s\nPool: %s\nExecutor: %s\nEnrolled: %s\n", cfg.RunnerID, cfg.Pool, cfg.Executor, cfg.EnrolledAt)
}

func handleRunnerDoctor(args []string) {
	args = configureCLIBehavior(args)
	configureCLIOutputMode(args)
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("runner doctor")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	cfg, err := readRunnerLocalConfig()
	shellOK := false
	if err == nil {
		_, shellErr := exec.LookPath(defaultRunnerShell(cfg.Executor))
		shellOK = shellErr == nil
	}
	result := map[string]any{"config_ok": err == nil, "shell_ok": shellOK}
	if jsonRequested || *jsonOutput {
		writeJSONOutput(result)
		return
	}
	fmt.Printf("config: %v\nshell: %v\n", err == nil, shellOK)
}

func handleRunnerUnenroll(args []string) {
	args = configureCLIBehavior(args)
	configureCLIOutputMode(args)
	fs := newCommandFlagSet("runner unenroll")
	parseCommandFlags(fs, args)
	if err := removeRunnerLocalConfig(); err != nil {
		commandFatalf("RUNNER_CONFIG_FAILED", false, "", "Failed to remove runner config: %v", err)
	}
	fmt.Println("Runner credentials removed")
}

func defaultRunnerShell(executor string) string {
	if strings.TrimSpace(executor) == "" || executor == "shell" {
		return "bash"
	}
	return executor
}

func singleRunnerIDArg(command string, args []string) string {
	fs := newCommandFlagSet(command)
	parseCommandFlags(fs, args)
	if fs.NArg() != 1 {
		commandUsage("Usage: gs " + command + " <runner-id>")
		return ""
	}
	return strings.TrimSpace(fs.Arg(0))
}

func withRunnerAuth(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+strings.TrimSpace(token))
}

func runnerConfigPath() (string, error) {
	configDir, err := gitsliceConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "runner", "config.json"), nil
}

func readRunnerLocalConfig() (runnerLocalConfig, error) {
	path, err := runnerConfigPath()
	if err != nil {
		return runnerLocalConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return runnerLocalConfig{}, err
	}
	var cfg runnerLocalConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return runnerLocalConfig{}, err
	}
	if strings.TrimSpace(cfg.RunnerID) == "" || strings.TrimSpace(cfg.RunnerToken) == "" {
		return runnerLocalConfig{}, fmt.Errorf("runner config is missing credentials")
	}
	return cfg, nil
}

func writeRunnerLocalConfig(cfg runnerLocalConfig) error {
	path, err := runnerConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func removeRunnerLocalConfig() error {
	path, err := runnerConfigPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
