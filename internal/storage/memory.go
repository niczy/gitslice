package storage

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

// InMemoryStorage implements Storage interface with in-memory data structures
type InMemoryStorage struct {
	mu sync.RWMutex

	// Lock tracking
	lockedSlices map[string]bool   // sliceID -> locked
	fileLocks    map[string]string // fileID -> owning sliceID

	// Slice storage
	slices        map[string]*models.Slice         // sliceID -> slice
	sliceMetadata map[string]*models.SliceMetadata // sliceID -> metadata

	// File indexing: fileID -> set of slice IDs
	fileIndex map[string]map[string]bool // fileID -> {sliceID: true}

	// File content storage
	fileContents       map[string]*models.FileContent  // fileID -> content
	blocks             map[string][]byte               // block hash -> content
	manifests          map[string]*models.FileManifest // sliceID:path -> manifest
	versionedManifests map[string]*models.FileManifest // file hash -> manifest

	// Directory entries
	entries         map[string]*models.DirectoryEntry // entryID -> entry
	entriesByPath   map[string]string                 // sliceID:path -> entryID
	entriesBySlice  map[string][]string               // sliceID -> []entryID
	entriesByParent map[string][]string               // parentID -> []entryID (direct children)

	// Changesets
	changesets                map[string]*models.Changeset         // changesetID -> changeset
	sliceChangesets           map[string][]string                  // sliceID -> []changesetID
	changesetSnapshots        map[string]*models.ChangesetSnapshot // snapshotID -> snapshot
	changesetSnapshotVersions map[string][]string                  // changesetID -> []snapshotID (newest first)

	// Commit history
	sliceCommits       map[string][]*models.Commit // sliceID -> commits (newest first)
	commitsBySliceHash map[string]map[string]*models.Commit

	// Global state
	globalState *models.GlobalState

	// Versioned file storage
	commitSnapshots  map[string]*models.CommitSnapshot // commitHash -> snapshot
	versionedContent map[string]*models.FileContent    // contentHash -> content

	// File change history
	fileChanges         map[string]*models.FileChangeRecord // changeID -> record
	fileChangesByPath   map[string][]string                 // "sliceID:path" -> []changeID (newest first)
	fileChangesByCommit map[string][]string                 // commitHash -> []changeID
	fileChangesByDir    map[string][]string                 // "sliceID:dirPrefix" -> []changeID (newest first)

	// Accounts / Orgs
	users                            map[string]*models.User                          // username -> user
	userByEmail                      map[string]string                                // lower(email) -> username
	repoBindings                     map[string]*models.RepoBinding                   // bindingID -> binding
	repoBindingsByPath               map[string]string                                // sliceID:path -> bindingID
	repoBindingsByOwner              map[string]map[string]bool                       // username -> bindingID -> true
	authSessions                     map[string]*models.AuthSession                   // sessionID -> auth session
	authSessionByToken               map[string]string                                // token -> sessionID
	authSessionByRefreshToken        map[string]string                                // refresh token -> sessionID
	authSessionsByUser               map[string]map[string]bool                       // username -> sessionID -> true
	deviceAuthorizationsByDeviceCode map[string]*models.DeviceAuthorization           // device code -> auth request
	deviceAuthorizationByUserCode    map[string]string                                // user code -> device code
	orgs                             map[string]*models.Organization                  // orgSlug -> org
	orgMembers                       map[string]map[string]*models.OrganizationMember // orgSlug -> username -> membership
	userOrgs                         map[string]map[string]bool                       // username -> orgSlug -> true
	orgInvites                       map[string]map[string]*models.OrganizationInvite // orgSlug -> inviteID -> invite
	teams                            map[string]*models.Team                          // teamID -> team
	teamsByOrg                       map[string]map[string]bool                       // orgSlug -> teamID -> true
	teamMembers                      map[string]map[string]*models.TeamMember         // teamID -> username -> membership
	environments                     map[string]*models.Environment                   // env name -> environment

	// Agent sessions
	agentSessions      map[string]*models.AgentSession        // sessionID -> session
	activeAgentBySlice map[string]string                      // sliceID -> active sessionID
	agentSessionEvents map[string][]*models.AgentSessionEvent // sessionID -> events ordered by seq asc
	agentSessionAudit  map[string][]*models.AgentSessionAudit // sessionID -> audit ordered by created asc
	nextAuditID        int64
	nextChangesetSeq   int64
}

// NewInMemoryStorage creates a new in-memory storage instance
func NewInMemoryStorage() *InMemoryStorage {
	return &InMemoryStorage{
		slices:                           make(map[string]*models.Slice),
		sliceMetadata:                    make(map[string]*models.SliceMetadata),
		fileIndex:                        make(map[string]map[string]bool),
		fileContents:                     make(map[string]*models.FileContent),
		blocks:                           make(map[string][]byte),
		manifests:                        make(map[string]*models.FileManifest),
		versionedManifests:               make(map[string]*models.FileManifest),
		entries:                          make(map[string]*models.DirectoryEntry),
		entriesByPath:                    make(map[string]string),
		entriesBySlice:                   make(map[string][]string),
		entriesByParent:                  make(map[string][]string),
		changesets:                       make(map[string]*models.Changeset),
		sliceChangesets:                  make(map[string][]string),
		changesetSnapshots:               make(map[string]*models.ChangesetSnapshot),
		changesetSnapshotVersions:        make(map[string][]string),
		sliceCommits:                     make(map[string][]*models.Commit),
		commitsBySliceHash:               make(map[string]map[string]*models.Commit),
		lockedSlices:                     make(map[string]bool),
		fileLocks:                        make(map[string]string),
		commitSnapshots:                  make(map[string]*models.CommitSnapshot),
		versionedContent:                 make(map[string]*models.FileContent),
		fileChanges:                      make(map[string]*models.FileChangeRecord),
		fileChangesByPath:                make(map[string][]string),
		fileChangesByCommit:              make(map[string][]string),
		fileChangesByDir:                 make(map[string][]string),
		users:                            make(map[string]*models.User),
		userByEmail:                      make(map[string]string),
		repoBindings:                     make(map[string]*models.RepoBinding),
		repoBindingsByPath:               make(map[string]string),
		repoBindingsByOwner:              make(map[string]map[string]bool),
		authSessions:                     make(map[string]*models.AuthSession),
		authSessionByToken:               make(map[string]string),
		authSessionByRefreshToken:        make(map[string]string),
		authSessionsByUser:               make(map[string]map[string]bool),
		deviceAuthorizationsByDeviceCode: make(map[string]*models.DeviceAuthorization),
		deviceAuthorizationByUserCode:    make(map[string]string),
		orgs:                             make(map[string]*models.Organization),
		orgMembers:                       make(map[string]map[string]*models.OrganizationMember),
		userOrgs:                         make(map[string]map[string]bool),
		orgInvites:                       make(map[string]map[string]*models.OrganizationInvite),
		teams:                            make(map[string]*models.Team),
		teamsByOrg:                       make(map[string]map[string]bool),
		teamMembers:                      make(map[string]map[string]*models.TeamMember),
		environments:                     make(map[string]*models.Environment),
		agentSessions:                    make(map[string]*models.AgentSession),
		activeAgentBySlice:               make(map[string]string),
		agentSessionEvents:               make(map[string][]*models.AgentSessionEvent),
		agentSessionAudit:                make(map[string][]*models.AgentSessionAudit),
		nextAuditID:                      1,
		nextChangesetSeq:                 1,
		globalState: &models.GlobalState{
			GlobalCommitHash: "global-init",
			Timestamp:        time.Now(),
			History:          []*models.GlobalCommit{},
		},
	}
}

