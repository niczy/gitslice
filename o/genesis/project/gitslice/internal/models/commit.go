package models

import "time"

// Commit represents a single commit in a slice's history.
type Commit struct {
	CommitHash string    `json:"commit_hash"`
	ParentHash string    `json:"parent_hash"`
	Timestamp  time.Time `json:"timestamp"`
	Message    string    `json:"message"`
}

// GlobalCommit represents a commit recorded in the global state timeline.
type GlobalCommit struct {
	CommitHash     string    `json:"commit_hash"`
	Timestamp      time.Time `json:"timestamp"`
	MergedSliceIDs []string  `json:"merged_slice_ids"`
}

// GlobalState represents the current merged view across all slices.
type GlobalState struct {
	GlobalCommitHash string          `json:"global_commit_hash"`
	Timestamp        time.Time       `json:"timestamp"`
	History          []*GlobalCommit `json:"history"`
}
