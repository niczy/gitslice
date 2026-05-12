package storage

import (
	"bytes"
	"context"
	"sort"
	"testing"

	"github.com/niczy/gitslice/internal/models"
)

func TestChunkFileRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		content   []byte
		blockSize int
	}{
		{name: "empty", content: nil, blockSize: 16},
		{name: "smaller-than-block", content: []byte("hello world"), blockSize: 16},
		{name: "multiple-blocks", content: bytes.Repeat([]byte("abcdef"), 20), blockSize: 17},
		{name: "repeated-blocks-dedup", content: bytes.Repeat([]byte("xyz12345"), 8), blockSize: 8},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocks, payloads := ChunkFile(tc.content, tc.blockSize)
			manifest := &models.FileManifest{
				Path:      "notes/test.txt",
				TotalSize: int64(len(tc.content)),
				Hash:      hashBlock(tc.content),
				Blocks:    blocks,
			}
			assembled, err := AssembleFile(manifest, func(hash string) ([]byte, error) {
				payload, ok := payloads[hash]
				if !ok {
					return nil, ErrEntryNotFound
				}
				return append([]byte(nil), payload...), nil
			})
			if err != nil {
				t.Fatalf("AssembleFile failed: %v", err)
			}
			if !bytes.Equal(assembled, tc.content) {
				t.Fatalf("assembled content mismatch: got=%q want=%q", string(assembled), string(tc.content))
			}
		})
	}
}

func TestReadManifestContentUsesBatchGetBlocks(t *testing.T) {
	ctx := context.Background()
	st := NewInMemoryStorage()

	content := bytes.Repeat([]byte("abc"), 4)
	blocks, payloads := ChunkFile(content, 3)
	if err := st.PutBlocks(ctx, payloads); err != nil {
		t.Fatalf("PutBlocks failed: %v", err)
	}

	counting := &countingBlockReadStorage{Storage: st}
	manifest := &models.FileManifest{
		Path:      "duplicate-blocks.txt",
		TotalSize: int64(len(content)),
		Hash:      hashBlock(content),
		Blocks:    blocks,
	}
	file, err := ReadManifestContent(ctx, counting, manifest)
	if err != nil {
		t.Fatalf("ReadManifestContent failed: %v", err)
	}
	if !bytes.Equal(file.Content, content) {
		t.Fatalf("content mismatch: got=%q want=%q", string(file.Content), string(content))
	}
	if counting.getBlocksCalls != 1 {
		t.Fatalf("GetBlocks calls = %d, want 1", counting.getBlocksCalls)
	}
	if counting.getBlockCalls != 0 {
		t.Fatalf("GetBlock calls = %d, want 0", counting.getBlockCalls)
	}
	if counting.lastBatchLen != 1 {
		t.Fatalf("GetBlocks batch len = %d, want one unique block", counting.lastBatchLen)
	}
}

type countingBlockReadStorage struct {
	Storage

	getBlockCalls  int
	getBlocksCalls int
	lastBatchLen   int
}

func (s *countingBlockReadStorage) GetBlock(ctx context.Context, hash string) ([]byte, error) {
	s.getBlockCalls++
	return s.Storage.GetBlock(ctx, hash)
}

func (s *countingBlockReadStorage) GetBlocks(ctx context.Context, hashes []string) (map[string][]byte, error) {
	s.getBlocksCalls++
	s.lastBatchLen = len(hashes)
	return s.Storage.GetBlocks(ctx, hashes)
}

func TestFindBlocksForRange(t *testing.T) {
	t.Parallel()

	manifest := &models.FileManifest{
		Path:      "big.txt",
		TotalSize: 40,
		Blocks: []models.Block{
			{Hash: "a", Size: 10},
			{Hash: "b", Size: 10},
			{Hash: "c", Size: 10},
			{Hash: "d", Size: 10},
		},
	}

	cases := []struct {
		name   string
		offset int64
		length int64
		want   []int
	}{
		{name: "start of first block", offset: 0, length: 5, want: []int{0}},
		{name: "spans two blocks", offset: 8, length: 5, want: []int{0, 1}},
		{name: "middle blocks", offset: 12, length: 15, want: []int{1, 2}},
		{name: "tail clipped", offset: 35, length: 20, want: []int{3}},
		{name: "empty length", offset: 5, length: 0, want: nil},
		{name: "offset past end", offset: 50, length: 1, want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FindBlocksForRange(manifest, tc.offset, tc.length)
			if len(got) != len(tc.want) {
				t.Fatalf("FindBlocksForRange length mismatch: got=%v want=%v", got, tc.want)
			}
			for idx := range got {
				if got[idx] != tc.want[idx] {
					t.Fatalf("FindBlocksForRange mismatch: got=%v want=%v", got, tc.want)
				}
			}
		})
	}
}

