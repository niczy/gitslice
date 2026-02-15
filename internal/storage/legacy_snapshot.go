package storage

import "github.com/niczy/gitslice/internal/models"

// legacyPostgresSnapshot matches the JSON layout stored in the deprecated snapshot
// backend (storage_state.payload). It exists only to support one-time backfill into
// native tables during the cutover.
//
// This type must remain compatible with the historical JSON tags.
type LegacyPostgresSnapshot struct {
	LockedSlices        map[string]bool                     `json:"locked_slices"`
	FileLocks           map[string]string                   `json:"file_locks"`
	Slices              map[string]*models.Slice            `json:"slices"`
	SliceMetadata       map[string]*models.SliceMetadata    `json:"slice_metadata"`
	FileIndex           map[string]map[string]bool          `json:"file_index"`
	FileContents        map[string]*models.FileContent      `json:"file_contents"`
	Entries             map[string]*models.DirectoryEntry   `json:"entries"`
	EntriesByPath       map[string]string                   `json:"entries_by_path"`
	EntriesBySlice      map[string][]string                 `json:"entries_by_slice"`
	EntriesByParent     map[string][]string                 `json:"entries_by_parent"`
	Changesets          map[string]*models.Changeset        `json:"changesets"`
	SliceChangesets     map[string][]string                 `json:"slice_changesets"`
	SliceCommits        map[string][]*models.Commit         `json:"slice_commits"`
	GlobalState         *models.GlobalState                 `json:"global_state"`
	CommitSnapshots     map[string]*models.CommitSnapshot   `json:"commit_snapshots"`
	VersionedContent    map[string]*models.FileContent      `json:"versioned_content"`
	FileChanges         map[string]*models.FileChangeRecord `json:"file_changes"`
	FileChangesByPath   map[string][]string                 `json:"file_changes_by_path"`
	FileChangesByCommit map[string][]string                 `json:"file_changes_by_commit"`
	FileChangesByDir    map[string][]string                 `json:"file_changes_by_dir"`

	Users      map[string]*models.User                          `json:"users"`
	Orgs       map[string]*models.Organization                  `json:"orgs"`
	OrgMembers map[string]map[string]*models.OrganizationMember `json:"org_members"`
	UserOrgs   map[string]map[string]bool                       `json:"user_orgs"`
}
