package sliceservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/searchindex"
	"github.com/niczy/gitslice/internal/storage"
	commonv1 "github.com/niczy/gitslice/proto/common"
	filev1 "github.com/niczy/gitslice/proto/file"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func parseSliceMetricMap(t *testing.T, raw string) map[string]int64 {
	t.Helper()
	if raw == "" {
		return map[string]int64{}
	}
	decoded := map[string]int64{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(%q) failed: %v", raw, err)
	}
	return decoded
}

const statusFilterAll = slicev1.ChangesetStatus(-1)

func mustWriteSliceManifest(tb testing.TB, ctx context.Context, st storage.Storage, sliceID, filePath string, content []byte) string {
	tb.Helper()
	manifest, err := storage.WriteSliceFileManifest(ctx, st, sliceID, filePath, content)
	if err != nil {
		tb.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	return manifest.Hash
}

func mustWriteSliceManifestWithMetadata(tb testing.TB, ctx context.Context, st storage.Storage, sliceID, filePath string, content []byte, executable bool, symlinkTarget string) string {
	tb.Helper()
	manifest, err := storage.WriteSliceFileManifestWithMetadata(ctx, st, sliceID, filePath, content, executable, symlinkTarget)
	if err != nil {
		tb.Fatalf("WriteSliceFileManifestWithMetadata failed: %v", err)
	}
	return manifest.Hash
}

func mustWriteVersionedManifest(tb testing.TB, ctx context.Context, st storage.Storage, filePath, hash string, content []byte) {
	tb.Helper()
	blocks, payloads := storage.ChunkFile(content, storage.DefaultFileBlockSize)
	if len(payloads) > 0 {
		if err := st.PutBlocks(ctx, payloads); err != nil {
			tb.Fatalf("PutBlocks failed: %v", err)
		}
	}
	if err := st.PutVersionedFileManifest(ctx, &models.FileManifest{
		Path:      filePath,
		TotalSize: int64(len(content)),
		Hash:      hash,
		Blocks:    blocks,
	}); err != nil {
		tb.Fatalf("PutVersionedFileManifest failed: %v", err)
	}
}

func mustAssembleCheckoutContent(tb testing.TB, resp *slicev1.CheckoutResponse, meta *slicev1.FileMetadata) []byte {
	tb.Helper()
	if meta == nil {
		tb.Fatal("file metadata is nil")
	}
	for _, file := range resp.GetFiles() {
		if file.GetFileId() == meta.GetFileId() {
			return append([]byte(nil), file.GetContent()...)
		}
	}
	blockPayloads := make(map[string][]byte, len(resp.GetBlocks()))
	for _, block := range resp.GetBlocks() {
		blockPayloads[block.GetHash()] = append([]byte(nil), block.GetContent()...)
	}
	manifest := &models.FileManifest{
		Path:      meta.GetFileId(),
		TotalSize: meta.GetSize(),
		Hash:      meta.GetHash(),
	}
	for _, block := range meta.GetBlocks() {
		manifest.Blocks = append(manifest.Blocks, models.Block{
			Hash: block.GetHash(),
			Size: int(block.GetSize()),
		})
	}
	content, err := storage.AssembleFile(manifest, func(hash string) ([]byte, error) {
		payload, ok := blockPayloads[hash]
		if !ok {
			return nil, storage.ErrEntryNotFound
		}
		return payload, nil
	})
	if err != nil {
		tb.Fatalf("failed to assemble checkout content: %v", err)
	}
	return content
}

type checkoutStreamRecorder struct {
	ctx    context.Context
	chunks []*slicev1.CheckoutChunk
}

func (r *checkoutStreamRecorder) Context() context.Context {
	if r.ctx != nil {
		return r.ctx
	}
	return context.Background()
}

func (r *checkoutStreamRecorder) Send(chunk *slicev1.CheckoutChunk) error {
	r.chunks = append(r.chunks, chunk)
	return nil
}

func (r *checkoutStreamRecorder) SetHeader(metadata.MD) error  { return nil }
func (r *checkoutStreamRecorder) SendHeader(metadata.MD) error { return nil }
func (r *checkoutStreamRecorder) SetTrailer(metadata.MD)       {}
func (r *checkoutStreamRecorder) SendMsg(any) error            { return nil }
func (r *checkoutStreamRecorder) RecvMsg(any) error            { return nil }

var _ slicev1.SliceService_StreamCheckoutSliceServer = (*checkoutStreamRecorder)(nil)
var _ grpc.ServerStream = (*checkoutStreamRecorder)(nil)

type countingCheckoutStorage struct {
	storage.Storage

	mu                        sync.Mutex
	getBlockCalls             int
	getBlocksCalls            int
	getCommitSnapshotCalls    int
	getFileManifestCalls      int
	getVersionedManifestCalls int
}

type blockingLockStorage struct {
	storage.Storage

	firstLockAcquired chan struct{}
	releaseFirstLock  chan struct{}
	lockOnce          sync.Once
}

func newBlockingLockStorage(base storage.Storage) *blockingLockStorage {
	return &blockingLockStorage{
		Storage:           base,
		firstLockAcquired: make(chan struct{}),
		releaseFirstLock:  make(chan struct{}),
	}
}

func (s *blockingLockStorage) LockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) error {
	if err := s.Storage.LockSliceAndFiles(ctx, sliceID, fileIDs); err != nil {
		return err
	}

	shouldBlock := false
	s.lockOnce.Do(func() {
		shouldBlock = true
		close(s.firstLockAcquired)
	})
	if !shouldBlock {
		return nil
	}

	select {
	case <-s.releaseFirstLock:
		return nil
	case <-ctx.Done():
		s.Storage.UnlockSliceAndFiles(context.Background(), sliceID, fileIDs)
		return ctx.Err()
	}
}

func (s *countingCheckoutStorage) GetBlock(ctx context.Context, hash string) ([]byte, error) {
	s.mu.Lock()
	s.getBlockCalls++
	s.mu.Unlock()
	return s.Storage.GetBlock(ctx, hash)
}

func (s *countingCheckoutStorage) GetBlocks(ctx context.Context, hashes []string) (map[string][]byte, error) {
	s.mu.Lock()
	s.getBlocksCalls++
	s.mu.Unlock()
	return s.Storage.GetBlocks(ctx, hashes)
}

func (s *countingCheckoutStorage) GetCommitSnapshot(ctx context.Context, commitHash string) (*models.CommitSnapshot, error) {
	s.mu.Lock()
	s.getCommitSnapshotCalls++
	s.mu.Unlock()
	return s.Storage.GetCommitSnapshot(ctx, commitHash)
}

func (s *countingCheckoutStorage) GetFileManifest(ctx context.Context, sliceID, path string) (*models.FileManifest, error) {
	s.mu.Lock()
	s.getFileManifestCalls++
	s.mu.Unlock()
	return s.Storage.GetFileManifest(ctx, sliceID, path)
}

func (s *countingCheckoutStorage) GetVersionedFileManifest(ctx context.Context, hash string) (*models.FileManifest, error) {
	s.mu.Lock()
	s.getVersionedManifestCalls++
	s.mu.Unlock()
	return s.Storage.GetVersionedFileManifest(ctx, hash)
}

func collectCheckoutStreamResponse(tb testing.TB, recorder *checkoutStreamRecorder) *slicev1.CheckoutResponse {
	tb.Helper()
	resp := &slicev1.CheckoutResponse{
		Manifest: &slicev1.SliceManifest{},
	}
	for _, chunk := range recorder.chunks {
		switch payload := chunk.GetChunk().(type) {
		case *slicev1.CheckoutChunk_Manifest:
			if payload.Manifest == nil {
				continue
			}
			if resp.Manifest.CommitHash == "" {
				resp.Manifest.CommitHash = payload.Manifest.GetCommitHash()
			}
			resp.Manifest.FileMetadata = append(resp.Manifest.FileMetadata, payload.Manifest.GetFileMetadata()...)
		case *slicev1.CheckoutChunk_File:
			resp.Files = append(resp.Files, payload.File)
		case *slicev1.CheckoutChunk_Block:
			resp.Blocks = append(resp.Blocks, payload.Block)
		}
	}
	return resp
}

