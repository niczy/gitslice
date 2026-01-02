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

	// File indexing
	AddFileToSlice(ctx context.Context, fileID, sliceID string) error
	GetActiveSlicesForFile(ctx context.Context, fileID string) ([]string, error)
	RemoveFileFromSlice(ctx context.Context, fileID, sliceID string) error
	ListConflicts(ctx context.Context) ([]*models.FileConflict, error)
	ResolveConflict(ctx context.Context, fileID, preferredSliceID string) (*models.FileConflict, error)

	// Changesets
	CreateChangeset(ctx context.Context, changeset *models.Changeset) error
	GetChangeset(ctx context.Context, changesetID string) (*models.Changeset, error)
	ListChangesets(ctx context.Context, sliceID string, status *models.ChangesetStatus, limit int) ([]*models.Changeset, error)
	UpdateChangeset(ctx context.Context, changeset *models.Changeset) error

	// Health check
	Ping(ctx context.Context) error
}
