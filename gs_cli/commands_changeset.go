package gscli

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func handleChangesetCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		printChangesetHelp()
		return
	}

	switch args[0] {
	case "create":
		handleChangesetCreate(ctx, cli, args[1:])
	case "show":
		handleChangesetShow(ctx, cli, args[1:])
	case "snapshots":
		handleChangesetSnapshots(ctx, cli, args[1:])
	case "review":
		handleChangesetReview(ctx, cli, args[1:])
	case "merge":
		handleChangesetMerge(ctx, cli, args[1:])
	case "close":
		handleChangesetClose(ctx, cli, args[1:])
	case "rebase":
		handleChangesetRebase(ctx, cli, args[1:])
	case "switch":
		handleChangesetSwitch(ctx, cli, args[1:])
	case "list":
		handleChangesetList(ctx, cli, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown changeset command: %s", args[0]), false, "gs changeset --help")
	}
}

func handleChangesetShow(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("changeset show")
	snapshotVersion := fs.Int("snapshot", 0, "Show a specific snapshot version")
	includePatches := fs.Bool("patches", false, "Include inline patch text when available")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() > 1 {
		commandUsage("Usage: gs changeset show [<changeset-id>] [--snapshot <version>] [--patches] [--json]")
		return
	}

	changesetID, err := resolveChangesetIDForRead("")
	if fs.NArg() == 1 {
		changesetID, err = resolveChangesetIDForRead(fs.Arg(0))
	}
	if err != nil {
		commandFatalf("CHANGESET_RESOLUTION_FAILED", false, "", "Failed to resolve changeset ID: %v", err)
		return
	}

	resp, err := cli.sliceClient.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{
		ChangesetId:     changesetID,
		SnapshotVersion: int32(*snapshotVersion),
	})
	if err != nil {
		commandFatalf("CHANGESET_SHOW_FAILED", true, "", "Failed to show changeset: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(buildChangesetReviewOutput(resp, *includePatches))
		return
	}
	printChangesetReview(resp, *includePatches)
}

