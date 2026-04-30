package gscli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type jsonCLIErrorOutput struct {
	ErrorCode       string `json:"error_code"`
	ExitCode        int    `json:"exit_code,omitempty"`
	Message         string `json:"message"`
	Retryable       bool   `json:"retryable,omitempty"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

var cliStructuredJSON bool
var cliNonInteractive bool

const (
	cliExitGeneral   = 1
	cliExitUsage     = 2
	cliExitAuth      = 3
	cliExitNotFound  = 4
	cliExitState     = 5
	cliExitRetryable = 10
)

func configureCLIOutputMode(args []string) {
	_, cliStructuredJSON = consumeBoolFlag(args, "json")
}

func newCommandFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func parseCommandFlags(fs *flag.FlagSet, args []string) {
	if err := fs.Parse(args); err != nil {
		commandFatal("INVALID_ARGUMENT", err.Error(), false, "")
	}
}

func commandUsage(usage string) {
	if cliStructuredJSON {
		commandFatal("INVALID_ARGUMENT", usage, false, "")
	}
	_, _ = fmt.Fprintln(os.Stderr, usage)
}

func commandFatal(code, message string, retryable bool, suggestedAction string) {
	exitCode := exitCodeForCLIError(code, retryable)
	if cliStructuredJSON {
		writeJSONOutput(jsonCLIErrorOutput{
			ErrorCode:       code,
			ExitCode:        exitCode,
			Message:         message,
			Retryable:       retryable,
			SuggestedAction: suggestedAction,
		})
		os.Exit(exitCode)
	}
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(exitCode)
}

func commandFatalf(code string, retryable bool, suggestedAction, format string, args ...any) {
	commandFatal(code, fmt.Sprintf(format, args...), retryable, suggestedAction)
}

func exitCodeForCLIError(code string, retryable bool) int {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	switch normalized {
	case "INVALID_ARGUMENT", "INPUT_REQUIRED", "INTERACTIVE_REQUIRED", "INVALID_SLICE_REFERENCE":
		return cliExitUsage
	case "CHECKOUT_METADATA_MISSING", "DIRECTORY_NOT_EMPTY", "NO_LOCAL_CHANGES", "SLICE_NOT_BOUND", "WORKING_TREE_DIRTY":
		return cliExitState
	}
	if strings.HasPrefix(normalized, "AUTH_") {
		return cliExitAuth
	}
	if strings.Contains(normalized, "NOT_FOUND") {
		return cliExitNotFound
	}
	if retryable {
		return cliExitRetryable
	}
	return cliExitGeneral
}
