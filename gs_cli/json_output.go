package main

import (
	"encoding/json"
	"log"
	"os"

	adminv1 "github.com/niczy/gitslice/proto/admin"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

type jsonChangesetCreateOutput struct {
	ChangesetID   string   `json:"changeset_id"`
	ChangesetHash string   `json:"changeset_hash"`
	Status        string   `json:"status"`
	Updated       bool     `json:"updated"`
	SliceID       string   `json:"slice_id,omitempty"`
	ModifiedFiles []string `json:"modified_files,omitempty"`
}

type jsonChangesetDiffSummary struct {
	FilesAdded    int32 `json:"files_added"`
	FilesModified int32 `json:"files_modified"`
	FilesDeleted  int32 `json:"files_deleted"`
	LinesAdded    int64 `json:"lines_added"`
	LinesRemoved  int64 `json:"lines_removed"`
}

type jsonChangesetSnapshot struct {
	Version int32  `json:"version"`
	Hash    string `json:"hash"`
}

type jsonChangesetIssue struct {
	Type               string   `json:"type"`
	FileID             string   `json:"file_id,omitempty"`
	ConflictingSliceID []string `json:"conflicting_slice_ids,omitempty"`
	Message            string   `json:"message,omitempty"`
}

type jsonChangesetChange struct {
	Path         string `json:"path"`
	OldPath      string `json:"old_path,omitempty"`
	ChangeType   string `json:"change_type"`
	LinesAdded   int32  `json:"lines_added"`
	LinesDeleted int32  `json:"lines_deleted"`
	Patch        string `json:"patch,omitempty"`
}

type jsonChangesetReviewOutput struct {
	ChangesetID  string                   `json:"changeset_id"`
	SliceID      string                   `json:"slice_id,omitempty"`
	Message      string                   `json:"message,omitempty"`
	ReviewStatus string                   `json:"review_status"`
	Snapshot     *jsonChangesetSnapshot   `json:"snapshot,omitempty"`
	Diff         jsonChangesetDiffSummary `json:"diff"`
	Warnings     []string                 `json:"warnings,omitempty"`
	Issues       []jsonChangesetIssue     `json:"issues,omitempty"`
	Changes      []jsonChangesetChange    `json:"changes,omitempty"`
}

type jsonMergeConflict struct {
	FileID              string   `json:"file_id"`
	Type                string   `json:"type,omitempty"`
	ConflictingSliceIDs []string `json:"conflicting_slice_ids,omitempty"`
	Message             string   `json:"message,omitempty"`
}

type jsonMergeOutput struct {
	ChangesetID   string              `json:"changeset_id,omitempty"`
	Status        string              `json:"status"`
	NewCommitHash string              `json:"new_commit_hash,omitempty"`
	Message       string              `json:"message,omitempty"`
	Conflicts     []jsonMergeConflict `json:"conflicts,omitempty"`
}

type jsonSliceInfo struct {
	Name        string   `json:"name"`
	SliceID     string   `json:"slice_id"`
	Slug        string   `json:"slug,omitempty"`
	Description string   `json:"description,omitempty"`
	Owners      []string `json:"owners,omitempty"`
	FileCount   int32    `json:"file_count"`
	UpdatedAt   int64    `json:"updated_at,omitempty"`
}

type jsonSliceListOutput struct {
	Total  int             `json:"total"`
	Slices []jsonSliceInfo `json:"slices"`
}

type jsonSliceCreateOutput struct {
	Name        string `json:"name"`
	SliceID     string `json:"slice_id"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
}

type jsonWorkingTreeSummary struct {
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Deleted  int `json:"deleted"`
}

type jsonWorkingTreePath struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type jsonSliceStatusOutput struct {
	SliceID                string                 `json:"slice_id"`
	Mode                   string                 `json:"mode"`
	CheckoutBase           string                 `json:"checkout_base,omitempty"`
	RemoteQueried          bool                   `json:"remote_queried"`
	RemoteHead             string                 `json:"remote_head,omitempty"`
	SyncStatus             string                 `json:"sync_status"`
	TrackedChangesetID     string                 `json:"tracked_changeset_id,omitempty"`
	TrackedChangesetStatus string                 `json:"tracked_changeset_status,omitempty"`
	WorkingTree            string                 `json:"working_tree"`
	Changes                jsonWorkingTreeSummary `json:"changes"`
	PathCount              int                    `json:"path_count"`
	Paths                  []jsonWorkingTreePath  `json:"paths,omitempty"`
	Truncated              bool                   `json:"truncated"`
}

type jsonSliceDeleteOutput struct {
	SliceID                string `json:"slice_id"`
	Slug                   string `json:"slug,omitempty"`
	Status                 string `json:"status"`
	RemovedCheckoutRecords int    `json:"removed_checkout_records"`
}

type jsonSlicePublishOutput struct {
	Changeset  jsonChangesetCreateOutput `json:"changeset"`
	Review     jsonChangesetReviewOutput `json:"review"`
	ReviewOnly bool                      `json:"review_only"`
	Merge      *jsonMergeOutput          `json:"merge,omitempty"`
}

type jsonChangesetListItem struct {
	ChangesetID string `json:"changeset_id"`
	Status      string `json:"status"`
	Message     string `json:"message,omitempty"`
	CreatedAt   int64  `json:"created_at,omitempty"`
}

type jsonChangesetListOutput struct {
	SliceID    string                  `json:"slice_id"`
	Total      int                     `json:"total"`
	Changesets []jsonChangesetListItem `json:"changesets"`
}

func writeJSONOutput(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		log.Fatalf("Failed to write JSON output: %v", err)
	}
}

func buildChangesetCreateOutput(resp *slicev1.CreateChangesetResponse, updated bool, sliceID string, modifiedFiles []string) jsonChangesetCreateOutput {
	if resp == nil {
		return jsonChangesetCreateOutput{Updated: updated, SliceID: sliceID, ModifiedFiles: append([]string(nil), modifiedFiles...)}
	}
	return jsonChangesetCreateOutput{
		ChangesetID:   resp.GetChangesetId(),
		ChangesetHash: resp.GetChangesetHash(),
		Status:        resp.GetStatus().String(),
		Updated:       updated,
		SliceID:       sliceID,
		ModifiedFiles: append([]string(nil), modifiedFiles...),
	}
}

func buildChangesetReviewOutput(resp *slicev1.ReviewChangesetResponse, includePatches bool) jsonChangesetReviewOutput {
	output := jsonChangesetReviewOutput{}
	if resp == nil {
		return output
	}
	output.ReviewStatus = resp.GetReviewStatus().String()
	if changeset := resp.GetChangeset(); changeset != nil {
		output.ChangesetID = changeset.GetChangesetId()
		output.SliceID = changeset.GetSliceId()
		output.Message = changeset.GetMessage()
	}
	if snapshot := resp.GetSnapshot(); snapshot != nil {
		output.Snapshot = &jsonChangesetSnapshot{
			Version: snapshot.GetVersion(),
			Hash:    snapshot.GetHash(),
		}
	}
	if diff := resp.GetDiff(); diff != nil {
		output.Diff = jsonChangesetDiffSummary{
			FilesAdded:    diff.GetFilesAdded(),
			FilesModified: diff.GetFilesModified(),
			FilesDeleted:  diff.GetFilesDeleted(),
			LinesAdded:    diff.GetLinesAdded(),
			LinesRemoved:  diff.GetLinesRemoved(),
		}
	}
	output.Warnings = append([]string(nil), resp.GetWarnings()...)
	if len(resp.GetIssues()) > 0 {
		output.Issues = make([]jsonChangesetIssue, 0, len(resp.GetIssues()))
		for _, issue := range resp.GetIssues() {
			output.Issues = append(output.Issues, jsonChangesetIssue{
				Type:               issue.GetType().String(),
				FileID:             issue.GetFileId(),
				ConflictingSliceID: append([]string(nil), issue.GetConflictingSliceIds()...),
				Message:            issue.GetMessage(),
			})
		}
	}
	if len(resp.GetChanges()) > 0 {
		output.Changes = make([]jsonChangesetChange, 0, len(resp.GetChanges()))
		for _, change := range resp.GetChanges() {
			item := jsonChangesetChange{
				Path:         change.GetPath(),
				OldPath:      change.GetOldPath(),
				ChangeType:   change.GetChangeType().String(),
				LinesAdded:   change.GetLinesAdded(),
				LinesDeleted: change.GetLinesDeleted(),
			}
			if includePatches {
				item.Patch = change.GetPatch()
			}
			output.Changes = append(output.Changes, item)
		}
	}
	return output
}

func buildMergeOutput(resp *slicev1.MergeChangesetResponse) *jsonMergeOutput {
	if resp == nil {
		return nil
	}
	output := &jsonMergeOutput{
		ChangesetID:   resp.GetChangesetId(),
		Status:        resp.GetStatus().String(),
		NewCommitHash: resp.GetNewCommitHash(),
		Message:       resp.GetMessage(),
	}
	if len(resp.GetConflicts()) > 0 {
		output.Conflicts = make([]jsonMergeConflict, 0, len(resp.GetConflicts()))
		for _, conflict := range resp.GetConflicts() {
			output.Conflicts = append(output.Conflicts, jsonMergeConflict{
				FileID:              conflict.GetFileId(),
				Type:                conflict.GetType().String(),
				ConflictingSliceIDs: append([]string(nil), conflict.GetConflictingSliceIds()...),
				Message:             conflict.GetMessage(),
			})
		}
	}
	return output
}

func buildSliceInfoOutput(slice *adminv1.SliceInfo) jsonSliceInfo {
	if slice == nil {
		return jsonSliceInfo{}
	}
	return jsonSliceInfo{
		Name:        slice.GetName(),
		SliceID:     slice.GetSliceId(),
		Slug:        slice.GetSlug(),
		Description: slice.GetDescription(),
		Owners:      append([]string(nil), slice.GetOwners()...),
		FileCount:   slice.GetFileCount(),
		UpdatedAt:   slice.GetUpdatedAt(),
	}
}