func handleChangesetCreate(ctx context.Context, cli *CLI, args []string) {
	sliceID, err := sliceIDFromConfig()
	if err != nil {
		commandFatalf("SLICE_NOT_BOUND", false, "gs slice checkout <slice-id>", "Failed to read slice binding: %v", err)
		return
	}

	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("changeset create")
	message := fs.String("message", "", "Changeset message")
	base := fs.String("base", "", "Base commit hash")
	files := fs.String("files", "", "Comma-separated file list")
	author := fs.String("author", "user", "Author of the changeset")
	changesetID := fs.String("changeset-id", "", "Deprecated; use gs slice export to append to an existing changeset")
	replaceTracked := fs.Bool("replace-tracked", false, "Create a new changeset and replace the locally tracked changeset ID")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if strings.TrimSpace(*changesetID) != "" {
		commandFatal("INVALID_ARGUMENT", "gs changeset create always creates a new changeset; use gs slice export --changeset-id to append to an existing changeset.", false, "gs slice export --changeset-id "+strings.TrimSpace(*changesetID))
	}
	trackedChangesetID, err := readTrackedChangesetIDFromConfig()
	if err != nil {
		commandFatalf("CHANGESET_RESOLUTION_FAILED", false, "", "Failed to read tracked changeset ID: %v", err)
		return
	}
	trackedChangesetID = strings.TrimSpace(trackedChangesetID)
	if trackedChangesetID != "" && !*replaceTracked {
		commandFatalf(
			"CHANGESET_ALREADY_TRACKED",
			false,
			"gs slice export",
			"A changeset is already tracked: %s. Use gs slice export to append to it, gs changeset merge to merge it, or pass --replace-tracked to create and track a new changeset.",
			trackedChangesetID,
		)
	}

	modifiedFiles := []string{}
	if *files != "" {
		modifiedFiles = splitAndTrim(*files, ",")
	}
	modifiedFiles = append(modifiedFiles, fs.Args()...)
	modifiedFiles, _, err = resolveWorkingTreeModifiedFiles(".", modifiedFiles)
	if err != nil {
		commandFatalf("CHANGESET_CREATE_FAILED", false, "gs slice diff", "Cannot create changeset: %v", err)
	}
	if len(modifiedFiles) == 0 {
		commandFatal("NO_LOCAL_CHANGES", "No modified files specified and working tree is clean", false, "Edit files or pass --files explicitly")
	}
	fileContents, err := buildLocalChangesetFileContents(".", modifiedFiles)
	if err != nil {
		commandFatalf("CHANGESET_CREATE_FAILED", false, "gs slice diff", "Cannot read local changes: %v", err)
	}
	baseCommitHash, err := resolveCheckoutBaseCommit(".", *base)
	if err != nil {
		commandFatalf("CHANGESET_CREATE_FAILED", false, "gs slice status", "Cannot resolve checkout base commit: %v", err)
	}

	req := &slicev1.CreateChangesetRequest{
		SliceId:        sliceID,
		BaseCommitHash: baseCommitHash,
		ModifiedFiles:  modifiedFiles,
		Author:         *author,
		Message:        *message,
		FileContents:   fileContents,
	}

	resp, err := cli.sliceClient.CreateChangeset(ctx, req)
	if err != nil {
		commandFatalf("CHANGESET_CREATE_FAILED", true, "", "Failed to create changeset: %v", err)
	}
	if err := writeTrackedChangesetIDConfig(resp.GetChangesetId()); err != nil {
		log.Printf("Warning: failed to track changeset ID locally: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(buildChangesetCreateOutput(resp, false, sliceID, modifiedFiles))
		return
	}

	fmt.Printf("Created changeset %s (hash: %s)\n", resp.ChangesetId, resp.ChangesetHash)
	fmt.Printf("Status: %s\n", resp.Status.String())
	printChangesetCIFromRun(resp.GetCiStatus(), resp.GetCiRunId())
}

func resolveWorkingTreeModifiedFiles(dir string, explicit []string) ([]string, bool, error) {
	modifiedFiles := normalizeLocalModifiedFiles(explicit)
	checkoutIndex, err := detectCheckoutMode(dir)
	if err != nil {
		return nil, false, err
	}

	if len(modifiedFiles) == 0 {
		modifiedFiles, err = detectNoGitModifiedFiles(dir, checkoutIndex)
		if err != nil {
			return nil, false, err
		}
	}
	return modifiedFiles, false, nil
}

func resolveCheckoutBaseCommit(dir, explicit string) (string, error) {
	if base := strings.TrimSpace(explicit); base != "" {
		return base, nil
	}
	index, err := readCheckoutIndex(dir)
	if err != nil {
		return "", err
	}
	if index == nil {
		return "", nil
	}
	return strings.TrimSpace(index.CommitHash), nil
}

func normalizeLocalModifiedFiles(files []string) []string {
	if len(files) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(files))
	out := make([]string, 0, len(files))
	for _, raw := range files {
		cleaned := filepath.Clean(strings.TrimSpace(raw))
		if cleaned == "" || cleaned == "." {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	sort.Strings(out)
	return out
}

func resolveChangesetIDForExport(explicit string) (string, bool, error) {
	return resolveChangesetIDForExportAt(".", explicit)
}

func resolveChangesetIDForExportAt(dir, explicit string) (string, bool, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit, true, nil
	}
	tracked, err := readTrackedChangesetIDFromConfigAt(dir)
	if err != nil {
		return "", false, err
	}
	tracked = strings.TrimSpace(tracked)
	if tracked != "" {
		return tracked, true, nil
	}
	return "", false, nil
}

func handleChangesetReview(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("changeset review")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	changesetID, err := resolveChangesetIDForRead("")
	switch fs.NArg() {
	case 0:
	case 1:
		changesetID, err = resolveChangesetIDForRead(fs.Arg(0))
	default:
		commandUsage("Usage: gs changeset review [<changeset-id>] [--json]")
		return
	}
	if err != nil {
		commandFatalf("CHANGESET_RESOLUTION_FAILED", false, "", "Failed to resolve changeset ID: %v", err)
		return
	}

	resp, err := cli.sliceClient.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: changesetID})
	if err != nil {
		commandFatalf("CHANGESET_REVIEW_FAILED", true, "", "Failed to review changeset: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(buildChangesetReviewOutput(resp, false))
		return
	}
	printChangesetReview(resp, false)
}

