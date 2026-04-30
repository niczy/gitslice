package gscli

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	jobStatusQueued    = "queued"
	jobStatusRunning   = "running"
	jobStatusSucceeded = "succeeded"
	jobStatusFailed    = "failed"
)

type localCLIJobRecord struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Command    []string        `json:"command"`
	WorkingDir string          `json:"working_dir,omitempty"`
	CreatedAt  string          `json:"created_at"`
	StartedAt  string          `json:"started_at,omitempty"`
	FinishedAt string          `json:"finished_at,omitempty"`
	PID        int             `json:"pid,omitempty"`
	ExitCode   int             `json:"exit_code,omitempty"`
	StdoutPath string          `json:"stdout_path,omitempty"`
	StderrPath string          `json:"stderr_path,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
}

func jobsRootPath() (string, error) {
	configDir, err := gitsliceConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "jobs"), nil
}

func jobDirPath(id string) (string, error) {
	root, err := jobsRootPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, id), nil
}

func jobMetadataPath(id string) (string, error) {
	dir, err := jobDirPath(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "job.json"), nil
}

func jobStdoutPath(id string) (string, error) {
	dir, err := jobDirPath(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "stdout.log"), nil
}

func jobStderrPath(id string) (string, error) {
	dir, err := jobDirPath(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "stderr.log"), nil
}

func createLocalCLIJob(kind string, commandArgs []string) (*localCLIJobRecord, error) {
	id, err := newLocalCLIJobID()
	if err != nil {
		return nil, err
	}
	dir, err := jobDirPath(id)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	stdoutPath, err := jobStdoutPath(id)
	if err != nil {
		return nil, err
	}
	stderrPath, err := jobStderrPath(id)
	if err != nil {
		return nil, err
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	record := &localCLIJobRecord{
		ID:         id,
		Kind:       strings.TrimSpace(kind),
		Status:     jobStatusQueued,
		Command:    append([]string(nil), ensureJSONFlag(commandArgs)...),
		WorkingDir: workingDir,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
	}
	if err := saveLocalCLIJob(record); err != nil {
		return nil, err
	}
	return record, nil
}

func saveLocalCLIJob(record *localCLIJobRecord) error {
	if record == nil {
		return errors.New("nil job record")
	}
	path, err := jobMetadataPath(record.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".job-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func loadLocalCLIJob(id string) (*localCLIJobRecord, error) {
	path, err := jobMetadataPath(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record localCLIJobRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func listLocalCLIJobs() ([]localCLIJobRecord, error) {
	root, err := jobsRootPath()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	jobs := make([]localCLIJobRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, err := loadLocalCLIJob(entry.Name())
		if err != nil {
			continue
		}
		jobs = append(jobs, *record)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt == jobs[j].CreatedAt {
			return jobs[i].ID > jobs[j].ID
		}
		return jobs[i].CreatedAt > jobs[j].CreatedAt
	})
	return jobs, nil
}

func readLocalCLIJobLogs(record *localCLIJobRecord) (string, string, error) {
	if record == nil {
		return "", "", errors.New("nil job record")
	}
	stdoutBytes, err := os.ReadFile(record.StdoutPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	stderrBytes, err := os.ReadFile(record.StderrPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	return string(stdoutBytes), string(stderrBytes), nil
}

func ensureJSONFlag(args []string) []string {
	for _, arg := range args {
		if strings.TrimSpace(arg) == "--json" {
			return append([]string(nil), args...)
		}
	}
	return append(append([]string(nil), args...), "--json")
}

func startDetachedCLIJob(kind string, commandArgs []string) (*localCLIJobRecord, error) {
	record, err := createLocalCLIJob(kind, commandArgs)
	if err != nil {
		return nil, err
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	defer devNull.Close()

	runnerArgs := append(globalCLIFlagArgs(), "__run-job", record.ID, "--")
	runnerArgs = append(runnerArgs, record.Command...)
	cmd := exec.Command(os.Args[0], runnerArgs...)
	cmd.Env = detachedCLIJobEnv(record.ID)
	cmd.Dir = record.WorkingDir
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	_ = cmd.Process.Release()
	return record, nil
}

func detachedCLIJobEnv(jobID string) []string {
	env := append([]string(nil), os.Environ()...)
	env = setEnvValue(env, "GS_NON_INTERACTIVE", "1")
	env = setEnvValue(env, "GS_CLI_JOB_ID", jobID)
	if token := strings.TrimSpace(*apiKeyFlag); token != "" {
		env = setEnvValue(env, "GS_API_KEY", token)
	}
	if username := strings.TrimSpace(*userFlag); username != "" {
		env = setEnvValue(env, "GS_USERNAME", username)
	}
	return env
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	replaced := false
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}

func globalCLIFlagArgs() []string {
	args := []string{
		"--account-addr", strings.TrimSpace(*accountServerAddr),
		"--slice-addr", strings.TrimSpace(*sliceServerAddr),
		"--admin-addr", strings.TrimSpace(*adminServerAddr),
		"--file-addr", strings.TrimSpace(*fileServerAddr),
		"--fs-addr", strings.TrimSpace(*fsServerAddr),
	}
	if strings.TrimSpace(*coreServerAddr) != "" {
		args = append(args, "--addr", strings.TrimSpace(*coreServerAddr))
	}
	if *useTLS {
		args = append(args, "--tls")
	}
	if cliNonInteractive || *nonInteractive {
		args = append(args, "--non-interactive")
	}
	return args
}

func runDetachedCLIJob(jobID string, commandArgs []string) int {
	record, err := loadLocalCLIJob(jobID)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to load job %s: %v\n", jobID, err)
		return cliExitGeneral
	}
	record.Status = jobStatusRunning
	record.StartedAt = time.Now().UTC().Format(time.RFC3339)
	record.PID = os.Getpid()
	record.Result = nil
	record.ExitCode = 0
	_ = saveLocalCLIJob(record)

	stdoutFile, err := os.Create(record.StdoutPath)
	if err != nil {
		record.Status = jobStatusFailed
		record.ExitCode = cliExitGeneral
		record.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		_ = saveLocalCLIJob(record)
		_, _ = fmt.Fprintf(os.Stderr, "failed to open job stdout: %v\n", err)
		return cliExitGeneral
	}
	defer stdoutFile.Close()

	stderrFile, err := os.Create(record.StderrPath)
	if err != nil {
		record.Status = jobStatusFailed
		record.ExitCode = cliExitGeneral
		record.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		_ = saveLocalCLIJob(record)
		_, _ = fmt.Fprintf(os.Stderr, "failed to open job stderr: %v\n", err)
		return cliExitGeneral
	}
	defer stderrFile.Close()

	childArgs := append(globalCLIFlagArgs(), commandArgs...)
	cmd := exec.Command(os.Args[0], childArgs...)
	cmd.Dir = record.WorkingDir
	cmd.Env = setEnvValue(append([]string(nil), os.Environ()...), "GS_NON_INTERACTIVE", "1")
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = cliExitGeneral
		}
	}

	record.ExitCode = exitCode
	record.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if exitCode == 0 {
		record.Status = jobStatusSucceeded
	} else {
		record.Status = jobStatusFailed
	}
	if stdoutBytes, err := os.ReadFile(record.StdoutPath); err == nil {
		trimmed := bytes.TrimSpace(stdoutBytes)
		if len(trimmed) > 0 && json.Valid(trimmed) {
			record.Result = append(json.RawMessage(nil), trimmed...)
		}
	}
	_ = saveLocalCLIJob(record)
	return exitCode
}

func waitForLocalCLIJob(id string, timeout time.Duration) (*localCLIJobRecord, error) {
	deadline := time.Now().Add(timeout)
	for {
		record, err := loadLocalCLIJob(id)
		if err != nil {
			return nil, err
		}
		switch record.Status {
		case jobStatusSucceeded, jobStatusFailed:
			return record, nil
		}
		if timeout > 0 && time.Now().After(deadline) {
			return record, contextDeadlineExceededError(timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func newLocalCLIJobID() (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("job_%d_%s", time.Now().UTC().Unix(), hex.EncodeToString(suffix[:])), nil
}

func contextDeadlineExceededError(timeout time.Duration) error {
	return fmt.Errorf("timed out waiting for job after %s", timeout)
}
