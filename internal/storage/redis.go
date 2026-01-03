package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/redis/go-redis/v9"
)

// RedisStorage implements the Storage interface using Redis for metadata and an object store for binary content.
type RedisStorage struct {
	rdb         redis.UniversalClient
	objectStore ObjectStore
	keyPrefix   string
}

type durableState struct {
	Slices            map[string]*models.Slice          `json:"slices"`
	Metadata          map[string]*models.SliceMetadata  `json:"metadata"`
	SliceCommits      map[string][]*models.Commit       `json:"slice_commits"`
	Changesets        map[string]*models.Changeset      `json:"changesets"`
	SliceChangesets   map[string][]string               `json:"slice_changesets"`
	Entries           map[string]*models.DirectoryEntry `json:"entries"`
	EntriesByParent   map[string][]string               `json:"entries_by_parent"`
	EntryPathsBySlice map[string]map[string]string      `json:"entry_paths_by_slice"`
	GlobalState       *models.GlobalState               `json:"global_state"`
}

func newDurableState() *durableState {
	return &durableState{
		Slices:            make(map[string]*models.Slice),
		Metadata:          make(map[string]*models.SliceMetadata),
		SliceCommits:      make(map[string][]*models.Commit),
		Changesets:        make(map[string]*models.Changeset),
		SliceChangesets:   make(map[string][]string),
		Entries:           make(map[string]*models.DirectoryEntry),
		EntriesByParent:   make(map[string][]string),
		EntryPathsBySlice: make(map[string]map[string]string),
	}
}

func ensureCtx(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (s *RedisStorage) durableKey(parts ...string) string {
	return s.key(append([]string{"durable"}, parts...)...)
}

func (s *RedisStorage) loadDurableState(ctx context.Context) (*durableState, error) {
	ctx = ensureCtx(ctx)
	raw, err := s.objectStore.GetObject(ctx, s.durableKey("state"))
	if err != nil {
		if errors.Is(err, ErrEntryNotFound) {
			return newDurableState(), nil
		}
		return nil, err
	}

	var state durableState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}

	if state.Slices == nil {
		return newDurableState(), nil
	}

	if state.Metadata == nil {
		state.Metadata = make(map[string]*models.SliceMetadata)
	}
	if state.SliceCommits == nil {
		state.SliceCommits = make(map[string][]*models.Commit)
	}
	if state.Changesets == nil {
		state.Changesets = make(map[string]*models.Changeset)
	}
	if state.SliceChangesets == nil {
		state.SliceChangesets = make(map[string][]string)
	}
	if state.Entries == nil {
		state.Entries = make(map[string]*models.DirectoryEntry)
	}
	if state.EntriesByParent == nil {
		state.EntriesByParent = make(map[string][]string)
	}
	if state.EntryPathsBySlice == nil {
		state.EntryPathsBySlice = make(map[string]map[string]string)
	}

	return &state, nil
}

func (s *RedisStorage) saveDurableState(ctx context.Context, state *durableState) error {
	ctx = ensureCtx(ctx)
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.objectStore.PutObject(ctx, s.durableKey("state"), raw)
}

func (s *RedisStorage) withDurableState(ctx context.Context, fn func(state *durableState) error) error {
	state, err := s.loadDurableState(ctx)
	if err != nil {
		return err
	}
	if err := fn(state); err != nil {
		return err
	}
	return s.saveDurableState(ctx, state)
}

func (s *RedisStorage) cacheSlice(ctx context.Context, slice *models.Slice, meta *models.SliceMetadata) error {
	raw, err := marshal(slice)
	if err != nil {
		return err
	}

	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, s.key("slice", slice.ID), raw, 0)
	pipe.SAdd(ctx, s.key("slices"), slice.ID)
	for _, fileID := range slice.Files {
		pipe.SAdd(ctx, s.key("file_index", fileID), slice.ID)
	}

	if meta != nil {
		metaRaw, err := marshal(meta)
		if err != nil {
			pipe.Discard()
			return err
		}
		pipe.Set(ctx, s.key("slice_metadata", slice.ID), metaRaw, 0)
	}

	_, err = pipe.Exec(ctx)
	return err
}

func (s *RedisStorage) clearKeys(ctx context.Context, pattern string) error {
	ctx = ensureCtx(ctx)
	var cursor uint64
	for {
		keys, next, err := s.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := s.rdb.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		if next == 0 || next == cursor {
			return nil
		}
		cursor = next
	}
}

// NewRedisStorage creates a Redis-backed storage implementation.
func NewRedisStorage(rdb redis.UniversalClient, objectStore ObjectStore, keyPrefix string) *RedisStorage {
	return &RedisStorage{rdb: rdb, objectStore: objectStore, keyPrefix: keyPrefix}
}