func handleChangesetMerge(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("changeset merge")
	waitForProjections := fs.Bool("wait", false, "Wait for merge projections before returning")
	waitTimeout := fs.Duration("wait-timeout", 30*time.Second, "Maximum time to wait for merge projections")
	force := fs.Bool("force", false, "Bypass CI merge gates when policy allows it")
	forceReason := fs.String("reason", "", "Required reason when using --force")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	changesetID, err := resolveChangesetIDForRead("")
	if fs.NArg() == 1 {
		changesetID, err = resolveChangesetIDForRead(fs.Arg(0))
	} else if fs.NArg() > 1 {
		commandUsage("Usage: gs changeset merge [<changeset-id>] [--force --reason <text>] [--wait] [--wait-timeout <duration>] [--json]")
		return
	}
	if *waitTimeout < 0 {
		commandFatal("INVALID_ARGUMENT", "--wait-timeout must be non-negative", false, "")
	}
	if *force && strings.TrimSpace(*forceReason) == "" {
		commandFatal("INVALID_ARGUMENT", "--reason is required with --force", false, "gs changeset merge --force --reason \"why this is safe\"")
	}
	if err != nil {
		commandFatalf("CHANGESET_RESOLUTION_FAILED", false, "", "Failed to resolve changeset ID: %v", err)
		return
	}

	req := &slicev1.MergeChangesetRequest{
		ChangesetId: changesetID,
		Force:       *force,
		ForceReason: strings.TrimSpace(*forceReason),
	}
	resp, err := cli.sliceClient.MergeChangeset(ctx, req)
	if err != nil {
		commandFatalf("CHANGESET_MERGE_FAILED", true, "gs slice sync", "Failed to merge changeset: %v", err)
	}
	if resp.GetStatus() == slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		if err := clearTrackedChangesetIDIfMatches(req.ChangesetId); err != nil {
			log.Printf("Warning: failed to clear tracked changeset ID: %v", err)
		}
		if *waitForProjections {
			resp, err = waitForMergeProjections(ctx, cli, resp, *waitTimeout)
			if err != nil {
				commandFatalf("PROJECTION_WAIT_FAILED", true, "gs changeset merge --wait --wait-timeout 60s", "Failed waiting for merge projections: %v", err)
			}
		}
	}

	if jsonEnabled {
		writeJSONOutput(buildMergeOutput(resp))
		return
	}
	printMergeResult(resp)
}

func waitForMergeProjections(ctx context.Context, cli *CLI, resp *slicev1.MergeChangesetResponse, timeout time.Duration) (*slicev1.MergeChangesetResponse, error) {
	if resp == nil {
		return resp, nil
	}
	deadline := time.Now().Add(timeout)
	for i, projection := range resp.GetProjections() {
		if projection == nil || projection.GetRequestedSeq() <= 0 || projection.GetProjectionName() == "" {
			continue
		}
		projectionTimeout := timeout
		if timeout > 0 {
			projectionTimeout = time.Until(deadline)
			if projectionTimeout < 0 {
				projectionTimeout = 0
			}
		}
		updated, err := waitForMergeProjection(ctx, cli, projection, projectionTimeout)
		if err != nil {
			return resp, err
		}
		if i >= 0 && i < len(resp.Projections) {
			resp.Projections[i] = updated
		}
	}
	return resp, nil
}

func waitForMergeProjection(ctx context.Context, cli *CLI, projection *slicev1.ProjectionStatus, timeout time.Duration) (*slicev1.ProjectionStatus, error) {
	if projection == nil || projection.GetRequestedSeq() <= 0 {
		return projection, nil
	}
	deadline := time.Now().Add(timeout)
	for {
		wait := timeout
		if timeout > 0 {
			wait = time.Until(deadline)
			if wait < 0 {
				wait = 0
			}
			if wait > 30*time.Second {
				wait = 30 * time.Second
			}
		}
		updated, err := cli.sliceClient.GetProjectionStatus(ctx, &slicev1.GetProjectionStatusRequest{
			ProjectionName: projection.GetProjectionName(),
			ShardId:        projection.GetShardId(),
			MergeSeq:       projection.GetRequestedSeq(),
			WaitMs:         durationMilliseconds(wait),
		})
		if err != nil {
			return projection, err
		}
		if updated.GetState() == slicev1.ProjectionState_PROJECTION_STATE_CAUGHT_UP {
			return updated, nil
		}
		if timeout <= 0 || !time.Now().Before(deadline) {
			return updated, fmt.Errorf("%s is pending: applied=%d requested=%d", updated.GetProjectionName(), updated.GetAppliedSeq(), updated.GetRequestedSeq())
		}
	}
}

