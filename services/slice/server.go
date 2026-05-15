package sliceservice

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"path"
	"runtime"
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
	"github.com/niczy/gitslice/internal/rootpromote"
	"github.com/niczy/gitslice/internal/searchindex"
	"github.com/niczy/gitslice/internal/sliceconfig"
	"github.com/niczy/gitslice/internal/storage"
	commonv1 "github.com/niczy/gitslice/proto/common"
	filev1 "github.com/niczy/gitslice/proto/file"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	ciservice "github.com/niczy/gitslice/services/ci"
	"github.com/pmezard/go-difflib/difflib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type sliceServiceServer struct {
	slicev1.UnimplementedSliceServiceServer
	storage               storage.Storage
	promotionStorage      storage.Storage
	rootSliceMu           sync.RWMutex
	rootSliceID           string
	promotionQueueMu      sync.Mutex
	promotionQueue        *rootpromote.Queue
	promotionBatchWindow  time.Duration
	promotionBatchMaxSize int
	promotionWorkerCount  int
	historyProjectionWG   sync.WaitGroup
	durablePromotion      bool
}

func (s *sliceServiceServer) requireUsername(ctx context.Context) (string, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.storage)
	if err != nil {
		return "", err
	}
	return identity.Username, nil
}

func (s *sliceServiceServer) optionalUsername(ctx context.Context) (string, error) {
	identity, err := authresolver.OptionalGRPCIdentity(ctx, s.storage)
	if err != nil {
		return "", err
	}
	if identity == nil {
		return "", nil
	}
	return identity.Username, nil
}

func (s *sliceServiceServer) hasSliceViewAccess(ctx context.Context, slice *models.Slice, username string) bool {
	ok, err := authz.CanViewSlice(ctx, s.storage, slice, username)
	if err != nil {
		log.Printf("slice: failed to resolve slice access for %s: %v", strings.TrimSpace(username), err)
		return false
	}
	return ok
}

func (s *sliceServiceServer) loadAuthorizedChangeset(ctx context.Context, changesetID, username string) (*models.Changeset, *models.Slice, error) {
	cs, err := s.storage.GetChangeset(ctx, changesetID)
	if err != nil {
		return nil, nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", changesetID))
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", cs.SliceID))
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		return nil, nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}
	return cs, slice, nil
}

const (
	defaultPromotionBatchWindow  = 50 * time.Millisecond
	defaultPromotionBatchMaxSize = 512
	defaultPromotionWorkerCount  = 2
	revertChangesetHashPrefix    = common.ChangesetVersionIDPrefix + "revert~"
	revertAllChangesToken        = "*"
	checkoutManifestChunkSize    = 256
	maxReviewPatchableChanges    = 100
	maxChangesetListReviewPaths  = 500
	agentArtifactLinkLimit       = 20
)

func newSliceServiceServer(st storage.Storage) *sliceServiceServer {
	return newSliceServiceServerWithPromotionStorage(st, st)
}

func newSliceServiceServerWithPromotionStorage(st storage.Storage, promotionSt storage.Storage) *sliceServiceServer {
	if promotionSt == nil {
		promotionSt = st
	}
	return &sliceServiceServer{
		storage:               st,
		promotionStorage:      promotionSt,
		promotionBatchWindow:  defaultPromotionBatchWindow,
		promotionBatchMaxSize: defaultPromotionBatchMaxSize,
	}
}

func (s *sliceServiceServer) promotionStore() storage.Storage {
	if s.promotionStorage != nil {
		return s.promotionStorage
	}
	return s.storage
}

// RegisterGRPCServer registers the slice service handlers on an existing gRPC server.
func RegisterGRPCServer(srv *grpc.Server, st storage.Storage) {
	slicev1.RegisterSliceServiceServer(srv, newSliceServiceServer(st))
}

// RegisterGRPCServerWithPromotionStorage registers the slice service with a
// separate storage backend for async promotion workers.
func RegisterGRPCServerWithPromotionStorage(srv *grpc.Server, st storage.Storage, promotionSt storage.Storage) {
	slicev1.RegisterSliceServiceServer(srv, newSliceServiceServerWithPromotionStorage(st, promotionSt))
}

// RegisterGRPCServerWithPromotionStorageAndDurablePromotion registers the slice
// service and optionally starts merge-event-backed promotion workers.
func RegisterGRPCServerWithPromotionStorageAndDurablePromotion(srv *grpc.Server, st storage.Storage, promotionSt storage.Storage, cfg DurablePromotionConfig) {
	service := newSliceServiceServerWithPromotionStorage(st, promotionSt)
	service.StartDurablePromotionWorkers(context.Background(), cfg)
	slicev1.RegisterSliceServiceServer(srv, service)
}

// NewGRPCServer constructs a gRPC server for the slice service using the provided storage backend.
func NewGRPCServer(st storage.Storage) *grpc.Server {
	srv := grpc.NewServer()
	RegisterGRPCServer(srv, st)
	return srv
}

// NewService constructs the slice service implementation for use without gRPC.
func NewService(st storage.Storage) slicev1.SliceServiceServer {
	return newSliceServiceServer(st)
}

// NewInternalService constructs the concrete slice service for cross-package reuse.
func NewInternalService(st storage.Storage) *sliceServiceServer {
	return newSliceServiceServer(st)
}

// NewInternalServiceWithPromotionStorage constructs the concrete slice service
// with a separate storage backend for async promotion workers.
func NewInternalServiceWithPromotionStorage(st storage.Storage, promotionSt storage.Storage) *sliceServiceServer {
	return newSliceServiceServerWithPromotionStorage(st, promotionSt)
}

func modelVisibilityToProto(v models.Visibility) commonv1.Visibility {
	switch models.NormalizeVisibility(v) {
	case models.VisibilityPublic:
		return commonv1.Visibility_VISIBILITY_PUBLIC
	default:
		return commonv1.Visibility_VISIBILITY_PRIVATE
	}
}

func protoVisibilityToModel(v commonv1.Visibility) (models.Visibility, error) {
	switch v {
	case commonv1.Visibility_VISIBILITY_PRIVATE:
		return models.VisibilityPrivate, nil
	case commonv1.Visibility_VISIBILITY_PUBLIC:
		return models.VisibilityPublic, nil
	default:
		return models.VisibilityPrivate, status.Error(codes.InvalidArgument, "visibility must be public or private")
	}
}

func canManageSliceVisibility(slice *models.Slice, username string) bool {
	if slice == nil || strings.TrimSpace(username) == "" {
		return false
	}
	if slice.CreatedBy == username {
		return true
	}
	for _, owner := range slice.Owners {
		if owner == username {
			return true
		}
	}
	return false
}

func externalSliceSlug(slice *models.Slice) string {
	if slug, ok := homeslice.ExternalSlugForSlice(slice); ok {
		return slug
	}
	return storage.QualifiedSliceSlug(slice)
}

func (s *sliceServiceServer) canReadSliceInfo(ctx context.Context, slice *models.Slice, username string) bool {
	if s.hasSliceViewAccess(ctx, slice, username) {
		return true
	}
	return slice != nil && !slice.IsRoot && slice.Visibility.IsPublic()
}

func (s *sliceServiceServer) authorizeSliceRead(ctx context.Context, slice *models.Slice) (string, error) {
	username, err := s.optionalUsername(ctx)
	if err != nil {
		return "", err
	}
	if !s.canReadSliceInfo(ctx, slice, username) {
		return "", sliceReadAccessError(username)
	}
	return username, nil
}

func (s *sliceServiceServer) loadReadableChangeset(ctx context.Context, changesetID string) (*models.Changeset, *models.Slice, string, error) {
	cs, err := s.storage.GetChangeset(ctx, changesetID)
	if err != nil {
		return nil, nil, "", status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", changesetID))
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, nil, "", status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", cs.SliceID))
	}
	username, err := s.authorizeSliceRead(ctx, slice)
	if err != nil {
		return nil, nil, "", err
	}
	return cs, slice, username, nil
}

func sliceReadAccessError(username string) error {
	if strings.TrimSpace(username) == "" {
		return status.Error(codes.Unauthenticated, "login required")
	}
	return status.Error(codes.PermissionDenied, "not authorized for slice")
}

func (s *sliceServiceServer) loadReadableSliceByRef(ctx context.Context, ref string) (*models.Slice, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, status.Error(codes.InvalidArgument, "slice ref cannot be empty")
	}
	slice, err := s.resolveSliceByRef(ctx, ref)
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", ref))
		}
		if errors.Is(err, storage.ErrInvalidInput) {
			return nil, status.Error(codes.InvalidArgument, "slice ref cannot be empty")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to look up slice: %v", err))
	}

	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}
	if !s.canReadSliceInfo(ctx, slice, username) {
		return nil, sliceReadAccessError(username)
	}
	return slice, nil
}

func (s *sliceServiceServer) resolveSliceByRef(ctx context.Context, ref string) (*models.Slice, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, storage.ErrInvalidInput
	}
	if owner, slug, ok := storage.SplitQualifiedSliceRef(ref); ok {
		return s.storage.GetSliceByOwnerAndSlug(ctx, owner, slug)
	}
	if slice, err := s.storage.GetSlice(ctx, ref); err == nil {
		return slice, nil
	} else if !errors.Is(err, storage.ErrSliceNotFound) {
		return nil, err
	}
	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}
	if username != "" {
		slice, err := s.storage.GetSliceByOwnerAndSlug(ctx, username, ref)
		if err == nil {
			return slice, nil
		}
		if !errors.Is(err, storage.ErrSliceNotFound) {
			return nil, err
		}
	}
	return s.storage.GetSliceBySlug(ctx, ref)
}

// RunGenesisInit populates the root slice from the git repository by creating
// and merging a changeset through the service's own RPC methods.
func RunGenesisInit(ctx context.Context, st storage.Storage) error {
	svc := newSliceServiceServer(st)
	return svc.PopulateGenesisFromGit(ctx)
}

func (s *sliceServiceServer) collectSliceEntries(ctx context.Context, sliceID string) ([]*models.DirectoryEntry, error) {
	rootChildren, err := s.storage.ListEntries(ctx, sliceID, sliceID)
	if err != nil {
		return nil, err
	}

	result := make([]*models.DirectoryEntry, 0, len(rootChildren))
	queue := append([]*models.DirectoryEntry(nil), rootChildren...)
	for len(queue) > 0 {
		entry := queue[0]
		queue = queue[1:]
		if entry == nil {
			continue
		}
		result = append(result, entry)
		if entry.Type != "directory" {
			continue
		}
		children, err := s.storage.ListEntries(ctx, sliceID, entry.ID)
		if err != nil {
			return nil, err
		}
		queue = append(queue, children...)
	}
	return result, nil
}

func (s *sliceServiceServer) collectSliceEntriesForRequestedFolders(ctx context.Context, parentSlice *models.Slice, folderPaths []string, username string) ([]*models.DirectoryEntry, bool, error) {
	if parentSlice == nil {
		return nil, false, nil
	}
	prefixes := candidateFolderEntryPrefixes(parentSlice, folderPaths, username)
	if len(prefixes) > 0 {
		entries, ok, err := listEntriesByPathPrefixes(ctx, s.storage, parentSlice.ID, prefixes)
		if ok || err != nil {
			return entries, ok, err
		}
	}
	entries, err := s.collectSliceEntries(ctx, parentSlice.ID)
	return entries, false, err
}

func (s *sliceServiceServer) resolveBackingSliceID(ctx context.Context, sliceID string, slice *models.Slice) (string, error) {
	backingSliceID := strings.TrimSpace(sliceID)
	if slice != nil && strings.TrimSpace(slice.ParentSlice) != "" {
		backingSliceID = strings.TrimSpace(slice.ParentSlice)
	}
	if slice == nil || strings.TrimSpace(slice.ParentSlice) == "" || len(slice.FolderMounts) == 0 {
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

func (s *sliceServiceServer) collectSliceCheckoutEntries(ctx context.Context, sliceID string, slice *models.Slice) ([]*models.DirectoryEntry, error) {
	if sliceUsesUnsupportedParentShape(slice) {
		return nil, nil
	}
	if slice == nil || strings.TrimSpace(slice.ParentSlice) == "" || len(slice.FolderMounts) == 0 {
		entries, err := s.collectSliceEntries(ctx, sliceID)
		if err != nil {
			return nil, err
		}
		return filterSliceEntriesForTrackedRoots(slice, entries), nil
	}
	backingSliceID, err := s.resolveBackingSliceID(ctx, sliceID, slice)
	if err != nil {
		return nil, err
	}
	entries, err := s.collectMountedBackingEntries(ctx, backingSliceID, slice.FolderMounts)
	if err != nil {
		return nil, err
	}
	return filterSliceEntriesForTrackedRoots(slice, entries), nil
}

func filterSliceEntriesForTrackedRoots(slice *models.Slice, entries []*models.DirectoryEntry) []*models.DirectoryEntry {
	if sliceUsesUnsupportedParentShape(slice) {
		return nil
	}
	roots := sliceTrackedStorageRoots(slice)
	if len(roots) == 0 || len(entries) == 0 {
		return entries
	}
	filtered := make([]*models.DirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		if sliceEntryWithinTrackedRoots(entry, roots) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func sliceEntryWithinTrackedRoots(entry *models.DirectoryEntry, roots []string) bool {
	if entry == nil {
		return false
	}
	cleaned := common.CleanRelativePath(entry.Path)
	if cleaned == "" {
		return false
	}
	for _, root := range roots {
		if cleaned == root {
			return entry.Type == "directory"
		}
		if strings.HasPrefix(cleaned, root+"/") {
			return true
		}
	}
	return false
}

func filterSliceFilesForTrackedRoots(slice *models.Slice, files []string) []string {
	if sliceUsesUnsupportedParentShape(slice) {
		return nil
	}
	roots := sliceTrackedStorageRoots(slice)
	if len(roots) == 0 || len(files) == 0 {
		return files
	}
	filtered := make([]string, 0, len(files))
	for _, filePath := range files {
		if pathUnderTrackedStorageRoot(filePath, roots) {
			filtered = append(filtered, filePath)
		}
	}
	return normalizeModifiedFiles(filtered)
}

func sliceTrackedStorageRoots(slice *models.Slice) []string {
	if slice == nil {
		return nil
	}
	roots := make([]string, 0, len(slice.FolderMounts)*2+1)
	if username := homeslice.UsernameFromSliceID(slice.ID); username != "" {
		roots = append(roots, homeslice.RelativeRootPath(username))
	}
	for _, mount := range slice.FolderMounts {
		roots = append(roots, mount.SourcePath)
		if mount.Alias != mount.SourcePath {
			roots = append(roots, mount.Alias)
		}
	}
	return normalizeChangesetAllowedAddRoots(roots)
}

func sliceUsesUnsupportedParentShape(slice *models.Slice) bool {
	if slice == nil || slice.IsRoot || homeslice.IsHomeSliceID(slice.ID) {
		return false
	}
	return strings.TrimSpace(slice.ParentSlice) != "" && len(slice.FolderMounts) == 0
}

func pathUnderTrackedStorageRoot(pathValue string, roots []string) bool {
	cleaned := common.CleanRelativePath(pathValue)
	if cleaned == "" {
		return false
	}
	for _, root := range roots {
		if cleaned != root && strings.HasPrefix(cleaned, root+"/") {
			return true
		}
	}
	return false
}

func (s *sliceServiceServer) collectMountedBackingEntries(ctx context.Context, backingSliceID string, mounts []models.SliceFolderMount) ([]*models.DirectoryEntry, error) {
	backingSliceID = strings.TrimSpace(backingSliceID)
	if backingSliceID == "" || len(mounts) == 0 {
		return nil, nil
	}

	result := make([]*models.DirectoryEntry, 0)
	seen := make(map[string]struct{})
	for _, mount := range mounts {
		sourcePath := common.CleanRelativePath(mount.SourcePath)
		if sourcePath == "" {
			continue
		}
		rootEntry, err := s.storage.GetEntryByPath(ctx, backingSliceID, sourcePath)
		if err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				fallbackEntries, fallbackErr := s.collectMountedBackingFileEntriesFromSlice(ctx, backingSliceID, sourcePath)
				if fallbackErr != nil {
					return nil, fallbackErr
				}
				for _, entry := range fallbackEntries {
					if entry == nil {
						continue
					}
					key := entry.ID + "\x00" + entry.Path
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					result = append(result, entry)
				}
				continue
			}
			return nil, err
		}

		filesBefore := countFileEntries(result)
		queue := []*models.DirectoryEntry{rootEntry}
		for len(queue) > 0 {
			entry := queue[0]
			queue = queue[1:]
			if entry == nil {
				continue
			}
			key := entry.ID + "\x00" + entry.Path
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, entry)
			if entry.Type != "directory" {
				continue
			}
			children, err := s.storage.ListEntries(ctx, backingSliceID, entry.ID)
			if err != nil {
				return nil, err
			}
			queue = append(queue, children...)
		}
		if countFileEntries(result) == filesBefore {
			fallbackEntries, fallbackErr := s.collectMountedBackingFileEntriesFromSlice(ctx, backingSliceID, sourcePath)
			if fallbackErr != nil {
				return nil, fallbackErr
			}
			for _, entry := range fallbackEntries {
				if entry == nil {
					continue
				}
				key := entry.ID + "\x00" + entry.Path
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				result = append(result, entry)
			}
		}
	}
	return result, nil
}

func countFileEntries(entries []*models.DirectoryEntry) int {
	count := 0
	for _, entry := range entries {
		if entry != nil && entry.Type == "file" {
			count++
		}
	}
	return count
}

func (s *sliceServiceServer) collectMountedBackingFileEntriesFromSlice(ctx context.Context, backingSliceID, sourcePath string) ([]*models.DirectoryEntry, error) {
	backingSlice, err := s.storage.GetSlice(ctx, backingSliceID)
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil, nil
		}
		return nil, err
	}

	prefix := common.CleanRelativePath(sourcePath) + "/"
	result := make([]*models.DirectoryEntry, 0)
	for _, rawPath := range backingSlice.Files {
		filePath := common.CleanRelativePath(rawPath)
		if filePath == "" || !strings.HasPrefix(filePath, prefix) {
			continue
		}
		entry, entryErr := s.storage.GetEntryByPath(ctx, backingSliceID, filePath)
		if entryErr == nil && entry != nil {
			result = append(result, entry)
			continue
		}
		if entryErr != nil && !errors.Is(entryErr, storage.ErrEntryNotFound) {
			return nil, entryErr
		}
		result = append(result, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(backingSliceID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: backingSliceID,
		})
	}
	return result, nil
}

func (s *sliceServiceServer) resolveCheckoutEffectiveCommit(ctx context.Context, primarySliceID, resolvedCommit string) string {
	effectiveCommit := strings.TrimSpace(resolvedCommit)
	if strings.TrimSpace(primarySliceID) != "" && (effectiveCommit == "" || common.IsInitialCommitID(effectiveCommit)) {
		if meta, err := s.storage.GetSliceMetadata(ctx, primarySliceID); err == nil && meta != nil {
			effectiveCommit = strings.TrimSpace(meta.HeadCommitHash)
		}
	}
	return effectiveCommit
}

func (s *sliceServiceServer) resolveCheckoutFileMetadata(
	ctx context.Context,
	slice *models.Slice,
	sliceID string,
	entry *models.DirectoryEntry,
	snapshotHash string,
	loadBlocks bool,
) (hash string, size int64, manifestBlocks []models.Block, executable bool, symlinkTarget string, err error) {
	if strings.TrimSpace(sliceID) == "" || entry == nil || strings.TrimSpace(entry.Path) == "" {
		return "", 0, nil, false, "", storage.ErrEntryNotFound
	}

	storedPath := strings.TrimSpace(entry.Path)
	hash = strings.TrimSpace(snapshotHash)
	if hash == "" {
		hash = strings.TrimSpace(entry.Hash)
	}

	size = entry.Size
	executable = entry.Executable
	symlinkTarget = entry.SymlinkTarget

	if !loadBlocks {
		return strings.TrimSpace(hash), size, nil, executable, symlinkTarget, nil
	}

	if hash != "" {
		versionedManifest, manifestErr := s.storage.GetVersionedFileManifest(ctx, hash)
		if manifestErr == nil && versionedManifest != nil {
			if size == 0 {
				size = versionedManifest.TotalSize
			}
			manifestBlocks = append(manifestBlocks, versionedManifest.Blocks...)
			if !executable {
				executable = versionedManifest.Executable
			}
			if symlinkTarget == "" {
				symlinkTarget = versionedManifest.SymlinkTarget
			}
			return strings.TrimSpace(hash), size, manifestBlocks, executable, symlinkTarget, nil
		}
		if manifestErr != nil && !errors.Is(manifestErr, storage.ErrEntryNotFound) {
			return "", 0, nil, false, "", manifestErr
		}
	}

	resolveFromSliceManifest := func(targetSliceID string) error {
		if strings.TrimSpace(targetSliceID) == "" {
			return nil
		}
		manifest, manifestErr := s.storage.GetFileManifest(ctx, targetSliceID, storedPath)
		if manifestErr != nil {
			if errors.Is(manifestErr, storage.ErrEntryNotFound) {
				return nil
			}
			return manifestErr
		}
		if manifest == nil {
			return nil
		}
		if size == 0 {
			size = manifest.TotalSize
		}
		if hash == "" {
			hash = strings.TrimSpace(manifest.Hash)
		}
		if len(manifestBlocks) == 0 {
			manifestBlocks = append(manifestBlocks, manifest.Blocks...)
		}
		if !executable {
			executable = manifest.Executable
		}
		if symlinkTarget == "" {
			symlinkTarget = manifest.SymlinkTarget
		}
		return nil
	}

	if err := resolveFromSliceManifest(sliceID); err != nil {
		return "", 0, nil, false, "", err
	}

	if (size == 0 || hash == "" || symlinkTarget == "") && slice != nil && slice.ParentSlice != "" && slice.ParentSlice != sliceID {
		parentEntry, entryErr := s.storage.GetEntryByPath(ctx, slice.ParentSlice, storedPath)
		if entryErr == nil && parentEntry != nil {
			if size == 0 {
				size = parentEntry.Size
			}
			if hash == "" {
				hash = strings.TrimSpace(parentEntry.Hash)
			}
			if !executable {
				executable = parentEntry.Executable
			}
			if symlinkTarget == "" {
				symlinkTarget = parentEntry.SymlinkTarget
			}
		} else if entryErr != nil && !errors.Is(entryErr, storage.ErrEntryNotFound) {
			return "", 0, nil, false, "", entryErr
		}
	}
	if len(manifestBlocks) == 0 && slice != nil && slice.ParentSlice != "" && slice.ParentSlice != sliceID {
		if err := resolveFromSliceManifest(slice.ParentSlice); err != nil {
			return "", 0, nil, false, "", err
		}
	}

	return strings.TrimSpace(hash), size, manifestBlocks, executable, symlinkTarget, nil
}

func (s *sliceServiceServer) resolveCheckoutFileContent(
	ctx context.Context,
	slice *models.Slice,
	sliceID, storedPath, resolvedCommit string,
) (*models.FileContent, error) {
	if strings.TrimSpace(sliceID) == "" || strings.TrimSpace(storedPath) == "" {
		return nil, storage.ErrEntryNotFound
	}

	effectiveCommit := s.resolveCheckoutEffectiveCommit(ctx, sliceID, resolvedCommit)
	if effectiveCommit != "" {
		if content, err := s.storage.GetFileAtCommit(ctx, effectiveCommit, storedPath); err == nil && content != nil {
			return content, nil
		}
	}

	content, err := storage.ReadSliceFileContent(ctx, s.storage, sliceID, storedPath)
	if err == nil && content != nil {
		return content, nil
	}
	if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
		return nil, err
	}

	if slice != nil && slice.ParentSlice != "" && slice.ParentSlice != sliceID {
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

func (s *sliceServiceServer) prepareCheckout(
	ctx context.Context,
	req *slicev1.CheckoutRequest,
) (*models.SliceMetadata, *models.Slice, string, string, []*slicev1.FileMetadata, map[string]struct{}, error) {
	log.Printf("CheckoutSlice called: slice_id=%s, commit_hash=%s", req.SliceId, req.CommitHash)

	metadata, slice, effectiveCommit, err := s.resolveCheckoutTarget(ctx, req.GetSliceId(), req.GetCommitHash())
	if err != nil {
		return nil, nil, "", "", nil, nil, err
	}
	resolvedCommit := strings.TrimSpace(req.GetCommitHash())
	if resolvedCommit == "" || strings.EqualFold(resolvedCommit, "HEAD") {
		resolvedCommit = strings.TrimSpace(metadata.HeadCommitHash)
	}
	backingSliceID := req.SliceId
	mountedHeadCheckout := false
	if slice != nil && strings.TrimSpace(slice.ParentSlice) != "" && len(slice.FolderMounts) > 0 {
		backingSliceID, err = s.resolveBackingSliceID(ctx, req.SliceId, slice)
		if err != nil {
			return nil, nil, "", "", nil, nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve backing slice: %v", err))
		}
		requestedCommit := strings.TrimSpace(req.GetCommitHash())
		mountedHeadCheckout = requestedCommit == "" || strings.EqualFold(requestedCommit, "HEAD")
	}

	snapshotFiles := map[string]string(nil)
	if effectiveCommit != "" && !mountedHeadCheckout {
		snapshot, snapshotErr := s.storage.GetCommitSnapshot(ctx, effectiveCommit)
		if snapshotErr != nil && !errors.Is(snapshotErr, storage.ErrCommitNotFound) {
			return nil, nil, "", "", nil, nil, status.Error(codes.Internal, fmt.Sprintf("failed to load checkout snapshot for %s: %v", effectiveCommit, snapshotErr))
		}
		if snapshot != nil {
			snapshotFiles = snapshot.Files
		}
	}

	entries, err := s.collectSliceCheckoutEntries(ctx, req.SliceId, slice)
	if err != nil {
		return nil, nil, "", "", nil, nil, status.Error(codes.Internal, fmt.Sprintf("failed to list slice entries: %v", err))
	}

	knownHashes := make(map[string]struct{}, len(req.GetKnownHashes()))
	for _, hash := range req.GetKnownHashes() {
		hash = strings.TrimSpace(hash)
		if hash == "" {
			continue
		}
		knownHashes[hash] = struct{}{}
	}

	fileEntries := make([]*models.DirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Type != "file" {
			continue
		}
		fileEntries = append(fileEntries, entry)
	}

	fileMetadata := make([]*slicev1.FileMetadata, len(fileEntries))
	var firstErr error
	var firstErrMu sync.Mutex
	workerCount := checkoutMetadataWorkerCount(len(fileEntries))
	if workerCount > 0 {
		jobCh := make(chan int)
		var wg sync.WaitGroup
		for i := 0; i < workerCount; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range jobCh {
					if ctx.Err() != nil {
						return
					}
					entry := fileEntries[idx]
					storedPath := strings.TrimSpace(entry.Path)
					resolvedHash := strings.TrimSpace(entry.Hash)
					if snapshotFiles != nil {
						if snapshotHash := strings.TrimSpace(snapshotFiles[storedPath]); snapshotHash != "" {
							resolvedHash = snapshotHash
						}
					}
					loadBlocks := true
					if resolvedHash != "" {
						if _, ok := knownHashes[resolvedHash]; ok {
							loadBlocks = false
						}
					}
					hash, size, manifestBlocks, executable, symlinkTarget, metaErr := s.resolveCheckoutFileMetadata(ctx, slice, backingSliceID, entry, resolvedHash, loadBlocks)
					if metaErr != nil && !errors.Is(metaErr, storage.ErrEntryNotFound) {
						firstErrMu.Lock()
						if firstErr == nil {
							firstErr = status.Error(codes.Internal, fmt.Sprintf("failed to load checkout metadata for %s: %v", storedPath, metaErr))
						}
						firstErrMu.Unlock()
						return
					}
					fileMetadata[idx] = &slicev1.FileMetadata{
						FileId:        storedPath,
						Path:          common.SliceDisplayPath(slice, storedPath),
						Size:          size,
						Hash:          hash,
						ContentUrl:    "",
						Blocks:        checkoutProtoBlocks(manifestBlocks),
						Executable:    executable,
						SymlinkTarget: symlinkTarget,
					}
				}
			}()
		}
		for idx := range fileEntries {
			firstErrMu.Lock()
			hasErr := firstErr != nil
			firstErrMu.Unlock()
			if hasErr {
				break
			}
			jobCh <- idx
		}
		close(jobCh)
		wg.Wait()
	}
	firstErrMu.Lock()
	if firstErr != nil {
		defer firstErrMu.Unlock()
		return nil, nil, "", "", nil, nil, firstErr
	}
	firstErrMu.Unlock()
	sort.Slice(fileMetadata, func(i, j int) bool {
		return fileMetadata[i].GetPath() < fileMetadata[j].GetPath()
	})

	if len(fileMetadata) == 0 {
		for _, fileID := range filterSliceFilesForTrackedRoots(slice, slice.Files) {
			fileMetadata = append(fileMetadata, &slicev1.FileMetadata{
				FileId:     fileID,
				Path:       common.SliceDisplayPath(slice, fileID),
				Size:       0,
				Hash:       "",
				ContentUrl: "",
			})
		}
	}
	fileMetadata, err = s.filterCheckoutMissingDirectContent(ctx, req.GetSliceId(), slice, backingSliceID, resolvedCommit, fileMetadata, knownHashes)
	if err != nil {
		return nil, nil, "", "", nil, nil, err
	}

	return metadata, slice, resolvedCommit, backingSliceID, fileMetadata, knownHashes, nil
}