// Reset clears all in-memory state.
//
// This is intentionally not part of the Storage interface; it's an admin/ops escape hatch.
func (s *InMemoryStorage) Reset(ctx context.Context) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	fresh := NewInMemoryStorage()
	// Do not overwrite the mutex while it's locked; copy the state fields instead.
	s.lockedSlices = fresh.lockedSlices
	s.fileLocks = fresh.fileLocks
	s.slices = fresh.slices
	s.sliceMetadata = fresh.sliceMetadata
	s.fileIndex = fresh.fileIndex
	s.fileContents = fresh.fileContents
	s.blocks = fresh.blocks
	s.manifests = fresh.manifests
	s.versionedManifests = fresh.versionedManifests
	s.entries = fresh.entries
	s.entriesByPath = fresh.entriesByPath
	s.entriesBySlice = fresh.entriesBySlice
	s.entriesByParent = fresh.entriesByParent
	s.changesets = fresh.changesets
	s.sliceChangesets = fresh.sliceChangesets
	s.changesetSnapshots = fresh.changesetSnapshots
	s.changesetSnapshotVersions = fresh.changesetSnapshotVersions
	s.sliceCommits = fresh.sliceCommits
	s.commitsBySliceHash = fresh.commitsBySliceHash
	s.globalState = fresh.globalState
	s.commitSnapshots = fresh.commitSnapshots
	s.versionedContent = fresh.versionedContent
	s.fileChanges = fresh.fileChanges
	s.fileChangesByPath = fresh.fileChangesByPath
	s.fileChangesByCommit = fresh.fileChangesByCommit
	s.fileChangesByDir = fresh.fileChangesByDir
	s.users = fresh.users
	s.userByEmail = fresh.userByEmail
	s.repoBindings = fresh.repoBindings
	s.repoBindingsByPath = fresh.repoBindingsByPath
	s.repoBindingsByOwner = fresh.repoBindingsByOwner
	s.authSessions = fresh.authSessions
	s.authSessionByToken = fresh.authSessionByToken
	s.authSessionByRefreshToken = fresh.authSessionByRefreshToken
	s.authSessionsByUser = fresh.authSessionsByUser
	s.deviceAuthorizationsByDeviceCode = fresh.deviceAuthorizationsByDeviceCode
	s.deviceAuthorizationByUserCode = fresh.deviceAuthorizationByUserCode
	s.orgs = fresh.orgs
	s.orgMembers = fresh.orgMembers
	s.userOrgs = fresh.userOrgs
	s.orgInvites = fresh.orgInvites
	s.teams = fresh.teams
	s.teamsByOrg = fresh.teamsByOrg
	s.teamMembers = fresh.teamMembers
	s.environments = fresh.environments
	s.agentSessions = fresh.agentSessions
	s.activeAgentBySlice = fresh.activeAgentBySlice
	s.agentSessionEvents = fresh.agentSessionEvents
	s.agentSessionAudit = fresh.agentSessionAudit
	s.nextAuditID = fresh.nextAuditID
	s.nextChangesetSeq = fresh.nextChangesetSeq
	return nil
}

// LockSliceAndFiles acquires a lock on the slice and the provided files.
func (s *InMemoryStorage) LockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.slices[sliceID]; !exists {
		return ErrSliceNotFound
	}

	for _, fileID := range fileIDs {
		if owner, locked := s.fileLocks[fileID]; locked && owner != sliceID {
			return ErrLockHeld
		}
	}

	s.lockedSlices[sliceID] = true
	for _, fileID := range fileIDs {
		s.fileLocks[fileID] = sliceID
	}

	return nil
}

// UnlockSliceAndFiles releases a previously acquired lock.
func (s *InMemoryStorage) UnlockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.lockedSlices, sliceID)
	for _, fileID := range fileIDs {
		if owner, locked := s.fileLocks[fileID]; locked && owner == sliceID {
			delete(s.fileLocks, fileID)
		}
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

	if strings.TrimSpace(slice.Slug) == "" {
		for attempt := 1; ; attempt++ {
			candidate := sliceSlugCandidate(slice, attempt)
			if candidate == "" {
				return ErrInvalidInput
			}
			if _, err := s.getSliceBySlugLocked(candidate); errors.Is(err, ErrSliceNotFound) {
				slice.Slug = candidate
				break
			}
		}
	} else {
		slice.Slug = strings.TrimSpace(slice.Slug)
		if _, err := s.getSliceBySlugLocked(slice.Slug); err == nil {
			return ErrSliceAlreadyExists
		}
	}

	now := time.Now()
	slice.CreatedAt = now
	slice.UpdatedAt = now

	s.slices[slice.ID] = slice

	// Initialize metadata with initial commit hash
	initialCommitHash := fmt.Sprintf("init-%s", slice.ID)
	s.sliceMetadata[slice.ID] = &models.SliceMetadata{
		SliceID:            slice.ID,
		HeadCommitHash:     initialCommitHash,
		ModifiedFiles:      []string{},
		LastModified:       now,
		ModifiedFilesCount: 0,
	}

	// Initialize commit history slice
	if _, exists := s.sliceCommits[slice.ID]; !exists {
		s.sliceCommits[slice.ID] = []*models.Commit{}
	}

	// Index files
	for _, fileID := range slice.Files {
		if s.fileIndex[fileID] == nil {
			s.fileIndex[fileID] = make(map[string]bool)
		}
		s.fileIndex[fileID][slice.ID] = true
	}

	// Materialize directory-entry tree from the slice file set so file browsing can
	// list direct children without scanning all paths.
	s.materializeDirectoryTreeLocked(slice.ID, slice.Files, true)

	return nil
}

// DeleteSlice removes a slice and its slice-scoped metadata.
func (s *InMemoryStorage) DeleteSlice(ctx context.Context, sliceID string) error {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.slices[sliceID]; !exists {
		return ErrSliceNotFound
	}

	delete(s.slices, sliceID)
	delete(s.sliceMetadata, sliceID)
	delete(s.lockedSlices, sliceID)

	for fileID, ownerSliceID := range s.fileLocks {
		if ownerSliceID == sliceID {
			delete(s.fileLocks, fileID)
		}
	}

	for fileID, indexedSlices := range s.fileIndex {
		delete(indexedSlices, sliceID)
		if len(indexedSlices) == 0 {
			delete(s.fileIndex, fileID)
		}
	}

	entryIDs := append([]string(nil), s.entriesBySlice[sliceID]...)
	for _, entryID := range entryIDs {
		s.deleteEntryLocked(entryID)
	}
	delete(s.entriesBySlice, sliceID)
	delete(s.entriesByParent, sliceID)

	if changeIDs, ok := s.sliceChangesets[sliceID]; ok {
		for _, changeID := range changeIDs {
			delete(s.changesets, changeID)
			if versionIDs, ok := s.changesetSnapshotVersions[changeID]; ok {
				for _, versionID := range versionIDs {
					delete(s.changesetSnapshots, versionID)
				}
				delete(s.changesetSnapshotVersions, changeID)
			}
		}
		delete(s.sliceChangesets, sliceID)
	}

	delete(s.sliceCommits, sliceID)
	delete(s.commitsBySliceHash, sliceID)
	for commitHash, snapshot := range s.commitSnapshots {
		if snapshot != nil && snapshot.SliceID == sliceID {
			delete(s.commitSnapshots, commitHash)
		}
	}

	for changeID, change := range s.fileChanges {
		if change != nil && change.SliceID == sliceID {
			delete(s.fileChanges, changeID)
		}
	}
	s.rebuildFileChangeIndexesLocked()

	delete(s.activeAgentBySlice, sliceID)
	for sessionID, session := range s.agentSessions {
		if session != nil && session.SliceID == sliceID {
			delete(s.agentSessions, sessionID)
			delete(s.agentSessionEvents, sessionID)
			delete(s.agentSessionAudit, sessionID)
		}
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

// CountSlices returns the total number of slices stored.
func (s *InMemoryStorage) CountSlices(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.slices), nil
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

	if metadata.LastModified.IsZero() {
		metadata.LastModified = time.Now()
	}
	s.sliceMetadata[sliceID] = metadata
	return nil
}

// AddSliceCommit records a commit for a slice, keeping most recent commits first.
func (s *InMemoryStorage) AddSliceCommit(ctx context.Context, sliceID string, commit *models.Commit) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.slices[sliceID]; !exists {
		return ErrSliceNotFound
	}

	commitCopy := *commit
	s.sliceCommits[sliceID] = append([]*models.Commit{&commitCopy}, s.sliceCommits[sliceID]...)
	if s.commitsBySliceHash[sliceID] == nil {
		s.commitsBySliceHash[sliceID] = make(map[string]*models.Commit)
	}
	s.commitsBySliceHash[sliceID][commit.CommitHash] = &commitCopy
	return nil
}