func TestCheckoutRootSliceAllowsAnonymousAccess(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	const path = "o/genesis/projects/repo/main.go"
	if err := st.AddFileToSlice(ctx, path, "root_slice"); err != nil {
		t.Fatalf("failed to add root file: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{SliceId: "root_slice"})
	if err != nil {
		t.Fatalf("CheckoutSlice returned error: %v", err)
	}
	if len(resp.GetManifest().GetFileMetadata()) != 1 || resp.GetManifest().GetFileMetadata()[0].GetFileId() != path {
		t.Fatalf("unexpected manifest: %#v", resp.GetManifest().GetFileMetadata())
	}
}

func TestGetSliceVisibilityDefaultsPrivate(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	slice := &models.Slice{ID: "slice-visibility", Name: "slice-visibility", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := newSliceServiceServer(st)
	resp, err := svc.GetSliceVisibility(ctx, &slicev1.GetSliceVisibilityRequest{SliceId: slice.ID})
	if err != nil {
		t.Fatalf("GetSliceVisibility failed: %v", err)
	}
	if got, want := resp.GetVisibility(), commonv1.Visibility_VISIBILITY_PRIVATE; got != want {
		t.Fatalf("visibility = %v, want %v", got, want)
	}
}

func TestSetSliceVisibilityUpdatesSlice(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	slice := &models.Slice{ID: "slice-visibility", Name: "slice-visibility", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	svc := newSliceServiceServer(st)

	resp, err := svc.SetSliceVisibility(ctx, &slicev1.SetSliceVisibilityRequest{
		SliceId:    "slice-visibility",
		Visibility: commonv1.Visibility_VISIBILITY_PUBLIC,
	})
	if err != nil {
		t.Fatalf("SetSliceVisibility failed: %v", err)
	}
	if got, want := resp.GetVisibility(), commonv1.Visibility_VISIBILITY_PUBLIC; got != want {
		t.Fatalf("response visibility = %v, want %v", got, want)
	}
	stored, err := st.GetSlice(ctx, "slice-visibility")
	if err != nil {
		t.Fatalf("GetSlice failed: %v", err)
	}
	if got, want := stored.Visibility, models.VisibilityPublic; got != want {
		t.Fatalf("stored visibility = %q, want %q", got, want)
	}
}

func TestSetSliceVisibilityPropagatesPublicPaths(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	slice := &models.Slice{
		ID:        "slice-visibility",
		Name:      "slice-visibility",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{"docs/guide.md"},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{ID: "dir-docs", Path: "docs", Type: "directory", ParentID: slice.ID}); err != nil {
		t.Fatalf("AddEntry(dir) failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{ID: "file-guide", Path: "docs/guide.md", Type: "file", ParentID: slice.ID}); err != nil {
		t.Fatalf("AddEntry(file) failed: %v", err)
	}

	svc := newSliceServiceServer(st)
	resp, err := svc.SetSliceVisibility(ctx, &slicev1.SetSliceVisibilityRequest{
		SliceId:             slice.ID,
		Visibility:          commonv1.Visibility_VISIBILITY_PUBLIC,
		PathPropagationMode: commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_PUBLIC,
	})
	if err != nil {
		t.Fatalf("SetSliceVisibility failed: %v", err)
	}
	if got, want := resp.GetPathPropagationMode(), commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_PUBLIC; got != want {
		t.Fatalf("path_propagation_mode = %v, want %v", got, want)
	}

	dirRule, err := st.GetPathVisibilityRule(ctx, "/docs")
	if err != nil {
		t.Fatalf("GetPathVisibilityRule(dir) failed: %v", err)
	}
	if got, want := dirRule.Visibility, models.VisibilityPublic; got != want {
		t.Fatalf("dir visibility = %q, want %q", got, want)
	}

	fileRule, err := st.GetPathVisibilityRule(ctx, "/docs/guide.md")
	if err != nil {
		t.Fatalf("GetPathVisibilityRule(file) failed: %v", err)
	}
	if got, want := fileRule.Visibility, models.VisibilityPublic; got != want {
		t.Fatalf("file visibility = %q, want %q", got, want)
	}
}

func TestSetSliceVisibilityPropagatesPrivatePaths(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	slice := &models.Slice{
		ID:        "slice-visibility-private",
		Name:      "slice-visibility-private",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{"docs/guide.md"},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{ID: "dir-docs", Path: "docs", Type: "directory", ParentID: slice.ID}); err != nil {
		t.Fatalf("AddEntry(dir) failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{ID: "file-guide", Path: "docs/guide.md", Type: "file", ParentID: slice.ID}); err != nil {
		t.Fatalf("AddEntry(file) failed: %v", err)
	}

	svc := newSliceServiceServer(st)
	if _, err := svc.SetSliceVisibility(ctx, &slicev1.SetSliceVisibilityRequest{
		SliceId:             slice.ID,
		Visibility:          commonv1.Visibility_VISIBILITY_PUBLIC,
		PathPropagationMode: commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_PRIVATE,
	}); err != nil {
		t.Fatalf("SetSliceVisibility failed: %v", err)
	}

	fileRule, err := st.GetPathVisibilityRule(ctx, "/docs/guide.md")
	if err != nil {
		t.Fatalf("GetPathVisibilityRule(file) failed: %v", err)
	}
	if got, want := fileRule.Visibility, models.VisibilityPrivate; got != want {
		t.Fatalf("file visibility = %q, want %q", got, want)
	}
}

func TestSetSliceVisibilityRejectsPropagationOnPrivateSlice(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	slice := &models.Slice{ID: "slice-private-reject", Name: "slice-private-reject", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := newSliceServiceServer(st)
	_, err := svc.SetSliceVisibility(ctx, &slicev1.SetSliceVisibilityRequest{
		SliceId:             slice.ID,
		Visibility:          commonv1.Visibility_VISIBILITY_PRIVATE,
		PathPropagationMode: commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_PUBLIC,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SetSliceVisibility error = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}

func TestCheckoutSliceReturnsOnlyMissingBlocks(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-checkout-blocks", Name: "slice-checkout-blocks", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	filePath := "src/main.go"
	content := append([]byte{}, bytes.Repeat([]byte("A"), storage.DefaultFileBlockSize)...)
	content = append(content, bytes.Repeat([]byte("B"), storage.DefaultFileBlockSize)...)
	content = append(content, bytes.Repeat([]byte("C"), 97)...)

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	manifest, err := storage.WriteSliceFileManifest(ctx, st, slice.ID, filePath, content)
	if err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, filePath, slice.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{
		SliceId:     slice.ID,
		KnownHashes: []string{manifest.Blocks[0].Hash},
	})
	if err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}

	if got, want := len(resp.GetManifest().GetFileMetadata()), 1; got != want {
		t.Fatalf("expected %d file metadata entries, got %d", want, got)
	}
	meta := resp.GetManifest().GetFileMetadata()[0]
	if got, want := len(meta.GetBlocks()), len(manifest.Blocks); got != want {
		t.Fatalf("expected %d manifest blocks, got %d", want, got)
	}
	if len(resp.GetFiles()) != 0 {
		t.Fatalf("expected no fallback file payloads, got %d", len(resp.GetFiles()))
	}
	if got, want := len(resp.GetBlocks()), len(manifest.Blocks)-1; got != want {
		t.Fatalf("expected %d returned blocks, got %d", want, got)
	}
	for _, block := range resp.GetBlocks() {
		if block.GetHash() == manifest.Blocks[0].Hash {
			t.Fatalf("expected known block %s to be omitted", block.GetHash())
		}
	}
}

func TestCheckoutSliceOmitsPayloadWhenFileHashKnown(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-checkout-known-file", Name: "slice-checkout-known-file", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	filePath := "README.md"
	content := []byte("hello from cached checkout")
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	manifest, err := storage.WriteSliceFileManifest(ctx, st, slice.ID, filePath, content)
	if err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, filePath, slice.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{
		SliceId:     slice.ID,
		KnownHashes: []string{manifest.Hash},
	})
	if err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}

	if got, want := len(resp.GetManifest().GetFileMetadata()), 1; got != want {
		t.Fatalf("expected %d file metadata entries, got %d", want, got)
	}
	if len(resp.GetBlocks()) != 0 {
		t.Fatalf("expected no block payloads, got %d", len(resp.GetBlocks()))
	}
	if len(resp.GetFiles()) != 0 {
		t.Fatalf("expected no fallback file payloads, got %d", len(resp.GetFiles()))
	}
}

func TestCheckoutSliceIncludesFileMetadata(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-checkout-metadata", Name: "slice-checkout-metadata", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	scriptPath := "bin/run.sh"
	scriptContent := []byte("#!/bin/sh\necho hi\n")
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:         slice.ID + ":" + scriptPath,
		Path:       scriptPath,
		Type:       "file",
		ParentID:   slice.ID,
		Size:       int64(len(scriptContent)),
		Executable: true,
	}); err != nil {
		t.Fatalf("AddEntry script failed: %v", err)
	}
	mustWriteSliceManifestWithMetadata(t, ctx, st, slice.ID, scriptPath, scriptContent, true, "")
	if err := st.AddFileToSlice(ctx, scriptPath, slice.ID); err != nil {
		t.Fatalf("AddFileToSlice script failed: %v", err)
	}

	linkPath := "bin/current"
	linkTarget := "bin/run.sh"
	linkContent := []byte(linkTarget)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:            slice.ID + ":" + linkPath,
		Path:          linkPath,
		Type:          "file",
		ParentID:      slice.ID,
		Size:          int64(len(linkContent)),
		SymlinkTarget: linkTarget,
	}); err != nil {
		t.Fatalf("AddEntry symlink failed: %v", err)
	}
	mustWriteSliceManifestWithMetadata(t, ctx, st, slice.ID, linkPath, linkContent, false, linkTarget)
	if err := st.AddFileToSlice(ctx, linkPath, slice.ID); err != nil {
		t.Fatalf("AddFileToSlice symlink failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{SliceId: slice.ID})
	if err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}

	byPath := map[string]*slicev1.FileMetadata{}
	for _, meta := range resp.GetManifest().GetFileMetadata() {
		byPath[meta.GetPath()] = meta
	}
	if !byPath[scriptPath].GetExecutable() {
		t.Fatalf("expected executable metadata for %s", scriptPath)
	}
	if got := byPath[linkPath].GetSymlinkTarget(); got != linkTarget {
		t.Fatalf("expected symlink target %q, got %q", linkTarget, got)
	}
	if got := string(mustAssembleCheckoutContent(t, resp, byPath[linkPath])); got != linkTarget {
		t.Fatalf("expected symlink content %q, got %q", linkTarget, got)
	}
}

func TestGetSliceSearchArtifactBuildsMissingArtifactForHeadCommit(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-search-artifact", Name: "slice-search-artifact", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	filePath := "docs/readme.md"
	content := []byte("alpha beta gamma\n")
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	manifestHash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, content)
	if err := st.AddFileToSlice(ctx, filePath, slice.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	commitHash := "commit-search-artifact-head"
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    slice.ID,
		Files: map[string]string{
			filePath: manifestHash,
		},
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, slice.ID, &models.Commit{
		CommitHash: commitHash,
		Message:    "seed",
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}
	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	meta.HeadCommitHash = commitHash
	if err := st.UpdateSliceMetadata(ctx, slice.ID, meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.GetSliceSearchArtifact(ctx, &slicev1.GetSliceSearchArtifactRequest{
		SliceId: slice.ID,
	})
	if err != nil {
		t.Fatalf("GetSliceSearchArtifact failed: %v", err)
	}
	if resp.GetCommitHash() != commitHash {
		t.Fatalf("expected commit hash %q, got %q", commitHash, resp.GetCommitHash())
	}
	if resp.GetVersion() != searchindex.CurrentArtifactVersion {
		t.Fatalf("expected version %d, got %d", searchindex.CurrentArtifactVersion, resp.GetVersion())
	}
	artifact, err := searchindex.DecodeSliceArtifact(resp.GetArtifact())
	if err != nil {
		t.Fatalf("DecodeSliceArtifact failed: %v", err)
	}
	if got := len(artifact.Files); got != 1 || artifact.Files[0].Path != filePath {
		t.Fatalf("unexpected artifact files: %#v", artifact.Files)
	}
	if _, err := st.GetSliceSearchArtifact(ctx, slice.ID, commitHash, searchindex.CurrentArtifactVersion); err != nil {
		t.Fatalf("expected built artifact to be stored, got %v", err)
	}
}

func TestGetSliceSearchArtifactReturnsStoredExplicitCommitArtifact(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-search-artifact-explicit", Name: "slice-search-artifact-explicit", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	commitHash := "commit-search-artifact-explicit"
	encoded, err := searchindex.EncodeSliceArtifact(&searchindex.SliceArtifact{
		Version:    searchindex.CurrentArtifactVersion,
		SliceID:    slice.ID,
		CommitHash: commitHash,
		Files: []searchindex.SliceArtifactFile{
			{Path: "docs/readme.md", SearchContentHash: "hash-1"},
		},
	})
	if err != nil {
		t.Fatalf("EncodeSliceArtifact failed: %v", err)
	}
	if err := st.PutSliceSearchArtifact(ctx, slice.ID, commitHash, searchindex.CurrentArtifactVersion, encoded); err != nil {
		t.Fatalf("PutSliceSearchArtifact failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, slice.ID, &models.Commit{
		CommitHash: commitHash,
		Message:    "seed",
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}
	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	meta.HeadCommitHash = commitHash
	if err := st.UpdateSliceMetadata(ctx, slice.ID, meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.GetSliceSearchArtifact(ctx, &slicev1.GetSliceSearchArtifactRequest{
		SliceId:    slice.ID,
		CommitHash: commitHash,
	})
	if err != nil {
		t.Fatalf("GetSliceSearchArtifact failed: %v", err)
	}
	if !bytes.Equal(resp.GetArtifact(), encoded) {
		t.Fatalf("expected stored artifact payload to be returned")
	}
}

func TestGetSliceSearchArtifactRepairsCorruptStoredArtifact(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-search-artifact-repair", Name: "slice-search-artifact-repair", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	commitHash := "commit-search-artifact-repair"
	filePath := "docs/readme.md"
	manifestHash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, []byte("repair me\n"))
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    slice.ID,
		Files: map[string]string{
			filePath: manifestHash,
		},
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, slice.ID, &models.Commit{
		CommitHash: commitHash,
		Message:    "seed",
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}
	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	meta.HeadCommitHash = commitHash
	if err := st.UpdateSliceMetadata(ctx, slice.ID, meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}
	if err := st.PutSliceSearchArtifact(ctx, slice.ID, commitHash, searchindex.CurrentArtifactVersion, []byte("corrupt")); err != nil {
		t.Fatalf("PutSliceSearchArtifact(corrupt) failed: %v", err)
	}

	before := snapshotSliceSearchMetrics()
	srv := newSliceServiceServer(st)
	resp, err := srv.GetSliceSearchArtifact(ctx, &slicev1.GetSliceSearchArtifactRequest{SliceId: slice.ID})
	if err != nil {
		t.Fatalf("GetSliceSearchArtifact failed: %v", err)
	}
	artifact, err := searchindex.DecodeSliceArtifact(resp.GetArtifact())
	if err != nil {
		t.Fatalf("DecodeSliceArtifact failed: %v", err)
	}
	if got := len(artifact.Files); got != 1 || artifact.Files[0].Path != filePath {
		t.Fatalf("unexpected repaired artifact files: %#v", artifact.Files)
	}
	after := snapshotSliceSearchMetrics()
	beforeCounts := parseSliceMetricMap(t, before["slice_search_artifact_download_count"])
	afterCounts := parseSliceMetricMap(t, after["slice_search_artifact_download_count"])
	if afterCounts["rebuilt"] <= beforeCounts["rebuilt"] {
		t.Fatalf("expected rebuilt download metric to increase, before=%v after=%v", beforeCounts, afterCounts)
	}
}

func TestGetSliceSearchArtifactRejectsUnsupportedVersion(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	slice := &models.Slice{ID: "slice-search-artifact-version", Name: "slice-search-artifact-version", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	_, err := srv.GetSliceSearchArtifact(ctx, &slicev1.GetSliceSearchArtifactRequest{
		SliceId: slice.ID,
		Version: searchindex.CurrentArtifactVersion + 1,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestGetSliceSearchArtifactRequiresAuthForPublicSlice(t *testing.T) {
	st := storage.NewInMemoryStorage()
	slice := &models.Slice{
		ID:         "slice-search-artifact-public",
		Name:       "slice-search-artifact-public",
		Owners:     []string{"tester"},
		CreatedBy:  "tester",
		Visibility: models.VisibilityPublic,
	}
	if err := st.CreateSlice(context.Background(), slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	_, err := srv.GetSliceSearchArtifact(context.Background(), &slicev1.GetSliceSearchArtifactRequest{
		SliceId: slice.ID,
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anonymous caller, got %v", err)
	}

	otherCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User other"))
	_, err = srv.GetSliceSearchArtifact(otherCtx, &slicev1.GetSliceSearchArtifactRequest{
		SliceId: slice.ID,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for non-owner on public slice, got %v", err)
	}
}

func TestStreamCheckoutSliceReturnsOnlyMissingBlocks(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()
	st := &countingCheckoutStorage{Storage: base}

	slice := &models.Slice{ID: "slice-stream-checkout-blocks", Name: "slice-stream-checkout-blocks", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	filePath := "src/main.go"
	content := append([]byte{}, bytes.Repeat([]byte("A"), storage.DefaultFileBlockSize)...)
	content = append(content, bytes.Repeat([]byte("B"), storage.DefaultFileBlockSize)...)
	content = append(content, bytes.Repeat([]byte("C"), 97)...)

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	manifest, err := storage.WriteSliceFileManifest(ctx, st, slice.ID, filePath, content)
	if err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, filePath, slice.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	recorder := &checkoutStreamRecorder{ctx: ctx}
	srv := newSliceServiceServer(st)
	if err := srv.StreamCheckoutSlice(&slicev1.CheckoutRequest{
		SliceId:     slice.ID,
		KnownHashes: []string{manifest.Blocks[0].Hash},
	}, recorder); err != nil {
		t.Fatalf("StreamCheckoutSlice failed: %v", err)
	}

	resp := collectCheckoutStreamResponse(t, recorder)
	if got, want := len(resp.GetManifest().GetFileMetadata()), 1; got != want {
		t.Fatalf("expected %d file metadata entries, got %d", want, got)
	}
	meta := resp.GetManifest().GetFileMetadata()[0]
	if got, want := len(meta.GetBlocks()), len(manifest.Blocks); got != want {
		t.Fatalf("expected %d manifest blocks, got %d", want, got)
	}
	if len(resp.GetFiles()) != 0 {
		t.Fatalf("expected no fallback file payloads, got %d", len(resp.GetFiles()))
	}
	if got, want := len(resp.GetBlocks()), len(manifest.Blocks)-1; got != want {
		t.Fatalf("expected %d returned blocks, got %d", want, got)
	}
	for _, block := range resp.GetBlocks() {
		if block.GetHash() == manifest.Blocks[0].Hash {
			t.Fatalf("expected known block %s to be omitted", block.GetHash())
		}
	}
	if got := st.getBlocksCalls; got != 1 {
		t.Fatalf("expected one batched block fetch, got %d", got)
	}
	if got := st.getBlockCalls; got != 0 {
		t.Fatalf("expected no per-block fetches, got %d", got)
	}
}

func TestStreamCheckoutSliceSplitsManifestIntoChunks(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-stream-manifest-chunks", Name: "slice-stream-manifest-chunks", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	const fileCount = checkoutManifestChunkSize + 7
	for i := 0; i < fileCount; i++ {
		filePath := fmt.Sprintf("src/file-%03d.txt", i)
		content := []byte(filePath)
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       slice.ID + ":" + filePath,
			Path:     filePath,
			Type:     "file",
			ParentID: slice.ID,
			Size:     int64(len(content)),
		}); err != nil {
			t.Fatalf("AddEntry failed: %v", err)
		}
		mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, content)
		if err := st.AddFileToSlice(ctx, filePath, slice.ID); err != nil {
			t.Fatalf("AddFileToSlice failed: %v", err)
		}
	}

	recorder := &checkoutStreamRecorder{ctx: ctx}
	srv := newSliceServiceServer(st)
	if err := srv.StreamCheckoutSlice(&slicev1.CheckoutRequest{SliceId: slice.ID}, recorder); err != nil {
		t.Fatalf("StreamCheckoutSlice failed: %v", err)
	}

	manifestChunks := 0
	totalMetadata := 0
	for _, chunk := range recorder.chunks {
		manifest, ok := chunk.GetChunk().(*slicev1.CheckoutChunk_Manifest)
		if !ok || manifest.Manifest == nil {
			continue
		}
		manifestChunks++
		totalMetadata += len(manifest.Manifest.GetFileMetadata())
		if got := len(manifest.Manifest.GetFileMetadata()); got > checkoutManifestChunkSize {
			t.Fatalf("expected at most %d metadata entries per chunk, got %d", checkoutManifestChunkSize, got)
		}
	}
	if manifestChunks < 2 {
		t.Fatalf("expected multiple manifest chunks, got %d", manifestChunks)
	}
	if totalMetadata != fileCount {
		t.Fatalf("expected %d metadata entries across chunks, got %d", fileCount, totalMetadata)
	}
}

func TestCheckoutProfileSummary(t *testing.T) {
	profile := newCheckoutProfile("stream", "slice-123", "HEAD", 42)
	profile.markPrepared(256, 150*time.Millisecond)
	profile.addManifestChunk(128)
	profile.addManifestChunk(128)
	profile.addBlockPayload(1024)
	profile.addBlockPayload(2048)
	profile.addFilePayload(512)
	profile.finish(250 * time.Millisecond)

	summary := profile.summary()
	for _, want := range []string{
		"mode=stream",
		"slice_id=slice-123",
		"requested_commit=HEAD",
		"known_hashes=42",
		"files=256",
		"manifest_chunks=2",
		"block_payloads=2",
		"block_bytes=3072",
		"file_payloads=1",
		"file_payload_bytes=512",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected summary to contain %q, got %q", want, summary)
		}
	}
}

func TestMergeProfileSummary(t *testing.T) {
	profile := newMergeProfile("cs-123", "slice-123", 256)
	profile.markConflictCheck(3, 120*time.Millisecond)
	profile.markRevertApply(15 * time.Millisecond)
	profile.markFinalize(230 * time.Millisecond)
	profile.markPromotion(40 * time.Millisecond)
	profile.markConfig(5 * time.Millisecond)
	profile.finish()

	summary := profile.summary()
	for _, want := range []string{
		"changeset_id=cs-123",
		"slice_id=slice-123",
		"modified_files=256",
		"conflicts=3",
		"conflict_ms=120",
		"revert_ms=15",
		"finalize_ms=230",
		"promotion_ms=40",
		"config_ms=5",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected summary to contain %q, got %q", want, summary)
		}
	}
}

func TestCheckoutSliceLoadsCommitSnapshotOnce(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()
	st := &countingCheckoutStorage{Storage: base}
	srv := newSliceServiceServer(st)

	slice := &models.Slice{ID: "slice-checkout-snapshot", Name: "slice-checkout-snapshot", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	for _, filePath := range []string{"app/a.txt", "app/b.txt", "app/c.txt"} {
		mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, []byte("content:"+filePath))
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       slice.ID + ":" + filePath,
			Path:     filePath,
			Type:     "file",
			ParentID: slice.ID,
			Size:     int64(len("content:" + filePath)),
		}); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", filePath, err)
		}
	}

	if err := st.UpdateSliceMetadata(ctx, slice.ID, &models.SliceMetadata{
		SliceID:        slice.ID,
		HeadCommitHash: "commit-snapshot-once",
		LastModified:   time.Now(),
	}); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: "commit-snapshot-once",
		SliceID:    slice.ID,
		Files: map[string]string{
			"app/a.txt": "hash-a",
			"app/b.txt": "hash-b",
			"app/c.txt": "hash-c",
		},
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}

	if _, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{SliceId: slice.ID}); err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}
	if got := st.getCommitSnapshotCalls; got != 1 {
		t.Fatalf("expected exactly one commit snapshot lookup, got %d", got)
	}
}

func TestCheckoutSliceSkipsManifestFetchWhenFileHashKnown(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()
	st := &countingCheckoutStorage{Storage: base}
	srv := newSliceServiceServer(st)

	slice := &models.Slice{ID: "slice-checkout-known-metadata", Name: "slice-checkout-known-metadata", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	filePath := "pkg/main.go"
	content := []byte("package main\n")
	hash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, content)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(content)),
		Hash:     hash,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	resp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{
		SliceId:     slice.ID,
		KnownHashes: []string{hash},
	})
	if err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}
	if got := len(resp.GetManifest().GetFileMetadata()); got != 1 {
		t.Fatalf("expected 1 metadata entry, got %d", got)
	}
	if got := len(resp.GetManifest().GetFileMetadata()[0].GetBlocks()); got != 0 {
		t.Fatalf("expected no block refs when full file hash is already known, got %d", got)
	}
	if got := st.getFileManifestCalls; got != 0 {
		t.Fatalf("expected no slice manifest lookups, got %d", got)
	}
	if got := st.getVersionedManifestCalls; got != 0 {
		t.Fatalf("expected no versioned manifest lookups, got %d", got)
	}
}

