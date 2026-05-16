package fileservice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/niczy/gitslice/internal/authresolver"
	"github.com/niczy/gitslice/internal/authz"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	"github.com/niczy/gitslice/internal/visibility"
	filev1 "github.com/niczy/gitslice/proto/file"
	"github.com/pmezard/go-difflib/difflib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fileServiceServer struct {
	filev1.UnimplementedFileServiceServer
	storage storage.Storage

	// Cache of computed displayPath->storedPath maps and their sorted keys. This avoids
	// rebuilding and sorting the full slice path set on every ListEntries/GetFile call,
	// which is very expensive for imported repos.
	pathCache *slicePathCache
}

func newFileServiceServer(st storage.Storage) *fileServiceServer {
	return &fileServiceServer{
		storage:   st,
		pathCache: newSlicePathCache(64),
	}
}

// RegisterGRPCServer registers the file service handlers on an existing gRPC server.
func RegisterGRPCServer(srv *grpc.Server, st storage.Storage) {
	filev1.RegisterFileServiceServer(srv, newFileServiceServer(st))
}

type slicePathCache struct {
	mu       sync.RWMutex
	items    map[string]*cachedPaths
	order    []string
	maxItems int
	ttl      time.Duration
}

type cachedPaths struct {
	pathMap      map[string]string // displayPath -> storedPath
	displayPaths []string          // sorted keys of pathMap
	cachedAt     time.Time
}

func newSlicePathCache(maxItems int) *slicePathCache {
	if maxItems <= 0 {
		maxItems = 64
	}
	return &slicePathCache{
		items:    make(map[string]*cachedPaths, maxItems),
		order:    make([]string, 0, maxItems),
		maxItems: maxItems,
		ttl:      30 * time.Second,
	}
}

func (c *slicePathCache) get(key string) (*cachedPaths, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	v, ok := c.items[key]
	ttl := c.ttl
	c.mu.RUnlock()
	if !ok || v == nil {
		return nil, false
	}
	if ttl > 0 && !v.cachedAt.IsZero() && time.Since(v.cachedAt) > ttl {
		c.mu.Lock()
		// Re-check under write lock to avoid deleting a refreshed entry.
		if current, exists := c.items[key]; exists && current == v {
			delete(c.items, key)
		}
		c.mu.Unlock()
		return nil, false
	}
	return v, true
}

func (c *slicePathCache) put(key string, v *cachedPaths) {
	if c == nil || key == "" || v == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[key]; exists {
		v.cachedAt = time.Now()
		c.items[key] = v
		return
	}
	v.cachedAt = time.Now()
	c.items[key] = v
	c.order = append(c.order, key)
	if len(c.order) <= c.maxItems {
		return
	}
	evict := c.order[0]
	c.order = c.order[1:]
	delete(c.items, evict)
}

// NewService constructs the file service implementation for use without gRPC.
func NewService(st storage.Storage) filev1.FileServiceServer {
	return newFileServiceServer(st)
}

func (s *fileServiceServer) requireUsername(ctx context.Context) (string, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.storage)
	if err != nil {
		return "", err
	}
	return identity.Username, nil
}

func (s *fileServiceServer) optionalUsername(ctx context.Context) (string, error) {
	identity, err := authresolver.OptionalGRPCIdentity(ctx, s.storage)
	if err != nil {
		return "", err
	}
	if identity == nil {
		return "", nil
	}
	return identity.Username, nil
}

func (s *fileServiceServer) hasSliceViewAccess(ctx context.Context, slice *models.Slice, username string) bool {
	ok, err := authz.CanViewSlice(ctx, s.storage, slice, username)
	if err != nil {
		log.Printf("file: failed to resolve slice access for %s: %v", strings.TrimSpace(username), err)
		return false
	}
	return ok
}

func (s *fileServiceServer) authorizeSliceRead(ctx context.Context, slice *models.Slice, username, requestedPath string) (publicRead bool, err error) {
	if s.hasSliceViewAccess(ctx, slice, username) {
		return false, nil
	}
	if slice != nil && !slice.IsRoot {
		public, visibilityErr := visibility.IsPublic(slice, requestedPath)
		if visibilityErr != nil {
			return false, status.Error(codes.Internal, fmt.Sprintf("failed to resolve visibility: %v", visibilityErr))
		}
		if public {
			return true, nil
		}
	}
	if strings.TrimSpace(username) == "" {
		return false, status.Error(codes.Unauthenticated, "login required")
	}
	return false, status.Error(codes.PermissionDenied, "not authorized for slice")
}

func (s *fileServiceServer) resolvePublicSlice(ctx context.Context, sliceID, sliceSlug string) (*models.Slice, error) {
	if trimmed := strings.TrimSpace(sliceSlug); trimmed != "" {
		slice, err := s.storage.GetSliceBySlug(ctx, trimmed)
		if err == nil {
			return slice, nil
		}
		if !errors.Is(err, storage.ErrSliceNotFound) {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice by slug: %v", err))
		}
	}

	if trimmed := strings.TrimSpace(sliceID); trimmed != "" {
		slice, err := s.storage.GetSlice(ctx, trimmed)
		if err == nil {
			return slice, nil
		}
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil, status.Error(codes.NotFound, "slice not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice: %v", err))
	}

	return nil, status.Error(codes.InvalidArgument, "slice_id or slice_slug is required")
}

func (s *fileServiceServer) ensurePublicPathVisible(ctx context.Context, slice *models.Slice, requestedPath string) error {
	if slice == nil || slice.IsRoot {
		return status.Error(codes.NotFound, "path not found")
	}
	public, err := visibility.IsPublic(slice, requestedPath)
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to resolve visibility: %v", err))
	}
	if !public {
		return status.Error(codes.NotFound, "path not found")
	}
	return nil
}

func (s *fileServiceServer) ensurePublicDirectoryVisible(ctx context.Context, slice *models.Slice, requestedPath string) error {
	if slice == nil || slice.IsRoot {
		return status.Error(codes.NotFound, "path not found")
	}
	public, err := visibility.IsPublic(slice, requestedPath)
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to resolve visibility: %v", err))
	}
	if public {
		return nil
	}
	return status.Error(codes.NotFound, "path not found")
}

func (s *fileServiceServer) filterPublicEntries(ctx context.Context, slice *models.Slice, entries []*filev1.DirectoryEntry) ([]*filev1.DirectoryEntry, error) {
	filtered := make([]*filev1.DirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		public, err := s.publicEntryVisible(ctx, slice, entry)
		if err != nil {
			return nil, err
		}
		if public {
			filtered = append(filtered, entry)
		}
	}
	return filtered, nil
}

func (s *fileServiceServer) publicEntryVisible(ctx context.Context, slice *models.Slice, entry *filev1.DirectoryEntry) (bool, error) {
	if entry == nil {
		return false, nil
	}
	public, err := visibility.IsPublic(slice, entry.GetPath())
	if err != nil {
		return false, err
	}
	return public, nil
}

func sliceBackingSliceID(sliceID string, slice *models.Slice) string {
	if slice != nil && strings.TrimSpace(slice.ParentSlice) != "" && len(slice.FolderMounts) > 0 {
		return strings.TrimSpace(slice.ParentSlice)
	}
	return strings.TrimSpace(sliceID)
}

func (s *fileServiceServer) resolveBackingSliceID(ctx context.Context, sliceID string, slice *models.Slice, preferSnapshots bool) (string, error) {
	backingSliceID := sliceBackingSliceID(sliceID, slice)
	if preferSnapshots || slice == nil || strings.TrimSpace(slice.ParentSlice) == "" || len(slice.FolderMounts) == 0 {
		return backingSliceID, nil
	}
	liveBackingID, ok, err := homeslice.ResolveLiveBackingSliceID(ctx, s.storage, slice)
	if err != nil {
		return "", err
	}
	if ok {
		return liveBackingID, nil
	}
	return backingSliceID, nil
}

func (s *fileServiceServer) rootHomeBackingForPath(ctx context.Context, slice *models.Slice, requestPath string, preferSnapshots bool) (string, bool, error) {
	if preferSnapshots || slice == nil || !slice.IsRoot {
		return "", false, nil
	}
	cleaned := common.CleanRelativePath(requestPath)
	if cleaned == "" {
		return "", false, nil
	}
	username, _, _ := strings.Cut(cleaned, "/")
	username = strings.TrimSpace(username)
	if username == "" {
		return "", false, nil
	}
	homeSliceID := homeslice.IDForUsername(username)
	homeSlice, err := s.storage.GetSlice(ctx, homeSliceID)
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if homeSlice == nil {
		return "", false, nil
	}
	return homeSlice.ID, true, nil
}