func (s *sliceServiceServer) filterCheckoutMissingDirectContent(
	ctx context.Context,
	sliceID string,
	slice *models.Slice,
	backingSliceID string,
	resolvedCommit string,
	fileMetadata []*slicev1.FileMetadata,
	knownHashes map[string]struct{},
) ([]*slicev1.FileMetadata, error) {
	if len(fileMetadata) == 0 {
		return fileMetadata, nil
	}
	filtered := make([]*slicev1.FileMetadata, 0, len(fileMetadata))
	for _, meta := range fileMetadata {
		if meta == nil {
			continue
		}
		if len(meta.GetBlocks()) > 0 {
			filtered = append(filtered, meta)
			continue
		}
		if _, ok := knownHashes[strings.TrimSpace(meta.GetHash())]; ok && strings.TrimSpace(meta.GetHash()) != "" {
			filtered = append(filtered, meta)
			continue
		}
		if _, err := s.resolveCheckoutFileContent(ctx, slice, backingSliceID, meta.GetFileId(), resolvedCommit); err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				log.Printf(
					"Warning: skipping checkout file with missing content: slice_id=%s backing_slice_id=%s path=%s hash=%s",
					strings.TrimSpace(sliceID),
					strings.TrimSpace(backingSliceID),
					meta.GetFileId(),
					meta.GetHash(),
				)
				continue
			}
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to validate checkout content for %s: %v", meta.GetFileId(), err))
		}
		filtered = append(filtered, meta)
	}
	return filtered, nil
}

func (s *sliceServiceServer) resolveCheckoutTarget(
	ctx context.Context,
	sliceID string,
	commitHash string,
) (*models.SliceMetadata, *models.Slice, string, error) {
	if err := common.ValidateSliceID(sliceID); err != nil {
		return nil, nil, "", status.Error(codes.InvalidArgument, fmt.Sprintf("invalid slice ID: %v", err))
	}

	metadata, err := s.storage.GetSliceMetadata(ctx, sliceID)
	if err != nil {
		return nil, nil, "", status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	resolvedCommit := strings.TrimSpace(commitHash)
	if resolvedCommit == "" || strings.EqualFold(resolvedCommit, "HEAD") {
		resolvedCommit = strings.TrimSpace(metadata.HeadCommitHash)
	}

	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, nil, "", status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		if username == "" {
			return nil, nil, "", status.Error(codes.Unauthenticated, "login required")
		}
		return nil, nil, "", status.Error(codes.PermissionDenied, "not authorized for slice")
	}
	effectiveCommit := s.resolveCheckoutEffectiveCommit(ctx, sliceID, resolvedCommit)
	return metadata, slice, effectiveCommit, nil
}

func checkoutMetadataWorkerCount(jobs int) int {
	if jobs <= 0 {
		return 0
	}
	workers := runtime.GOMAXPROCS(0) * 4
	if workers < 4 {
		workers = 4
	}
	if workers > 32 {
		workers = 32
	}
	if jobs < workers {
		return jobs
	}
	return workers
}

func collectMissingCheckoutBlockHashes(fileMetadata []*slicev1.FileMetadata, knownHashes map[string]struct{}) []string {
	if len(fileMetadata) == 0 {
		return nil
	}

	hashes := make([]string, 0)
	seen := make(map[string]struct{})
	for _, meta := range fileMetadata {
		if meta == nil {
			continue
		}
		if meta.GetHash() != "" {
			if _, ok := knownHashes[meta.GetHash()]; ok {
				continue
			}
		}
		for _, block := range meta.GetBlocks() {
			if block == nil {
				continue
			}
			blockHash := strings.TrimSpace(block.GetHash())
			if blockHash == "" {
				continue
			}
			if _, ok := knownHashes[blockHash]; ok {
				continue
			}
			if _, ok := seen[blockHash]; ok {
				continue
			}
			seen[blockHash] = struct{}{}
			hashes = append(hashes, blockHash)
		}
	}
	return hashes
}

func (s *sliceServiceServer) CheckoutSlice(ctx context.Context, req *slicev1.CheckoutRequest) (*slicev1.CheckoutResponse, error) {
	profile := newCheckoutProfile("unary", req.GetSliceId(), req.GetCommitHash(), len(req.GetKnownHashes()))
	var err error
	defer func() {
		profile.logResult(err)
	}()

	prepareStartedAt := time.Now()
	metadata, slice, resolvedCommit, backingSliceID, fileMetadata, knownHashes, err := s.prepareCheckout(ctx, req)
	if err != nil {
		return nil, err
	}
	profile.markPrepared(len(fileMetadata), time.Since(prepareStartedAt))

	// Convert file contents to proto format.
	payloadStartedAt := time.Now()
	var fileContents []*slicev1.FileContent
	var blockContents []*slicev1.BlockContent
	blockHashes := collectMissingCheckoutBlockHashes(fileMetadata, knownHashes)
	if len(blockHashes) > 0 {
		blockPayloads, err := s.storage.GetBlocks(ctx, blockHashes)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load checkout blocks: %v", err))
		}
		for _, blockHash := range blockHashes {
			payload, ok := blockPayloads[blockHash]
			if !ok {
				return nil, status.Error(codes.Internal, fmt.Sprintf("missing checkout block payload for %s", blockHash))
			}
			blockContents = append(blockContents, &slicev1.BlockContent{
				Hash:    blockHash,
				Content: payload,
			})
			profile.addBlockPayload(len(payload))
		}
	}
	for _, meta := range fileMetadata {
		if meta == nil || len(meta.GetBlocks()) > 0 {
			continue
		}
		if meta.GetHash() != "" {
			if _, ok := knownHashes[meta.GetHash()]; ok {
				continue
			}
		}
		if meta.GetSize() == 0 {
			file, err := s.resolveCheckoutFileContent(ctx, slice, backingSliceID, meta.GetFileId(), resolvedCommit)
			if err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load content for %s: %v", meta.GetFileId(), err))
			}
			fileContents = append(fileContents, &slicev1.FileContent{
				FileId:  file.FileID,
				Content: file.Content,
			})
			profile.addFilePayload(len(file.Content))
			continue
		}
		file, err := s.resolveCheckoutFileContent(ctx, slice, backingSliceID, meta.GetFileId(), resolvedCommit)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load content for %s: %v", meta.GetFileId(), err))
		}
		fileContents = append(fileContents, &slicev1.FileContent{
			FileId:  file.FileID,
			Content: file.Content,
		})
		profile.addFilePayload(len(file.Content))
	}

	manifest := &slicev1.SliceManifest{
		CommitHash:   metadata.HeadCommitHash,
		FileMetadata: fileMetadata,
	}
	profile.addManifestChunk(len(fileMetadata))
	profile.finish(time.Since(payloadStartedAt))

	return &slicev1.CheckoutResponse{
		Manifest: manifest,
		Files:    fileContents,
		Blocks:   blockContents,
	}, nil
}

func (s *sliceServiceServer) StreamCheckoutSlice(req *slicev1.CheckoutRequest, stream slicev1.SliceService_StreamCheckoutSliceServer) (err error) {
	profile := newCheckoutProfile("stream", req.GetSliceId(), req.GetCommitHash(), len(req.GetKnownHashes()))
	defer func() {
		profile.logResult(err)
	}()

	prepareStartedAt := time.Now()
	metadata, slice, resolvedCommit, backingSliceID, fileMetadata, knownHashes, err := s.prepareCheckout(stream.Context(), req)
	if err != nil {
		return err
	}
	profile.markPrepared(len(fileMetadata), time.Since(prepareStartedAt))
	payloadStartedAt := time.Now()

	if len(fileMetadata) == 0 {
		profile.addManifestChunk(0)
		if err := stream.Send(&slicev1.CheckoutChunk{
			Chunk: &slicev1.CheckoutChunk_Manifest{
				Manifest: &slicev1.SliceManifest{CommitHash: metadata.HeadCommitHash},
			},
		}); err != nil {
			return err
		}
	} else {
		for start := 0; start < len(fileMetadata); start += checkoutManifestChunkSize {
			end := start + checkoutManifestChunkSize
			if end > len(fileMetadata) {
				end = len(fileMetadata)
			}
			profile.addManifestChunk(end - start)
			if err := stream.Send(&slicev1.CheckoutChunk{
				Chunk: &slicev1.CheckoutChunk_Manifest{
					Manifest: &slicev1.SliceManifest{
						CommitHash:   metadata.HeadCommitHash,
						FileMetadata: fileMetadata[start:end],
					},
				},
			}); err != nil {
				return err
			}
		}
	}

	blockHashes := collectMissingCheckoutBlockHashes(fileMetadata, knownHashes)
	if len(blockHashes) > 0 {
		blockPayloads, err := s.storage.GetBlocks(stream.Context(), blockHashes)
		if err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("failed to load checkout blocks: %v", err))
		}
		for _, blockHash := range blockHashes {
			payload, ok := blockPayloads[blockHash]
			if !ok {
				return status.Error(codes.Internal, fmt.Sprintf("missing checkout block payload for %s", blockHash))
			}
			if err := stream.Send(&slicev1.CheckoutChunk{
				Chunk: &slicev1.CheckoutChunk_Block{
					Block: &slicev1.BlockContent{
						Hash:    blockHash,
						Content: payload,
					},
				},
			}); err != nil {
				return err
			}
			profile.addBlockPayload(len(payload))
		}
	}
	for _, meta := range fileMetadata {
		if meta == nil || len(meta.GetBlocks()) > 0 {
			continue
		}
		if meta.GetHash() != "" {
			if _, ok := knownHashes[meta.GetHash()]; ok {
				continue
			}
		}
		file, err := s.resolveCheckoutFileContent(stream.Context(), slice, backingSliceID, meta.GetFileId(), resolvedCommit)
		if err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("failed to load content for %s: %v", meta.GetFileId(), err))
		}
		if err := stream.Send(&slicev1.CheckoutChunk{
			Chunk: &slicev1.CheckoutChunk_File{
				File: &slicev1.FileContent{
					FileId:  file.FileID,
					Content: file.Content,
				},
			},
		}); err != nil {
			return err
		}
		profile.addFilePayload(len(file.Content))
	}

	profile.finish(time.Since(payloadStartedAt))
	return nil
}

func (s *sliceServiceServer) GetSliceSearchArtifact(ctx context.Context, req *slicev1.GetSliceSearchArtifactRequest) (*slicev1.GetSliceSearchArtifactResponse, error) {
	startedAt := time.Now()
	version := req.GetVersion()
	if version == 0 {
		version = searchindex.CurrentArtifactVersion
	}
	if version != searchindex.CurrentArtifactVersion {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("unsupported search artifact version: %d", version))
	}

	_, _, effectiveCommit, err := s.resolveCheckoutTarget(ctx, req.GetSliceId(), req.GetCommitHash())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(effectiveCommit) == "" {
		return nil, status.Error(codes.NotFound, "slice commit not found")
	}

	artifact, outcome, err := storage.LoadOrBuildSliceSearchArtifact(ctx, s.storage, req.GetSliceId(), effectiveCommit)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrCommitNotFound), errors.Is(err, storage.ErrEntryNotFound):
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice search artifact unavailable for commit %s", effectiveCommit))
		case errors.Is(err, storage.ErrInvalidInput):
			return nil, status.Error(codes.InvalidArgument, "invalid slice search artifact request")
		default:
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice search artifact: %v", err))
		}
	}

	payload, err := searchindex.EncodeSliceArtifact(artifact)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to encode slice search artifact: %v", err))
	}
	observeSliceSearchArtifactDownload(outcome.String(), time.Since(startedAt))

	return &slicev1.GetSliceSearchArtifactResponse{
		SliceId:    req.GetSliceId(),
		CommitHash: effectiveCommit,
		Version:    version,
		Artifact:   payload,
	}, nil
}

func checkoutProtoBlocks(blocks []models.Block) []*slicev1.FileBlockRef {
	if len(blocks) == 0 {
		return nil
	}
	protoBlocks := make([]*slicev1.FileBlockRef, 0, len(blocks))
	for _, block := range blocks {
		hash := strings.TrimSpace(block.Hash)
		if hash == "" {
			continue
		}
		protoBlocks = append(protoBlocks, &slicev1.FileBlockRef{
			Hash: hash,
			Size: int64(block.Size),
		})
	}
	return protoBlocks
}

func (s *sliceServiceServer) CreateChangeset(ctx context.Context, req *slicev1.CreateChangesetRequest) (*slicev1.CreateChangesetResponse, error) {
	log.Printf("CreateChangeset called: slice_id=%s, author=%s", req.SliceId, req.Author)

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	fileContents, err := normalizeChangesetFileContentChanges(req.GetFileContents())
	if err != nil {
		return nil, err
	}
	modifiedFiles := mergeModifiedFilesWithFileContents(req.ModifiedFiles, fileContents)

	// Validate modified files
	for _, fileID := range modifiedFiles {
		if err := common.ValidateFileID(fileID); err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid file ID %s: %v", fileID, err))
		}
	}

	targetChangesetID := strings.TrimSpace(req.GetChangesetId())
	if targetChangesetID != "" {
		if strings.TrimSpace(req.SliceId) != "" {
			if err := common.ValidateSliceID(req.SliceId); err != nil {
				return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid slice ID: %v", err))
			}
		}

		existing, err := s.storage.GetChangeset(ctx, targetChangesetID)
		if err != nil {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", targetChangesetID))
		}
		if existing.Status == models.ChangesetStatusMerged || existing.Status == models.ChangesetStatusRejected {
			return nil, status.Error(codes.FailedPrecondition, "closed changeset cannot accept new snapshots")
		}

		if strings.TrimSpace(req.SliceId) != "" && strings.TrimSpace(req.SliceId) != strings.TrimSpace(existing.SliceID) {
			return nil, status.Error(codes.InvalidArgument, "slice_id must match the existing changeset")
		}

		slice, err := s.storage.GetSlice(ctx, existing.SliceID)
		if err != nil {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", existing.SliceID))
		}
		if !s.hasSliceViewAccess(ctx, slice, username) {
			return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
		}
		modifiedFiles, fileContents = filterChangesetChangesForSlice(slice, modifiedFiles, fileContents)
		if len(modifiedFiles) == 0 {
			return nil, status.Error(codes.InvalidArgument, "no modified files remain after filtering paths outside the slice tracked folders")
		}
		baseCommitHash := strings.TrimSpace(req.BaseCommitHash)
		if baseCommitHash == "" {
			baseCommitHash = strings.TrimSpace(existing.BaseCommitHash)
		}
		if err := s.applyChangesetFileContents(ctx, existing.SliceID, fileContents); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to apply changeset file content: %v", err))
		}

		existing.Hash = common.GenerateChangesetVersionHash()
		existing.BaseCommitHash = baseCommitHash
		existing.ModifiedFiles = modifiedFiles
		existing.Author = username
		existing.Message = req.Message
		if existing.Status == models.ChangesetStatusApproved {
			existing.Status = models.ChangesetStatusPending
		}

		if err := s.storage.UpdateChangeset(ctx, existing); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update changeset: %v", err))
		}
		if err := s.createChangesetSnapshot(ctx, existing); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to save changeset snapshot: %v", err))
		}
		ciRunID, ciStatus := s.enqueueChangesetExportCI(ctx, existing.ID, username)

		return &slicev1.CreateChangesetResponse{
			ChangesetId:   existing.ID,
			ChangesetHash: existing.Hash,
			Status:        convertChangesetStatusToProto(existing.Status),
			CiRunId:       ciRunID,
			CiStatus:      ciStatus,
		}, nil
	}

	// Validate slice ID for new changesets.
	if err := common.ValidateSliceID(req.SliceId); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid slice ID: %v", err))
	}

	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}
	modifiedFiles, fileContents = filterChangesetChangesForSlice(slice, modifiedFiles, fileContents)
	if len(modifiedFiles) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no modified files remain after filtering paths outside the slice tracked folders")
	}
	if err := s.applyChangesetFileContents(ctx, req.SliceId, fileContents); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to apply changeset file content: %v", err))
	}

	id := ""
	hash := common.GenerateChangesetVersionHash()

	cs := &models.Changeset{
		ID:             id,
		Hash:           hash,
		SliceID:        req.SliceId,
		BaseCommitHash: strings.TrimSpace(req.BaseCommitHash),
		ModifiedFiles:  modifiedFiles,
		Status:         models.ChangesetStatusPending,
		Author:         username,
		Message:        req.Message,
		CreatedAt:      time.Now(),
	}

	if err := s.storage.CreateChangeset(ctx, cs); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create changeset: %v", err))
	}
	if err := s.createChangesetSnapshot(ctx, cs); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to save changeset snapshot: %v", err))
	}
	ciRunID, ciStatus := s.enqueueChangesetExportCI(ctx, cs.ID, username)

	return &slicev1.CreateChangesetResponse{
		ChangesetId:   cs.ID,
		ChangesetHash: cs.Hash,
		Status:        convertChangesetStatusToProto(cs.Status),
		CiRunId:       ciRunID,
		CiStatus:      ciStatus,
	}, nil
}

func (s *sliceServiceServer) CreateAndMergeChangeset(ctx context.Context, req *slicev1.CreateChangesetRequest) (*slicev1.MergeChangesetResponse, error) {
	log.Printf("CreateAndMergeChangeset called: slice_id=%s", req.GetSliceId())

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetChangesetId()) != "" {
		return nil, status.Error(codes.InvalidArgument, "changeset_id is not supported for direct file-tree commits")
	}
	hasFileContentChange := false
	for _, change := range req.GetFileContents() {
		if change != nil {
			hasFileContentChange = true
			break
		}
	}
	if !hasFileContentChange {
		return nil, status.Error(codes.InvalidArgument, "file_contents is required for direct file-tree commits")
	}

	createResp, err := s.CreateChangeset(ctx, req)
	if err != nil {
		return nil, err
	}
	return s.mergeChangeset(ctx, createResp.GetChangesetId(), username, false, mergeChangesetOptions{
		skipGate: true,
	})
}

func (s *sliceServiceServer) ReviewChangeset(ctx context.Context, req *slicev1.ReviewChangesetRequest) (*slicev1.ReviewChangesetResponse, error) {
	log.Printf("ReviewChangeset called: changeset_id=%s", req.ChangesetId)

	cs, _, _, err := s.loadReadableChangeset(ctx, req.GetChangesetId())
	if err != nil {
		return nil, err
	}

	snapshot, err := s.resolveChangesetSnapshotForReview(ctx, cs, req.GetSnapshotVersion())
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	reviewCS := applySnapshotToChangeset(cs, snapshot)

	diff := &slicev1.DiffSummary{
		FilesAdded:    int32(len(reviewCS.ModifiedFiles)),
		FilesModified: 0,
		FilesDeleted:  0,
		LinesAdded:    int64(len(reviewCS.ModifiedFiles)),
		LinesRemoved:  0,
	}
	reviewChanges, warnings := s.buildReviewChanges(ctx, reviewCS, snapshot)
	if len(reviewChanges) > 0 {
		diff = summarizeReviewChanges(reviewChanges)
	}

	reviewStatus, issues, stateWarnings, err := s.evaluateChangesetReviewState(ctx, reviewCS, snapshot)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to evaluate changeset state: %v", err))
	}
	warnings = append(warnings, stateWarnings...)

	changesetInfo := convertChangesetToProto(reviewCS)
	changesetInfo.ReviewStatus = reviewStatus
	changesetInfo.Ci = s.buildChangesetCISummary(ctx, reviewCS.ID, snapshot.ID)

	return &slicev1.ReviewChangesetResponse{
		Changeset:    changesetInfo,
		Diff:         diff,
		ReviewStatus: reviewStatus,
		Warnings:     warnings,
		Changes:      reviewChanges,
		Snapshot:     changesetSnapshotToProto(snapshot),
		Issues:       issues,
	}, nil
}

func (s *sliceServiceServer) GetChangesetArtifactLinks(ctx context.Context, req *slicev1.GetChangesetArtifactLinksRequest) (*slicev1.GetChangesetArtifactLinksResponse, error) {
	changesetID := strings.TrimSpace(req.GetChangesetId())
	if changesetID == "" {
		return nil, status.Error(codes.InvalidArgument, "changeset_id is required")
	}

	cs, _, _, err := s.loadReadableChangeset(ctx, changesetID)
	if err != nil {
		return nil, err
	}

	links, err := s.agentSessionChangesetLinks(ctx, cs)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load agent session links: %v", err))
	}

	mergeLink, err := s.changesetMergeLink(ctx, cs.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load merge link: %v", err))
	}

	return &slicev1.GetChangesetArtifactLinksResponse{
		Changeset:     convertChangesetToProto(cs),
		AgentSessions: links,
		Merge:         mergeLink,
	}, nil
}

func (s *sliceServiceServer) GetCommitArtifactLinks(ctx context.Context, req *slicev1.GetCommitArtifactLinksRequest) (*slicev1.GetCommitArtifactLinksResponse, error) {
	commitHash := strings.TrimSpace(req.GetCommitHash())
	if commitHash == "" {
		return nil, status.Error(codes.InvalidArgument, "commit_hash is required")
	}

	resp := &slicev1.GetCommitArtifactLinksResponse{CommitHash: commitHash}
	mergeStore, ok := s.storage.(storage.MergeEventStore)
	if !ok {
		return resp, nil
	}
	event, err := mergeStore.GetMergeEventBySourceCommitHash(ctx, commitHash)
	if err != nil {
		if errors.Is(err, storage.ErrMergeEventNotFound) {
			return resp, nil
		}
		if errors.Is(err, storage.ErrInvalidInput) {
			return nil, status.Error(codes.InvalidArgument, "commit_hash is required")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load merge event: %v", err))
	}

	cs, _, _, err := s.loadReadableChangeset(ctx, event.ChangesetID)
	if err != nil {
		return nil, err
	}
	links, err := s.agentSessionChangesetLinks(ctx, cs)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load agent session links: %v", err))
	}

	resp.Changeset = convertChangesetToProto(cs)
	resp.AgentSessions = links
	return resp, nil
}

func (s *sliceServiceServer) ListChangesetSnapshots(ctx context.Context, req *slicev1.ListChangesetSnapshotsRequest) (*slicev1.ListChangesetSnapshotsResponse, error) {
	log.Printf("ListChangesetSnapshots called: changeset_id=%s", req.GetChangesetId())

	cs, _, _, err := s.loadReadableChangeset(ctx, req.GetChangesetId())
	if err != nil {
		return nil, err
	}

	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 100
	}

	snapshots, err := s.listChangesetSnapshots(ctx, cs.ID, limit, !req.GetOmitModifiedFiles())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list changeset snapshots: %v", err))
	}
	if len(snapshots) == 0 {
		snapshots = []*models.ChangesetSnapshot{buildSyntheticChangesetSnapshot(cs)}
	}

	resp := &slicev1.ListChangesetSnapshotsResponse{}
	for _, snapshot := range snapshots {
		info := changesetSnapshotToProto(snapshot)
		if info == nil {
			continue
		}
		if req.GetOmitModifiedFiles() {
			info.ModifiedFiles = nil
		}
		resp.Snapshots = append(resp.Snapshots, info)
	}
	return resp, nil
}