func TestListChangesetsFiltersByStatus(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-1", Name: "slice-1", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	now := time.Now()
	changesets := []*models.Changeset{
		{
			ID:            "cs-pending",
			SliceID:       slice.ID,
			Status:        models.ChangesetStatusPending,
			ModifiedFiles: []string{"file1"},
			CreatedAt:     now,
		},
		{
			ID:            "cs-merged",
			SliceID:       slice.ID,
			Status:        models.ChangesetStatusMerged,
			ModifiedFiles: []string{"file2"},
			CreatedAt:     now.Add(time.Minute),
		},
	}

	for _, cs := range changesets {
		if err := st.CreateChangeset(ctx, cs); err != nil {
			t.Fatalf("failed to seed changeset %s: %v", cs.ID, err)
		}
	}

	srv := newSliceServiceServer(st)

	t.Run("no filter returns all", func(t *testing.T) {
		resp, err := srv.ListChangesets(ctx, &slicev1.ListChangesetsRequest{SliceId: slice.ID, StatusFilter: statusFilterAll})
		if err != nil {
			t.Fatalf("ListChangesets returned error: %v", err)
		}
		if got, want := len(resp.Changesets), len(changesets); got != want {
			t.Fatalf("expected %d changesets, got %d", want, got)
		}
	})

	t.Run("pending filter", func(t *testing.T) {
		resp, err := srv.ListChangesets(ctx, &slicev1.ListChangesetsRequest{SliceId: slice.ID, StatusFilter: slicev1.ChangesetStatus_PENDING})
		if err != nil {
			t.Fatalf("ListChangesets returned error: %v", err)
		}
		if got, want := len(resp.Changesets), 1; got != want {
			t.Fatalf("expected %d pending changeset, got %d", want, got)
		}
		if resp.Changesets[0].ChangesetId != "cs-pending" {
			t.Fatalf("expected cs-pending, got %s", resp.Changesets[0].ChangesetId)
		}
	})

	t.Run("merged filter", func(t *testing.T) {
		resp, err := srv.ListChangesets(ctx, &slicev1.ListChangesetsRequest{SliceId: slice.ID, StatusFilter: slicev1.ChangesetStatus_MERGED})
		if err != nil {
			t.Fatalf("ListChangesets returned error: %v", err)
		}
		if got, want := len(resp.Changesets), 1; got != want {
			t.Fatalf("expected %d merged changeset, got %d", want, got)
		}
		if resp.Changesets[0].ChangesetId != "cs-merged" {
			t.Fatalf("expected cs-merged, got %s", resp.Changesets[0].ChangesetId)
		}
	})
}

func TestCreateSliceAutoGeneratesID(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	if err := st.AddFileToSlice(ctx, "o/genesis/projects/repo/main.go", "root_slice"); err != nil {
		t.Fatalf("failed to add root file: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root_slice",
		FolderPaths:   []string{"o/genesis/projects/repo"},
		Name:          "my-slice",
		Description:   "auto id test",
	})
	if err != nil {
		t.Fatalf("CreateSliceFromFolder failed: %v", err)
	}

	if !strings.HasPrefix(resp.SliceId, "sl-") {
		t.Fatalf("expected auto-generated ID with sl- prefix, got %q", resp.SliceId)
	}
	if resp.Name != "my-slice" {
		t.Fatalf("expected name %q, got %q", "my-slice", resp.Name)
	}
	if resp.Slug != "tester/my-slice" {
		t.Fatalf("expected slug %q, got %q", "tester/my-slice", resp.Slug)
	}

	// Verify we can retrieve it
	slice, err := st.GetSlice(ctx, resp.SliceId)
	if err != nil {
		t.Fatalf("failed to get slice by generated ID: %v", err)
	}
	if slice.Name != "my-slice" {
		t.Fatalf("stored name mismatch: %q", slice.Name)
	}
	if slice.Slug != "my-slice" {
		t.Fatalf("stored slug mismatch: %q", slice.Slug)
	}
}

func TestCreateSliceUsesFolderPathsAsDefaultName(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	if err := st.AddFileToSlice(ctx, "org/project/service/main.go", "root_slice"); err != nil {
		t.Fatalf("failed to add root file: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root_slice",
		FolderPaths:   []string{"org/project/service"},
		Description:   "derive default name from folder",
	})
	if err != nil {
		t.Fatalf("CreateSliceFromFolder failed: %v", err)
	}

	if resp.Name != "org/project/service" {
		t.Fatalf("expected derived name %q, got %q", "org/project/service", resp.Name)
	}

	slice, err := st.GetSlice(ctx, resp.SliceId)
	if err != nil {
		t.Fatalf("failed to get created slice: %v", err)
	}
	if slice.Name != "org/project/service" {
		t.Fatalf("stored name mismatch: got %q", slice.Name)
	}
}

