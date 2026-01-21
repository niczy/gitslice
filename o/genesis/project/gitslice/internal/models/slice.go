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
	ParentSlice string    `json:"parent_slice,omitempty"`
	IsRoot      bool      `json:"is_root,omitempty"`
}

// SliceMetadata represents slice metadata
type SliceMetadata struct {
	SliceID            string    `json:"slice_id"`
	HeadCommitHash     string    `json:"head_commit_hash"`
	ModifiedFiles      []string  `json:"modified_files"`
	LastModified       time.Time `json:"last_modified"`
	ModifiedFilesCount int       `json:"modified_files_count"`
}

// FileContent represents file content for checkout
type FileContent struct {
	FileID  string `json:"file_id"`
	Path    string `json:"path"`
	Content []byte `json:"content"`
	Size    int64  `json:"size"`
	Hash    string `json:"hash"`
}

// DirectoryEntry represents a file or directory entry
type DirectoryEntry struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Type     string `json:"type"` // "file" or "directory"
	ParentID string `json:"parent_id"`
	Content  []byte `json:"content,omitempty"`
	Size     int64  `json:"size"`
}