func (s *sliceServiceServer) StreamChangesetSnapshot(req *slicev1.ChangesetSnapshotRequest, stream slicev1.SliceService_StreamChangesetSnapshotServer) error {
	ctx := stream.Context()
	changesetID := strings.TrimSpace(req.GetChangesetId())
	if changesetID == "" {
		return status.Error(codes.InvalidArgument, "changeset_id is required")
	}

	cs, err := s.storage.GetChangeset(ctx, changesetID)
	if err != nil {
		return status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", changesetID))
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", cs.SliceID))
	}
	username, err := s.optionalUsername(ctx)
	if err != nil {
		return err
	}
	if !s.canReadSliceInfo(ctx, slice, username) {
		return sliceReadAccessError(username)
	}

	snapshot, err := s.resolveChangesetSnapshotForStream(ctx, cs, req.GetSnapshotVersion(), req.GetSnapshotHash())
	if err != nil {
		return err
	}
	paths := normalizeModifiedFiles(req.GetPaths())
	if len(paths) == 0 {
		paths = normalizeModifiedFiles(snapshot.ModifiedFiles)
	}
	fileMetadata, deletedPaths, err := s.buildChangesetSnapshotStreamManifest(ctx, snapshot, paths)
	if err != nil {
		return err
	}

	if err := sendChangesetSnapshotManifestChunks(stream, snapshot, cs.SliceID, fileMetadata, deletedPaths); err != nil {
		return err
	}
	if req.GetMetadataOnly() {
		return nil
	}

	knownHashes := make(map[string]struct{}, len(req.GetKnownHashes()))
	for _, hash := range req.GetKnownHashes() {
		hash = strings.TrimSpace(hash)
		if hash == "" {
			continue
		}
		knownHashes[hash] = struct{}{}
	}

	blockHashes := collectMissingCheckoutBlockHashes(fileMetadata, knownHashes)
	if len(blockHashes) > 0 {
		blockPayloads, err := s.storage.GetBlocks(ctx, blockHashes)
		if err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("failed to load snapshot blocks: %v", err))
		}
		for _, blockHash := range blockHashes {
			payload, ok := blockPayloads[blockHash]
			if !ok {
				return status.Error(codes.Internal, fmt.Sprintf("missing snapshot block payload for %s", blockHash))
			}
			if err := stream.Send(&slicev1.ChangesetSnapshotChunk{
				Chunk: &slicev1.ChangesetSnapshotChunk_Block{
					Block: &slicev1.BlockContent{Hash: blockHash, Content: payload},
				},
			}); err != nil {
				return err
			}
		}
	}

	for _, meta := range fileMetadata {
		if meta == nil || len(meta.GetBlocks()) > 0 || strings.TrimSpace(meta.GetSymlinkTarget()) != "" {
			continue
		}
		if hash := strings.TrimSpace(meta.GetHash()); hash != "" {
			if _, ok := knownHashes[hash]; ok {
				continue
			}
		}
		content, err := storage.ReadVersionedFileContent(ctx, s.storage, meta.GetHash())
		if err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("failed to load snapshot content for %s: %v", meta.GetPath(), err))
		}
		if err := stream.Send(&slicev1.ChangesetSnapshotChunk{
			Chunk: &slicev1.ChangesetSnapshotChunk_File{
				File: &slicev1.FileContent{
					FileId:  meta.GetFileId(),
					Content: content.Content,
				},
			},
		}); err != nil {
			return err
		}
	}

	return nil
}

func (s *sliceServiceServer) MergeChangeset(ctx context.Context, req *slicev1.MergeChangesetRequest) (*slicev1.MergeChangesetResponse, error) {
	if shouldLogProfiles() {
		log.Printf("MergeChangeset called: changeset_id=%s", req.ChangesetId)
	}

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	return s.mergeChangeset(ctx, req.GetChangesetId(), username, false, mergeChangesetOptions{
		force:       req.GetForce(),
		forceReason: req.GetForceReason(),
	})
}

// MergeChangesetUsingCurrentHead marks a changeset as merged and publishes the
// slice's existing head commit instead of creating a new no-op merge commit.
func (s *sliceServiceServer) MergeChangesetUsingCurrentHead(ctx context.Context, changesetID string) (*slicev1.MergeChangesetResponse, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	return s.mergeChangeset(ctx, changesetID, username, true, mergeChangesetOptions{})
}

type mergeChangesetOptions struct {
	force       bool
	forceReason string
	gate        *ciservice.MergeGateResult
	skipGate    bool
}

func (s *sliceServiceServer) mergeChangeset(ctx context.Context, changesetID, username string, useCurrentHead bool, opts mergeChangesetOptions) (_ *slicev1.MergeChangesetResponse, retErr error) {
	cs, err := s.storage.GetChangeset(ctx, changesetID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", changesetID))
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", cs.SliceID))
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	modifiedFiles := normalizeModifiedFiles(cs.ModifiedFiles)
	cs.ModifiedFiles = modifiedFiles
	profile := newMergeProfile(cs.ID, cs.SliceID, len(modifiedFiles))
	defer func() {
		profile.finish()
		profile.logResult(retErr)
	}()

	if !useCurrentHead && !opts.skipGate {
		gate, err := ciservice.EnforceChangesetMergeGate(ctx, s.storage, ciservice.MergeGateRequest{
			ChangesetID:       cs.ID,
			TriggeredByUserID: username,
			Force:             opts.force,
			ForceReason:       opts.forceReason,
		})
		if err != nil {
			return nil, err
		}
		opts.gate = gate
	}

	if !opts.force {
		if resp, handled, err := s.tryMergeChangesetFastPath(ctx, cs, slice, modifiedFiles, useCurrentHead, profile); handled {
			return resp, err
		}
	}

	if !opts.force && opts.gate == nil {
		if resp, handled, err := s.tryMergeChangesetByIDFastPath(ctx, changesetID, username, useCurrentHead); handled {
			return resp, err
		}
	}

	if err := s.storage.LockSliceAndFiles(ctx, cs.SliceID, modifiedFiles); err != nil {
		if errors.Is(err, storage.ErrLockHeld) {
			return &slicev1.MergeChangesetResponse{
				Status:      slicev1.MergeStatus_MERGE_STATUS_LOCKED,
				ChangesetId: cs.ID,
				Message:     "slice or files are locked by another operation",
			}, nil
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to acquire locks: %v", err))
	}
	defer s.storage.UnlockSliceAndFiles(ctx, cs.SliceID, modifiedFiles)

	pathHeadSnapshot, pathHeadAuthority, err := s.loadPathHeadMergeAuthority(ctx, cs, modifiedFiles)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load path-head merge authority: %v", err))
	}

	if pathHeadAuthority {
		drifts, _, err := s.changesetPathHeadDrifts(ctx, cs, pathHeadSnapshot)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to evaluate path-head merge authority: %v", err))
		}
		if len(drifts) > 0 {
			return &slicev1.MergeChangesetResponse{
				Status:      slicev1.MergeStatus_MERGE_STATUS_STALE_BASE,
				ChangesetId: cs.ID,
				Message:     pathHeadDriftMergeMessage(drifts),
			}, nil
		}
	} else {
		return &slicev1.MergeChangesetResponse{
			Status:      slicev1.MergeStatus_MERGE_STATUS_STALE_BASE,
			ChangesetId: cs.ID,
			Message:     missingPathHeadAuthorityMessage(),
		}, nil
	}

	if err := s.validateChangesetSnapshotContentRefs(ctx, s.storage, cs); err != nil {
		return nil, err
	}

	revertStartedAt := time.Now()
	appliedRevertChanges, err := s.applyRevertChangesetContent(ctx, cs)
	profile.markRevertApply(time.Since(revertStartedAt))
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to apply revert changeset content: %v", err))
	}

	newCommit := common.GenerateCommitID()
	cs.Status = models.ChangesetStatusMerged
	now := time.Now()
	cs.MergedAt = &now

	var promotionCommitHash string
	var promotionParentHash string
	var promotionCommitTime time.Time
	var acceptedMergeEvent *models.MergeEvent
	eventPathHeadSnapshot := pathHeadSnapshot
	if !pathHeadAuthority {
		eventPathHeadSnapshot = nil
	}
	finalizeStartedAt := time.Now()
	if err := withMergeStorage(ctx, s.storage, func(st storage.Storage) error {
		if len(appliedRevertChanges) == 0 {
			// Conflicts were already checked under lock above. At this point no
			// divergent owner exists for the touched paths.
			if err := addFilesToSlice(ctx, st, modifiedFiles, cs.SliceID); err != nil {
				return fmt.Errorf("failed to mark file ownership: %w", err)
			}
		}
		if err := st.UpdateChangeset(ctx, cs); err != nil {
			return fmt.Errorf("failed to update changeset: %w", err)
		}

		metadata, err := st.GetSliceMetadata(ctx, cs.SliceID)
		if err != nil {
			return nil
		}

		promotionCommitHash = newCommit
		promotionCommitTime = now
		if useCurrentHead {
			promotionCommitHash = strings.TrimSpace(metadata.HeadCommitHash)
			if promotionCommitHash == "" {
				return status.Error(codes.FailedPrecondition, "slice head is empty")
			}
			existingCommit, commitErr := st.GetCommitByHash(ctx, cs.SliceID, promotionCommitHash)
			if commitErr != nil {
				return status.Error(codes.Internal, fmt.Sprintf("failed to load slice head commit: %v", commitErr))
			}
			if existingCommit != nil && !existingCommit.Timestamp.IsZero() {
				promotionCommitTime = existingCommit.Timestamp
			}
			if existingCommit != nil {
				promotionParentHash = existingCommit.ParentHash
			}
			event, err := s.appendAcceptedMergeEvent(ctx, st, cs, slice, promotionCommitHash, promotionParentHash, modifiedFiles, promotionCommitTime, eventPathHeadSnapshot, pathHeadAuthority, opts.gate)
			if err != nil {
				return fmt.Errorf("failed to append merge event: %w", err)
			}
			acceptedMergeEvent = event
			return nil
		}

		parentHash := metadata.HeadCommitHash
		promotionParentHash = parentHash
		metadata.HeadCommitHash = newCommit
		metadata.ModifiedFiles = modifiedFiles
		metadata.ModifiedFilesCount = len(modifiedFiles)

		if err := st.UpdateSliceMetadata(ctx, cs.SliceID, metadata); err != nil {
			log.Printf("Warning: failed to update slice metadata for %s: %v", cs.SliceID, err)
		}

		event, err := s.appendAcceptedMergeEvent(ctx, st, cs, slice, newCommit, parentHash, modifiedFiles, now, eventPathHeadSnapshot, pathHeadAuthority, opts.gate)
		if err != nil {
			return fmt.Errorf("failed to append merge event: %w", err)
		}
		acceptedMergeEvent = event
		return nil
	}); err != nil {
		profile.markFinalize(time.Since(finalizeStartedAt))
		if pathHeadAuthority && errors.Is(err, storage.ErrHomePathHeadConflict) {
			drifts, _, driftErr := s.changesetPathHeadDrifts(ctx, cs, pathHeadSnapshot)
			if driftErr != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to evaluate path-head conflict: %v", driftErr))
			}
			return &slicev1.MergeChangesetResponse{
				Status:      slicev1.MergeStatus_MERGE_STATUS_STALE_BASE,
				ChangesetId: cs.ID,
				Message:     pathHeadDriftMergeMessage(drifts),
			}, nil
		}
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	profile.markFinalize(time.Since(finalizeStartedAt))

	if acceptedMergeEvent != nil {
		s.enqueueHistoryProjection(ctx, acceptedMergeEvent)
	}

	promotionStartedAt := time.Now()
	if promotionCommitHash != "" {
		var promotionErr error
		if s.durablePromotion && !changesetTouchesConfig(modifiedFiles) {
			// Durable promotion workers consume the merge event appended above.
		} else if changesetTouchesConfig(modifiedFiles) {
			promotionErr = s.enqueueRootPromotionAndWait(ctx, cs.SliceID, promotionCommitHash, modifiedFiles, promotionCommitTime, acceptedMergeEvent)
		} else {
			promotionErr = s.enqueueRootPromotion(ctx, cs.SliceID, promotionCommitHash, modifiedFiles, promotionCommitTime, acceptedMergeEvent)
		}
		if promotionErr != nil {
			profile.markPromotion(time.Since(promotionStartedAt))
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to enqueue merged changeset promotion: %v", promotionErr))
		}
		if useCurrentHead {
			newCommit = promotionCommitHash
		}
	}
	profile.markPromotion(time.Since(promotionStartedAt))
	if username := homeslice.UsernameFromSliceID(cs.SliceID); username != "" {
		if err := ciservice.UpdateManifestIndexForHome(ctx, s.storage, username); err != nil {
			log.Printf("Warning: failed to update CI manifest index for home %s: %v", username, err)
		}
	}

	configStartedAt := time.Now()
	if changesetTouchesConfig(modifiedFiles) {
		if err := sliceconfig.ApplyFromFileTree(ctx, s.storage); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to sync %s: %v", sliceconfig.ConfigFilePath, err))
		}
	}
	profile.markConfig(time.Since(configStartedAt))

	return &slicev1.MergeChangesetResponse{
		Status:        slicev1.MergeStatus_MERGE_STATUS_SUCCESS,
		NewCommitHash: newCommit,
		ChangesetId:   cs.ID,
		Conflicts:     []*slicev1.Conflict{},
		Message:       mergeGateMessage(opts.gate),
		MergeHomeId:   mergeEventHomeIDFromEvent(acceptedMergeEvent),
		MergeShard:    mergeEventShardFromEvent(acceptedMergeEvent),
		MergeSeq:      mergeEventSeqFromEvent(acceptedMergeEvent),
		Projections:   s.mergeProjectionStatuses(ctx, acceptedMergeEvent),
	}, nil
}

func mergeGateMessage(gate *ciservice.MergeGateResult) string {
	if gate == nil {
		return ""
	}
	return strings.TrimSpace(gate.Message)
}

func (s *sliceServiceServer) tryMergeChangesetByIDFastPath(ctx context.Context, changesetID, username string, useCurrentHead bool) (_ *slicev1.MergeChangesetResponse, _ bool, retErr error) {
	accepter, ok := s.storage.(storage.ChangesetMergeByIDAccepter)
	if !ok || useCurrentHead {
		return nil, false, nil
	}
	profile := newMergeProfile(strings.TrimSpace(changesetID), "", 0)
	defer func() {
		profile.finish()
		profile.logResult(retErr)
	}()

	now := time.Now()
	newCommit := common.GenerateCommitID()
	finalizeStartedAt := time.Now()
	result, err := accepter.AcceptChangesetMergeByID(ctx, changesetID, username, newCommit, now)
	profile.markFinalize(time.Since(finalizeStartedAt))
	if err != nil {
		if errors.Is(err, storage.ErrMergeFastPathUnsupported) {
			return nil, false, nil
		}
		if errors.Is(err, storage.ErrChangesetNotFound) {
			return nil, true, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", changesetID))
		}
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil, true, status.Error(codes.NotFound, "slice not found")
		}
		if errors.Is(err, storage.ErrPermissionDenied) {
			return nil, true, status.Error(codes.PermissionDenied, "not authorized for slice")
		}
		if errors.Is(err, storage.ErrHomePathHeadConflict) {
			return &slicev1.MergeChangesetResponse{
				Status:      slicev1.MergeStatus_MERGE_STATUS_STALE_BASE,
				ChangesetId: strings.TrimSpace(changesetID),
				Message:     "changeset paths changed since export. Sync the changeset before merging.",
			}, true, nil
		}
		return nil, true, status.Error(codes.Internal, fmt.Sprintf("failed to accept changeset merge: %v", err))
	}
	if result == nil || result.Event == nil || result.Changeset == nil {
		return nil, false, nil
	}
	profile.sliceID = strings.TrimSpace(result.Changeset.SliceID)
	profile.modifiedFiles = len(result.Event.TouchedPaths)

	s.enqueueHistoryProjection(ctx, result.Event)
	promotionStartedAt := time.Now()
	if s.durablePromotion {
		// Durable promotion workers consume the accepted merge event.
	} else if err := s.enqueueRootPromotion(ctx, result.Changeset.SliceID, newCommit, result.Event.TouchedPaths, now, result.Event); err != nil {
		profile.markPromotion(time.Since(promotionStartedAt))
		return nil, true, status.Error(codes.Internal, fmt.Sprintf("failed to enqueue merged changeset promotion: %v", err))
	}
	profile.markPromotion(time.Since(promotionStartedAt))
	if username := homeslice.UsernameFromSliceID(result.Changeset.SliceID); username != "" {
		if err := ciservice.UpdateManifestIndexForHome(ctx, s.storage, username); err != nil {
			log.Printf("Warning: failed to update CI manifest index for home %s: %v", username, err)
		}
	}

	return &slicev1.MergeChangesetResponse{
		Status:        slicev1.MergeStatus_MERGE_STATUS_SUCCESS,
		NewCommitHash: newCommit,
		ChangesetId:   result.Changeset.ID,
		Conflicts:     []*slicev1.Conflict{},
		MergeHomeId:   mergeEventHomeIDFromEvent(result.Event),
		MergeShard:    mergeEventShardFromEvent(result.Event),
		MergeSeq:      mergeEventSeqFromEvent(result.Event),
		Projections:   s.mergeProjectionStatuses(ctx, result.Event),
	}, true, nil
}

func (s *sliceServiceServer) tryMergeChangesetFastPath(ctx context.Context, cs *models.Changeset, sourceSlice *models.Slice, modifiedFiles []string, useCurrentHead bool, profile *mergeProfile) (*slicev1.MergeChangesetResponse, bool, error) {
	accepter, ok := s.storage.(storage.ChangesetMergeAccepter)
	if !ok || cs == nil || sourceSlice == nil {
		return nil, false, nil
	}
	if useCurrentHead || isRevertChangesetHash(cs.Hash) || changesetTouchesConfig(modifiedFiles) {
		return nil, false, nil
	}

	now := time.Now()
	newCommit := common.GenerateCommitID()
	homeID := mergeEventHomeID(sourceSlice, cs, modifiedFiles)
	shardID := mergeEventShardID(homeID)

	finalizeStartedAt := time.Now()
	result, err := accepter.AcceptChangesetMerge(ctx, &storage.AcceptChangesetMergeRequest{
		Changeset:     cs,
		SourceSlice:   sourceSlice,
		ModifiedFiles: modifiedFiles,
		HomeID:        homeID,
		ShardID:       shardID,
		CommitHash:    newCommit,
		MergedAt:      now,
	})
	profile.markFinalize(time.Since(finalizeStartedAt))
	if err != nil {
		if errors.Is(err, storage.ErrMergeFastPathUnsupported) {
			return nil, false, nil
		}
		if errors.Is(err, storage.ErrHomePathHeadConflict) {
			snapshot, snapshotErr := s.storage.GetChangesetSnapshot(ctx, cs.ID, 0)
			if snapshotErr != nil && !errors.Is(snapshotErr, storage.ErrChangesetNotFound) {
				return nil, true, status.Error(codes.Internal, fmt.Sprintf("failed to load changeset snapshot: %v", snapshotErr))
			}
			drifts, _, driftErr := s.changesetPathHeadDrifts(ctx, cs, snapshot)
			if driftErr != nil {
				return nil, true, status.Error(codes.Internal, fmt.Sprintf("failed to evaluate path-head conflict: %v", driftErr))
			}
			return &slicev1.MergeChangesetResponse{
				Status:      slicev1.MergeStatus_MERGE_STATUS_STALE_BASE,
				ChangesetId: cs.ID,
				Message:     pathHeadDriftMergeMessage(drifts),
			}, true, nil
		}
		return nil, true, status.Error(codes.Internal, fmt.Sprintf("failed to accept changeset merge: %v", err))
	}
	if result == nil || result.Event == nil {
		return nil, false, nil
	}

	cs.Status = models.ChangesetStatusMerged
	cs.MergedAt = &now
	s.enqueueHistoryProjection(ctx, result.Event)

	promotionStartedAt := time.Now()
	if s.durablePromotion {
		// Durable promotion workers consume the accepted merge event.
	} else if err := s.enqueueRootPromotion(ctx, cs.SliceID, newCommit, modifiedFiles, now, result.Event); err != nil {
		profile.markPromotion(time.Since(promotionStartedAt))
		return nil, true, status.Error(codes.Internal, fmt.Sprintf("failed to enqueue merged changeset promotion: %v", err))
	}
	profile.markPromotion(time.Since(promotionStartedAt))
	if username := homeslice.UsernameFromSliceID(cs.SliceID); username != "" {
		if err := ciservice.UpdateManifestIndexForHome(ctx, s.storage, username); err != nil {
			log.Printf("Warning: failed to update CI manifest index for home %s: %v", username, err)
		}
	}

	return &slicev1.MergeChangesetResponse{
		Status:        slicev1.MergeStatus_MERGE_STATUS_SUCCESS,
		NewCommitHash: newCommit,
		ChangesetId:   cs.ID,
		Conflicts:     []*slicev1.Conflict{},
		MergeHomeId:   mergeEventHomeIDFromEvent(result.Event),
		MergeShard:    mergeEventShardFromEvent(result.Event),
		MergeSeq:      mergeEventSeqFromEvent(result.Event),
		Projections:   s.mergeProjectionStatuses(ctx, result.Event),
	}, true, nil
}

const mergeEventShardCount = 1024

func (s *sliceServiceServer) appendAcceptedMergeEvent(ctx context.Context, st storage.Storage, cs *models.Changeset, sourceSlice *models.Slice, commitHash string, parentHash string, modifiedFiles []string, mergedAt time.Time, snapshot *models.ChangesetSnapshot, requirePathHeadCAS bool, gate *ciservice.MergeGateResult) (*models.MergeEvent, error) {
	eventStore, ok := st.(storage.MergeEventStore)
	if !ok || cs == nil {
		return nil, nil
	}
	homeID := mergeEventHomeID(sourceSlice, cs, modifiedFiles)
	shardID := mergeEventShardID(homeID)
	mergeSeq, err := eventStore.NextMergeEventSequence(ctx, shardID)
	if err != nil {
		return nil, err
	}

	paths := normalizeModifiedFiles(modifiedFiles)
	fileHashes, err := getFileManifestHashes(ctx, st, cs.SliceID, paths)
	if err != nil {
		return nil, err
	}
	existingEntries, err := getExistingEntriesByPaths(ctx, st, cs.SliceID, paths)
	if err != nil {
		return nil, err
	}

	baseVersions, err := s.mergeEventBasePathVersions(ctx, st, homeID, paths, snapshot)
	if err != nil {
		return nil, err
	}
	pathUpdates := make([]*models.MergePathUpdate, 0, len(paths))
	for _, filePath := range paths {
		hash := strings.TrimSpace(fileHashes[filePath])
		baseVersion := baseVersions[filePath]
		pathUpdates = append(pathUpdates, &models.MergePathUpdate{
			Path:             filePath,
			BaseVersion:      baseVersion,
			NewVersion:       baseVersion + 1,
			ContentHash:      hash,
			ManifestHash:     hash,
			SourceSliceID:    cs.SliceID,
			SourceCommitHash: commitHash,
			ParentCommitHash: strings.TrimSpace(parentHash),
			Deleted:          !existingEntries[filePath],
		})
	}

	author := strings.TrimSpace(cs.Author)
	if author == "" && sourceSlice != nil {
		author = strings.TrimSpace(sourceSlice.CreatedBy)
	}
	if author == "" {
		author = "system"
	}
	event := &models.MergeEvent{
		HomeID:           homeID,
		ShardID:          shardID,
		MergeSeq:         mergeSeq,
		EventID:          common.GenerateMergeEventID(),
		ChangesetID:      cs.ID,
		SourceSliceID:    cs.SliceID,
		SourceCommitHash: commitHash,
		Author:           author,
		Message:          cs.Message,
		TouchedPaths:     paths,
		PathUpdates:      pathUpdates,
		CreatedAt:        mergedAt,
	}
	if gate != nil && gate.Forced {
		event.Forced = true
		event.ForceReason = gate.ForceReason
		event.ForcedBy = gate.ForcedBy
	}

	if requirePathHeadCAS {
		casStore, ok := st.(storage.MergeEventPathHeadCASStore)
		if !ok {
			return nil, storage.ErrInvalidInput
		}
		if err := casStore.AppendMergeEventWithPathHeadCAS(ctx, event); err != nil {
			return nil, err
		}
		return event, nil
	}

	if err := eventStore.AppendMergeEvent(ctx, event); err != nil {
		return nil, err
	}
	if headStore, ok := st.(storage.HomePathHeadStore); ok {
		if err := headStore.UpsertHomePathHeads(ctx, mergeEventHomePathHeads(event)); err != nil {
			return nil, err
		}
	}
	return event, nil
}

func (s *sliceServiceServer) loadPathHeadMergeAuthority(ctx context.Context, cs *models.Changeset, modifiedFiles []string) (*models.ChangesetSnapshot, bool, error) {
	if cs == nil {
		return nil, false, nil
	}
	if _, ok := s.storage.(storage.MergeEventPathHeadCASStore); !ok {
		return nil, false, nil
	}
	snapshot, err := s.storage.GetChangesetSnapshot(ctx, cs.ID, 0)
	if err != nil {
		if errors.Is(err, storage.ErrChangesetNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !snapshotHasBaseVersionsForPaths(snapshot, modifiedFiles) {
		return snapshot, false, nil
	}
	return snapshot, true, nil
}

func snapshotHasBaseVersionsForPaths(snapshot *models.ChangesetSnapshot, paths []string) bool {
	cleaned := normalizeModifiedFiles(paths)
	if snapshot == nil || len(cleaned) == 0 || len(snapshot.BasePathVersions) == 0 {
		return false
	}
	for _, rawPath := range cleaned {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}
		if _, ok := snapshot.BasePathVersions[filePath]; !ok {
			return false
		}
	}
	return true
}

func (s *sliceServiceServer) mergeEventBasePathVersions(ctx context.Context, st storage.Storage, homeID string, paths []string, snapshot *models.ChangesetSnapshot) (map[string]int64, error) {
	cleaned := normalizeModifiedFiles(paths)
	baseVersions := make(map[string]int64, len(cleaned))
	for _, rawPath := range cleaned {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}
		if snapshot != nil && snapshot.BasePathVersions != nil {
			if version, ok := snapshot.BasePathVersions[filePath]; ok && version >= 0 {
				baseVersions[filePath] = version
				continue
			}
		}
		baseVersions[filePath] = 0
	}
	if headStore, ok := st.(storage.HomePathHeadStore); ok && snapshot == nil {
		heads, err := headStore.GetHomePathHeads(ctx, homeID, cleaned)
		if err != nil {
			return nil, err
		}
		for _, rawPath := range cleaned {
			filePath := cleanDiffPath(rawPath)
			if filePath == "" {
				continue
			}
			if head := heads[filePath]; head != nil && head.PathVersion > 0 {
				baseVersions[filePath] = head.PathVersion
			}
		}
	}
	return baseVersions, nil
}

