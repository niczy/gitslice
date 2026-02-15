package fileservice

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/authz"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
	"github.com/pmezard/go-difflib/difflib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
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
}

type cachedPaths struct {
	pathMap      map[string]string // displayPath -> storedPath
	displayPaths []string          // sorted keys of pathMap
}

func newSlicePathCache(maxItems int) *slicePathCache {
	if maxItems <= 0 {
		maxItems = 64
	}
	return &slicePathCache{
		items:    make(map[string]*cachedPaths, maxItems),
		order:    make([]string, 0, maxItems),
		maxItems: maxItems,
	}
}

func (c *slicePathCache) get(key string) (*cachedPaths, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.items[key]
	return v, ok
}

func (c *slicePathCache) put(key string, v *cachedPaths) {
	if c == nil || key == "" || v == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[key]; exists {
		c.items[key] = v
		return
	}
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

	// Case 2: commit_hash specified (use root_slice)
	rootSlice, err := s.storage.GetRootSlice(ctx)
	if err != nil {
		return "", "", status.Error(codes.Internal, "root slice not found")
	}
	if commitHash != "" {
		return rootSlice.ID, commitHash, nil
	}

	// Case 3: Nothing specified (use root_slice HEAD)
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

func (s *fileServiceServer) cachedSlicePathMap(ctx context.Context, sliceID string, slice *models.Slice, resolvedCommit string) (map[string]string, []string, error) {
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

	key := sliceID + "|" + commit + "|" + sliceFolderMountKey(slice)
	if cached, ok := s.pathCache.get(key); ok && cached != nil {
		return cached.pathMap, cached.displayPaths, nil
	}

	storedPaths, err := s.effectiveSlicePaths(ctx, sliceID, slice, commit)
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

func (s *fileServiceServer) effectiveSlicePaths(ctx context.Context, sliceID string, slice *models.Slice, resolvedCommit string) ([]string, error) {
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

	// Prefer commit snapshot paths when available. This avoids listing stale
	// file IDs that may remain in legacy metadata after deletes.
	if commitHash != "" {
		if snapshot, err := s.storage.GetCommitSnapshot(ctx, commitHash); err == nil && snapshot != nil {
			paths := make([]string, 0, len(snapshot.Files))
			for raw := range snapshot.Files {
				p := common.CleanRelativePath(raw)
				if p == "" {
					continue
				}
				paths = append(paths, p)
			}
			sort.Strings(paths)
			return paths, nil
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

func (s *fileServiceServer) ListEntries(ctx context.Context, req *filev1.ListEntriesRequest) (*filev1.ListEntriesResponse, error) {
	// Resolve version from oneof
	sliceID, resolvedCommit, err := s.resolveVersion(ctx, req.GetCommitHash(), req.GetSliceVersion())
	if err != nil {
		return nil, err
	}

	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	username := auth.UsernameFromGRPCContext(ctx)
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	normalizedPath := cleanPath(req.Path)

	// Fast path for mounted slices: the root entries are the mount aliases, which we can
	// list without scanning all underlying files. For nested paths we can translate the
	// display path to the stored path and use the directory-entry tree when available.
	if slice != nil && len(slice.FolderMounts) > 0 {
		if normalizedPath == "" {
			entries := make([]*filev1.DirectoryEntry, 0, len(slice.FolderMounts))
			seen := make(map[string]struct{}, len(slice.FolderMounts))
			for _, m := range slice.FolderMounts {
				alias := cleanPath(m.Alias)
				if alias == "" {
					continue
				}
				if _, ok := seen[alias]; ok {
					continue
				}
				seen[alias] = struct{}{}
				entries = append(entries, &filev1.DirectoryEntry{
					Name:        path.Base(alias),
					Path:        alias,
					Type:        filev1.EntryType_ENTRY_TYPE_DIRECTORY,
					HasChildren: true,
				})
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			truncated := false
			if req.Limit > 0 && int(req.Limit) < len(entries) {
				entries = entries[:req.Limit]
				truncated = true
			}

			return &filev1.ListEntriesResponse{
				SliceId:   sliceID,
				Path:      "",
				Entries:   entries,
				Truncated: truncated,
			}, nil
		}

		// Enforce that requests stay under a mount alias to avoid leaking parent paths.
		underAlias := false
		for _, m := range slice.FolderMounts {
			alias := cleanPath(m.Alias)
			if alias == "" {
				continue
			}
			if normalizedPath == alias || strings.HasPrefix(normalizedPath, alias+"/") {
				underAlias = true
				break
			}
		}
		if !underAlias {
			return nil, status.Error(codes.NotFound, "path not found")
		}

		backingSliceID := sliceID
		if slice.ParentSlice != "" {
			backingSliceID = slice.ParentSlice
		}
		storedParentPath := common.SliceStoredPath(slice, normalizedPath)
		if storedParentPath == "" {
			return nil, status.Error(codes.NotFound, "path not found")
		}
		if parentEntry, err := s.storage.GetEntryByPath(ctx, backingSliceID, storedParentPath); err == nil && parentEntry != nil {
			if parentEntry.Type == "file" {
				return nil, status.Error(codes.FailedPrecondition, "path refers to a file")
			}
			children, err := s.storage.ListEntries(ctx, backingSliceID, parentEntry.ID)
			if err == nil {
				entries := make([]*filev1.DirectoryEntry, 0, len(children))
				for _, child := range children {
					if child == nil {
						continue
					}
					displayChildPath := common.SliceDisplayPath(slice, child.Path)
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
					})
				}
				sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

				truncated := false
				if req.Limit > 0 && int(req.Limit) < len(entries) {
					entries = entries[:req.Limit]
					truncated = true
				}
				return &filev1.ListEntriesResponse{
					SliceId:   sliceID,
					Path:      normalizedPath,
					Entries:   entries,
					Truncated: truncated,
				}, nil
			}
		}
		// Fall back to legacy path scanning if directory entries are not materialized.
	}

	// Fast path for materialized directory trees: list direct children via directory entries
	// instead of scanning all descendant file paths under a prefix.
	{
		backingSliceID := sliceID
		if slice.ParentSlice != "" {
			backingSliceID = slice.ParentSlice
		}

		if normalizedPath == "" {
			// Detect whether directory nodes are materialized by probing a likely top-level dir.
			treeLikely := false
			if len(slice.Files) > 0 {
				sample := common.CleanRelativePath(slice.Files[0])
				if sample != "" {
					top := strings.Split(sample, "/")[0]
					if top != "" {
						if e, err := s.storage.GetEntryByPath(ctx, backingSliceID, top); err == nil && e != nil {
							// If "top" exists as an entry, we're likely materialized enough to use tree queries.
							treeLikely = true
						}
					}
				}
			}

			if treeLikely {
				children, err := s.storage.ListEntries(ctx, backingSliceID, backingSliceID)
				if err == nil {
					entries := make([]*filev1.DirectoryEntry, 0, len(children))
					for _, child := range children {
						if child == nil {
							continue
						}
						displayChildPath := common.SliceDisplayPath(slice, child.Path)
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
						})
					}
					sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

					truncated := false
					if req.Limit > 0 && int(req.Limit) < len(entries) {
						entries = entries[:req.Limit]
						truncated = true
					}
					return &filev1.ListEntriesResponse{
						SliceId:   sliceID,
						Path:      "",
						Entries:   entries,
						Truncated: truncated,
					}, nil
				}
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
						entries := make([]*filev1.DirectoryEntry, 0, len(children))
						for _, child := range children {
							if child == nil {
								continue
							}
							displayChildPath := common.SliceDisplayPath(slice, child.Path)
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
							})
						}
						sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

						truncated := false
						if req.Limit > 0 && int(req.Limit) < len(entries) {
							entries = entries[:req.Limit]
							truncated = true
						}
						return &filev1.ListEntriesResponse{
							SliceId:   sliceID,
							Path:      normalizedPath,
							Entries:   entries,
							Truncated: truncated,
						}, nil
					}
				}
			}
		}
		// Fall back to legacy path scanning if tree nodes are missing.
	}

	pathMap, displayPaths, err := s.cachedSlicePathMap(ctx, sliceID, slice, resolvedCommit)
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
			return &filev1.ListEntriesResponse{SliceId: sliceID, Path: normalizedPath, Entries: []*filev1.DirectoryEntry{}, Truncated: false}, nil
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
	if req.Limit > 0 && int(req.Limit) < len(entries) {
		entries = entries[:req.Limit]
		truncated = true
	}

	return &filev1.ListEntriesResponse{
		SliceId:   sliceID,
		Path:      normalizedPath,
		Entries:   entries,
		Truncated: truncated,
	}, nil
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

	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	username := auth.UsernameFromGRPCContext(ctx)
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	requestPath := cleanPath(req.Path)

	// Mounted slices can translate display paths to stored paths without scanning the
	// full slice file set. Restrict requests to mount aliases to avoid leaking parent files.
	if slice != nil && len(slice.FolderMounts) > 0 {
		underAlias := false
		for _, m := range slice.FolderMounts {
			alias := cleanPath(m.Alias)
			if alias == "" {
				continue
			}
			if requestPath == alias || strings.HasPrefix(requestPath, alias+"/") {
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

		backingSliceID := sliceID
		if slice.ParentSlice != "" {
			backingSliceID = slice.ParentSlice
		}

		effectiveCommit := strings.TrimSpace(resolvedCommit)
		if slice.ParentSlice != "" && (effectiveCommit == "" || strings.HasPrefix(effectiveCommit, "init-")) {
			if meta, err := s.storage.GetSliceMetadata(ctx, slice.ParentSlice); err == nil && meta != nil {
				effectiveCommit = strings.TrimSpace(meta.HeadCommitHash)
			}
		}

		var content *models.FileContent
		if effectiveCommit != "" {
			if c, err := s.storage.GetFileAtCommit(ctx, effectiveCommit, storedPath); err == nil && c != nil {
				content = c
			}
		}
		if content == nil {
			c, err := s.storage.GetSliceFileByPath(ctx, backingSliceID, storedPath)
			if err != nil {
				return nil, status.Error(codes.NotFound, "file not found")
			}
			content = c
		}

		file := &filev1.File{
			Path:    common.SliceDisplayPath(slice, storedPath),
			Content: content.Content,
			Size:    contentSize(content),
			Hash:    content.Hash,
		}
		return &filev1.GetFileResponse{File: file}, nil
	}

	pathMap, _, mapErr := s.cachedSlicePathMap(ctx, sliceID, slice, resolvedCommit)
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

	// Prefer loading content via the resolved commit snapshot. Forked slices may
	// not have fully materialized directory entries, but commit snapshots can
	// still serve versioned content.
	var content *models.FileContent
	if strings.TrimSpace(resolvedCommit) != "" {
		if c, err := s.storage.GetFileAtCommit(ctx, resolvedCommit, storedPath); err == nil && c != nil {
			content = c
		}
	}
	if content == nil {
		c, err := s.storage.GetSliceFileByPath(ctx, sliceID, storedPath)
		if err != nil && slice.ParentSlice != "" {
			// Forked slices are views over a parent slice; their directory entries
			// may not be materialized, but the underlying blob lives in the parent.
			if parentContent, perr := s.storage.GetSliceFileByPath(ctx, slice.ParentSlice, storedPath); perr == nil && parentContent != nil {
				c = parentContent
				err = nil
			}
		}
		if err != nil {
			if sliceHasPath(pathMap, displayPath) {
				return nil, status.Error(codes.NotFound, "file content not available")
			}
			return nil, status.Error(codes.NotFound, "file not found")
		}
		content = c
	}

	file := &filev1.File{
		Path:    common.SliceDisplayPath(slice, storedPath),
		Content: content.Content,
		Size:    contentSize(content),
		Hash:    content.Hash,
	}

	return &filev1.GetFileResponse{File: file}, nil
}

func cleanPath(raw string) string {
	return common.CleanRelativePath(raw)
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
	username := auth.UsernameFromGRPCContext(ctx)
	if !authz.HasSliceViewAccess(slice, username) {
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

	changes, err := s.storage.GetFileHistory(ctx, sliceID, normalizedPath, limit+1, req.FromCommit)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get file history: %v", err))
	}
	if len(changes) == 0 {
		storedPath := common.SliceStoredPath(slice, normalizedPath)
		if storedPath != "" && storedPath != normalizedPath {
			changes, err = s.storage.GetFileHistory(ctx, sliceID, storedPath, limit+1, req.FromCommit)
			if err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get file history: %v", err))
			}
		}
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
		protoChanges = append(protoChanges, modelToProtoChange(change, ""))
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
	username := auth.UsernameFromGRPCContext(ctx)
	if !authz.HasSliceViewAccess(slice, username) {
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

	changes, err := s.storage.GetDirectoryHistory(ctx, sliceID, normalizedPath, limit+1, req.FromCommit)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get directory history: %v", err))
	}
	if len(changes) == 0 {
		storedPath := common.SliceStoredPath(slice, normalizedPath)
		if storedPath != normalizedPath {
			changes, err = s.storage.GetDirectoryHistory(ctx, sliceID, storedPath, limit+1, req.FromCommit)
			if err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get directory history: %v", err))
			}
		}
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
		protoChanges = append(protoChanges, modelToProtoChange(change, ""))
	}

	// Get summary
	summary, err := s.storage.GetDirectorySummary(ctx, sliceID, normalizedPath)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get directory summary: %v", err))
	}

	return &filev1.GetDirectoryHistoryResponse{
		Changes:    protoChanges,
		HasMore:    hasMore,
		NextCommit: nextCommit,
		Summary:    modelToProtoSummary(summary),
	}, nil
}