func TestCreateSliceFromFolderRejectsMissingFolder(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	if err := st.AddFileToSlice(ctx, "docs/README.md", "root_slice"); err != nil {
		t.Fatalf("failed to add root file: %v", err)
	}

	srv := newSliceServiceServer(st)
	_, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root_slice",
		NewSliceId:    "missing-folder-slice",
		FolderPaths:   []string{"does-not-exist"},
		Name:          "missing-folder",
		Description:   "missing folder test",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for missing folder, got %v", err)
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing folder message, got %v", err)
	}
	if _, getErr := st.GetSlice(ctx, "missing-folder-slice"); !errors.Is(getErr, storage.ErrSliceNotFound) {
		t.Fatalf("missing folder slice should not be created, got %v", getErr)
	}
}

func TestCreateSliceFromFolderUsesParentEntriesWhenSliceFilesEmpty(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	filePath := "o/genesis/projects/repo/README.md"
	content := []byte("repo readme")
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       "root_slice:" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: "root_slice",
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, "root_slice", filePath, content)

	rootSlice, err := st.GetSlice(ctx, "root_slice")
	if err != nil {
		t.Fatalf("failed to load root slice: %v", err)
	}
	if got := len(rootSlice.Files); got != 0 {
		t.Fatalf("expected prod-like empty root file index, got %d entries", got)
	}

	srv := NewService(st)
	createResp, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root_slice",
		NewSliceId:    "entry-backed-slice",
		Name:          "entry-backed-slice",
		FolderPaths:   []string{"o/genesis/projects/repo"},
		Description:   "entry-backed root slice test",
	})
	if err != nil {
		t.Fatalf("CreateSliceFromFolder failed: %v", err)
	}
	if got, want := createResp.GetFiles(), []string{filePath}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected selected files %v, got %v", want, got)
	}

	checkoutResp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{
		SliceId:    createResp.GetSliceId(),
		CommitHash: "HEAD",
	})
	if err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}
	if got := len(checkoutResp.GetManifest().GetFileMetadata()); got != 1 {
		t.Fatalf("expected 1 checkout file, got %d", got)
	}
	meta := checkoutResp.GetManifest().GetFileMetadata()[0]
	if got, want := meta.GetPath(), filePath; got != want {
		t.Fatalf("expected checkout path %q, got %q", want, got)
	}
	if got, want := meta.GetSize(), int64(len(content)); got != want {
		t.Fatalf("expected checkout size %d, got %d", want, got)
	}
	if got := string(mustAssembleCheckoutContent(t, checkoutResp, meta)); got != string(content) {
		t.Fatalf("expected checkout content %q, got %q", string(content), got)
	}

	childEntry, err := st.GetEntryByPath(ctx, createResp.GetSliceId(), filePath)
	if err != nil {
		t.Fatalf("GetEntryByPath child failed: %v", err)
	}
	if got, want := childEntry.Size, int64(len(content)); got != want {
		t.Fatalf("expected hydrated child entry size %d, got %d", want, got)
	}
}

func TestCheckoutMountedSliceUsesLiveBackingFolder(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	writeBackingFile := func(filePath, content string) {
		t.Helper()
		hash := mustWriteSliceManifest(t, ctx, st, "root_slice", filePath, []byte(content))
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       "root_slice:" + filePath,
			Path:     filePath,
			Type:     "file",
			ParentID: "root_slice",
			Size:     int64(len(content)),
			Hash:     hash,
		}); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", filePath, err)
		}
		if err := st.AddFileToSlice(ctx, filePath, "root_slice"); err != nil {
			t.Fatalf("AddFileToSlice(%s) failed: %v", filePath, err)
		}
	}

	writeBackingFile("tester/shared/README.md", "old readme")
	writeBackingFile("tester/other/secret.txt", "secret")

	srv := NewService(st)
	for _, sliceID := range []string{"shared-a", "shared-b"} {
		if _, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
			ParentSliceId: "root_slice",
			NewSliceId:    sliceID,
			Name:          sliceID,
			FolderPaths:   []string{"tester/shared"},
			Description:   "shared folder slice",
		}); err != nil {
			t.Fatalf("CreateSliceFromFolder(%s) failed: %v", sliceID, err)
		}
	}

	writeBackingFile("tester/shared/README.md", "new readme")
	writeBackingFile("tester/shared/src/new.txt", "new file")

	checkoutResp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{SliceId: "shared-b"})
	if err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}
	byPath := map[string]*slicev1.FileMetadata{}
	paths := make([]string, 0)
	for _, meta := range checkoutResp.GetManifest().GetFileMetadata() {
		byPath[meta.GetPath()] = meta
		paths = append(paths, meta.GetPath())
	}
	if _, ok := byPath["tester/shared/src/new.txt"]; !ok {
		t.Fatalf("checkout is missing live backing file; paths=%v", paths)
	}
	if _, ok := byPath["tester/other/secret.txt"]; ok {
		t.Fatalf("checkout leaked file outside mounted folder")
	}
	if got := string(mustAssembleCheckoutContent(t, checkoutResp, byPath["tester/shared/README.md"])); got != "new readme" {
		t.Fatalf("checkout README content = %q, want updated backing content", got)
	}
	if got := string(mustAssembleCheckoutContent(t, checkoutResp, byPath["tester/shared/src/new.txt"])); got != "new file" {
		t.Fatalf("checkout new file content = %q, want live backing content", got)
	}
}

func TestCheckoutRootMountedSliceUsesLiveHomeBackingFolder(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	home, err := homeslice.EnsureUserHomeSlice(ctx, st, "tester")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}
	root, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}

	hash, err := storage.WriteSliceFileManifest(ctx, st, home.ID, "tester/shared/new.txt", []byte("new from home"))
	if err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(home.ID, "tester/shared/new.txt"),
		Path:     "tester/shared/new.txt",
		Type:     "file",
		ParentID: common.GenerateEntryID(home.ID, "tester/shared"),
		Size:     int64(len("new from home")),
		Hash:     hash.Hash,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "tester/shared/new.txt", home.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}
	slice := &models.Slice{
		ID:          "root-shared-live-home",
		Name:        "root-shared-live-home",
		Owners:      []string{"tester"},
		CreatedBy:   "tester",
		ParentSlice: root.ID,
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "tester/shared", Alias: "tester/shared"},
		},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	srv := NewService(st)
	checkoutResp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{SliceId: slice.ID})
	if err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}
	byPath := map[string]*slicev1.FileMetadata{}
	for _, meta := range checkoutResp.GetManifest().GetFileMetadata() {
		byPath[meta.GetPath()] = meta
	}
	meta, ok := byPath["tester/shared/new.txt"]
	if !ok {
		t.Fatalf("checkout is missing live home file: %#v", checkoutResp.GetManifest().GetFileMetadata())
	}
	if got := string(mustAssembleCheckoutContent(t, checkoutResp, meta)); got != "new from home" {
		t.Fatalf("checkout content = %q, want live home content", got)
	}
}

func TestCheckoutRootMountedSliceUsesLiveHomeBackingSliceFilesFallback(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	home, err := homeslice.EnsureUserHomeSlice(ctx, st, "tester")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}
	root, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}

	rootManifest, err := storage.WriteSliceFileManifest(ctx, st, root.ID, "tester/shared/fallback.txt", []byte("stale root"))
	if err != nil {
		t.Fatalf("WriteSliceFileManifest(root) failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(root.ID, "tester/shared/fallback.txt"),
		Path:     "tester/shared/fallback.txt",
		Type:     "file",
		ParentID: common.GenerateEntryID(root.ID, "tester/shared"),
		Size:     int64(len("stale root")),
		Hash:     rootManifest.Hash,
	}); err != nil {
		t.Fatalf("AddEntry(root) failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "tester/shared/fallback.txt", root.ID); err != nil {
		t.Fatalf("AddFileToSlice(root) failed: %v", err)
	}
	if _, err := storage.WriteSliceFileManifest(ctx, st, home.ID, "tester/shared/fallback.txt", []byte("live home")); err != nil {
		t.Fatalf("WriteSliceFileManifest(home) failed: %v", err)
	}
	if err := st.SetSliceFiles(ctx, home.ID, []string{"tester/shared/fallback.txt"}); err != nil {
		t.Fatalf("SetSliceFiles(home) failed: %v", err)
	}

	slice := &models.Slice{
		ID:          "root-shared-live-home-files-fallback",
		Name:        "root-shared-live-home-files-fallback",
		Owners:      []string{"tester"},
		CreatedBy:   "tester",
		ParentSlice: root.ID,
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "tester/shared", Alias: "tester/shared"},
		},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	srv := NewService(st)
	checkoutResp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{SliceId: slice.ID})
	if err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}
	byPath := map[string]*slicev1.FileMetadata{}
	for _, meta := range checkoutResp.GetManifest().GetFileMetadata() {
		byPath[meta.GetPath()] = meta
	}
	meta, ok := byPath["tester/shared/fallback.txt"]
	if !ok {
		t.Fatalf("checkout is missing live home fallback file: %#v", checkoutResp.GetManifest().GetFileMetadata())
	}
	if got := string(mustAssembleCheckoutContent(t, checkoutResp, meta)); got != "live home" {
		t.Fatalf("checkout content = %q, want live home fallback content", got)
	}
}

func TestCheckoutMountedSliceFallsBackToBackingSliceFiles(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	if _, err := storage.WriteSliceFileManifest(ctx, st, "root_slice", "tester/legacy/README.md", []byte("legacy readme")); err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "tester/legacy/README.md", "root_slice"); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	srv := NewService(st)
	if _, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root_slice",
		NewSliceId:    "legacy-mounted",
		Name:          "legacy-mounted",
		FolderPaths:   []string{"tester/legacy"},
		Description:   "legacy folder slice",
	}); err != nil {
		t.Fatalf("CreateSliceFromFolder failed: %v", err)
	}

	checkoutResp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{SliceId: "legacy-mounted"})
	if err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}
	var readme *slicev1.FileMetadata
	for _, meta := range checkoutResp.GetManifest().GetFileMetadata() {
		if meta.GetPath() == "tester/legacy/README.md" {
			readme = meta
			break
		}
	}
	if readme == nil {
		t.Fatalf("checkout is missing backing slice file: %#v", checkoutResp.GetManifest().GetFileMetadata())
	}
	if got := string(mustAssembleCheckoutContent(t, checkoutResp, readme)); got != "legacy readme" {
		t.Fatalf("checkout README content = %q, want backing file content", got)
	}
}

func TestRenameSlice(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "sl-test-rename", Name: "old-name", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.RenameSlice(ctx, &slicev1.RenameSliceRequest{
		SliceId: "sl-test-rename",
		NewName: "new-name",
	})
	if err != nil {
		t.Fatalf("RenameSlice failed: %v", err)
	}

	if resp.Name != "new-name" {
		t.Fatalf("expected name %q, got %q", "new-name", resp.Name)
	}
	if resp.Slug != "tester/old-name" {
		t.Fatalf("expected stable slug %q, got %q", "tester/old-name", resp.Slug)
	}

	// Verify persistence
	updated, err := st.GetSlice(ctx, "sl-test-rename")
	if err != nil {
		t.Fatalf("failed to get slice: %v", err)
	}
	if updated.Name != "new-name" {
		t.Fatalf("stored name not updated: %q", updated.Name)
	}
	if updated.Slug != "old-name" {
		t.Fatalf("stored slug should stay stable, got %q", updated.Slug)
	}
}

func TestDeleteSliceRemovesCustomSlice(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "sl-delete-test", Name: "delete-me", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.DeleteSlice(ctx, &slicev1.DeleteSliceRequest{SliceId: slice.ID})
	if err != nil {
		t.Fatalf("DeleteSlice failed: %v", err)
	}
	if resp.GetStatus() != "deleted" {
		t.Fatalf("expected deleted status, got %q", resp.GetStatus())
	}
	if resp.GetSlug() != "tester/delete-me" {
		t.Fatalf("expected slug %q, got %q", "tester/delete-me", resp.GetSlug())
	}
	if _, err := st.GetSlice(ctx, slice.ID); !errors.Is(err, storage.ErrSliceNotFound) {
		t.Fatalf("expected deleted slice to be gone, got err=%v", err)
	}
}

func TestDeleteSliceRejectsHomeSlice(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(context.Background()); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}
	if _, err := homeslice.EnsureUserHomeSlice(ctx, st, "tester"); err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}

	srv := NewService(st)
	_, err := srv.DeleteSlice(ctx, &slicev1.DeleteSliceRequest{SliceId: homeslice.IDForUsername("tester")})
	if err == nil {
		t.Fatal("expected home slice deletion to fail")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected failed precondition, got %v", status.Code(err))
	}
}