func mergeEventHomePathHeads(event *models.MergeEvent) []*models.HomePathHead {
	if event == nil {
		return nil
	}
	heads := make([]*models.HomePathHead, 0, len(event.PathUpdates))
	for _, update := range event.PathUpdates {
		if update == nil {
			continue
		}
		sourceSliceID := strings.TrimSpace(update.SourceSliceID)
		if sourceSliceID == "" {
			sourceSliceID = event.SourceSliceID
		}
		sourceCommitHash := strings.TrimSpace(update.SourceCommitHash)
		if sourceCommitHash == "" {
			sourceCommitHash = event.SourceCommitHash
		}
		heads = append(heads, &models.HomePathHead{
			HomeID:           event.HomeID,
			Path:             update.Path,
			PathVersion:      update.NewVersion,
			ContentHash:      update.ContentHash,
			ManifestHash:     update.ManifestHash,
			SourceSliceID:    sourceSliceID,
			SourceCommitHash: sourceCommitHash,
			LastMergeSeq:     event.MergeSeq,
			Deleted:          update.Deleted,
			UpdatedAt:        event.CreatedAt,
		})
	}
	return heads
}

func pathHeadDriftMergeMessage(drifts []changesetPathHeadDrift) string {
	if len(drifts) == 0 {
		return "changeset paths changed since export. Sync the changeset before merging."
	}
	first := drifts[0]
	return fmt.Sprintf(
		"path %s changed from version %d to %d. Sync the changeset before merging.",
		first.Path,
		first.BaseVersion,
		first.CurrentVersion,
	)
}

func mergeEventHomeID(sourceSlice *models.Slice, cs *models.Changeset, modifiedFiles []string) string {
	if sourceSlice != nil {
		if username := homeslice.UsernameFromSliceID(sourceSlice.ID); username != "" {
			return username
		}
	}
	if homeRoot := commonHomeRootFromFiles(modifiedFiles); homeRoot != "" {
		return homeRoot
	}
	if sourceSlice != nil {
		if createdBy := strings.TrimSpace(sourceSlice.CreatedBy); createdBy != "" {
			return createdBy
		}
		for _, owner := range sourceSlice.Owners {
			if owner = strings.TrimSpace(owner); owner != "" {
				return owner
			}
		}
	}
	if cs != nil && strings.TrimSpace(cs.SliceID) != "" {
		return strings.TrimSpace(cs.SliceID)
	}
	return "global"
}

func mergeEventShardID(homeID string) int32 {
	homeID = strings.TrimSpace(homeID)
	if homeID == "" {
		homeID = "global"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(homeID))
	return int32(h.Sum32() % mergeEventShardCount)
}

func (s *sliceServiceServer) CloseChangeset(ctx context.Context, req *slicev1.CloseChangesetRequest) (*slicev1.CloseChangesetResponse, error) {
	log.Printf("CloseChangeset called: changeset_id=%s", req.ChangesetId)

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	cs, err := s.storage.GetChangeset(ctx, req.ChangesetId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", req.ChangesetId))
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", cs.SliceID))
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}
	if cs.Status == models.ChangesetStatusMerged {
		return nil, status.Error(codes.FailedPrecondition, "merged changeset cannot be closed")
	}

	if cs.Status != models.ChangesetStatusRejected {
		cs.Status = models.ChangesetStatusRejected
		if err := s.storage.UpdateChangeset(ctx, cs); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to close changeset: %v", err))
		}
	}

	return &slicev1.CloseChangesetResponse{
		ChangesetId: cs.ID,
		Status:      slicev1.ChangesetStatus_REJECTED,
	}, nil
}

func (s *sliceServiceServer) RevertCommitChange(ctx context.Context, req *slicev1.RevertCommitChangeRequest) (*slicev1.CreateChangesetResponse, error) {
	log.Printf("RevertCommitChange called: commit_hash=%s, change_id=%s", req.GetCommitHash(), req.GetChangeId())

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.storage.EnsureUser(ctx, username); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user")
	}

	commitHash := strings.TrimSpace(req.GetCommitHash())
	changeID := strings.TrimSpace(req.GetChangeId())
	if commitHash == "" {
		return nil, status.Error(codes.InvalidArgument, "commit_hash is required")
	}

	changes, err := s.storage.GetCommitChanges(ctx, commitHash)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load commit changes: %v", err))
	}

	sliceID := strings.TrimSpace(req.GetSliceId())
	targetChanges := make([]*models.FileChangeRecord, 0, len(changes))
	if changeID != "" {
		var targetChange *models.FileChangeRecord
		for _, change := range changes {
			if change != nil && change.ID == changeID {
				targetChange = change
				break
			}
		}
		if targetChange == nil {
			return nil, status.Error(codes.NotFound, "change not found")
		}
		if sliceID == "" {
			sliceID = strings.TrimSpace(targetChange.SliceID)
		}
		if sliceID != "" && strings.TrimSpace(targetChange.SliceID) != "" && strings.TrimSpace(targetChange.SliceID) != sliceID {
			return nil, status.Error(codes.InvalidArgument, "change does not belong to slice_id")
		}
		targetChanges = append(targetChanges, targetChange)
	} else {
		if sliceID == "" {
			sliceIDs := make(map[string]struct{}, 2)
			for _, change := range changes {
				if change == nil {
					continue
				}
				candidate := strings.TrimSpace(change.SliceID)
				if candidate == "" {
					continue
				}
				sliceIDs[candidate] = struct{}{}
			}
			if len(sliceIDs) == 1 {
				for candidate := range sliceIDs {
					sliceID = candidate
				}
			}
		}
		if sliceID == "" {
			return nil, status.Error(codes.InvalidArgument, "slice_id is required to revert this commit")
		}
		for _, change := range changes {
			if change == nil {
				continue
			}
			if strings.TrimSpace(change.SliceID) == sliceID {
				targetChanges = append(targetChanges, change)
			}
		}
		if len(targetChanges) == 0 {
			return nil, status.Error(codes.NotFound, "no changes found for commit in slice")
		}
	}
	if sliceID == "" {
		return nil, status.Error(codes.InvalidArgument, "slice_id is required")
	}

	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	metadata, err := s.storage.GetSliceMetadata(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice metadata: %v", err))
	}

	modifiedFiles := normalizeModifiedFiles(revertModifiedFilesForChanges(targetChanges))
	if len(modifiedFiles) == 0 {
		return nil, status.Error(codes.InvalidArgument, "commit has no revertable file path")
	}
	for _, fileID := range modifiedFiles {
		if err := common.ValidateFileID(fileID); err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid file ID %s: %v", fileID, err))
		}
	}

	message := strings.TrimSpace(req.GetMessage())
	if message == "" {
		if changeID != "" {
			message = fmt.Sprintf("Revert change %s from %s", changeID, shortHash(commitHash))
		} else {
			message = fmt.Sprintf("Revert commit %s", shortHash(commitHash))
		}
	}

	id := ""
	hash := buildRevertChangesetHash(commitHash, changeID)
	cs := &models.Changeset{
		ID:             id,
		Hash:           hash,
		SliceID:        sliceID,
		BaseCommitHash: metadata.HeadCommitHash,
		ModifiedFiles:  modifiedFiles,
		Status:         models.ChangesetStatusPending,
		Author:         username,
		Message:        message,
		CreatedAt:      time.Now(),
	}
	if err := s.storage.CreateChangeset(ctx, cs); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create revert changeset: %v", err))
	}
	if err := s.createChangesetSnapshot(ctx, cs); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to save changeset snapshot: %v", err))
	}

	return &slicev1.CreateChangesetResponse{
		ChangesetId:   cs.ID,
		ChangesetHash: cs.Hash,
		Status:        convertChangesetStatusToProto(cs.Status),
	}, nil
}

func changesetTouchesConfig(modifiedFiles []string) bool {
	for _, filePath := range modifiedFiles {
		trimmed := strings.Trim(strings.TrimSpace(filePath), "/")
		if trimmed == sliceconfig.ConfigFilePath || strings.HasSuffix(trimmed, "/"+sliceconfig.ConfigFilePath) {
			return true
		}
	}
	return false
}

func normalizeModifiedFiles(files []string) []string {
	if len(files) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, fileID := range files {
		cleaned := strings.TrimSpace(fileID)
		if cleaned == "" {
			continue
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	return normalized
}

func filterChangesetChangesForSlice(slice *models.Slice, modifiedFiles []string, fileContents []changesetFileContentChange) ([]string, []changesetFileContentChange) {
	roots, restrictAdds := changesetAllowedAddRoots(slice)
	if !restrictAdds {
		return modifiedFiles, fileContents
	}
	existingPaths := changesetExistingDisplayPathSet(slice)
	keepPath := func(raw string) bool {
		cleaned := common.CleanRelativePath(raw)
		if cleaned == "" {
			return false
		}
		if _, ok := existingPaths[cleaned]; ok {
			return true
		}
		return changesetPathUnderAllowedRoot(cleaned, roots)
	}

	filteredFiles := make([]string, 0, len(modifiedFiles))
	for _, filePath := range modifiedFiles {
		if keepPath(filePath) {
			filteredFiles = append(filteredFiles, filePath)
		}
	}
	filteredContents := make([]changesetFileContentChange, 0, len(fileContents))
	for _, change := range fileContents {
		if keepPath(change.path) {
			filteredContents = append(filteredContents, change)
		}
	}
	return normalizeModifiedFiles(filteredFiles), filteredContents
}

func changesetAllowedAddRoots(slice *models.Slice) ([]string, bool) {
	if slice == nil {
		return nil, false
	}
	if sliceUsesUnsupportedParentShape(slice) {
		return nil, true
	}
	roots := make([]string, 0, len(slice.FolderMounts)*2+1)
	if username := homeslice.UsernameFromSliceID(slice.ID); username != "" {
		roots = append(roots, homeslice.RelativeRootPath(username))
	}
	for _, mount := range slice.FolderMounts {
		roots = append(roots, mount.Alias, mount.SourcePath)
	}
	normalized := normalizeChangesetAllowedAddRoots(roots)
	return normalized, len(normalized) > 0 || len(slice.FolderMounts) > 0
}

func normalizeChangesetAllowedAddRoots(roots []string) []string {
	if len(roots) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(roots))
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		cleaned := common.CleanRelativePath(root)
		if cleaned == "" {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	sort.Strings(out)
	return out
}

func changesetExistingDisplayPathSet(slice *models.Slice) map[string]struct{} {
	if slice == nil {
		return map[string]struct{}{}
	}
	paths := make(map[string]struct{}, len(slice.Files)*2)
	for _, rawPath := range slice.Files {
		cleaned := common.CleanRelativePath(rawPath)
		if cleaned == "" {
			continue
		}
		paths[cleaned] = struct{}{}
		if displayPath := common.SliceDisplayPath(slice, cleaned); displayPath != "" {
			paths[common.CleanRelativePath(displayPath)] = struct{}{}
		}
	}
	return paths
}

func changesetPathUnderAllowedRoot(pathValue string, roots []string) bool {
	cleaned := common.CleanRelativePath(pathValue)
	if cleaned == "" {
		return false
	}
	for _, root := range roots {
		if cleaned == root {
			return false
		}
		if strings.HasPrefix(cleaned, root+"/") {
			return true
		}
	}
	return false
}

type changesetFileContentChange struct {
	path          string
	content       []byte
	deleted       bool
	executable    bool
	symlinkTarget string
}

func normalizeChangesetFileContentChanges(changes []*slicev1.FileContentChange) ([]changesetFileContentChange, error) {
	if len(changes) == 0 {
		return nil, nil
	}

	byPath := make(map[string]changesetFileContentChange, len(changes))
	for _, change := range changes {
		if change == nil {
			continue
		}
		filePath := cleanDiffPath(change.GetPath())
		if filePath == "" {
			return nil, status.Error(codes.InvalidArgument, "file content path is required")
		}
		if err := common.ValidateFileID(filePath); err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid file content path %s: %v", filePath, err))
		}
		byPath[filePath] = changesetFileContentChange{
			path:          filePath,
			content:       append([]byte(nil), change.GetContent()...),
			deleted:       change.GetDeleted(),
			executable:    change.GetExecutable(),
			symlinkTarget: strings.TrimSpace(change.GetSymlinkTarget()),
		}
	}
	if len(byPath) == 0 {
		return nil, nil
	}

	paths := make([]string, 0, len(byPath))
	for filePath := range byPath {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	normalized := make([]changesetFileContentChange, 0, len(paths))
	for _, filePath := range paths {
		normalized = append(normalized, byPath[filePath])
	}
	return normalized, nil
}

func mergeModifiedFilesWithFileContents(files []string, changes []changesetFileContentChange) []string {
	seen := make(map[string]struct{}, len(files)+len(changes))
	out := make([]string, 0, len(files)+len(changes))
	for _, filePath := range normalizeModifiedFiles(files) {
		cleaned := cleanDiffPath(filePath)
		if cleaned == "" {
			continue
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	for _, change := range changes {
		if change.path == "" {
			continue
		}
		if _, exists := seen[change.path]; exists {
			continue
		}
		seen[change.path] = struct{}{}
		out = append(out, change.path)
	}
	sort.Strings(out)
	return out
}

func (s *sliceServiceServer) applyChangesetFileContents(ctx context.Context, sliceID string, changes []changesetFileContentChange) error {
	for _, change := range changes {
		if change.deleted {
			if err := s.removeSliceFilePath(ctx, sliceID, change.path); err != nil {
				return err
			}
			continue
		}
		if err := s.upsertSliceFilePathWithMetadata(ctx, sliceID, change.path, "", change.content, change.executable, change.symlinkTarget); err != nil {
			return err
		}
	}
	return nil
}

func revertModifiedFiles(change *models.FileChangeRecord) []string {
	if change == nil {
		return nil
	}
	out := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	appendFile := func(path string) {
		cleaned := strings.TrimSpace(path)
		if cleaned == "" {
			return
		}
		if _, exists := seen[cleaned]; exists {
			return
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}

	appendFile(change.Path)
	if change.ChangeType == models.ChangeTypeRename {
		appendFile(change.OldPath)
	}
	if len(out) == 0 {
		appendFile(change.OldPath)
	}
	return out
}

func revertModifiedFilesForChanges(changes []*models.FileChangeRecord) []string {
	if len(changes) == 0 {
		return nil
	}
	out := make([]string, 0, len(changes))
	for _, change := range changes {
		out = append(out, revertModifiedFiles(change)...)
	}
	return out
}

func shortHash(hash string) string {
	cleaned := strings.TrimSpace(hash)
	if len(cleaned) > 12 {
		return cleaned[:12]
	}
	return cleaned
}

func buildRevertChangesetHash(commitHash, changeID string) string {
	encodedCommit := base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(commitHash)))
	selectedChange := strings.TrimSpace(changeID)
	if selectedChange == "" {
		selectedChange = revertAllChangesToken
	}
	encodedChange := base64.RawURLEncoding.EncodeToString([]byte(selectedChange))
	suffix := strings.TrimPrefix(common.GenerateChangesetVersionHash(), common.ChangesetVersionIDPrefix)
	return fmt.Sprintf("%s%s~%s~%s", revertChangesetHashPrefix, encodedCommit, encodedChange, suffix)
}

func parseRevertChangesetHash(hash string) (commitHash, changeID string, ok bool) {
	cleaned := strings.TrimSpace(hash)
	if !strings.HasPrefix(cleaned, revertChangesetHashPrefix) {
		return "", "", false
	}
	parts := strings.Split(cleaned, "~")
	if len(parts) < 4 {
		return "", "", false
	}
	commitBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", false
	}
	changeBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", false
	}
	commit := strings.TrimSpace(string(commitBytes))
	change := strings.TrimSpace(string(changeBytes))
	if change == revertAllChangesToken {
		change = ""
	}
	if commit == "" {
		return "", "", false
	}
	return commit, change, true
}

func isRevertChangesetHash(hash string) bool {
	_, _, ok := parseRevertChangesetHash(hash)
	return ok
}

func (s *sliceServiceServer) buildReviewChanges(ctx context.Context, cs *models.Changeset, snapshot *models.ChangesetSnapshot) ([]*filev1.FileChangeRecord, []string) {
	targetChanges, err := s.resolveRevertSourceChanges(ctx, cs)
	if err != nil {
		return nil, []string{err.Error()}
	}
	if len(targetChanges) == 0 {
		return s.buildStandardReviewChanges(ctx, cs, snapshot)
	}

	reviewChanges := make([]*filev1.FileChangeRecord, 0, len(targetChanges))
	missingPatchCount := 0
	for _, targetChange := range targetChanges {
		revertChange := buildRevertReviewChange(targetChange, cs)
		patch := s.buildChangePatchFromHashes(ctx, revertChange)
		if patch == "" {
			missingPatchCount++
		}
		reviewChanges = append(reviewChanges, modelToProtoReviewChange(revertChange, patch))
	}
	warnings := []string{}
	if missingPatchCount > 0 {
		warnings = append(warnings, fmt.Sprintf("inline patch unavailable for %d revert entries", missingPatchCount))
	}

	return reviewChanges, warnings
}

type reviewFileState struct {
	exists    bool
	lines     []string
	hash      string
	patchable bool
}

func (s *sliceServiceServer) buildStandardReviewChanges(ctx context.Context, cs *models.Changeset, snapshot *models.ChangesetSnapshot) ([]*filev1.FileChangeRecord, []string) {
	if cs == nil {
		return nil, nil
	}

	paths := normalizeModifiedFiles(cs.ModifiedFiles)
	if len(paths) == 0 {
		return nil, nil
	}
	if len(paths) > maxReviewPatchableChanges {
		return s.buildStandardReviewChangesFromHashes(ctx, cs, snapshot, paths)
	}

	reviewChanges := make([]*filev1.FileChangeRecord, 0, len(paths))
	warnings := make([]string, 0, 1)
	missingPatchCount := 0

	for _, rawPath := range paths {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}

		before, beforeWarning := s.loadReviewFileAtBase(ctx, cs, filePath)
		if beforeWarning != "" {
			warnings = append(warnings, beforeWarning)
		}
		after, afterWarning := s.loadReviewFileAtChangesetHead(ctx, cs, snapshot, filePath)
		if afterWarning != "" {
			warnings = append(warnings, afterWarning)
		}
		if !before.exists && !after.exists {
			continue
		}

		changeType := models.ChangeTypeModify
		switch {
		case !before.exists && after.exists:
			changeType = models.ChangeTypeAdd
		case before.exists && !after.exists:
			changeType = models.ChangeTypeDelete
		}

		if changeType == models.ChangeTypeModify && before.hash != "" && after.hash != "" && before.hash == after.hash {
			continue
		}

		patch := ""
		if before.patchable && after.patchable {
			patch = buildUnifiedPatchFromLines(filePath, filePath, before.lines, after.lines)
		} else {
			missingPatchCount++
		}
		linesAdded, linesDeleted := summarizePatchLineDelta(patch)

		reviewChanges = append(reviewChanges, modelToProtoReviewChange(&models.FileChangeRecord{
			ID:           common.GenerateFileChangeID(cs.ID, filePath),
			SliceID:      cs.SliceID,
			CommitHash:   cs.Hash,
			Path:         filePath,
			OldPath:      "",
			ChangeType:   changeType,
			OldHash:      before.hash,
			NewHash:      after.hash,
			LinesAdded:   linesAdded,
			LinesDeleted: linesDeleted,
			Author:       cs.Author,
			Message:      cs.Message,
			Timestamp:    cs.CreatedAt,
		}, patch))
	}

	if missingPatchCount > 0 {
		warnings = append(warnings, fmt.Sprintf("inline patch unavailable for %d changeset entries", missingPatchCount))
	}
	return reviewChanges, warnings
}

func (s *sliceServiceServer) buildStandardReviewChangesFromHashes(ctx context.Context, cs *models.Changeset, snapshot *models.ChangesetSnapshot, paths []string) ([]*filev1.FileChangeRecord, []string) {
	if cs == nil {
		return nil, nil
	}

	warnings := []string{fmt.Sprintf("inline patches skipped for %d changeset entries", len(paths))}

	baseHashes := map[string]string{}
	if baseCommitHash := strings.TrimSpace(cs.BaseCommitHash); baseCommitHash != "" {
		baseSnapshot, err := s.storage.GetCommitSnapshot(ctx, baseCommitHash)
		if err != nil {
			if !errors.Is(err, storage.ErrCommitNotFound) {
				warnings = append(warnings, fmt.Sprintf("base snapshot lookup failed: %v", err))
			}
		} else if baseSnapshot != nil && baseSnapshot.Files != nil {
			baseHashes = baseSnapshot.Files
		}
	}

	afterHashes, afterExists, afterWarnings := s.loadChangesetHeadHashes(ctx, cs, snapshot, paths)
	warnings = append(warnings, afterWarnings...)

	reviewChanges := make([]*filev1.FileChangeRecord, 0, len(paths))
	for _, rawPath := range paths {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}

		beforeHash := strings.TrimSpace(baseHashes[filePath])
		beforeExists := beforeHash != ""
		afterHash := strings.TrimSpace(afterHashes[filePath])
		existsAfter := afterExists[filePath] && afterHash != ""
		if !beforeExists && !existsAfter {
			continue
		}

		changeType := models.ChangeTypeModify
		switch {
		case !beforeExists && existsAfter:
			changeType = models.ChangeTypeAdd
		case beforeExists && !existsAfter:
			changeType = models.ChangeTypeDelete
		}
		if changeType == models.ChangeTypeModify && beforeHash == afterHash {
			continue
		}

		reviewChanges = append(reviewChanges, modelToProtoReviewChange(&models.FileChangeRecord{
			ID:         common.GenerateFileChangeID(cs.ID, filePath),
			SliceID:    cs.SliceID,
			CommitHash: cs.Hash,
			Path:       filePath,
			OldPath:    "",
			ChangeType: changeType,
			OldHash:    beforeHash,
			NewHash:    afterHash,
			Author:     cs.Author,
			Message:    cs.Message,
			Timestamp:  cs.CreatedAt,
		}, ""))
	}

	return reviewChanges, warnings
}

func (s *sliceServiceServer) loadChangesetHeadHashes(ctx context.Context, cs *models.Changeset, snapshot *models.ChangesetSnapshot, paths []string) (map[string]string, map[string]bool, []string) {
	hashes := make(map[string]string, len(paths))
	exists := make(map[string]bool, len(paths))
	if snapshot != nil && snapshot.FileHashes != nil {
		for _, rawPath := range paths {
			filePath := cleanDiffPath(rawPath)
			if filePath == "" {
				continue
			}
			hash := strings.TrimSpace(snapshot.FileHashes[filePath])
			if hash == "" {
				continue
			}
			hashes[filePath] = hash
			exists[filePath] = true
		}
		return hashes, exists, nil
	}
	if cs == nil {
		return hashes, exists, nil
	}

	warnings := make([]string, 0, 2)
	manifestHashes, err := getFileManifestHashes(ctx, s.storage, cs.SliceID, paths)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("changeset head hash lookup failed: %v", err))
		return hashes, exists, warnings
	}
	existingEntries, err := getExistingEntriesByPaths(ctx, s.storage, cs.SliceID, paths)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("changeset head entry lookup failed: %v", err))
		return hashes, exists, warnings
	}
	for _, rawPath := range paths {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" || !existingEntries[filePath] {
			continue
		}
		hash := strings.TrimSpace(manifestHashes[filePath])
		if hash == "" {
			continue
		}
		hashes[filePath] = hash
		exists[filePath] = true
	}
	return hashes, exists, warnings
}

func (s *sliceServiceServer) loadReviewFileAtBase(ctx context.Context, cs *models.Changeset, filePath string) (reviewFileState, string) {
	state := reviewFileState{
		exists:    false,
		lines:     []string{},
		hash:      "",
		patchable: true,
	}
	if cs == nil || strings.TrimSpace(cs.BaseCommitHash) == "" {
		return state, ""
	}

	content, err := s.storage.GetFileAtCommit(ctx, strings.TrimSpace(cs.BaseCommitHash), filePath)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) || errors.Is(err, storage.ErrCommitNotFound) {
			return state, ""
		}
		return state, fmt.Sprintf("base lookup failed for %s: %v", filePath, err)
	}

	state.exists = true
	state.lines, state.hash, state.patchable = s.extractDiffLinesFromContent(ctx, filePath, content)
	return state, ""
}

func (s *sliceServiceServer) loadReviewFileAtChangesetHead(ctx context.Context, cs *models.Changeset, snapshot *models.ChangesetSnapshot, filePath string) (reviewFileState, string) {
	if snapshot != nil && snapshot.FileHashes != nil {
		return s.loadReviewFileAtSnapshot(ctx, snapshot, filePath)
	}
	return s.loadReviewFileAtHead(ctx, cs, filePath)
}

func (s *sliceServiceServer) loadReviewFileAtSnapshot(ctx context.Context, snapshot *models.ChangesetSnapshot, filePath string) (reviewFileState, string) {
	state := reviewFileState{
		exists:    false,
		lines:     []string{},
		hash:      "",
		patchable: true,
	}
	if snapshot == nil {
		return state, ""
	}

	hash := strings.TrimSpace(snapshot.FileHashes[cleanDiffPath(filePath)])
	if hash == "" {
		return state, ""
	}
	content, err := storage.ReadVersionedFileContent(ctx, s.storage, hash)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) {
			return state, ""
		}
		return state, fmt.Sprintf("snapshot lookup failed for %s: %v", filePath, err)
	}

	state.exists = true
	state.lines, state.hash, state.patchable = s.extractDiffLinesFromContent(ctx, filePath, content)
	return state, ""
}

func (s *sliceServiceServer) loadReviewFileAtHead(ctx context.Context, cs *models.Changeset, filePath string) (reviewFileState, string) {
	state := reviewFileState{
		exists:    false,
		lines:     []string{},
		hash:      "",
		patchable: true,
	}
	if cs == nil {
		return state, ""
	}

	content, err := storage.ReadSliceFileContent(ctx, s.storage, strings.TrimSpace(cs.SliceID), filePath)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) {
			return state, ""
		}
		return state, fmt.Sprintf("head lookup failed for %s: %v", filePath, err)
	}

	state.exists = true
	state.lines, state.hash, state.patchable = s.extractDiffLinesFromContent(ctx, filePath, content)
	return state, ""
}