func (s *fileServiceServer) sliceDisplayPathExists(ctx context.Context, sliceID string, slice *models.Slice, displayPath string) (bool, bool, error) {
	normalizedDisplayPath := cleanPath(displayPath)
	if normalizedDisplayPath == "" {
		return true, true, nil
	}

	pathMap, displayPaths, err := s.cachedSlicePathMap(ctx, sliceID, slice, "", false)
	if err != nil {
		return false, false, err
	}
	if _, ok := pathMap[normalizedDisplayPath]; ok {
		return true, false, nil
	}
	prefix := normalizedDisplayPath + "/"
	idx := sort.SearchStrings(displayPaths, prefix)
	if idx < len(displayPaths) && strings.HasPrefix(displayPaths[idx], prefix) {
		return true, true, nil
	}

	storedPath := common.SliceStoredPath(slice, normalizedDisplayPath)
	if storedPath == "" {
		return false, false, nil
	}
	backingSliceID, err := s.resolveBackingSliceID(ctx, sliceID, slice, false)
	if err != nil {
		return false, false, err
	}
	entry, err := s.storage.GetEntryByPath(ctx, backingSliceID, storedPath)
	if err == nil && entry != nil {
		return true, entry.Type == "directory", nil
	}
	if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
		return false, false, err
	}
	return false, false, nil
}

// resolveVersion extracts the effective slice and commit from oneof version specifiers.
// Returns sliceID and resolvedCommit (empty string means use current HEAD).
func (s *fileServiceServer) resolveVersion(ctx context.Context, commitHash string, sliceVer *filev1.SliceVersion) (sliceID, resolvedCommit string, err error) {
	// Case 1: slice_version specified
	if sliceVer != nil {
		sliceID = sliceVer.SliceId
		if sliceVer.SliceHash != "" {
			return sliceID, sliceVer.SliceHash, nil
		}
		// No slice_hash: use slice HEAD
		metadata, err := s.storage.GetSliceMetadata(ctx, sliceID)
		if err != nil {
			if errors.Is(err, storage.ErrSliceNotFound) {
				return "", "", status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
			}
			return "", "", status.Error(codes.Internal, fmt.Sprintf("failed to get slice metadata: %v", err))
		}
		return sliceID, metadata.HeadCommitHash, nil
	}

	// Case 2: commit_hash specified (use root)
	rootSlice, err := s.storage.GetRootSlice(ctx)
	if err != nil {
		return "", "", status.Error(codes.Internal, "root slice not found")
	}
	if commitHash != "" {
		return rootSlice.ID, commitHash, nil
	}

	// Case 3: Nothing specified (use root HEAD)
	metadata, err := s.storage.GetSliceMetadata(ctx, rootSlice.ID)
	if err != nil {
		return "", "", status.Error(codes.Internal, fmt.Sprintf("failed to get root slice metadata: %v", err))
	}
	return rootSlice.ID, metadata.HeadCommitHash, nil
}

func sliceFolderMountKey(slice *models.Slice) string {
	if slice == nil || len(slice.FolderMounts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(slice.FolderMounts))
	for _, m := range slice.FolderMounts {
		parts = append(parts, m.SourcePath+"=>"+m.Alias)
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func (s *fileServiceServer) cachedSlicePathMap(ctx context.Context, sliceID string, slice *models.Slice, resolvedCommit string, preferSnapshots bool) (map[string]string, []string, error) {
	commit := strings.TrimSpace(resolvedCommit)
	if commit == "" {
		metadata, err := s.storage.GetSliceMetadata(ctx, sliceID)
		if err != nil {
			if errors.Is(err, storage.ErrSliceNotFound) {
				return map[string]string{}, []string{}, nil
			}
			return nil, nil, err
		}
		commit = strings.TrimSpace(metadata.HeadCommitHash)
	}

	cacheCommit := commit
	if slice != nil && strings.TrimSpace(slice.ParentSlice) != "" && len(slice.FolderMounts) > 0 && !preferSnapshots {
		backingSliceID, err := s.resolveBackingSliceID(ctx, sliceID, slice, false)
		if err != nil {
			return nil, nil, err
		}
		if backingSliceID != "" {
			if backingMeta, err := s.storage.GetSliceMetadata(ctx, backingSliceID); err == nil && backingMeta != nil {
				backingHead := strings.TrimSpace(backingMeta.HeadCommitHash)
				if backingHead != "" {
					cacheCommit = strings.TrimSpace(backingSliceID) + ":" + backingHead
				}
			}
		}
	}

	key := sliceID + "|" + cacheCommit + "|" + sliceFolderMountKey(slice)
	if preferSnapshots {
		key += "|snap"
	} else {
		key += "|live"
	}
	if cached, ok := s.pathCache.get(key); ok && cached != nil {
		return cached.pathMap, cached.displayPaths, nil
	}

	storedPaths, err := s.effectiveSlicePaths(ctx, sliceID, slice, commit, preferSnapshots)
	if err != nil {
		return nil, nil, err
	}

	pathMap := make(map[string]string, len(storedPaths))
	for _, storedPath := range storedPaths {
		displayPath := common.SliceDisplayPath(slice, storedPath)
		if displayPath == "" {
			continue
		}
		if _, exists := pathMap[displayPath]; exists {
			continue
		}
		pathMap[displayPath] = storedPath
	}

	displayPaths := make([]string, 0, len(pathMap))
	for p := range pathMap {
		displayPaths = append(displayPaths, p)
	}
	sort.Strings(displayPaths)

	s.pathCache.put(key, &cachedPaths{pathMap: pathMap, displayPaths: displayPaths})
	return pathMap, displayPaths, nil
}

func (s *fileServiceServer) effectiveSlicePaths(ctx context.Context, sliceID string, slice *models.Slice, resolvedCommit string, preferSnapshots bool) ([]string, error) {
	metadata, err := s.storage.GetSliceMetadata(ctx, sliceID)
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return []string{}, nil
		}
		return nil, err
	}

	commitHash := strings.TrimSpace(resolvedCommit)
	if commitHash == "" {
		commitHash = strings.TrimSpace(metadata.HeadCommitHash)
	}

	if slice != nil && strings.TrimSpace(slice.ParentSlice) != "" && len(slice.FolderMounts) > 0 && !preferSnapshots {
		backingSliceID, err := s.resolveBackingSliceID(ctx, sliceID, slice, false)
		if err != nil {
			return nil, err
		}
		return s.mountedBackingPaths(ctx, backingSliceID, slice.FolderMounts)
	}

	// Prefer commit snapshot paths when available. This keeps current HEAD reads
	// consistent with the materialized directory tree and avoids stale legacy file
	// IDs after deletes.
	if commitHash != "" {
		paths, err := s.storage.ListFilesAtCommit(ctx, commitHash, "")
		if err != nil {
			log.Printf("WARN: snapshot lookup failed for commit %s: %v, falling back to file list", commitHash, err)
		} else {
			cleaned := make([]string, 0, len(paths))
			for _, raw := range paths {
				p := common.CleanRelativePath(raw)
				if p == "" {
					continue
				}
				cleaned = append(cleaned, p)
			}
			sort.Strings(cleaned)
			return cleaned, nil
		}
	}

	paths := make([]string, 0, len(slice.Files))
	seen := make(map[string]bool, len(slice.Files))
	for _, raw := range slice.Files {
		p := common.CleanRelativePath(raw)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}

	for _, raw := range metadata.ModifiedFiles {
		p := common.CleanRelativePath(raw)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	return paths, nil
}

func (s *fileServiceServer) mountedBackingPaths(ctx context.Context, backingSliceID string, mounts []models.SliceFolderMount) ([]string, error) {
	backingSliceID = strings.TrimSpace(backingSliceID)
	if backingSliceID == "" || len(mounts) == 0 {
		return []string{}, nil
	}

	paths := make([]string, 0)
	seenEntries := make(map[string]struct{})
	seenPaths := make(map[string]struct{})
	for _, mount := range mounts {
		sourcePath := common.CleanRelativePath(mount.SourcePath)
		if sourcePath == "" {
			continue
		}
		rootEntry, err := s.storage.GetEntryByPath(ctx, backingSliceID, sourcePath)
		if err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				fallbackPaths, fallbackErr := s.mountedBackingSliceFiles(ctx, backingSliceID, sourcePath)
				if fallbackErr != nil {
					return nil, fallbackErr
				}
				for _, cleanedPath := range fallbackPaths {
					if _, ok := seenPaths[cleanedPath]; !ok {
						seenPaths[cleanedPath] = struct{}{}
						paths = append(paths, cleanedPath)
					}
				}
				continue
			}
			return nil, err
		}

		filesBefore := len(paths)
		queue := []*models.DirectoryEntry{rootEntry}
		for len(queue) > 0 {
			entry := queue[0]
			queue = queue[1:]
			if entry == nil {
				continue
			}
			entryKey := entry.ID + "\x00" + entry.Path
			if _, ok := seenEntries[entryKey]; ok {
				continue
			}
			seenEntries[entryKey] = struct{}{}
			cleanedPath := common.CleanRelativePath(entry.Path)
			if cleanedPath != "" && entry.Type == "file" {
				if _, ok := seenPaths[cleanedPath]; !ok {
					seenPaths[cleanedPath] = struct{}{}
					paths = append(paths, cleanedPath)
				}
			}
			if entry.Type != "directory" {
				continue
			}
			children, err := s.storage.ListEntries(ctx, backingSliceID, entry.ID)
			if err != nil {
				return nil, err
			}
			queue = append(queue, children...)
		}
		if len(paths) == filesBefore {
			fallbackPaths, fallbackErr := s.mountedBackingSliceFiles(ctx, backingSliceID, sourcePath)
			if fallbackErr != nil {
				return nil, fallbackErr
			}
			for _, cleanedPath := range fallbackPaths {
				if _, ok := seenPaths[cleanedPath]; !ok {
					seenPaths[cleanedPath] = struct{}{}
					paths = append(paths, cleanedPath)
				}
			}
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func (s *fileServiceServer) mountedBackingSliceFiles(ctx context.Context, backingSliceID, sourcePath string) ([]string, error) {
	backingSlice, err := s.storage.GetSlice(ctx, backingSliceID)
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil, nil
		}
		return nil, err
	}
	prefix := common.CleanRelativePath(sourcePath) + "/"
	paths := make([]string, 0)
	for _, rawPath := range backingSlice.Files {
		cleanedPath := common.CleanRelativePath(rawPath)
		if cleanedPath == "" || !strings.HasPrefix(cleanedPath, prefix) {
			continue
		}
		paths = append(paths, cleanedPath)
	}
	sort.Strings(paths)
	return paths, nil
}

