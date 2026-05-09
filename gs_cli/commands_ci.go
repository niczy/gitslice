package gscli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCICommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "ci <command> [options]",
		Short:              "Run and inspect Gitslice CI checks",
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			if isHelpRequest(args) {
				_ = cmd.Help()
				return
			}
			handleCICommand(args)
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printCIHelp()
	})
	return cmd
}

func newRunnerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "runner <command> [options]",
		Short:              "Manage and run CI executors",
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			if isHelpRequest(args) {
				_ = cmd.Help()
				return
			}
			handleRunnerCommand(args)
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printRunnerHelp()
	})
	return cmd
}

func handleCICommand(args []string) {
	if len(args) == 0 {
		printCIHelp()
		return
	}
	commandFatal("NOT_IMPLEMENTED", fmt.Sprintf("gs ci %s is not implemented yet", args[0]), false, "See spec/ongoing_SLICE_CI_DESIGN.md")
}

func handleRunnerCommand(args []string) {
	if len(args) == 0 {
		printRunnerHelp()
		return
	}
	commandFatal("NOT_IMPLEMENTED", fmt.Sprintf("gs runner %s is not implemented yet", args[0]), false, "See spec/ongoing_SLICE_CI_DESIGN.md")
}
