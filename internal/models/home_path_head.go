package models

import "time"

// HomePathHead is the strongly-consistent head for one path inside a user home.
type HomePathHead struct {
	HomeID           string
	Path             string
	PathVersion      int64
	ContentHash      string
	ManifestHash     string
	SourceSliceID    string
	SourceCommitHash string
	LastMergeSeq     int64
	Deleted          bool
	UpdatedAt        time.Time
}

// HomePathHeadBackfillResult summarizes a materialized-home-to-path-head backfill.
type HomePathHeadBackfillResult struct {
	HomeID        string
	SourceSliceID string
	Upserted      int
}

// HomePathHeadDrift describes one disagreement between path heads and materialized home state.
type HomePathHeadDrift struct {
	Path                     string
	Reason                   string
	HeadManifestHash         string
	MaterializedManifestHash string
	HeadDeleted              bool
	MaterializedDeleted      bool
}

// HomePathHeadValidationResult summarizes path-head validation for one home.
type HomePathHeadValidationResult struct {
	HomeID        string
	SourceSliceID string
	Checked       int
	Drifts        []*HomePathHeadDrift
}