// ListSliceCommits returns the commit history for a slice applying optional pagination.
func (s *InMemoryStorage) ListSliceCommits(ctx context.Context, sliceID string, limit int, fromCommitHash string) ([]*models.Commit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, exists := s.slices[sliceID]; !exists {
		return nil, ErrSliceNotFound
	}

	commits := s.sliceCommits[sliceID]
	start := 0
	if fromCommitHash != "" {
		for i, c := range commits {
			if c.CommitHash == fromCommitHash {
				start = i + 1
				break
			}
		}
	}

	if start > len(commits) {
		return []*models.Commit{}, nil
	}

	result := commits[start:]
	limit = normalizeSliceCommitLimit(limit)
	if limit < len(result) {
		result = result[:limit]
	}

	copy := make([]*models.Commit, 0, len(result))
	for _, c := range result {
		commitCopy := *c
		copy = append(copy, &commitCopy)
	}

	return copy, nil
}

func (s *InMemoryStorage) GetCommitByHash(ctx context.Context, sliceID, commitHash string) (*models.Commit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, exists := s.slices[sliceID]; !exists {
		return nil, ErrSliceNotFound
	}

	commitsForSlice := s.commitsBySliceHash[sliceID]
	if commitsForSlice == nil {
		return nil, ErrCommitNotFound
	}
	commit, exists := commitsForSlice[commitHash]
	if !exists {
		return nil, ErrCommitNotFound
	}

	commitCopy := *commit
	return &commitCopy, nil
}

