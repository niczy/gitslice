package models

import "time"

// ChangeType represents the type of change made to a file
type ChangeType string

const (
	ChangeTypeAdd    ChangeType = "add"
	ChangeTypeModify ChangeType = "modify"
	ChangeTypeDelete ChangeType = "delete"
	ChangeTypeRename ChangeType = "rename"
)

// FileChangeRecord represents a single change to a file within a commit.
// This enables efficient querying of file history by path.
type FileChangeRecord struct {
	// ID is a unique identifier for this change record
	ID string `json:"id"`

	// SliceID identifies which slice this change belongs to
	SliceID string `json:"slice_id"`

	// CommitHash links this change to the commit that made it
	CommitHash string `json:"commit_hash"`

	// Path is the file path (after the change, for renames)
	Path string `json:"path"`

	// OldPath is set only for renames (the path before renaming)
	OldPath string `json:"old_path,omitempty"`

	// ChangeType indicates what kind of change was made
	ChangeType ChangeType `json:"change_type"`

	// OldHash is the content hash before the change (empty for add)
	OldHash string `json:"old_hash,omitempty"`

	// NewHash is the content hash after the change (empty for delete)
	NewHash string `json:"new_hash,omitempty"`

	// LinesAdded is the number of lines added in this change
	LinesAdded int `json:"lines_added"`

	// LinesDeleted is the number of lines removed in this change
	LinesDeleted int `json:"lines_deleted"`

	// Author who made this change
	Author string `json:"author"`

	// Message is the commit message (denormalized for efficient queries)
	Message string `json:"message"`

	// Timestamp when this change was made
	Timestamp time.Time `json:"timestamp"`
}

// FileHistoryQuery specifies filters for querying file history
type FileHistoryQuery struct {
	// SliceID to filter by (empty for all slices)
	SliceID string

	// Path to get history for (exact match)
	Path string

	// PathPrefix for directory-level queries (e.g., "src/services/")
	PathPrefix string

	// ChangeTypes to filter by (empty for all types)
	ChangeTypes []ChangeType

	// Author to filter by (empty for all authors)
	Author string

	// FromCommit to start pagination from (exclusive)
	FromCommit string

	// FromTimestamp to filter changes after this time
	FromTimestamp *time.Time

	// ToTimestamp to filter changes before this time
	ToTimestamp *time.Time

	// Limit maximum number of results
	Limit int

	// Offset for pagination
	Offset int
}

// FileHistoryResult contains the result of a file history query
type FileHistoryResult struct {
	// Changes is the list of change records matching the query
	Changes []*FileChangeRecord `json:"changes"`

	// TotalCount is the total number of matching records (for pagination)
	TotalCount int `json:"total_count"`

	// HasMore indicates if there are more results beyond this page
	HasMore bool `json:"has_more"`
}

// DirectoryChangeSummary aggregates changes for a directory
type DirectoryChangeSummary struct {
	// Path is the directory path
	Path string `json:"path"`

	// TotalChanges is the number of changes under this directory
	TotalChanges int `json:"total_changes"`

	// FilesChanged is the number of unique files changed
	FilesChanged int `json:"files_changed"`

	// LastChange is the most recent change record
	LastChange *FileChangeRecord `json:"last_change,omitempty"`

	// ChangesByType counts changes by type
	ChangesByType map[ChangeType]int `json:"changes_by_type"`
}
