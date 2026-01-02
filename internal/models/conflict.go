package models

// FileConflict represents a file that is claimed by multiple slices.
type FileConflict struct {
	FileID   string
	SliceIDs []string
}