func TestDeleteSliceRequiresForceForOpenChangesets(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "sl-delete-open", Name: "open-work", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	if err := st.CreateChangeset(ctx, &models.Changeset{
		ID:             "cs-open",
		Hash:           "hash-open",
		SliceID:        slice.ID,
		BaseCommitHash: "base-1",
		ModifiedFiles:  []string{"README.md"},
		Status:         models.ChangesetStatusPending,
		Author:         "tester",
		Message:        "pending",
		CreatedAt:      time.Now(),
	}); err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}

	srv := NewService(st)
	_, err := srv.DeleteSlice(ctx, &slicev1.DeleteSliceRequest{SliceId: slice.ID})
	if err == nil {
		t.Fatal("expected delete without force to fail")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected failed precondition, got %v", status.Code(err))
	}

	if _, err := srv.DeleteSlice(ctx, &slicev1.DeleteSliceRequest{SliceId: slice.ID, Force: true}); err != nil {
		t.Fatalf("forced DeleteSlice failed: %v", err)
	}
}

func TestGetSliceByName(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "sl-lookup-test", Name: "my-project", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.GetSliceByName(ctx, &slicev1.GetSliceByNameRequest{Name: "my-project"})
	if err != nil {
		t.Fatalf("GetSliceByName failed: %v", err)
	}

	if resp.SliceId != "sl-lookup-test" {
		t.Fatalf("expected ID %q, got %q", "sl-lookup-test", resp.SliceId)
	}
	if resp.Name != "my-project" {
		t.Fatalf("expected name %q, got %q", "my-project", resp.Name)
	}
	if resp.Slug != "tester/my-project" {
		t.Fatalf("expected slug %q, got %q", "tester/my-project", resp.Slug)
	}
}

func TestGetSliceBySlug(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "sl-slug-test", Name: "my-project", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.GetSliceBySlug(ctx, &slicev1.GetSliceBySlugRequest{Slug: "tester/my-project"})
	if err != nil {
		t.Fatalf("GetSliceBySlug failed: %v", err)
	}

	if resp.SliceId != "sl-slug-test" {
		t.Fatalf("expected ID %q, got %q", "sl-slug-test", resp.SliceId)
	}
	if resp.Slug != "tester/my-project" {
		t.Fatalf("expected slug %q, got %q", "tester/my-project", resp.Slug)
	}
}

func TestGetSliceBySlugUsesAuthenticatedOwnerNamespaceForLocalSlug(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	for _, slice := range []*models.Slice{
		{ID: "sl-slug-tester", Name: "my-project", Owners: []string{"tester"}, CreatedBy: "tester"},
		{ID: "sl-slug-other", Name: "my-project", Owners: []string{"other"}, CreatedBy: "other"},
	} {
		if err := st.CreateSlice(ctx, slice); err != nil {
			t.Fatalf("failed to create slice %s: %v", slice.ID, err)
		}
	}

	srv := NewService(st)
	resp, err := srv.GetSliceBySlug(ctx, &slicev1.GetSliceBySlugRequest{Slug: "my-project"})
	if err != nil {
		t.Fatalf("GetSliceBySlug local failed: %v", err)
	}
	if got, want := resp.GetSliceId(), "sl-slug-tester"; got != want {
		t.Fatalf("slice id = %q, want %q", got, want)
	}
	if got, want := resp.GetSlug(), "tester/my-project"; got != want {
		t.Fatalf("slug = %q, want %q", got, want)
	}
}

func TestCreateSliceFromMultipleFoldersRemapsCheckoutPaths(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	seedFiles := map[string][]byte{
		"o/genesis/projects/repo-a/README.md":   []byte("repo a"),
		"o/genesis/projects/repo-a/pkg/util.go": []byte("package repoa"),
		"o/genesis/projects/repo-b/main.go":     []byte("package main"),
	}
	for filePath, content := range seedFiles {
		if err := st.AddFileToSlice(ctx, filePath, "root_slice"); err != nil {
			t.Fatalf("failed to add root file %s: %v", filePath, err)
		}
		mustWriteSliceManifest(t, ctx, st, "root_slice", filePath, content)
	}

	srv := NewService(st)
	createResp, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root_slice",
		NewSliceId:    "multi-folder-slice",
		Name:          "multi-folder-slice",
		Description:   "multi folder test",
		FolderPaths:   []string{"o/genesis/projects/repo-a", "o/genesis/projects/repo-b"},
	})
	if err != nil {
		t.Fatalf("CreateSliceFromFolder failed: %v", err)
	}
	if got, want := createResp.GetSliceId(), "multi-folder-slice"; got != want {
		t.Fatalf("expected slice %q, got %q", want, got)
	}

	slice, err := st.GetSlice(ctx, "multi-folder-slice")
	if err != nil {
		t.Fatalf("failed to load created slice: %v", err)
	}
	if len(slice.FolderMounts) != 2 {
		t.Fatalf("expected 2 folder mounts, got %d", len(slice.FolderMounts))
	}

	mounts := map[string]string{}
	for _, mount := range slice.FolderMounts {
		mounts[mount.SourcePath] = mount.Alias
	}
	if mounts["o/genesis/projects/repo-a"] != "o/genesis/projects/repo-a" {
		t.Fatalf("unexpected alias for repo-a: %q", mounts["o/genesis/projects/repo-a"])
	}
	if mounts["o/genesis/projects/repo-b"] != "o/genesis/projects/repo-b" {
		t.Fatalf("unexpected alias for repo-b: %q", mounts["o/genesis/projects/repo-b"])
	}

	checkoutResp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{
		SliceId:    "multi-folder-slice",
		CommitHash: "HEAD",
	})
	if err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}

	manifestPaths := map[string]bool{}
	for _, fm := range checkoutResp.GetManifest().GetFileMetadata() {
		manifestPaths[fm.GetPath()] = true
	}

	expectedPaths := []string{
		"o/genesis/projects/repo-a/README.md",
		"o/genesis/projects/repo-a/pkg/util.go",
		"o/genesis/projects/repo-b/main.go",
	}
	for _, path := range expectedPaths {
		if !manifestPaths[path] {
			t.Fatalf("expected checkout path %q, got %#v", path, manifestPaths)
		}
	}
}

type rootSliceLookupCounter struct {
	storage.Storage
	lookups int
}

func (c *rootSliceLookupCounter) GetRootSlice(ctx context.Context) (*models.Slice, error) {
	c.lookups++
	return c.Storage.GetRootSlice(ctx)
}

func TestPromoteSliceCachesRootSliceLookup(t *testing.T) {
	ctx := context.Background()
	base := storage.NewInMemoryStorage()
	if err := base.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	countingStorage := &rootSliceLookupCounter{Storage: base}
	srv := newSliceServiceServer(countingStorage)

	if err := srv.promoteSlice(ctx, "slice-a", "commit-a", []string{"a.txt"}, time.Now()); err != nil {
		t.Fatalf("first promoteSlice failed: %v", err)
	}
	if err := srv.promoteSlice(ctx, "slice-b", "commit-b", []string{"b.txt"}, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("second promoteSlice failed: %v", err)
	}

	if countingStorage.lookups != 1 {
		t.Fatalf("expected one GetRootSlice lookup across promotions, got %d", countingStorage.lookups)
	}
}

type addFileToSliceCounter struct {
	storage.Storage
	counts map[string]int
}

func (c *addFileToSliceCounter) AddFileToSlice(ctx context.Context, fileID, sliceID string) error {
	if c.counts == nil {
		c.counts = make(map[string]int)
	}
	c.counts[sliceID+":"+fileID]++
	return c.Storage.AddFileToSlice(ctx, fileID, sliceID)
}

func TestMergeChangesetDeduplicatesModifiedFiles(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()
	if err := base.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	slice := &models.Slice{ID: "slice-dup", Name: "slice-dup", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := base.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	cs := &models.Changeset{
		ID:            "cs-dup",
		SliceID:       slice.ID,
		ModifiedFiles: []string{"dup.txt", "dup.txt", "dup.txt"},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	if err := base.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	countingStorage := &addFileToSliceCounter{Storage: base, counts: map[string]int{}}
	srv := newSliceServiceServer(countingStorage)

	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", resp.GetStatus())
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedPromotions(waitCtx); err != nil {
		t.Fatalf("timed out waiting for root promotion queue: %v", err)
	}

	if got := countingStorage.counts["slice-dup:dup.txt"]; got != 1 {
		t.Fatalf("expected one ownership write for slice file, got %d", got)
	}
	if got := countingStorage.counts["root_slice:dup.txt"]; got != 1 {
		t.Fatalf("expected one ownership write for root file, got %d", got)
	}

	updatedCS, err := base.GetChangeset(ctx, cs.ID)
	if err != nil {
		t.Fatalf("failed to load merged changeset: %v", err)
	}
	if len(updatedCS.ModifiedFiles) != 1 || updatedCS.ModifiedFiles[0] != "dup.txt" {
		t.Fatalf("expected deduplicated modified files, got %#v", updatedCS.ModifiedFiles)
	}
}

func TestMergeChangesetReturnsAbortedWhenSliceAlreadyLocked(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-locked", Name: "slice-locked", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	cs := &models.Changeset{
		ID:            "cs-locked",
		SliceID:       slice.ID,
		ModifiedFiles: []string{"locked.txt"},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}
	if err := st.LockSliceAndFiles(ctx, slice.ID, []string{"other.txt"}); err != nil {
		t.Fatalf("failed to pre-lock slice: %v", err)
	}
	defer st.UnlockSliceAndFiles(ctx, slice.ID, []string{"other.txt"})

	srv := newSliceServiceServer(st)
	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("expected structured lock response, got error: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_LOCKED {
		t.Fatalf("expected LOCKED status, got %v", resp.GetStatus())
	}
	if !strings.Contains(resp.GetMessage(), "locked") {
		t.Fatalf("expected lock message, got %q", resp.GetMessage())
	}
}

func TestMergeChangesetConcurrentSameSliceOneAborts(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-concurrent", Name: "slice-concurrent", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := base.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	cs1 := &models.Changeset{
		ID:            "cs-concurrent-1",
		SliceID:       slice.ID,
		ModifiedFiles: []string{"a.txt"},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	cs2 := &models.Changeset{
		ID:            "cs-concurrent-2",
		SliceID:       slice.ID,
		ModifiedFiles: []string{"b.txt"},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	if err := base.CreateChangeset(ctx, cs1); err != nil {
		t.Fatalf("failed to create changeset 1: %v", err)
	}
	if err := base.CreateChangeset(ctx, cs2); err != nil {
		t.Fatalf("failed to create changeset 2: %v", err)
	}

	blocking := newBlockingLockStorage(base)
	srv := newSliceServiceServer(blocking)

	type mergeResult struct {
		resp *slicev1.MergeChangesetResponse
		err  error
	}
	resultCh := make(chan mergeResult, 1)
	go func() {
		resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs1.ID})
		resultCh <- mergeResult{resp: resp, err: err}
	}()

	select {
	case <-blocking.firstLockAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first merge to acquire slice lock")
	}

	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs2.ID})
	if err != nil {
		t.Fatalf("expected structured lock response for second merge, got error: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_LOCKED {
		t.Fatalf("expected LOCKED status for second merge, got %v", resp.GetStatus())
	}

	close(blocking.releaseFirstLock)
	first := <-resultCh
	if first.err != nil {
		t.Fatalf("expected first merge to succeed after releasing lock, got %v", first.err)
	}
	if first.resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected first merge success, got %v", first.resp.GetStatus())
	}
}

