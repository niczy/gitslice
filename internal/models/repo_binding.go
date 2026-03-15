package models

import "time"

// RepoBinding tracks a remote repository bound to a subtree in a user's slice.
type RepoBinding struct {
	BindingID            string    `json:"binding_id"`
	OwnerUsername        string    `json:"owner_username"`
	SliceID              string    `json:"slice_id"`
	RootPath             string    `json:"root_path"`
	Provider             string    `json:"provider"`
	RepoURL              string    `json:"repo_url"`
	Branch               string    `json:"branch"`
	PushEnabled          bool      `json:"push_enabled"`
	LastImportedCommit   string    `json:"last_imported_commit,omitempty"`
	LastPushedCommit     string    `json:"last_pushed_commit,omitempty"`
	LastSeenRemoteCommit string    `json:"last_seen_remote_commit,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}