// AddFileToSlice adds a file to the index for a slice
func (s *InMemoryStorage) AddFileToSlice(ctx context.Context, fileID, sliceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	slice, exists := s.slices[sliceID]
	if !exists {
		return ErrSliceNotFound
	}

	if slice.IsRoot {
		hasFile := false
		for _, existing := range slice.Files {
			if existing == fileID {
				hasFile = true
				break
			}
		}
		if !hasFile {
			slice.Files = append(slice.Files, fileID)
		}
		return nil
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

	if slice, exists := s.slices[sliceID]; exists && slice.IsRoot {
		filtered := make([]string, 0, len(slice.Files))
		for _, existing := range slice.Files {
			if existing != fileID {
				filtered = append(filtered, existing)
			}
		}
		slice.Files = filtered
	}

	if slices, exists := s.fileIndex[fileID]; exists {
		delete(slices, sliceID)
		if len(slices) == 0 {
			delete(s.fileIndex, fileID)
		}
	}
	return nil
}

// SetSliceFiles sets the immutable file list for a slice.
func (s *InMemoryStorage) SetSliceFiles(ctx context.Context, sliceID string, files []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	slice, exists := s.slices[sliceID]
	if !exists {
		return ErrSliceNotFound
	}

	if len(slice.Files) > 0 {
		return ErrSliceFilesImmutable
	}

	copied := make([]string, len(files))
	copy(copied, files)
	slice.Files = copied

	return nil
}

// UpdateSliceName updates the display name of a slice.
func (s *InMemoryStorage) UpdateSliceName(ctx context.Context, sliceID, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	slice, exists := s.slices[sliceID]
	if !exists {
		return ErrSliceNotFound
	}

	slice.Name = newName
	slice.UpdatedAt = time.Now()
	return nil
}

// UpdateSliceEnvironment sets the default environment for a slice.
func (s *InMemoryStorage) UpdateSliceEnvironment(ctx context.Context, sliceID, environment string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	slice, exists := s.slices[sliceID]
	if !exists {
		return ErrSliceNotFound
	}

	slice.Environment = environment
	slice.UpdatedAt = time.Now()
	return nil
}

// GetSliceByName retrieves the first non-root slice matching the given display name.
func (s *InMemoryStorage) GetSliceByName(ctx context.Context, name string) (*models.Slice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, slice := range s.slices {
		if slice.Name == name && !slice.IsRoot {
			copy := *slice
			return &copy, nil
		}
	}

	return nil, ErrSliceNotFound
}

func (s *InMemoryStorage) GetSliceBySlug(ctx context.Context, slug string) (*models.Slice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.getSliceBySlugLocked(slug)
}

func (s *InMemoryStorage) getSliceBySlugLocked(slug string) (*models.Slice, error) {
	for _, slice := range s.slices {
		if slice.Slug == slug {
			copy := *slice
			return &copy, nil
		}
	}

	return nil, ErrSliceNotFound
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

	if changeset == nil {
		return ErrInvalidInput
	}
	if _, exists := s.slices[changeset.SliceID]; !exists {
		return ErrSliceNotFound
	}
	if strings.TrimSpace(changeset.ID) == "" {
		changeset.ID = s.nextChangesetIDLocked()
	}
	if n, ok := parseGlobalChangesetSeq(changeset.ID); ok && n >= s.nextChangesetSeq {
		s.nextChangesetSeq = n + 1
	}

	s.changesets[changeset.ID] = changeset
	s.sliceChangesets[changeset.SliceID] = append([]string{changeset.ID}, s.sliceChangesets[changeset.SliceID]...)
	return nil
}

func (s *InMemoryStorage) nextChangesetIDLocked() string {
	if s.nextChangesetSeq <= 0 {
		s.nextChangesetSeq = 1
	}
	if s.nextChangesetSeq == 1 {
		var maxSeen int64
		for id := range s.changesets {
			if n, ok := parseGlobalChangesetSeq(id); ok && n > maxSeen {
				maxSeen = n
			}
		}
		if maxSeen >= s.nextChangesetSeq {
			s.nextChangesetSeq = maxSeen + 1
		}
	}
	id := fmt.Sprintf("cs-global-%d", s.nextChangesetSeq)
	s.nextChangesetSeq++
	return id
}

func parseGlobalChangesetSeq(id string) (int64, bool) {
	rawID := strings.TrimSpace(id)
	var raw string
	switch {
	case strings.HasPrefix(rawID, "cs-global-"):
		raw = strings.TrimSpace(strings.TrimPrefix(rawID, "cs-global-"))
	case strings.HasPrefix(rawID, "cs-"):
		// Backward compatibility for existing IDs.
		raw = strings.TrimSpace(strings.TrimPrefix(rawID, "cs-"))
	default:
		return 0, false
	}
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
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

func (s *InMemoryStorage) CreateChangesetSnapshot(ctx context.Context, snapshot *models.ChangesetSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if snapshot == nil || snapshot.ID == "" || snapshot.ChangesetID == "" || snapshot.Version <= 0 {
		return ErrInvalidInput
	}
	if _, exists := s.changesets[snapshot.ChangesetID]; !exists {
		return ErrChangesetNotFound
	}
	if _, exists := s.changesetSnapshots[snapshot.ID]; exists {
		return ErrInvalidInput
	}
	for _, existingID := range s.changesetSnapshotVersions[snapshot.ChangesetID] {
		if existing, ok := s.changesetSnapshots[existingID]; ok && existing.Version == snapshot.Version {
			return ErrInvalidInput
		}
	}

	copySnapshot := *snapshot
	copySnapshot.ModifiedFiles = append([]string(nil), snapshot.ModifiedFiles...)
	s.changesetSnapshots[snapshot.ID] = &copySnapshot
	s.changesetSnapshotVersions[snapshot.ChangesetID] = append(
		[]string{snapshot.ID},
		s.changesetSnapshotVersions[snapshot.ChangesetID]...,
	)
	return nil
}

func (s *InMemoryStorage) GetChangesetSnapshot(ctx context.Context, changesetID string, version int32) (*models.ChangesetSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.changesetSnapshotVersions[changesetID]
	if len(ids) == 0 {
		return nil, ErrChangesetNotFound
	}

	if version <= 0 {
		latest := s.changesetSnapshots[ids[0]]
		if latest == nil {
			return nil, ErrChangesetNotFound
		}
		copySnapshot := *latest
		copySnapshot.ModifiedFiles = append([]string(nil), latest.ModifiedFiles...)
		return &copySnapshot, nil
	}

	for _, id := range ids {
		snapshot, ok := s.changesetSnapshots[id]
		if !ok {
			continue
		}
		if snapshot.Version != version {
			continue
		}
		copySnapshot := *snapshot
		copySnapshot.ModifiedFiles = append([]string(nil), snapshot.ModifiedFiles...)
		return &copySnapshot, nil
	}

	return nil, ErrChangesetNotFound
}

func (s *InMemoryStorage) ListChangesetSnapshots(ctx context.Context, changesetID string, limit int) ([]*models.ChangesetSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.changesetSnapshotVersions[changesetID]
	if len(ids) == 0 {
		return []*models.ChangesetSnapshot{}, nil
	}

	if limit <= 0 || limit > len(ids) {
		limit = len(ids)
	}

	result := make([]*models.ChangesetSnapshot, 0, limit)
	for _, id := range ids[:limit] {
		snapshot, ok := s.changesetSnapshots[id]
		if !ok {
			continue
		}
		copySnapshot := *snapshot
		copySnapshot.ModifiedFiles = append([]string(nil), snapshot.ModifiedFiles...)
		result = append(result, &copySnapshot)
	}
	return result, nil
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

func (s *InMemoryStorage) PutBlock(ctx context.Context, hash string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(hash) == "" {
		return ErrInvalidInput
	}
	s.blocks[hash] = append([]byte(nil), data...)
	return nil
}

func (s *InMemoryStorage) GetBlock(ctx context.Context, hash string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, ok := s.blocks[strings.TrimSpace(hash)]
	if !ok {
		return nil, ErrEntryNotFound
	}
	return append([]byte(nil), data...), nil
}

func (s *InMemoryStorage) GetBlocks(ctx context.Context, hashes []string) (map[string][]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	blocks := make(map[string][]byte, len(hashes))
	for _, rawHash := range hashes {
		hash := strings.TrimSpace(rawHash)
		if hash == "" {
			return nil, ErrInvalidInput
		}
		if _, exists := blocks[hash]; exists {
			continue
		}
		data, ok := s.blocks[hash]
		if !ok {
			return nil, ErrEntryNotFound
		}
		blocks[hash] = append([]byte(nil), data...)
	}
	return blocks, nil
}

func (s *InMemoryStorage) HasBlock(ctx context.Context, hash string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.blocks[strings.TrimSpace(hash)]
	return ok, nil
}

func (s *InMemoryStorage) PutBlocks(ctx context.Context, blocks map[string][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for hash, data := range blocks {
		hash = strings.TrimSpace(hash)
		if hash == "" {
			return ErrInvalidInput
		}
		s.blocks[hash] = append([]byte(nil), data...)
	}
	return nil
}

func (s *InMemoryStorage) PutFileManifest(ctx context.Context, sliceID, path string, manifest *models.FileManifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sliceID = strings.TrimSpace(sliceID)
	path = cleanRelativePath(path)
	if sliceID == "" || path == "" || manifest == nil {
		return ErrInvalidInput
	}
	s.manifests[sliceID+":"+path] = cloneManifest(manifest)
	return nil
}

func (s *InMemoryStorage) GetFileManifest(ctx context.Context, sliceID, path string) (*models.FileManifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	manifest, ok := s.manifests[strings.TrimSpace(sliceID)+":"+cleanRelativePath(path)]
	if !ok {
		return nil, ErrEntryNotFound
	}
	return cloneManifest(manifest), nil
}

func (s *InMemoryStorage) DeleteFileManifest(ctx context.Context, sliceID, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := strings.TrimSpace(sliceID) + ":" + cleanRelativePath(path)
	if _, ok := s.manifests[key]; !ok {
		return ErrEntryNotFound
	}
	delete(s.manifests, key)
	return nil
}

func (s *InMemoryStorage) PutVersionedFileManifest(ctx context.Context, manifest *models.FileManifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if manifest == nil || strings.TrimSpace(manifest.Hash) == "" {
		return ErrInvalidInput
	}
	s.versionedManifests[strings.TrimSpace(manifest.Hash)] = cloneManifest(manifest)
	return nil
}

func (s *InMemoryStorage) GetVersionedFileManifest(ctx context.Context, hash string) (*models.FileManifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	manifest, ok := s.versionedManifests[strings.TrimSpace(hash)]
	if !ok {
		return nil, ErrEntryNotFound
	}
	return cloneManifest(manifest), nil
}

// GetRootSlice returns the root slice
func (s *InMemoryStorage) GetRootSlice(ctx context.Context) (*models.Slice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, slice := range s.slices {
		if slice.IsRoot {
			copy := *slice
			return &copy, nil
		}
	}

	return nil, ErrSliceNotFound
}

// InitializeRootSlice creates the root slice if it doesn't exist
func (s *InMemoryStorage) InitializeRootSlice(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if root slice already exists
	for _, slice := range s.slices {
		if slice.IsRoot {
			return nil
		}
	}

	rootSlice := &models.Slice{
		ID:          "root_slice",
		Name:        "Root Slice",
		Slug:        "root",
		Description: "The root slice containing all files",
		Files:       []string{},
		Owners:      []string{"system"},
		CreatedBy:   "system",
		IsRoot:      true,
	}

	now := time.Now()
	rootSlice.CreatedAt = now
	rootSlice.UpdatedAt = now

	s.slices[rootSlice.ID] = rootSlice
	s.sliceMetadata[rootSlice.ID] = &models.SliceMetadata{
		SliceID:            rootSlice.ID,
		HeadCommitHash:     "root-initial",
		ModifiedFiles:      []string{},
		LastModified:       now,
		ModifiedFilesCount: 0,
	}

	return nil
}

func sliceIDFromEntryID(entryID string) string {
	if entryID == "" {
		return ""
	}
	parts := strings.SplitN(entryID, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[0]
}

func inferSliceIDForEntry(entry *models.DirectoryEntry) string {
	if entry == nil {
		return ""
	}
	if s := sliceIDFromEntryID(entry.ID); s != "" {
		return s
	}
	// For nested entries, ParentID is often an entry ID (sliceID:path).
	if s := sliceIDFromEntryID(entry.ParentID); s != "" {
		return s
	}
	// Legacy behavior: many callers set ParentID=sliceID.
	return entry.ParentID
}

func entryIDForPath(sliceID, p string) string {
	if p == "" {
		// Root node uses the slice ID so callers can list root children via parentID=sliceID.
		return sliceID
	}
	return generateEntryID(sliceID, p)
}

func parentIDForPath(sliceID, p string) string {
	if p == "" {
		return ""
	}
	dir := path.Dir(p)
	if dir == "." || dir == "/" || dir == "" {
		return sliceID
	}
	return entryIDForPath(sliceID, dir)
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func removeString(ss []string, s string) []string {
	out := ss[:0]
	for _, v := range ss {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func (s *InMemoryStorage) upsertEntryLocked(sliceID string, e *models.DirectoryEntry) {
	if e == nil || e.ID == "" {
		return
	}

	if existing, ok := s.entries[e.ID]; ok {
		// Keep indexes consistent if the parent changes.
		if existing.ParentID != e.ParentID {
			s.entriesByParent[existing.ParentID] = removeString(s.entriesByParent[existing.ParentID], e.ID)
			if !containsString(s.entriesByParent[e.ParentID], e.ID) {
				s.entriesByParent[e.ParentID] = append(s.entriesByParent[e.ParentID], e.ID)
			}
		}

		// Replace the stored value with a copy to avoid external mutation.
		c := *e
		if len(e.Content) > 0 {
			c.Content = append([]byte(nil), e.Content...)
		}
		s.entries[e.ID] = &c
		s.entriesByPath[sliceID+":"+e.Path] = e.ID
		return
	}

	c := *e
	if len(e.Content) > 0 {
		c.Content = append([]byte(nil), e.Content...)
	}
	s.entries[e.ID] = &c
	s.entriesByPath[sliceID+":"+e.Path] = e.ID
	s.entriesBySlice[sliceID] = append(s.entriesBySlice[sliceID], e.ID)
	s.entriesByParent[e.ParentID] = append(s.entriesByParent[e.ParentID], e.ID)
}

func (s *InMemoryStorage) materializeDirectoryTreeLocked(sliceID string, rawFiles []string, includeFiles bool) {
	// Ensure the root node exists.
	s.upsertEntryLocked(sliceID, &models.DirectoryEntry{
		ID:       entryIDForPath(sliceID, ""),
		Path:     "",
		Type:     "directory",
		ParentID: "",
		Size:     0,
	})

	paths := make([]string, 0, len(rawFiles))
	seen := make(map[string]struct{}, len(rawFiles))
	for _, raw := range rawFiles {
		p := cleanRelativePath(raw)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	sort.Strings(paths)

	// Compute directory set from parent dirs and prefix relationships.
	dirsToCreate := make(map[string]bool)
	for _, p := range paths {
		for _, d := range extractParentDirs(p) {
			if d == "" {
				continue
			}
			dirsToCreate[d] = true
		}
	}
	for _, p := range paths {
		prefix := p + "/"
		i := sort.SearchStrings(paths, prefix)
		if i < len(paths) && strings.HasPrefix(paths[i], prefix) {
			dirsToCreate[p] = true
		}
	}

	// Insert directories parents-first.
	for _, dirPath := range sortDirsByDepth(dirsToCreate) {
		dirPath = cleanRelativePath(dirPath)
		if dirPath == "" {
			continue
		}
		s.upsertEntryLocked(sliceID, &models.DirectoryEntry{
			ID:       entryIDForPath(sliceID, dirPath),
			Path:     dirPath,
			Type:     "directory",
			ParentID: parentIDForPath(sliceID, dirPath),
			Size:     0,
		})
	}

	// Insert leaf files.
	if !includeFiles {
		return
	}
	for _, p := range paths {
		if dirsToCreate[p] {
			continue
		}
		s.upsertEntryLocked(sliceID, &models.DirectoryEntry{
			ID:       entryIDForPath(sliceID, p),
			Path:     p,
			Type:     "file",
			ParentID: parentIDForPath(sliceID, p),
			Size:     0,
		})
	}
}

// AddEntry adds a directory entry
func (s *InMemoryStorage) AddEntry(ctx context.Context, entry *models.DirectoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.ID == "" {
		return ErrInvalidInput
	}
	sliceID := inferSliceIDForEntry(entry)
	if sliceID == "" {
		return ErrInvalidInput
	}

	p := cleanRelativePath(entry.Path)
	typ := strings.TrimSpace(entry.Type)
	if typ == "" {
		typ = "file"
	}

	// Ensure parent directories exist (without creating placeholder file leaves).
	s.materializeDirectoryTreeLocked(sliceID, []string{p}, false)

	id := entry.ID
	if typ == "directory" {
		// Directory IDs must be deterministic so child-parent pointers can be computed.
		id = entryIDForPath(sliceID, p)
	}
	if existingID, ok := s.entriesByPath[sliceID+":"+p]; ok {
		// Path already exists (e.g. created from slice.Files); update that row.
		id = existingID
	}

	s.upsertEntryLocked(sliceID, &models.DirectoryEntry{
		ID:            id,
		Path:          p,
		Type:          typ,
		ParentID:      parentIDForPath(sliceID, p),
		Content:       entry.Content,
		Size:          entry.Size,
		Hash:          entry.Hash,
		Executable:    entry.Executable,
		SymlinkTarget: entry.SymlinkTarget,
	})

	return nil
}

// GetEntry retrieves a directory entry by ID
func (s *InMemoryStorage) GetEntry(ctx context.Context, entryID string) (*models.DirectoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.entries[entryID]
	if !exists {
		return nil, ErrEntryNotFound
	}

	copy := *entry
	copy.Hash = s.entryHashLocked(entry)
	return &copy, nil
}

// GetEntryByPath retrieves a directory entry by path for a slice
func (s *InMemoryStorage) GetEntryByPath(ctx context.Context, sliceID, path string) (*models.DirectoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entryID, ok := s.entriesByPath[sliceID+":"+path]
	if !ok {
		return nil, ErrEntryNotFound
	}

	entry, ok := s.entries[entryID]
	if !ok {
		return nil, ErrEntryNotFound
	}

	copy := *entry
	copy.Hash = s.entryHashLocked(entry)
	return &copy, nil
}

// ListEntries retrieves all entries for a slice with a given parent ID
func (s *InMemoryStorage) ListEntries(ctx context.Context, sliceID, parentID string) ([]*models.DirectoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*models.DirectoryEntry
	for _, entryID := range s.entriesByParent[parentID] {
		entry, ok := s.entries[entryID]
		if !ok {
			continue
		}
		// Defensive: parentID index is global; ensure we don't leak across slices when
		// entry IDs are slice-qualified. Legacy entries may not encode slice in ID.
		if sliceID != "" {
			if entrySlice := sliceIDFromEntryID(entry.ID); entrySlice != "" && entrySlice != sliceID {
				continue
			}
		}
		copy := *entry
		copy.Hash = s.entryHashLocked(entry)
		result = append(result, &copy)
	}

	return result, nil
}

func (s *InMemoryStorage) entryHashLocked(entry *models.DirectoryEntry) string {
	if entry == nil || entry.Type != "file" {
		return ""
	}
	sliceID := inferSliceIDForEntry(entry)
	if sliceID != "" {
		if manifest, ok := s.manifests[sliceID+":"+cleanRelativePath(entry.Path)]; ok && manifest != nil && strings.TrimSpace(manifest.Hash) != "" {
			return strings.TrimSpace(manifest.Hash)
		}
	}
	return strings.TrimSpace(entry.Hash)
}

// UpdateEntry updates a directory entry
func (s *InMemoryStorage) UpdateEntry(ctx context.Context, entry *models.DirectoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry == nil || entry.ID == "" {
		return ErrInvalidInput
	}

	prev, exists := s.entries[entry.ID]
	if !exists {
		return ErrEntryNotFound
	}

	// Derive slice ID from the stored entry; callers often pass legacy ParentID=sliceID.
	sliceID := inferSliceIDForEntry(prev)
	if sliceID == "" {
		return ErrInvalidInput
	}

	p := cleanRelativePath(entry.Path)
	if p == "" {
		p = prev.Path
	}
	typ := strings.TrimSpace(entry.Type)
	if typ == "" {
		typ = prev.Type
	}

	// Ensure parent directories exist and recompute parent pointer from the path.
	s.materializeDirectoryTreeLocked(sliceID, []string{p}, false)
	newParent := parentIDForPath(sliceID, p)

	// Maintain indexes if parent/path changed.
	if prev.ParentID != newParent {
		// Remove from old parent list.
		prevList := s.entriesByParent[prev.ParentID]
		out := prevList[:0]
		for _, id := range prevList {
			if id != entry.ID {
				out = append(out, id)
			}
		}
		if len(out) == 0 {
			delete(s.entriesByParent, prev.ParentID)
		} else {
			s.entriesByParent[prev.ParentID] = out
		}
		s.entriesByParent[newParent] = append(s.entriesByParent[newParent], entry.ID)
	}
	if prev.Path != p {
		delete(s.entriesByPath, sliceID+":"+prev.Path)
		s.entriesByPath[sliceID+":"+p] = entry.ID
	}

	updated := *entry
	updated.Path = p
	updated.Type = typ
	updated.ParentID = newParent
	if len(entry.Content) > 0 {
		updated.Content = append([]byte(nil), entry.Content...)
	}
	s.entries[entry.ID] = &updated
	return nil
}

// DeleteEntry removes a directory entry
func (s *InMemoryStorage) DeleteEntry(ctx context.Context, entryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.entries[entryID]; !exists {
		return ErrEntryNotFound
	}

	s.deleteEntryLocked(entryID)

	return nil
}

func (s *InMemoryStorage) deleteEntryLocked(entryID string) {
	entry, exists := s.entries[entryID]
	if !exists {
		return
	}

	delete(s.entries, entryID)

	sliceID := inferSliceIDForEntry(entry)
	if sliceID != "" {
		delete(s.entriesByPath, sliceID+":"+entry.Path)
		ids := s.entriesBySlice[sliceID]
		out := ids[:0]
		for _, id := range ids {
			if id != entryID {
				out = append(out, id)
			}
		}
		if len(out) == 0 {
			delete(s.entriesBySlice, sliceID)
		} else {
			s.entriesBySlice[sliceID] = out
		}
	}

	parentList := s.entriesByParent[entry.ParentID]
	outParent := parentList[:0]
	for _, id := range parentList {
		if id != entryID {
			outParent = append(outParent, id)
		}
	}
	if len(outParent) == 0 {
		delete(s.entriesByParent, entry.ParentID)
	} else {
		s.entriesByParent[entry.ParentID] = outParent
	}
}

// GetGlobalState returns the tracked global state snapshot.
func (s *InMemoryStorage) GetGlobalState(ctx context.Context) (*models.GlobalState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.globalState == nil {
		return nil, ErrInvalidInput
	}

	stateCopy := *s.globalState
	stateCopy.History = make([]*models.GlobalCommit, 0, len(s.globalState.History))
	for _, item := range s.globalState.History {
		entryCopy := *item
		stateCopy.History = append(stateCopy.History, &entryCopy)
	}

	return &stateCopy, nil
}

// UpdateGlobalState replaces the stored global state snapshot.
func (s *InMemoryStorage) UpdateGlobalState(ctx context.Context, state *models.GlobalState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stateCopy := *state
	stateCopy.History = make([]*models.GlobalCommit, 0, len(state.History))
	for _, item := range state.History {
		entryCopy := *item
		stateCopy.History = append(stateCopy.History, &entryCopy)
	}

	s.globalState = &stateCopy
	return nil
}

// RebuildIndexes is a no-op for the in-memory backend because indexes are kept in memory alongside data.
func (s *InMemoryStorage) RebuildIndexes(ctx context.Context) error {
	_ = ctx
	return nil
}

// GetCommitSnapshot retrieves a commit snapshot by hash.
func (s *InMemoryStorage) GetCommitSnapshot(ctx context.Context, commitHash string) (*models.CommitSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot, exists := s.commitSnapshots[commitHash]
	if !exists {
		return nil, ErrCommitNotFound
	}

	// Return a copy
	copySnapshot := *snapshot
	copySnapshot.Files = make(map[string]string, len(snapshot.Files))
	for k, v := range snapshot.Files {
		copySnapshot.Files[k] = v
	}
	return &copySnapshot, nil
}

// SaveCommitSnapshot stores a commit snapshot.
func (s *InMemoryStorage) SaveCommitSnapshot(ctx context.Context, snapshot *models.CommitSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if snapshot.CommitHash == "" {
		return ErrInvalidInput
	}

	// Store a copy
	copySnapshot := *snapshot
	copySnapshot.Files = make(map[string]string, len(snapshot.Files))
	for k, v := range snapshot.Files {
		copySnapshot.Files[k] = v
	}
	s.commitSnapshots[snapshot.CommitHash] = &copySnapshot
	return nil
}

// GetFileAtCommit retrieves a file's content at a specific commit.
func (s *InMemoryStorage) GetFileAtCommit(ctx context.Context, commitHash, path string) (*models.FileContent, error) {
	s.mu.RLock()
	snapshot, exists := s.commitSnapshots[commitHash]
	if !exists {
		s.mu.RUnlock()
		return nil, ErrCommitNotFound
	}

	contentHash, exists := snapshot.Files[path]
	if !exists {
		s.mu.RUnlock()
		return nil, ErrEntryNotFound
	}
	s.mu.RUnlock()

	content, err := ReadVersionedFileContent(ctx, s, contentHash)
	if err != nil {
		return nil, err
	}

	// The underlying blob store is keyed only by hash; the caller's requested
	// path is the authoritative lookup key at a given commit.
	content.Path = path
	content.FileID = path
	return content, nil
}

// ListFilesAtCommit lists all files at a specific commit, optionally filtered by path prefix.
func (s *InMemoryStorage) ListFilesAtCommit(ctx context.Context, commitHash, pathPrefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot, exists := s.commitSnapshots[commitHash]
	if !exists {
		return nil, ErrCommitNotFound
	}

	var files []string
	for path := range snapshot.Files {
		if pathPrefix == "" || strings.HasPrefix(path, pathPrefix) {
			files = append(files, path)
		}
	}

	sort.Strings(files)
	return files, nil
}

// ============ File Change History Operations ============

// AddFileChange records a file change associated with a commit.
func (s *InMemoryStorage) AddFileChange(ctx context.Context, change *models.FileChangeRecord) error {
	if change.ID == "" || change.Path == "" || change.CommitHash == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Store the change record
	changeCopy := *change
	s.fileChanges[change.ID] = &changeCopy

	// Index by path (prepend for newest-first ordering)
	pathKey := change.SliceID + ":" + change.Path
	s.fileChangesByPath[pathKey] = append([]string{change.ID}, s.fileChangesByPath[pathKey]...)

	// Index by commit
	s.fileChangesByCommit[change.CommitHash] = append(s.fileChangesByCommit[change.CommitHash], change.ID)

	// Index by directory prefixes (for directory history queries)
	s.indexChangeByDirectories(change.SliceID, change.Path, change.ID)

	return nil
}

// indexChangeByDirectories adds change ID to all parent directory indexes.
func (s *InMemoryStorage) indexChangeByDirectories(sliceID, path, changeID string) {
	// Index each directory level
	parts := strings.Split(path, "/")
	for i := range len(parts) - 1 {
		dirPrefix := strings.Join(parts[:i+1], "/") + "/"
		dirKey := sliceID + ":" + dirPrefix
		s.fileChangesByDir[dirKey] = append([]string{changeID}, s.fileChangesByDir[dirKey]...)
	}

	// Also index root directory (empty prefix means all files)
	rootKey := sliceID + ":"
	s.fileChangesByDir[rootKey] = append([]string{changeID}, s.fileChangesByDir[rootKey]...)
}

func (s *InMemoryStorage) rebuildFileChangeIndexesLocked() {
	s.fileChangesByPath = make(map[string][]string)
	s.fileChangesByCommit = make(map[string][]string)
	s.fileChangesByDir = make(map[string][]string)

	changes := make([]*models.FileChangeRecord, 0, len(s.fileChanges))
	for _, change := range s.fileChanges {
		if change != nil {
			changes = append(changes, change)
		}
	}

	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Timestamp.Equal(changes[j].Timestamp) {
			return changes[i].ID > changes[j].ID
		}
		return changes[i].Timestamp.After(changes[j].Timestamp)
	})

	for _, change := range changes {
		pathKey := change.SliceID + ":" + change.Path
		s.fileChangesByPath[pathKey] = append(s.fileChangesByPath[pathKey], change.ID)
		s.fileChangesByCommit[change.CommitHash] = append(s.fileChangesByCommit[change.CommitHash], change.ID)
		s.indexChangeByDirectories(change.SliceID, change.Path, change.ID)
	}
}

// AddFileChanges records multiple file changes in a batch.
func (s *InMemoryStorage) AddFileChanges(ctx context.Context, changes []*models.FileChangeRecord) error {
	for _, change := range changes {
		if err := s.AddFileChange(ctx, change); err != nil {
			return err
		}
	}
	return nil
}

// GetFileHistory retrieves the change history for a specific file path.
func (s *InMemoryStorage) GetFileHistory(ctx context.Context, sliceID, path string, limit int, fromCommit string) ([]*models.FileChangeRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pathKey := sliceID + ":" + path
	changeIDs := s.fileChangesByPath[pathKey]

	return s.getChangesFromIDs(changeIDs, limit, fromCommit)
}

// GetDirectoryHistory retrieves change history for all files under a directory.
func (s *InMemoryStorage) GetDirectoryHistory(ctx context.Context, sliceID, pathPrefix string, limit int, fromCommit string) ([]*models.FileChangeRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Normalize path prefix
	if pathPrefix != "" && !strings.HasSuffix(pathPrefix, "/") {
		pathPrefix += "/"
	}

	dirKey := sliceID + ":" + pathPrefix
	changeIDs := s.fileChangesByDir[dirKey]

	return s.getChangesFromIDs(changeIDs, limit, fromCommit)
}

// GetCommitChanges retrieves all file changes made in a specific commit.
func (s *InMemoryStorage) GetCommitChanges(ctx context.Context, commitHash string) ([]*models.FileChangeRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	changeIDs := s.fileChangesByCommit[commitHash]
	if len(changeIDs) == 0 {
		return []*models.FileChangeRecord{}, nil
	}

	var result []*models.FileChangeRecord
	for _, id := range changeIDs {
		if change, exists := s.fileChanges[id]; exists {
			changeCopy := *change
			result = append(result, &changeCopy)
		}
	}

	return result, nil
}

// QueryFileHistory performs a flexible query on file change history.
func (s *InMemoryStorage) QueryFileHistory(ctx context.Context, query *models.FileHistoryQuery) (*models.FileHistoryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var candidates []*models.FileChangeRecord

	// Determine which index to use
	if query.Path != "" {
		// Exact path match
		pathKey := query.SliceID + ":" + query.Path
		changeIDs := s.fileChangesByPath[pathKey]
		for _, id := range changeIDs {
			if change, exists := s.fileChanges[id]; exists {
				candidates = append(candidates, change)
			}
		}
	} else if query.PathPrefix != "" {
		// Directory prefix match
		prefix := query.PathPrefix
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		dirKey := query.SliceID + ":" + prefix
		changeIDs := s.fileChangesByDir[dirKey]
		for _, id := range changeIDs {
			if change, exists := s.fileChanges[id]; exists {
				candidates = append(candidates, change)
			}
		}
	} else {
		// All changes for slice
		dirKey := query.SliceID + ":"
		changeIDs := s.fileChangesByDir[dirKey]
		for _, id := range changeIDs {
			if change, exists := s.fileChanges[id]; exists {
				candidates = append(candidates, change)
			}
		}
	}

	// Apply filters
	var filtered []*models.FileChangeRecord
	for _, change := range candidates {
		if !s.matchesQueryFilters(change, query) {
			continue
		}
		filtered = append(filtered, change)
	}

	// Sort by timestamp descending (newest first)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp.After(filtered[j].Timestamp)
	})

	totalCount := len(filtered)

	// Apply offset and limit
	if query.Offset > 0 {
		if query.Offset >= len(filtered) {
			filtered = nil
		} else {
			filtered = filtered[query.Offset:]
		}
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 50 // Default limit
	}

	hasMore := len(filtered) > limit
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	// Create copies
	var result []*models.FileChangeRecord
	for _, change := range filtered {
		changeCopy := *change
		result = append(result, &changeCopy)
	}

	return &models.FileHistoryResult{
		Changes:    result,
		TotalCount: totalCount,
		HasMore:    hasMore,
	}, nil
}

// matchesQueryFilters checks if a change matches the query filters.
func (s *InMemoryStorage) matchesQueryFilters(change *models.FileChangeRecord, query *models.FileHistoryQuery) bool {
	// Filter by change types
	if len(query.ChangeTypes) > 0 {
		found := false
		for _, ct := range query.ChangeTypes {
			if change.ChangeType == ct {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Filter by author
	if query.Author != "" && change.Author != query.Author {
		return false
	}

	// Filter by time range
	if query.FromTimestamp != nil && change.Timestamp.Before(*query.FromTimestamp) {
		return false
	}
	if query.ToTimestamp != nil && change.Timestamp.After(*query.ToTimestamp) {
		return false
	}

	return true
}

// GetDirectorySummary gets an aggregated summary of changes for a directory.
func (s *InMemoryStorage) GetDirectorySummary(ctx context.Context, sliceID, pathPrefix string) (*models.DirectoryChangeSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Normalize path prefix
	if pathPrefix != "" && !strings.HasSuffix(pathPrefix, "/") {
		pathPrefix += "/"
	}

	dirKey := sliceID + ":" + pathPrefix
	changeIDs := s.fileChangesByDir[dirKey]

	if len(changeIDs) == 0 {
		return &models.DirectoryChangeSummary{
			Path:          pathPrefix,
			TotalChanges:  0,
			FilesChanged:  0,
			ChangesByType: make(map[models.ChangeType]int),
		}, nil
	}

	uniqueFiles := make(map[string]bool)
	changesByType := make(map[models.ChangeType]int)
	var lastChange *models.FileChangeRecord
	var latestTimestamp time.Time

	for _, id := range changeIDs {
		change, exists := s.fileChanges[id]
		if !exists {
			continue
		}

		uniqueFiles[change.Path] = true
		changesByType[change.ChangeType]++

		if lastChange == nil || change.Timestamp.After(latestTimestamp) {
			changeCopy := *change
			lastChange = &changeCopy
			latestTimestamp = change.Timestamp
		}
	}

	return &models.DirectoryChangeSummary{
		Path:          pathPrefix,
		TotalChanges:  len(changeIDs),
		FilesChanged:  len(uniqueFiles),
		LastChange:    lastChange,
		ChangesByType: changesByType,
	}, nil
}

// getChangesFromIDs retrieves changes from a list of IDs with pagination.
func (s *InMemoryStorage) getChangesFromIDs(changeIDs []string, limit int, fromCommit string) ([]*models.FileChangeRecord, error) {
	if len(changeIDs) == 0 {
		return []*models.FileChangeRecord{}, nil
	}

	// Find starting point if fromCommit is specified
	startIdx := 0
	if fromCommit != "" {
		for i, id := range changeIDs {
			if change, exists := s.fileChanges[id]; exists {
				if change.CommitHash == fromCommit {
					startIdx = i + 1
					break
				}
			}
		}
	}

	if startIdx >= len(changeIDs) {
		return []*models.FileChangeRecord{}, nil
	}

	// Apply limit
	endIdx := len(changeIDs)
	if limit > 0 && startIdx+limit < endIdx {
		endIdx = startIdx + limit
	}

	var result []*models.FileChangeRecord
	for _, id := range changeIDs[startIdx:endIdx] {
		if change, exists := s.fileChanges[id]; exists {
			changeCopy := *change
			result = append(result, &changeCopy)
		}
	}

	return result, nil
}

// ============ Agent Session Operations ============

func (s *InMemoryStorage) CreateAgentSession(ctx context.Context, session *models.AgentSession) error {
	_ = ctx
	if session == nil || session.SessionID == "" || session.SliceID == "" || session.UserID == "" || session.Provider == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.agentSessions[session.SessionID]; exists {
		return ErrAgentSessionConflict
	}
	if activeID, ok := s.activeAgentBySlice[session.SliceID]; ok && activeID != "" {
		if current, ok := s.agentSessions[activeID]; ok && current.State.IsActive() {
			return ErrAgentSessionConflict
		}
	}

	copySession := cloneAgentSession(session)
	s.agentSessions[session.SessionID] = copySession
	if copySession.State.IsActive() {
		s.activeAgentBySlice[copySession.SliceID] = copySession.SessionID
	}
	return nil
}

func (s *InMemoryStorage) GetAgentSession(ctx context.Context, sessionID string) (*models.AgentSession, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.agentSessions[sessionID]
	if !ok {
		return nil, ErrAgentSessionNotFound
	}
	return cloneAgentSession(session), nil
}

func (s *InMemoryStorage) GetActiveAgentSessionBySlice(ctx context.Context, sliceID string) (*models.AgentSession, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	activeID, ok := s.activeAgentBySlice[sliceID]
	if !ok || activeID == "" {
		return nil, ErrAgentSessionNotFound
	}
	session, ok := s.agentSessions[activeID]
	if !ok || !session.State.IsActive() {
		return nil, ErrAgentSessionNotFound
	}
	return cloneAgentSession(session), nil
}

func (s *InMemoryStorage) ListAgentSessionsByState(ctx context.Context, states []models.AgentSessionState, limit int) ([]*models.AgentSession, error) {
	_ = ctx
	if len(states) == 0 {
		return []*models.AgentSession{}, nil
	}
	stateSet := make(map[models.AgentSessionState]struct{}, len(states))
	for _, state := range states {
		stateSet[state] = struct{}{}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*models.AgentSession, 0)
	for _, session := range s.agentSessions {
		if _, ok := stateSet[session.State]; !ok {
			continue
		}
		out = append(out, cloneAgentSession(session))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *InMemoryStorage) UpdateAgentSession(ctx context.Context, session *models.AgentSession) error {
	_ = ctx
	if session == nil || session.SessionID == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.agentSessions[session.SessionID]
	if !ok {
		return ErrAgentSessionNotFound
	}

	if current.State.IsActive() && !session.State.IsActive() {
		if activeID, ok := s.activeAgentBySlice[current.SliceID]; ok && activeID == current.SessionID {
			delete(s.activeAgentBySlice, current.SliceID)
		}
	}
	if session.State.IsActive() {
		if activeID, ok := s.activeAgentBySlice[session.SliceID]; ok && activeID != "" && activeID != session.SessionID {
			if existing, ok := s.agentSessions[activeID]; ok && existing.State.IsActive() {
				return ErrAgentSessionConflict
			}
		}
		s.activeAgentBySlice[session.SliceID] = session.SessionID
	}

	s.agentSessions[session.SessionID] = cloneAgentSession(session)
	return nil
}

func (s *InMemoryStorage) AppendAgentSessionEvent(ctx context.Context, event *models.AgentSessionEvent) error {
	_ = ctx
	if event == nil || event.SessionID == "" || event.Stream == "" || event.Type == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.agentSessions[event.SessionID]; !ok {
		return ErrAgentSessionNotFound
	}
	events := s.agentSessionEvents[event.SessionID]
	if len(events) > 0 && event.Seq <= events[len(events)-1].Seq {
		return ErrAgentSessionConflict
	}
	s.agentSessionEvents[event.SessionID] = append(events, cloneAgentSessionEvent(event))
	return nil
}

func (s *InMemoryStorage) ListAgentSessionEvents(ctx context.Context, sessionID string, sinceSeq uint64, limit int) ([]*models.AgentSessionEvent, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.agentSessions[sessionID]; !ok {
		return nil, ErrAgentSessionNotFound
	}
	events := s.agentSessionEvents[sessionID]
	if limit <= 0 {
		limit = 200
	}

	out := make([]*models.AgentSessionEvent, 0, limit)
	for _, event := range events {
		if event.Seq <= sinceSeq {
			continue
		}
		out = append(out, cloneAgentSessionEvent(event))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *InMemoryStorage) AddAgentSessionAudit(ctx context.Context, audit *models.AgentSessionAudit) error {
	_ = ctx
	if audit == nil || audit.SessionID == "" || audit.Action == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.agentSessions[audit.SessionID]; !ok {
		return ErrAgentSessionNotFound
	}
	copyAudit := cloneAgentSessionAudit(audit)
	if copyAudit.ID == 0 {
		copyAudit.ID = s.nextAuditID
		s.nextAuditID++
	}
	if copyAudit.CreatedAt.IsZero() {
		copyAudit.CreatedAt = time.Now()
	}
	s.agentSessionAudit[audit.SessionID] = append(s.agentSessionAudit[audit.SessionID], copyAudit)
	return nil
}

func cloneAgentSession(in *models.AgentSession) *models.AgentSession {
	if in == nil {
		return nil
	}
	out := *in
	if in.StartedAt != nil {
		ts := *in.StartedAt
		out.StartedAt = &ts
	}
	if in.LastActivityAt != nil {
		ts := *in.LastActivityAt
		out.LastActivityAt = &ts
	}
	if in.StoppedAt != nil {
		ts := *in.StoppedAt
		out.StoppedAt = &ts
	}
	return &out
}

func cloneAgentSessionEvent(in *models.AgentSessionEvent) *models.AgentSessionEvent {
	if in == nil {
		return nil
	}
	out := *in
	if in.Payload != nil {
		out.Payload = append([]byte(nil), in.Payload...)
	}
	return &out
}

func cloneAgentSessionAudit(in *models.AgentSessionAudit) *models.AgentSessionAudit {
	if in == nil {
		return nil
	}
	out := *in
	if in.Metadata != nil {
		out.Metadata = append([]byte(nil), in.Metadata...)
	}
	return &out
}
