package storage

import (
	"context"
	"errors"

	"github.com/niczy/gitslice/internal/models"
)

var (
	ErrSliceNotFound      = errors.New("slice not found")
	ErrSliceAlreadyExists = errors.New("slice already exists")
	ErrInvalidInput       = errors.New("invalid input")
	ErrChangesetNotFound  = errors.New("changeset not found")
	ErrEntryNotFound      = errors.New("entry not found")
	ErrEntryExists        = errors.New("entry already exists")
	ErrLockHeld           = errors.New("resource locked")
)

// Storage defines the interface for data storage operations
// This allows us to swap implementations (in-memory, Redis, etc.)
type Storage interface {
	// Slice operations
	CreateSlice(ctx context.Context, slice *models.Slice) error
	GetSlice(ctx context.Context, sliceID string) (*models.Slice, error)
	ListSlices(ctx context.Context, limit, offset int) ([]*models.Slice, error)
	ListSlicesByOwner(ctx context.Context, owner string, limit, offset int) ([]*models.Slice, error)
	SearchSlices(ctx context.Context, query string, limit, offset int) ([]*models.Slice, error)
	GetSliceMetadata(ctx context.Context, sliceID string) (*models.SliceMetadata, error)
	UpdateSliceMetadata(ctx context.Context, sliceID string, metadata *models.SliceMetadata) error
	GetRootSlice(ctx context.Context) (*models.Slice, error)
	InitializeRootSlice(ctx context.Context) error
	AddSliceCommit(ctx context.Context, sliceID string, commit *models.Commit) error
	ListSliceCommits(ctx context.Context, sliceID string, limit int, fromCommitHash string) ([]*models.Commit, error)

	// File indexing
	AddFileToSlice(ctx context.Context, fileID, sliceID string) error
	GetActiveSlicesForFile(ctx context.Context, fileID string) ([]string, error)
	RemoveFileFromSlice(ctx context.Context, fileID, sliceID string) error
	ListConflicts(ctx context.Context) ([]*models.FileConflict, error)
	ResolveConflict(ctx context.Context, fileID, preferredSliceID string) (*models.FileConflict, error)
	LockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) error
	UnlockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string)

	// Changesets
	CreateChangeset(ctx context.Context, changeset *models.Changeset) error
	GetChangeset(ctx context.Context, changesetID string) (*models.Changeset, error)
	ListChangesets(ctx context.Context, sliceID string, status *models.ChangesetStatus, limit int) ([]*models.Changeset, error)
	UpdateChangeset(ctx context.Context, changeset *models.Changeset) error

	// File content for checkout
	GetSliceFiles(ctx context.Context, sliceID string) ([]*models.FileContent, error)
	GetSliceFileByPath(ctx context.Context, sliceID, path string) (*models.FileContent, error)

	// Directory entries
	AddEntry(ctx context.Context, entry *models.DirectoryEntry) error
	GetEntry(ctx context.Context, entryID string) (*models.DirectoryEntry, error)
	GetEntryByPath(ctx context.Context, sliceID, path string) (*models.DirectoryEntry, error)
	ListEntries(ctx context.Context, sliceID, parentID string) ([]*models.DirectoryEntry, error)
	UpdateEntry(ctx context.Context, entry *models.DirectoryEntry) error
	DeleteEntry(ctx context.Context, entryID string) error

	// Global state
	GetGlobalState(ctx context.Context) (*models.GlobalState, error)
	UpdateGlobalState(ctx context.Context, state *models.GlobalState) error

	// Health check
	Ping(ctx context.Context) error
}