func (s *sliceServiceServer) extractDiffLinesFromContent(ctx context.Context, filePath string, content *models.FileContent) ([]string, string, bool) {
	if content == nil {
		return []string{}, "", true
	}

	fileHash := strings.TrimSpace(content.Hash)
	fileBytes := content.Content
	if len(fileBytes) == 0 && content.Size > 0 && fileHash != "" {
		versioned, err := storage.ReadVersionedFileContent(ctx, s.storage, fileHash)
		if err == nil && versioned != nil {
			fileBytes = versioned.Content
			if fileHash == "" {
				fileHash = strings.TrimSpace(versioned.Hash)
			}
		}
	}

	if !isUsableContentHash(filePath, fileHash) {
		if fileBytes == nil && content.Size > 0 {
			return nil, "", false
		}
		fileHash = hashBytes(fileBytes)
	}

	if fileBytes == nil {
		fileBytes = []byte{}
	}
	if !utf8.Valid(fileBytes) || bytesContainsNUL(fileBytes) {
		return nil, fileHash, false
	}
	lines := strings.SplitAfter(string(fileBytes), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, fileHash, true
}

func (s *sliceServiceServer) resolveRevertSourceChanges(ctx context.Context, cs *models.Changeset) ([]*models.FileChangeRecord, error) {
	if cs == nil {
		return nil, nil
	}

	commitHash, changeID, ok := parseRevertChangesetHash(cs.Hash)
	if !ok {
		return nil, nil
	}

	changes, err := s.storage.GetCommitChanges(ctx, commitHash)
	if err != nil {
		return nil, fmt.Errorf("revert source commit %s could not be loaded", shortHash(commitHash))
	}

	targetChanges := make([]*models.FileChangeRecord, 0, len(changes))
	if changeID == "" {
		for _, change := range changes {
			if change == nil {
				continue
			}
			if strings.TrimSpace(change.SliceID) == strings.TrimSpace(cs.SliceID) {
				targetChanges = append(targetChanges, change)
			}
		}
		if len(targetChanges) == 0 {
			return nil, fmt.Errorf("revert source commit has no changes for this slice")
		}
		resolved := make([]*models.FileChangeRecord, 0, len(targetChanges))
		for _, change := range targetChanges {
			resolved = append(resolved, s.fillMissingChangeHashes(ctx, change))
		}
		return resolved, nil
	}

	for _, change := range changes {
		if change != nil && change.ID == changeID {
			targetChanges = append(targetChanges, change)
			break
		}
	}
	if len(targetChanges) == 0 {
		return nil, fmt.Errorf("revert source change was not found")
	}
	resolved := make([]*models.FileChangeRecord, 0, len(targetChanges))
	for _, change := range targetChanges {
		resolved = append(resolved, s.fillMissingChangeHashes(ctx, change))
	}
	return resolved, nil
}

func (s *sliceServiceServer) applyRevertChangesetContent(ctx context.Context, cs *models.Changeset) ([]*models.FileChangeRecord, error) {
	targetChanges, err := s.resolveRevertSourceChanges(ctx, cs)
	if err != nil {
		return nil, err
	}
	if len(targetChanges) == 0 {
		return nil, nil
	}

	applied := make([]*models.FileChangeRecord, 0, len(targetChanges))
	for _, targetChange := range targetChanges {
		revertChange := buildRevertReviewChange(targetChange, cs)
		if revertChange == nil {
			continue
		}
		if err := s.applyRevertChangeToSlice(ctx, cs.SliceID, revertChange); err != nil {
			return nil, fmt.Errorf("failed to apply revert for %s: %w", targetChange.Path, err)
		}
		applied = append(applied, revertChange)
	}
	return applied, nil
}

func (s *sliceServiceServer) applyRevertChangeToSlice(ctx context.Context, sliceID string, change *models.FileChangeRecord) error {
	if change == nil {
		return nil
	}
	pathAfter := cleanDiffPath(change.Path)
	if pathAfter == "" {
		return fmt.Errorf("revert path is empty")
	}

	if change.ChangeType == models.ChangeTypeDelete {
		return s.removeSliceFilePath(ctx, sliceID, pathAfter)
	}

	pathBefore := cleanDiffPath(change.OldPath)
	if pathBefore != "" && pathBefore != pathAfter {
		if err := s.removeSliceFilePath(ctx, sliceID, pathBefore); err != nil {
			return err
		}
	}
	if strings.TrimSpace(change.NewHash) == "" {
		return fmt.Errorf("revert source missing previous content hash for %s", pathAfter)
	}

	content, err := s.loadRevertContentByHash(ctx, change.NewHash)
	if err != nil {
		return err
	}
	return s.upsertSliceFilePath(ctx, sliceID, pathAfter, strings.TrimSpace(change.NewHash), content)
}

func (s *sliceServiceServer) fillMissingChangeHashes(ctx context.Context, source *models.FileChangeRecord) *models.FileChangeRecord {
	if source == nil {
		return nil
	}

	change := *source
	oldHash := strings.TrimSpace(change.OldHash)
	newHash := strings.TrimSpace(change.NewHash)

	if newHash == "" && change.ChangeType != models.ChangeTypeDelete {
		if snapshot, err := s.storage.GetCommitSnapshot(ctx, strings.TrimSpace(change.CommitHash)); err == nil && snapshot != nil {
			newHash = snapshotHashForPath(snapshot, change.Path)
		}
	}

	if oldHash == "" && change.ChangeType != models.ChangeTypeAdd {
		if commit, err := s.storage.GetCommitByHash(ctx, strings.TrimSpace(change.SliceID), strings.TrimSpace(change.CommitHash)); err == nil && commit != nil {
			parentHash := strings.TrimSpace(commit.ParentHash)
			if parentHash != "" {
				if parentSnapshot, snapErr := s.storage.GetCommitSnapshot(ctx, parentHash); snapErr == nil && parentSnapshot != nil {
					lookupPath := change.Path
					if change.ChangeType == models.ChangeTypeRename {
						lookupPath = change.OldPath
					}
					oldHash = snapshotHashForPath(parentSnapshot, lookupPath)
				}
			}
		}
	}

	if oldHash == "" && change.ChangeType != models.ChangeTypeAdd {
		historyPath := change.Path
		if change.ChangeType == models.ChangeTypeRename {
			historyPath = change.OldPath
		}
		oldHash = s.findPreviousKnownFileHash(ctx, change.SliceID, historyPath, change.CommitHash)
	}

	change.OldHash = oldHash
	change.NewHash = newHash
	return &change
}

func snapshotHashForPath(snapshot *models.CommitSnapshot, filePath string) string {
	if snapshot == nil {
		return ""
	}
	pathKey := cleanDiffPath(filePath)
	if pathKey == "" {
		return ""
	}
	candidate := strings.TrimSpace(snapshot.Files[pathKey])
	if !isUsableContentHash(pathKey, candidate) {
		return ""
	}
	return candidate
}

func isUsableContentHash(filePath, hash string) bool {
	cleanedHash := strings.TrimSpace(hash)
	if cleanedHash == "" {
		return false
	}
	cleanedPath := cleanDiffPath(filePath)
	if cleanedPath != "" && cleanedHash == cleanedPath {
		return false
	}
	return !strings.Contains(cleanedHash, "/")
}

func (s *sliceServiceServer) findPreviousKnownFileHash(ctx context.Context, sliceID, filePath, fromCommit string) string {
	return s.findPreviousKnownFileHashWithStorage(ctx, s.storage, sliceID, filePath, fromCommit)
}

func (s *sliceServiceServer) findPreviousKnownFileHashWithStorage(ctx context.Context, st storage.Storage, sliceID, filePath, fromCommit string) string {
	cleanedPath := cleanDiffPath(filePath)
	if cleanedPath == "" {
		return ""
	}
	history, err := st.GetFileHistory(ctx, strings.TrimSpace(sliceID), cleanedPath, 64, strings.TrimSpace(fromCommit))
	if err != nil {
		return ""
	}
	for _, item := range history {
		if item == nil {
			continue
		}
		candidate := strings.TrimSpace(item.NewHash)
		if isUsableContentHash(cleanedPath, candidate) {
			return candidate
		}
	}
	return ""
}

func (s *sliceServiceServer) loadRevertContentByHash(ctx context.Context, contentHash string) ([]byte, error) {
	hash := strings.TrimSpace(contentHash)
	if hash == "" {
		return []byte{}, nil
	}
	content, err := storage.ReadVersionedFileContent(ctx, s.storage, hash)
	if err != nil || content == nil {
		return nil, fmt.Errorf("content hash %s not found", shortHash(hash))
	}
	return append([]byte(nil), content.Content...), nil
}

func (s *sliceServiceServer) upsertSliceFilePath(ctx context.Context, sliceID, filePath, contentHash string, data []byte) error {
	return s.upsertSliceFilePathWithMetadata(ctx, sliceID, filePath, contentHash, data, false, "")
}

func (s *sliceServiceServer) upsertSliceFilePathWithMetadata(ctx context.Context, sliceID, filePath, contentHash string, data []byte, executable bool, symlinkTarget string) error {
	cleanedPath := cleanDiffPath(filePath)
	if cleanedPath == "" {
		return fmt.Errorf("file path is empty")
	}

	manifest, err := storage.WriteSliceFileManifestWithMetadata(ctx, s.storage, sliceID, cleanedPath, append([]byte(nil), data...), executable, symlinkTarget)
	if err != nil {
		return fmt.Errorf("failed to upsert file content: %w", err)
	}
	if expected := strings.TrimSpace(contentHash); expected != "" && expected != strings.TrimSpace(manifest.Hash) {
		return fmt.Errorf("content hash mismatch for %s", cleanedPath)
	}

	if err := s.storage.AddEntry(ctx, &models.DirectoryEntry{
		ID:            fmt.Sprintf("%s:%s", sliceID, cleanedPath),
		Path:          cleanedPath,
		Type:          "file",
		ParentID:      sliceID,
		Size:          int64(len(data)),
		Hash:          manifest.Hash,
		Executable:    executable,
		SymlinkTarget: symlinkTarget,
	}); err != nil {
		return fmt.Errorf("failed to upsert file entry: %w", err)
	}

	if err := s.storage.AddFileToSlice(ctx, cleanedPath, sliceID); err != nil {
		return fmt.Errorf("failed to mark file ownership: %w", err)
	}
	return nil
}

func (s *sliceServiceServer) removeSliceFilePath(ctx context.Context, sliceID, filePath string) error {
	cleanedPath := cleanDiffPath(filePath)
	if cleanedPath == "" {
		return nil
	}

	entry, err := s.storage.GetEntryByPath(ctx, sliceID, cleanedPath)
	if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
		return fmt.Errorf("failed to lookup existing entry: %w", err)
	}
	if err == nil && entry != nil {
		if delErr := s.storage.DeleteEntry(ctx, entry.ID); delErr != nil && !errors.Is(delErr, storage.ErrEntryNotFound) {
			return fmt.Errorf("failed to delete entry: %w", delErr)
		}
	}

	if err := s.storage.RemoveFileFromSlice(ctx, cleanedPath, sliceID); err != nil {
		return fmt.Errorf("failed to clear file ownership: %w", err)
	}
	return nil
}

func buildRevertReviewChange(target *models.FileChangeRecord, cs *models.Changeset) *models.FileChangeRecord {
	if target == nil {
		return nil
	}

	revertPath := target.Path
	revertOldPath := ""
	if target.ChangeType == models.ChangeTypeRename {
		revertPath = strings.TrimSpace(target.OldPath)
		if revertPath == "" {
			revertPath = target.Path
		}
		revertOldPath = target.Path
	}

	return &models.FileChangeRecord{
		ID:           common.GenerateFileChangeID(cs.ID, target.ID),
		SliceID:      cs.SliceID,
		CommitHash:   cs.Hash,
		Path:         revertPath,
		OldPath:      revertOldPath,
		ChangeType:   invertChangeType(target.ChangeType),
		OldHash:      target.NewHash,
		NewHash:      target.OldHash,
		LinesAdded:   target.LinesDeleted,
		LinesDeleted: target.LinesAdded,
		Author:       cs.Author,
		Message:      cs.Message,
		Timestamp:    cs.CreatedAt,
	}
}

func invertChangeType(changeType models.ChangeType) models.ChangeType {
	switch changeType {
	case models.ChangeTypeAdd:
		return models.ChangeTypeDelete
	case models.ChangeTypeDelete:
		return models.ChangeTypeAdd
	default:
		return changeType
	}
}

func (s *sliceServiceServer) buildChangePatchFromHashes(ctx context.Context, change *models.FileChangeRecord) string {
	if change == nil {
		return ""
	}

	beforeLines, beforeOK := s.loadDiffLinesFromHash(ctx, change.OldHash)
	if !beforeOK {
		return ""
	}
	afterLines, afterOK := s.loadDiffLinesFromHash(ctx, change.NewHash)
	if !afterOK {
		return ""
	}
	return buildUnifiedPatchFromLines(change.OldPath, change.Path, beforeLines, afterLines)
}

func buildUnifiedPatchFromLines(oldPath, newPath string, beforeLines, afterLines []string) string {
	if len(beforeLines) == 0 && len(afterLines) == 0 {
		return ""
	}

	newPath = cleanDiffPath(newPath)
	oldPath = cleanDiffPath(oldPath)
	if oldPath == "" {
		oldPath = newPath
	}
	if newPath == "" {
		newPath = oldPath
	}
	if oldPath == "" {
		oldPath = "unknown"
	}
	if newPath == "" {
		newPath = "unknown"
	}

	patch, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        beforeLines,
		B:        afterLines,
		FromFile: "a/" + oldPath,
		ToFile:   "b/" + newPath,
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return patch
}

func summarizePatchLineDelta(patch string) (added int, deleted int) {
	if strings.TrimSpace(patch) == "" {
		return 0, 0
	}
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "@@"):
			continue
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			deleted++
		}
	}
	return added, deleted
}

func (s *sliceServiceServer) loadDiffLinesFromHash(ctx context.Context, hash string) ([]string, bool) {
	cleaned := strings.TrimSpace(hash)
	if cleaned == "" {
		return []string{}, true
	}
	content, err := storage.ReadVersionedFileContent(ctx, s.storage, cleaned)
	if err != nil || content == nil {
		return []string{}, true
	}
	if !utf8.Valid(content.Content) || bytesContainsNUL(content.Content) {
		return nil, false
	}
	lines := strings.SplitAfter(string(content.Content), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, true
}

func cleanDiffPath(raw string) string {
	cleaned := strings.Trim(strings.TrimSpace(raw), "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func bytesContainsNUL(content []byte) bool {
	for _, b := range content {
		if b == 0 {
			return true
		}
	}
	return false
}

func modelToProtoReviewChange(change *models.FileChangeRecord, patch string) *filev1.FileChangeRecord {
	if change == nil {
		return nil
	}
	return &filev1.FileChangeRecord{
		Id:           change.ID,
		SliceId:      change.SliceID,
		CommitHash:   change.CommitHash,
		Path:         change.Path,
		OldPath:      change.OldPath,
		ChangeType:   modelChangeTypeToProto(change.ChangeType),
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

func modelChangeTypeToProto(changeType models.ChangeType) filev1.ChangeType {
	switch changeType {
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

func summarizeReviewChanges(changes []*filev1.FileChangeRecord) *slicev1.DiffSummary {
	summary := &slicev1.DiffSummary{}
	for _, change := range changes {
		if change == nil {
			continue
		}
		switch change.ChangeType {
		case filev1.ChangeType_CHANGE_TYPE_ADD:
			summary.FilesAdded++
		case filev1.ChangeType_CHANGE_TYPE_DELETE:
			summary.FilesDeleted++
		default:
			summary.FilesModified++
		}
		summary.LinesAdded += int64(change.GetLinesAdded())
		summary.LinesRemoved += int64(change.GetLinesDeleted())
	}
	return summary
}

func (s *sliceServiceServer) createChangesetSnapshot(ctx context.Context, cs *models.Changeset) error {
	if cs == nil {
		return nil
	}

	snapshots, err := s.storage.ListChangesetSnapshots(ctx, cs.ID, 1)
	if err != nil {
		return err
	}

	nextVersion := int32(1)
	if len(snapshots) > 0 && snapshots[0] != nil && snapshots[0].Version > 0 {
		nextVersion = snapshots[0].Version + 1
	}
	modifiedFiles := normalizeModifiedFiles(cs.ModifiedFiles)
	fileHashes, err := getFileManifestHashes(ctx, s.storage, cs.SliceID, modifiedFiles)
	if err != nil {
		return err
	}
	existingEntries, err := getExistingEntriesByPaths(ctx, s.storage, cs.SliceID, modifiedFiles)
	if err != nil {
		return err
	}
	snapshotFileHashes := make(map[string]string, len(fileHashes))
	for _, rawPath := range modifiedFiles {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" || !existingEntries[filePath] {
			continue
		}
		snapshotFileHashes[filePath] = strings.TrimSpace(fileHashes[filePath])
	}
	basePathVersions, err := s.changesetBasePathVersions(ctx, cs, modifiedFiles)
	if err != nil {
		return err
	}

	snapshot := &models.ChangesetSnapshot{
		ID:               common.GenerateChangesetSnapshotID(cs.ID, int64(nextVersion)),
		ChangesetID:      cs.ID,
		Version:          nextVersion,
		Hash:             cs.Hash,
		BaseCommitHash:   cs.BaseCommitHash,
		ModifiedFiles:    modifiedFiles,
		FileHashes:       normalizeSnapshotFileHashes(snapshotFileHashes),
		BasePathVersions: normalizeSnapshotBasePathVersions(basePathVersions),
		Author:           cs.Author,
		Message:          cs.Message,
		CreatedAt:        time.Now(),
	}
	return s.storage.CreateChangesetSnapshot(ctx, snapshot)
}

func (s *sliceServiceServer) enqueueChangesetExportCI(ctx context.Context, changesetID string, username string) (string, string) {
	resp, enabled, err := ciservice.EnqueueChangesetExportRun(ctx, s.storage, changesetID, username)
	if err != nil {
		log.Printf("failed to enqueue changeset export CI for %s: %v", changesetID, err)
		return "", "error"
	}
	if !enabled || resp == nil {
		return "", ""
	}
	return resp.GetRunId(), resp.GetStatus()
}

func (s *sliceServiceServer) changesetBasePathVersions(ctx context.Context, cs *models.Changeset, modifiedFiles []string) (map[string]int64, error) {
	headStore, ok := s.storage.(storage.HomePathHeadStore)
	if !ok || cs == nil {
		return nil, nil
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, err
	}
	homeID := mergeEventHomeID(slice, cs, modifiedFiles)
	if strings.TrimSpace(homeID) == "" {
		return nil, nil
	}
	paths := normalizeModifiedFiles(modifiedFiles)
	heads, err := headStore.GetHomePathHeads(ctx, homeID, paths)
	if err != nil {
		return nil, err
	}
	baseVersions := make(map[string]int64, len(paths))
	missingHeadPaths := make([]string, 0)
	for _, rawPath := range paths {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}
		version := int64(0)
		if head := heads[filePath]; head != nil && head.PathVersion > 0 {
			version = head.PathVersion
		} else {
			missingHeadPaths = append(missingHeadPaths, filePath)
		}
		baseVersions[filePath] = version
	}
	if len(missingHeadPaths) > 0 {
		synthesized, err := s.synthesizeBasePathHeadsFromCommit(ctx, headStore, homeID, cs, missingHeadPaths)
		if err != nil {
			return nil, err
		}
		for filePath, version := range synthesized {
			baseVersions[filePath] = version
		}
	}
	if len(baseVersions) == 0 {
		return nil, nil
	}
	return baseVersions, nil
}

func (s *sliceServiceServer) synthesizeBasePathHeadsFromCommit(ctx context.Context, headStore storage.HomePathHeadStore, homeID string, cs *models.Changeset, paths []string) (map[string]int64, error) {
	result := make(map[string]int64)
	if headStore == nil || cs == nil || strings.TrimSpace(homeID) == "" {
		return result, nil
	}
	baseCommitHash := strings.TrimSpace(cs.BaseCommitHash)
	if baseCommitHash == "" {
		return result, nil
	}
	snapshot, err := s.storage.GetCommitSnapshot(ctx, baseCommitHash)
	if err != nil {
		if errors.Is(err, storage.ErrCommitNotFound) {
			return result, nil
		}
		return nil, err
	}
	if snapshot == nil || len(snapshot.Files) == 0 {
		return result, nil
	}

	heads := make([]*models.HomePathHead, 0, len(paths))
	for _, rawPath := range paths {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}
		manifestHash := strings.TrimSpace(snapshot.Files[filePath])
		if manifestHash == "" {
			continue
		}
		heads = append(heads, &models.HomePathHead{
			HomeID:           homeID,
			Path:             filePath,
			PathVersion:      1,
			ContentHash:      manifestHash,
			ManifestHash:     manifestHash,
			SourceSliceID:    cs.SliceID,
			SourceCommitHash: baseCommitHash,
			LastMergeSeq:     0,
			UpdatedAt:        time.Now(),
		})
		result[filePath] = 1
	}
	if len(heads) == 0 {
		return result, nil
	}
	if err := headStore.UpsertHomePathHeads(ctx, heads); err != nil {
		return nil, err
	}
	return result, nil
}

func buildSyntheticChangesetSnapshot(cs *models.Changeset) *models.ChangesetSnapshot {
	if cs == nil {
		return nil
	}
	createdAt := cs.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return &models.ChangesetSnapshot{
		ID:               common.GenerateChangesetSnapshotID(cs.ID, 1),
		ChangesetID:      cs.ID,
		Version:          1,
		Hash:             cs.Hash,
		BaseCommitHash:   cs.BaseCommitHash,
		ModifiedFiles:    normalizeModifiedFiles(cs.ModifiedFiles),
		FileHashes:       nil,
		BasePathVersions: nil,
		Author:           cs.Author,
		Message:          cs.Message,
		CreatedAt:        createdAt,
	}
}

func (s *sliceServiceServer) resolveChangesetSnapshotForStream(ctx context.Context, cs *models.Changeset, version int32, hash string) (*models.ChangesetSnapshot, error) {
	if cs == nil {
		return nil, status.Error(codes.InvalidArgument, "changeset is required")
	}
	hash = strings.TrimSpace(hash)
	if hash == "" {
		snapshot, err := s.storage.GetChangesetSnapshot(ctx, cs.ID, version)
		if err == nil && snapshot != nil {
			return snapshot, nil
		}
		if version > 0 {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset snapshot version %d not found", version))
		}
		return nil, status.Error(codes.FailedPrecondition, "changeset snapshot content references are unavailable")
	}

	snapshot, err := s.storage.GetChangesetSnapshotByHash(ctx, cs.ID, hash)
	if err != nil {
		if errors.Is(err, storage.ErrChangesetNotFound) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset snapshot hash not found: %s", hash))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load changeset snapshot by hash: %v", err))
	}
	if version > 0 && snapshot.Version != version {
		return nil, status.Error(codes.InvalidArgument, "snapshot_hash and snapshot_version refer to different snapshots")
	}
	return snapshot, nil
}

func (s *sliceServiceServer) buildChangesetSnapshotStreamManifest(ctx context.Context, snapshot *models.ChangesetSnapshot, paths []string) ([]*slicev1.FileMetadata, []string, error) {
	if snapshot == nil {
		return nil, nil, status.Error(codes.InvalidArgument, "snapshot is required")
	}
	if len(paths) == 0 {
		return nil, nil, nil
	}
	if snapshot.FileHashes == nil {
		return nil, nil, status.Error(codes.FailedPrecondition, "changeset snapshot content references are unavailable")
	}

	modifiedSet := make(map[string]struct{}, len(snapshot.ModifiedFiles))
	for _, path := range normalizeModifiedFiles(snapshot.ModifiedFiles) {
		modifiedSet[path] = struct{}{}
	}
	fileMetadata := make([]*slicev1.FileMetadata, 0, len(paths))
	deletedPaths := make([]string, 0)
	for _, rawPath := range paths {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}
		if _, ok := modifiedSet[filePath]; !ok {
			continue
		}
		manifestHash := strings.TrimSpace(snapshot.FileHashes[filePath])
		if manifestHash == "" {
			deletedPaths = append(deletedPaths, filePath)
			continue
		}
		manifest, err := s.storage.GetVersionedFileManifest(ctx, manifestHash)
		if err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				return nil, nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("changeset snapshot content for %s is missing manifest %s", filePath, shortHash(manifestHash)))
			}
			return nil, nil, status.Error(codes.Internal, fmt.Sprintf("failed to load snapshot manifest %s: %v", shortHash(manifestHash), err))
		}
		if manifest == nil {
			return nil, nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("changeset snapshot content for %s is missing manifest %s", filePath, shortHash(manifestHash)))
		}
		fileMetadata = append(fileMetadata, &slicev1.FileMetadata{
			FileId:        filePath,
			Path:          filePath,
			Size:          manifest.TotalSize,
			Hash:          strings.TrimSpace(manifest.Hash),
			Blocks:        checkoutProtoBlocks(manifest.Blocks),
			Executable:    manifest.Executable,
			SymlinkTarget: manifest.SymlinkTarget,
		})
	}
	sort.Slice(fileMetadata, func(i, j int) bool {
		return fileMetadata[i].GetPath() < fileMetadata[j].GetPath()
	})
	sort.Strings(deletedPaths)
	return fileMetadata, normalizeModifiedFiles(deletedPaths), nil
}

func sendChangesetSnapshotManifestChunks(stream slicev1.SliceService_StreamChangesetSnapshotServer, snapshot *models.ChangesetSnapshot, sliceID string, fileMetadata []*slicev1.FileMetadata, deletedPaths []string) error {
	if len(fileMetadata) == 0 {
		return stream.Send(&slicev1.ChangesetSnapshotChunk{
			Chunk: &slicev1.ChangesetSnapshotChunk_Manifest{
				Manifest: &slicev1.ChangesetSnapshotManifest{
					Snapshot:     changesetSnapshotToProto(snapshot),
					DeletedPaths: deletedPaths,
					SliceId:      strings.TrimSpace(sliceID),
				},
			},
		})
	}

	for start := 0; start < len(fileMetadata); start += checkoutManifestChunkSize {
		end := start + checkoutManifestChunkSize
		if end > len(fileMetadata) {
			end = len(fileMetadata)
		}
		manifest := &slicev1.ChangesetSnapshotManifest{
			Snapshot:     changesetSnapshotToProto(snapshot),
			FileMetadata: fileMetadata[start:end],
			SliceId:      strings.TrimSpace(sliceID),
		}
		if start == 0 {
			manifest.DeletedPaths = deletedPaths
		}
		if err := stream.Send(&slicev1.ChangesetSnapshotChunk{
			Chunk: &slicev1.ChangesetSnapshotChunk_Manifest{Manifest: manifest},
		}); err != nil {
			return err
		}
	}
	return nil
}

func normalizeSnapshotFileHashes(fileHashes map[string]string) map[string]string {
	out := make(map[string]string, len(fileHashes))
	for rawPath, rawHash := range fileHashes {
		path := cleanDiffPath(rawPath)
		hash := strings.TrimSpace(rawHash)
		if path == "" || !isUsableContentHash(path, hash) {
			continue
		}
		out[path] = hash
	}
	return out
}

