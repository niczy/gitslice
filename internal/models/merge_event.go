package models

import "time"

// MergePathUpdate is the per-path payload stored in an accepted merge event.
type MergePathUpdate struct {
	Path             string `json:"path"`
	BaseVersion      int64  `json:"base_version"`
	NewVersion       int64  `json:"new_version"`
	ContentHash      string `json:"content_hash,omitempty"`
	ManifestHash     string `json:"manifest_hash,omitempty"`
	SourceSliceID    string `json:"source_slice_id,omitempty"`
	SourceCommitHash string `json:"source_commit_hash,omitempty"`
	Deleted          bool   `json:"deleted,omitempty"`
}

// MergeEvent is the immutable durable record for an accepted changeset merge.
type MergeEvent struct {
	HomeID           string
	ShardID          int32
	MergeSeq         int64
	EventID          string
	ChangesetID      string
	SourceSliceID    string
	SourceCommitHash string
	Author           string
	Message          string
	TouchedPaths     []string
	PathUpdates      []*MergePathUpdate
	CreatedAt        time.Time
}

// ProjectionOffset tracks the latest merge event consumed by a projection.
type ProjectionOffset struct {
	ProjectionName string
	ShardID        int32
	MergeSeq       int64
	UpdatedAt      time.Time
}