func (s *fileServiceServer) mountedBackingHasSliceFile(ctx context.Context, backingSliceID, sourcePath string) bool {
	paths, err := s.mountedBackingSliceFiles(ctx, backingSliceID, sourcePath)
	return err == nil && len(paths) > 0
}

func (s *fileServiceServer) childrenIncludeLocalModifiedPath(ctx context.Context, sliceID, parentPath string, children []*models.DirectoryEntry) bool {
	if len(children) == 0 {
		return false
	}
	metadata, err := s.storage.GetSliceMetadata(ctx, sliceID)
	if err != nil || metadata == nil {
		return false
	}

	childPaths := make(map[string]struct{}, len(children))
	for _, child := range children {
		if child == nil {
			continue
		}
		childPath := common.CleanRelativePath(child.Path)
		if childPath == "" {
			continue
		}
		childPaths[childPath] = struct{}{}
	}

	parentPath = common.CleanRelativePath(parentPath)
	for _, rawPath := range metadata.ModifiedFiles {
		modifiedPath := common.CleanRelativePath(rawPath)
		if modifiedPath == "" {
			continue
		}
		if _, ok := childPaths[modifiedPath]; ok {
			return true
		}
		if parentPath != "" {
			prefix := parentPath + "/"
			if !strings.HasPrefix(modifiedPath, prefix) {
				continue
			}
			remaining := strings.TrimPrefix(modifiedPath, prefix)
			name := strings.SplitN(remaining, "/", 2)[0]
			if _, ok := childPaths[path.Join(parentPath, name)]; ok {
				return true
			}
			continue
		}
		name := strings.SplitN(modifiedPath, "/", 2)[0]
		if _, ok := childPaths[name]; ok {
			return true
		}
	}
	return false
}

func (s *fileServiceServer) ListEntries(ctx context.Context, req *filev1.ListEntriesRequest) (*filev1.ListEntriesResponse, error) {
	// Resolve version from oneof
	sliceID, resolvedCommit, err := s.resolveVersion(ctx, req.GetCommitHash(), req.GetSliceVersion())
	if err != nil {
		return nil, err
	}
	preferSnapshots := req.GetCommitHash() != "" || (req.GetSliceVersion() != nil && req.GetSliceVersion().GetSliceHash() != "")

	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}
	publicRead, err := s.authorizeSliceRead(ctx, slice, username, req.GetPath())
	if err != nil {
		return nil, err
	}

	resp, err := s.listEntriesResolved(ctx, sliceID, resolvedCommit, slice, req.GetPath(), req.GetLimit(), preferSnapshots)
	if err != nil {
		return nil, err
	}
	if publicRead {
		filtered, filterErr := s.filterPublicEntries(ctx, slice, resp.GetEntries())
		if filterErr != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to filter visibility: %v", filterErr))
		}
		resp.Entries = filtered
	}
	return resp, nil
}

func (s *fileServiceServer) ListPublicEntries(ctx context.Context, req *filev1.ListPublicEntriesRequest) (*filev1.ListEntriesResponse, error) {
	slice, err := s.resolvePublicSlice(ctx, req.GetSliceId(), req.GetSliceSlug())
	if err != nil {
		return nil, err
	}
	if err := s.ensurePublicDirectoryVisible(ctx, slice, req.GetPath()); err != nil {
		return nil, err
	}

	resp, err := s.listEntriesResolved(ctx, slice.ID, "", slice, req.GetPath(), req.GetLimit(), false)
	if err != nil {
		if status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated {
			return nil, status.Error(codes.NotFound, "path not found")
		}
		return nil, err
	}
	filtered, filterErr := s.filterPublicEntries(ctx, slice, resp.GetEntries())
	if filterErr != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to filter visibility: %v", filterErr))
	}
	resp.Entries = filtered
	return resp, nil
}

