package gscli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/agentsession"
	agentv1 "github.com/niczy/gitslice/proto/agent"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

const (
	defaultAgentLocalChangesLimit = 100
	maxAgentLocalChangesLimit     = 500
)

type localAgentChangesRequest struct {
	RequestID         string `json:"requestId"`
	RequestIDSnake    string `json:"request_id"`
	Limit             int    `json:"limit"`
	IncludeDiffs      bool   `json:"includeDiffs"`
	IncludeDiffsSnake bool   `json:"include_diffs"`
}

type localAgentChangesetExportRequest struct {
	RequestID        string   `json:"requestId"`
	RequestIDSnake   string   `json:"request_id"`
	Message          string   `json:"message"`
	Author           string   `json:"author"`
	ChangesetID      string   `json:"changesetId"`
	ChangesetIDSnake string   `json:"changeset_id"`
	Files            []string `json:"files"`
}

type pendingLocalAgentChangesRequest struct {
	Seq     uint64
	Request localAgentChangesRequest
}

func parseLocalAgentChangesRequest(payload []byte) localAgentChangesRequest {
	var request localAgentChangesRequest
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &request)
	}
	return request
}

func parseLocalAgentChangesetExportRequest(payload []byte) localAgentChangesetExportRequest {
	var request localAgentChangesetExportRequest
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &request)
	}
	return request
}

func localAgentRequestID(camel, snake string) string {
	return firstNonEmpty(strings.TrimSpace(camel), strings.TrimSpace(snake))
}

func latestPendingLocalAgentChangesRequest(events []*agentv1.EventEnvelope) (pendingLocalAgentChangesRequest, bool) {
	var pending pendingLocalAgentChangesRequest
	pendingKey := ""
	for _, event := range events {
		switch event.GetStream() + "/" + event.GetType() {
		case agentsession.EventStreamControl + "/" + agentsession.EventTypeLocalChangesRequested:
			request := parseLocalAgentChangesRequest(event.GetPayload())
			pending = pendingLocalAgentChangesRequest{Seq: event.GetSeq(), Request: request}
			pendingKey = localAgentRequestKey(event.GetSeq(), request)
		case agentsession.EventStreamStatus + "/" + agentsession.EventTypeLocalChanges,
			agentsession.EventStreamControl + "/" + agentsession.EventTypeLocalChangesFailed:
			if pendingKey != "" && pendingKey == localAgentResultRequestKey(event.GetPayload()) {
				pending = pendingLocalAgentChangesRequest{}
				pendingKey = ""
			}
		}
	}
	return pending, pendingKey != ""
}

func localAgentRequestKey(seq uint64, request localAgentChangesRequest) string {
	if requestID := localAgentRequestID(request.RequestID, request.RequestIDSnake); requestID != "" {
		return "id:" + requestID
	}
	return fmt.Sprintf("seq:%d", seq)
}