func TestStorageBlockInterfaceSemantics(t *testing.T) {
	ctx := context.Background()

	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.factory(t)

			if _, err := st.GetBlock(ctx, ""); err != ErrInvalidInput {
				t.Fatalf("GetBlock(empty) = %v, want %v", err, ErrInvalidInput)
			}
			if _, err := st.HasBlock(ctx, ""); err != ErrInvalidInput {
				t.Fatalf("HasBlock(empty) = %v, want %v", err, ErrInvalidInput)
			}
			if err := st.PutBlock(ctx, "", []byte("bad")); err != ErrInvalidInput {
				t.Fatalf("PutBlock(empty) = %v, want %v", err, ErrInvalidInput)
			}
			if _, err := st.GetBlocks(ctx, []string{"missing"}); err != ErrEntryNotFound {
				t.Fatalf("GetBlocks(missing) = %v, want %v", err, ErrEntryNotFound)
			}
			if _, err := st.GetBlocks(ctx, []string{""}); err != ErrInvalidInput {
				t.Fatalf("GetBlocks(empty hash) = %v, want %v", err, ErrInvalidInput)
			}

			blockBody := []byte("immutable block")
			if err := st.PutBlock(ctx, "hash-a", blockBody); err != nil {
				t.Fatalf("PutBlock failed: %v", err)
			}
			blockBody[0] = 'X'
			got, err := st.GetBlock(ctx, "hash-a")
			if err != nil {
				t.Fatalf("GetBlock failed: %v", err)
			}
			if !bytes.Equal(got, []byte("immutable block")) {
				t.Fatalf("block payload aliases caller buffer: %q", string(got))
			}
			got[0] = 'Y'
			gotAgain, err := st.GetBlock(ctx, "hash-a")
			if err != nil {
				t.Fatalf("GetBlock second read failed: %v", err)
			}
			if !bytes.Equal(gotAgain, []byte("immutable block")) {
				t.Fatalf("block payload aliases read buffer: %q", string(gotAgain))
			}

			exists, err := st.HasBlock(ctx, "hash-a")
			if err != nil {
				t.Fatalf("HasBlock failed: %v", err)
			}
			if !exists {
				t.Fatalf("HasBlock(hash-a) = false, want true")
			}
			exists, err = st.HasBlock(ctx, "hash-missing")
			if err != nil {
				t.Fatalf("HasBlock missing failed: %v", err)
			}
			if exists {
				t.Fatalf("HasBlock(missing) = true, want false")
			}

			blocks := map[string][]byte{
				"hash-b": []byte("block b"),
				"hash-c": []byte("block c"),
			}
			if err := st.PutBlocks(ctx, blocks); err != nil {
				t.Fatalf("PutBlocks failed: %v", err)
			}
			blocks["hash-b"][0] = 'X'
			batched, err := st.GetBlocks(ctx, []string{"hash-a", "hash-b", "hash-a"})
			if err != nil {
				t.Fatalf("GetBlocks failed: %v", err)
			}
			if len(batched) != 2 {
				t.Fatalf("GetBlocks returned %d unique blocks, want 2", len(batched))
			}
			if !bytes.Equal(batched["hash-b"], []byte("block b")) {
				t.Fatalf("PutBlocks payload aliases caller buffer: %q", string(batched["hash-b"]))
			}
			batched["hash-b"][0] = 'Y'
			gotB, err := st.GetBlock(ctx, "hash-b")
			if err != nil {
				t.Fatalf("GetBlock(hash-b) failed: %v", err)
			}
			if !bytes.Equal(gotB, []byte("block b")) {
				t.Fatalf("GetBlocks payload aliases stored buffer: %q", string(gotB))
			}
		})
	}
}