func (s *fileServiceServer) listEntriesResolved(ctx context.Context, sliceID, resolvedCommit string, slice *models.Slice, requestPath string, limit int32, preferSnapshots bool) (*filev1.ListEntriesResponse, error) {
	normalizedPath := cleanPath(requestPath)
	responseSliceHash := s.resolvedListEntriesSliceHash(ctx, sliceID, resolvedCommit)

	// Fast path for mounted slices: the root entries are the mount aliases, which we can
	// list without scanning all underlying files. For nested paths we can translate the
	// display path to the stored path and use the directory-entry tree when available.
	if slice != nil && len(slice.FolderMounts) > 0 {
		if normalizedPath == "" {
			entries := make([]*filev1.DirectoryEntry, 0, len(slice.FolderMounts))
			seen := make(map[string]struct{}, len(slice.FolderMounts))
			backingSliceID, err := s.resolveBackingSliceID(ctx, sliceID, slice, preferSnapshots)
			if err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve backing slice: %v", err))
			}
			for _, m := range slice.FolderMounts {
				sourcePath := common.CleanRelativePath(m.SourcePath)
				if sourcePath == "" {
					continue
				}
				if !s.folderMountHasBacking(ctx, backingSliceID, m) {
					continue
				}
				topLevel, _, _ := strings.Cut(sourcePath, "/")
				if topLevel == "" {
					continue
				}
				if _, ok := seen[topLevel]; ok {
					continue
				}
				seen[topLevel] = struct{}{}
				entries = append(entries, &filev1.DirectoryEntry{
					Name:        topLevel,
					Path:        topLevel,
					Type:        filev1.EntryType_ENTRY_TYPE_DIRECTORY,
					HasChildren: true,
				})
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			truncated := false
			if limit > 0 && int(limit) < len(entries) {
				entries = entries[:limit]
				truncated = true
			}

			return newListEntriesResponse(sliceID, "", responseSliceHash, entries, truncated), nil
		}

		// Enforce that requests stay under a mount alias or source path to avoid leaking parent paths.
		underAlias := false
		for _, m := range slice.FolderMounts {
			alias := cleanPath(m.Alias)
			sourcePath := common.CleanRelativePath(m.SourcePath)
			if alias != "" && (normalizedPath == alias || strings.HasPrefix(normalizedPath, alias+"/")) {
				underAlias = true
				break
			}
			if sourcePath != "" && (normalizedPath == sourcePath || strings.HasPrefix(normalizedPath, sourcePath+"/") || strings.HasPrefix(sourcePath, normalizedPath+"/")) {
				underAlias = true
				break
			}
		}
		if !underAlias {
			return nil, status.Error(codes.NotFound, "path not found")
		}

		backingSliceID, err := s.resolveBackingSliceID(ctx, sliceID, slice, preferSnapshots)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve backing slice: %v", err))
		}

		// If the request is for an ancestor of mount source paths (e.g. "nicholas"
		// when mounts include "nicholas/test-project"), synthesize intermediate entries.
		ancestorChildren := make([]*filev1.DirectoryEntry, 0)
		ancestorSeen := make(map[string]struct{})
		for _, m := range slice.FolderMounts {
			sourcePath := common.CleanRelativePath(m.SourcePath)
			if sourcePath == "" {
				continue
			}
			if !strings.HasPrefix(sourcePath, normalizedPath+"/") {
				continue
			}
			rest := strings.TrimPrefix(sourcePath, normalizedPath+"/")
			next, _, _ := strings.Cut(rest, "/")
			if next == "" {
				continue
			}
			if _, ok := ancestorSeen[next]; ok {
				continue
			}
			ancestorSeen[next] = struct{}{}
			entryPath := normalizedPath + "/" + next
			ancestorChildren = append(ancestorChildren, &filev1.DirectoryEntry{
				Name:        next,
				Path:        entryPath,
				Type:        filev1.EntryType_ENTRY_TYPE_DIRECTORY,
				HasChildren: true,
			})
		}
		if len(ancestorChildren) > 0 {
			sort.Slice(ancestorChildren, func(i, j int) bool { return ancestorChildren[i].Name < ancestorChildren[j].Name })
			return newListEntriesResponse(sliceID, normalizedPath, responseSliceHash, ancestorChildren, false), nil
		}

		storedParentPath := common.SliceStoredPath(slice, normalizedPath)
		if storedParentPath == "" {
			return nil, status.Error(codes.NotFound, "path not found")
		}
		if parentEntry, err := s.storage.GetEntryByPath(ctx, sliceID, storedParentPath); err == nil && parentEntry != nil {
			if parentEntry.Type == "file" {
				return nil, status.Error(codes.FailedPrecondition, "path refers to a file")
			}
			children, err := s.storage.ListEntries(ctx, sliceID, parentEntry.ID)
			if err == nil && s.childrenIncludeLocalModifiedPath(ctx, sliceID, storedParentPath, children) {
				return buildListResponse(sliceID, normalizedPath, responseSliceHash, children, slice, limit), nil
			}
		}
		if parentEntry, err := s.storage.GetEntryByPath(ctx, backingSliceID, storedParentPath); err == nil && parentEntry != nil {
			if parentEntry.Type == "file" {
				return nil, status.Error(codes.FailedPrecondition, "path refers to a file")
			}
			children, err := s.storage.ListEntries(ctx, backingSliceID, parentEntry.ID)
			if err == nil {
				return buildListResponse(sliceID, normalizedPath, responseSliceHash, children, slice, limit), nil
			}
		}
		// Fall back to legacy path scanning if directory entries are not materialized.
	}

	// Fast path for materialized directory trees: list direct children via directory entries
	// instead of scanning all descendant file paths under a prefix.
	{
		if normalizedPath != "" {
			if homeBackingID, ok, err := s.rootHomeBackingForPath(ctx, slice, normalizedPath, preferSnapshots); err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve root home backing: %v", err))
			} else if ok {
				if parentEntry, err := s.storage.GetEntryByPath(ctx, homeBackingID, normalizedPath); err == nil && parentEntry != nil {
					if parentEntry.Type == "file" {
						return nil, status.Error(codes.FailedPrecondition, "path refers to a file")
					}
					children, err := s.storage.ListEntries(ctx, homeBackingID, parentEntry.ID)
					if err == nil {
						return buildListResponse(sliceID, normalizedPath, responseSliceHash, children, slice, limit), nil
					}
				} else if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
					return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list root home backing: %v", err))
				}
			}
		}

		backingSliceID, err := s.resolveBackingSliceID(ctx, sliceID, slice, preferSnapshots)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve backing slice: %v", err))
		}

		if normalizedPath == "" {
			children, err := s.storage.ListEntries(ctx, backingSliceID, backingSliceID)
			if err == nil && len(children) > 0 {
				return buildListResponse(sliceID, "", responseSliceHash, children, slice, limit), nil
			}
		} else {
			storedParentPath := common.SliceStoredPath(slice, normalizedPath)
			if storedParentPath != "" {
				if parentEntry, err := s.storage.GetEntryByPath(ctx, backingSliceID, storedParentPath); err == nil && parentEntry != nil {
					if parentEntry.Type == "file" {
						return nil, status.Error(codes.FailedPrecondition, "path refers to a file")
					}
					children, err := s.storage.ListEntries(ctx, backingSliceID, parentEntry.ID)
					if err == nil {
						return buildListResponse(sliceID, normalizedPath, responseSliceHash, children, slice, limit), nil
					}
				}
			}
		}
		// Fall back to legacy path scanning if tree nodes are missing.
	}

	pathMap, displayPaths, err := s.cachedSlicePathMap(ctx, sliceID, slice, resolvedCommit, preferSnapshots)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get slice metadata: %v", err))
	}

	if normalizedPath != "" {
		if _, exists := pathMap[normalizedPath]; !exists {
			for displayPath, storedPath := range pathMap {
				if storedPath == normalizedPath {
					normalizedPath = displayPath
					break
				}
			}
		}
		candidateDisplayPath := common.SliceDisplayPath(slice, normalizedPath)
		if candidateDisplayPath != "" {
			normalizedPath = candidateDisplayPath
		}
	}

	prefix := ""
	if normalizedPath != "" {
		prefix = normalizedPath + "/"
	}

	entriesByName := map[string]*filev1.DirectoryEntry{}
	matchedAny := false
	exactFile := false
	if normalizedPath != "" {
		_, exactFile = pathMap[normalizedPath]
	}

	// When listing a directory, avoid scanning the entire slice path list.
	// displayPaths is sorted, so we can binary-search the prefix range.
	start := 0
	if prefix != "" {
		start = sort.SearchStrings(displayPaths, prefix)
	}

	for i := start; i < len(displayPaths); i++ {
		filePath := displayPaths[i]
		if filePath == "" {
			continue
		}

		if prefix != "" && !strings.HasPrefix(filePath, prefix) {
			// Sorted order: once we pass the prefix range, we can stop.
			break
		}
		matchedAny = true

		remaining := strings.TrimPrefix(filePath, prefix)
		if remaining == "" {
			continue
		}

		parts := strings.Split(remaining, "/")
		name := parts[0]
		entryPath := name
		if normalizedPath != "" {
			entryPath = normalizedPath + "/" + name
		}

		entry, ok := entriesByName[name]
		if !ok {
			entry = &filev1.DirectoryEntry{
				Name: name,
				Path: entryPath,
			}
			entriesByName[name] = entry
		}

		if len(parts) == 1 {
			// Only classify as FILE if not already known to be a DIRECTORY.
			// slice.Files may contain both bare directory paths and nested
			// file paths; Go map iteration order is random, so a bare
			// directory path could be processed after a nested path and
			// incorrectly downgrade the entry from DIRECTORY to FILE.
			if entry.Type != filev1.EntryType_ENTRY_TYPE_DIRECTORY {
				entry.Type = filev1.EntryType_ENTRY_TYPE_FILE
				entry.HasChildren = false
				// Directory listings must stay metadata-only; do not fetch blob content here.
				storedPath := pathMap[filePath]
				if meta, err := s.storage.GetEntryByPath(ctx, sliceID, storedPath); err == nil && meta != nil {
					entry.Size = meta.Size
					entry.Hash = meta.Hash
				}
			}
		} else {
			entry.Type = filev1.EntryType_ENTRY_TYPE_DIRECTORY
			entry.HasChildren = true
		}
	}

	if len(entriesByName) == 0 && normalizedPath != "" {
		if exactFile {
			return nil, status.Error(codes.FailedPrecondition, "path refers to a file")
		}
		if matchedAny {
			// Path exists but contains no children (e.g. empty dir in model).
			return newListEntriesResponse(sliceID, normalizedPath, responseSliceHash, []*filev1.DirectoryEntry{}, false), nil
		}
		return nil, status.Error(codes.NotFound, "path not found")
	}

	entries := make([]*filev1.DirectoryEntry, 0, len(entriesByName))
	for _, entry := range entriesByName {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	truncated := false
	if limit > 0 && int(limit) < len(entries) {
		entries = entries[:limit]
		truncated = true
	}

	return newListEntriesResponse(sliceID, normalizedPath, responseSliceHash, entries, truncated), nil
}

func (s *fileServiceServer) folderMountHasBacking(ctx context.Context, backingSliceID string, mount models.SliceFolderMount) bool {
	sourcePath := cleanPath(mount.SourcePath)
	if sourcePath == "" {
		return false
	}

	entry, err := s.storage.GetEntryByPath(ctx, backingSliceID, sourcePath)
	if err == nil && entry != nil && entry.Type == "directory" {
		return true
	}
	if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
		return false
	}
	return s.mountedBackingHasSliceFile(ctx, backingSliceID, sourcePath)
}

const (
	maxUnaryGetFileBytes   int64 = 10 * 1024 * 1024
	ifNoneMatchMetadataKey       = "if-none-match"
	notModifiedMetadataKey       = "x-gitslice-not-modified"
)

func ifNoneMatchMatches(hash string, values []string) bool {
	hash = strings.TrimSpace(hash)
	if hash == "" || len(values) == 0 {
		return false
	}
	for _, raw := range values {
		for _, token := range strings.Split(raw, ",") {
			t := strings.TrimSpace(token)
			if t == "" {
				continue
			}
			if t == "*" {
				return true
			}
			if len(t) > 2 && (strings.HasPrefix(t, "W/") || strings.HasPrefix(t, "w/")) {
				t = strings.TrimSpace(t[2:])
			}
			t = strings.Trim(t, `"`)
			if t == hash {
				return true
			}
		}
	}
	return false
}

func (s *fileServiceServer) incomingIfNoneMatchValues(ctx context.Context) []string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok || md == nil {
		return nil
	}
	return md.Get(ifNoneMatchMetadataKey)
}

func (s *fileServiceServer) maybeSetNotModifiedHeader(ctx context.Context, responsePath, hash string, size int64) (*filev1.GetFileResponse, bool) {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return nil, false
	}
	if !ifNoneMatchMatches(hash, s.incomingIfNoneMatchValues(ctx)) {
		return nil, false
	}
	_ = grpc.SetHeader(ctx, metadata.Pairs(notModifiedMetadataKey, "true"))
	return &filev1.GetFileResponse{
		File: &filev1.File{
			Path: responsePath,
			Size: size,
			Hash: hash,
		},
	}, true
}

func (s *fileServiceServer) resolveEffectiveCommit(ctx context.Context, primarySliceID, resolvedCommit string) string {
	effectiveCommit := strings.TrimSpace(resolvedCommit)
	if strings.TrimSpace(primarySliceID) != "" && (effectiveCommit == "" || common.IsInitialCommitID(effectiveCommit)) {
		if meta, err := s.storage.GetSliceMetadata(ctx, primarySliceID); err == nil && meta != nil {
			effectiveCommit = strings.TrimSpace(meta.HeadCommitHash)
		}
	}
	return effectiveCommit
}

