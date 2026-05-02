package gscli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestNewCLIReusesConnectionsForSameAddress(t *testing.T) {
	addr := "passthrough:///shared-target"

	cli, err := NewCLI(addr, addr, addr, addr, addr, false)
	if err != nil {
		t.Fatalf("NewCLI failed: %v", err)
	}
	defer cli.Close()

	if cli.accountConn == nil || cli.sliceConn == nil || cli.adminConn == nil || cli.fileConn == nil || cli.filesystemConn == nil {
		t.Fatal("expected all service connections to be initialized")
	}
	if cli.accountConn != cli.sliceConn || cli.accountConn != cli.adminConn || cli.accountConn != cli.fileConn || cli.accountConn != cli.filesystemConn {
		t.Fatal("expected same-address services to share a single gRPC connection")
	}
	if len(cli.conns) != 1 {
		t.Fatalf("expected 1 unique connection, got %d", len(cli.conns))
	}
}

func TestNewCLIKeepsDistinctConnectionsForDistinctAddresses(t *testing.T) {
	cli, err := NewCLI(
		"passthrough:///account-target",
		"passthrough:///slice-target",
		"passthrough:///admin-target",
		"passthrough:///file-target",
		"passthrough:///fs-target",
		false,
	)
	if err != nil {
		t.Fatalf("NewCLI failed: %v", err)
	}
	defer cli.Close()

	if len(cli.conns) != 5 {
		t.Fatalf("expected 5 unique connections, got %d", len(cli.conns))
	}
	if cli.accountConn == cli.sliceConn || cli.accountConn == cli.adminConn || cli.accountConn == cli.fileConn || cli.accountConn == cli.filesystemConn {
		t.Fatal("expected distinct-address services to keep distinct connections")
	}
}

func TestConfigureCLIBehaviorConsumesNonInteractiveFlag(t *testing.T) {
	originalFlag := *nonInteractive
	originalMode := cliNonInteractive
	defer func() {
		*nonInteractive = originalFlag
		cliNonInteractive = originalMode
	}()

	*nonInteractive = false
	t.Setenv("GS_NON_INTERACTIVE", "")

	args := configureCLIBehavior([]string{"login", "--non-interactive"})
	if !cliNonInteractive {
		t.Fatal("expected non-interactive mode to be enabled")
	}
	if len(args) != 1 || args[0] != "login" {
		t.Fatalf("unexpected remaining args: %#v", args)
	}
}

func TestConfigureCLIBehaviorHonorsEnvAndGlobalFlag(t *testing.T) {
	originalFlag := *nonInteractive
	originalMode := cliNonInteractive
	defer func() {
		*nonInteractive = originalFlag
		cliNonInteractive = originalMode
	}()

	*nonInteractive = false
	t.Setenv("GS_NON_INTERACTIVE", "true")
	args := configureCLIBehavior([]string{"status"})
	if !cliNonInteractive {
		t.Fatal("expected env to enable non-interactive mode")
	}
	if len(args) != 1 || args[0] != "status" {
		t.Fatalf("unexpected remaining args from env case: %#v", args)
	}

	*nonInteractive = true
	t.Setenv("GS_NON_INTERACTIVE", "")
	args = configureCLIBehavior([]string{"context"})
	if !cliNonInteractive {
		t.Fatal("expected global flag to enable non-interactive mode")
	}
	if len(args) != 1 || args[0] != "context" {
		t.Fatalf("unexpected remaining args from flag case: %#v", args)
	}
}

func TestRootCommandDelegatesHelpToLegacyHelp(t *testing.T) {
	output := captureStdout(t, func() {
		if err := NewRootCommand([]string{"help"}).Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	})

	if !strings.Contains(output, "Usage: gs <command> [options]") {
		t.Fatalf("expected legacy usage in help output, got: %s", output)
	}
	if !strings.Contains(output, "Commands:") {
		t.Fatalf("expected command list in help output, got: %s", output)
	}
}

func TestRootCommandPrintsCommandSpecificHelp(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		wantUsage   string
		wantExample string
	}{
		{
			name:        "context",
			args:        []string{"context", "--help"},
			wantUsage:   "Usage: gs context [options]",
			wantExample: "gs context --json",
		},
		{
			name:        "changeset",
			args:        []string{"changeset", "--help"},
			wantUsage:   "Usage: gs changeset <command> [options]",
			wantExample: "gs changeset show --json",
		},
		{
			name:        "status",
			args:        []string{"status", "--help"},
			wantUsage:   "Usage: gs status [options]",
			wantExample: "gs status --json",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := captureStdout(t, func() {
				if err := NewRootCommand(tc.args).Execute(); err != nil {
					t.Fatalf("Execute failed: %v", err)
				}
			})

			if !strings.Contains(output, tc.wantUsage) {
				t.Fatalf("expected command usage %q in help output, got: %s", tc.wantUsage, output)
			}
			if !strings.Contains(output, tc.wantExample) {
				t.Fatalf("expected example %q in help output, got: %s", tc.wantExample, output)
			}
		})
	}
}

