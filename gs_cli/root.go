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
		Run: func(cmd *cobra.Command, args []string) {
			runCobraRoot(args)
		},
	}
	cmd.SetArgs(args)
	return cmd
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
