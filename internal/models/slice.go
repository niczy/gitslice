package models

import "time"

// Slice represents a slice in the system
type Slice struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Files       []string  `json:"files"`
	Owners      []string  `json:"owners"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SliceMetadata represents slice metadata
type SliceMetadata struct {
	SliceID            string    `json:"slice_id"`
	HeadCommitHash     string    `json:"head_commit_hash"`
	ModifiedFiles      []string  `json:"modified_files"`
	LastModified       time.Time `json:"last_modified"`
	ModifiedFilesCount int       `json:"modified_files_count"`
}