func TestMergeChangesetConcurrentOverlappingFilesOneAborts(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()

	const sharedFile = "shared.txt"
	sliceA := &models.Slice{ID: "slice-overlap-a", Name: "slice-overlap-a", Files: []string{sharedFile}, Owners: []string{"tester"}, CreatedBy: "tester"}
	sliceB := &models.Slice{ID: "slice-overlap-b", Name: "slice-overlap-b", Files: []string{sharedFile}, Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := base.CreateSlice(ctx, sliceA); err != nil {
		t.Fatalf("failed to create slice A: %v", err)
	}
	if err := base.CreateSlice(ctx, sliceB); err != nil {
		t.Fatalf("failed to create slice B: %v", err)
	}
	if _, err := base.ResolveConflict(ctx, sharedFile, sliceA.ID); err != nil {
		t.Fatalf("failed to resolve initial ownership to slice A: %v", err)
	}

	csA := &models.Changeset{
		ID:            "cs-overlap-a",
		SliceID:       sliceA.ID,
		ModifiedFiles: []string{sharedFile},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	csB := &models.Changeset{
		ID:            "cs-overlap-b",
		SliceID:       sliceB.ID,
		ModifiedFiles: []string{sharedFile},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	if err := base.CreateChangeset(ctx, csA); err != nil {
		t.Fatalf("failed to create changeset A: %v", err)
	}
	if err := base.CreateChangeset(ctx, csB); err != nil {
		t.Fatalf("failed to create changeset B: %v", err)
	}

	blocking := newBlockingLockStorage(base)
	srv := newSliceServiceServer(blocking)

	type mergeResult struct {
		resp *slicev1.MergeChangesetResponse
		err  error
	}
	resultCh := make(chan mergeResult, 1)
	go func() {
		resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: csA.ID})
		resultCh <- mergeResult{resp: resp, err: err}
	}()

	select {
	case <-blocking.firstLockAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for overlapping merge to acquire file lock")
	}

	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: csB.ID})
	if err != nil {
		t.Fatalf("expected structured lock response for overlapping merge, got error: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_LOCKED {
		t.Fatalf("expected LOCKED status for overlapping merge, got %v", resp.GetStatus())
	}

	close(blocking.releaseFirstLock)
	first := <-resultCh
	if first.err != nil {
		t.Fatalf("expected first overlapping merge to succeed after releasing lock, got %v", first.err)
	}
	if first.resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected first overlapping merge success, got %v", first.resp.GetStatus())
	}
}

func TestCreateChangesetDeduplicatesModifiedFiles(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-create-dup", Name: "slice-create-dup", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	createResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       slice.ID,
		ModifiedFiles: []string{"dup.txt", "dup.txt", "dup.txt"},
		Message:       "dedupe",
	})
	if err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}

	cs, err := st.GetChangeset(ctx, createResp.GetChangesetId())
	if err != nil {
		t.Fatalf("failed to load changeset: %v", err)
	}
	if len(cs.ModifiedFiles) != 1 || cs.ModifiedFiles[0] != "dup.txt" {
		t.Fatalf("expected deduplicated modified files, got %#v", cs.ModifiedFiles)
	}

	reviewResp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if reviewResp.GetDiff().GetFilesAdded() != 1 {
		t.Fatalf("expected diff files_added=1, got %d", reviewResp.GetDiff().GetFilesAdded())
	}
}

func TestCreateChangesetUsesIncrementalGlobalIDs(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-create-id-seq", Name: "slice-create-id-seq", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	first, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       slice.ID,
		ModifiedFiles: []string{"first.txt"},
		Message:       "first changeset",
	})
	if err != nil {
		t.Fatalf("CreateChangeset(first) failed: %v", err)
	}
	second, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       slice.ID,
		ModifiedFiles: []string{"second.txt"},
		Message:       "second changeset",
	})
	if err != nil {
		t.Fatalf("CreateChangeset(second) failed: %v", err)
	}

	if got, want := first.GetChangesetId(), "cs-global-1"; got != want {
		t.Fatalf("expected first changeset id %q, got %q", want, got)
	}
	if got, want := second.GetChangesetId(), "cs-global-2"; got != want {
		t.Fatalf("expected second changeset id %q, got %q", want, got)
	}
}

func TestReviewChangesetIncludesInlinePatchForStandardChangeset(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-standard-patch", Name: "slice-standard-patch", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	const (
		baseCommit = "commit-standard-base"
		filePath   = "README.md"
	)
	baseContent := []byte("line1\n")
	headContent := []byte("line1\nline2\n")
	baseHash := hashBytes(baseContent)

	mustWriteVersionedManifest(t, ctx, st, filePath, baseHash, baseContent)
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: baseCommit,
		SliceID:    slice.ID,
		Files: map[string]string{
			filePath: baseHash,
		},
		Timestamp: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatalf("failed to save base commit snapshot: %v", err)
	}

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(headContent)),
	}); err != nil {
		t.Fatalf("failed to add current file entry: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, headContent)
	if err := st.AddFileToSlice(ctx, filePath, slice.ID); err != nil {
		t.Fatalf("failed to index current file in slice: %v", err)
	}

	srv := NewService(st)
	createResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        slice.ID,
		BaseCommitHash: baseCommit,
		ModifiedFiles:  []string{filePath},
		Message:        "standard patch coverage",
	})
	if err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}

	reviewResp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{
		ChangesetId: createResp.GetChangesetId(),
	})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if len(reviewResp.GetChanges()) != 1 {
		t.Fatalf("expected one review change, got %d", len(reviewResp.GetChanges()))
	}

	change := reviewResp.GetChanges()[0]
	if got, want := change.GetChangeType(), filev1.ChangeType_CHANGE_TYPE_MODIFY; got != want {
		t.Fatalf("expected change type %v, got %v", want, got)
	}
	if strings.TrimSpace(change.GetPatch()) == "" {
		t.Fatalf("expected inline patch, got empty patch")
	}
	if !strings.Contains(change.GetPatch(), "+line2") {
		t.Fatalf("expected patch to include added line, got %q", change.GetPatch())
	}
	if reviewResp.GetDiff().GetFilesModified() != 1 {
		t.Fatalf("expected files_modified=1, got %d", reviewResp.GetDiff().GetFilesModified())
	}
}

func TestReviewChangesetMarksStaleBaseAsNeedsRebase(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-stale-review", Name: "slice-stale-review", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		t.Fatalf("failed to load slice metadata: %v", err)
	}
	meta.HeadCommitHash = "head-current"
	if err := st.UpdateSliceMetadata(ctx, slice.ID, meta); err != nil {
		t.Fatalf("failed to update slice metadata: %v", err)
	}

	cs := &models.Changeset{
		ID:             "cs-stale-review",
		SliceID:        slice.ID,
		BaseCommitHash: "head-old",
		ModifiedFiles:  []string{"README.md"},
		Status:         models.ChangesetStatusPending,
		CreatedAt:      time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if resp.GetReviewStatus() != slicev1.ReviewStatus_NEEDS_REBASE {
		t.Fatalf("expected NEEDS_REBASE, got %v", resp.GetReviewStatus())
	}
	if len(resp.GetWarnings()) == 0 || !strings.Contains(resp.GetWarnings()[0], "stale") {
		t.Fatalf("expected stale-base warning, got %#v", resp.GetWarnings())
	}
	if len(resp.GetIssues()) == 0 || resp.GetIssues()[0].GetType() != slicev1.ReviewIssueType_REVIEW_ISSUE_TYPE_STALE_BASE {
		t.Fatalf("expected stale-base issue, got %#v", resp.GetIssues())
	}
}

func TestMergeChangesetRejectsStaleBase(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-stale-merge", Name: "slice-stale-merge", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		t.Fatalf("failed to load slice metadata: %v", err)
	}
	meta.HeadCommitHash = "head-current"
	if err := st.UpdateSliceMetadata(ctx, slice.ID, meta); err != nil {
		t.Fatalf("failed to update slice metadata: %v", err)
	}

	cs := &models.Changeset{
		ID:             "cs-stale-merge",
		SliceID:        slice.ID,
		BaseCommitHash: "head-old",
		ModifiedFiles:  []string{"README.md"},
		Status:         models.ChangesetStatusPending,
		CreatedAt:      time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("expected structured stale-base response, got error: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_STALE_BASE {
		t.Fatalf("expected STALE_BASE status, got %v", resp.GetStatus())
	}
	if !strings.Contains(resp.GetMessage(), "stale") {
		t.Fatalf("expected stale-base message, got %q", resp.GetMessage())
	}
}

func TestReviewChangesetReportsContentConflictIssue(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	sliceA := &models.Slice{ID: "slice-review-conflict-a", Name: "slice-review-conflict-a", Owners: []string{"tester"}, CreatedBy: "tester"}
	sliceB := &models.Slice{ID: "slice-review-conflict-b", Name: "slice-review-conflict-b", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, sliceA); err != nil {
		t.Fatalf("failed to create slice A: %v", err)
	}
	if err := st.CreateSlice(ctx, sliceB); err != nil {
		t.Fatalf("failed to create slice B: %v", err)
	}

	filePath := "README.md"
	for _, setup := range []struct {
		sliceID string
		content string
	}{
		{sliceA.ID, "alpha\n"},
		{sliceB.ID, "beta\n"},
	} {
		hash := mustWriteSliceManifest(t, ctx, st, setup.sliceID, filePath, []byte(setup.content))
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       fmt.Sprintf("%s:%s", setup.sliceID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: setup.sliceID,
			Hash:     hash,
			Size:     int64(len(setup.content)),
		}); err != nil {
			t.Fatalf("failed to add entry for %s: %v", setup.sliceID, err)
		}
		if err := st.AddFileToSlice(ctx, filePath, setup.sliceID); err != nil {
			t.Fatalf("failed to add file to %s: %v", setup.sliceID, err)
		}
	}

	cs := &models.Changeset{
		ID:            "cs-review-content-conflict",
		SliceID:       sliceA.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if resp.GetReviewStatus() != slicev1.ReviewStatus_HAS_CONFLICTS {
		t.Fatalf("expected HAS_CONFLICTS, got %v", resp.GetReviewStatus())
	}
	if len(resp.GetIssues()) == 0 || resp.GetIssues()[0].GetType() != slicev1.ReviewIssueType_REVIEW_ISSUE_TYPE_CONTENT_CONFLICT {
		t.Fatalf("expected content-conflict issue, got %#v", resp.GetIssues())
	}
}

func TestReviewChangesetReportsOwnershipIssueWithoutBlockingMerge(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	sliceA := &models.Slice{ID: "slice-review-ownership-a", Name: "slice-review-ownership-a", Owners: []string{"tester"}, CreatedBy: "tester"}
	sliceB := &models.Slice{ID: "slice-review-ownership-b", Name: "slice-review-ownership-b", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, sliceA); err != nil {
		t.Fatalf("failed to create slice A: %v", err)
	}
	if err := st.CreateSlice(ctx, sliceB); err != nil {
		t.Fatalf("failed to create slice B: %v", err)
	}

	filePath := "README.md"
	for _, sliceID := range []string{sliceA.ID, sliceB.ID} {
		hash := mustWriteSliceManifest(t, ctx, st, sliceID, filePath, []byte("shared\n"))
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       fmt.Sprintf("%s:%s", sliceID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: sliceID,
			Hash:     hash,
			Size:     int64(len("shared\n")),
		}); err != nil {
			t.Fatalf("failed to add entry for %s: %v", sliceID, err)
		}
		if err := st.AddFileToSlice(ctx, filePath, sliceID); err != nil {
			t.Fatalf("failed to add file to %s: %v", sliceID, err)
		}
	}

	cs := &models.Changeset{
		ID:            "cs-review-ownership",
		SliceID:       sliceA.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if resp.GetReviewStatus() != slicev1.ReviewStatus_READY_FOR_MERGE {
		t.Fatalf("expected READY_FOR_MERGE, got %v", resp.GetReviewStatus())
	}
	if len(resp.GetIssues()) == 0 || resp.GetIssues()[0].GetType() != slicev1.ReviewIssueType_REVIEW_ISSUE_TYPE_OWNERSHIP_CONFLICT {
		t.Fatalf("expected ownership issue, got %#v", resp.GetIssues())
	}
}

func TestMergeChangesetIgnoresNormalizedCrossSliceFileState(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	sliceA := &models.Slice{ID: "slice-merge-a", Name: "slice-merge-a", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, sliceA); err != nil {
		t.Fatalf("failed to create slice A: %v", err)
	}
	sliceB := &models.Slice{ID: "slice-merge-b", Name: "slice-merge-b", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, sliceB); err != nil {
		t.Fatalf("failed to create slice B: %v", err)
	}

	filePath := "README.md"
	for _, setup := range []struct {
		sliceID string
		content string
	}{
		{sliceA.ID, "shared\n"},
		{sliceB.ID, "different\n"},
	} {
		manifest, err := storage.WriteSliceFileManifest(ctx, st, setup.sliceID, filePath, []byte(setup.content))
		if err != nil {
			t.Fatalf("failed to write manifest for %s: %v", setup.sliceID, err)
		}
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       setup.sliceID + ":" + filePath,
			Path:     filePath,
			Type:     "file",
			ParentID: setup.sliceID,
			Size:     manifest.TotalSize,
			Hash:     manifest.Hash,
		}); err != nil {
			t.Fatalf("failed to add entry for %s: %v", setup.sliceID, err)
		}
		if err := st.AddFileToSlice(ctx, filePath, setup.sliceID); err != nil {
			t.Fatalf("failed to index file for %s: %v", setup.sliceID, err)
		}
	}

	if _, err := storage.NormalizeConflictToPreferred(ctx, st, filePath, sliceA.ID); err != nil {
		t.Fatalf("failed to normalize conflicting slices: %v", err)
	}

	cs := &models.Changeset{
		ID:            "cs-normalized-merge",
		SliceID:       sliceA.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success once conflicting slices are normalized, got %v", resp.GetStatus())
	}
}