func normalizeSnapshotBasePathVersions(baseVersions map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(baseVersions))
	for rawPath, version := range baseVersions {
		path := cleanDiffPath(rawPath)
		if path == "" || version < 0 {
			continue
		}
		out[path] = version
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *sliceServiceServer) resolveChangesetSnapshotForReview(ctx context.Context, cs *models.Changeset, requestedVersion int32) (*models.ChangesetSnapshot, error) {
	if cs == nil {
		return nil, fmt.Errorf("changeset is required")
	}

	snapshot, err := s.storage.GetChangesetSnapshot(ctx, cs.ID, requestedVersion)
	if err == nil && snapshot != nil {
		return snapshot, nil
	}
	if requestedVersion > 0 {
		return nil, fmt.Errorf("changeset snapshot version %d not found", requestedVersion)
	}
	return buildSyntheticChangesetSnapshot(cs), nil
}

func applySnapshotToChangeset(cs *models.Changeset, snapshot *models.ChangesetSnapshot) *models.Changeset {
	if cs == nil {
		return nil
	}
	copyCS := *cs
	if snapshot == nil {
		copyCS.ModifiedFiles = normalizeModifiedFiles(copyCS.ModifiedFiles)
		return &copyCS
	}
	copyCS.Hash = strings.TrimSpace(snapshot.Hash)
	copyCS.BaseCommitHash = strings.TrimSpace(snapshot.BaseCommitHash)
	copyCS.ModifiedFiles = normalizeModifiedFiles(snapshot.ModifiedFiles)
	copyCS.Author = snapshot.Author
	copyCS.Message = snapshot.Message
	return &copyCS
}

func changesetSnapshotToProto(snapshot *models.ChangesetSnapshot) *slicev1.ChangesetSnapshotInfo {
	if snapshot == nil {
		return nil
	}
	return &slicev1.ChangesetSnapshotInfo{
		SnapshotId:        snapshot.ID,
		ChangesetId:       snapshot.ChangesetID,
		Version:           snapshot.Version,
		Hash:              snapshot.Hash,
		BaseCommitHash:    snapshot.BaseCommitHash,
		ModifiedFiles:     normalizeModifiedFiles(snapshot.ModifiedFiles),
		Author:            snapshot.Author,
		Message:           snapshot.Message,
		CreatedAt:         snapshot.CreatedAt.Unix(),
		ModifiedFileCount: int32(changesetSnapshotModifiedFileCount(snapshot)),
	}
}

func (s *sliceServiceServer) RebaseChangeset(ctx context.Context, req *slicev1.RebaseChangesetRequest) (*slicev1.RebaseChangesetResponse, error) {
	log.Printf("RebaseChangeset called: changeset_id=%s", req.ChangesetId)

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	cs, err := s.storage.GetChangeset(ctx, req.ChangesetId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", req.ChangesetId))
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", cs.SliceID))
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	metadata, err := s.storage.GetSliceMetadata(ctx, cs.SliceID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice metadata: %v", err))
	}
	newBase := ""
	if metadata != nil {
		newBase = strings.TrimSpace(metadata.HeadCommitHash)
	}
	if newBase == "" {
		return nil, status.Error(codes.FailedPrecondition, "slice head is empty")
	}
	cs.BaseCommitHash = newBase
	if err := s.storage.UpdateChangeset(ctx, cs); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update changeset: %v", err))
	}
	if err := s.createChangesetSnapshot(ctx, cs); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to save changeset snapshot: %v", err))
	}

	return &slicev1.RebaseChangesetResponse{
		Status:              slicev1.RebaseStatus_REBASE_STATUS_SUCCESS,
		NewBaseCommitHash:   newBase,
		SliceCommitsToApply: []string{},
		Conflicts:           []*slicev1.Conflict{},
	}, nil
}

func (s *sliceServiceServer) GetSliceCommits(ctx context.Context, req *slicev1.CommitHistoryRequest) (*slicev1.CommitHistoryResponse, error) {
	log.Printf("GetSliceCommits called: slice_id=%s", req.SliceId)

	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		if err == storage.ErrSliceNotFound {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice: %v", err))
	}
	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}
	if !s.canReadSliceInfo(ctx, slice, username) {
		return nil, sliceReadAccessError(username)
	}

	commits, err := s.storage.ListSliceCommits(ctx, req.SliceId, int(req.Limit), req.FromCommitHash)
	if err != nil {
		if err == storage.ErrSliceNotFound {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list commits: %v", err))
	}

	response := &slicev1.CommitHistoryResponse{}
	for _, commit := range commits {
		response.Commits = append(response.Commits, &slicev1.CommitInfo{
			CommitHash: commit.CommitHash,
			Timestamp:  commit.Timestamp.Unix(),
			ParentHash: commit.ParentHash,
			Message:    commit.Message,
		})
	}

	return response, nil
}

func (s *sliceServiceServer) GetSliceState(ctx context.Context, req *slicev1.StateRequest) (*slicev1.StateResponse, error) {
	log.Printf("GetSliceState called: slice_id=%s", req.SliceId)

	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}
	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}
	if !s.canReadSliceInfo(ctx, slice, username) {
		return nil, sliceReadAccessError(username)
	}

	// Get slice metadata
	metadata, err := s.storage.GetSliceMetadata(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}

	return &slicev1.StateResponse{
		LatestCommitHash: metadata.HeadCommitHash,
		ModifiedFiles:    metadata.ModifiedFiles,
		LastModified:     metadata.LastModified.Unix(),
	}, nil
}

func (s *sliceServiceServer) ListChangesets(ctx context.Context, req *slicev1.ListChangesetsRequest) (*slicev1.ListChangesetsResponse, error) {
	log.Printf("ListChangesets called: slice_id=%s", req.SliceId)

	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}
	if _, err := s.authorizeSliceRead(ctx, slice); err != nil {
		return nil, err
	}

	var statusFilter *models.ChangesetStatus
	if !req.IncludeAllStatuses && req.StatusFilter >= 0 {
		converted := convertProtoStatusToModel(req.StatusFilter)
		statusFilter = &converted
	}

	omitModifiedFiles := req.GetOmitModifiedFiles()
	changesets, err := s.listChangesets(ctx, req.SliceId, statusFilter, int(req.Limit), !omitModifiedFiles)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list changesets: %v", err))
	}

	response := &slicev1.ListChangesetsResponse{}
	for _, cs := range changesets {
		info := convertChangesetToProto(cs)
		if omitModifiedFiles {
			info.ModifiedFiles = nil
		}
		if cs.Status == models.ChangesetStatusPending || cs.Status == models.ChangesetStatusApproved {
			fileCount := changesetModifiedFileCount(cs)
			if omitModifiedFiles && fileCount > maxChangesetListReviewPaths {
				info.ReviewStatus = slicev1.ReviewStatus_REVIEW_STATUS_UNKNOWN
				if snapshot, snapshotErr := s.latestChangesetSnapshot(ctx, cs.ID, false); snapshotErr == nil && snapshot != nil {
					info.Ci = s.buildChangesetCISummary(ctx, cs.ID, snapshot.ID)
				} else if snapshotErr != nil && !errors.Is(snapshotErr, storage.ErrChangesetNotFound) {
					return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load changeset snapshot: %v", snapshotErr))
				}
			} else {
				snapshot, snapshotErr := s.latestChangesetSnapshot(ctx, cs.ID, true)
				if snapshotErr != nil {
					if !errors.Is(snapshotErr, storage.ErrChangesetNotFound) {
						return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load changeset snapshot: %v", snapshotErr))
					}
					snapshot = nil
				}
				reviewCS := cs
				if snapshot != nil {
					reviewCS = applySnapshotToChangeset(cs, snapshot)
				} else if omitModifiedFiles {
					fullCS, fullErr := s.storage.GetChangeset(ctx, cs.ID)
					if fullErr != nil {
						if !errors.Is(fullErr, storage.ErrChangesetNotFound) {
							return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load changeset: %v", fullErr))
						}
					} else {
						reviewCS = fullCS
					}
				}
				reviewStatus, _, _, err := s.evaluateChangesetReviewState(ctx, reviewCS, snapshot)
				if err != nil {
					return nil, status.Error(codes.Internal, fmt.Sprintf("failed to evaluate changeset %s state: %v", cs.ID, err))
				}
				info.ReviewStatus = reviewStatus
				if snapshot != nil {
					info.Ci = s.buildChangesetCISummary(ctx, cs.ID, snapshot.ID)
				}
			}
		}
		response.Changesets = append(response.Changesets, info)
	}

	return response, nil
}

func (s *sliceServiceServer) listChangesets(ctx context.Context, sliceID string, statusFilter *models.ChangesetStatus, limit int, includeModifiedFiles bool) ([]*models.Changeset, error) {
	if lister, ok := s.storage.(storage.ChangesetOptionLister); ok {
		return lister.ListChangesetsWithOptions(ctx, sliceID, storage.ListChangesetsOptions{
			Status:               statusFilter,
			Limit:                limit,
			IncludeModifiedFiles: includeModifiedFiles,
		})
	}
	return s.storage.ListChangesets(ctx, sliceID, statusFilter, limit)
}

func (s *sliceServiceServer) listChangesetSnapshots(ctx context.Context, changesetID string, limit int, includeModifiedFiles bool) ([]*models.ChangesetSnapshot, error) {
	if lister, ok := s.storage.(storage.ChangesetSnapshotOptionLister); ok {
		return lister.ListChangesetSnapshotsWithOptions(ctx, changesetID, storage.ListChangesetSnapshotsOptions{
			Limit:                limit,
			IncludeModifiedFiles: includeModifiedFiles,
		})
	}
	return s.storage.ListChangesetSnapshots(ctx, changesetID, limit)
}

func (s *sliceServiceServer) latestChangesetSnapshot(ctx context.Context, changesetID string, includeModifiedFiles bool) (*models.ChangesetSnapshot, error) {
	if includeModifiedFiles {
		return s.storage.GetChangesetSnapshot(ctx, changesetID, 0)
	}
	snapshots, err := s.listChangesetSnapshots(ctx, changesetID, 1, false)
	if err != nil {
		return nil, err
	}
	if len(snapshots) == 0 || snapshots[0] == nil {
		return nil, storage.ErrChangesetNotFound
	}
	return snapshots[0], nil
}

func (s *sliceServiceServer) evaluateChangesetReviewState(ctx context.Context, cs *models.Changeset, snapshot *models.ChangesetSnapshot) (slicev1.ReviewStatus, []*slicev1.ReviewIssue, []string, error) {
	if cs == nil {
		return slicev1.ReviewStatus_READY_FOR_MERGE, nil, nil, nil
	}
	if cs.Status == models.ChangesetStatusMerged || cs.Status == models.ChangesetStatusRejected {
		return slicev1.ReviewStatus_READY_FOR_MERGE, nil, nil, nil
	}

	if handled, pathStatus, pathIssues, pathWarnings, err := s.evaluateChangesetPathHeadReviewState(ctx, cs, snapshot); err != nil {
		return slicev1.ReviewStatus_READY_FOR_MERGE, nil, nil, err
	} else if handled {
		return pathStatus, pathIssues, pathWarnings, nil
	}

	message := missingPathHeadAuthorityMessage()
	return slicev1.ReviewStatus_NEEDS_SYNC,
		[]*slicev1.ReviewIssue{{
			Type:    slicev1.ReviewIssueType_REVIEW_ISSUE_TYPE_STALE_BASE,
			Message: message,
		}},
		[]string{message},
		nil
}

func missingPathHeadAuthorityMessage() string {
	return "changeset does not include complete path-head base versions. Re-export the changeset before merging."
}

func (s *sliceServiceServer) evaluateChangesetPathHeadReviewState(ctx context.Context, cs *models.Changeset, snapshot *models.ChangesetSnapshot) (bool, slicev1.ReviewStatus, []*slicev1.ReviewIssue, []string, error) {
	drifts, compared, err := s.changesetPathHeadDrifts(ctx, cs, snapshot)
	if err != nil {
		return compared, slicev1.ReviewStatus_READY_FOR_MERGE, nil, nil, err
	}
	if !compared {
		return false, slicev1.ReviewStatus_READY_FOR_MERGE, nil, nil, nil
	}
	if len(drifts) == 0 {
		return true, slicev1.ReviewStatus_READY_FOR_MERGE, nil, nil, nil
	}

	issues := make([]*slicev1.ReviewIssue, 0, len(drifts))
	warnings := make([]string, 0, len(drifts))
	for _, drift := range drifts {
		message := fmt.Sprintf(
			"path %s changed from version %d to %d. Sync the changeset before merging.",
			drift.Path,
			drift.BaseVersion,
			drift.CurrentVersion,
		)
		warnings = append(warnings, message)
		issues = append(issues, &slicev1.ReviewIssue{
			Type:    slicev1.ReviewIssueType_REVIEW_ISSUE_TYPE_STALE_BASE,
			FileId:  drift.Path,
			Message: message,
		})
	}
	return true, slicev1.ReviewStatus_NEEDS_SYNC, issues, warnings, nil
}

type changesetPathHeadDrift struct {
	Path           string
	BaseVersion    int64
	CurrentVersion int64
}

func (s *sliceServiceServer) changesetPathHeadDrifts(ctx context.Context, cs *models.Changeset, snapshot *models.ChangesetSnapshot) ([]changesetPathHeadDrift, bool, error) {
	if cs == nil || snapshot == nil || len(snapshot.BasePathVersions) == 0 {
		return nil, false, nil
	}
	headStore, ok := s.storage.(storage.HomePathHeadStore)
	if !ok {
		return nil, false, nil
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, true, err
	}
	paths := normalizeModifiedFiles(cs.ModifiedFiles)
	if !snapshotHasBaseVersionsForPaths(snapshot, paths) {
		return nil, false, nil
	}
	homeID := mergeEventHomeID(slice, cs, paths)
	if strings.TrimSpace(homeID) == "" {
		return nil, false, nil
	}
	heads, err := headStore.GetHomePathHeads(ctx, homeID, paths)
	if err != nil {
		return nil, true, err
	}

	compared := 0
	drifts := make([]changesetPathHeadDrift, 0)
	for _, rawPath := range paths {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}
		baseVersion, ok := snapshot.BasePathVersions[filePath]
		if !ok {
			continue
		}
		compared++
		currentVersion := int64(0)
		if head := heads[filePath]; head != nil && head.PathVersion > 0 {
			currentVersion = head.PathVersion
		}
		if currentVersion == baseVersion {
			continue
		}
		drifts = append(drifts, changesetPathHeadDrift{
			Path:           filePath,
			BaseVersion:    baseVersion,
			CurrentVersion: currentVersion,
		})
	}
	return drifts, compared > 0, nil
}

func convertChangesetToProto(cs *models.Changeset) *slicev1.ChangesetInfo {
	status := convertChangesetStatusToProto(cs.Status)

	var mergedAt int64
	if cs.MergedAt != nil {
		mergedAt = cs.MergedAt.Unix()
	}

	return &slicev1.ChangesetInfo{
		ChangesetId:       cs.ID,
		ChangesetHash:     cs.Hash,
		SliceId:           cs.SliceID,
		BaseCommitHash:    cs.BaseCommitHash,
		ModifiedFiles:     cs.ModifiedFiles,
		Status:            status,
		Author:            cs.Author,
		Message:           cs.Message,
		CreatedAt:         cs.CreatedAt.Unix(),
		MergedAt:          mergedAt,
		ModifiedFileCount: int32(changesetModifiedFileCount(cs)),
	}
}

func (s *sliceServiceServer) agentSessionChangesetLinks(ctx context.Context, cs *models.Changeset) ([]*slicev1.AgentSessionChangesetLink, error) {
	if cs == nil || strings.TrimSpace(cs.ID) == "" {
		return nil, nil
	}
	links, err := s.storage.ListChangesetAgentSessions(ctx, cs.ID, agentArtifactLinkLimit)
	if err != nil {
		if errors.Is(err, storage.ErrChangesetNotFound) {
			return []*slicev1.AgentSessionChangesetLink{}, nil
		}
		return nil, err
	}
	out := make([]*slicev1.AgentSessionChangesetLink, 0, len(links))
	for _, link := range links {
		protoLink := agentSessionChangesetLinkToProto(link, cs.SliceID)
		if protoLink != nil {
			out = append(out, protoLink)
		}
	}
	return out, nil
}

func (s *sliceServiceServer) changesetMergeLink(ctx context.Context, changesetID string) (*slicev1.ChangesetMergeLink, error) {
	mergeStore, ok := s.storage.(storage.MergeEventStore)
	if !ok {
		return nil, nil
	}
	event, err := mergeStore.GetMergeEventByChangeset(ctx, changesetID)
	if err != nil {
		if errors.Is(err, storage.ErrMergeEventNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return changesetMergeLinkToProto(event), nil
}

func agentSessionChangesetLinkToProto(link *models.AgentSessionChangeset, sliceID string) *slicev1.AgentSessionChangesetLink {
	if link == nil {
		return nil
	}
	return &slicev1.AgentSessionChangesetLink{
		SessionId:       link.SessionID,
		ChangesetId:     link.ChangesetID,
		SnapshotId:      link.SnapshotID,
		SnapshotVersion: link.SnapshotVersion,
		SnapshotHash:    link.SnapshotHash,
		BaseCommitHash:  link.BaseCommitHash,
		ExportedFromSeq: link.ExportedFromSeq,
		RunnerId:        link.RunnerID,
		Source:          link.Source,
		ExportedAt:      link.ExportedAt.Unix(),
		SliceId:         sliceID,
	}
}

func changesetMergeLinkToProto(event *models.MergeEvent) *slicev1.ChangesetMergeLink {
	if event == nil {
		return nil
	}
	return &slicev1.ChangesetMergeLink{
		CommitHash:    event.SourceCommitHash,
		SourceSliceId: event.SourceSliceID,
		MergedAt:      event.CreatedAt.Unix(),
	}
}

func changesetModifiedFileCount(cs *models.Changeset) int {
	if cs == nil {
		return 0
	}
	if cs.ModifiedFileCount > 0 {
		return cs.ModifiedFileCount
	}
	return len(cs.ModifiedFiles)
}

func changesetSnapshotModifiedFileCount(snapshot *models.ChangesetSnapshot) int {
	if snapshot == nil {
		return 0
	}
	if snapshot.ModifiedFileCount > 0 {
		return snapshot.ModifiedFileCount
	}
	return len(snapshot.ModifiedFiles)
}

func (s *sliceServiceServer) buildChangesetCISummary(ctx context.Context, changesetID string, changesetVersionID string) *slicev1.ChangesetCISummary {
	changesetID = strings.TrimSpace(changesetID)
	changesetVersionID = strings.TrimSpace(changesetVersionID)
	if changesetID == "" || changesetVersionID == "" {
		return nil
	}
	runs, err := s.storage.ListCIRuns(ctx, storage.CIRunListFilter{
		ChangesetID:        changesetID,
		ChangesetVersionID: changesetVersionID,
		Limit:              1,
	})
	if err != nil || len(runs) == 0 || runs[0] == nil {
		return &slicev1.ChangesetCISummary{
			Status:             "missing",
			ChangesetVersionId: changesetVersionID,
		}
	}
	run := runs[0]
	summary := &slicev1.ChangesetCISummary{
		Status:             run.Status,
		RunId:              run.ID,
		PlanHash:           run.PlanHash,
		ChangesetVersionId: run.ChangesetVersionID,
		Stale:              run.ChangesetVersionID != changesetVersionID,
	}
	checks, err := s.storage.ListCIChecks(ctx, changesetID, run.ChangesetVersionID, run.PlanHash)
	if err != nil {
		return summary
	}
	for _, check := range checks {
		if check == nil || !check.Required {
			continue
		}
		summary.RequiredTotal++
		switch check.Status {
		case "passed":
			summary.RequiredPassed++
		case "queued":
			summary.RequiredQueued++
		case "running":
			summary.RequiredRunning++
		default:
			summary.RequiredFailed++
		}
	}
	return summary
}

func convertChangesetStatusToProto(status models.ChangesetStatus) slicev1.ChangesetStatus {
	switch status {
	case models.ChangesetStatusApproved:
		return slicev1.ChangesetStatus_APPROVED
	case models.ChangesetStatusRejected:
		return slicev1.ChangesetStatus_REJECTED
	case models.ChangesetStatusMerged:
		return slicev1.ChangesetStatus_MERGED
	default:
		return slicev1.ChangesetStatus_PENDING
	}
}

func convertProtoStatusToModel(status slicev1.ChangesetStatus) models.ChangesetStatus {
	switch status {
	case slicev1.ChangesetStatus_APPROVED:
		return models.ChangesetStatusApproved
	case slicev1.ChangesetStatus_REJECTED:
		return models.ChangesetStatusRejected
	case slicev1.ChangesetStatus_MERGED:
		return models.ChangesetStatusMerged
	default:
		return models.ChangesetStatusPending
	}
}

func (s *sliceServiceServer) GetRootSlice(ctx context.Context, req *slicev1.GetRootSliceRequest) (*slicev1.GetRootSliceResponse, error) {
	log.Printf("GetRootSlice called")

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	rootSlice, err := s.storage.GetRootSlice(ctx)
	if err != nil {
		return nil, status.Error(codes.NotFound, "root slice not found")
	}
	if !s.hasSliceViewAccess(ctx, rootSlice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for root slice")
	}

	metadata, _ := s.storage.GetSliceMetadata(ctx, rootSlice.ID)

	return &slicev1.GetRootSliceResponse{
		SliceId:    rootSlice.ID,
		CommitHash: metadata.HeadCommitHash,
		Visibility: modelVisibilityToProto(rootSlice.Visibility),
	}, nil
}

func (s *sliceServiceServer) CreateSliceFromFolder(ctx context.Context, req *slicev1.CreateSliceFromFolderRequest) (*slicev1.CreateSliceFromFolderResponse, error) {
	log.Printf("CreateSliceFromFolder called: parent_slice_id=%s, folder_paths=%d, new_slice_id=%s",
		req.ParentSliceId, len(req.FolderPaths), req.NewSliceId)

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	// Validate parent slice ID
	if err := common.ValidateSliceID(req.ParentSliceId); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid parent slice ID: %v", err))
	}

	// Auto-generate slice ID if not provided; validate if provided.
	sliceID := req.NewSliceId
	if sliceID == "" {
		sliceID = common.GenerateSliceID()
	} else {
		if err := common.ValidateSliceID(sliceID); err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid new slice ID: %v", err))
		}
	}

	folderPaths, err := collectRequestedFolderPaths(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	sliceName := strings.TrimSpace(req.Name)
	if sliceName == "" {
		sliceName = defaultSliceNameFromFolders(folderPaths, sliceID)
	}

	parentSlice, err := s.storage.GetSlice(ctx, req.ParentSliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("parent slice not found: %s", req.ParentSliceId))
	}
	if !s.hasSliceViewAccess(ctx, parentSlice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for parent slice")
	}

	parentEntries, entriesScopedToFolders, err := s.collectSliceEntriesForRequestedFolders(ctx, parentSlice, folderPaths, username)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to enumerate parent slice entries: %v", err))
	}

	folderSelections, err := resolveRequestedFolderSelections(parentSlice, parentEntries, folderPaths, username)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := validateFolderSelectionsExist(parentSlice, parentEntries, folderSelections); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	includeParentSliceFiles := !entriesScopedToFolders || !entriesContainFilesForSelections(parentEntries, folderSelections)
	selectedFiles := collectSliceFilesForFolders(parentSlice, parentEntries, folderSelections, includeParentSliceFiles)

	newSlice := &models.Slice{
		ID:           sliceID,
		Name:         sliceName,
		Description:  req.Description,
		Visibility:   models.VisibilityPrivate,
		Files:        selectedFiles,
		FolderMounts: buildSliceFolderMounts(folderSelections),
		Owners:       []string{username},
		CreatedBy:    username,
		ParentSlice:  parentSlice.ID,
		IsRoot:       false,
	}

	if err := s.storage.CreateSlice(ctx, newSlice); err != nil {
		if errors.Is(err, storage.ErrSliceAlreadyExists) {
			return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("slice already exists: %s", sliceID))
		}
		if errors.Is(err, storage.ErrInvalidInput) {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid slice: %v", err))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create slice: %v", err))
	}
	if err := s.hydrateSliceEntryMetadataFromParent(ctx, newSlice, parentSlice, selectedFiles); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to hydrate slice entry metadata: %v", err))
	}
	if err := s.initializeSliceFromFolderHead(ctx, newSlice, parentSlice, selectedFiles); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to initialize slice commit: %v", err))
	}

	return &slicev1.CreateSliceFromFolderResponse{
		SliceId:    sliceID,
		Status:     "created",
		Files:      selectedFiles,
		Name:       sliceName,
		Slug:       externalSliceSlug(newSlice),
		Visibility: modelVisibilityToProto(newSlice.Visibility),
	}, nil
}

func (s *sliceServiceServer) initializeSliceFromFolderHead(ctx context.Context, slice, parentSlice *models.Slice, selectedFiles []string) error {
	if slice == nil {
		return nil
	}

	meta, err := s.storage.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		return err
	}
	commitHash := strings.TrimSpace(meta.HeadCommitHash)
	if commitHash == "" {
		commitHash = common.GenerateInitialCommitID(slice.ID)
	}

	files := make(map[string]string)
	if parentSlice != nil {
		parentHashes, err := getFileManifestHashes(ctx, s.storage, parentSlice.ID, selectedFiles)
		if err != nil {
			return err
		}
		for _, rawPath := range normalizeModifiedFiles(selectedFiles) {
			sourcePath := cleanDiffPath(rawPath)
			hash := strings.TrimSpace(parentHashes[sourcePath])
			displayPath := common.SliceDisplayPath(slice, sourcePath)
			if displayPath == "" {
				displayPath = sourcePath
			}
			if !isUsableContentHash(displayPath, hash) {
				continue
			}
			files[displayPath] = hash
		}
	}

	now := time.Now().UTC()
	if err := s.storage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    slice.ID,
		Files:      files,
		Timestamp:  now,
	}); err != nil {
		return err
	}
	return s.storage.AddSliceCommit(ctx, slice.ID, &models.Commit{
		CommitHash: commitHash,
		ParentHash: "",
		Message:    "create slice from folder",
		Timestamp:  now,
	})
}

func (s *sliceServiceServer) RenameSlice(ctx context.Context, req *slicev1.RenameSliceRequest) (*slicev1.RenameSliceResponse, error) {
	log.Printf("RenameSlice called: slice_id=%s, new_name=%s", req.SliceId, req.NewName)

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	if err := common.ValidateSliceID(req.SliceId); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid slice ID: %v", err))
	}
	if strings.TrimSpace(req.NewName) == "" {
		return nil, status.Error(codes.InvalidArgument, "new name cannot be empty")
	}

	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	if err := s.storage.UpdateSliceName(ctx, req.SliceId, req.NewName); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to rename slice: %v", err))
	}

	return &slicev1.RenameSliceResponse{
		SliceId: req.SliceId,
		Name:    req.NewName,
		Slug:    externalSliceSlug(slice),
	}, nil
}

