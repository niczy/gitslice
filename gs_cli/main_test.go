package gscli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
			name:        "config",
			args:        []string{"config", "--help"},
			wantUsage:   "Usage: gs config <command> [options]",
			wantExample: "gs config endpoint set api.agenttools.dev:443 --tls",
		},
		{
			name:        "update",
			args:        []string{"update", "--help"},
			wantUsage:   "Usage: gs update [options]",
			wantExample: "gs upgrade --dry-run",
		},
		{
			name:        "status",
			args:        []string{"status", "--help"},
			wantUsage:   "Usage: gs status [options]",
			wantExample: "gs status --json",
		},
		{
			name:        "ci",
			args:        []string{"ci", "--help"},
			wantUsage:   "Usage: gs ci <command> [options]",
			wantExample: "gs ci status --json",
		},
		{
			name:        "runner",
			args:        []string{"runner", "--help"},
			wantUsage:   "Usage: gs runner <command> [options]",
			wantExample: "gs runner pool list --json",
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
		"cache", "update", "jobs", "__watch-checkout", "__run-job",
		"auth", "git", "config", "login", "logout",
		"file", "doctor", "context", "slice",
		"ci", "runner", "changeset", "conflict", "import", "repo", "fs",
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

func TestRootCommandRegistersUpgradeAlias(t *testing.T) {
	cmd := NewRootCommand(nil)
	child, _, err := cmd.Find([]string{"upgrade"})
	if err != nil {
		t.Fatalf("Find(upgrade) failed: %v", err)
	}
	if child == nil || child.Name() != "update" {
		t.Fatalf("expected upgrade alias to resolve to update command, got %#v", child)
	}
}

func TestRootCommandPrintsVersion(t *testing.T) {
	for _, arg := range []string{"--version", "-v"} {
		t.Run(arg, func(t *testing.T) {
			output := captureStdout(t, func() {
				if err := NewRootCommand([]string{arg}).Execute(); err != nil {
					t.Fatalf("Execute failed: %v", err)
				}
			})
			if !strings.Contains(output, "gs ") || !strings.Contains(output, "commit") {
				t.Fatalf("unexpected version output: %s", output)
			}
		})
	}
}

func TestUpdateCommandDryRunDoesNotInstall(t *testing.T) {
	installDir := t.TempDir()
	output := captureStdout(t, func() {
		if err := NewRootCommand([]string{"upgrade", "--dry-run", "--repo", "https://example.com/repo.git", "--ref", "test-ref", "--install-dir", installDir}).Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	})
	if !strings.Contains(output, "https://example.com/repo.git") || !strings.Contains(output, "test-ref") || !strings.Contains(output, "Dry run") {
		t.Fatalf("unexpected dry-run output: %s", output)
	}
	if _, err := os.Stat(filepath.Join(installDir, "gs")); !os.IsNotExist(err) {
		t.Fatalf("dry run should not install gs, stat err: %v", err)
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

func TestConfigEndpointSetPersistsAndShows(t *testing.T) {
	resetEndpointFlagState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	output := captureStdout(t, func() {
		if err := NewRootCommand([]string{"config", "endpoint", "set", "api.agenttools.dev:443", "--tls", "--json"}).Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	})

	var saved jsonEndpointConfigOutput
	if err := json.Unmarshal([]byte(output), &saved); err != nil {
		t.Fatalf("decode saved endpoint JSON: %v\n%s", err, output)
	}
	if saved.Status != "saved" || saved.Addr != "api.agenttools.dev:443" || !saved.TLS {
		t.Fatalf("unexpected saved endpoint output: %#v", saved)
	}
	if saved.ConfigPath != filepath.Join(home, ".gitslice", "config.json") {
		t.Fatalf("unexpected config path %q", saved.ConfigPath)
	}

	cfg, present, err := readEndpointConfig()
	if err != nil {
		t.Fatalf("read endpoint config: %v", err)
	}
	if !present {
		t.Fatal("expected endpoint config to exist")
	}
	if cfg.Addr != "api.agenttools.dev:443" || cfg.TLS == nil || !*cfg.TLS {
		t.Fatalf("unexpected persisted endpoint config: %#v", cfg)
	}

	output = captureStdout(t, func() {
		if err := NewRootCommand([]string{"config", "endpoint", "--json"}).Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	})
	var shown jsonEndpointConfigOutput
	if err := json.Unmarshal([]byte(output), &shown); err != nil {
		t.Fatalf("decode shown endpoint JSON: %v\n%s", err, output)
	}
	if !shown.ConfigPresent || shown.Addr != "api.agenttools.dev:443" || !shown.TLS {
		t.Fatalf("unexpected shown endpoint output: %#v", shown)
	}
}

func TestConfigEndpointSetSharedAddrWithoutTLSClearsPersistedTLS(t *testing.T) {
	resetEndpointFlagState(t)
	t.Setenv("HOME", t.TempDir())
	tlsEnabled := true
	if err := writeEndpointConfig(cliEndpointConfig{Addr: "api.agenttools.dev:443", TLS: &tlsEnabled}); err != nil {
		t.Fatalf("write endpoint config: %v", err)
	}

	output := captureStdout(t, func() {
		if err := NewRootCommand([]string{"config", "endpoint", "set", "127.0.0.1:50051", "--json"}).Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	})

	var saved jsonEndpointConfigOutput
	if err := json.Unmarshal([]byte(output), &saved); err != nil {
		t.Fatalf("decode saved endpoint JSON: %v\n%s", err, output)
	}
	if saved.Status != "saved" || saved.Addr != "127.0.0.1:50051" || saved.TLS {
		t.Fatalf("unexpected saved endpoint output: %#v", saved)
	}
	if saved.TLSSource != "default" {
		t.Fatalf("expected TLS source to reset to default, got %q", saved.TLSSource)
	}

	cfg, present, err := readEndpointConfig()
	if err != nil {
		t.Fatalf("read endpoint config: %v", err)
	}
	if !present {
		t.Fatal("expected endpoint config to exist")
	}
	if cfg.Addr != "127.0.0.1:50051" || cfg.TLS != nil {
		t.Fatalf("unexpected persisted endpoint config: %#v", cfg)
	}
}

func TestConfigEndpointClearRemovesPersistedConfig(t *testing.T) {
	resetEndpointFlagState(t)
	t.Setenv("HOME", t.TempDir())
	tlsEnabled := true
	if err := writeEndpointConfig(cliEndpointConfig{Addr: "api.agenttools.dev:443", TLS: &tlsEnabled}); err != nil {
		t.Fatalf("write endpoint config: %v", err)
	}

	output := captureStdout(t, func() {
		if err := NewRootCommand([]string{"config", "endpoint", "clear", "--json"}).Execute(); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	})

	var cleared jsonEndpointConfigOutput
	if err := json.Unmarshal([]byte(output), &cleared); err != nil {
		t.Fatalf("decode cleared endpoint JSON: %v\n%s", err, output)
	}
	if cleared.Status != "cleared" || cleared.ConfigPresent || cleared.Addr != defaultGRPCServerAddr || cleared.TLS {
		t.Fatalf("unexpected cleared endpoint output: %#v", cleared)
	}
	_, present, err := readEndpointConfig()
	if err != nil {
		t.Fatalf("read endpoint config: %v", err)
	}
	if present {
		t.Fatal("expected endpoint config to be removed")
	}
}

func TestResolveEndpointSettingsHonorsExplicitGlobalOverrides(t *testing.T) {
	resetEndpointFlagState(t)
	t.Setenv("HOME", t.TempDir())
	tlsEnabled := true
	if err := writeEndpointConfig(cliEndpointConfig{Addr: "api.agenttools.dev:443", TLS: &tlsEnabled}); err != nil {
		t.Fatalf("write endpoint config: %v", err)
	}

	parsedGlobalFlagNames = map[string]bool{
		"account-addr": true,
		"tls":          true,
	}
	*accountServerAddr = defaultGRPCServerAddr
	*useTLS = false

	settings, err := resolveEndpointSettings()
	if err != nil {
		t.Fatalf("resolve endpoint settings: %v", err)
	}
	if settings.AccountAddr != defaultGRPCServerAddr {
		t.Fatalf("expected explicit account addr to override config, got %q", settings.AccountAddr)
	}
	if settings.SliceAddr != "api.agenttools.dev:443" || settings.AdminAddr != "api.agenttools.dev:443" {
		t.Fatalf("expected non-overridden services to use config, got %#v", settings)
	}
	if settings.TLS {
		t.Fatal("expected explicit --tls=false to override persisted TLS")
	}
}

func resetEndpointFlagState(t *testing.T) {
	t.Helper()

	originalCore := *coreServerAddr
	originalAccount := *accountServerAddr
	originalSlice := *sliceServerAddr
	originalAdmin := *adminServerAddr
	originalFile := *fileServerAddr
	originalFS := *fsServerAddr
	originalTLS := *useTLS
	originalParsed := parsedGlobalFlagNames
	originalJSON := cliStructuredJSON
	t.Cleanup(func() {
		*coreServerAddr = originalCore
		*accountServerAddr = originalAccount
		*sliceServerAddr = originalSlice
		*adminServerAddr = originalAdmin
		*fileServerAddr = originalFile
		*fsServerAddr = originalFS
		*useTLS = originalTLS
		parsedGlobalFlagNames = originalParsed
		cliStructuredJSON = originalJSON
	})

	*coreServerAddr = ""
	*accountServerAddr = defaultGRPCServerAddr
	*sliceServerAddr = defaultGRPCServerAddr
	*adminServerAddr = defaultGRPCServerAddr
	*fileServerAddr = defaultGRPCServerAddr
	*fsServerAddr = defaultGRPCServerAddr
	*useTLS = false
	parsedGlobalFlagNames = nil
	cliStructuredJSON = false
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
