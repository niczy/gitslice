package models

import "time"

// ChangesetSnapshot captures one exported version of a changeset.
type ChangesetSnapshot struct {
	ID             string
	ChangesetID    string
	Version        int32
	Hash           string
	BaseCommitHash string
	ModifiedFiles  []string
	// ModifiedFileCount is populated by summary reads that intentionally omit
	// ModifiedFiles to keep snapshot list payloads small.
	ModifiedFileCount int
	FileHashes        map[string]string
	BasePathVersions  map[string]int64
	// RenameSources maps destination path -> source path for explicit file
	// rename intents captured in this snapshot.
	RenameSources map[string]string
	Author        string
	Message       string
	CreatedAt     time.Time
}