func (s *RedisStorage) key(parts ...string) string {
	if s.keyPrefix == "" {
		return fmt.Sprintf("gitslice:%s", joinKey(parts...))
	}
	return fmt.Sprintf("%s:%s", s.keyPrefix, joinKey(parts...))
}

func joinKey(parts ...string) string {
	return strings.Join(parts, ":")
}

func marshal(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func unmarshal[T any](raw string, target *T) error {
	return json.Unmarshal([]byte(raw), target)
}

// LockSliceAndFiles acquires a lock on a slice and its associated files.
func (s *RedisStorage) LockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) error {
	ctx = ensureCtx(ctx)
	if _, err := s.GetSlice(ctx, sliceID); err != nil {
		return err
	}

	pipe := s.rdb.TxPipeline()
	pipe.SAdd(ctx, s.key("locked_slices"), sliceID)
	fileLockKey := s.key("file_lock")

	for _, fileID := range fileIDs {
		owner, err := s.rdb.HGet(ctx, fileLockKey, fileID).Result()
		if err == nil && owner != sliceID {
			pipe.Discard()
			return ErrLockHeld
		}
		if err != nil && err != redis.Nil {
			pipe.Discard()
			return err
		}
		pipe.HSet(ctx, fileLockKey, fileID, sliceID)
	}

	_, err := pipe.Exec(ctx)
	return err
}

// UnlockSliceAndFiles releases locks for a slice and associated files.
func (s *RedisStorage) UnlockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) {
	ctx = ensureCtx(ctx)
	_ = s.rdb.SRem(ctx, s.key("locked_slices"), sliceID)
	fileLockKey := s.key("file_lock")
	for _, fileID := range fileIDs {
		owner, err := s.rdb.HGet(ctx, fileLockKey, fileID).Result()
		if err == nil && owner == sliceID {
			_ = s.rdb.HDel(ctx, fileLockKey, fileID).Err()
		}
	}
}

// CreateSlice stores a new slice definition and metadata.
func (s *RedisStorage) CreateSlice(ctx context.Context, slice *models.Slice) error {
	ctx = ensureCtx(ctx)
	if slice.ID == "" {
		return ErrInvalidInput
	}

	now := time.Now()
	slice.CreatedAt = now
	slice.UpdatedAt = now

	sliceKey := s.key("slice", slice.ID)
	raw, err := marshal(slice)
	if err != nil {
		return err
	}

	meta := &models.SliceMetadata{
		SliceID:            slice.ID,
		HeadCommitHash:     "",
		ModifiedFiles:      []string{},
		LastModified:       now,
		ModifiedFilesCount: 0,
	}

	if err := s.withDurableState(ctx, func(state *durableState) error {
		if _, exists := state.Slices[slice.ID]; exists {
			return ErrSliceAlreadyExists
		}
		copySlice := *slice
		state.Slices[slice.ID] = &copySlice
		copyMeta := *meta
		state.Metadata[slice.ID] = &copyMeta
		if _, ok := state.SliceChangesets[slice.ID]; !ok {
			state.SliceChangesets[slice.ID] = []string{}
		}
		if _, ok := state.SliceCommits[slice.ID]; !ok {
			state.SliceCommits[slice.ID] = []*models.Commit{}
		}
		return nil
	}); err != nil {
		return err
	}
	metaKey := s.key("slice_metadata", slice.ID)
	metaRaw, err := marshal(meta)
	if err != nil {
		return err
	}

	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, sliceKey, raw, 0)
	pipe.Set(ctx, metaKey, metaRaw, 0)
	pipe.SAdd(ctx, s.key("slices"), slice.ID)
	pipe.Del(ctx, s.key("slice_commits", slice.ID))
	pipe.Del(ctx, s.key("slice_changesets", slice.ID))

	for _, fileID := range slice.Files {
		pipe.SAdd(ctx, s.key("file_index", fileID), slice.ID)
	}

	_, err = pipe.Exec(ctx)
	return err
}

// GetSlice retrieves a slice by ID.
func (s *RedisStorage) GetSlice(ctx context.Context, sliceID string) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	val, err := s.rdb.Get(ctx, s.key("slice", sliceID)).Result()
	if err != nil {
		if err == redis.Nil {
			state, loadErr := s.loadDurableState(ctx)
			if loadErr == nil {
				if saved, ok := state.Slices[sliceID]; ok {
					_ = s.cacheSlice(ctx, saved, state.Metadata[sliceID])
					copy := *saved
					return &copy, nil
				}
			}
			return nil, ErrSliceNotFound
		}
		return nil, err
	}

	var slice models.Slice
	if err := unmarshal(val, &slice); err != nil {
		return nil, err
	}

	return &slice, nil
}