func (s *fileServiceServer) resolveFileMetadata(
	ctx context.Context,
	slice *models.Slice,
	primarySliceID, storedPath, resolvedCommit string,
	preferSnapshots bool,
) (hash string, size int64, err error) {
	if strings.TrimSpace(primarySliceID) == "" || strings.TrimSpace(storedPath) == "" {
		return "", 0, storage.ErrEntryNotFound
	}

	if manifest, handled, err := s.resolveHomePathHeadFileManifest(ctx, primarySliceID, storedPath, preferSnapshots); handled {
		if err != nil {
			return "", 0, err
		}
		return strings.TrimSpace(manifest.Hash), manifest.TotalSize, nil
	}

	effectiveCommit := s.resolveEffectiveCommit(ctx, primarySliceID, resolvedCommit)
	if effectiveCommit != "" {
		if h, err := s.storage.GetCommitSnapshotFileHash(ctx, effectiveCommit, storedPath); err == nil {
			hash = strings.TrimSpace(h)
		} else if err != nil && !errors.Is(err, storage.ErrEntryNotFound) && !errors.Is(err, storage.ErrCommitNotFound) {
			return "", 0, err
		}
	}

	entry, err := s.storage.GetEntryByPath(ctx, primarySliceID, storedPath)
	if err == nil && entry != nil {
		if strings.TrimSpace(hash) == "" {
			hash = strings.TrimSpace(entry.Hash)
		}
		size = entry.Size
		if strings.TrimSpace(hash) != "" {
			return hash, size, nil
		}
	} else if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
		return "", 0, err
	}

	if strings.TrimSpace(hash) == "" || size == 0 {
		manifest, manifestErr := s.storage.GetFileManifest(ctx, primarySliceID, storedPath)
		if manifestErr == nil && manifest != nil {
			if strings.TrimSpace(hash) == "" {
				hash = strings.TrimSpace(manifest.Hash)
			}
			if size == 0 {
				size = manifest.TotalSize
			}
		} else if manifestErr != nil && !errors.Is(manifestErr, storage.ErrEntryNotFound) {
			return "", 0, manifestErr
		}
		if strings.TrimSpace(hash) != "" {
			return strings.TrimSpace(hash), size, nil
		}
	}

	if slice != nil && slice.ParentSlice != "" && slice.ParentSlice != primarySliceID {
		parentEntry, parentErr := s.storage.GetEntryByPath(ctx, slice.ParentSlice, storedPath)
		if parentErr == nil && parentEntry != nil {
			if strings.TrimSpace(hash) == "" {
				hash = strings.TrimSpace(parentEntry.Hash)
			}
			if size == 0 {
				size = parentEntry.Size
			}
		} else if parentErr != nil && !errors.Is(parentErr, storage.ErrEntryNotFound) {
			return "", 0, parentErr
		}
	}

	return strings.TrimSpace(hash), size, nil
}

func (s *fileServiceServer) resolveHomePathHeadFileManifest(
	ctx context.Context,
	primarySliceID, storedPath string,
	preferSnapshots bool,
) (*models.FileManifest, bool, error) {
	if preferSnapshots {
		return nil, false, nil
	}
	homeID := homeslice.UsernameFromSliceID(primarySliceID)
	if homeID == "" {
		return nil, false, nil
	}
	headStore, ok := s.storage.(storage.HomePathHeadStore)
	if !ok {
		return nil, false, nil
	}
	cleanedPath := cleanPath(storedPath)
	if cleanedPath == "" {
		return nil, false, nil
	}
	heads, err := headStore.GetHomePathHeads(ctx, homeID, []string{cleanedPath})
	if err != nil {
		return nil, true, err
	}
	head := heads[cleanedPath]
	if head == nil {
		return nil, false, nil
	}
	entryType := strings.TrimSpace(head.EntryType)
	if head.Deleted || (entryType != "" && entryType != "file") {
		return nil, true, storage.ErrEntryNotFound
	}
	manifestHash := strings.TrimSpace(head.ManifestHash)
	if manifestHash == "" {
		manifestHash = strings.TrimSpace(head.ContentHash)
	}
	if manifestHash == "" {
		return nil, true, storage.ErrEntryNotFound
	}
	manifest, err := s.storage.GetVersionedFileManifest(ctx, manifestHash)
	if err != nil {
		return nil, true, err
	}
	copied := *manifest
	copied.Path = cleanedPath
	copied.Hash = manifestHash
	return &copied, true, nil
}

func (s *fileServiceServer) resolveFileContent(
	ctx context.Context,
	sliceID string,
	slice *models.Slice,
	primarySliceID, storedPath, resolvedCommit string,
	preferSnapshots bool,
) (*models.FileContent, error) {
	if strings.TrimSpace(primarySliceID) == "" || strings.TrimSpace(storedPath) == "" {
		return nil, storage.ErrEntryNotFound
	}

	if manifest, handled, err := s.resolveHomePathHeadFileManifest(ctx, primarySliceID, storedPath, preferSnapshots); handled {
		if err != nil {
			return nil, err
		}
		return storage.ReadManifestContent(ctx, s.storage, manifest)
	}

	effectiveCommit := s.resolveEffectiveCommit(ctx, primarySliceID, resolvedCommit)
	if effectiveCommit != "" {
		if content, err := s.storage.GetFileAtCommit(ctx, effectiveCommit, storedPath); err == nil && content != nil {
			return content, nil
		}
	}

	content, err := storage.ReadSliceFileContent(ctx, s.storage, primarySliceID, storedPath)
	if err == nil && content != nil {
		return content, nil
	}
	if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
		return nil, err
	}

	if slice != nil && slice.ParentSlice != "" && slice.ParentSlice != primarySliceID {
		parentContent, parentErr := storage.ReadSliceFileContent(ctx, s.storage, slice.ParentSlice, storedPath)
		if parentErr == nil && parentContent != nil {
			return parentContent, nil
		}
		if parentErr != nil && !errors.Is(parentErr, storage.ErrEntryNotFound) {
			return nil, parentErr
		}
	}

	return nil, storage.ErrEntryNotFound
}

func (s *fileServiceServer) getFileFromCommitSnapshot(
	ctx context.Context,
	primarySliceID, storedPath, responsePath, resolvedCommit string,
) (*filev1.GetFileResponse, bool, error) {
	effectiveCommit := s.resolveEffectiveCommit(ctx, primarySliceID, resolvedCommit)
	if effectiveCommit == "" {
		return nil, false, nil
	}
	hash, err := s.storage.GetCommitSnapshotFileHash(ctx, effectiveCommit, storedPath)
	if err != nil {
		if errors.Is(err, storage.ErrCommitNotFound) {
			return nil, true, status.Error(codes.NotFound, "commit snapshot not found")
		}
		if errors.Is(err, storage.ErrEntryNotFound) {
			return nil, true, status.Error(codes.NotFound, "file not found")
		}
		return nil, true, status.Error(codes.Internal, fmt.Sprintf("failed to load file metadata: %v", err))
	}
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return nil, true, status.Error(codes.NotFound, "file content not available")
	}

	manifest, err := s.storage.GetVersionedFileManifest(ctx, hash)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) {
			return nil, true, status.Error(codes.NotFound, "file content not available")
		}
		return nil, true, status.Error(codes.Internal, fmt.Sprintf("failed to load file metadata: %v", err))
	}
	size := manifest.TotalSize
	if notModifiedResp, matched := s.maybeSetNotModifiedHeader(ctx, responsePath, hash, size); matched {
		return notModifiedResp, true, nil
	}
	if size > maxUnaryGetFileBytes {
		return nil, true, status.Error(codes.ResourceExhausted, fmt.Sprintf("file is too large for GetFile (%d bytes > %d bytes); use a streamed download path", size, maxUnaryGetFileBytes))
	}

	content, err := storage.ReadManifestContent(ctx, s.storage, manifest)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) {
			return nil, true, status.Error(codes.NotFound, "file content not available")
		}
		return nil, true, status.Error(codes.Internal, fmt.Sprintf("failed to load file content: %v", err))
	}
	if contentSize(content) > maxUnaryGetFileBytes {
		return nil, true, status.Error(codes.ResourceExhausted, fmt.Sprintf("file is too large for GetFile (%d bytes > %d bytes); use a streamed download path", contentSize(content), maxUnaryGetFileBytes))
	}
	if size == 0 {
		size = contentSize(content)
	}

	return &filev1.GetFileResponse{File: &filev1.File{
		Path:    responsePath,
		Content: content.Content,
		Size:    size,
		Hash:    hash,
	}}, true, nil
}

func (s *fileServiceServer) GetFile(ctx context.Context, req *filev1.GetFileRequest) (*filev1.GetFileResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	// Resolve version from oneof
	sliceID, resolvedCommit, err := s.resolveVersion(ctx, req.GetCommitHash(), req.GetSliceVersion())
	if err != nil {
		return nil, err
	}
	preferSnapshots := req.GetCommitHash() != "" || (req.GetSliceVersion() != nil && req.GetSliceVersion().GetSliceHash() != "")

	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.authorizeSliceRead(ctx, slice, username, req.GetPath()); err != nil {
		return nil, err
	}

	return s.getFileResolved(ctx, sliceID, resolvedCommit, slice, req.GetPath(), preferSnapshots)
}

