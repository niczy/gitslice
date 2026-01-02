package storage

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

// InMemoryStorage implements Storage interface with in-memory data structures
type InMemoryStorage struct {
	mu sync.RWMutex

	// Slice storage
	slices        map[string]*models.Slice         // sliceID -> slice
	sliceMetadata map[string]*models.SliceMetadata // sliceID -> metadata

	// File indexing: fileID -> set of slice IDs
	fileIndex map[string]map[string]bool // fileID -> {sliceID: true}

	// File content storage
	fileContents map[string]*models.FileContent // fileID -> content

	// Changesets
	changesets      map[string]*models.Changeset // changesetID -> changeset
	sliceChangesets map[string][]string          // sliceID -> []changesetID
}

// NewInMemoryStorage creates a new in-memory storage instance
func NewInMemoryStorage() *InMemoryStorage {
	return &InMemoryStorage{
		slices:          make(map[string]*models.Slice),
		sliceMetadata:   make(map[string]*models.SliceMetadata),
		fileIndex:       make(map[string]map[string]bool),
		fileContents:    make(map[string]*models.FileContent),
		changesets:      make(map[string]*models.Changeset),
		sliceChangesets: make(map[string][]string),
	}
}

// CreateSlice creates a new slice
func (s *InMemoryStorage) CreateSlice(ctx context.Context, slice *models.Slice) error {
	if slice.ID == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.slices[slice.ID]; exists {
		return ErrSliceAlreadyExists
	}

	now := time.Now()
	slice.CreatedAt = now
	slice.UpdatedAt = now

	s.slices[slice.ID] = slice

	// Initialize metadata
	s.sliceMetadata[slice.ID] = &models.SliceMetadata{
		SliceID:            slice.ID,
		HeadCommitHash:     "",
		ModifiedFiles:      []string{},
		LastModified:       now,
		ModifiedFilesCount: 0,
	}

	// Index files
	for _, fileID := range slice.Files {
		if s.fileIndex[fileID] == nil {
			s.fileIndex[fileID] = make(map[string]bool)
		}
		s.fileIndex[fileID][slice.ID] = true
	}

	return nil
}

// GetSlice retrieves a slice by ID
func (s *InMemoryStorage) GetSlice(ctx context.Context, sliceID string) (*models.Slice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	slice, exists := s.slices[sliceID]
	if !exists {
		return nil, ErrSliceNotFound
	}

	// Return a copy to avoid race conditions
	copy := *slice
	return &copy, nil
}

// ListSlices retrieves all slices with pagination
func (s *InMemoryStorage) ListSlices(ctx context.Context, limit, offset int) ([]*models.Slice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	slices := make([]*models.Slice, 0, len(s.slices))
	for _, slice := range s.slices {
		slices = append(slices, slice)
	}

	// Apply pagination
	if offset >= len(slices) {
		return []*models.Slice{}, nil
	}

	end := offset + limit
	if end > len(slices) {
		end = len(slices)
	}

	return slices[offset:end], nil
}

// ListSlicesByOwner retrieves slices owned by a specific user
func (s *InMemoryStorage) ListSlicesByOwner(ctx context.Context, owner string, limit, offset int) ([]*models.Slice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*models.Slice
	for _, slice := range s.slices {
		for _, sliceOwner := range slice.Owners {
			if sliceOwner == owner {
				result = append(result, slice)
				break
			}
		}
	}

	// Apply pagination
	if offset >= len(result) {
		return []*models.Slice{}, nil
	}

	end := offset + limit
	if end > len(result) {
		end = len(result)
	}

	return result[offset:end], nil
}

// SearchSlices searches for slices by name or description
func (s *InMemoryStorage) SearchSlices(ctx context.Context, query string, limit, offset int) ([]*models.Slice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*models.Slice
	for _, slice := range s.slices {
		if contains(slice.Name, query) || contains(slice.Description, query) {
			result = append(result, slice)
		}
	}

	// Apply pagination
	if offset >= len(result) {
		return []*models.Slice{}, nil
	}

	end := offset + limit
	if end > len(result) {
		end = len(result)
	}

	return result[offset:end], nil
}

// GetSliceMetadata retrieves slice metadata
func (s *InMemoryStorage) GetSliceMetadata(ctx context.Context, sliceID string) (*models.SliceMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	metadata, exists := s.sliceMetadata[sliceID]
	if !exists {
		return nil, ErrSliceNotFound
	}

	// Return a copy to avoid race conditions
	copy := *metadata
	return &copy, nil
}

// UpdateSliceMetadata updates slice metadata
func (s *InMemoryStorage) UpdateSliceMetadata(ctx context.Context, sliceID string, metadata *models.SliceMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.sliceMetadata[sliceID]; !exists {
		return ErrSliceNotFound
	}

	metadata.LastModified = time.Now()
	s.sliceMetadata[sliceID] = metadata
	return nil
}

// AddFileToSlice adds a file to the index for a slice
func (s *InMemoryStorage) AddFileToSlice(ctx context.Context, fileID, sliceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.slices[sliceID]; !exists {
		return ErrSliceNotFound
	}

	if s.fileIndex[fileID] == nil {
		s.fileIndex[fileID] = make(map[string]bool)
	}
	s.fileIndex[fileID][sliceID] = true
	return nil
}

