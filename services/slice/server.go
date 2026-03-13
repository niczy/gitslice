package sliceservice

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	"github.com/niczy/gitslice/internal/rootpromote"
	"github.com/niczy/gitslice/internal/sliceconfig"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"github.com/pmezard/go-difflib/difflib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type sliceServiceServer struct {
	slicev1.UnimplementedSliceServiceServer
	storage               storage.Storage
	rootSliceMu           sync.RWMutex
	rootSliceID           string
	promotionQueueMu      sync.Mutex
	promotionQueue        *rootpromote.Queue
	promotionBatchWindow  time.Duration
	promotionBatchMaxSize int
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

const (
	defaultPromotionBatchWindow  = rootpromote.DefaultBatchWindow
	defaultPromotionBatchMaxSize = rootpromote.DefaultBatchMaxSize
	revertChangesetHashPrefix    = "revert~"
	revertAllChangesToken        = "*"
)

func newSliceServiceServer(st storage.Storage) *sliceServiceServer {
	return &sliceServiceServer{
		storage:               st,
		promotionBatchWindow:  defaultPromotionBatchWindow,
		promotionBatchMaxSize: defaultPromotionBatchMaxSize,
	}
}

// RegisterGRPCServer registers the slice service handlers on an existing gRPC server.
func RegisterGRPCServer(srv *grpc.Server, st storage.Storage) {
	slicev1.RegisterSliceServiceServer(srv, newSliceServiceServer(st))
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

func (s *sliceServiceServer) CheckoutSlice(ctx context.Context, req *slicev1.CheckoutRequest) (*slicev1.CheckoutResponse, error) {
	log.Printf("CheckoutSlice called: slice_id=%s, commit_hash=%s", req.SliceId, req.CommitHash)

	// Validate slice ID
	if err := common.ValidateSliceID(req.SliceId); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid slice ID: %v", err))
	}

	// Get slice metadata
	metadata, err := s.storage.GetSliceMetadata(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}

	// Get slice
	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}
	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	entries, err := s.collectSliceEntries(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list slice entries: %v", err))
	}

	knownHashes := make(map[string]struct{}, len(req.GetKnownHashes()))
	for _, hash := range req.GetKnownHashes() {
		hash = strings.TrimSpace(hash)
		if hash == "" {
			continue
		}
		knownHashes[hash] = struct{}{}
	}

	var fileMetadata []*slicev1.FileMetadata
	for _, entry := range entries {
		if entry == nil || entry.Type != "file" {
			continue
		}

		storedPath := entry.Path
		size := entry.Size
		hash := strings.TrimSpace(entry.Hash)
		var manifestBlocks []models.Block
		if manifest, manifestErr := s.storage.GetFileManifest(ctx, req.SliceId, storedPath); manifestErr == nil && manifest != nil {
			if size == 0 {
				size = manifest.TotalSize
			}
			if hash == "" {
				hash = strings.TrimSpace(manifest.Hash)
			}
			manifestBlocks = append(manifestBlocks, manifest.Blocks...)
		} else if manifestErr != nil && manifestErr != storage.ErrEntryNotFound {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load manifest for %s: %v", storedPath, manifestErr))
		}
		if hash != "" && (len(manifestBlocks) == 0 || size == 0) {
			versionedManifest, manifestErr := s.storage.GetVersionedFileManifest(ctx, hash)
			if manifestErr == nil && versionedManifest != nil {
				if size == 0 {
					size = versionedManifest.TotalSize
				}
				if len(manifestBlocks) == 0 {
					manifestBlocks = append(manifestBlocks, versionedManifest.Blocks...)
				}
			} else if manifestErr != nil && manifestErr != storage.ErrEntryNotFound {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load versioned manifest for %s: %v", storedPath, manifestErr))
			}
		}

		fileMetadata = append(fileMetadata, &slicev1.FileMetadata{
			FileId:     storedPath,
			Path:       common.SliceDisplayPath(slice, storedPath),
			Size:       size,
			Hash:       hash,
			ContentUrl: "", // No presigned URL for in-memory storage
			Blocks:     checkoutProtoBlocks(manifestBlocks),
		})
	}
	sort.Slice(fileMetadata, func(i, j int) bool {
		return fileMetadata[i].GetPath() < fileMetadata[j].GetPath()
	})

	// If no files in storage, create metadata from slice definition
	if len(fileMetadata) == 0 {
		for _, fileID := range slice.Files {
			fileMetadata = append(fileMetadata, &slicev1.FileMetadata{
				FileId:     fileID,
				Path:       common.SliceDisplayPath(slice, fileID),
				Size:       0,
				Hash:       "",
				ContentUrl: "",
			})
		}
	}

	// Convert file contents to proto format.
	var fileContents []*slicev1.FileContent
	var blockContents []*slicev1.BlockContent
	sentBlocks := make(map[string]struct{})
	for _, meta := range fileMetadata {
		if meta == nil {
			continue
		}
		if meta.GetHash() != "" {
			if _, ok := knownHashes[meta.GetHash()]; ok {
				continue
			}
		}
		if len(meta.GetBlocks()) > 0 {
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
				if _, ok := sentBlocks[blockHash]; ok {
					continue
				}
				payload, err := s.storage.GetBlock(ctx, blockHash)
				if err != nil {
					return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load block %s for %s: %v", blockHash, meta.GetFileId(), err))
				}
				blockContents = append(blockContents, &slicev1.BlockContent{
					Hash:    blockHash,
					Content: payload,
				})
				sentBlocks[blockHash] = struct{}{}
			}
			continue
		}
		if meta.GetSize() == 0 {
			continue
		}
		file, err := storage.ReadSliceFileContent(ctx, s.storage, req.SliceId, meta.GetFileId())
		if err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				continue
			}
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load content for %s: %v", meta.GetFileId(), err))
		}
		fileContents = append(fileContents, &slicev1.FileContent{
			FileId:  file.FileID,
			Content: file.Content,
		})
	}

	manifest := &slicev1.SliceManifest{
		CommitHash:   metadata.HeadCommitHash,
		FileMetadata: fileMetadata,
	}

	return &slicev1.CheckoutResponse{
		Manifest: manifest,
		Files:    fileContents,
		Blocks:   blockContents,
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

	modifiedFiles := normalizeModifiedFiles(req.ModifiedFiles)

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
		if !authz.HasSliceViewAccess(slice, username) {
			return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
		}

		existing.Hash = fmt.Sprintf("hash-%d", time.Now().UnixNano())
		existing.BaseCommitHash = req.BaseCommitHash
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

		return &slicev1.CreateChangesetResponse{
			ChangesetId:   existing.ID,
			ChangesetHash: existing.Hash,
			Status:        convertChangesetStatusToProto(existing.Status),
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
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	id := ""
	hash := fmt.Sprintf("hash-%d", time.Now().UnixNano())

	cs := &models.Changeset{
		ID:             id,
		Hash:           hash,
		SliceID:        req.SliceId,
		BaseCommitHash: req.BaseCommitHash,
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

	return &slicev1.CreateChangesetResponse{
		ChangesetId:   cs.ID,
		ChangesetHash: cs.Hash,
		Status:        convertChangesetStatusToProto(cs.Status),
	}, nil
}

func (s *sliceServiceServer) ReviewChangeset(ctx context.Context, req *slicev1.ReviewChangesetRequest) (*slicev1.ReviewChangesetResponse, error) {
	log.Printf("ReviewChangeset called: changeset_id=%s", req.ChangesetId)

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
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
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
	reviewChanges, warnings := s.buildReviewChanges(ctx, reviewCS)
	if len(reviewChanges) > 0 {
		diff = summarizeReviewChanges(reviewChanges)
	}

	return &slicev1.ReviewChangesetResponse{
		Changeset:    convertChangesetToProto(reviewCS),
		Diff:         diff,
		ReviewStatus: slicev1.ReviewStatus_READY_FOR_MERGE,
		Warnings:     warnings,
		Changes:      reviewChanges,
		Snapshot:     changesetSnapshotToProto(snapshot),
	}, nil
}

func (s *sliceServiceServer) ListChangesetSnapshots(ctx context.Context, req *slicev1.ListChangesetSnapshotsRequest) (*slicev1.ListChangesetSnapshotsResponse, error) {
	log.Printf("ListChangesetSnapshots called: changeset_id=%s", req.GetChangesetId())

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	cs, err := s.storage.GetChangeset(ctx, req.GetChangesetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", req.GetChangesetId()))
	}

	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", cs.SliceID))
	}
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 100
	}

	snapshots, err := s.storage.ListChangesetSnapshots(ctx, cs.ID, limit)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list changeset snapshots: %v", err))
	}
	if len(snapshots) == 0 {
		snapshots = []*models.ChangesetSnapshot{buildSyntheticChangesetSnapshot(cs)}
	}

	resp := &slicev1.ListChangesetSnapshotsResponse{}
	for _, snapshot := range snapshots {
		resp.Snapshots = append(resp.Snapshots, changesetSnapshotToProto(snapshot))
	}
	return resp, nil
}