func (s *fileServiceServer) GetPublicFile(ctx context.Context, req *filev1.GetPublicFileRequest) (*filev1.GetFileResponse, error) {
	if strings.TrimSpace(req.GetPath()) == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}
	slice, err := s.resolvePublicSlice(ctx, req.GetSliceId(), req.GetSliceSlug())
	if err != nil {
		return nil, err
	}
	if err := s.ensurePublicPathVisible(ctx, slice, req.GetPath()); err != nil {
		return nil, err
	}
	return s.getFileResolved(ctx, slice.ID, "", slice, req.GetPath(), false)
}

func (s *fileServiceServer) getFileResolved(ctx context.Context, sliceID, resolvedCommit string, slice *models.Slice, requestPath string, preferSnapshots bool) (*filev1.GetFileResponse, error) {
	requestPath = cleanPath(requestPath)

	// Mounted slices can translate display paths to stored paths without scanning the
	// full slice file set. Restrict requests to mount aliases to avoid leaking parent files.
	if slice != nil && len(slice.FolderMounts) > 0 {
		underAlias := false
		for _, m := range slice.FolderMounts {
			alias := cleanPath(m.Alias)
			sourcePath := common.CleanRelativePath(m.SourcePath)
			if alias != "" && (requestPath == alias || strings.HasPrefix(requestPath, alias+"/")) {
				underAlias = true
				break
			}
			if sourcePath != "" && (requestPath == sourcePath || strings.HasPrefix(requestPath, sourcePath+"/") || strings.HasPrefix(sourcePath, requestPath+"/")) {
				underAlias = true
				break
			}
		}
		if !underAlias {
			return nil, status.Error(codes.NotFound, "file not found")
		}

		storedPath := common.SliceStoredPath(slice, requestPath)
		if storedPath == "" {
			return nil, status.Error(codes.NotFound, "file not found")
		}

		backingSliceID, err := s.resolveBackingSliceID(ctx, sliceID, slice, preferSnapshots)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve backing slice: %v", err))
		}
		responsePath := storedPath
		if len(slice.FolderMounts) == 0 {
			responsePath = common.SliceDisplayPath(slice, storedPath)
		}

		hash, size, metaErr := s.resolveFileMetadata(ctx, slice, backingSliceID, storedPath, resolvedCommit, preferSnapshots)
		if metaErr != nil && !errors.Is(metaErr, storage.ErrEntryNotFound) {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load file metadata: %v", metaErr))
		}
		if notModifiedResp, matched := s.maybeSetNotModifiedHeader(ctx, responsePath, hash, size); matched {
			return notModifiedResp, nil
		}

		content, err := s.resolveFileContent(ctx, sliceID, slice, backingSliceID, storedPath, resolvedCommit, preferSnapshots)
		if err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				return nil, status.Error(codes.NotFound, "file not found")
			}
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load file content: %v", err))
		}
		if size := contentSize(content); size > maxUnaryGetFileBytes {
			return nil, status.Error(codes.ResourceExhausted, fmt.Sprintf("file is too large for GetFile (%d bytes > %d bytes); use a streamed download path", size, maxUnaryGetFileBytes))
		}
		if strings.TrimSpace(hash) == "" {
			hash = strings.TrimSpace(content.Hash)
		}
		if size == 0 {
			size = contentSize(content)
		}

		file := &filev1.File{
			Path:    responsePath,
			Content: content.Content,
			Size:    size,
			Hash:    hash,
		}
		return &filev1.GetFileResponse{File: file}, nil
	}

	if homeBackingID, ok, err := s.rootHomeBackingForPath(ctx, slice, requestPath, preferSnapshots); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve root home backing: %v", err))
	} else if ok {
		hash, size, metaErr := s.resolveFileMetadata(ctx, slice, homeBackingID, requestPath, resolvedCommit, preferSnapshots)
		if metaErr != nil && !errors.Is(metaErr, storage.ErrEntryNotFound) {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load file metadata: %v", metaErr))
		}
		if notModifiedResp, matched := s.maybeSetNotModifiedHeader(ctx, requestPath, hash, size); matched {
			return notModifiedResp, nil
		}
		content, err := s.resolveFileContent(ctx, sliceID, slice, homeBackingID, requestPath, resolvedCommit, preferSnapshots)
		if err == nil && content != nil {
			if size := contentSize(content); size > maxUnaryGetFileBytes {
				return nil, status.Error(codes.ResourceExhausted, fmt.Sprintf("file is too large for GetFile (%d bytes > %d bytes); use a streamed download path", size, maxUnaryGetFileBytes))
			}
			if strings.TrimSpace(hash) == "" {
				hash = strings.TrimSpace(content.Hash)
			}
			if size == 0 {
				size = contentSize(content)
			}
			return &filev1.GetFileResponse{File: &filev1.File{
				Path:    requestPath,
				Content: content.Content,
				Size:    size,
				Hash:    hash,
			}}, nil
		}
		if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load file content: %v", err))
		}
	}

	if preferSnapshots && (slice == nil || len(slice.FolderMounts) == 0) {
		if resp, handled, err := s.getFileFromCommitSnapshot(ctx, sliceID, requestPath, requestPath, resolvedCommit); handled {
			return resp, err
		}
	}

	pathMap, _, mapErr := s.cachedSlicePathMap(ctx, sliceID, slice, resolvedCommit, preferSnapshots)
	if mapErr != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get slice metadata: %v", mapErr))
	}

	// API requests are expressed in slice *display* paths. pathMap keys are display
	// paths and values are stored paths.
	displayPath := requestPath
	storedPath, found := pathMap[displayPath]
	if !found && displayPath != "" {
		// Backward-compatible: accept stored paths if a client passes them through.
		for dp, sp := range pathMap {
			if sp == displayPath {
				displayPath = dp
				storedPath = sp
				found = true
				break
			}
		}
	}
	if !found || storedPath == "" {
		return nil, status.Error(codes.NotFound, "file not found")
	}
	responsePath := common.SliceDisplayPath(slice, storedPath)
	if responsePath == "" {
		responsePath = displayPath
	}

	backingSliceID, err := s.resolveBackingSliceID(ctx, sliceID, slice, preferSnapshots)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve backing slice: %v", err))
	}

	hash, size, metaErr := s.resolveFileMetadata(ctx, slice, backingSliceID, storedPath, resolvedCommit, preferSnapshots)
	if metaErr != nil && !errors.Is(metaErr, storage.ErrEntryNotFound) {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load file metadata: %v", metaErr))
	}
	if notModifiedResp, matched := s.maybeSetNotModifiedHeader(ctx, responsePath, hash, size); matched {
		return notModifiedResp, nil
	}

	content, err := s.resolveFileContent(ctx, sliceID, slice, backingSliceID, storedPath, resolvedCommit, preferSnapshots)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) {
			if sliceHasPath(pathMap, displayPath) {
				return nil, status.Error(codes.NotFound, "file content not available")
			}
			return nil, status.Error(codes.NotFound, "file not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load file content: %v", err))
	}
	if size := contentSize(content); size > maxUnaryGetFileBytes {
		return nil, status.Error(codes.ResourceExhausted, fmt.Sprintf("file is too large for GetFile (%d bytes > %d bytes); use a streamed download path", size, maxUnaryGetFileBytes))
	}
	if strings.TrimSpace(hash) == "" {
		hash = strings.TrimSpace(content.Hash)
	}
	if size == 0 {
		size = contentSize(content)
	}

	file := &filev1.File{
		Path:    responsePath,
		Content: content.Content,
		Size:    size,
		Hash:    hash,
	}

	return &filev1.GetFileResponse{File: file}, nil
}

func cleanPath(raw string) string {
	return common.CleanRelativePath(raw)
}

func (s *fileServiceServer) resolvedListEntriesSliceHash(ctx context.Context, sliceID, resolvedCommit string) string {
	if commitHash := strings.TrimSpace(resolvedCommit); commitHash != "" {
		return commitHash
	}
	if strings.TrimSpace(sliceID) == "" {
		return ""
	}
	metadata, err := s.storage.GetSliceMetadata(ctx, sliceID)
	if err != nil || metadata == nil {
		return ""
	}
	return strings.TrimSpace(metadata.HeadCommitHash)
}

func newListEntriesResponse(sliceID, listPath, sliceHash string, entries []*filev1.DirectoryEntry, truncated bool) *filev1.ListEntriesResponse {
	return &filev1.ListEntriesResponse{
		SliceId:   sliceID,
		Path:      listPath,
		Entries:   entries,
		Truncated: truncated,
		SliceHash: strings.TrimSpace(sliceHash),
	}
}