func localAgentResultRequestKey(payload []byte) string {
	var result struct {
		RequestID         string `json:"requestId"`
		RequestIDSnake    string `json:"request_id"`
		RequestedSeq      uint64 `json:"requestedSeq"`
		RequestedSeqSnake uint64 `json:"requested_seq"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &result)
	}
	if requestID := localAgentRequestID(result.RequestID, result.RequestIDSnake); requestID != "" {
		return "id:" + requestID
	}
	requestedSeq := result.RequestedSeq
	if requestedSeq == 0 {
		requestedSeq = result.RequestedSeqSnake
	}
	if requestedSeq > 0 {
		return fmt.Sprintf("seq:%d", requestedSeq)
	}
	return ""
}

func localAgentChangesLimit(limit int) int {
	switch {
	case limit < 0:
		return defaultAgentLocalChangesLimit
	case limit == 0:
		return defaultAgentLocalChangesLimit
	case limit > maxAgentLocalChangesLimit:
		return maxAgentLocalChangesLimit
	default:
		return limit
	}
}

func handleLocalAgentChangesRequest(ctx context.Context, cli *CLI, cfg localAgentRunConfig, eventSeq uint64, request localAgentChangesRequest) error {
	payload, err := buildLocalAgentChangesPayloadForRunner(ctx, cli, cfg, eventSeq, request)
	if err != nil {
		return appendAgentJSONEvent(ctx, cli, cfg.SessionID, agentsession.EventStreamControl, agentsession.EventTypeLocalChangesFailed, map[string]any{
			"status":        "failed",
			"request_id":    localAgentRequestID(request.RequestID, request.RequestIDSnake),
			"requested_seq": eventSeq,
			"code":          "LOCAL_CHANGES_STATUS_FAILED",
			"message":       err.Error(),
			"failed_at":     time.Now().UTC().Format(time.RFC3339),
		})
	}
	return appendAgentJSONEvent(ctx, cli, cfg.SessionID, agentsession.EventStreamStatus, agentsession.EventTypeLocalChanges, payload)
}

func buildLocalAgentChangesPayload(dir, sessionID string, requestedSeq uint64, request localAgentChangesRequest) (map[string]any, error) {
	return buildLocalAgentChangesPayloadForRunner(context.Background(), nil, localAgentRunConfig{
		CWD:       dir,
		SessionID: sessionID,
	}, requestedSeq, request)
}

func buildLocalAgentChangesPayloadForRunner(ctx context.Context, cli *CLI, cfg localAgentRunConfig, requestedSeq uint64, request localAgentChangesRequest) (map[string]any, error) {
	dir := cfg.CWD
	sliceID, err := readSliceIDFromConfigAt(dir)
	if err != nil {
		return nil, err
	}
	sliceID, err = normalizeSliceID(sliceID)
	if err != nil {
		return nil, err
	}
	checkoutIndex, err := detectCheckoutMode(dir)
	if err != nil {
		return nil, err
	}
	statusEntries, err := collectNoGitWorkingTreeStatus(dir, checkoutIndex)
	if err != nil {
		return nil, err
	}
	statusEntries = filterWorkingTreeStatusEntries(statusEntries)
	added, modified, deleted := summarizeWorkingTreeStatus(statusEntries)
	trackedChangesetID, err := readTrackedChangesetIDFromConfigAt(dir)
	if err != nil {
		return nil, err
	}
	workingTree := "clean"
	if len(statusEntries) > 0 {
		workingTree = "dirty"
	}
	limit := localAgentChangesLimit(request.Limit)
	printed := statusEntries
	truncated := false
	if limit > 0 && len(statusEntries) > limit {
		printed = statusEntries[:limit]
		truncated = true
	}
	paths := make([]map[string]any, 0, len(printed))
	for _, entry := range printed {
		paths = append(paths, map[string]any{
			"path":   entry.Path,
			"status": entry.Status,
		})
	}
	includeDiffs := request.IncludeDiffs || request.IncludeDiffsSnake
	if includeDiffs && len(printed) > 0 {
		if err := attachLocalAgentDiffs(ctx, cli, cfg, checkoutIndex, printed, paths); err != nil {
			return nil, err
		}
	}
	checkoutBase := ""
	if checkoutIndex != nil {
		checkoutBase = strings.TrimSpace(checkoutIndex.CommitHash)
	}
	return map[string]any{
		"status":                "ready",
		"request_id":            localAgentRequestID(request.RequestID, request.RequestIDSnake),
		"requested_seq":         requestedSeq,
		"session_id":            strings.TrimSpace(cfg.SessionID),
		"slice_id":              sliceID,
		"checkout_dir":          strings.TrimSpace(dir),
		"checkout_base":         checkoutBase,
		"tracked_changeset_id":  strings.TrimSpace(trackedChangesetID),
		"working_tree":          workingTree,
		"changes":               map[string]any{"added": added, "modified": modified, "deleted": deleted},
		"diffs_included":        includeDiffs,
		"path_count":            len(statusEntries),
		"paths":                 paths,
		"truncated":             truncated,
		"refreshed_at":          time.Now().UTC().Format(time.RFC3339),
		"local_changes_version": 1,
	}, nil
}

func attachLocalAgentDiffs(ctx context.Context, cli *CLI, cfg localAgentRunConfig, checkoutIndex *localCheckoutIndex, entries []workingTreeStatusEntry, paths []map[string]any) error {
	if len(entries) == 0 || len(paths) == 0 {
		return nil
	}
	diffCtx := ctx
	if cfg.AuthContext != nil {
		var err error
		diffCtx, err = cfg.AuthContext(ctx)
		if err != nil {
			return err
		}
	}
	cache, cacheErr := NewCacheManager()
	if cacheErr != nil {
		cache = nil
	}
	diffs, err := buildLocalSliceDiffsAt(diffCtx, cli, checkoutIndex, cache, entries, cfg.CWD)
	if err != nil {
		return err
	}
	byKey := make(map[string]localSliceDiff, len(diffs))
	for _, diff := range diffs {
		byKey[diff.Status+"\x00"+diff.Path] = diff
	}
	for _, path := range paths {
		key := strings.TrimSpace(fmt.Sprint(path["status"])) + "\x00" + strings.TrimSpace(fmt.Sprint(path["path"]))
		diff, ok := byKey[key]
		if !ok {
			continue
		}
		path["patch"] = diff.Patch
		path["lines_added"] = diff.LinesAdded
		path["lines_deleted"] = diff.LinesDeleted
		path["binary"] = diff.Binary
		if len(diff.MetadataNotes) > 0 {
			path["metadata_notes"] = append([]string(nil), diff.MetadataNotes...)
		}
	}
	return nil
}

func handleLocalAgentChangesetExportRequest(ctx context.Context, cli *CLI, cfg localAgentRunConfig, eventSeq uint64, request localAgentChangesetExportRequest) error {
	requestID := localAgentRequestID(request.RequestID, request.RequestIDSnake)
	_ = appendAgentJSONEvent(ctx, cli, cfg.SessionID, agentsession.EventStreamControl, agentsession.EventTypeChangesetExportStarted, map[string]any{
		"status":        "started",
		"request_id":    requestID,
		"requested_seq": eventSeq,
		"started_at":    time.Now().UTC().Format(time.RFC3339),
	})

	result, err := exportLocalAgentChangeset(ctx, cli, cfg, request)
	if err != nil {
		return appendAgentJSONEvent(ctx, cli, cfg.SessionID, agentsession.EventStreamControl, agentsession.EventTypeChangesetExportFailed, map[string]any{
			"status":        "failed",
			"request_id":    requestID,
			"requested_seq": eventSeq,
			"code":          "LOCAL_CHANGESET_EXPORT_FAILED",
			"message":       err.Error(),
			"failed_at":     time.Now().UTC().Format(time.RFC3339),
		})
	}

	extra := map[string]any{
		"status":         "completed",
		"request_id":     requestID,
		"requested_seq":  eventSeq,
		"updated":        result.updated,
		"modified_files": append([]string(nil), result.modifiedFiles...),
		"file_count":     len(result.modifiedFiles),
		"message":        result.message,
	}
	if err := appendAgentChangesetExportEvent(ctx, cli, cfg.SessionID, result.changesetID, result.changesetHash, "web_ui", result.reviewResp, extra); err != nil {
		return appendAgentJSONEvent(ctx, cli, cfg.SessionID, agentsession.EventStreamControl, agentsession.EventTypeChangesetExportFailed, map[string]any{
			"status":        "failed",
			"request_id":    requestID,
			"requested_seq": eventSeq,
			"code":          "LOCAL_CHANGESET_EXPORT_RECORD_FAILED",
			"message":       err.Error(),
			"failed_at":     time.Now().UTC().Format(time.RFC3339),
		})
	}
	statusRequest := localAgentChangesRequest{RequestID: request.RequestID, RequestIDSnake: request.RequestIDSnake, Limit: defaultAgentLocalChangesLimit}
	return handleLocalAgentChangesRequest(ctx, cli, cfg, eventSeq, statusRequest)
}

type localAgentChangesetExportResult struct {
	changesetID   string
	changesetHash string
	updated       bool
	modifiedFiles []string
	message       string
	reviewResp    *slicev1.ReviewChangesetResponse
}

func exportLocalAgentChangeset(ctx context.Context, cli *CLI, cfg localAgentRunConfig, request localAgentChangesetExportRequest) (localAgentChangesetExportResult, error) {
	sliceID, err := readSliceIDFromConfigAt(cfg.CWD)
	if err != nil {
		return localAgentChangesetExportResult{}, err
	}
	sliceID, err = normalizeSliceID(sliceID)
	if err != nil {
		return localAgentChangesetExportResult{}, err
	}
	changesetID, isUpdate, err := resolveChangesetIDForExportAt(cfg.CWD, firstNonEmpty(request.ChangesetID, request.ChangesetIDSnake))
	if err != nil {
		return localAgentChangesetExportResult{}, err
	}
	modifiedFiles, _, err := resolveWorkingTreeModifiedFiles(cfg.CWD, request.Files)
	if err != nil {
		return localAgentChangesetExportResult{}, err
	}
	if len(modifiedFiles) == 0 {
		return localAgentChangesetExportResult{}, fmt.Errorf("no local changes to export")
	}
	fileContents, err := buildLocalChangesetFileContents(cfg.CWD, modifiedFiles)
	if err != nil {
		return localAgentChangesetExportResult{}, err
	}
	baseCommitHash := ""
	if !isUpdate {
		baseCommitHash, err = resolveCheckoutBaseCommit(cfg.CWD, "")
		if err != nil {
			return localAgentChangesetExportResult{}, err
		}
	}
	message := strings.TrimSpace(request.Message)
	if message == "" {
		message = fmt.Sprintf("Agent changes from %s", shortSessionIdForAgentEvent(cfg.SessionID))
	}
	author := strings.TrimSpace(request.Author)
	if author == "" {
		author = "agent"
	}
	createCtx, err := localAgentRPCContext(ctx, cfg)
	if err != nil {
		return localAgentChangesetExportResult{}, err
	}
	createResp, err := cli.sliceClient.CreateChangeset(createCtx, &slicev1.CreateChangesetRequest{
		SliceId:        sliceID,
		BaseCommitHash: baseCommitHash,
		ModifiedFiles:  modifiedFiles,
		Author:         author,
		Message:        message,
		ChangesetId:    changesetID,
		FileContents:   fileContents,
	})
	if err != nil {
		return localAgentChangesetExportResult{}, err
	}
	targetChangesetID := createResp.GetChangesetId()
	if err := writeTrackedChangesetIDConfigAt(cfg.CWD, targetChangesetID); err != nil {
		return localAgentChangesetExportResult{}, err
	}
	reviewCtx, err := localAgentRPCContext(ctx, cfg)
	if err != nil {
		return localAgentChangesetExportResult{}, err
	}
	reviewResp, err := cli.sliceClient.ReviewChangeset(reviewCtx, &slicev1.ReviewChangesetRequest{
		ChangesetId: targetChangesetID,
	})
	if err != nil {
		return localAgentChangesetExportResult{}, err
	}
	return localAgentChangesetExportResult{
		changesetID:   targetChangesetID,
		changesetHash: createResp.GetChangesetHash(),
		updated:       isUpdate,
		modifiedFiles: append([]string(nil), modifiedFiles...),
		message:       message,
		reviewResp:    reviewResp,
	}, nil
}

func localAgentRPCContext(ctx context.Context, cfg localAgentRunConfig) (context.Context, error) {
	if cfg.AuthContext == nil {
		return ctx, nil
	}
	return cfg.AuthContext(ctx)
}

func shortSessionIdForAgentEvent(sessionID string) string {
	sessionID = strings.TrimPrefix(strings.TrimSpace(sessionID), "sess_")
	if len(sessionID) <= 12 {
		return sessionID
	}
	return sessionID[:12]
}