func durationMilliseconds(duration time.Duration) int32 {
	if duration <= 0 {
		return 0
	}
	ms := duration.Milliseconds()
	if ms > 30000 {
		ms = 30000
	}
	if ms < 1 {
		ms = 1
	}
	return int32(ms)
}

func handleChangesetClose(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("changeset close")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	changesetID, err := resolveChangesetIDForRead("")
	switch fs.NArg() {
	case 0:
	case 1:
		changesetID, err = resolveChangesetIDForRead(fs.Arg(0))
	default:
		commandUsage("Usage: gs changeset close [<changeset-id>] [--json]")
		return
	}
	if err != nil {
		commandFatalf("CHANGESET_RESOLUTION_FAILED", false, "", "Failed to resolve changeset ID: %v", err)
		return
	}

	req := &slicev1.CloseChangesetRequest{ChangesetId: changesetID}
	resp, err := cli.sliceClient.CloseChangeset(ctx, req)
	if err != nil {
		commandFatalf("CHANGESET_CLOSE_FAILED", true, "", "Failed to close changeset: %v", err)
	}
	if resp.GetStatus() == slicev1.ChangesetStatus_REJECTED {
		if err := clearTrackedChangesetIDIfMatches(req.ChangesetId); err != nil {
			log.Printf("Warning: failed to clear tracked changeset ID: %v", err)
		}
	}

	if jsonEnabled {
		writeJSONOutput(buildChangesetCloseOutput(resp))
		return
	}
	fmt.Printf("Closed changeset %s\n", resp.GetChangesetId())
	fmt.Printf("Status: %s\n", resp.GetStatus().String())
}

func handleChangesetRebase(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("changeset rebase")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs changeset rebase <changeset-id> [--json]")
		return
	}

	req := &slicev1.RebaseChangesetRequest{ChangesetId: fs.Arg(0)}
	resp, err := cli.sliceClient.RebaseChangeset(ctx, req)
	if err != nil {
		commandFatalf("CHANGESET_REBASE_FAILED", true, "", "Failed to rebase changeset: %v", err)
	}
	if jsonEnabled {
		out := jsonChangesetRebaseOutput{
			ChangesetID:         req.ChangesetId,
			Status:              resp.GetStatus().String(),
			NewBaseCommitHash:   resp.GetNewBaseCommitHash(),
			SliceCommitsToApply: append([]string(nil), resp.GetSliceCommitsToApply()...),
			Conflicts:           buildMergeConflicts(resp.GetConflicts()),
		}
		writeJSONOutput(out)
		return
	}

	fmt.Printf("Rebase status: %s\n", resp.Status.String())
	fmt.Printf("New base commit: %s\n", resp.NewBaseCommitHash)
}

func handleChangesetList(ctx context.Context, cli *CLI, args []string) {
	sliceID, err := sliceIDFromConfig()
	if err != nil {
		commandFatalf("SLICE_NOT_BOUND", false, "gs slice checkout <slice-id>", "Failed to read slice binding: %v", err)
		return
	}

	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("changeset list")
	limit := fs.Int("limit", 20, "Maximum results")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	status := &stringFlag{}
	fs.Var(status, "status", "Filter by status (pending, approved, rejected, merged)")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	statusFilter := slicev1.ChangesetStatus(-1)
	if status.set {
		switch strings.ToLower(status.value) {
		case "approved":
			statusFilter = slicev1.ChangesetStatus_APPROVED
		case "rejected":
			statusFilter = slicev1.ChangesetStatus_REJECTED
		case "merged":
			statusFilter = slicev1.ChangesetStatus_MERGED
		case "pending":
			statusFilter = slicev1.ChangesetStatus_PENDING
		default:
			commandFatalf("INVALID_ARGUMENT", false, "", "Unknown status filter: %s", status.value)
			return
		}
	}

	req := &slicev1.ListChangesetsRequest{
		SliceId:      sliceID,
		StatusFilter: statusFilter,
		Limit:        int32(*limit),
	}

	resp, err := cli.sliceClient.ListChangesets(ctx, req)
	if err != nil {
		commandFatalf("CHANGESET_LIST_FAILED", true, "", "Failed to list changesets: %v", err)
	}

	sort.Slice(resp.Changesets, func(i, j int) bool {
		return resp.Changesets[i].CreatedAt > resp.Changesets[j].CreatedAt
	})

	if jsonEnabled {
		out := jsonChangesetListOutput{
			SliceID:    sliceID,
			Total:      len(resp.Changesets),
			Changesets: make([]jsonChangesetListItem, 0, len(resp.Changesets)),
		}
		for _, cs := range resp.Changesets {
			out.Changesets = append(out.Changesets, jsonChangesetListItem{
				ChangesetID:  cs.GetChangesetId(),
				Status:       cs.GetStatus().String(),
				ReviewStatus: reviewStatusForChangesetInfo(cs),
				CI:           buildChangesetCIOutput(cs.GetCi()),
				Message:      cs.GetMessage(),
				CreatedAt:    cs.GetCreatedAt(),
			})
		}
		writeJSONOutput(out)
		return
	}

	fmt.Printf("Found %d changeset(s) for slice %s\n", len(resp.Changesets), sliceID)
	for _, cs := range resp.Changesets {
		statusText := cs.GetStatus().String()
		if reviewStatus := reviewStatusForChangesetInfo(cs); reviewStatus != "" {
			statusText = fmt.Sprintf("%s/%s", statusText, reviewStatus)
		}
		if ciText := changesetCIText(cs.GetCi()); ciText != "" {
			statusText = fmt.Sprintf("%s ci:%s", statusText, ciText)
		}
		fmt.Printf("- %s [%s] %s\n", cs.ChangesetId, statusText, cs.Message)
	}
}

