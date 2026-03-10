package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/niczy/gitslice/internal/models"
)

const DefaultFileBlockSize = 16 * 1024

func hashBlock(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ChunkFile splits content into fixed-size content-addressed blocks and returns
// the ordered block list plus a deduplicated hash->content map.
func ChunkFile(data []byte, blockSize int) ([]models.Block, map[string][]byte) {
	if blockSize <= 0 {
		blockSize = DefaultFileBlockSize
	}
	if len(data) == 0 {
		return nil, map[string][]byte{}
	}

	blocks := make([]models.Block, 0, (len(data)+blockSize-1)/blockSize)
	payloads := make(map[string][]byte)
	for offset := 0; offset < len(data); offset += blockSize {
		end := offset + blockSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]
		hash := hashBlock(chunk)
		blocks = append(blocks, models.Block{
			Hash: hash,
			Size: len(chunk),
		})
		if _, exists := payloads[hash]; !exists {
			payloads[hash] = append([]byte(nil), chunk...)
		}
	}

	return blocks, payloads
}

// AssembleFile reconstructs a file from its ordered block manifest.
func AssembleFile(manifest *models.FileManifest, getBlock func(hash string) ([]byte, error)) ([]byte, error) {
	if manifest == nil || getBlock == nil {
		return nil, ErrInvalidInput
	}

	assembled := make([]byte, 0, manifest.TotalSize)
	var total int64
	for _, block := range manifest.Blocks {
		if block.Hash == "" {
			return nil, fmt.Errorf("manifest contains empty block hash")
		}
		payload, err := getBlock(block.Hash)
		if err != nil {
			return nil, err
		}
		if block.Size >= 0 && len(payload) != block.Size {
			return nil, fmt.Errorf("block %s size mismatch: manifest=%d actual=%d", block.Hash, block.Size, len(payload))
		}
		assembled = append(assembled, payload...)
		total += int64(len(payload))
	}
	if manifest.TotalSize != 0 && total != manifest.TotalSize {
		return nil, fmt.Errorf("assembled size mismatch: manifest=%d actual=%d", manifest.TotalSize, total)
	}
	return assembled, nil
}

// FindBlocksForRange returns the block indices overlapping [offset, offset+length).
func FindBlocksForRange(manifest *models.FileManifest, offset, length int64) []int {
	if manifest == nil || offset < 0 || length <= 0 || offset >= manifest.TotalSize {
		return nil
	}

	end := offset + length
	if end > manifest.TotalSize {
		end = manifest.TotalSize
	}

	indices := make([]int, 0, len(manifest.Blocks))
	var cursor int64
	for idx, block := range manifest.Blocks {
		blockStart := cursor
		blockEnd := blockStart + int64(block.Size)
		cursor = blockEnd
		if blockEnd <= offset {
			continue
		}
		if blockStart >= end {
			break
		}
		indices = append(indices, idx)
	}
	return indices
}
