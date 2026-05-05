package gscli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "config <command> [options]",
		Short:              "Manage local CLI configuration",
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			if isHelpRequest(args) {
				_ = cmd.Help()
				return
			}
			runConfigCommand(args)
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printConfigHelp()
	})
	return cmd
}

func runConfigCommand(args []string) {
	args = configureCLIBehavior(args)
	configureCLIOutputMode(args)
	if len(args) == 0 {
		printConfigHelp()
		return
	}

	switch args[0] {
	case "endpoint":
		handleConfigEndpointCommand(args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown config command: %s", args[0]), false, "gs config --help")
	}
}

func handleConfigEndpointCommand(args []string) {
	if isHelpRequest(args) {
		printConfigHelp()
		return
	}
	if len(args) > 0 {
		switch args[0] {
		case "set":
			handleConfigEndpointSet(args[1:])
			return
		case "clear":
			handleConfigEndpointClear(args[1:])
			return
		}
		if !strings.HasPrefix(args[0], "-") {
			commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown config endpoint command: %s", args[0]), false, "gs config endpoint --help")
		}
	}
	handleConfigEndpointShow(args)
}

func handleConfigEndpointShow(args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("config endpoint")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 0 {
		commandUsage("Usage: gs config endpoint [--json]")
		return
	}

	settings, err := resolveEndpointSettings()
	if err != nil {
		commandFatalf("CONFIG_READ_FAILED", false, "", "Failed to read endpoint config: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(buildEndpointConfigOutput("", settings))
		return
	}
	printEndpointSettings(settings)
}

func handleConfigEndpointSet(args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	var tlsValue bool
	var tlsSet bool
	var err error
	args, tlsValue, tlsSet, err = consumeOptionalBoolFlag(args, "tls")
	if err != nil {
		commandFatal("INVALID_ARGUMENT", err.Error(), false, "gs config endpoint --help")
	}
	args, noTLSSet := consumeBoolFlag(args, "no-tls")
	if tlsSet && noTLSSet {
		commandFatal("INVALID_ARGUMENT", "Use either --tls/--tls=false or --no-tls, not both", false, "gs config endpoint --help")
	}
	if noTLSSet {
		tlsValue = false
		tlsSet = true
	}

	fs := newCommandFlagSet("config endpoint set")
	addr := fs.String("addr", "", "Set all service endpoints to this gRPC address")
	accountAddr := fs.String("account-addr", "", "Account service address")
	sliceAddr := fs.String("slice-addr", "", "Slice service address")
	adminAddr := fs.String("admin-addr", "", "Admin service address")
	fileAddr := fs.String("file-addr", "", "File service address")
	fsAddr := fs.String("fs-addr", "", "Filesystem service address")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() > 1 {
		commandUsage("Usage: gs config endpoint set [<addr>|--addr <addr>] [--tls|--tls=false|--no-tls] [--json]")
		return
	}
	if fs.NArg() == 1 && strings.TrimSpace(*addr) != "" {
		commandFatal("INVALID_ARGUMENT", "Provide either <addr> or --addr, not both", false, "gs config endpoint --help")
	}

	cfg, _, err := readEndpointConfig()
	if err != nil {
		commandFatalf("CONFIG_READ_FAILED", false, "", "Failed to read endpoint config: %v", err)
	}
	changed := false
	sharedAddr := strings.TrimSpace(*addr)
	if fs.NArg() == 1 {
		sharedAddr = strings.TrimSpace(fs.Arg(0))
	}
	if sharedAddr != "" {
		cfg.Addr = sharedAddr
		cfg.AccountAddr = ""
		cfg.SliceAddr = ""
		cfg.AdminAddr = ""
		cfg.FileAddr = ""
		cfg.FSAddr = ""
		changed = true
	}
	if serviceAddr := strings.TrimSpace(*accountAddr); serviceAddr != "" {
		cfg.AccountAddr = serviceAddr
		changed = true
	}
	if serviceAddr := strings.TrimSpace(*sliceAddr); serviceAddr != "" {
		cfg.SliceAddr = serviceAddr
		changed = true
	}
	if serviceAddr := strings.TrimSpace(*adminAddr); serviceAddr != "" {
		cfg.AdminAddr = serviceAddr
		changed = true
	}
	if serviceAddr := strings.TrimSpace(*fileAddr); serviceAddr != "" {
		cfg.FileAddr = serviceAddr
		changed = true
	}
	if serviceAddr := strings.TrimSpace(*fsAddr); serviceAddr != "" {
		cfg.FSAddr = serviceAddr
		changed = true
	}
	if tlsSet {
		cfg.TLS = &tlsValue
		changed = true
	}
	if !changed {
		commandUsage("Usage: gs config endpoint set [<addr>|--addr <addr>] [--tls|--tls=false|--no-tls] [--json]")
		return
	}

	if err := writeEndpointConfig(cfg); err != nil {
		commandFatalf("CONFIG_WRITE_FAILED", false, "", "Failed to write endpoint config: %v", err)
	}
	settings, err := resolveEndpointSettings()
	if err != nil {
		commandFatalf("CONFIG_READ_FAILED", false, "", "Failed to read endpoint config: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(buildEndpointConfigOutput("saved", settings))
		return
	}
	fmt.Printf("Saved endpoint config: %s\n", settings.ConfigPath)
	printEndpointSettings(settings)
}

func handleConfigEndpointClear(args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("config endpoint clear")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 0 {
		commandUsage("Usage: gs config endpoint clear [--json]")
		return
	}
	path, err := endpointConfigPath()
	if err != nil {
		commandFatalf("CONFIG_CLEAR_FAILED", false, "", "Failed to resolve endpoint config path: %v", err)
	}
	if err := removeEndpointConfig(); err != nil {
		commandFatalf("CONFIG_CLEAR_FAILED", false, "", "Failed to clear endpoint config: %v", err)
	}
	settings, err := resolveEndpointSettings()
	if err != nil {
		commandFatalf("CONFIG_READ_FAILED", false, "", "Failed to read endpoint config: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(buildEndpointConfigOutput("cleared", settings))
		return
	}
	fmt.Printf("Cleared endpoint config: %s\n", path)
	printEndpointSettings(settings)
}

func printEndpointSettings(settings cliEndpointSettings) {
	state := "not found"
	if settings.ConfigPresent {
		state = "present"
	}
	fmt.Println("Endpoint config:")
	fmt.Printf("  Config file: %s (%s)\n", settings.ConfigPath, state)
	if shared := settings.sharedAddr(); shared != "" {
		fmt.Printf("  Addr: %s\n", shared)
	}
	fmt.Printf("  TLS: %t\n", settings.TLS)
	fmt.Printf("  Account: %s\n", settings.AccountAddr)
	fmt.Printf("  Slice: %s\n", settings.SliceAddr)
	fmt.Printf("  Admin: %s\n", settings.AdminAddr)
	fmt.Printf("  File: %s\n", settings.FileAddr)
	fmt.Printf("  Filesystem: %s\n", settings.FSAddr)
	fmt.Printf("  Address source: %s\n", settings.AddressSource)
	fmt.Printf("  TLS source: %s\n", settings.TLSSource)
}