func TestStorageBlockManifestRoundTrip(t *testing.T) {
	ctx := context.Background()

	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.factory(t)

			sliceID := "slice-blocks"
			if err := st.CreateSlice(ctx, &models.Slice{
				ID:        sliceID,
				Name:      "blocks",
				Owners:    []string{"alice"},
				CreatedBy: "alice",
			}); err != nil {
				t.Fatalf("CreateSlice failed: %v", err)
			}

			content := bytes.Repeat([]byte("block-data-"), 32)
			blocks, payloads := ChunkFile(content, 32)
			if err := st.PutBlocks(ctx, payloads); err != nil {
				t.Fatalf("PutBlocks failed: %v", err)
			}
			for hash, body := range payloads {
				has, err := st.HasBlock(ctx, hash)
				if err != nil {
					t.Fatalf("HasBlock failed: %v", err)
				}
				if !has {
					t.Fatalf("expected block %s to exist", hash)
				}
				got, err := st.GetBlock(ctx, hash)
				if err != nil {
					t.Fatalf("GetBlock failed: %v", err)
				}
				if !bytes.Equal(got, body) {
					t.Fatalf("GetBlock mismatch for %s", hash)
				}
			}
			orderedHashes := make([]string, 0, len(payloads))
			for hash := range payloads {
				orderedHashes = append(orderedHashes, hash)
			}
			sort.Strings(orderedHashes)
			batched, err := st.GetBlocks(ctx, orderedHashes)
			if err != nil {
				t.Fatalf("GetBlocks failed: %v", err)
			}
			if got, want := len(batched), len(payloads); got != want {
				t.Fatalf("expected %d batched blocks, got %d", want, got)
			}
			for _, hash := range orderedHashes {
				if !bytes.Equal(batched[hash], payloads[hash]) {
					t.Fatalf("GetBlocks mismatch for %s", hash)
				}
			}

			manifest := &models.FileManifest{
				Path:      "src/main.go",
				TotalSize: int64(len(content)),
				Hash:      hashBlock(content),
				Blocks:    blocks,
			}
			if err := st.PutFileManifest(ctx, sliceID, manifest.Path, manifest); err != nil {
				t.Fatalf("PutFileManifest failed: %v", err)
			}
			if err := st.PutVersionedFileManifest(ctx, manifest); err != nil {
				t.Fatalf("PutVersionedFileManifest failed: %v", err)
			}
			if err := st.AddEntry(ctx, &models.DirectoryEntry{
				ID:       generateEntryID(sliceID, manifest.Path),
				Path:     manifest.Path,
				Type:     "file",
				ParentID: sliceID,
				Size:     manifest.TotalSize,
			}); err != nil {
				t.Fatalf("AddEntry failed: %v", err)
			}
			if err := st.AddFileToSlice(ctx, manifest.Path, sliceID); err != nil {
				t.Fatalf("AddFileToSlice failed: %v", err)
			}
			gotManifest, err := st.GetFileManifest(ctx, sliceID, manifest.Path)
			if err != nil {
				t.Fatalf("GetFileManifest failed: %v", err)
			}
			if gotManifest.Hash != manifest.Hash || gotManifest.TotalSize != manifest.TotalSize || len(gotManifest.Blocks) != len(manifest.Blocks) {
				t.Fatalf("manifest mismatch: got=%#v want=%#v", gotManifest, manifest)
			}

			assembled, err := AssembleFile(gotManifest, func(hash string) ([]byte, error) {
				return st.GetBlock(ctx, hash)
			})
			if err != nil {
				t.Fatalf("AssembleFile via storage failed: %v", err)
			}
			if !bytes.Equal(assembled, content) {
				t.Fatalf("assembled content mismatch")
			}

			versioned, err := st.GetVersionedFileManifest(ctx, manifest.Hash)
			if err != nil {
				t.Fatalf("GetVersionedFileManifest failed: %v", err)
			}
			assembledFromVersioned, err := AssembleFile(versioned, func(hash string) ([]byte, error) {
				return st.GetBlock(ctx, hash)
			})
			if err != nil {
				t.Fatalf("AssembleFile versioned failed: %v", err)
			}
			if !bytes.Equal(assembledFromVersioned, content) {
				t.Fatalf("versioned assembled content mismatch")
			}

			legacyRead, err := ReadSliceFileContent(ctx, st, sliceID, manifest.Path)
			if err != nil {
				t.Fatalf("ReadSliceFileContent failed: %v", err)
			}
			if !bytes.Equal(legacyRead.Content, content) {
				t.Fatalf("legacy path content mismatch")
			}

			entry, err := st.GetEntryByPath(ctx, sliceID, manifest.Path)
			if err != nil {
				t.Fatalf("GetEntryByPath manifest hash failed: %v", err)
			}
			if entry.Hash != manifest.Hash {
				t.Fatalf("entry hash mismatch: got=%q want=%q", entry.Hash, manifest.Hash)
			}

			hashRead, err := ReadVersionedFileContent(ctx, st, manifest.Hash)
			if err != nil {
				t.Fatalf("ReadVersionedFileContent failed: %v", err)
			}
			if !bytes.Equal(hashRead.Content, content) {
				t.Fatalf("hash content mismatch")
			}

			if err := st.DeleteFileManifest(ctx, sliceID, manifest.Path); err != nil {
				t.Fatalf("DeleteFileManifest failed: %v", err)
			}
			if _, err := st.GetFileManifest(ctx, sliceID, manifest.Path); err != ErrEntryNotFound {
				t.Fatalf("expected ErrEntryNotFound after delete, got %v", err)
			}
		})
	}
}
