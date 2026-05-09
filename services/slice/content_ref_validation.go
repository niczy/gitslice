package sliceservice

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *sliceServiceServer) validateChangesetSnapshotContentRefs(ctx context.Context, st storage.Storage, cs *models.Changeset) error {
	if cs == nil {
		return nil
	}
	snapshots, err := st.ListChangesetSnapshots(ctx, cs.ID, 1)
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to load changeset snapshot: %v", err))
	}
	if len(snapshots) == 0 || snapshots[0] == nil || len(snapshots[0].FileHashes) == 0 {
		return nil
	}

	neededManifestHashes := make(map[string]string)
	for _, rawPath := range normalizeModifiedFiles(cs.ModifiedFiles) {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}
		hash := strings.TrimSpace(snapshots[0].FileHashes[filePath])
		if hash == "" {
			continue
		}
		neededManifestHashes[hash] = filePath
	}
	if len(neededManifestHashes) == 0 {
		return nil
	}

	neededBlockHashes := make(map[string]string)
	for manifestHash, filePath := range neededManifestHashes {
		manifest, err := st.GetVersionedFileManifest(ctx, manifestHash)
		if err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				return status.Error(codes.FailedPrecondition, fmt.Sprintf("changeset content reference for %s is missing manifest %s", filePath, shortHash(manifestHash)))
			}
			return status.Error(codes.Internal, fmt.Sprintf("failed to load changeset manifest %s: %v", shortHash(manifestHash), err))
		}
		if manifest == nil || strings.TrimSpace(manifest.Hash) != manifestHash {
			return status.Error(codes.FailedPrecondition, fmt.Sprintf("changeset content reference for %s has invalid manifest %s", filePath, shortHash(manifestHash)))
		}
		for _, block := range manifest.Blocks {
			blockHash := strings.TrimSpace(block.Hash)
			if blockHash == "" {
				return status.Error(codes.FailedPrecondition, fmt.Sprintf("changeset content reference for %s has an empty block hash", filePath))
			}
			neededBlockHashes[blockHash] = filePath
		}
	}
	if len(neededBlockHashes) == 0 {
		return nil
	}

	for blockHash := range neededBlockHashes {
		exists, err := st.HasBlock(ctx, blockHash)
		if err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("failed to load changeset content block %s: %v", shortHash(blockHash), err))
		}
		if !exists {
			return status.Error(codes.FailedPrecondition, fmt.Sprintf("changeset content reference for %s is missing block %s", neededBlockHashes[blockHash], shortHash(blockHash)))
		}
	}
	return nil
}
