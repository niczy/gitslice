package models

import "time"

// DirectoryMove records an accepted directory rename. The first implementation
// materializes affected path heads during merge, while preserving this compact
// fact for future projection/search behavior.
type DirectoryMove struct {
	MoveID             string    `json:"move_id"`
	HomeID             string    `json:"home_id"`
	SourceSliceID      string    `json:"source_slice_id"`
	SourceCommitHash   string    `json:"source_commit_hash"`
	OldPrefix          string    `json:"old_prefix"`
	NewPrefix          string    `json:"new_prefix"`
	BaseSubtreeVersion int64     `json:"base_subtree_version"`
	BaseSubtreeDigest  string    `json:"base_subtree_digest"`
	NewSubtreeVersion  int64     `json:"new_subtree_version"`
	MergeSeq           int64     `json:"merge_seq"`
	CreatedAt          time.Time `json:"created_at"`
}
