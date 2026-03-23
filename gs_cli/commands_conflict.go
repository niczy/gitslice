package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	adminv1 "github.com/niczy/gitslice/proto/admin"
)

func handleConflictCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		printConflictHelp()
		return
	}

	switch args[0] {
	case "list":
		handleConflictList(ctx, cli, args[1:])
	case "resolve":
		handleConflictResolve(ctx, cli, args[1:])
	case "show":
		handleConflictShow(ctx, cli, args[1:])
	default:
		log.Printf("Unknown conflict command: %s", args[0])
		printConflictHelp()
	}
}

func handleConflictList(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := flag.NewFlagSet("conflict list", flag.ExitOnError)
	sliceFlag := fs.String("slice", "", "Slice ID to inspect for conflicts")
	detailed := fs.Bool("detailed", false, "Show detailed conflict information")
	severity := fs.Bool("severity", false, "Show severity level")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	fs.Parse(args)
	jsonEnabled := jsonRequested || *jsonOutput

	sliceID := *sliceFlag
	if sliceID == "" {
		if cfgSlice, err := sliceIDFromConfig(); err == nil {
			sliceID = cfgSlice
		}
	}

	req := &adminv1.ConflictsRequest{}
	if sliceID != "" {
		req.SliceId = sliceID
	}

	resp, err := cli.adminClient.GetConflicts(ctx, req)
	if err != nil {
		log.Fatalf("Failed to list conflicts: %v", err)
	}
	if jsonEnabled {
		out := jsonConflictListOutput{
			SliceID:   sliceID,
			Total:     len(resp.Conflicts),
			Conflicts: make([]jsonConflictInfo, 0, len(resp.Conflicts)),
		}
		for _, conflict := range resp.Conflicts {
			out.Conflicts = append(out.Conflicts, buildConflictOutput(conflict))
		}
		writeJSONOutput(out)
		return
	}

	fmt.Printf("Found %d conflict(s)\n", len(resp.Conflicts))
	for _, conflict := range resp.Conflicts {
		severityLabel := ""
		if *severity {
			switch {
			case len(conflict.ConflictingSliceIds) >= 3:
				severityLabel = "HIGH"
			case len(conflict.ConflictingSliceIds) == 2:
				severityLabel = "MEDIUM"
			default:
				severityLabel = "LOW"
			}
		}

		line := fmt.Sprintf("- %s", conflict.FileId)
		if *detailed || len(conflict.ConflictingSliceIds) > 0 {
			line = fmt.Sprintf("%s (slices: %s)", line, strings.Join(conflict.ConflictingSliceIds, ", "))
		}
		if severityLabel != "" {
			line = fmt.Sprintf("%s severity: %s", line, severityLabel)
		}

		fmt.Println(line)
	}
}

func handleConflictResolve(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := flag.NewFlagSet("conflict resolve", flag.ExitOnError)
	theirs := fs.String("theirs", "", "Resolve in favor of provided slice ID")
	ours := fs.Bool("ours", false, "Resolve in favor of current slice")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	fs.Parse(args)
	jsonEnabled := jsonRequested || *jsonOutput

	remaining := fs.Args()
	if len(remaining) < 1 {
		log.Println("Usage: gs conflict resolve [--ours|--theirs <slice-id>] <file>")
		return
	}

	fileID := remaining[0]
	preferredSlice := *theirs
	if preferredSlice == "" {
		if *ours {
			cfgSlice, err := sliceIDFromConfig()
			if err != nil {
				log.Fatalf("Failed to read slice binding: %v", err)
			}
			preferredSlice = cfgSlice
		}
	}

	req := &adminv1.ResolveConflictRequest{FileId: fileID, PreferredSliceId: preferredSlice}
	resp, err := cli.adminClient.ResolveConflict(ctx, req)
	if err != nil {
		log.Fatalf("Failed to resolve conflict: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonConflictResolveOutput{
			Conflict: buildConflictOutput(resp.GetResolvedConflict()),
		})
		return
	}

	fmt.Printf("Resolved conflict for %s\n", resp.ResolvedConflict.FileId)
	fmt.Printf("Remaining ownership: %s\n", strings.Join(resp.ResolvedConflict.ConflictingSliceIds, ", "))
}

func handleConflictShow(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := flag.NewFlagSet("conflict show", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	fs.Parse(args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() < 1 {
		log.Println("Usage: gs conflict show <file>")
		return
	}

	fileID := fs.Arg(0)
	req := &adminv1.ConflictsRequest{}
	resp, err := cli.adminClient.GetConflicts(ctx, req)
	if err != nil {
		log.Fatalf("Failed to fetch conflicts: %v", err)
	}

	for _, conflict := range resp.Conflicts {
		if conflict.FileId == fileID {
			if jsonEnabled {
				out := jsonConflictShowOutput{
					Found:    true,
					Conflict: func() *jsonConflictInfo { c := buildConflictOutput(conflict); return &c }(),
				}
				writeJSONOutput(out)
				return
			}
			fmt.Printf("Conflict for %s\n", fileID)
			fmt.Printf("Conflicting slices: %s\n", strings.Join(conflict.ConflictingSliceIds, ", "))
			return
		}
	}
	if jsonEnabled {
		writeJSONOutput(jsonConflictShowOutput{Found: false})
		return
	}

	fmt.Printf("No conflict found for %s\n", fileID)
}
