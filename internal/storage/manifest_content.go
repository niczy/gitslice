package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/niczy/gitslice/internal/models"
)

// ReadSliceFileContent assembles the current file content for a slice path from
// the stored manifest and content-addressed blocks.
func ReadSliceFileContent(ctx context.Context, st Storage, sliceID, filePath string) (*models.FileContent, error) {
	manifest, err := st.GetFileManifest(ctx, strings.TrimSpace(sliceID), strings.TrimSpace(filePath))
	if err != nil {
		return nil, err
	}
	return ReadManifestContent(ctx, st, manifest)
}

// ReadVersionedFileContent assembles file content for a versioned file hash.
func ReadVersionedFileContent(ctx context.Context, st Storage, contentHash string) (*models.FileContent, error) {
	manifest, err := st.GetVersionedFileManifest(ctx, strings.TrimSpace(contentHash))
	if err != nil {
		return nil, err
	}
	return ReadManifestContent(ctx, st, manifest)
}

// ReadManifestContent assembles file content directly from a manifest.
func ReadManifestContent(ctx context.Context, st Storage, manifest *models.FileManifest) (*models.FileContent, error) {
	if manifest == nil {
		return nil, ErrInvalidInput
	}
	data, err := AssembleFile(manifest, func(hash string) ([]byte, error) {
		return st.GetBlock(ctx, hash)
	})
	if err != nil {
		return nil, err
	}
	return &models.FileContent{
		FileID:  strings.TrimSpace(manifest.Path),
		Path:    strings.TrimSpace(manifest.Path),
		Content: data,
		Size:    manifest.TotalSize,
		Hash:    strings.TrimSpace(manifest.Hash),
	}, nil
}

// WriteSliceFileManifest chunks content into blocks and persists both the
// slice-local and versioned manifests. Entry/index maintenance remains the
// responsibility of the caller.
func WriteSliceFileManifest(ctx context.Context, st Storage, sliceID, filePath string, content []byte) (*models.FileManifest, error) {
	path := strings.TrimSpace(filePath)
	manifest := &models.FileManifest{
		Path:      path,
		TotalSize: int64(len(content)),
		Hash:      hashFileContent(content),
		Blocks:    nil,
	}
	blocks, payloads := ChunkFile(content, DefaultFileBlockSize)
	manifest.Blocks = blocks

	if len(payloads) > 0 {
		missing := make(map[string][]byte, len(payloads))
		for blockHash, payload := range payloads {
			exists, err := st.HasBlock(ctx, blockHash)
			if err != nil {
				return nil, err
			}
			if !exists {
				missing[blockHash] = payload
			}
		}
		if len(missing) > 0 {
			if err := st.PutBlocks(ctx, missing); err != nil {
				return nil, err
			}
		}
	}

	if err := st.PutFileManifest(ctx, strings.TrimSpace(sliceID), path, manifest); err != nil {
		return nil, err
	}
	if err := st.PutVersionedFileManifest(ctx, manifest); err != nil {
		return nil, err
	}
	return cloneManifest(manifest), nil
}

func hashFileContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
