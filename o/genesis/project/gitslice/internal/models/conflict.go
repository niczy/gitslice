package models

// FileConflict represents a file that is claimed by multiple slices.
type FileConflict struct {
	FileID            string   `json:"file_id"`
	Path              string   `json:"path"`
	ConflictingSlices []string `json:"conflicting_slices"`
	Resolved          bool     `json:"resolved"`
}