// GetCommitChanges retrieves all file changes made in a specific commit.
func (s *fileServiceServer) GetCommitChanges(ctx context.Context, req *filev1.GetCommitChangesRequest) (*filev1.GetCommitChangesResponse, error) {
	if req.CommitHash == "" {
		return nil, status.Error(codes.InvalidArgument, "commit_hash is required")
	}

	changes, err := s.storage.GetCommitChanges(ctx, req.CommitHash)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get commit changes: %v", err))
	}

	protoChanges := make([]*filev1.FileChangeRecord, 0, len(changes))
	parentHashes := make(map[string]string)
	var added, modified, deleted, renamed int32
	for _, change := range changes {
		parentHash := ""
		if change != nil {
			key := change.SliceID + "\x00" + change.CommitHash
			if cached, ok := parentHashes[key]; ok {
				parentHash = cached
			} else {
				resolvedParent, err := s.findParentCommitHash(ctx, change.SliceID, change.CommitHash)
				if err == nil {
					parentHash = resolvedParent
				}
				parentHashes[key] = parentHash
			}
		}

		patch := s.buildChangePatch(ctx, change, parentHash)
		protoChanges = append(protoChanges, modelToProtoChange(change, patch))
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

	return &filev1.GetCommitChangesResponse{
		CommitHash:    req.CommitHash,
		Changes:       protoChanges,
		FilesAdded:    added,
		FilesModified: modified,
		FilesDeleted:  deleted,
		FilesRenamed:  renamed,
	}, nil
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

func (s *fileServiceServer) buildChangePatch(ctx context.Context, change *models.FileChangeRecord, parentHash string) string {
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
		if parentHash != "" {
			if prev, ferr := s.storage.GetFileAtCommit(ctx, parentHash, oldPath); ferr == nil && prev != nil {
				if lines, ok := splitLinesForDiff(prev.Content); ok {
					beforeLines = lines
				} else {
					beforeUndiffable = true
				}
			}
		}
	}

	shouldLoadAfter := change.NewHash != "" || change.ChangeType == models.ChangeTypeAdd || change.ChangeType == models.ChangeTypeModify || change.ChangeType == models.ChangeTypeRename
	afterUndiffable := false
	if shouldLoadAfter {
		if curr, err := s.storage.GetFileAtCommit(ctx, change.CommitHash, newPath); err == nil && curr != nil {
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

func (s *fileServiceServer) findParentCommitHash(ctx context.Context, sliceID, commitHash string) (string, error) {
	if sliceID == "" || commitHash == "" {
		return "", nil
	}

	commits, err := s.storage.ListSliceCommits(ctx, sliceID, 0, "")
	if err != nil {
		return "", err
	}
	for _, c := range commits {
		if c.CommitHash == commitHash {
			return c.ParentHash, nil
		}
	}
	return "", nil
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