func (s *sliceServiceServer) AddSliceFolder(ctx context.Context, req *slicev1.AddSliceFolderRequest) (*slicev1.AddSliceFolderResponse, error) {
	log.Printf("AddSliceFolder called: slice_id=%s folder_path=%s", req.SliceId, req.FolderPath)

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	if err := common.ValidateSliceID(req.SliceId); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid slice ID: %v", err))
	}

	folderPath := common.CleanRelativePath(req.FolderPath)
	if folderPath == "" {
		return nil, status.Error(codes.InvalidArgument, "folder path cannot be empty")
	}

	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}
	if slice.IsRoot {
		return nil, status.Error(codes.FailedPrecondition, "cannot add folders to root slice")
	}
	if _, isHome := homeslice.ExternalSlugForSlice(slice); isHome {
		return nil, status.Error(codes.FailedPrecondition, "cannot add folders to home slice")
	}
	if !canManageSliceVisibility(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "only slice owners can modify tracked folders")
	}

	parentSliceID := strings.TrimSpace(slice.ParentSlice)
	if parentSliceID == "" {
		return nil, status.Error(codes.FailedPrecondition, "slice has no parent")
	}
	parentSlice, err := s.storage.GetSlice(ctx, parentSliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("parent slice not found: %s", parentSliceID))
	}

	for _, mount := range slice.FolderMounts {
		if common.CleanRelativePath(mount.SourcePath) == folderPath {
			return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("folder %q is already tracked", folderPath))
		}
	}

	parentEntries, err := s.collectSliceEntries(ctx, parentSlice.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to enumerate parent slice entries: %v", err))
	}

	folderExists, isFile := folderSelectionExistsInEntries(parentEntries, folderPath)
	if isFile {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("folder path %q is a file", folderPath))
	}
	if !folderExists {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("folder %q does not exist in parent slice", folderPath))
	}

	alias := path.Base(folderPath)
	if alias == "." || alias == "/" || alias == "" {
		alias = strings.ReplaceAll(folderPath, "/", "_")
	}
	alias = uniqueFolderMountAlias(slice.FolderMounts, alias)
	newMount := models.SliceFolderMount{SourcePath: folderPath, Alias: alias}

	newMounts := make([]models.SliceFolderMount, len(slice.FolderMounts)+1)
	copy(newMounts, slice.FolderMounts)
	newMounts[len(slice.FolderMounts)] = newMount

	prefix := folderPath + "/"
	newFiles := make([]string, 0)
	for _, entry := range parentEntries {
		if entry.Path == folderPath || strings.HasPrefix(entry.Path, prefix) {
			if entry.Type == "file" {
				newFiles = append(newFiles, entry.Path)
			}
		}
	}

	allFiles := deduplicateSortedStrings(append(slice.Files, newFiles...))

	if err := s.storage.UpdateSliceFolderMounts(ctx, req.SliceId, newMounts, allFiles); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update slice folder mounts: %v", err))
	}

	protoMounts := make([]*slicev1.FolderMount, len(newMounts))
	for i, m := range newMounts {
		protoMounts[i] = &slicev1.FolderMount{SourcePath: m.SourcePath, Alias: m.Alias}
	}

	return &slicev1.AddSliceFolderResponse{
		SliceId:      req.SliceId,
		FolderMounts: protoMounts,
		Files:        allFiles,
	}, nil
}

func (s *sliceServiceServer) RemoveSliceFolder(ctx context.Context, req *slicev1.RemoveSliceFolderRequest) (*slicev1.RemoveSliceFolderResponse, error) {
	log.Printf("RemoveSliceFolder called: slice_id=%s folder_path=%s", req.SliceId, req.FolderPath)

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	if err := common.ValidateSliceID(req.SliceId); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid slice ID: %v", err))
	}

	folderPath := common.CleanRelativePath(req.FolderPath)
	if folderPath == "" {
		return nil, status.Error(codes.InvalidArgument, "folder path cannot be empty")
	}

	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}
	if slice.IsRoot {
		return nil, status.Error(codes.FailedPrecondition, "cannot remove folders from root slice")
	}
	if _, isHome := homeslice.ExternalSlugForSlice(slice); isHome {
		return nil, status.Error(codes.FailedPrecondition, "cannot remove folders from home slice")
	}
	if !canManageSliceVisibility(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "only slice owners can modify tracked folders")
	}

	mountIndex := -1
	for i, mount := range slice.FolderMounts {
		mountPath := common.CleanRelativePath(mount.SourcePath)
		if mountPath == folderPath || common.CleanRelativePath(mount.Alias) == folderPath {
			mountIndex = i
			break
		}
	}
	if mountIndex < 0 {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("folder %q is not tracked by this slice", folderPath))
	}

	removedMount := slice.FolderMounts[mountIndex]
	newMounts := make([]models.SliceFolderMount, 0, len(slice.FolderMounts)-1)
	for i, mount := range slice.FolderMounts {
		if i != mountIndex {
			newMounts = append(newMounts, mount)
		}
	}

	sourcePrefix := common.CleanRelativePath(removedMount.SourcePath) + "/"
	allFiles := make([]string, 0, len(slice.Files))
	for _, f := range slice.Files {
		p := common.CleanRelativePath(f)
		if p == common.CleanRelativePath(removedMount.SourcePath) {
			continue
		}
		if strings.HasPrefix(p, sourcePrefix) {
			continue
		}
		allFiles = append(allFiles, f)
	}

	if err := s.storage.UpdateSliceFolderMounts(ctx, req.SliceId, newMounts, allFiles); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update slice folder mounts: %v", err))
	}

	protoMounts := make([]*slicev1.FolderMount, len(newMounts))
	for i, m := range newMounts {
		protoMounts[i] = &slicev1.FolderMount{SourcePath: m.SourcePath, Alias: m.Alias}
	}

	return &slicev1.RemoveSliceFolderResponse{
		SliceId:      req.SliceId,
		FolderMounts: protoMounts,
		Files:        allFiles,
	}, nil
}

func (s *sliceServiceServer) DeleteSlice(ctx context.Context, req *slicev1.DeleteSliceRequest) (*slicev1.DeleteSliceResponse, error) {
	log.Printf("DeleteSlice called: slice_id=%s force=%t", req.GetSliceId(), req.GetForce())

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	if err := common.ValidateSliceID(req.GetSliceId()); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid slice ID: %v", err))
	}

	slice, err := s.storage.GetSlice(ctx, req.GetSliceId())
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.GetSliceId()))
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}
	if slice.IsRoot {
		return nil, status.Error(codes.FailedPrecondition, "root slice cannot be deleted")
	}
	if _, ok := homeslice.ExternalSlugForSlice(slice); ok {
		return nil, status.Error(codes.FailedPrecondition, "home slices cannot be deleted")
	}

	changesets, err := s.storage.ListChangesets(ctx, slice.ID, nil, 1024)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list slice changesets: %v", err))
	}
	if !req.GetForce() {
		pendingCount := 0
		for _, cs := range changesets {
			if cs == nil {
				continue
			}
			if cs.Status == models.ChangesetStatusPending || cs.Status == models.ChangesetStatusApproved {
				pendingCount++
			}
		}
		if pendingCount > 0 {
			return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("slice has %d open changeset(s); rerun with force to delete", pendingCount))
		}
	}

	if err := s.storage.DeleteSlice(ctx, slice.ID); err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", slice.ID))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to delete slice: %v", err))
	}

	return &slicev1.DeleteSliceResponse{
		SliceId: slice.ID,
		Slug:    externalSliceSlug(slice),
		Status:  "deleted",
	}, nil
}

func (s *sliceServiceServer) GetSliceByName(ctx context.Context, req *slicev1.GetSliceByNameRequest) (*slicev1.GetSliceByNameResponse, error) {
	log.Printf("GetSliceByName called: name=%s", req.Name)

	if strings.TrimSpace(req.Name) == "" {
		return nil, status.Error(codes.InvalidArgument, "name cannot be empty")
	}

	slice, err := s.storage.GetSliceByName(ctx, req.Name)
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found with name: %s", req.Name))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to look up slice: %v", err))
	}

	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}
	if !s.canReadSliceInfo(ctx, slice, username) {
		return nil, sliceReadAccessError(username)
	}

	return &slicev1.GetSliceByNameResponse{
		SliceId:       slice.ID,
		Name:          slice.Name,
		Description:   slice.Description,
		ParentSliceId: slice.ParentSlice,
		Files:         slice.Files,
		Environment:   slice.Environment,
		Slug:          externalSliceSlug(slice),
		Visibility:    modelVisibilityToProto(slice.Visibility),
	}, nil
}

func (s *sliceServiceServer) GetSliceBySlug(ctx context.Context, req *slicev1.GetSliceBySlugRequest) (*slicev1.GetSliceBySlugResponse, error) {
	log.Printf("GetSliceBySlug called: slug=%s", req.Slug)

	slice, err := s.loadReadableSliceByRef(ctx, req.GetSlug())
	if err != nil {
		return nil, err
	}

	return &slicev1.GetSliceBySlugResponse{
		SliceId:       slice.ID,
		Name:          slice.Name,
		Description:   slice.Description,
		ParentSliceId: slice.ParentSlice,
		Files:         slice.Files,
		Environment:   slice.Environment,
		Slug:          externalSliceSlug(slice),
		Visibility:    modelVisibilityToProto(slice.Visibility),
	}, nil
}

func (s *sliceServiceServer) ResolveSlice(ctx context.Context, req *slicev1.ResolveSliceRequest) (*slicev1.ResolveSliceResponse, error) {
	log.Printf("ResolveSlice called: ref=%s", req.GetRef())

	slice, err := s.loadReadableSliceByRef(ctx, req.GetRef())
	if err != nil {
		return nil, err
	}

	return &slicev1.ResolveSliceResponse{
		SliceId:       slice.ID,
		Name:          slice.Name,
		Description:   slice.Description,
		ParentSliceId: slice.ParentSlice,
		Files:         slice.Files,
		Environment:   slice.Environment,
		Slug:          externalSliceSlug(slice),
		Visibility:    modelVisibilityToProto(slice.Visibility),
	}, nil
}

func (s *sliceServiceServer) GetSliceVisibility(ctx context.Context, req *slicev1.GetSliceVisibilityRequest) (*slicev1.GetSliceVisibilityResponse, error) {
	slice, err := s.storage.GetSlice(ctx, req.GetSliceId())
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.GetSliceId()))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice: %v", err))
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
	return &slicev1.GetSliceVisibilityResponse{
		SliceId:    slice.ID,
		Visibility: modelVisibilityToProto(slice.Visibility),
	}, nil
}

func (s *sliceServiceServer) SetSliceVisibility(ctx context.Context, req *slicev1.SetSliceVisibilityRequest) (*slicev1.SetSliceVisibilityResponse, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	slice, err := s.storage.GetSlice(ctx, req.GetSliceId())
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.GetSliceId()))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice: %v", err))
	}
	if !canManageSliceVisibility(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized to change slice visibility")
	}

	visibility, err := protoVisibilityToModel(req.GetVisibility())
	if err != nil {
		return nil, err
	}

	if err := s.storage.UpdateSliceVisibility(ctx, slice.ID, visibility); err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", slice.ID))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update slice visibility: %v", err))
	}

	updatedSlice, err := s.storage.GetSlice(ctx, slice.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to reload slice visibility: %v", err))
	}

	return &slicev1.SetSliceVisibilityResponse{
		SliceId:    updatedSlice.ID,
		Visibility: modelVisibilityToProto(updatedSlice.Visibility),
	}, nil
}

func defaultSliceNameFromFolders(folderPaths []string, fallback string) string {
	if len(folderPaths) == 0 {
		return fallback
	}
	if len(folderPaths) == 1 {
		return folderPaths[0]
	}
	return strings.Join(folderPaths, ", ")
}

func collectRequestedFolderPaths(req *slicev1.CreateSliceFromFolderRequest) ([]string, error) {
	paths := req.GetFolderPaths()

	dedup := make(map[string]struct{}, len(paths))
	normalized := make([]string, 0, len(paths))
	for _, raw := range paths {
		cleaned := common.CleanRelativePath(raw)
		if cleaned == "" {
			continue
		}
		if err := common.ValidateFilePath(cleaned); err != nil {
			return nil, fmt.Errorf("invalid folder path %q: %v", raw, err)
		}
		if _, exists := dedup[cleaned]; exists {
			continue
		}
		dedup[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("at least one folder path is required")
	}
	return normalized, nil
}

type sliceFolderSelection struct {
	displayPath string
	storedPath  string
}

func candidateFolderEntryPrefixes(parentSlice *models.Slice, folderPaths []string, username string) []string {
	if parentSlice == nil || len(folderPaths) == 0 {
		return nil
	}
	prefixes := make([]string, 0, len(folderPaths)*2)
	seen := make(map[string]struct{}, len(folderPaths)*2)
	add := func(raw string) {
		cleaned := common.CleanRelativePath(raw)
		if cleaned == "" {
			return
		}
		if _, ok := seen[cleaned]; ok {
			return
		}
		seen[cleaned] = struct{}{}
		prefixes = append(prefixes, cleaned)
	}

	homeRoot := ""
	if parentSlice.IsRoot || homeslice.IsHomeSliceID(parentSlice.ID) {
		homeUsername := strings.TrimSpace(username)
		if sliceUsername := homeslice.UsernameFromSliceID(parentSlice.ID); sliceUsername != "" {
			homeUsername = sliceUsername
		}
		homeRoot = homeslice.RelativeRootPath(homeUsername)
	}

	for _, displayPath := range folderPaths {
		cleanDisplayPath := common.CleanRelativePath(displayPath)
		if cleanDisplayPath == "" {
			continue
		}
		storedPath := common.SliceStoredPath(parentSlice, cleanDisplayPath)
		add(storedPath)
		if homeRoot != "" && storedPath != homeRoot && !strings.HasPrefix(storedPath, homeRoot+"/") {
			add(path.Join(homeRoot, cleanDisplayPath))
		}
	}
	return prefixes
}

func resolveRequestedFolderSelections(parentSlice *models.Slice, entries []*models.DirectoryEntry, folderPaths []string, username string) ([]sliceFolderSelection, error) {
	if len(folderPaths) == 0 {
		return nil, nil
	}

	selections := make([]sliceFolderSelection, 0, len(folderPaths))
	seenStored := make(map[string]struct{}, len(folderPaths))
	homeRoot := ""
	rootScoped := false
	if parentSlice != nil && (parentSlice.IsRoot || homeslice.IsHomeSliceID(parentSlice.ID)) {
		rootScoped = parentSlice.IsRoot
		homeUsername := strings.TrimSpace(username)
		if sliceUsername := homeslice.UsernameFromSliceID(parentSlice.ID); sliceUsername != "" {
			homeUsername = sliceUsername
		}
		homeRoot = homeslice.RelativeRootPath(homeUsername)
		if homeRoot == "" {
			return nil, fmt.Errorf("home slice %q has no root path", parentSlice.ID)
		}
	}
	for _, displayPath := range folderPaths {
		cleanDisplayPath := common.CleanRelativePath(displayPath)
		storedPath := common.SliceStoredPath(parentSlice, cleanDisplayPath)
		if homeRoot != "" {
			directExists, directExactFile := folderSelectionExists(parentSlice, entries, storedPath)
			if rootScoped && (directExists || directExactFile) {
				// Keep explicit paths that already exist in the published root.
			} else if storedPath == homeRoot {
				if !rootScoped {
					cleanDisplayPath = path.Base(homeRoot)
				}
			} else if strings.HasPrefix(storedPath, homeRoot+"/") {
				if !rootScoped {
					cleanDisplayPath = strings.TrimPrefix(storedPath, homeRoot+"/")
				}
			} else {
				storedPath = path.Join(homeRoot, cleanDisplayPath)
			}
		}
		if storedPath == "" {
			continue
		}
		if _, exists := seenStored[storedPath]; exists {
			continue
		}
		seenStored[storedPath] = struct{}{}
		selections = append(selections, sliceFolderSelection{
			displayPath: cleanDisplayPath,
			storedPath:  storedPath,
		})
	}
	return selections, nil
}

func collectSliceFilesForFolders(parentSlice *models.Slice, entries []*models.DirectoryEntry, selections []sliceFolderSelection, includeParentSliceFiles bool) []string {
	if len(selections) == 0 {
		return nil
	}

	selectedSet := make(map[string]struct{})
	matchesSelection := func(rawPath string) bool {
		cleaned := common.CleanRelativePath(rawPath)
		if cleaned == "" {
			return false
		}
		for _, selection := range selections {
			if cleaned == selection.storedPath {
				return true
			}
			if strings.HasPrefix(cleaned, selection.storedPath+"/") {
				return true
			}
		}
		return false
	}

	for _, entry := range entries {
		if entry == nil || entry.Type != "file" {
			continue
		}
		if matchesSelection(entry.Path) {
			selectedSet[common.CleanRelativePath(entry.Path)] = struct{}{}
		}
	}

	if includeParentSliceFiles && parentSlice != nil {
		for _, rawPath := range parentSlice.Files {
			if matchesSelection(rawPath) {
				selectedSet[common.CleanRelativePath(rawPath)] = struct{}{}
			}
		}
	}

	selectedFiles := make([]string, 0, len(selectedSet))
	for fileID := range selectedSet {
		if fileID == "" {
			continue
		}
		selectedFiles = append(selectedFiles, fileID)
	}
	sort.Strings(selectedFiles)
	return selectedFiles
}

func entriesContainFilesForSelections(entries []*models.DirectoryEntry, selections []sliceFolderSelection) bool {
	for _, entry := range entries {
		if entry == nil || entry.Type != "file" {
			continue
		}
		cleaned := common.CleanRelativePath(entry.Path)
		if cleaned == "" {
			continue
		}
		for _, selection := range selections {
			if cleaned == selection.storedPath || strings.HasPrefix(cleaned, selection.storedPath+"/") {
				return true
			}
		}
	}
	return false
}

func buildSliceFolderMounts(selections []sliceFolderSelection) []models.SliceFolderMount {
	if len(selections) == 0 {
		return nil
	}

	mounts := make([]models.SliceFolderMount, 0, len(selections))
	usedAliases := make(map[string]struct{}, len(selections))
	for _, selection := range selections {
		sourcePath := selection.storedPath
		alias := selection.displayPath
		if alias == "" {
			alias = path.Base(sourcePath)
		}
		if alias == "." || alias == "/" || alias == "" {
			alias = strings.ReplaceAll(sourcePath, "/", "_")
		}
		if alias == "" {
			alias = "folder"
		}

		baseAlias := alias
		for n := 2; ; n++ {
			if _, exists := usedAliases[alias]; !exists {
				break
			}
			alias = fmt.Sprintf("%s_%d", baseAlias, n)
		}
		usedAliases[alias] = struct{}{}

		mounts = append(mounts, models.SliceFolderMount{
			SourcePath: sourcePath,
			Alias:      alias,
		})
	}
	return mounts
}

func validateFolderSelectionsExist(parentSlice *models.Slice, entries []*models.DirectoryEntry, selections []sliceFolderSelection) error {
	for _, selection := range selections {
		if selection.storedPath == "" {
			continue
		}
		exists, exactFile := folderSelectionExists(parentSlice, entries, selection.storedPath)
		if exists {
			continue
		}
		if exactFile {
			return fmt.Errorf("folder path %q is a file", selection.displayPath)
		}
		return fmt.Errorf("folder path %q does not exist in parent slice", selection.displayPath)
	}
	return nil
}

func folderSelectionExists(parentSlice *models.Slice, entries []*models.DirectoryEntry, storedPath string) (exists bool, exactFile bool) {
	matches := func(rawPath string) (exact bool, descendant bool) {
		cleaned := common.CleanRelativePath(rawPath)
		if cleaned == "" {
			return false, false
		}
		return cleaned == storedPath, strings.HasPrefix(cleaned, storedPath+"/")
	}

	for _, entry := range entries {
		if entry == nil {
			continue
		}
		exact, descendant := matches(entry.Path)
		if descendant {
			return true, false
		}
		if !exact {
			continue
		}
		if entry.Type == "directory" {
			return true, false
		}
		if entry.Type == "file" {
			exactFile = true
		}
	}

	if parentSlice != nil {
		for _, rawPath := range parentSlice.Files {
			exact, descendant := matches(rawPath)
			if descendant {
				return true, false
			}
			if exact {
				exactFile = true
			}
		}
	}

	return false, exactFile
}

func (s *sliceServiceServer) enqueueRootPromotion(ctx context.Context, sliceID, commitHash string, files []string, commitTime time.Time, event *models.MergeEvent) error {
	return s.rootPromotionQueue().Enqueue(ctx, rootPromotionJob(sliceID, commitHash, files, commitTime, event))
}

func (s *sliceServiceServer) enqueueRootPromotionAndWait(ctx context.Context, sliceID, commitHash string, files []string, commitTime time.Time, event *models.MergeEvent) error {
	return s.rootPromotionQueue().EnqueueAndWait(ctx, rootPromotionJob(sliceID, commitHash, files, commitTime, event))
}

func rootPromotionJob(sliceID, commitHash string, files []string, commitTime time.Time, event *models.MergeEvent) rootpromote.Job {
	job := rootpromote.Job{
		SliceID:    sliceID,
		CommitHash: commitHash,
		Files:      append([]string(nil), files...),
		CommitTime: commitTime,
		ShardKey:   rootPromotionShardKey(sliceID, files),
	}
	if event != nil {
		job.ProjectionShardID = event.ShardID
		job.ProjectionMergeSeq = event.MergeSeq
	}
	return job
}

func (s *sliceServiceServer) promoteSlice(ctx context.Context, sliceID, commitHash string, files []string, commitTime time.Time) error {
	return s.promoteSliceBatch(ctx, []rootpromote.Job{rootPromotionJob(sliceID, commitHash, files, commitTime, nil)})
}

func rootPromotionShardKey(sliceID string, files []string) string {
	sliceID = strings.TrimSpace(sliceID)
	if username := homeslice.UsernameFromSliceID(sliceID); username != "" {
		return "home:" + username
	}
	if homeRoot := commonHomeRootFromFiles(files); homeRoot != "" {
		return "home:" + homeRoot
	}
	if sliceID != "" {
		return "slice:" + sliceID
	}
	return "global"
}

func commonHomeRootFromFiles(files []string) string {
	var root string
	for _, rawPath := range normalizeModifiedFiles(files) {
		cleaned := common.CleanRelativePath(rawPath)
		if cleaned == "" {
			continue
		}
		part, _, _ := strings.Cut(cleaned, "/")
		if part == "" {
			continue
		}
		if root == "" {
			root = part
			continue
		}
		if root != part {
			return ""
		}
	}
	return root
}

func (s *sliceServiceServer) promoteSliceBatch(ctx context.Context, batch []rootpromote.Job) error {
	if len(batch) == 0 {
		return nil
	}
	st := s.promotionStore()

	if promoted, err := s.promoteSliceBatchToHomeViews(ctx, batch); err != nil {
		return err
	} else if promoted {
		return s.updateRootPromotionProjectionOffsets(ctx, batch)
	}

	if _, ok := st.(rootPromotionFilePromoter); !ok || hasHomePromotionJob(batch) {
		if err := s.promoteSliceBatchWithGlobalLock(ctx, batch); err != nil {
			return err
		}
		return s.updateRootPromotionProjectionOffsets(ctx, batch)
	}

	start := time.Now()
	rootSliceID, err := s.getRootSliceIDWithStorage(ctx, st)
	if err != nil {
		return err
	}

	fileStart := time.Now()
	if err := st.(rootPromotionFilePromoter).PromoteFilesToRoot(ctx, rootSliceID, storageRootPromotionJobs(batch)); err != nil {
		return err
	}
	fileDuration := time.Since(fileStart)

	stateStart := time.Now()
	latest := latestPromotionJob(batch)
	if err := s.updateRootPromotionState(ctx, rootSliceID, latest, promotionGlobalCommits(batch)); err != nil {
		return err
	}
	stateDuration := time.Since(stateStart)
	totalDuration := time.Since(start)
	if totalDuration > 250*time.Millisecond || len(batch) > 1 {
		log.Printf("root promotion batch jobs=%d files=%d file_sync=%s state_update=%s total=%s", len(batch), len(collectUniquePromotionFiles(batch)), fileDuration, stateDuration, totalDuration)
	}
	return s.updateRootPromotionProjectionOffsets(ctx, batch)
}

func (s *sliceServiceServer) updateRootPromotionProjectionOffsets(ctx context.Context, batch []rootpromote.Job) error {
	eventStore, ok := s.promotionStore().(storage.MergeEventStore)
	if !ok {
		return nil
	}
	latestByShard := make(map[int32]int64)
	for _, job := range batch {
		if job.ProjectionMergeSeq <= 0 || job.ProjectionShardID < 0 {
			continue
		}
		if job.ProjectionMergeSeq > latestByShard[job.ProjectionShardID] {
			latestByShard[job.ProjectionShardID] = job.ProjectionMergeSeq
		}
	}
	for shardID, seq := range latestByShard {
		if err := eventStore.UpdateProjectionOffset(ctx, &models.ProjectionOffset{
			ProjectionName: durablePromotionProjectionName,
			ShardID:        shardID,
			MergeSeq:       seq,
			UpdatedAt:      time.Now(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *sliceServiceServer) promoteSliceBatchToHomeViews(ctx context.Context, batch []rootpromote.Job) (bool, error) {
	groups := make(map[string][]rootpromote.Job)
	for _, job := range batch {
		username, ok, err := s.promotionHomeUsername(ctx, job)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		groups[username] = append(groups[username], job)
	}
	if len(groups) == 0 {
		return false, nil
	}

	start := time.Now()
	rootSliceID, err := s.getRootSliceIDWithStorage(ctx, s.promotionStore())
	if err != nil {
		return false, err
	}

	fileStart := time.Now()
	if err := s.promoteHomeGroups(ctx, groups); err != nil {
		return false, err
	}
	fileDuration := time.Since(fileStart)

	stateStart := time.Now()
	latest := latestPromotionJob(batch)
	if err := s.updateRootPromotionState(ctx, rootSliceID, latest, promotionGlobalCommits(batch)); err != nil {
		return false, err
	}
	stateDuration := time.Since(stateStart)
	totalDuration := time.Since(start)
	if totalDuration > 250*time.Millisecond || len(batch) > 1 {
		log.Printf("home promotion batch homes=%d jobs=%d files=%d home_sync=%s state_update=%s total=%s", len(groups), len(batch), len(collectUniquePromotionFilesIncludingHome(batch)), fileDuration, stateDuration, totalDuration)
	}
	return true, nil
}

func (s *sliceServiceServer) promoteHomeGroups(ctx context.Context, groups map[string][]rootpromote.Job) error {
	if len(groups) == 0 {
		return nil
	}
	usernames := make([]string, 0, len(groups))
	for username := range groups {
		usernames = append(usernames, username)
	}
	sort.Strings(usernames)

	parallelism := len(usernames)
	if parallelism > 8 {
		parallelism = 8
	}
	sem := make(chan struct{}, parallelism)
	errCh := make(chan error, len(usernames))
	var wg sync.WaitGroup

	for _, username := range usernames {
		username := username
		jobs := append([]rootpromote.Job(nil), groups[username]...)
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := s.promoteHomeGroup(ctx, username, jobs); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *sliceServiceServer) promoteHomeGroup(ctx context.Context, username string, jobs []rootpromote.Job) error {
	st := s.promotionStore()
	homeSlice, err := homeslice.EnsureUserHomeSlice(ctx, st, username)
	if err != nil {
		return fmt.Errorf("failed to ensure home slice for %s: %w", username, err)
	}
	copyJobs := nonHomeSourcePromotionJobs(homeSlice.ID, jobs)
	if len(copyJobs) > 0 {
		if promoter, ok := st.(sliceFilePromoter); ok {
			if err := promoter.PromoteFilesToSlice(ctx, homeSlice.ID, storageRootPromotionJobs(copyJobs)); err != nil {
				return fmt.Errorf("failed to publish files into home slice %s: %w", homeSlice.ID, err)
			}
		} else {
			for _, job := range copyJobs {
				if err := s.syncPromotionJobToSlice(ctx, homeSlice.ID, job); err != nil {
					return err
				}
			}
			if err := addFilesToSlice(ctx, st, collectUniquePromotionFiles(copyJobs), homeSlice.ID); err != nil {
				return fmt.Errorf("failed to add promoted files to home slice: %w", err)
			}
		}
	}
	if len(copyJobs) == 0 {
		return nil
	}
	return s.updateHomePromotionState(ctx, homeSlice.ID, copyJobs)
}

func (s *sliceServiceServer) promotionHomeUsername(ctx context.Context, job rootpromote.Job) (string, bool, error) {
	st := s.promotionStore()
	if username := homeslice.UsernameFromSliceID(job.SliceID); username != "" {
		return username, true, nil
	}
	homeRoot := commonHomeRootFromFiles(job.Files)
	if homeRoot == "" {
		return "", false, nil
	}
	if user, err := st.GetUser(ctx, homeRoot); err == nil && user != nil {
		rootPath := strings.TrimPrefix(strings.TrimSpace(user.RootPath), "/")
		if rootPath == "" {
			rootPath = homeslice.RelativeRootPath(user.Username)
		}
		if common.CleanRelativePath(rootPath) == homeRoot {
			return user.Username, true, nil
		}
	}
	sourceSlice, err := st.GetSlice(ctx, strings.TrimSpace(job.SliceID))
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if sliceOwnedByUsername(sourceSlice, homeRoot) {
		return homeRoot, true, nil
	}
	return "", false, nil
}

func sliceOwnedByUsername(slice *models.Slice, username string) bool {
	username = strings.TrimSpace(username)
	if slice == nil || username == "" {
		return false
	}
	if strings.TrimSpace(slice.CreatedBy) == username {
		return true
	}
	for _, owner := range slice.Owners {
		if strings.TrimSpace(owner) == username {
			return true
		}
	}
	return false
}

func nonHomeSourcePromotionJobs(homeSliceID string, jobs []rootpromote.Job) []rootpromote.Job {
	homeSliceID = strings.TrimSpace(homeSliceID)
	result := make([]rootpromote.Job, 0, len(jobs))
	for _, job := range jobs {
		sourceSliceID := strings.TrimSpace(job.SliceID)
		if sourceSliceID == "" || sourceSliceID == homeSliceID || homeslice.IsHomeSliceID(sourceSliceID) {
			continue
		}
		result = append(result, job)
	}
	return result
}

func (s *sliceServiceServer) updateHomePromotionState(ctx context.Context, homeSliceID string, jobs []rootpromote.Job) error {
	homeSliceID = strings.TrimSpace(homeSliceID)
	if homeSliceID == "" || len(jobs) == 0 {
		return nil
	}
	st := s.promotionStore()
	metadata, err := st.GetSliceMetadata(ctx, homeSliceID)
	if err != nil {
		return fmt.Errorf("failed to load home metadata: %w", err)
	}

	ordered := append([]rootpromote.Job(nil), jobs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if !left.CommitTime.Equal(right.CommitTime) {
			return left.CommitTime.Before(right.CommitTime)
		}
		return strings.TrimSpace(left.CommitHash) < strings.TrimSpace(right.CommitHash)
	})

	parentHash := strings.TrimSpace(metadata.HeadCommitHash)
	for _, job := range ordered {
		commitHash := strings.TrimSpace(job.CommitHash)
		if commitHash == "" {
			continue
		}
		if strings.TrimSpace(job.SliceID) != homeSliceID {
			commitTime := job.CommitTime
			if commitTime.IsZero() {
				commitTime = time.Now()
			}
			if err := st.AddSliceCommit(ctx, homeSliceID, &models.Commit{
				CommitHash: commitHash,
				ParentHash: parentHash,
				Timestamp:  commitTime,
				Message:    "publish changeset to home",
			}); err != nil {
				return fmt.Errorf("failed to add home commit: %w", err)
			}
		}
		parentHash = commitHash
	}

	latest := latestPromotionJob(jobs)
	if strings.TrimSpace(latest.CommitHash) != "" {
		metadata.HeadCommitHash = strings.TrimSpace(latest.CommitHash)
	}
	latestFiles := collectUniquePromotionFilesIncludingHome(jobs)
	metadata.ModifiedFiles = latestFiles
	metadata.ModifiedFilesCount = len(latestFiles)
	if latest.CommitTime.IsZero() {
		metadata.LastModified = time.Now()
	} else {
		metadata.LastModified = latest.CommitTime
	}
	if err := st.UpdateSliceMetadata(ctx, homeSliceID, metadata); err != nil {
		return fmt.Errorf("failed to update home metadata: %w", err)
	}
	if username := homeslice.UsernameFromSliceID(homeSliceID); username != "" {
		if err := ciservice.UpdateManifestIndexForHome(ctx, st, username); err != nil {
			log.Printf("Warning: failed to update CI manifest index for home %s: %v", username, err)
		}
	}
	return nil
}

func (s *sliceServiceServer) promoteSliceBatchWithGlobalLock(ctx context.Context, batch []rootpromote.Job) error {
	st := s.promotionStore()
	return rootpromote.WithGlobalLock(func() error {
		rootSliceID, err := s.getRootSliceIDWithStorage(ctx, st)
		if err != nil {
			return err
		}

		rootMetadata, err := st.GetSliceMetadata(ctx, rootSliceID)
		if err != nil {
			return fmt.Errorf("failed to load root metadata: %w", err)
		}

		for _, job := range latestHomeSlicePromotionJobs(batch) {
			if _, err := homeslice.SyncHomeSliceToRoot(ctx, st, job.SliceID); err != nil {
				return fmt.Errorf("failed to sync %s into root: %w", job.SliceID, err)
			}
		}

		for _, job := range batch {
			if homeslice.IsHomeSliceID(job.SliceID) {
				continue
			}
			if err := s.syncPromotionJobToRoot(ctx, rootSliceID, job); err != nil {
				return err
			}
		}

		if err := addFilesToSlice(ctx, st, collectUniquePromotionFiles(batch), rootSliceID); err != nil {
			return fmt.Errorf("failed to add promoted files to root slice: %w", err)
		}

		latest := batch[len(batch)-1]
		if err := updateRootPromotionStateFallback(ctx, st, rootSliceID, rootMetadata, latest, promotionGlobalCommits(batch)); err != nil {
			return err
		}

		return nil
	})
}

func (s *sliceServiceServer) updateRootPromotionState(ctx context.Context, rootSliceID string, latest rootpromote.Job, history []*models.GlobalCommit) error {
	st := s.promotionStore()
	if updater, ok := st.(rootPromotionStateUpdater); ok {
		latestFiles := normalizeModifiedFiles(latest.Files)
		return updater.UpdateRootPromotionState(ctx, rootSliceID, latest.CommitHash, latest.CommitTime, latestFiles, history)
	}
	return rootpromote.WithGlobalLock(func() error {
		rootMetadata, err := st.GetSliceMetadata(ctx, rootSliceID)
		if err != nil {
			return fmt.Errorf("failed to load root metadata: %w", err)
		}
		return updateRootPromotionStateFallback(ctx, st, rootSliceID, rootMetadata, latest, history)
	})
}

func updateRootPromotionStateFallback(ctx context.Context, st storage.Storage, rootSliceID string, rootMetadata *models.SliceMetadata, latest rootpromote.Job, history []*models.GlobalCommit) error {
	state, err := st.GetGlobalState(ctx)
	if err != nil && !errors.Is(err, storage.ErrInvalidInput) {
		return fmt.Errorf("failed to load global state: %w", err)
	}
	if state == nil {
		state = &models.GlobalState{}
	}
	state.GlobalCommitHash = latest.CommitHash
	state.Timestamp = latest.CommitTime
	state.History = append(history, state.History...)

	if err := st.UpdateGlobalState(ctx, state); err != nil {
		return fmt.Errorf("failed to update global state: %w", err)
	}

	rootMetadata.HeadCommitHash = state.GlobalCommitHash
	latestFiles := normalizeModifiedFiles(latest.Files)
	rootMetadata.ModifiedFiles = latestFiles
	rootMetadata.ModifiedFilesCount = len(latestFiles)
	rootMetadata.LastModified = state.Timestamp
	if err := st.UpdateSliceMetadata(ctx, rootSliceID, rootMetadata); err != nil {
		return fmt.Errorf("failed to update root metadata: %w", err)
	}
	return nil
}

func (s *sliceServiceServer) syncPromotionJobToRoot(ctx context.Context, rootSliceID string, job rootpromote.Job) error {
	return s.syncPromotionJobToSlice(ctx, rootSliceID, job)
}

func (s *sliceServiceServer) syncPromotionJobToSlice(ctx context.Context, targetSliceID string, job rootpromote.Job) error {
	st := s.promotionStore()
	sliceID := strings.TrimSpace(job.SliceID)
	targetSliceID = strings.TrimSpace(targetSliceID)
	if sliceID == "" || targetSliceID == "" {
		return nil
	}
	if _, err := st.GetSlice(ctx, sliceID); err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil
		}
		return fmt.Errorf("failed to load promotion source slice %s: %w", sliceID, err)
	}

	for _, rawPath := range normalizeModifiedFiles(job.Files) {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}

		content, err := storage.ReadSliceFileContent(ctx, st, sliceID, filePath)
		if err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				continue
			}
			return fmt.Errorf("failed to read promoted file %s from %s: %w", filePath, sliceID, err)
		}

		var executable bool
		var symlinkTarget string
		if entry, entryErr := st.GetEntryByPath(ctx, sliceID, filePath); entryErr == nil && entry != nil {
			executable = entry.Executable
			symlinkTarget = entry.SymlinkTarget
		} else if entryErr != nil && !errors.Is(entryErr, storage.ErrEntryNotFound) {
			return fmt.Errorf("failed to load promoted entry metadata for %s: %w", filePath, entryErr)
		}

		manifest, err := storage.WriteSliceFileManifestWithMetadata(ctx, st, targetSliceID, filePath, append([]byte(nil), content.Content...), executable, symlinkTarget)
		if err != nil {
			return fmt.Errorf("failed to write promoted manifest for %s in %s: %w", filePath, targetSliceID, err)
		}
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:            common.GenerateEntryID(targetSliceID, filePath),
			Path:          filePath,
			Type:          "file",
			ParentID:      targetSliceID,
			Size:          manifest.TotalSize,
			Hash:          manifest.Hash,
			Executable:    manifest.Executable,
			SymlinkTarget: manifest.SymlinkTarget,
		}); err != nil {
			return fmt.Errorf("failed to materialize promoted entry for %s in %s: %w", filePath, targetSliceID, err)
		}
	}

	return nil
}