func TestRebaseChangesetUsesCurrentSliceHead(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-rebase-head", Name: "slice-rebase-head", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		t.Fatalf("failed to load slice metadata: %v", err)
	}
	meta.HeadCommitHash = "head-current"
	if err := st.UpdateSliceMetadata(ctx, slice.ID, meta); err != nil {
		t.Fatalf("failed to update slice metadata: %v", err)
	}

	cs := &models.Changeset{
		ID:             "cs-rebase-head",
		SliceID:        slice.ID,
		BaseCommitHash: "head-old",
		ModifiedFiles:  []string{"README.md"},
		Status:         models.ChangesetStatusPending,
		CreatedAt:      time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.RebaseChangeset(ctx, &slicev1.RebaseChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("RebaseChangeset failed: %v", err)
	}
	if resp.GetNewBaseCommitHash() != "head-current" {
		t.Fatalf("expected new base to be current head, got %q", resp.GetNewBaseCommitHash())
	}

	updated, err := st.GetChangeset(ctx, cs.ID)
	if err != nil {
		t.Fatalf("failed to reload changeset: %v", err)
	}
	if updated.BaseCommitHash != "head-current" {
		t.Fatalf("expected changeset base to update to current head, got %q", updated.BaseCommitHash)
	}

	snapshots, err := st.ListChangesetSnapshots(ctx, cs.ID, 10)
	if err != nil {
		t.Fatalf("failed to list changeset snapshots: %v", err)
	}
	if len(snapshots) == 0 || snapshots[0].BaseCommitHash != "head-current" {
		t.Fatalf("expected latest snapshot base to be current head, got %#v", snapshots)
	}
}

func TestCreateChangesetAppendCreatesSnapshotVersions(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-snapshots", Name: "slice-snapshots", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	createResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        slice.ID,
		BaseCommitHash: "base-v1",
		ModifiedFiles:  []string{"a.txt", "b.txt"},
		Message:        "snapshot v1",
	})
	if err != nil {
		t.Fatalf("CreateChangeset v1 failed: %v", err)
	}

	appendResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		ChangesetId:    createResp.GetChangesetId(),
		BaseCommitHash: "base-v2",
		ModifiedFiles:  []string{"c.txt"},
		Message:        "snapshot v2",
	})
	if err != nil {
		t.Fatalf("CreateChangeset append failed: %v", err)
	}
	if appendResp.GetChangesetId() != createResp.GetChangesetId() {
		t.Fatalf("expected append to keep changeset ID %q, got %q", createResp.GetChangesetId(), appendResp.GetChangesetId())
	}

	cs, err := st.GetChangeset(ctx, createResp.GetChangesetId())
	if err != nil {
		t.Fatalf("failed to reload changeset: %v", err)
	}
	if len(cs.ModifiedFiles) != 1 || cs.ModifiedFiles[0] != "c.txt" {
		t.Fatalf("expected latest changeset files to be [c.txt], got %#v", cs.ModifiedFiles)
	}

	snapshotsResp, err := srv.ListChangesetSnapshots(ctx, &slicev1.ListChangesetSnapshotsRequest{
		ChangesetId: createResp.GetChangesetId(),
	})
	if err != nil {
		t.Fatalf("ListChangesetSnapshots failed: %v", err)
	}
	if len(snapshotsResp.GetSnapshots()) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snapshotsResp.GetSnapshots()))
	}
	if snapshotsResp.GetSnapshots()[0].GetVersion() != 2 || snapshotsResp.GetSnapshots()[1].GetVersion() != 1 {
		t.Fatalf("unexpected snapshot versions: got [%d, %d]", snapshotsResp.GetSnapshots()[0].GetVersion(), snapshotsResp.GetSnapshots()[1].GetVersion())
	}

	reviewLatest, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{
		ChangesetId: createResp.GetChangesetId(),
	})
	if err != nil {
		t.Fatalf("ReviewChangeset latest failed: %v", err)
	}
	if reviewLatest.GetSnapshot() == nil || reviewLatest.GetSnapshot().GetVersion() != 2 {
		t.Fatalf("expected latest snapshot version 2, got %#v", reviewLatest.GetSnapshot())
	}
	if reviewLatest.GetDiff().GetFilesAdded() != 1 {
		t.Fatalf("expected latest diff files_added=1, got %d", reviewLatest.GetDiff().GetFilesAdded())
	}

	reviewV1, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{
		ChangesetId:     createResp.GetChangesetId(),
		SnapshotVersion: 1,
	})
	if err != nil {
		t.Fatalf("ReviewChangeset snapshot v1 failed: %v", err)
	}
	if reviewV1.GetSnapshot() == nil || reviewV1.GetSnapshot().GetVersion() != 1 {
		t.Fatalf("expected snapshot version 1, got %#v", reviewV1.GetSnapshot())
	}
	if reviewV1.GetDiff().GetFilesAdded() != 2 {
		t.Fatalf("expected snapshot v1 diff files_added=2, got %d", reviewV1.GetDiff().GetFilesAdded())
	}

	if _, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{
		ChangesetId:     createResp.GetChangesetId(),
		SnapshotVersion: 99,
	}); err == nil {
		t.Fatalf("expected error for missing snapshot version")
	}
}

func TestListChangesetSnapshotsReturnsSyntheticWhenNoStoredSnapshots(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-synthetic-snapshot", Name: "slice-synthetic-snapshot", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	cs := &models.Changeset{
		ID:             "cs-synthetic-snapshot",
		Hash:           "hash-synthetic",
		SliceID:        slice.ID,
		BaseCommitHash: "base-synthetic",
		ModifiedFiles:  []string{"README.md"},
		Status:         models.ChangesetStatusPending,
		Author:         "tester",
		Message:        "synthetic",
		CreatedAt:      time.Now().Add(-time.Minute),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to seed changeset: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.ListChangesetSnapshots(ctx, &slicev1.ListChangesetSnapshotsRequest{
		ChangesetId: cs.ID,
	})
	if err != nil {
		t.Fatalf("ListChangesetSnapshots failed: %v", err)
	}
	if len(resp.GetSnapshots()) != 1 {
		t.Fatalf("expected one synthetic snapshot, got %d", len(resp.GetSnapshots()))
	}
	snapshot := resp.GetSnapshots()[0]
	if snapshot.GetVersion() != 1 {
		t.Fatalf("expected synthetic snapshot version 1, got %d", snapshot.GetVersion())
	}
	if snapshot.GetHash() != cs.Hash {
		t.Fatalf("expected synthetic snapshot hash %q, got %q", cs.Hash, snapshot.GetHash())
	}
}

type promotionWriteCounter struct {
	storage.Storage

	mu                      sync.Mutex
	rootAddCalls            map[string]int
	updateGlobalStateCalls  int
	updateRootMetadataCalls int
}

func (c *promotionWriteCounter) AddFileToSlice(ctx context.Context, fileID, sliceID string) error {
	if sliceID == "root_slice" {
		c.mu.Lock()
		if c.rootAddCalls == nil {
			c.rootAddCalls = make(map[string]int)
		}
		c.rootAddCalls[fileID]++
		c.mu.Unlock()
	}
	return c.Storage.AddFileToSlice(ctx, fileID, sliceID)
}

func (c *promotionWriteCounter) UpdateGlobalState(ctx context.Context, state *models.GlobalState) error {
	c.mu.Lock()
	c.updateGlobalStateCalls++
	c.mu.Unlock()
	return c.Storage.UpdateGlobalState(ctx, state)
}

func (c *promotionWriteCounter) UpdateSliceMetadata(ctx context.Context, sliceID string, metadata *models.SliceMetadata) error {
	if sliceID == "root_slice" {
		c.mu.Lock()
		c.updateRootMetadataCalls++
		c.mu.Unlock()
	}
	return c.Storage.UpdateSliceMetadata(ctx, sliceID, metadata)
}

func TestRootPromotionQueueBatchesSameSlice(t *testing.T) {
	ctx := context.Background()
	base := storage.NewInMemoryStorage()
	if err := base.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	countingStorage := &promotionWriteCounter{
		Storage:      base,
		rootAddCalls: make(map[string]int),
	}
	srv := newSliceServiceServer(countingStorage)
	srv.promotionBatchWindow = 50 * time.Millisecond

	now := time.Now()
	jobs := []struct {
		commitHash string
		files      []string
	}{
		{commitHash: "commit-1", files: []string{"a.txt", "a.txt"}},
		{commitHash: "commit-2", files: []string{"a.txt", "b.txt"}},
		{commitHash: "commit-3", files: []string{"b.txt", "c.txt"}},
	}
	for i, job := range jobs {
		if err := srv.enqueueRootPromotion(ctx, "slice-batch", job.commitHash, job.files, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("enqueueRootPromotion(%s) failed: %v", job.commitHash, err)
		}
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedPromotions(waitCtx); err != nil {
		t.Fatalf("timed out waiting for promotion queue: %v", err)
	}

	if got := countingStorage.updateGlobalStateCalls; got != 1 {
		t.Fatalf("expected one global state write for batched promotion, got %d", got)
	}
	if got := countingStorage.updateRootMetadataCalls; got != 1 {
		t.Fatalf("expected one root metadata write for batched promotion, got %d", got)
	}
	for _, fileID := range []string{"a.txt", "b.txt", "c.txt"} {
		if got := countingStorage.rootAddCalls[fileID]; got != 1 {
			t.Fatalf("expected one root ownership write for %s, got %d", fileID, got)
		}
	}

	state, err := base.GetGlobalState(ctx)
	if err != nil {
		t.Fatalf("failed to load global state: %v", err)
	}
	if got, want := state.GlobalCommitHash, "commit-3"; got != want {
		t.Fatalf("expected head commit %q, got %q", want, got)
	}
	if got, want := len(state.History), len(jobs); got != want {
		t.Fatalf("expected %d history entries, got %d", want, got)
	}
	if got, want := state.History[0].CommitHash, "commit-3"; got != want {
		t.Fatalf("expected newest history commit %q, got %q", want, got)
	}
}

func TestRevertChangesetHashRoundTrip(t *testing.T) {
	commitHash := "commit-abcdef123456"
	changeID := "chg-001"

	hash := buildRevertChangesetHash(commitHash, changeID)
	gotCommit, gotChange, ok := parseRevertChangesetHash(hash)
	if !ok {
		t.Fatalf("expected parseRevertChangesetHash to parse %q", hash)
	}
	if gotCommit != commitHash {
		t.Fatalf("expected commit hash %q, got %q", commitHash, gotCommit)
	}
	if gotChange != changeID {
		t.Fatalf("expected change id %q, got %q", changeID, gotChange)
	}
}

func TestReviewChangesetIncludesRevertPatch(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-revert", Name: "slice-revert", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	mustWriteVersionedManifest(t, ctx, st, "README.md", "content-old", []byte("line1\n"))
	mustWriteVersionedManifest(t, ctx, st, "README.md", "content-new", []byte("line1\nline2\n"))

	const sourceCommit = "commit-source"
	const sourceChangeID = "chg-source"
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:           sourceChangeID,
		SliceID:      slice.ID,
		CommitHash:   sourceCommit,
		Path:         "README.md",
		ChangeType:   models.ChangeTypeModify,
		OldHash:      "content-old",
		NewHash:      "content-new",
		LinesAdded:   1,
		LinesDeleted: 0,
		Author:       "tester",
		Message:      "add line2",
		Timestamp:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("failed to add source change: %v", err)
	}

	srv := newSliceServiceServer(st)
	createResp, err := srv.RevertCommitChange(ctx, &slicev1.RevertCommitChangeRequest{
		CommitHash: sourceCommit,
		ChangeId:   sourceChangeID,
		SliceId:    slice.ID,
	})
	if err != nil {
		t.Fatalf("RevertCommitChange failed: %v", err)
	}

	reviewResp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if len(reviewResp.GetChanges()) != 1 {
		t.Fatalf("expected 1 review change, got %d", len(reviewResp.GetChanges()))
	}

	change := reviewResp.GetChanges()[0]
	if got, want := change.GetChangeType(), filev1.ChangeType_CHANGE_TYPE_MODIFY; got != want {
		t.Fatalf("expected revert change type %v, got %v", want, got)
	}
	if !strings.Contains(change.GetPatch(), "-line2") {
		t.Fatalf("expected revert patch to remove line2, got %q", change.GetPatch())
	}
	if reviewResp.GetDiff().GetFilesModified() != 1 {
		t.Fatalf("expected diff files_modified=1, got %d", reviewResp.GetDiff().GetFilesModified())
	}
}