func resolveChangesetIDForRead(explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit, nil
	}
	tracked, err := readTrackedChangesetIDFromConfig()
	if err != nil {
		return "", err
	}
	tracked = strings.TrimSpace(tracked)
	if tracked == "" {
		return "", fmt.Errorf("no tracked changeset; pass an explicit changeset ID")
	}
	return tracked, nil
}

func printMergeConflicts(conflicts []*slicev1.Conflict) {
	if len(conflicts) == 0 {
		return
	}
	fmt.Println("Conflicts detected:")
	for _, conflict := range conflicts {
		typeLabel := strings.TrimPrefix(conflict.GetType().String(), "CONFLICT_TYPE_")
		if typeLabel == "UNSPECIFIED" {
			typeLabel = ""
		}
		if typeLabel != "" {
			fmt.Printf("- %s [%s] (slices: %s)\n", conflict.GetFileId(), typeLabel, strings.Join(conflict.GetConflictingSliceIds(), ", "))
		} else {
			fmt.Printf("- %s (slices: %s)\n", conflict.GetFileId(), strings.Join(conflict.GetConflictingSliceIds(), ", "))
		}
		if message := strings.TrimSpace(conflict.GetMessage()); message != "" {
			fmt.Printf("  %s\n", message)
		}
	}
}

func printSliceConflictGuidance() {
	fmt.Println("Hint: sync to the latest slice head, review your local changes, then publish again.")
	fmt.Println("      Suggested flow: gs slice sync && gs slice diff && gs slice export && gs changeset merge")
}

func printMergeResult(resp *slicev1.MergeChangesetResponse) {
	if resp == nil {
		fmt.Println("Merge status: unknown")
		return
	}

	fmt.Printf("Merge status: %s\n", resp.GetStatus().String())
	if resp.GetNewCommitHash() != "" {
		fmt.Printf("New commit: %s\n", resp.GetNewCommitHash())
	}
	if resp.GetMergeSeq() > 0 {
		fmt.Printf("Projection token: %s shard=%d seq=%d\n", resp.GetMergeHomeId(), resp.GetMergeShard(), resp.GetMergeSeq())
		for _, projection := range resp.GetProjections() {
			if projection == nil {
				continue
			}
			fmt.Printf("Projection %s: %s (applied=%d requested=%d)\n", projection.GetProjectionName(), projection.GetState().String(), projection.GetAppliedSeq(), projection.GetRequestedSeq())
		}
	}
	if message := strings.TrimSpace(resp.GetMessage()); message != "" {
		fmt.Printf("Message: %s\n", message)
	}

	switch resp.GetStatus() {
	case slicev1.MergeStatus_MERGE_STATUS_CONFLICT:
		printMergeConflicts(resp.GetConflicts())
		printSliceConflictGuidance()
	case slicev1.MergeStatus_MERGE_STATUS_STALE_BASE:
		fmt.Println("Hint: sync the changeset onto the latest slice head, then merge again.")
		fmt.Printf("      Suggested flow: gs changeset rebase %s && gs changeset merge\n", resp.GetChangesetId())
	case slicev1.MergeStatus_MERGE_STATUS_LOCKED:
		fmt.Println("Hint: another merge is already operating on this slice or file set. Retry shortly.")
	}
}