func collectUniquePromotionFiles(batch []rootpromote.Job) []string {
	if len(batch) == 0 {
		return nil
	}
	dedup := make(map[string]struct{})
	files := make([]string, 0)
	for _, job := range batch {
		if homeslice.IsHomeSliceID(job.SliceID) {
			continue
		}
		for _, fileID := range normalizeModifiedFiles(job.Files) {
			if _, exists := dedup[fileID]; exists {
				continue
			}
			dedup[fileID] = struct{}{}
			files = append(files, fileID)
		}
	}
	return files
}

func collectUniquePromotionFilesIncludingHome(batch []rootpromote.Job) []string {
	if len(batch) == 0 {
		return nil
	}
	dedup := make(map[string]struct{})
	files := make([]string, 0)
	for _, job := range batch {
		for _, fileID := range normalizeModifiedFiles(job.Files) {
			if _, exists := dedup[fileID]; exists {
				continue
			}
			dedup[fileID] = struct{}{}
			files = append(files, fileID)
		}
	}
	return files
}

func hasHomePromotionJob(batch []rootpromote.Job) bool {
	for _, job := range batch {
		if homeslice.IsHomeSliceID(job.SliceID) {
			return true
		}
	}
	return false
}

func storageRootPromotionJobs(batch []rootpromote.Job) []storage.RootPromotionJob {
	if len(batch) == 0 {
		return nil
	}
	jobs := make([]storage.RootPromotionJob, 0, len(batch))
	for _, job := range batch {
		if homeslice.IsHomeSliceID(job.SliceID) {
			continue
		}
		jobs = append(jobs, storage.RootPromotionJob{
			SliceID:    job.SliceID,
			CommitHash: job.CommitHash,
			Files:      append([]string(nil), job.Files...),
			CommitTime: job.CommitTime,
		})
	}
	return jobs
}

func promotionGlobalCommits(batch []rootpromote.Job) []*models.GlobalCommit {
	if len(batch) == 0 {
		return nil
	}
	history := make([]*models.GlobalCommit, 0, len(batch))
	for i := len(batch) - 1; i >= 0; i-- {
		job := batch[i]
		if strings.TrimSpace(job.CommitHash) == "" {
			continue
		}
		history = append(history, &models.GlobalCommit{
			CommitHash:     job.CommitHash,
			Timestamp:      job.CommitTime,
			MergedSliceIDs: []string{job.SliceID},
		})
	}
	return history
}

func latestPromotionJob(batch []rootpromote.Job) rootpromote.Job {
	if len(batch) == 0 {
		return rootpromote.Job{}
	}
	latest := batch[0]
	for _, job := range batch[1:] {
		if job.CommitTime.After(latest.CommitTime) || (job.CommitTime.Equal(latest.CommitTime) && strings.TrimSpace(job.CommitHash) > strings.TrimSpace(latest.CommitHash)) {
			latest = job
		}
	}
	return latest
}

func (s *sliceServiceServer) hydrateSliceEntryMetadataFromParent(ctx context.Context, slice, parentSlice *models.Slice, filePaths []string) error {
	if slice == nil || parentSlice == nil {
		return nil
	}
	for _, rawPath := range normalizeModifiedFiles(filePaths) {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}
		parentEntry, err := s.storage.GetEntryByPath(ctx, parentSlice.ID, filePath)
		if err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				continue
			}
			return err
		}
		if parentEntry == nil || parentEntry.Type != "file" {
			continue
		}
		childEntry, err := s.storage.GetEntryByPath(ctx, slice.ID, filePath)
		if err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				continue
			}
			return err
		}
		if childEntry == nil || childEntry.Type != "file" {
			continue
		}
		if childEntry.Size == parentEntry.Size &&
			childEntry.Executable == parentEntry.Executable &&
			childEntry.SymlinkTarget == parentEntry.SymlinkTarget {
			continue
		}
		updated := *childEntry
		updated.Size = parentEntry.Size
		updated.Executable = parentEntry.Executable
		updated.SymlinkTarget = parentEntry.SymlinkTarget
		if err := s.storage.UpdateEntry(ctx, &updated); err != nil {
			return err
		}
	}
	return nil
}

func (s *sliceServiceServer) waitForQueuedPromotions(ctx context.Context) error {
	if err := s.rootPromotionQueue().Wait(ctx); err != nil {
		return err
	}
	return s.waitForQueuedHistoryProjections(ctx)
}

func (s *sliceServiceServer) WaitForQueuedPromotions(ctx context.Context) error {
	return s.waitForQueuedPromotions(ctx)
}

func (s *sliceServiceServer) rootPromotionQueue() *rootpromote.Queue {
	s.promotionQueueMu.Lock()
	defer s.promotionQueueMu.Unlock()
	if s.promotionQueue != nil {
		return s.promotionQueue
	}
	workerCount := s.promotionWorkerCount
	if workerCount <= 0 {
		workerCount = 1
		if _, ok := s.promotionStore().(rootPromotionFilePromoter); ok {
			workerCount = defaultPromotionWorkerCount
		}
	}
	s.promotionQueue = rootpromote.NewWithWorkers(s.promotionBatchWindow, s.promotionBatchMaxSize, workerCount, func(ctx context.Context, batch []rootpromote.Job) error {
		if err := s.promoteSliceBatch(ctx, batch); err != nil {
			sliceID := ""
			if len(batch) > 0 {
				sliceID = batch[0].SliceID
			}
			log.Printf("failed to promote %d queued commits for slice %s: %v", len(batch), sliceID, err)
			return err
		}
		return nil
	})
	return s.promotionQueue
}

func latestHomeSlicePromotionJobs(batch []rootpromote.Job) []rootpromote.Job {
	if len(batch) == 0 {
		return nil
	}
	latestBySlice := make(map[string]rootpromote.Job, len(batch))
	order := make([]string, 0, len(batch))
	for _, job := range batch {
		sliceID := strings.TrimSpace(job.SliceID)
		if !homeslice.IsHomeSliceID(sliceID) {
			continue
		}
		if _, seen := latestBySlice[sliceID]; !seen {
			order = append(order, sliceID)
		}
		latestBySlice[sliceID] = job
	}
	sort.Strings(order)
	result := make([]rootpromote.Job, 0, len(order))
	for _, sliceID := range order {
		result = append(result, latestBySlice[sliceID])
	}
	return result
}

func (s *sliceServiceServer) getRootSliceID(ctx context.Context) (string, error) {
	return s.getRootSliceIDWithStorage(ctx, s.storage)
}

func (s *sliceServiceServer) getRootSliceIDWithStorage(ctx context.Context, st storage.Storage) (string, error) {
	s.rootSliceMu.RLock()
	if s.rootSliceID != "" {
		id := s.rootSliceID
		s.rootSliceMu.RUnlock()
		return id, nil
	}
	s.rootSliceMu.RUnlock()

	s.rootSliceMu.Lock()
	defer s.rootSliceMu.Unlock()
	if s.rootSliceID != "" {
		return s.rootSliceID, nil
	}

	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		if !errors.Is(err, storage.ErrSliceNotFound) {
			return "", fmt.Errorf("failed to load root slice: %w", err)
		}
		if err := st.InitializeRootSlice(ctx); err != nil {
			return "", fmt.Errorf("failed to initialize root slice: %w", err)
		}
		rootSlice, err = st.GetRootSlice(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to load root slice after initialization: %w", err)
		}
		log.Println("Root slice initialized successfully")
	}

	s.rootSliceID = rootSlice.ID
	return s.rootSliceID, nil
}

// createCommitSnapshot creates a snapshot of the current file state for a commit.
func (s *sliceServiceServer) createCommitSnapshotWithStorage(ctx context.Context, st storage.Storage, sliceID, commitHash, parentHash string, modifiedFiles []string, timestamp time.Time) error {
	files := make(map[string]string)
	parentSnapshotLoaded := false

	if strings.TrimSpace(parentHash) != "" {
		parentSnapshot, err := st.GetCommitSnapshot(ctx, strings.TrimSpace(parentHash))
		if err == nil && parentSnapshot != nil {
			parentSnapshotLoaded = true
			for filePath, contentHash := range parentSnapshot.Files {
				cleanedPath := cleanDiffPath(filePath)
				cleanedHash := strings.TrimSpace(contentHash)
				if cleanedPath == "" || !isUsableContentHash(cleanedPath, cleanedHash) {
					continue
				}
				files[cleanedPath] = cleanedHash
			}
		}
	}
	if !parentSnapshotLoaded {
		slice, err := st.GetSlice(ctx, sliceID)
		if err != nil {
			return fmt.Errorf("failed to load slice for snapshot: %w", err)
		}
		existingHashes, err := getFileManifestHashes(ctx, st, sliceID, normalizeModifiedFiles(slice.Files))
		if err != nil {
			return fmt.Errorf("failed to load slice manifests for snapshot: %w", err)
		}
		for filePath, hash := range existingHashes {
			if !isUsableContentHash(filePath, hash) {
				continue
			}
			files[filePath] = hash
		}
	}

	currentHashes, err := getFileManifestHashes(ctx, st, sliceID, normalizeModifiedFiles(modifiedFiles))
	if err != nil {
		return fmt.Errorf("failed to load changed file manifests for snapshot: %w", err)
	}
	existingEntries, err := getExistingEntriesByPaths(ctx, st, sliceID, normalizeModifiedFiles(modifiedFiles))
	if err != nil {
		return fmt.Errorf("failed to load changed file entries for snapshot: %w", err)
	}
	for _, rawPath := range normalizeModifiedFiles(modifiedFiles) {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}
		if !existingEntries[filePath] {
			delete(files, filePath)
			continue
		}

		hash := strings.TrimSpace(currentHashes[filePath])
		if hash == "" {
			delete(files, filePath)
			continue
		}
		files[filePath] = hash
	}

	snapshot := &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    sliceID,
		Files:      files,
		Timestamp:  timestamp,
	}

	return st.SaveCommitSnapshot(ctx, snapshot)
}

// recordFileChanges creates FileChangeRecord entries for each file in the changeset.
func (s *sliceServiceServer) recordFileChangesWithStorage(ctx context.Context, st storage.Storage, cs *models.Changeset, commitHash string, parentHash string, timestamp time.Time) error {
	previousHashes := make(map[string]string)
	parentSnapshotLoaded := false
	if strings.TrimSpace(parentHash) != "" {
		snapshot, err := st.GetCommitSnapshot(ctx, strings.TrimSpace(parentHash))
		if err == nil && snapshot != nil {
			parentSnapshotLoaded = true
			for filePath, contentHash := range snapshot.Files {
				cleanedPath := cleanDiffPath(filePath)
				cleanedHash := strings.TrimSpace(contentHash)
				if cleanedPath == "" || !isUsableContentHash(cleanedPath, cleanedHash) {
					continue
				}
				previousHashes[cleanedPath] = cleanedHash
			}
		}
	}

	currentPaths := normalizeModifiedFiles(cs.ModifiedFiles)
	currentEntries, err := getExistingEntriesByPaths(ctx, st, cs.SliceID, currentPaths)
	if err != nil {
		return fmt.Errorf("failed to load file entries for change history: %w", err)
	}
	currentHashes, err := getFileManifestHashes(ctx, st, cs.SliceID, currentPaths)
	if err != nil {
		return fmt.Errorf("failed to load file manifests for change history: %w", err)
	}

	var changes []*models.FileChangeRecord
	for _, rawPath := range currentPaths {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}

		oldHash := strings.TrimSpace(previousHashes[filePath])
		newHash := ""
		linesAdded := 0
		linesDeleted := 0
		hasCurrentEntry := currentEntries[filePath]
		if oldHash == "" && !parentSnapshotLoaded {
			oldHash = s.findPreviousKnownFileHashWithStorage(ctx, st, cs.SliceID, filePath, "")
		}
		newHash = strings.TrimSpace(currentHashes[filePath])

		changeType := models.ChangeTypeModify
		switch {
		case oldHash == "" && (newHash != "" || hasCurrentEntry):
			changeType = models.ChangeTypeAdd
			fileContent, readErr := storage.ReadVersionedFileContent(ctx, st, newHash)
			if readErr == nil && fileContent != nil && len(fileContent.Content) > 0 {
				linesAdded = countTextLines(fileContent.Content)
			}
		case oldHash != "" && newHash == "" && !hasCurrentEntry:
			changeType = models.ChangeTypeDelete
			if previousContent, hashErr := storage.ReadVersionedFileContent(ctx, st, oldHash); hashErr == nil && previousContent != nil {
				linesDeleted = countTextLines(previousContent.Content)
			}
		case oldHash != "" && (newHash != "" || hasCurrentEntry):
			changeType = models.ChangeTypeModify
		}

		changes = append(changes, &models.FileChangeRecord{
			ID:           common.GenerateFileChangeID(commitHash, filePath),
			SliceID:      cs.SliceID,
			CommitHash:   commitHash,
			Path:         filePath,
			ChangeType:   changeType,
			OldHash:      oldHash,
			NewHash:      newHash,
			LinesAdded:   linesAdded,
			LinesDeleted: linesDeleted,
			Author:       cs.Author,
			Message:      cs.Message,
			Timestamp:    timestamp,
		})
	}

	if len(changes) == 0 {
		return nil
	}
	return st.AddFileChanges(ctx, changes)
}

func effectiveContentHash(filePath string, content *models.FileContent) string {
	if content == nil {
		return ""
	}

	hash := strings.TrimSpace(content.Hash)
	if isUsableContentHash(filePath, hash) {
		return hash
	}

	if content.Content == nil && content.Size > 0 {
		return ""
	}

	return hashBytes(content.Content)
}

func hashBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func countTextLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	text := string(content)
	lines := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		lines++
	}
	return lines
}

func folderSelectionExistsInEntries(entries []*models.DirectoryEntry, storedPath string) (exists bool, isFile bool) {
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		cleaned := common.CleanRelativePath(entry.Path)
		if cleaned == storedPath {
			return true, entry.Type == "file"
		}
		if strings.HasPrefix(cleaned, storedPath+"/") {
			return true, false
		}
	}
	return false, false
}

func uniqueFolderMountAlias(existing []models.SliceFolderMount, baseAlias string) string {
	used := make(map[string]struct{}, len(existing))
	for _, m := range existing {
		used[common.CleanRelativePath(m.Alias)] = struct{}{}
	}
	if _, exists := used[baseAlias]; !exists {
		return baseAlias
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s_%d", baseAlias, n)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func deduplicateSortedStrings(s []string) []string {
	if len(s) <= 1 {
		return s
	}
	seen := make(map[string]struct{}, len(s))
	result := make([]string, 0, len(s))
	for _, v := range s {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	sort.Strings(result)
	return result
}
