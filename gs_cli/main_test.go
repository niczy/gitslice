package main

import "testing"

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