func (s *sliceServiceServer) MergeChangeset(ctx context.Context, req *slicev1.MergeChangesetRequest) (*slicev1.MergeChangesetResponse, error) {
	log.Printf("MergeChangeset called: changeset_id=%s", req.ChangesetId)

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	return s.mergeChangeset(ctx, req.GetChangesetId(), username, false)
}

// MergeChangesetUsingCurrentHead marks a changeset as merged and publishes the
// slice's existing head commit instead of creating a new no-op merge commit.
func (s *sliceServiceServer) MergeChangesetUsingCurrentHead(ctx context.Context, changesetID string) (*slicev1.MergeChangesetResponse, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	return s.mergeChangeset(ctx, changesetID, username, true)
}

func (s *sliceServiceServer) mergeChangeset(ctx context.Context, changesetID, username string, useCurrentHead bool) (*slicev1.MergeChangesetResponse, error) {
	cs, err := s.storage.GetChangeset(ctx, changesetID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", changesetID))
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", cs.SliceID))
	}
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	modifiedFiles := normalizeModifiedFiles(cs.ModifiedFiles)
	cs.ModifiedFiles = modifiedFiles

	if err := s.storage.LockSliceAndFiles(ctx, cs.SliceID, modifiedFiles); err != nil {
		if errors.Is(err, storage.ErrLockHeld) {
			return nil, status.Error(codes.Aborted, "slice or files are locked by another operation")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to acquire locks: %v", err))
	}
	defer s.storage.UnlockSliceAndFiles(ctx, cs.SliceID, modifiedFiles)

	if !isRevertChangesetHash(cs.Hash) {
		var conflicts []*slicev1.Conflict
		for _, fileID := range modifiedFiles {
			slices, err := s.storage.GetActiveSlicesForFile(ctx, fileID)
			if err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to check conflicts: %v", err))
			}

			var conflictingSlices []string
			for _, sliceID := range slices {
				if sliceID != cs.SliceID {
					conflictingSlices = append(conflictingSlices, sliceID)
				}
			}

			if len(conflictingSlices) > 0 {
				conflicts = append(conflicts, &slicev1.Conflict{FileId: fileID, ConflictingSliceIds: conflictingSlices})
			}
		}

		if len(conflicts) > 0 {
			return &slicev1.MergeChangesetResponse{
				Status:        slicev1.MergeStatus_MERGE_STATUS_CONFLICT,
				NewCommitHash: "",
				ChangesetId:   cs.ID,
				Conflicts:     conflicts,
			}, nil
		}
	}

	appliedRevertChanges, err := s.applyRevertChangesetContent(ctx, cs)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to apply revert changeset content: %v", err))
	}
	if len(appliedRevertChanges) == 0 {
		for _, fileID := range modifiedFiles {
			// Conflicts were already checked under lock above. At this point either
			// no owner exists or this slice is already the sole owner.
			if err := s.storage.AddFileToSlice(ctx, fileID, cs.SliceID); err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to mark file ownership: %v", err))
			}
		}
	}

	newCommit := fmt.Sprintf("commit-%d", time.Now().UnixNano())
	cs.Status = models.ChangesetStatusMerged
	now := time.Now()
	cs.MergedAt = &now

	if err := s.storage.UpdateChangeset(ctx, cs); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update changeset: %v", err))
	}

	metadata, err := s.storage.GetSliceMetadata(ctx, cs.SliceID)
	if err == nil {
		promotionCommitHash := newCommit
		promotionCommitTime := now
		if useCurrentHead {
			promotionCommitHash = strings.TrimSpace(metadata.HeadCommitHash)
			if promotionCommitHash == "" {
				return nil, status.Error(codes.FailedPrecondition, "slice head is empty")
			}
			existingCommit, commitErr := s.storage.GetCommitByHash(ctx, cs.SliceID, promotionCommitHash)
			if commitErr != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice head commit: %v", commitErr))
			}
			if existingCommit != nil && !existingCommit.Timestamp.IsZero() {
				promotionCommitTime = existingCommit.Timestamp
			}
		} else {
			parentHash := metadata.HeadCommitHash
			metadata.HeadCommitHash = newCommit
			metadata.ModifiedFiles = modifiedFiles
			metadata.ModifiedFilesCount = len(modifiedFiles)

			if err := s.storage.UpdateSliceMetadata(ctx, cs.SliceID, metadata); err != nil {
				log.Printf("Warning: failed to update slice metadata for %s: %v", cs.SliceID, err)
			}

			if err := s.storage.AddSliceCommit(ctx, cs.SliceID, &models.Commit{
				CommitHash: newCommit,
				ParentHash: parentHash,
				Timestamp:  now,
				Message:    cs.Message,
			}); err != nil {
				log.Printf("Warning: failed to add commit to slice %s: %v", cs.SliceID, err)
			}

			// Create commit snapshot for versioned file access
			if err := s.createCommitSnapshot(ctx, cs.SliceID, newCommit, parentHash, modifiedFiles, now); err != nil {
				log.Printf("Warning: failed to create commit snapshot for %s: %v", newCommit, err)
			}

			// Record file change history for each modified file
			if err := s.recordFileChanges(ctx, cs, newCommit, parentHash, now); err != nil {
				log.Printf("Warning: failed to record file changes for commit %s: %v", newCommit, err)
			}
		}

		if err := s.enqueueRootPromotion(ctx, cs.SliceID, promotionCommitHash, modifiedFiles, promotionCommitTime); err != nil {
			log.Printf("failed to enqueue promotion for slice %s: %v", cs.SliceID, err)
		}
		if useCurrentHead {
			newCommit = promotionCommitHash
		}
	}
	if changesetTouchesConfig(modifiedFiles) {
		if err := sliceconfig.ApplyFromFileTree(ctx, s.storage); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to sync %s: %v", sliceconfig.ConfigFilePath, err))
		}
	}

	return &slicev1.MergeChangesetResponse{
		Status:        slicev1.MergeStatus_MERGE_STATUS_SUCCESS,
		NewCommitHash: newCommit,
		ChangesetId:   cs.ID,
		Conflicts:     []*slicev1.Conflict{},
	}, nil
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
	if !authz.HasSliceViewAccess(slice, username) {
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
	if !authz.HasSliceViewAccess(slice, username) {
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
	return fmt.Sprintf("%s%s~%s~%d", revertChangesetHashPrefix, encodedCommit, encodedChange, time.Now().UnixNano())
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

func (s *sliceServiceServer) buildReviewChanges(ctx context.Context, cs *models.Changeset) ([]*filev1.FileChangeRecord, []string) {
	targetChanges, err := s.resolveRevertSourceChanges(ctx, cs)
	if err != nil {
		return nil, []string{err.Error()}
	}
	if len(targetChanges) == 0 {
		return s.buildStandardReviewChanges(ctx, cs)
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

func (s *sliceServiceServer) buildStandardReviewChanges(ctx context.Context, cs *models.Changeset) ([]*filev1.FileChangeRecord, []string) {
	if cs == nil {
		return nil, nil
	}

	paths := normalizeModifiedFiles(cs.ModifiedFiles)
	if len(paths) == 0 {
		return nil, nil
	}

	reviewChanges := make([]*filev1.FileChangeRecord, 0, len(paths))
	warnings := make([]string, 0, 1)
	missingPatchCount := 0

	for idx, rawPath := range paths {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}

		before, beforeWarning := s.loadReviewFileAtBase(ctx, cs, filePath)
		if beforeWarning != "" {
			warnings = append(warnings, beforeWarning)
		}
		after, afterWarning := s.loadReviewFileAtHead(ctx, cs, filePath)
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
			ID:           fmt.Sprintf("%s-review-%d", cs.ID, idx+1),
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
	cleanedPath := cleanDiffPath(filePath)
	if cleanedPath == "" {
		return ""
	}
	history, err := s.storage.GetFileHistory(ctx, strings.TrimSpace(sliceID), cleanedPath, 64, strings.TrimSpace(fromCommit))
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
	cleanedPath := cleanDiffPath(filePath)
	if cleanedPath == "" {
		return fmt.Errorf("file path is empty")
	}

	if err := s.storage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       fmt.Sprintf("%s:%s", sliceID, cleanedPath),
		Path:     cleanedPath,
		Type:     "file",
		ParentID: sliceID,
		Size:     int64(len(data)),
	}); err != nil {
		return fmt.Errorf("failed to upsert file entry: %w", err)
	}

	manifest, err := storage.WriteSliceFileManifest(ctx, s.storage, sliceID, cleanedPath, append([]byte(nil), data...))
	if err != nil {
		return fmt.Errorf("failed to upsert file content: %w", err)
	}
	if expected := strings.TrimSpace(contentHash); expected != "" && expected != strings.TrimSpace(manifest.Hash) {
		return fmt.Errorf("content hash mismatch for %s", cleanedPath)
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
		ID:           fmt.Sprintf("%s-revert-%s", cs.ID, target.ID),
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

	snapshot := &models.ChangesetSnapshot{
		ID:             fmt.Sprintf("%s-snapshot-%d", cs.ID, nextVersion),
		ChangesetID:    cs.ID,
		Version:        nextVersion,
		Hash:           cs.Hash,
		BaseCommitHash: cs.BaseCommitHash,
		ModifiedFiles:  normalizeModifiedFiles(cs.ModifiedFiles),
		Author:         cs.Author,
		Message:        cs.Message,
		CreatedAt:      time.Now(),
	}
	return s.storage.CreateChangesetSnapshot(ctx, snapshot)
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
		ID:             fmt.Sprintf("%s-snapshot-1", cs.ID),
		ChangesetID:    cs.ID,
		Version:        1,
		Hash:           cs.Hash,
		BaseCommitHash: cs.BaseCommitHash,
		ModifiedFiles:  normalizeModifiedFiles(cs.ModifiedFiles),
		Author:         cs.Author,
		Message:        cs.Message,
		CreatedAt:      createdAt,
	}
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
		SnapshotId:     snapshot.ID,
		ChangesetId:    snapshot.ChangesetID,
		Version:        snapshot.Version,
		Hash:           snapshot.Hash,
		BaseCommitHash: snapshot.BaseCommitHash,
		ModifiedFiles:  normalizeModifiedFiles(snapshot.ModifiedFiles),
		Author:         snapshot.Author,
		Message:        snapshot.Message,
		CreatedAt:      snapshot.CreatedAt.Unix(),
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
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	newBase := fmt.Sprintf("base-%d", time.Now().UnixNano())
	cs.BaseCommitHash = newBase
	if err := s.storage.UpdateChangeset(ctx, cs); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update changeset: %v", err))
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
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
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
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
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

	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	var statusFilter *models.ChangesetStatus
	if req.StatusFilter >= 0 {
		converted := convertProtoStatusToModel(req.StatusFilter)
		statusFilter = &converted
	}

	changesets, err := s.storage.ListChangesets(ctx, req.SliceId, statusFilter, int(req.Limit))
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list changesets: %v", err))
	}

	response := &slicev1.ListChangesetsResponse{}
	for _, cs := range changesets {
		response.Changesets = append(response.Changesets, convertChangesetToProto(cs))
	}

	return response, nil
}

func convertChangesetToProto(cs *models.Changeset) *slicev1.ChangesetInfo {
	status := convertChangesetStatusToProto(cs.Status)

	var mergedAt int64
	if cs.MergedAt != nil {
		mergedAt = cs.MergedAt.Unix()
	}

	return &slicev1.ChangesetInfo{
		ChangesetId:    cs.ID,
		ChangesetHash:  cs.Hash,
		SliceId:        cs.SliceID,
		BaseCommitHash: cs.BaseCommitHash,
		ModifiedFiles:  cs.ModifiedFiles,
		Status:         status,
		Author:         cs.Author,
		Message:        cs.Message,
		CreatedAt:      cs.CreatedAt.Unix(),
		MergedAt:       mergedAt,
	}
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

	rootSlice, err := s.storage.GetRootSlice(ctx)
	if err != nil {
		return nil, status.Error(codes.NotFound, "root slice not found")
	}

	metadata, _ := s.storage.GetSliceMetadata(ctx, rootSlice.ID)

	return &slicev1.GetRootSliceResponse{
		SliceId:    rootSlice.ID,
		CommitHash: metadata.HeadCommitHash,
	}, nil
}

func (s *sliceServiceServer) CreateSliceFromFolder(ctx context.Context, req *slicev1.CreateSliceFromFolderRequest) (*slicev1.CreateSliceFromFolderResponse, error) {
	log.Printf("CreateSliceFromFolder called: parent_slice_id=%s, folder_path=%s, folder_paths=%d, new_slice_id=%s",
		req.ParentSliceId, req.FolderPath, len(req.FolderPaths), req.NewSliceId)

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
	if !authz.HasSliceViewAccess(parentSlice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for parent slice")
	}

	selectedFiles := make([]string, 0)
	if len(folderPaths) > 0 {
		selectedSet := make(map[string]struct{})
		for _, folderPath := range folderPaths {
			prefix := folderPath + "/"
			for _, rawFileID := range parentSlice.Files {
				fileID := common.CleanRelativePath(rawFileID)
				if fileID == folderPath || strings.HasPrefix(fileID, prefix) {
					selectedSet[fileID] = struct{}{}
				}
			}
		}
		for fileID := range selectedSet {
			selectedFiles = append(selectedFiles, fileID)
		}
		sort.Strings(selectedFiles)
	}

	newSlice := &models.Slice{
		ID:           sliceID,
		Name:         sliceName,
		Description:  req.Description,
		Files:        selectedFiles,
		FolderMounts: buildSliceFolderMounts(folderPaths),
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

	return &slicev1.CreateSliceFromFolderResponse{
		SliceId: sliceID,
		Status:  "created",
		Files:   selectedFiles,
		Name:    sliceName,
		Slug:    newSlice.Slug,
	}, nil
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
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	if err := s.storage.UpdateSliceName(ctx, req.SliceId, req.NewName); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to rename slice: %v", err))
	}

	return &slicev1.RenameSliceResponse{
		SliceId: req.SliceId,
		Name:    req.NewName,
		Slug:    slice.Slug,
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
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	return &slicev1.GetSliceByNameResponse{
		SliceId:       slice.ID,
		Name:          slice.Name,
		Description:   slice.Description,
		ParentSliceId: slice.ParentSlice,
		Files:         slice.Files,
		Environment:   slice.Environment,
		Slug:          slice.Slug,
	}, nil
}

func (s *sliceServiceServer) GetSliceBySlug(ctx context.Context, req *slicev1.GetSliceBySlugRequest) (*slicev1.GetSliceBySlugResponse, error) {
	log.Printf("GetSliceBySlug called: slug=%s", req.Slug)

	if strings.TrimSpace(req.Slug) == "" {
		return nil, status.Error(codes.InvalidArgument, "slug cannot be empty")
	}

	slice, err := s.storage.GetSliceBySlug(ctx, strings.TrimSpace(req.Slug))
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found with slug: %s", req.Slug))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to look up slice: %v", err))
	}

	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	return &slicev1.GetSliceBySlugResponse{
		SliceId:       slice.ID,
		Name:          slice.Name,
		Description:   slice.Description,
		ParentSliceId: slice.ParentSlice,
		Files:         slice.Files,
		Environment:   slice.Environment,
		Slug:          slice.Slug,
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
	paths := make([]string, 0, len(req.GetFolderPaths())+1)
	if raw := strings.TrimSpace(req.GetFolderPath()); raw != "" {
		paths = append(paths, raw)
	}
	paths = append(paths, req.GetFolderPaths()...)

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
	return normalized, nil
}