func buildListResponse(sliceID, listPath, sliceHash string, children []*models.DirectoryEntry, slice *models.Slice, limit int32) *filev1.ListEntriesResponse {
	entries := make([]*filev1.DirectoryEntry, 0, len(children))
	for _, child := range children {
		if child == nil {
			continue
		}
		displayChildPath := child.Path
		if slice == nil || len(slice.FolderMounts) == 0 {
			displayChildPath = common.SliceDisplayPath(slice, child.Path)
		}
		if displayChildPath == "" {
			continue
		}

		typ := filev1.EntryType_ENTRY_TYPE_FILE
		hasChildren := false
		if child.Type == "directory" {
			typ = filev1.EntryType_ENTRY_TYPE_DIRECTORY
			hasChildren = true
		}

		entries = append(entries, &filev1.DirectoryEntry{
			Name:        path.Base(displayChildPath),
			Path:        displayChildPath,
			Type:        typ,
			HasChildren: hasChildren,
			Size:        child.Size,
			Hash:        child.Hash,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	truncated := false
	if limit > 0 && int(limit) < len(entries) {
		entries = entries[:limit]
		truncated = true
	}
	return newListEntriesResponse(sliceID, listPath, sliceHash, entries, truncated)
}

func contentSize(content *models.FileContent) int64 {
	if content == nil {
		return 0
	}
	if content.Size != 0 {
		return content.Size
	}
	return int64(len(content.Content))
}

func sliceHasPath(pathMap map[string]string, path string) bool {
	if _, ok := pathMap[path]; ok {
		return true
	}
	for _, stored := range pathMap {
		if stored == path {
			return true
		}
	}
	return false
}

type historyLookupTarget struct {
	sliceID string
	path    string
}

func appendHistoryLookupTarget(targets *[]historyLookupTarget, seen map[string]struct{}, sliceID, rawPath string) {
	sliceID = strings.TrimSpace(sliceID)
	if sliceID == "" {
		return
	}

	pathValue := cleanPath(rawPath)
	if strings.TrimSpace(rawPath) == "" {
		pathValue = ""
	}

	key := sliceID + "\x00" + pathValue
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	*targets = append(*targets, historyLookupTarget{
		sliceID: sliceID,
		path:    pathValue,
	})
}

func pathWithinMount(pathValue, mountPath string) bool {
	mountPath = cleanPath(mountPath)
	if mountPath == "" {
		return false
	}
	return pathValue == mountPath || strings.HasPrefix(pathValue, mountPath+"/")
}

func canFallbackToParentHistory(slice *models.Slice, normalizedPath string) bool {
	if slice == nil || strings.TrimSpace(slice.ParentSlice) == "" {
		return false
	}
	if len(slice.FolderMounts) == 0 {
		return true
	}
	if normalizedPath == "" {
		// Mounted slice roots can represent multiple folders; querying parent root
		// would leak unrelated history outside the mount set.
		return false
	}

	for _, mount := range slice.FolderMounts {
		if pathWithinMount(normalizedPath, mount.Alias) || pathWithinMount(normalizedPath, mount.SourcePath) {
			return true
		}
	}
	return false
}

func historyLookupTargets(slice *models.Slice, sliceID, normalizedPath string) []historyLookupTarget {
	targets := make([]historyLookupTarget, 0, 4)
	seen := make(map[string]struct{}, 4)

	appendHistoryLookupTarget(&targets, seen, sliceID, normalizedPath)

	storedPath := common.SliceStoredPath(slice, normalizedPath)
	if storedPath != "" {
		appendHistoryLookupTarget(&targets, seen, sliceID, storedPath)
	}

	if !canFallbackToParentHistory(slice, normalizedPath) {
		return targets
	}

	parentSliceID := strings.TrimSpace(slice.ParentSlice)
	if storedPath != "" {
		appendHistoryLookupTarget(&targets, seen, parentSliceID, storedPath)
	}
	appendHistoryLookupTarget(&targets, seen, parentSliceID, normalizedPath)

	return targets
}

func remapChangePathForSlice(slice *models.Slice, change *filev1.FileChangeRecord) {
	if change == nil {
		return
	}
	if mapped := common.SliceDisplayPath(slice, change.Path); mapped != "" {
		change.Path = mapped
	}
	if change.OldPath != "" {
		if mapped := common.SliceDisplayPath(slice, change.OldPath); mapped != "" {
			change.OldPath = mapped
		}
	}
}

func remapPathForSummary(slice *models.Slice, rawPath string) string {
	cleaned := cleanPath(rawPath)
	if cleaned == "" {
		return ""
	}

	mapped := common.SliceDisplayPath(slice, cleaned)
	if mapped == "" {
		mapped = cleaned
	}
	if strings.HasSuffix(rawPath, "/") && !strings.HasSuffix(mapped, "/") {
		return mapped + "/"
	}
	return mapped
}

func remapDirectorySummaryForSlice(slice *models.Slice, summary *filev1.DirectoryChangeSummary) {
	if summary == nil {
		return
	}
	summary.Path = remapPathForSummary(slice, summary.Path)
	remapChangePathForSlice(slice, summary.LastChange)
}

// GetFileHistory retrieves the change history for a specific file.
func (s *fileServiceServer) GetFileHistory(ctx context.Context, req *filev1.GetFileHistoryRequest) (*filev1.GetFileHistoryResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	sliceID := req.SliceId
	if sliceID == "" {
		rootSlice, err := s.storage.GetRootSlice(ctx)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to get root slice")
		}
		sliceID = rootSlice.ID
	}

	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	normalizedPath := cleanPath(req.Path)
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 50
	}

	targets := historyLookupTargets(slice, sliceID, normalizedPath)
	var changes []*models.FileChangeRecord
	for _, target := range targets {
		candidate, err := s.storage.GetFileHistory(ctx, target.sliceID, target.path, limit+1, req.FromCommit)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get file history: %v", err))
		}
		if len(candidate) == 0 {
			continue
		}
		changes = candidate
		break
	}

	hasMore := len(changes) > limit
	if hasMore {
		changes = changes[:limit]
	}

	var nextCommit string
	if hasMore && len(changes) > 0 {
		nextCommit = changes[len(changes)-1].CommitHash
	}

	protoChanges := make([]*filev1.FileChangeRecord, 0, len(changes))
	for _, change := range changes {
		protoChange := modelToProtoChange(change, "")
		remapChangePathForSlice(slice, protoChange)
		protoChanges = append(protoChanges, protoChange)
	}

	return &filev1.GetFileHistoryResponse{
		Changes:    protoChanges,
		HasMore:    hasMore,
		NextCommit: nextCommit,
	}, nil
}

// GetDirectoryHistory retrieves change history for all files under a directory.
func (s *fileServiceServer) GetDirectoryHistory(ctx context.Context, req *filev1.GetDirectoryHistoryRequest) (*filev1.GetDirectoryHistoryResponse, error) {
	sliceID := req.SliceId
	if sliceID == "" {
		rootSlice, err := s.storage.GetRootSlice(ctx)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to get root slice")
		}
		sliceID = rootSlice.ID
	}

	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	normalizedPath := cleanPath(req.Path)
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 100
	}

	targets := historyLookupTargets(slice, sliceID, normalizedPath)
	summaryTarget := historyLookupTarget{sliceID: sliceID, path: normalizedPath}
	if len(targets) > 0 {
		summaryTarget = targets[0]
	}

	var changes []*models.FileChangeRecord
	for _, target := range targets {
		candidate, err := s.storage.GetDirectoryHistory(ctx, target.sliceID, target.path, limit+1, req.FromCommit)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get directory history: %v", err))
		}
		if len(candidate) == 0 {
			continue
		}
		changes = candidate
		summaryTarget = target
		break
	}

	hasMore := len(changes) > limit
	if hasMore {
		changes = changes[:limit]
	}

	var nextCommit string
	if hasMore && len(changes) > 0 {
		nextCommit = changes[len(changes)-1].CommitHash
	}

	protoChanges := make([]*filev1.FileChangeRecord, 0, len(changes))
	for _, change := range changes {
		protoChange := modelToProtoChange(change, "")
		remapChangePathForSlice(slice, protoChange)
		protoChanges = append(protoChanges, protoChange)
	}

	// Get summary
	summary, err := s.storage.GetDirectorySummary(ctx, summaryTarget.sliceID, summaryTarget.path)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get directory summary: %v", err))
	}
	protoSummary := modelToProtoSummary(summary)
	remapDirectorySummaryForSlice(slice, protoSummary)

	return &filev1.GetDirectoryHistoryResponse{
		Changes:    protoChanges,
		HasMore:    hasMore,
		NextCommit: nextCommit,
		Summary:    protoSummary,
	}, nil
}

// maxPatchableChanges is the threshold above which patch generation is skipped
// even when requested, to avoid excessive storage reads on large commits.
const maxPatchableChanges = 100

// patchWorkers is the concurrency limit for parallel patch generation.
const patchWorkers = 8

