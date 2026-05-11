package gscli

import (
	"flag"
	"os"

	"github.com/spf13/cobra"
)

func NewRootCommand(args []string) *cobra.Command {
	cmd := &cobra.Command{
		Use:                "gs <command> [options]",
		Short:              "Git Slice command line client",
		SilenceErrors:      true,
		SilenceUsage:       true,
		DisableFlagParsing: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		Run: func(cmd *cobra.Command, args []string) {
			runCobraRoot(args)
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printHelp()
	})
	if shouldRegisterLocalCobraCommands(args) {
		cmd.AddCommand(
			newAgentCommand(),
			newCacheCommand(),
			newUpdateCommand(),
			newJobsCommand(),
			newCheckoutWatcherCommand(),
			newDetachedJobRunnerCommand(),
			newAuthCommand(),
			newGitCommand(),
			newConfigCommand(),
			newLoginCommand(),
			newLogoutCommand(),
			newFileCommand(),
			newDoctorCommand(),
			newContextCommand(),
			newCICommand(),
			newRunnerCommand(),
			newSliceCommand(),
			newChangesetCommand(),
			newConflictCommand(),
			newImportCommand(),
			newRepoCommand(),
			newFilesystemCommand(),
			newStatusCommand(),
			newInitCommand(),
			newLogCommand(),
			newRootSliceCommand(),
		)
	}
	cmd.SetArgs(args)
	return cmd
}

func shouldRegisterLocalCobraCommands(args []string) bool {
	return len(args) == 0 || args[0] == "" || args[0][0] != '-'
}

func isHelpRequest(args []string) bool {
	return len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help")
}

func runCobraRoot(args []string) {
	if isVersionRequest(args) {
		printVersion()
		return
	}
	if isHelpRequest(args) {
		printHelp()
		return
	}
	if err := flag.CommandLine.Parse(args); err != nil {
		os.Exit(2)
	}
	parsedGlobalFlagNames = collectGlobalFlagNames(args)
	remaining := flag.Args()
	if len(remaining) == 0 {
		printHelp()
		return
	}
	configureCLIOutputMode(remaining)
	remaining = configureCLIBehavior(remaining)
	if err := NewRootCommand(remaining).Execute(); err != nil {
		commandFatal("INVALID_ARGUMENT", err.Error(), false, "gs --help")
	}
}

func isVersionRequest(args []string) bool {
	return len(args) == 1 && (args[0] == "--version" || args[0] == "-v")
}