func TestReviewChangesetIncludesAllCommitDiffChangesForRevert(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-revert-all", Name: "slice-revert-all", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	contents := []*models.FileContent{
		{FileID: "readme-old", Path: "README.md", Content: []byte("line1\n"), Size: int64(len("line1\n")), Hash: "readme-old"},
		{FileID: "readme-new", Path: "README.md", Content: []byte("line1\nline2\n"), Size: int64(len("line1\nline2\n")), Hash: "readme-new"},
		{FileID: "newfile", Path: "NEW.md", Content: []byte("new file\n"), Size: int64(len("new file\n")), Hash: "newfile"},
	}
	for _, content := range contents {
		mustWriteVersionedManifest(t, ctx, st, content.Path, content.Hash, content.Content)
	}

	const sourceCommit = "commit-source-all"
	seedChanges := []*models.FileChangeRecord{
		{
			ID:           "chg-modify",
			SliceID:      slice.ID,
			CommitHash:   sourceCommit,
			Path:         "README.md",
			ChangeType:   models.ChangeTypeModify,
			OldHash:      "readme-old",
			NewHash:      "readme-new",
			LinesAdded:   1,
			LinesDeleted: 0,
			Author:       "tester",
			Message:      "modify readme",
			Timestamp:    time.Now().UTC(),
		},
		{
			ID:           "chg-add",
			SliceID:      slice.ID,
			CommitHash:   sourceCommit,
			Path:         "NEW.md",
			ChangeType:   models.ChangeTypeAdd,
			OldHash:      "",
			NewHash:      "newfile",
			LinesAdded:   1,
			LinesDeleted: 0,
			Author:       "tester",
			Message:      "add new file",
			Timestamp:    time.Now().UTC().Add(time.Second),
		},
	}
	if err := st.AddFileChanges(ctx, seedChanges); err != nil {
		t.Fatalf("failed to add source changes: %v", err)
	}

	srv := NewService(st)
	createResp, err := srv.RevertCommitChange(ctx, &slicev1.RevertCommitChangeRequest{
		CommitHash: sourceCommit,
		SliceId:    slice.ID,
	})
	if err != nil {
		t.Fatalf("RevertCommitChange failed: %v", err)
	}

	reviewResp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if len(reviewResp.GetChanges()) != 2 {
		t.Fatalf("expected 2 review changes, got %d", len(reviewResp.GetChanges()))
	}

	changesByPath := make(map[string]*filev1.FileChangeRecord, len(reviewResp.GetChanges()))
	for _, change := range reviewResp.GetChanges() {
		changesByPath[change.GetPath()] = change
	}
	readmeChange := changesByPath["README.md"]
	if readmeChange == nil {
		t.Fatalf("expected README.md revert change in response")
	}
	if got, want := readmeChange.GetChangeType(), filev1.ChangeType_CHANGE_TYPE_MODIFY; got != want {
		t.Fatalf("expected README.md revert change type %v, got %v", want, got)
	}
	if !strings.Contains(readmeChange.GetPatch(), "-line2") {
		t.Fatalf("expected README.md revert patch to remove line2, got %q", readmeChange.GetPatch())
	}

	newFileChange := changesByPath["NEW.md"]
	if newFileChange == nil {
		t.Fatalf("expected NEW.md revert change in response")
	}
	if got, want := newFileChange.GetChangeType(), filev1.ChangeType_CHANGE_TYPE_DELETE; got != want {
		t.Fatalf("expected NEW.md revert change type %v, got %v", want, got)
	}
	if newFileChange.GetPatch() == "" {
		t.Fatalf("expected NEW.md revert patch to be present")
	}

	if reviewResp.GetDiff().GetFilesModified() != 1 {
		t.Fatalf("expected diff files_modified=1, got %d", reviewResp.GetDiff().GetFilesModified())
	}
	if reviewResp.GetDiff().GetFilesDeleted() != 1 {
		t.Fatalf("expected diff files_deleted=1, got %d", reviewResp.GetDiff().GetFilesDeleted())
	}
}

func TestMergeRevertChangesetAppliesRevertedContent(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-revert-merge", Name: "slice-revert-merge", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	const filePath = "README.md"
	oldContent := []byte("line1\n")
	newContent := []byte("line1\nline2\n")
	oldHash := hashBytes(oldContent)
	newHash := hashBytes(newContent)

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(newContent)),
	}); err != nil {
		t.Fatalf("failed to add file entry: %v", err)
	}
	if err := st.AddFileToSlice(ctx, filePath, slice.ID); err != nil {
		t.Fatalf("failed to add file to slice index: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, newContent)
	mustWriteVersionedManifest(t, ctx, st, filePath, oldHash, oldContent)

	const sourceCommit = "commit-revert-merge"
	const sourceChangeID = "chg-revert-merge"
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:           sourceChangeID,
		SliceID:      slice.ID,
		CommitHash:   sourceCommit,
		Path:         filePath,
		ChangeType:   models.ChangeTypeModify,
		OldHash:      oldHash,
		NewHash:      newHash,
		LinesAdded:   1,
		LinesDeleted: 0,
		Author:       "tester",
		Message:      "introduce line2",
		Timestamp:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("failed to add source change: %v", err)
	}

	srv := newSliceServiceServer(st)
	createResp, err := srv.RevertCommitChange(ctx, &slicev1.RevertCommitChangeRequest{
		CommitHash: sourceCommit,
		SliceId:    slice.ID,
	})
	if err != nil {
		t.Fatalf("RevertCommitChange failed: %v", err)
	}

	mergeResp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", mergeResp.GetStatus())
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedPromotions(waitCtx); err != nil {
		t.Fatalf("timed out waiting for root promotion queue: %v", err)
	}

	fileAfterMerge, err := storage.ReadSliceFileContent(ctx, st, slice.ID, filePath)
	if err != nil {
		t.Fatalf("failed to load file after merge: %v", err)
	}
	if string(fileAfterMerge.Content) != string(oldContent) {
		t.Fatalf("expected reverted content %q, got %q", string(oldContent), string(fileAfterMerge.Content))
	}
	if fileAfterMerge.Hash != oldHash {
		t.Fatalf("expected reverted hash %q, got %q", oldHash, fileAfterMerge.Hash)
	}

	commitChanges, err := st.GetCommitChanges(ctx, mergeResp.GetNewCommitHash())
	if err != nil {
		t.Fatalf("failed to load commit changes for merged revert: %v", err)
	}
	if len(commitChanges) != 1 {
		t.Fatalf("expected 1 recorded commit change, got %d", len(commitChanges))
	}
	recorded := commitChanges[0]
	if recorded.OldHash != newHash {
		t.Fatalf("expected recorded old hash %q, got %q", newHash, recorded.OldHash)
	}
	if recorded.NewHash != oldHash {
		t.Fatalf("expected recorded new hash %q, got %q", oldHash, recorded.NewHash)
	}

	snapshot, err := st.GetCommitSnapshot(ctx, mergeResp.GetNewCommitHash())
	if err != nil {
		t.Fatalf("failed to load commit snapshot: %v", err)
	}
	if got := snapshot.Files[filePath]; got != oldHash {
		t.Fatalf("expected commit snapshot hash %q, got %q", oldHash, got)
	}
}

func TestMergeRevertChangesetBypassesCrossSliceConflictChecks(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	ownerSlice := &models.Slice{ID: "slice-revert-owner", Name: "slice-revert-owner", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, ownerSlice); err != nil {
		t.Fatalf("failed to create owner slice: %v", err)
	}
	otherSlice := &models.Slice{ID: "slice-revert-other", Name: "slice-revert-other", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, otherSlice); err != nil {
		t.Fatalf("failed to create other slice: %v", err)
	}

	const filePath = "README.md"
	oldContent := []byte("line1\n")
	newContent := []byte("line1\nline2\n")
	oldHash := hashBytes(oldContent)
	newHash := hashBytes(newContent)

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       ownerSlice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: ownerSlice.ID,
		Size:     int64(len(newContent)),
	}); err != nil {
		t.Fatalf("failed to add file entry: %v", err)
	}
	if err := st.AddFileToSlice(ctx, filePath, ownerSlice.ID); err != nil {
		t.Fatalf("failed to add file to owner slice index: %v", err)
	}
	if err := st.AddFileToSlice(ctx, filePath, otherSlice.ID); err != nil {
		t.Fatalf("failed to add file to other slice index: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, ownerSlice.ID, filePath, newContent)
	mustWriteVersionedManifest(t, ctx, st, filePath, oldHash, oldContent)

	const sourceCommit = "commit-revert-cross-slice"
	const sourceChangeID = "chg-revert-cross-slice"
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:           sourceChangeID,
		SliceID:      ownerSlice.ID,
		CommitHash:   sourceCommit,
		Path:         filePath,
		ChangeType:   models.ChangeTypeModify,
		OldHash:      oldHash,
		NewHash:      newHash,
		LinesAdded:   1,
		LinesDeleted: 0,
		Author:       "tester",
		Message:      "introduce line2",
		Timestamp:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("failed to add source change: %v", err)
	}

	srv := newSliceServiceServer(st)
	createResp, err := srv.RevertCommitChange(ctx, &slicev1.RevertCommitChangeRequest{
		CommitHash: sourceCommit,
		SliceId:    ownerSlice.ID,
	})
	if err != nil {
		t.Fatalf("RevertCommitChange failed: %v", err)
	}

	mergeResp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success for revert changeset despite cross-slice file ownership, got %v", mergeResp.GetStatus())
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedPromotions(waitCtx); err != nil {
		t.Fatalf("timed out waiting for root promotion queue: %v", err)
	}

	fileAfterMerge, err := storage.ReadSliceFileContent(ctx, st, ownerSlice.ID, filePath)
	if err != nil {
		t.Fatalf("failed to load file after merge: %v", err)
	}
	if string(fileAfterMerge.Content) != string(oldContent) {
		t.Fatalf("expected reverted content %q, got %q", string(oldContent), string(fileAfterMerge.Content))
	}
	if fileAfterMerge.Hash != oldHash {
		t.Fatalf("expected reverted hash %q, got %q", oldHash, fileAfterMerge.Hash)
	}
}

func TestMergeRevertChangesetBackfillsMissingOldHash(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-revert-backfill", Name: "slice-revert-backfill", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	const filePath = "README.md"
	oldContent := []byte("line1\n")
	newContent := []byte("line1\nline2\n")
	oldHash := hashBytes(oldContent)
	newHash := hashBytes(newContent)

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(newContent)),
	}); err != nil {
		t.Fatalf("failed to add file entry: %v", err)
	}
	if err := st.AddFileToSlice(ctx, filePath, slice.ID); err != nil {
		t.Fatalf("failed to add file to slice index: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, newContent)
	mustWriteVersionedManifest(t, ctx, st, filePath, oldHash, oldContent)

	previousCommit := "commit-backfill-previous"
	sourceCommit := "commit-backfill-source"
	now := time.Now().UTC()

	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:         "chg-backfill-previous",
		SliceID:    slice.ID,
		CommitHash: previousCommit,
		Path:       filePath,
		ChangeType: models.ChangeTypeModify,
		OldHash:    "hash-base-backfill",
		NewHash:    oldHash,
		Author:     "tester",
		Message:    "prepare history",
		Timestamp:  now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("failed to add previous change: %v", err)
	}
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:         "chg-backfill-source",
		SliceID:    slice.ID,
		CommitHash: sourceCommit,
		Path:       filePath,
		ChangeType: models.ChangeTypeModify,
		OldHash:    "",
		NewHash:    newHash,
		Author:     "tester",
		Message:    "source with missing old hash",
		Timestamp:  now,
	}); err != nil {
		t.Fatalf("failed to add source change: %v", err)
	}

	srv := newSliceServiceServer(st)
	createResp, err := srv.RevertCommitChange(ctx, &slicev1.RevertCommitChangeRequest{
		CommitHash: sourceCommit,
		SliceId:    slice.ID,
	})
	if err != nil {
		t.Fatalf("RevertCommitChange failed: %v", err)
	}

	mergeResp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", mergeResp.GetStatus())
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedPromotions(waitCtx); err != nil {
		t.Fatalf("timed out waiting for root promotion queue: %v", err)
	}

	fileAfterMerge, err := storage.ReadSliceFileContent(ctx, st, slice.ID, filePath)
	if err != nil {
		t.Fatalf("failed to load file after merge: %v", err)
	}
	if string(fileAfterMerge.Content) != string(oldContent) {
		t.Fatalf("expected reverted content %q, got %q", string(oldContent), string(fileAfterMerge.Content))
	}
	if fileAfterMerge.Hash != oldHash {
		t.Fatalf("expected reverted hash %q, got %q", oldHash, fileAfterMerge.Hash)
	}
}