func printChangesetCIFromRun(status string, runID string) {
	status = strings.TrimSpace(status)
	runID = strings.TrimSpace(runID)
	if status == "" && runID == "" {
		return
	}
	if runID != "" {
		fmt.Printf("CI: %s (%s)\n", status, runID)
		return
	}
	fmt.Printf("CI: %s\n", status)
}

func changesetCIText(ci *slicev1.ChangesetCISummary) string {
	if ci == nil {
		return ""
	}
	status := strings.TrimSpace(ci.GetStatus())
	if ci.GetStale() {
		status = "stale"
	}
	if status == "" {
		return ""
	}
	if ci.GetRequiredTotal() > 0 {
		return fmt.Sprintf("%s %d/%d", status, ci.GetRequiredPassed(), ci.GetRequiredTotal())
	}
	return status
}

func printChangesetCISummary(ci *slicev1.ChangesetCISummary) {
	text := changesetCIText(ci)
	if text == "" {
		return
	}
	if runID := strings.TrimSpace(ci.GetRunId()); runID != "" {
		fmt.Printf("CI: %s (%s)\n", text, runID)
		return
	}
	fmt.Printf("CI: %s\n", text)
}

func printChangesetReview(resp *slicev1.ReviewChangesetResponse, includePatches bool) {
	if resp == nil {
		fmt.Println("No changeset review response")
		return
	}

	changeset := resp.GetChangeset()
	changesetID := ""
	if changeset != nil {
		changesetID = changeset.GetChangesetId()
	}
	fmt.Printf("Changeset: %s\n", changesetID)
	fmt.Printf("Status: %s\n", resp.GetReviewStatus().String())
	if changeset != nil {
		fmt.Printf("Slice: %s\n", changeset.GetSliceId())
		if changeset.GetMessage() != "" {
			fmt.Printf("Message: %s\n", changeset.GetMessage())
		}
		printChangesetCISummary(changeset.GetCi())
	}
	if snapshot := resp.GetSnapshot(); snapshot != nil {
		fmt.Printf("Snapshot: v%d %s\n", snapshot.GetVersion(), snapshot.GetHash())
	}
	if diff := resp.GetDiff(); diff != nil {
		fmt.Printf(
			"Files: +%d ~%d -%d\n",
			diff.GetFilesAdded(),
			diff.GetFilesModified(),
			diff.GetFilesDeleted(),
		)
		fmt.Printf("Lines: +%d -%d\n", diff.GetLinesAdded(), diff.GetLinesRemoved())
	}
	if warnings := resp.GetWarnings(); len(warnings) > 0 {
		fmt.Println("Warnings:")
		for _, warning := range warnings {
			fmt.Printf("- %s\n", warning)
		}
	}
	if issues := resp.GetIssues(); len(issues) > 0 {
		fmt.Println("Issues:")
		for _, issue := range issues {
			label := strings.TrimPrefix(issue.GetType().String(), "REVIEW_ISSUE_TYPE_")
			switch {
			case issue.GetFileId() != "" && len(issue.GetConflictingSliceIds()) > 0:
				fmt.Printf("- [%s] %s (slices: %s)\n", label, issue.GetFileId(), strings.Join(issue.GetConflictingSliceIds(), ", "))
			case issue.GetFileId() != "":
				fmt.Printf("- [%s] %s\n", label, issue.GetFileId())
			default:
				fmt.Printf("- [%s]\n", label)
			}
			if message := strings.TrimSpace(issue.GetMessage()); message != "" {
				fmt.Printf("  %s\n", message)
			}
		}
	}
	if len(resp.GetChanges()) == 0 {
		return
	}

	fmt.Println("Changes:")
	for _, change := range resp.GetChanges() {
		path := change.GetPath()
		if change.GetOldPath() != "" && change.GetOldPath() != change.GetPath() {
			path = fmt.Sprintf("%s -> %s", change.GetOldPath(), change.GetPath())
		}
		fmt.Printf(
			"- [%s] %s (+%d -%d)\n",
			change.GetChangeType().String(),
			path,
			change.GetLinesAdded(),
			change.GetLinesDeleted(),
		)
		if includePatches && strings.TrimSpace(change.GetPatch()) != "" {
			fmt.Println(change.GetPatch())
		}
	}
}