// ListSlices returns slices with pagination.
func (s *RedisStorage) ListSlices(ctx context.Context, limit, offset int) ([]*models.Slice, error) {
	ctx = ensureCtx(ctx)
	ids, err := s.rdb.SMembers(ctx, s.key("slices")).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		state, loadErr := s.loadDurableState(ctx)
		if loadErr == nil {
			for id := range state.Slices {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	if offset >= len(ids) {
		return []*models.Slice{}, nil
	}

	end := offset + limit
	if end > len(ids) {
		end = len(ids)
	}

	var result []*models.Slice
	for _, id := range ids[offset:end] {
		slice, err := s.GetSlice(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, slice)
	}

	return result, nil
}

// ListSlicesByOwner returns slices owned by the provided owner.
func (s *RedisStorage) ListSlicesByOwner(ctx context.Context, owner string, limit, offset int) ([]*models.Slice, error) {
	ctx = ensureCtx(ctx)
	ids, err := s.rdb.SMembers(ctx, s.key("slices")).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		state, loadErr := s.loadDurableState(ctx)
		if loadErr == nil {
			for id := range state.Slices {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)

	var owned []*models.Slice
	for _, id := range ids {
		slice, err := s.GetSlice(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, o := range slice.Owners {
			if o == owner {
				owned = append(owned, slice)
				break
			}
		}
	}

	if offset >= len(owned) {
		return []*models.Slice{}, nil
	}
	end := offset + limit
	if end > len(owned) {
		end = len(owned)
	}
	return owned[offset:end], nil
}

// SearchSlices performs a case-sensitive substring search over name and description.
func (s *RedisStorage) SearchSlices(ctx context.Context, query string, limit, offset int) ([]*models.Slice, error) {
	ctx = ensureCtx(ctx)
	ids, err := s.rdb.SMembers(ctx, s.key("slices")).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		state, loadErr := s.loadDurableState(ctx)
		if loadErr == nil {
			for id := range state.Slices {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)

	var matches []*models.Slice
	for _, id := range ids {
		slice, err := s.GetSlice(ctx, id)
		if err != nil {
			return nil, err
		}
		if contains(slice.Name, query) || contains(slice.Description, query) {
			matches = append(matches, slice)
		}
	}

	if offset >= len(matches) {
		return []*models.Slice{}, nil
	}

	end := offset + limit
	if end > len(matches) {
		end = len(matches)
	}

	return matches[offset:end], nil
}

// GetSliceMetadata fetches metadata for a slice.
func (s *RedisStorage) GetSliceMetadata(ctx context.Context, sliceID string) (*models.SliceMetadata, error) {
	ctx = ensureCtx(ctx)
	raw, err := s.rdb.Get(ctx, s.key("slice_metadata", sliceID)).Result()
	if err != nil {
		if err == redis.Nil {
			state, loadErr := s.loadDurableState(ctx)
			if loadErr == nil {
				if meta, ok := state.Metadata[sliceID]; ok {
					_ = s.cacheSlice(ctx, state.Slices[sliceID], meta)
					copyMeta := *meta
					return &copyMeta, nil
				}
			}
			return nil, ErrSliceNotFound
		}
		return nil, err
	}

	var meta models.SliceMetadata
	if err := unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// UpdateSliceMetadata replaces the stored metadata snapshot.
func (s *RedisStorage) UpdateSliceMetadata(ctx context.Context, sliceID string, metadata *models.SliceMetadata) error {
	ctx = ensureCtx(ctx)
	if _, err := s.GetSlice(ctx, sliceID); err != nil {
		return err
	}

	if metadata.LastModified.IsZero() {
		metadata.LastModified = time.Now()
	}
	raw, err := marshal(metadata)
	if err != nil {
		return err
	}

	if err := s.withDurableState(ctx, func(state *durableState) error {
		copyMeta := *metadata
		state.Metadata[sliceID] = &copyMeta
		return nil
	}); err != nil {
		return err
	}

	return s.rdb.Set(ctx, s.key("slice_metadata", sliceID), raw, 0).Err()
}

// AddSliceCommit appends a commit to the slice history (newest first).
func (s *RedisStorage) AddSliceCommit(ctx context.Context, sliceID string, commit *models.Commit) error {
	ctx = ensureCtx(ctx)
	if _, err := s.GetSlice(ctx, sliceID); err != nil {
		return err
	}

	if err := s.withDurableState(ctx, func(state *durableState) error {
		copyCommit := *commit
		state.SliceCommits[sliceID] = append([]*models.Commit{&copyCommit}, state.SliceCommits[sliceID]...)
		return nil
	}); err != nil {
		return err
	}

	raw, err := marshal(commit)
	if err != nil {
		return err
	}
	return s.rdb.LPush(ctx, s.key("slice_commits", sliceID), raw).Err()
}

// ListSliceCommits lists commits for a slice applying pagination.
func (s *RedisStorage) ListSliceCommits(ctx context.Context, sliceID string, limit int, fromCommitHash string) ([]*models.Commit, error) {
	ctx = ensureCtx(ctx)
	if _, err := s.GetSlice(ctx, sliceID); err != nil {
		return nil, err
	}

	raws, err := s.rdb.LRange(ctx, s.key("slice_commits", sliceID), 0, -1).Result()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	if len(raws) == 0 {
		state, loadErr := s.loadDurableState(ctx)
		if loadErr == nil {
			if stored, ok := state.SliceCommits[sliceID]; ok {
				copyCommits := make([]*models.Commit, len(stored))
				for i, c := range stored {
					dup := *c
					copyCommits[i] = &dup
				}
				return copyCommits, nil
			}
		}
	}

	start := 0
	if fromCommitHash != "" {
		for i, raw := range raws {
			var c models.Commit
			if err := unmarshal(raw, &c); err != nil {
				return nil, err
			}
			if c.CommitHash == fromCommitHash {
				start = i + 1
				break
			}
		}
	}

	if start >= len(raws) {
		return []*models.Commit{}, nil
	}

	if limit <= 0 || limit > len(raws)-start {
		limit = len(raws) - start
	}

	var commits []*models.Commit
	for _, raw := range raws[start : start+limit] {
		var c models.Commit
		if err := unmarshal(raw, &c); err != nil {
			return nil, err
		}
		commits = append(commits, &c)
	}
	return commits, nil
}

// AddFileToSlice indexes a file for a slice.
func (s *RedisStorage) AddFileToSlice(ctx context.Context, fileID, sliceID string) error {
	ctx = ensureCtx(ctx)
	slice, err := s.GetSlice(ctx, sliceID)
	if err != nil {
		return err
	}

	exists := false
	for _, f := range slice.Files {
		if f == fileID {
			exists = true
			break
		}
	}
	if !exists {
		slice.Files = append(slice.Files, fileID)
	}

	if err := s.withDurableState(ctx, func(state *durableState) error {
		stored := slice
		if stateSlice, ok := state.Slices[sliceID]; ok {
			copySlice := *stateSlice
			exists := false
			for _, f := range copySlice.Files {
				if f == fileID {
					exists = true
					break
				}
			}
			if !exists {
				copySlice.Files = append(copySlice.Files, fileID)
			}
			stored = &copySlice
		}
		state.Slices[sliceID] = stored
		return nil
	}); err != nil {
		return err
	}

	if err := s.cacheSlice(ctx, slice, nil); err != nil {
		return err
	}

	return s.rdb.SAdd(ctx, s.key("file_index", fileID), sliceID).Err()
}

// GetActiveSlicesForFile returns slices currently referencing a file.
func (s *RedisStorage) GetActiveSlicesForFile(ctx context.Context, fileID string) ([]string, error) {
	ctx = ensureCtx(ctx)
	ids, err := s.rdb.SMembers(ctx, s.key("file_index", fileID)).Result()
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	return ids, nil
}

// RemoveFileFromSlice removes a file mapping for a slice.
func (s *RedisStorage) RemoveFileFromSlice(ctx context.Context, fileID, sliceID string) error {
	ctx = ensureCtx(ctx)
	slice, err := s.GetSlice(ctx, sliceID)
	if err != nil {
		return err
	}

	filtered := slice.Files[:0]
	for _, f := range slice.Files {
		if f != fileID {
			filtered = append(filtered, f)
		}
	}
	slice.Files = filtered

	if err := s.withDurableState(ctx, func(state *durableState) error {
		if existing, ok := state.Slices[sliceID]; ok {
			copySlice := *existing
			copySlice.Files = filtered
			state.Slices[sliceID] = &copySlice
		}
		return nil
	}); err != nil {
		return err
	}

	if err := s.cacheSlice(ctx, slice, nil); err != nil {
		return err
	}

	return s.rdb.SRem(ctx, s.key("file_index", fileID), sliceID).Err()
}

// ListConflicts returns files mapped to multiple slices.
func (s *RedisStorage) ListConflicts(ctx context.Context) ([]*models.FileConflict, error) {
	ctx = ensureCtx(ctx)
	keys, err := s.rdb.Keys(ctx, s.key("file_index", "*")).Result()
	if err != nil {
		return nil, err
	}

	var conflicts []*models.FileConflict
	for _, key := range keys {
		ids, err := s.rdb.SMembers(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		conflict := &models.FileConflict{FileID: lastKeySegment(key), ConflictingSlices: ids}
		conflicts = append(conflicts, conflict)
	}

	return conflicts, nil
}

// ResolveConflict keeps a preferred mapping and removes others.
func (s *RedisStorage) ResolveConflict(ctx context.Context, fileID, preferredSliceID string) (*models.FileConflict, error) {
	ctx = ensureCtx(ctx)
	key := s.key("file_index", fileID)
	ids, err := s.rdb.SMembers(ctx, key).Result()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	if len(ids) == 0 {
		return &models.FileConflict{FileID: fileID, ConflictingSlices: []string{}}, nil
	}

	allowed := make(map[string]struct{})
	for _, id := range ids {
		if preferredSliceID != "" && id == preferredSliceID {
			allowed[id] = struct{}{}
			break
		}
	}
	if len(allowed) == 0 {
		allowed[ids[0]] = struct{}{}
	}

	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, key)
	var remaining []string
	for id := range allowed {
		pipe.SAdd(ctx, key, id)
		remaining = append(remaining, id)
	}
	sort.Strings(remaining)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}

	return &models.FileConflict{FileID: fileID, ConflictingSlices: remaining}, nil
}

// RebuildIndexes reconstructs Redis indexes and cached records from the durable object store.
// This is useful when Redis has lost volatile keys after a restart while object storage still
// holds the authoritative metadata and manifests.
func (s *RedisStorage) RebuildIndexes(ctx context.Context) error {
	ctx = ensureCtx(ctx)

	state, err := s.loadDurableState(ctx)
	if err != nil {
		return err
	}

	patterns := []string{
		s.key("slice", "*"),
		s.key("slice_metadata", "*"),
		s.key("file_index", "*"),
		s.key("slice_commits", "*"),
		s.key("slice_changesets", "*"),
		s.key("changeset", "*"),
		s.key("entry", "*"),
		s.key("entry_path", "*"),
		s.key("entries_by_parent", "*"),
	}

	for _, pattern := range patterns {
		if err := s.clearKeys(ctx, pattern); err != nil {
			return err
		}
	}

	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, s.key("slices"))
	if len(state.Slices) > 0 {
		members := make([]any, 0, len(state.Slices))
		for id := range state.Slices {
			members = append(members, id)
		}
		pipe.SAdd(ctx, s.key("slices"), members...)
	}

	for id, slice := range state.Slices {
		meta := state.Metadata[id]
		if err := s.cacheSlice(ctx, slice, meta); err != nil {
			pipe.Discard()
			return err
		}
	}

	for sliceID, commits := range state.SliceCommits {
		if len(commits) == 0 {
			continue
		}
		rawCommits := make([]any, 0, len(commits))
		for _, commit := range commits {
			data, err := marshal(commit)
			if err != nil {
				pipe.Discard()
				return err
			}
			rawCommits = append(rawCommits, data)
		}
		pipe.Del(ctx, s.key("slice_commits", sliceID))
		pipe.RPush(ctx, s.key("slice_commits", sliceID), rawCommits...)
	}

	for id, cs := range state.Changesets {
		raw, err := marshal(cs)
		if err != nil {
			pipe.Discard()
			return err
		}
		pipe.Set(ctx, s.key("changeset", id), raw, 0)
	}
	for sliceID, ids := range state.SliceChangesets {
		if len(ids) == 0 {
			continue
		}
		members := make([]any, 0, len(ids))
		for _, id := range ids {
			members = append(members, id)
		}
		pipe.Del(ctx, s.key("slice_changesets", sliceID))
		pipe.RPush(ctx, s.key("slice_changesets", sliceID), members...)
	}

	for _, entry := range state.Entries {
		raw, err := marshal(entry)
		if err != nil {
			pipe.Discard()
			return err
		}
		pipe.Set(ctx, s.key("entry", entry.ID), raw, 0)
		pipe.Set(ctx, s.key("entry_path", entry.ParentID, entry.Path), entry.ID, 0)
	}
	for parentID, ids := range state.EntriesByParent {
		members := make([]any, 0, len(ids))
		for _, id := range ids {
			members = append(members, id)
		}
		pipe.Del(ctx, s.key("entries_by_parent", parentID))
		if len(members) > 0 {
			pipe.SAdd(ctx, s.key("entries_by_parent", parentID), members...)
		}
	}

	if state.GlobalState != nil {
		raw, err := marshal(state.GlobalState)
		if err != nil {
			pipe.Discard()
			return err
		}
		pipe.Set(ctx, s.key("global_state"), raw, 0)
	}

	_, err = pipe.Exec(ctx)
	return err
}

// CreateChangeset stores a new changeset.
func (s *RedisStorage) CreateChangeset(ctx context.Context, changeset *models.Changeset) error {
	ctx = ensureCtx(ctx)
	if _, err := s.GetSlice(ctx, changeset.SliceID); err != nil {
		return err
	}

	if err := s.withDurableState(ctx, func(state *durableState) error {
		copyCS := *changeset
		state.Changesets[changeset.ID] = &copyCS
		state.SliceChangesets[changeset.SliceID] = append([]string{changeset.ID}, state.SliceChangesets[changeset.SliceID]...)
		return nil
	}); err != nil {
		return err
	}

	raw, err := marshal(changeset)
	if err != nil {
		return err
	}

	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, s.key("changeset", changeset.ID), raw, 0)
	pipe.LPush(ctx, s.key("slice_changesets", changeset.SliceID), changeset.ID)
	_, err = pipe.Exec(ctx)
	return err
}

// GetChangeset returns a stored changeset by ID.
func (s *RedisStorage) GetChangeset(ctx context.Context, changesetID string) (*models.Changeset, error) {
	ctx = ensureCtx(ctx)
	raw, err := s.rdb.Get(ctx, s.key("changeset", changesetID)).Result()
	if err != nil {
		if err == redis.Nil {
			state, loadErr := s.loadDurableState(ctx)
			if loadErr == nil {
				if cs, ok := state.Changesets[changesetID]; ok {
					copyCS := *cs
					return &copyCS, nil
				}
			}
			return nil, ErrChangesetNotFound
		}
		return nil, err
	}
	var cs models.Changeset
	if err := unmarshal(raw, &cs); err != nil {
		return nil, err
	}
	return &cs, nil
}

// ListChangesets lists changesets for a slice, optionally filtered by status and limited.
func (s *RedisStorage) ListChangesets(ctx context.Context, sliceID string, status *models.ChangesetStatus, limit int) ([]*models.Changeset, error) {
	ctx = ensureCtx(ctx)
	ids, err := s.rdb.LRange(ctx, s.key("slice_changesets", sliceID), 0, -1).Result()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	if len(ids) == 0 {
		state, loadErr := s.loadDurableState(ctx)
		if loadErr == nil {
			ids = append(ids, state.SliceChangesets[sliceID]...)
		}
	}
	var result []*models.Changeset
	for _, id := range ids {
		cs, err := s.GetChangeset(ctx, id)
		if err != nil {
			continue
		}
		if status != nil && cs.Status != *status {
			continue
		}
		result = append(result, cs)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

// UpdateChangeset replaces a stored changeset.
func (s *RedisStorage) UpdateChangeset(ctx context.Context, changeset *models.Changeset) error {
	ctx = ensureCtx(ctx)
	if _, err := s.GetChangeset(ctx, changeset.ID); err != nil {
		return err
	}
	raw, err := marshal(changeset)
	if err != nil {
		return err
	}
	if err := s.withDurableState(ctx, func(state *durableState) error {
		copyCS := *changeset
		state.Changesets[changeset.ID] = &copyCS
		return nil
	}); err != nil {
		return err
	}
	return s.rdb.Set(ctx, s.key("changeset", changeset.ID), raw, 0).Err()
}

// GetSliceFiles reads file content entries for a slice.
func (s *RedisStorage) GetSliceFiles(ctx context.Context, sliceID string) ([]*models.FileContent, error) {
	ctx = ensureCtx(ctx)
	slice, err := s.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, err
	}

	var files []*models.FileContent
	for _, fileID := range slice.Files {
		raw, err := s.objectStore.GetObject(ctx, s.key("file_content", fileID))
		if err != nil {
			continue
		}
		var content models.FileContent
		if err := json.Unmarshal(raw, &content); err != nil {
			return nil, err
		}
		files = append(files, &content)
	}
	return files, nil
}

// AddFileContent writes file content to the object store for a slice.
func (s *RedisStorage) AddFileContent(ctx context.Context, content *models.FileContent) error {
	ctx = ensureCtx(ctx)
	raw, err := json.Marshal(content)
	if err != nil {
		return err
	}
	return s.objectStore.PutObject(ctx, s.key("file_content", content.FileID), raw)
}

// GetRootSlice returns the root slice if present.
func (s *RedisStorage) GetRootSlice(ctx context.Context) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	ids, err := s.rdb.SMembers(ctx, s.key("slices")).Result()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		slice, err := s.GetSlice(ctx, id)
		if err != nil {
			return nil, err
		}
		if slice.IsRoot {
			return slice, nil
		}
	}
	return nil, ErrSliceNotFound
}

// InitializeRootSlice creates a root slice if one is absent.
func (s *RedisStorage) InitializeRootSlice(ctx context.Context) error {
	ctx = ensureCtx(ctx)
	if _, err := s.GetRootSlice(ctx); err == nil {
		return nil
	}

	rootSlice := &models.Slice{
		ID:          "root_slice",
		Name:        "Root Slice",
		Description: "The root slice containing all files",
		Files:       []string{},
		Owners:      []string{"system"},
		CreatedBy:   "system",
		IsRoot:      true,
	}

	return s.CreateSlice(ctx, rootSlice)
}

// GetSliceFileByPath retrieves file content for a path within a slice.
func (s *RedisStorage) GetSliceFileByPath(ctx context.Context, sliceID, path string) (*models.FileContent, error) {
	ctx = ensureCtx(ctx)
	entryID, err := s.rdb.Get(ctx, s.key("entry_path", sliceID, path)).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	entry, err := s.GetEntry(ctx, entryID)
	if err != nil {
		return nil, err
	}
	return &models.FileContent{
		FileID:  entry.ID,
		Path:    entry.Path,
		Content: entry.Content,
		Size:    entry.Size,
	}, nil
}

// AddEntry stores a directory entry.
func (s *RedisStorage) AddEntry(ctx context.Context, entry *models.DirectoryEntry) error {
	ctx = ensureCtx(ctx)
	if entry.ID == "" {
		return ErrInvalidInput
	}
	if _, err := s.rdb.Get(ctx, s.key("entry", entry.ID)).Result(); err == nil {
		return ErrEntryExists
	} else if err != redis.Nil {
		return err
	}

	if err := s.withDurableState(ctx, func(state *durableState) error {
		copyEntry := *entry
		state.Entries[entry.ID] = &copyEntry
		state.EntriesByParent[entry.ParentID] = append(state.EntriesByParent[entry.ParentID], entry.ID)
		if _, ok := state.EntryPathsBySlice[entry.ParentID]; !ok {
			state.EntryPathsBySlice[entry.ParentID] = make(map[string]string)
		}
		state.EntryPathsBySlice[entry.ParentID][entry.Path] = entry.ID
		return nil
	}); err != nil {
		return err
	}

	raw, err := marshal(entry)
	if err != nil {
		return err
	}

	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, s.key("entry", entry.ID), raw, 0)
	pipe.Set(ctx, s.key("entry_path", entry.ParentID, entry.Path), entry.ID, 0)
	pipe.SAdd(ctx, s.key("entries_by_parent", entry.ParentID), entry.ID)
	_, err = pipe.Exec(ctx)
	return err
}

// GetEntry fetches a directory entry by ID.
func (s *RedisStorage) GetEntry(ctx context.Context, entryID string) (*models.DirectoryEntry, error) {
	ctx = ensureCtx(ctx)
	raw, err := s.rdb.Get(ctx, s.key("entry", entryID)).Result()
	if err != nil {
		if err == redis.Nil {
			state, loadErr := s.loadDurableState(ctx)
			if loadErr == nil {
				if entry, ok := state.Entries[entryID]; ok {
					copyEntry := *entry
					return &copyEntry, nil
				}
			}
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	var entry models.DirectoryEntry
	if err := unmarshal(raw, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// GetEntryByPath fetches a directory entry by slice and path.
func (s *RedisStorage) GetEntryByPath(ctx context.Context, sliceID, path string) (*models.DirectoryEntry, error) {
	ctx = ensureCtx(ctx)
	entryID, err := s.rdb.Get(ctx, s.key("entry_path", sliceID, path)).Result()
	if err != nil {
		if err == redis.Nil {
			state, loadErr := s.loadDurableState(ctx)
			if loadErr == nil {
				if paths, ok := state.EntryPathsBySlice[sliceID]; ok {
					if id, ok := paths[path]; ok {
						return s.GetEntry(ctx, id)
					}
				}
			}
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return s.GetEntry(ctx, entryID)
}

// ListEntries lists entries by parent ID.
func (s *RedisStorage) ListEntries(ctx context.Context, sliceID, parentID string) ([]*models.DirectoryEntry, error) {
	ctx = ensureCtx(ctx)
	ids, err := s.rdb.SMembers(ctx, s.key("entries_by_parent", parentID)).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		state, loadErr := s.loadDurableState(ctx)
		if loadErr == nil {
			ids = append(ids, state.EntriesByParent[parentID]...)
		}
	}
	sort.Strings(ids)
	var entries []*models.DirectoryEntry
	for _, id := range ids {
		entry, err := s.GetEntry(ctx, id)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// UpdateEntry replaces a stored entry.
func (s *RedisStorage) UpdateEntry(ctx context.Context, entry *models.DirectoryEntry) error {
	ctx = ensureCtx(ctx)
	if _, err := s.GetEntry(ctx, entry.ID); err != nil {
		return err
	}
	raw, err := marshal(entry)
	if err != nil {
		return err
	}
	if err := s.withDurableState(ctx, func(state *durableState) error {
		copyEntry := *entry
		state.Entries[entry.ID] = &copyEntry
		return nil
	}); err != nil {
		return err
	}
	return s.rdb.Set(ctx, s.key("entry", entry.ID), raw, 0).Err()
}

// DeleteEntry removes an entry and related indexes.
func (s *RedisStorage) DeleteEntry(ctx context.Context, entryID string) error {
	ctx = ensureCtx(ctx)
	entry, err := s.GetEntry(ctx, entryID)
	if err != nil {
		return err
	}

	if err := s.withDurableState(ctx, func(state *durableState) error {
		delete(state.Entries, entryID)
		if ids, ok := state.EntriesByParent[entry.ParentID]; ok {
			filtered := ids[:0]
			for _, id := range ids {
				if id != entryID {
					filtered = append(filtered, id)
				}
			}
			state.EntriesByParent[entry.ParentID] = filtered
		}
		if paths, ok := state.EntryPathsBySlice[entry.ParentID]; ok {
			delete(paths, entry.Path)
		}
		return nil
	}); err != nil {
		return err
	}
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, s.key("entry", entryID))
	pipe.Del(ctx, s.key("entry_path", entry.ParentID, entry.Path))
	pipe.SRem(ctx, s.key("entries_by_parent", entry.ParentID), entryID)
	_, err = pipe.Exec(ctx)
	return err
}

// GetGlobalState retrieves the current global state snapshot.
func (s *RedisStorage) GetGlobalState(ctx context.Context) (*models.GlobalState, error) {
	ctx = ensureCtx(ctx)
	raw, err := s.rdb.Get(ctx, s.key("global_state")).Result()
	if err != nil {
		if err == redis.Nil {
			durable, loadErr := s.loadDurableState(ctx)
			if loadErr == nil && durable.GlobalState != nil {
				copyState := *durable.GlobalState
				return &copyState, nil
			}
			return nil, ErrInvalidInput
		}
		return nil, err
	}
	var state models.GlobalState
	if err := unmarshal(raw, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// UpdateGlobalState replaces the stored global state snapshot.
func (s *RedisStorage) UpdateGlobalState(ctx context.Context, state *models.GlobalState) error {
	ctx = ensureCtx(ctx)

	key := s.key("global_state")
	attempts := 0
	var mergedResult *models.GlobalState
	for {
		attempts++
		err := s.rdb.Watch(ctx, func(tx *redis.Tx) error {
			var current *models.GlobalState
			raw, err := tx.Get(ctx, key).Result()
			if err != nil && err != redis.Nil {
				return err
			}
			if err == nil {
				var existing models.GlobalState
				if err := unmarshal(raw, &existing); err != nil {
					return err
				}
				current = &existing
			}

			merged := mergeGlobalStates(state, current)
			rawMerged, err := marshal(merged)
			if err != nil {
				return err
			}

			mergedResult = merged
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				return pipe.Set(ctx, key, rawMerged, 0).Err()
			})
			return err
		}, key)

		if err == nil {
			break
		}
		if err == redis.TxFailedErr && attempts < 5 {
			continue
		}
		return err
	}

	durableSnapshot := state
	if mergedResult != nil {
		durableSnapshot = mergedResult
	}

	return s.withDurableState(ctx, func(durable *durableState) error {
		copyState := *durableSnapshot
		durable.GlobalState = &copyState
		return nil
	})
}

func mergeGlobalStates(incoming, current *models.GlobalState) *models.GlobalState {
	merged := &models.GlobalState{
		GlobalCommitHash: incoming.GlobalCommitHash,
		Timestamp:        incoming.Timestamp,
		History:          make([]*models.GlobalCommit, 0, len(incoming.History)),
	}

	seen := make(map[string]struct{})
	for _, entry := range incoming.History {
		if entry == nil {
			continue
		}
		copyEntry := *entry
		merged.History = append(merged.History, &copyEntry)
		seen[entry.CommitHash] = struct{}{}
	}

	if current != nil {
		for _, entry := range current.History {
			if entry == nil {
				continue
			}
			if _, ok := seen[entry.CommitHash]; ok {
				continue
			}
			copyEntry := *entry
			merged.History = append(merged.History, &copyEntry)
		}

		if merged.GlobalCommitHash == "" {
			merged.GlobalCommitHash = current.GlobalCommitHash
		}
		if merged.Timestamp.IsZero() {
			merged.Timestamp = current.Timestamp
		}
	}

	return merged
}

// Ping validates the Redis connection and object store accessibility.
func (s *RedisStorage) Ping(ctx context.Context) error {
	ctx = ensureCtx(ctx)
	if err := s.rdb.Ping(ctx).Err(); err != nil {
		return err
	}
	// Verify object store is reachable via a small round trip.
	const probeKey = "healthcheck"
	if err := s.objectStore.PutObject(ctx, s.key(probeKey), []byte("ok")); err != nil {
		return err
	}
	_, err := s.objectStore.GetObject(ctx, s.key(probeKey))
	_ = s.objectStore.DeleteObject(ctx, s.key(probeKey))
	return err
}

func lastKeySegment(key string) string {
	parts := strings.Split(key, ":")
	return parts[len(parts)-1]
}
