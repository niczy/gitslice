package models

import "time"

// ChangesetStatus represents the lifecycle state of a changeset
type ChangesetStatus int

const (
	ChangesetStatusPending ChangesetStatus = iota
	ChangesetStatusApproved
	ChangesetStatusRejected
	ChangesetStatusMerged
)

// Changeset represents a change list submitted against a slice
type Changeset struct {
	ID             string
	Hash           string
	SliceID        string
	BaseCommitHash string
	ModifiedFiles  []string
	Status         ChangesetStatus
	Author         string
	Message        string
	CreatedAt      time.Time
	MergedAt       *time.Time
}