// GetCommitChanges retrieves all file changes made in a specific commit.
func (s *fileServiceServer) GetCommitChanges(ctx context.Context, req *filev1.GetCommitChangesRequest) (*filev1.GetCommitChangesResponse, error) {
	if req.CommitHash == "" {
		return nil, status.Error(codes.InvalidArgument, "commit_hash is required")
	}

	changes, err := s.storage.GetCommitChanges(ctx, req.CommitHash)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get commit changes: %v", err))
	}

	if err := s.authorizeCommitChanges(ctx, changes); err != nil {
		return nil, err
	}

	protoChanges := make([]*filev1.FileChangeRecord, len(changes))
	var added, modified, deleted, renamed int32
	for i, change := range changes {
		protoChanges[i] = modelToProtoChange(change, "")
		switch change.ChangeType {
		case models.ChangeTypeAdd:
			added++
		case models.ChangeTypeModify:
			modified++
		case models.ChangeTypeDelete:
			deleted++
		case models.ChangeTypeRename:
			renamed++
		}
	}

	// Only compute patches when explicitly requested and the commit is not too large.
	if req.IncludePatches && len(changes) <= maxPatchableChanges {
		// Resolve parent hashes (sequential -- typically one unique key per commit).
		parentHashes := make(map[string]string)
		for _, change := range changes {
			if change == nil {
				continue
			}
			key := change.SliceID + "\x00" + change.CommitHash
			if _, ok := parentHashes[key]; ok {
				continue
			}
			resolvedParent, err := s.findParentCommitHash(ctx, change.SliceID, change.CommitHash)
			if err == nil {
				parentHashes[key] = resolvedParent
			} else {
				parentHashes[key] = ""
			}
		}

		snapshots := make(map[string]*models.CommitSnapshot)
		for _, change := range changes {
			if change == nil {
				continue
			}
			if _, ok := snapshots[change.CommitHash]; !ok {
				snapshots[change.CommitHash] = s.loadSnapshot(ctx, change.CommitHash)
			}
			parentHash := parentHashes[change.SliceID+"\x00"+change.CommitHash]
			if parentHash == "" {
				continue
			}
			if _, ok := snapshots[parentHash]; !ok {
				snapshots[parentHash] = s.loadSnapshot(ctx, parentHash)
			}
		}

		// Generate patches in parallel with bounded concurrency.
		sem := make(chan struct{}, patchWorkers)
		var wg sync.WaitGroup
		for i, change := range changes {
			if change == nil {
				continue
			}
			parentHash := parentHashes[change.SliceID+"\x00"+change.CommitHash]

			wg.Add(1)
			sem <- struct{}{} // acquire slot
			go func(idx int, ch *models.FileChangeRecord, ph string) {
				defer wg.Done()
				defer func() { <-sem }() // release slot
				protoChanges[idx].Patch = s.buildChangePatch(ctx, ch, ph, snapshots[ph], snapshots[ch.CommitHash])
			}(i, change, parentHash)
		}
		wg.Wait()
	}

	return &filev1.GetCommitChangesResponse{
		CommitHash:    req.CommitHash,
		Changes:       protoChanges,
		FilesAdded:    added,
		FilesModified: modified,
		FilesDeleted:  deleted,
		FilesRenamed:  renamed,
	}, nil
}

func (s *fileServiceServer) authorizeCommitChanges(ctx context.Context, changes []*models.FileChangeRecord) error {
	username, err := s.optionalUsername(ctx)
	if err != nil {
		return err
	}
	checked := make(map[string]struct{})
	for _, change := range changes {
		if change == nil || strings.TrimSpace(change.SliceID) == "" {
			continue
		}
		sliceID := strings.TrimSpace(change.SliceID)
		if _, ok := checked[sliceID]; ok {
			continue
		}
		checked[sliceID] = struct{}{}

		slice, err := s.storage.GetSlice(ctx, sliceID)
		if err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("failed to load slice %s for authorization: %v", sliceID, err))
		}
		if _, err := s.authorizeSliceRead(ctx, slice, username, change.Path); err != nil {
			return err
		}
	}
	return nil
}

// modelToProtoChange converts a model FileChangeRecord to protobuf.
func modelToProtoChange(change *models.FileChangeRecord, patch string) *filev1.FileChangeRecord {
	return &filev1.FileChangeRecord{
		Id:           change.ID,
		SliceId:      change.SliceID,
		CommitHash:   change.CommitHash,
		Path:         change.Path,
		OldPath:      change.OldPath,
		ChangeType:   modelToProtoChangeType(change.ChangeType),
		OldHash:      change.OldHash,
		NewHash:      change.NewHash,
		LinesAdded:   int32(change.LinesAdded),
		LinesDeleted: int32(change.LinesDeleted),
		Author:       change.Author,
		Message:      change.Message,
		Timestamp:    change.Timestamp.Unix(),
		Patch:        patch,
	}
}

func (s *fileServiceServer) buildChangePatch(ctx context.Context, change *models.FileChangeRecord, parentHash string, parentSnapshot, currentSnapshot *models.CommitSnapshot) string {
	if change == nil {
		return ""
	}

	newPath := cleanPath(change.Path)
	oldPath := cleanPath(change.OldPath)
	if oldPath == "" {
		oldPath = newPath
	}

	beforeLines := []string{}
	afterLines := []string{}

	shouldLoadBefore := change.OldHash != "" || change.ChangeType == models.ChangeTypeModify || change.ChangeType == models.ChangeTypeDelete || change.ChangeType == models.ChangeTypeRename
	beforeUndiffable := false
	if shouldLoadBefore {
		oldHash := strings.TrimSpace(change.OldHash)
		if oldHash == "" && parentSnapshot != nil {
			oldHash = parentSnapshot.Files[oldPath]
		}
		if prev := s.getFileContent(ctx, oldHash, parentHash, oldPath); prev != nil {
			if lines, ok := splitLinesForDiff(prev.Content); ok {
				beforeLines = lines
			} else {
				beforeUndiffable = true
			}
		}
	}

	shouldLoadAfter := change.NewHash != "" || change.ChangeType == models.ChangeTypeAdd || change.ChangeType == models.ChangeTypeModify || change.ChangeType == models.ChangeTypeRename
	afterUndiffable := false
	if shouldLoadAfter {
		newHash := strings.TrimSpace(change.NewHash)
		if newHash == "" && currentSnapshot != nil {
			newHash = currentSnapshot.Files[newPath]
		}
		if curr := s.getFileContent(ctx, newHash, change.CommitHash, newPath); curr != nil {
			if lines, ok := splitLinesForDiff(curr.Content); ok {
				afterLines = lines
			} else {
				afterUndiffable = true
			}
		}
	}
	if beforeUndiffable || afterUndiffable {
		return ""
	}

	if len(beforeLines) == 0 && len(afterLines) == 0 {
		return ""
	}

	unified, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        beforeLines,
		B:        afterLines,
		FromFile: "a/" + oldPath,
		ToFile:   "b/" + newPath,
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return unified
}

func (s *fileServiceServer) loadSnapshot(ctx context.Context, commitHash string) *models.CommitSnapshot {
	if strings.TrimSpace(commitHash) == "" {
		return nil
	}
	snapshot, err := s.storage.GetCommitSnapshot(ctx, commitHash)
	if err != nil {
		return nil
	}
	return snapshot
}

func (s *fileServiceServer) getFileContent(ctx context.Context, contentHash, commitHash, filePath string) *models.FileContent {
	if strings.TrimSpace(contentHash) != "" {
		if content, err := storage.ReadVersionedFileContent(ctx, s.storage, contentHash); err == nil && content != nil {
			return content
		}
	}
	if strings.TrimSpace(commitHash) == "" || strings.TrimSpace(filePath) == "" {
		return nil
	}
	content, err := s.storage.GetFileAtCommit(ctx, commitHash, filePath)
	if err != nil {
		return nil
	}
	return content
}

func (s *fileServiceServer) findParentCommitHash(ctx context.Context, sliceID, commitHash string) (string, error) {
	if sliceID == "" || commitHash == "" {
		return "", nil
	}

	commit, err := s.storage.GetCommitByHash(ctx, sliceID, commitHash)
	if err != nil {
		if errors.Is(err, storage.ErrCommitNotFound) {
			return "", nil
		}
		return "", err
	}
	if commit == nil {
		return "", nil
	}
	return commit.ParentHash, nil
}

func splitLinesForDiff(content []byte) ([]string, bool) {
	if len(content) == 0 {
		return []string{}, true
	}
	// Diff patches are encoded as protobuf string fields, so invalid UTF-8 or
	// binary data cannot be returned safely as patch text.
	if !utf8.Valid(content) || bytesContainsNUL(content) {
		return nil, false
	}
	lines := strings.SplitAfter(string(content), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, true
}

func bytesContainsNUL(content []byte) bool {
	for _, b := range content {
		if b == 0 {
			return true
		}
	}
	return false
}

// modelToProtoChangeType converts model ChangeType to protobuf.
func modelToProtoChangeType(ct models.ChangeType) filev1.ChangeType {
	switch ct {
	case models.ChangeTypeAdd:
		return filev1.ChangeType_CHANGE_TYPE_ADD
	case models.ChangeTypeModify:
		return filev1.ChangeType_CHANGE_TYPE_MODIFY
	case models.ChangeTypeDelete:
		return filev1.ChangeType_CHANGE_TYPE_DELETE
	case models.ChangeTypeRename:
		return filev1.ChangeType_CHANGE_TYPE_RENAME
	default:
		return filev1.ChangeType_CHANGE_TYPE_UNSPECIFIED
	}
}

// modelToProtoSummary converts a model DirectoryChangeSummary to protobuf.
func modelToProtoSummary(summary *models.DirectoryChangeSummary) *filev1.DirectoryChangeSummary {
	changesByType := make(map[string]int32)
	for ct, count := range summary.ChangesByType {
		changesByType[string(ct)] = int32(count)
	}

	var lastChange *filev1.FileChangeRecord
	if summary.LastChange != nil {
		lastChange = modelToProtoChange(summary.LastChange, "")
	}

	return &filev1.DirectoryChangeSummary{
		Path:          summary.Path,
		TotalChanges:  int32(summary.TotalChanges),
		FilesChanged:  int32(summary.FilesChanged),
		ChangesByType: changesByType,
		LastChange:    lastChange,
	}
}
