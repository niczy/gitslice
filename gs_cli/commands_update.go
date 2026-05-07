package gscli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const defaultUpdateRepoURL = "https://github.com/niczy/gitslice.git"

type updateOptions struct {
	RepoURL    string `json:"repo_url"`
	Ref        string `json:"ref"`
	InstallDir string `json:"install_dir"`
	DryRun     bool   `json:"dry_run,omitempty"`
}

type updateOutput struct {
	Status string `json:"status"`
	updateOptions
	BinaryPath string `json:"binary_path,omitempty"`
}

func newUpdateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "update [options]",
		Aliases:            []string{"upgrade"},
		Short:              "Update the gs CLI",
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			if isHelpRequest(args) {
				_ = cmd.Help()
				return
			}
			configureCLIOutputMode(args)
			handleUpdateCommand(args)
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printUpdateHelp()
	})
	return cmd
}

func handleUpdateCommand(args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("update")
	repoURL := fs.String("repo", envWithDefault("GITSLICE_REPO_URL", defaultUpdateRepoURL), "Git repository URL to install from")
	ref := fs.String("ref", envWithDefault("GITSLICE_REF", "main"), "Git branch, tag, or ref to install")
	installDir := fs.String("install-dir", os.Getenv("GITSLICE_INSTALL_DIR"), "Directory where gs should be installed")
	dryRun := fs.Bool("dry-run", false, "Print the update plan without changing files")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 0 {
		commandUsage("Usage: gs update [--repo <url>] [--ref <ref>] [--install-dir <dir>] [--dry-run] [--json]")
		return
	}

	opts := updateOptions{
		RepoURL:    strings.TrimSpace(*repoURL),
		Ref:        strings.TrimSpace(*ref),
		InstallDir: strings.TrimSpace(*installDir),
		DryRun:     *dryRun,
	}
	if opts.RepoURL == "" {
		opts.RepoURL = defaultUpdateRepoURL
	}
	if opts.Ref == "" {
		opts.Ref = "main"
	}
	if opts.InstallDir == "" {
		dir, err := currentExecutableDir()
		if err != nil {
			commandFatalf("UPDATE_FAILED", false, "", "Failed to resolve current gs location: %v", err)
		}
		opts.InstallDir = dir
	}

	if opts.DryRun {
		if jsonEnabled {
			writeJSONOutput(updateOutput{Status: "dry_run", updateOptions: opts, BinaryPath: filepath.Join(opts.InstallDir, "gs")})
			return
		}
		fmt.Printf("Updating gs from %s (%s)\n", opts.RepoURL, opts.Ref)
		fmt.Printf("Install directory: %s\n", opts.InstallDir)
		fmt.Println("Dry run: no changes made")
		return
	}

	if !jsonEnabled {
		fmt.Printf("Updating gs from %s (%s)\n", opts.RepoURL, opts.Ref)
		fmt.Printf("Install directory: %s\n", opts.InstallDir)
	}

	if err := runCLIUpdate(opts, jsonEnabled); err != nil {
		commandFatalf("UPDATE_FAILED", false, "", "Failed to update gs: %v", err)
	}
	binaryPath := filepath.Join(opts.InstallDir, "gs")
	if jsonEnabled {
		writeJSONOutput(updateOutput{Status: "updated", updateOptions: opts, BinaryPath: binaryPath})
	} else {
		fmt.Printf("Updated gs at %s\n", binaryPath)
	}
}

func runCLIUpdate(opts updateOptions, jsonMode bool) error {
	for _, tool := range []string{"go", "git", "make", "protoc"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s is required to update gs", tool)
		}
	}

	workdir, err := os.MkdirTemp("", "gitslice-update.*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	repoDir := filepath.Join(workdir, "repo")
	if err := runUpdateCommand(jsonMode, "", "git", "clone", "--depth", "1", "--branch", opts.Ref, opts.RepoURL, repoDir); err != nil {
		return err
	}
	if err := runUpdateCommand(jsonMode, repoDir, "make", "install"); err != nil {
		return err
	}
	if err := runUpdateCommand(jsonMode, repoDir, "make", "build-cli"); err != nil {
		return err
	}
	return installBuiltGS(filepath.Join(repoDir, "bin", "gs"), opts.InstallDir)
}

func runUpdateCommand(jsonMode bool, dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if jsonMode {
		cmd.Stdout = os.Stderr
	} else {
		cmd.Stdout = os.Stdout
	}
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func installBuiltGS(sourcePath, installDir string) error {
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return err
	}
	src, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer src.Close()

	targetPath := filepath.Join(installDir, "gs")
	tmp, err := os.CreateTemp(installDir, ".gs.new.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func currentExecutableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	realExe, err := filepath.EvalSymlinks(exe)
	if err == nil && realExe != "" {
		exe = realExe
	}
	return filepath.Dir(exe), nil
}

func envWithDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
