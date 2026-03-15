package storage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
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

// ListSliceFileContents walks the slice entry tree and assembles all file
// contents from their manifests.
func ListSliceFileContents(ctx context.Context, st Storage, sliceID string) ([]*models.FileContent, error) {
	sliceID = strings.TrimSpace(sliceID)
	if sliceID == "" {
		return nil, ErrInvalidInput
	}

	var (
		files []*models.FileContent
		visit func(parentID string) error
	)

	visit = func(parentID string) error {
		entries, err := st.ListEntries(ctx, sliceID, parentID)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Path < entries[j].Path
		})
		for _, entry := range entries {
			if entry.Type == "directory" {
				if err := visit(entry.ID); err != nil {
					return err
				}
				continue
			}
			content, err := ReadSliceFileContent(ctx, st, sliceID, entry.Path)
			if err != nil {
				return err
			}
			files = append(files, content)
		}
		return nil
	}

	if err := visit(sliceID); err != nil {
		return nil, err
	}
	return files, nil
}

// WriteSliceFileManifest chunks content into blocks and persists both the
// slice-local and versioned manifests. Entry/index maintenance remains the
// responsibility of the caller.
func WriteSliceFileManifest(ctx context.Context, st Storage, sliceID, filePath string, content []byte) (*models.FileManifest, error) {
	return WriteSliceFileManifestWithMetadata(ctx, st, sliceID, filePath, content, false, "")
}

func WriteSliceFileManifestWithMetadata(
	ctx context.Context,
	st Storage,
	sliceID, filePath string,
	content []byte,
	executable bool,
	symlinkTarget string,
) (*models.FileManifest, error) {
	path := strings.TrimSpace(filePath)
	manifest := &models.FileManifest{
		Path:          path,
		TotalSize:     int64(len(content)),
		Hash:          HashFileManifestContent(content, executable, symlinkTarget),
		Blocks:        nil,
		Executable:    executable,
		SymlinkTarget: symlinkTarget,
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

func HashFileManifestContent(content []byte, executable bool, symlinkTarget string) string {
	if !executable && symlinkTarget == "" {
		return hashFileContent(content)
	}

	hasher := sha256.New()
	_, _ = hasher.Write([]byte("gitslice-manifest-meta-v1\x00"))
	if executable {
		_, _ = hasher.Write([]byte{1})
	} else {
		_, _ = hasher.Write([]byte{0})
	}
	lengthBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(lengthBuf, uint64(len(symlinkTarget)))
	_, _ = hasher.Write(lengthBuf)
	_, _ = hasher.Write([]byte(symlinkTarget))
	_, _ = hasher.Write(content)
	return hex.EncodeToString(hasher.Sum(nil))
}