func TestRootCommandRegistersLocalOnlyCommands(t *testing.T) {
	cmd := NewRootCommand(nil)
	for _, name := range []string{
		"cache", "jobs", "__watch-checkout", "__run-job",
		"auth", "git", "login", "logout",
		"file", "doctor", "context", "slice",
		"changeset", "conflict", "import", "repo", "fs",
		"status", "init", "log", "root",
	} {
		child, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find(%q) failed: %v", name, err)
		}
		if child == nil || child.Name() != name {
			t.Fatalf("expected %q command to be registered, got %#v", name, child)
		}
	}
}

func TestGitCredentialOutputUsesBearerToken(t *testing.T) {
	var buf bytes.Buffer
	err := writeGitCredential(&buf, cliAuth{
		Authorization: "Bearer token-123",
		Username:      "alice",
	})
	if err != nil {
		t.Fatalf("writeGitCredential failed: %v", err)
	}
	if got, want := buf.String(), "username=alice\npassword=token-123\n\n"; got != want {
		t.Fatalf("credential output = %q, want %q", got, want)
	}
}

func TestGitCredentialOutputRejectsLegacyAuth(t *testing.T) {
	var buf bytes.Buffer
	err := writeGitCredential(&buf, cliAuth{
		Authorization: "User alice",
		Username:      "alice",
	})
	if err == nil {
		t.Fatal("expected legacy auth to be rejected")
	}
}

func TestRootCommandRunsLocalOnlyCacheCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	output := captureStdout(t, func() {
		if err := NewRootCommand([]string{"cache", "stats", "--json"}).Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	})

	var decoded struct {
		CacheRoot string `json:"cache_root"`
	}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("decode cache stats JSON: %v\n%s", err, output)
	}
	if !strings.Contains(decoded.CacheRoot, home) {
		t.Fatalf("expected cache root under temp HOME %q, got %q", home, decoded.CacheRoot)
	}
}

func TestRootCommandKeepsLeadingGlobalFlagsCompatibleForLocalCommands(t *testing.T) {
	originalFlag := *nonInteractive
	originalMode := cliNonInteractive
	defer func() {
		*nonInteractive = originalFlag
		cliNonInteractive = originalMode
	}()
	*nonInteractive = false
	cliNonInteractive = false

	home := t.TempDir()
	t.Setenv("HOME", home)
	output := captureStdout(t, func() {
		if err := NewRootCommand([]string{"--non-interactive", "cache", "stats", "--json"}).Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	})

	var decoded struct {
		CacheRoot string `json:"cache_root"`
	}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("decode cache stats JSON: %v\n%s", err, output)
	}
	if !strings.Contains(decoded.CacheRoot, home) {
		t.Fatalf("expected cache root under temp HOME %q, got %q", home, decoded.CacheRoot)
	}
	if !cliNonInteractive {
		t.Fatal("expected leading --non-interactive to be honored")
	}
}

func TestRootCommandKeepsSliceCheckoutsLocalOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	output := captureStdout(t, func() {
		if err := NewRootCommand([]string{"slice", "checkouts"}).Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	})

	if !strings.Contains(output, "Tracked checkouts: 0") {
		t.Fatalf("expected local checkout summary, got: %s", output)
	}
}

func TestRootCommandRunsAuthStatusCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	output := captureStdout(t, func() {
		if err := NewRootCommand([]string{"auth", "status", "--json"}).Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	})

	var decoded struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("decode auth status JSON: %v\n%s", err, output)
	}
	if decoded.Authenticated {
		t.Fatalf("expected temp HOME to be unauthenticated, got: %s", output)
	}
}

func TestRootCommandRunsLocalOnlyJobsCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	output := captureStdout(t, func() {
		if err := NewRootCommand([]string{"jobs", "list", "--json"}).Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	})

	var decoded struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("decode jobs list JSON: %v\n%s", err, output)
	}
	if decoded.Total != 0 {
		t.Fatalf("expected no jobs in temp HOME, got %d", decoded.Total)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = original
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return buf.String()
}