func buildSliceFolderMounts(folderPaths []string) []models.SliceFolderMount {
	if len(folderPaths) == 0 {
		return nil
	}

	mounts := make([]models.SliceFolderMount, 0, len(folderPaths))
	usedAliases := make(map[string]struct{}, len(folderPaths))
	for _, sourcePath := range folderPaths {
		alias := sourcePath
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

func (s *sliceServiceServer) enqueueRootPromotion(ctx context.Context, sliceID, commitHash string, files []string, commitTime time.Time) error {
	return s.rootPromotionQueue().Enqueue(ctx, rootpromote.Job{
		SliceID:    sliceID,
		CommitHash: commitHash,
		Files:      append([]string(nil), files...),
		CommitTime: commitTime,
	})
}

func (s *sliceServiceServer) promoteSlice(ctx context.Context, sliceID, commitHash string, files []string, commitTime time.Time) error {
	return s.promoteSliceBatch(ctx, []rootpromote.Job{{
		SliceID:    sliceID,
		CommitHash: commitHash,
		Files:      files,
		CommitTime: commitTime,
	}})
}

func (s *sliceServiceServer) promoteSliceBatch(ctx context.Context, batch []rootpromote.Job) error {
	if len(batch) == 0 {
		return nil
	}

	return rootpromote.WithGlobalLock(func() error {
		rootSliceID, err := s.getRootSliceID(ctx)
		if err != nil {
			return err
		}

		rootMetadata, err := s.storage.GetSliceMetadata(ctx, rootSliceID)
		if err != nil {
			return fmt.Errorf("failed to load root metadata: %w", err)
		}

		for _, job := range latestHomeSlicePromotionJobs(batch) {
			if _, err := homeslice.SyncHomeSliceToRoot(ctx, s.storage, job.SliceID); err != nil {
				return fmt.Errorf("failed to sync %s into root: %w", job.SliceID, err)
			}
		}

		files := collectUniquePromotionFiles(batch)
		for _, fileID := range files {
			if err := s.storage.AddFileToSlice(ctx, fileID, rootSliceID); err != nil {
				return fmt.Errorf("failed to add file to root slice: %w", err)
			}
		}

		state, err := s.storage.GetGlobalState(ctx)
		if err != nil && !errors.Is(err, storage.ErrInvalidInput) {
			return fmt.Errorf("failed to load global state: %w", err)
		}
		if state == nil {
			state = &models.GlobalState{}
		}

		history := make([]*models.GlobalCommit, 0, len(batch))
		for i := len(batch) - 1; i >= 0; i-- {
			job := batch[i]
			history = append(history, &models.GlobalCommit{
				CommitHash:     job.CommitHash,
				Timestamp:      job.CommitTime,
				MergedSliceIDs: []string{job.SliceID},
			})
		}
		latest := batch[len(batch)-1]
		state.GlobalCommitHash = latest.CommitHash
		state.Timestamp = latest.CommitTime
		state.History = append(history, state.History...)

		if err := s.storage.UpdateGlobalState(ctx, state); err != nil {
			return fmt.Errorf("failed to update global state: %w", err)
		}

		rootMetadata.HeadCommitHash = state.GlobalCommitHash
		latestFiles := normalizeModifiedFiles(latest.Files)
		rootMetadata.ModifiedFiles = latestFiles
		rootMetadata.ModifiedFilesCount = len(latestFiles)
		rootMetadata.LastModified = state.Timestamp
		if err := s.storage.UpdateSliceMetadata(ctx, rootSliceID, rootMetadata); err != nil {
			return fmt.Errorf("failed to update root metadata: %w", err)
		}

		return nil
	})
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

func (s *sliceServiceServer) waitForQueuedPromotions(ctx context.Context) error {
	return s.rootPromotionQueue().Wait(ctx)
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
	s.promotionQueue = rootpromote.New(s.promotionBatchWindow, s.promotionBatchMaxSize, func(ctx context.Context, batch []rootpromote.Job) error {
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

	rootSlice, err := s.storage.GetRootSlice(ctx)
	if err != nil {
		if !errors.Is(err, storage.ErrSliceNotFound) {
			return "", fmt.Errorf("failed to load root slice: %w", err)
		}
		if err := s.storage.InitializeRootSlice(ctx); err != nil {
			return "", fmt.Errorf("failed to initialize root slice: %w", err)
		}
		rootSlice, err = s.storage.GetRootSlice(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to load root slice after initialization: %w", err)
		}
		log.Println("Root slice initialized successfully")
	}

	s.rootSliceID = rootSlice.ID
	return s.rootSliceID, nil
}

// createCommitSnapshot creates a snapshot of the current file state for a commit.
func (s *sliceServiceServer) createCommitSnapshot(ctx context.Context, sliceID, commitHash, parentHash string, modifiedFiles []string, timestamp time.Time) error {
	files := make(map[string]string)

	if strings.TrimSpace(parentHash) != "" {
		parentSnapshot, err := s.storage.GetCommitSnapshot(ctx, strings.TrimSpace(parentHash))
		if err == nil && parentSnapshot != nil {
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
	if len(files) == 0 {
		slice, err := s.storage.GetSlice(ctx, sliceID)
		if err != nil {
			return fmt.Errorf("failed to load slice for snapshot: %w", err)
		}
		for _, rawPath := range normalizeModifiedFiles(slice.Files) {
			filePath := cleanDiffPath(rawPath)
			if filePath == "" {
				continue
			}
			manifest, err := s.storage.GetFileManifest(ctx, sliceID, filePath)
			if err != nil {
				continue
			}
			hash := strings.TrimSpace(manifest.Hash)
			if hash == "" {
				continue
			}
			files[filePath] = hash
		}
	}

	for _, rawPath := range normalizeModifiedFiles(modifiedFiles) {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}

		manifest, err := s.storage.GetFileManifest(ctx, sliceID, filePath)
		if err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				delete(files, filePath)
				continue
			}
			return fmt.Errorf("failed to load file %s for snapshot: %w", filePath, err)
		}

		hash := strings.TrimSpace(manifest.Hash)
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

	return s.storage.SaveCommitSnapshot(ctx, snapshot)
}

// recordFileChanges creates FileChangeRecord entries for each file in the changeset.
func (s *sliceServiceServer) recordFileChanges(ctx context.Context, cs *models.Changeset, commitHash string, parentHash string, timestamp time.Time) error {
	previousHashes := make(map[string]string)
	if strings.TrimSpace(parentHash) != "" {
		snapshot, err := s.storage.GetCommitSnapshot(ctx, strings.TrimSpace(parentHash))
		if err == nil && snapshot != nil {
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

	var changes []*models.FileChangeRecord
	for _, rawPath := range normalizeModifiedFiles(cs.ModifiedFiles) {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}

		oldHash := strings.TrimSpace(previousHashes[filePath])
		newHash := ""
		linesAdded := 0
		linesDeleted := 0
		hasCurrentEntry := false
		if oldHash == "" {
			oldHash = s.findPreviousKnownFileHash(ctx, cs.SliceID, filePath, "")
		}

		if entry, entryErr := s.storage.GetEntryByPath(ctx, cs.SliceID, filePath); entryErr == nil && entry != nil {
			hasCurrentEntry = true
		} else if entryErr != nil && !errors.Is(entryErr, storage.ErrEntryNotFound) {
			return fmt.Errorf("failed to load file entry %s for change history: %w", filePath, entryErr)
		}

		manifest, err := s.storage.GetFileManifest(ctx, cs.SliceID, filePath)
		if err == nil && manifest != nil {
			newHash = strings.TrimSpace(manifest.Hash)
		} else if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
			return fmt.Errorf("failed to load file %s for change history: %w", filePath, err)
		}

		changeType := models.ChangeTypeModify
		switch {
		case oldHash == "" && (newHash != "" || hasCurrentEntry):
			changeType = models.ChangeTypeAdd
			fileContent, readErr := storage.ReadSliceFileContent(ctx, s.storage, cs.SliceID, filePath)
			if readErr == nil && fileContent != nil && len(fileContent.Content) > 0 {
				linesAdded = countTextLines(fileContent.Content)
			}
		case oldHash != "" && newHash == "" && !hasCurrentEntry:
			changeType = models.ChangeTypeDelete
			if previousContent, hashErr := storage.ReadVersionedFileContent(ctx, s.storage, oldHash); hashErr == nil && previousContent != nil {
				linesDeleted = countTextLines(previousContent.Content)
			}
		case oldHash != "" && (newHash != "" || hasCurrentEntry):
			changeType = models.ChangeTypeModify
		}

		changes = append(changes, &models.FileChangeRecord{
			ID:           fmt.Sprintf("%s-%s", commitHash, filePath),
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
	return s.storage.AddFileChanges(ctx, changes)
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
