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
	FileHashes     map[string]string
	Author         string
	Message        string
	CreatedAt      time.Time
}