// GetActiveSlicesForFile retrieves all active slices for a file
func (s *InMemoryStorage) GetActiveSlicesForFile(ctx context.Context, fileID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sliceIDs := make([]string, 0)
	for sliceID := range s.fileIndex[fileID] {
		sliceIDs = append(sliceIDs, sliceID)
	}

	return sliceIDs, nil
}

// RemoveFileFromSlice removes a file from the index for a slice
func (s *InMemoryStorage) RemoveFileFromSlice(ctx context.Context, fileID, sliceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if slices, exists := s.fileIndex[fileID]; exists {
		delete(slices, sliceID)
		if len(slices) == 0 {
			delete(s.fileIndex, fileID)
		}
	}
	return nil
}

// ListConflicts returns files that are associated with more than one slice.
func (s *InMemoryStorage) ListConflicts(ctx context.Context) ([]*models.FileConflict, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var conflicts []*models.FileConflict
	for fileID, slices := range s.fileIndex {
		if len(slices) < 2 {
			continue
		}

		var sliceIDs []string
		for id := range slices {
			sliceIDs = append(sliceIDs, id)
		}
		sort.Strings(sliceIDs)

		conflicts = append(conflicts, &models.FileConflict{
			FileID:            fileID,
			ConflictingSlices: sliceIDs,
		})
	}

	return conflicts, nil
}

// ResolveConflict keeps the preferred slice mapped to the file and removes other associations.
func (s *InMemoryStorage) ResolveConflict(ctx context.Context, fileID, preferredSliceID string) (*models.FileConflict, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	slices, exists := s.fileIndex[fileID]
	if !exists {
		return &models.FileConflict{FileID: fileID, ConflictingSlices: []string{}}, nil
	}

	updated := make(map[string]bool)
	if preferredSliceID != "" {
		if _, ok := slices[preferredSliceID]; ok {
			updated[preferredSliceID] = true
		}
	}

	if len(updated) == 0 && len(slices) > 0 {
		// Default to keeping the first slice if preference was unknown
		for sliceID := range slices {
			updated[sliceID] = true
			break
		}
	}

	s.fileIndex[fileID] = updated

	var remaining []string
	for id := range updated {
		remaining = append(remaining, id)
	}
	sort.Strings(remaining)

	return &models.FileConflict{FileID: fileID, ConflictingSlices: remaining}, nil
}

// CreateChangeset stores a new changeset
func (s *InMemoryStorage) CreateChangeset(ctx context.Context, changeset *models.Changeset) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.slices[changeset.SliceID]; !exists {
		return ErrSliceNotFound
	}

	s.changesets[changeset.ID] = changeset
	s.sliceChangesets[changeset.SliceID] = append([]string{changeset.ID}, s.sliceChangesets[changeset.SliceID]...)
	return nil
}

// GetChangeset retrieves a changeset by ID
func (s *InMemoryStorage) GetChangeset(ctx context.Context, changesetID string) (*models.Changeset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cs, ok := s.changesets[changesetID]
	if !ok {
		return nil, ErrChangesetNotFound
	}

	copy := *cs
	return &copy, nil
}

// ListChangesets returns changesets for a slice filtered by status and limited by count
func (s *InMemoryStorage) ListChangesets(ctx context.Context, sliceID string, status *models.ChangesetStatus, limit int) ([]*models.Changeset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.sliceChangesets[sliceID]
	if len(ids) == 0 {
		return []*models.Changeset{}, nil
	}

	var result []*models.Changeset
	for _, id := range ids {
		cs, ok := s.changesets[id]
		if !ok {
			continue
		}
		if status != nil && cs.Status != *status {
			continue
		}

		copy := *cs
		result = append(result, &copy)

		if limit > 0 && len(result) >= limit {
			break
		}
	}

	return result, nil
}

// UpdateChangeset replaces an existing changeset entry
func (s *InMemoryStorage) UpdateChangeset(ctx context.Context, changeset *models.Changeset) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.changesets[changeset.ID]; !exists {
		return ErrChangesetNotFound
	}

	s.changesets[changeset.ID] = changeset
	return nil
}

// Ping checks if storage is accessible
func (s *InMemoryStorage) Ping(ctx context.Context) error {
	return nil
}

// contains checks if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || findSubstring(s, substr))
}

// findSubstring is a simple substring finder
func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// GetSliceFiles returns all files for a slice
func (s *InMemoryStorage) GetSliceFiles(ctx context.Context, sliceID string) ([]*models.FileContent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	slice, exists := s.slices[sliceID]
	if !exists {
		return nil, ErrSliceNotFound
	}

	var files []*models.FileContent
	for _, fileID := range slice.Files {
		if content, ok := s.fileContents[fileID]; ok {
			files = append(files, content)
		}
	}

	return files, nil
}

// AddFileContent adds or updates file content
func (s *InMemoryStorage) AddFileContent(ctx context.Context, content *models.FileContent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fileContents[content.FileID] = content
	return nil
}
