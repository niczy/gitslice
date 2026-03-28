package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
)

type jsonCLIErrorOutput struct {
	ErrorCode       string `json:"error_code"`
	Message         string `json:"message"`
	Retryable       bool   `json:"retryable,omitempty"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

var cliStructuredJSON bool
var cliNonInteractive bool

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
	log.Println(usage)
}

func commandFatal(code, message string, retryable bool, suggestedAction string) {
	if cliStructuredJSON {
		writeJSONOutput(jsonCLIErrorOutput{
			ErrorCode:       code,
			Message:         message,
			Retryable:       retryable,
			SuggestedAction: suggestedAction,
		})
		os.Exit(1)
	}
	log.Fatal(message)
}

func commandFatalf(code string, retryable bool, suggestedAction, format string, args ...any) {
	commandFatal(code, fmt.Sprintf(format, args...), retryable, suggestedAction)
}
