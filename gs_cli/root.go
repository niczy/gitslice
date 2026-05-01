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
			newCacheCommand(),
			newJobsCommand(),
			newCheckoutWatcherCommand(),
			newDetachedJobRunnerCommand(),
		)
	}
	cmd.SetArgs(args)
	return cmd
}

func shouldRegisterLocalCobraCommands(args []string) bool {
	return len(args) == 0 || args[0] == "" || args[0][0] != '-'
}

func runCobraRoot(args []string) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printHelp()
		return
	}
	if err := flag.CommandLine.Parse(args); err != nil {
		os.Exit(2)
	}
	runLegacyCommand(flag.Args())
}
