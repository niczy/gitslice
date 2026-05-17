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
	fileservice "github.com/niczy/gitslice/services/file"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func adminAuthContextForUser(t testing.TB, st storage.Storage, username string) context.Context {
	t.Helper()
	email := strings.ToLower(strings.TrimSpace(username)) + "@example.com"
	t.Setenv("ADMIN_USER_EMAILS", email)
	if err := st.CreateUser(context.Background(), &models.User{
		Username:     username,
		PrimaryEmail: email,
		RootPath:     username,
	}); err != nil && err != storage.ErrEntryExists {
		t.Fatalf("CreateUser(%s) failed: %v", username, err)
	}
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User "+username))
}

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

func mustCreateChangesetSnapshot(tb testing.TB, ctx context.Context, srv *sliceServiceServer, cs *models.Changeset) {
	tb.Helper()
	if err := srv.createChangesetSnapshot(ctx, cs); err != nil {
		tb.Fatalf("createChangesetSnapshot(%s) failed: %v", cs.ID, err)
	}
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

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func projectionStatusByName(values []*slicev1.ProjectionStatus) map[string]*slicev1.ProjectionStatus {
	out := make(map[string]*slicev1.ProjectionStatus, len(values))
	for _, value := range values {
		if value != nil {
			out[value.GetProjectionName()] = value
		}
	}
	return out
}

func listEntriesContainPath(entries []*filev1.DirectoryEntry, target string) bool {
	for _, entry := range entries {
		if entry.GetPath() == target {
			return true
		}
	}
	return false
}

func listEntryPaths(entries []*filev1.DirectoryEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.GetPath())
	}
	return paths
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

type changesetSnapshotStreamRecorder struct {
	ctx    context.Context
	chunks []*slicev1.ChangesetSnapshotChunk
}

func (r *changesetSnapshotStreamRecorder) Context() context.Context {
	if r.ctx != nil {
		return r.ctx
	}
	return context.Background()
}

func (r *changesetSnapshotStreamRecorder) Send(chunk *slicev1.ChangesetSnapshotChunk) error {
	r.chunks = append(r.chunks, chunk)
	return nil
}

func (r *changesetSnapshotStreamRecorder) SetHeader(metadata.MD) error  { return nil }
func (r *changesetSnapshotStreamRecorder) SendHeader(metadata.MD) error { return nil }
func (r *changesetSnapshotStreamRecorder) SetTrailer(metadata.MD)       {}
func (r *changesetSnapshotStreamRecorder) SendMsg(any) error            { return nil }
func (r *changesetSnapshotStreamRecorder) RecvMsg(any) error            { return nil }

var _ slicev1.SliceService_StreamChangesetSnapshotServer = (*changesetSnapshotStreamRecorder)(nil)
var _ grpc.ServerStream = (*changesetSnapshotStreamRecorder)(nil)

func mergeChangesetSnapshotManifestChunks(chunks []*slicev1.ChangesetSnapshotChunk) *slicev1.ChangesetSnapshotManifest {
	out := &slicev1.ChangesetSnapshotManifest{}
	for _, chunk := range chunks {
		manifest := chunk.GetManifest()
		if manifest == nil {
			continue
		}
		if out.Snapshot == nil {
			out.Snapshot = manifest.GetSnapshot()
		}
		if out.SliceId == "" {
			out.SliceId = manifest.GetSliceId()
		}
		out.FileMetadata = append(out.FileMetadata, manifest.GetFileMetadata()...)
		out.DeletedPaths = append(out.DeletedPaths, manifest.GetDeletedPaths()...)
	}
	return out
}

func countChangesetSnapshotPayloadChunks(chunks []*slicev1.ChangesetSnapshotChunk) int {
	count := 0
	for _, chunk := range chunks {
		switch chunk.GetChunk().(type) {
		case *slicev1.ChangesetSnapshotChunk_Block, *slicev1.ChangesetSnapshotChunk_File:
			count++
		}
	}
	return count
}

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

func (s *blockingLockStorage) NextMergeEventSequence(ctx context.Context, shardID int32) (int64, error) {
	return forwardNextMergeEventSequence(ctx, s.Storage, shardID)
}

func (s *blockingLockStorage) AppendMergeEvent(ctx context.Context, event *models.MergeEvent) error {
	return forwardAppendMergeEvent(ctx, s.Storage, event)
}

func (s *blockingLockStorage) AppendMergeEventWithPathHeadCAS(ctx context.Context, event *models.MergeEvent) error {
	return forwardAppendMergeEventWithPathHeadCAS(ctx, s.Storage, event)
}

func (s *blockingLockStorage) GetMergeEventByChangeset(ctx context.Context, changesetID string) (*models.MergeEvent, error) {
	return forwardGetMergeEventByChangeset(ctx, s.Storage, changesetID)
}

func (s *blockingLockStorage) ListMergeEvents(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*models.MergeEvent, error) {
	return forwardListMergeEvents(ctx, s.Storage, shardID, afterSeq, limit)
}

func (s *blockingLockStorage) UpdateProjectionOffset(ctx context.Context, offset *models.ProjectionOffset) error {
	return forwardUpdateProjectionOffset(ctx, s.Storage, offset)
}

func (s *blockingLockStorage) GetProjectionOffset(ctx context.Context, projectionName string, shardID int32) (*models.ProjectionOffset, error) {
	return forwardGetProjectionOffset(ctx, s.Storage, projectionName, shardID)
}

func (s *blockingLockStorage) UpsertHomePathHeads(ctx context.Context, heads []*models.HomePathHead) error {
	return forwardUpsertHomePathHeads(ctx, s.Storage, heads)
}

func (s *blockingLockStorage) GetHomePathHeads(ctx context.Context, homeID string, paths []string) (map[string]*models.HomePathHead, error) {
	return forwardGetHomePathHeads(ctx, s.Storage, homeID, paths)
}

func (s *blockingLockStorage) ListHomePathHeads(ctx context.Context, homeID string, limit int) ([]*models.HomePathHead, error) {
	return forwardListHomePathHeads(ctx, s.Storage, homeID, limit)
}

func (s *blockingLockStorage) BackfillHomePathHeads(ctx context.Context, homeID string) (*models.HomePathHeadBackfillResult, error) {
	return forwardBackfillHomePathHeads(ctx, s.Storage, homeID)
}

func (s *blockingLockStorage) ValidateHomePathHeads(ctx context.Context, homeID string) (*models.HomePathHeadValidationResult, error) {
	return forwardValidateHomePathHeads(ctx, s.Storage, homeID)
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

func TestCheckoutRootSliceRequiresAuth(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	const path = "o/genesis/projects/repo/main.go"
	if err := st.AddFileToSlice(ctx, path, "root"); err != nil {
		t.Fatalf("failed to add root file: %v", err)
	}

	srv := newSliceServiceServer(st)
	_, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{SliceId: "root"})
	if err == nil {
		t.Fatal("expected Unauthenticated for anonymous root slice checkout, got nil")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", status.Code(err))
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

func TestGetSliceCommitsAllowsAnonymousPublicSlice(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	slice := &models.Slice{
		ID:         "slice-public-commits",
		Name:       "slice-public-commits",
		Owners:     []string{"tester"},
		CreatedBy:  "tester",
		Visibility: models.VisibilityPublic,
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, slice.ID, &models.Commit{
		CommitHash: "commit-public",
		Message:    "public commit",
		Timestamp:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}

	svc := newSliceServiceServer(st)
	resp, err := svc.GetSliceCommits(context.Background(), &slicev1.CommitHistoryRequest{
		SliceId: slice.ID,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("GetSliceCommits failed: %v", err)
	}
	if got, want := len(resp.GetCommits()), 1; got != want {
		t.Fatalf("len(commits) = %d, want %d", got, want)
	}
	if got, want := resp.GetCommits()[0].GetCommitHash(), "commit-public"; got != want {
		t.Fatalf("commit hash = %q, want %q", got, want)
	}
}

func TestGetSliceCommitsRespectsStateToken(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	homeSliceID := homeslice.IDForUsername("alice")
	slice := &models.Slice{ID: homeSliceID, Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := newSliceServiceServer(st)
	first, err := svc.CreateAndMergeChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       homeSliceID,
		ModifiedFiles: []string{"alice/app/first.go"},
		Message:       "first edit",
		FileContents: []*slicev1.FileContentChange{{
			Path:    "alice/app/first.go",
			Content: []byte("package app\n"),
		}},
	})
	if err != nil {
		t.Fatalf("first CreateAndMergeChangeset failed: %v", err)
	}
	if first.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("first merge status = %v, want success", first.GetStatus())
	}
	second, err := svc.CreateAndMergeChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       homeSliceID,
		ModifiedFiles: []string{"alice/app/second.go"},
		Message:       "second edit",
		FileContents: []*slicev1.FileContentChange{{
			Path:    "alice/app/second.go",
			Content: []byte("package app\n"),
		}},
	})
	if err != nil {
		t.Fatalf("second CreateAndMergeChangeset failed: %v", err)
	}
	if second.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("second merge status = %v, want success", second.GetStatus())
	}

	token := &filev1.SliceStateToken{
		SliceId: homeSliceID,
		Cursors: []*filev1.StateCursor{{
			HomeId:     "alice",
			MergeShard: first.GetMergeShard(),
			MergeSeq:   first.GetMergeSeq(),
		}},
	}
	tokenResp, err := svc.GetSliceCommits(ctx, &slicev1.CommitHistoryRequest{
		SliceId:    homeSliceID,
		Limit:      10,
		StateToken: token,
	})
	if err != nil {
		t.Fatalf("GetSliceCommits with token failed: %v", err)
	}
	assertCommitInfoHashes(t, tokenResp.GetCommits(), []string{first.GetNewCommitHash()})
	if tokenResp.GetStateToken().GetCursors()[0].GetMergeSeq() != first.GetMergeSeq() {
		t.Fatalf("response did not echo requested state token: %#v", tokenResp.GetStateToken())
	}

	currentTokenResp, err := svc.GetSliceCommits(ctx, &slicev1.CommitHistoryRequest{
		SliceId: homeSliceID,
		Limit:   10,
		StateToken: &filev1.SliceStateToken{
			SliceId: homeSliceID,
			Cursors: []*filev1.StateCursor{{
				HomeId:     "alice",
				MergeShard: second.GetMergeShard(),
				MergeSeq:   second.GetMergeSeq(),
			}},
		},
	})
	if err != nil {
		t.Fatalf("GetSliceCommits with current token failed: %v", err)
	}
	assertCommitInfoHashes(t, currentTokenResp.GetCommits(), []string{second.GetNewCommitHash(), first.GetNewCommitHash()})
}

func TestGetSliceCommitsIncludesOverlappingFolderCommits(t *testing.T) {
	st := storage.NewInMemoryStorage()
	ctx := adminAuthContextForUser(t, st, "tester")
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}
	home, err := homeslice.EnsureUserHomeSlice(ctx, st, "tester")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}
	root, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	now := time.Now().UTC()
	sliceA := &models.Slice{
		ID:          "slice-overlap-a",
		Name:        "slice-overlap-a",
		Owners:      []string{"tester"},
		CreatedBy:   "tester",
		ParentSlice: root.ID,
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "tester/X", Alias: "X"},
			{SourcePath: "tester/Y", Alias: "Y"},
		},
	}
	sliceB := &models.Slice{
		ID:          "slice-overlap-b",
		Name:        "slice-overlap-b",
		Owners:      []string{"tester"},
		CreatedBy:   "tester",
		ParentSlice: root.ID,
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "tester/X", Alias: "X"},
			{SourcePath: "tester/Z", Alias: "Z"},
		},
	}
	if err := st.CreateSlice(ctx, sliceA); err != nil {
		t.Fatalf("CreateSlice A failed: %v", err)
	}
	if err := st.CreateSlice(ctx, sliceB); err != nil {
		t.Fatalf("CreateSlice B failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, sliceA.ID, &models.Commit{
		CommitHash: "commit-a-initial",
		Timestamp:  now.Add(-2 * time.Minute),
		Message:    "create slice A from X and Y",
	}); err != nil {
		t.Fatalf("AddSliceCommit initial A failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, sliceB.ID, &models.Commit{
		CommitHash: "commit-b-initial",
		Timestamp:  now.Add(-2 * time.Minute),
		Message:    "create slice B from X and Z",
	}); err != nil {
		t.Fatalf("AddSliceCommit initial B failed: %v", err)
	}

	sharedDisplayPath := "X/shared.txt"
	sharedSourcePath := "tester/X/shared.txt"
	newHash := mustWriteSliceManifest(t, ctx, st, home.ID, sharedSourcePath, []byte("slice A edit\n"))
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: "commit-a-x-change",
		SliceID:    sliceA.ID,
		Files:      map[string]string{sharedDisplayPath: newHash},
		Timestamp:  now,
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot A change failed: %v", err)
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: "commit-a-x-change",
		SliceID:    home.ID,
		Files:      map[string]string{sharedSourcePath: newHash},
		Timestamp:  now,
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot home change failed: %v", err)
	}
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:         common.GenerateFileChangeID("commit-a-x-change", sharedDisplayPath),
		SliceID:    sliceA.ID,
		CommitHash: "commit-a-x-change",
		Path:       sharedDisplayPath,
		ChangeType: models.ChangeTypeModify,
		NewHash:    newHash,
		Author:     "tester",
		Message:    "update shared folder X in slice A",
		Timestamp:  now,
	}); err != nil {
		t.Fatalf("AddFileChange A shared path failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, sliceA.ID, &models.Commit{
		CommitHash: "commit-a-x-change",
		ParentHash: "commit-a-initial",
		Timestamp:  now,
		Message:    "update shared folder X in slice A",
	}); err != nil {
		t.Fatalf("AddSliceCommit A shared path failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, home.ID, &models.Commit{
		CommitHash: "commit-a-x-change",
		ParentHash: "commit-a-initial",
		Timestamp:  now,
		Message:    "update shared folder X in slice A",
	}); err != nil {
		t.Fatalf("AddSliceCommit home shared path failed: %v", err)
	}
	appendMergeEventForTest(t, ctx, st, "tester", "chg-a-x-change", sliceA.ID, "commit-a-x-change", "commit-a-initial", sharedSourcePath, now, "update shared folder X in slice A")

	yCommitTime := now.Add(time.Minute)
	yDisplayPath := "Y/only-a.txt"
	ySourcePath := "tester/Y/only-a.txt"
	yHash := mustWriteSliceManifest(t, ctx, st, home.ID, ySourcePath, []byte("slice A Y edit\n"))
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:         common.GenerateFileChangeID("commit-a-y-change", yDisplayPath),
		SliceID:    sliceA.ID,
		CommitHash: "commit-a-y-change",
		Path:       yDisplayPath,
		ChangeType: models.ChangeTypeModify,
		NewHash:    yHash,
		Author:     "tester",
		Message:    "update folder Y in slice A",
		Timestamp:  yCommitTime,
	}); err != nil {
		t.Fatalf("AddFileChange A Y path failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, sliceA.ID, &models.Commit{
		CommitHash: "commit-a-y-change",
		ParentHash: "commit-a-x-change",
		Timestamp:  yCommitTime,
		Message:    "update folder Y in slice A",
	}); err != nil {
		t.Fatalf("AddSliceCommit A Y path failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, home.ID, &models.Commit{
		CommitHash: "commit-a-y-change",
		ParentHash: "commit-a-x-change",
		Timestamp:  yCommitTime,
		Message:    "update folder Y in slice A",
	}); err != nil {
		t.Fatalf("AddSliceCommit home Y path failed: %v", err)
	}
	appendMergeEventForTest(t, ctx, st, "tester", "chg-a-y-change", sliceA.ID, "commit-a-y-change", "commit-a-x-change", ySourcePath, yCommitTime, "update folder Y in slice A")

	svc := newSliceServiceServer(st)
	aResp, err := svc.GetSliceCommits(ctx, &slicev1.CommitHistoryRequest{SliceId: sliceA.ID, Limit: 10})
	if err != nil {
		t.Fatalf("GetSliceCommits A failed: %v", err)
	}
	bResp, err := svc.GetSliceCommits(ctx, &slicev1.CommitHistoryRequest{SliceId: sliceB.ID, Limit: 10})
	if err != nil {
		t.Fatalf("GetSliceCommits B failed: %v", err)
	}
	if !commitHistoryContains(aResp.GetCommits(), "commit-a-x-change") {
		t.Fatalf("slice A commit list should include its X change, got %#v", aResp.GetCommits())
	}
	if !commitHistoryContains(bResp.GetCommits(), "commit-a-x-change") {
		t.Fatalf("slice B commit list should include slice A's X change because folder X is mounted, got %#v", bResp.GetCommits())
	}
	if commitHistoryContains(bResp.GetCommits(), "commit-a-y-change") {
		t.Fatalf("slice B commit list should not include slice A's Y-only change, got %#v", bResp.GetCommits())
	}
}

func commitHistoryContains(commits []*slicev1.CommitInfo, hash string) bool {
	for _, commit := range commits {
		if commit.GetCommitHash() == hash {
			return true
		}
	}
	return false
}

func assertCommitInfoHashes(t *testing.T, commits []*slicev1.CommitInfo, want []string) {
	t.Helper()
	got := make([]string, 0, len(commits))
	for _, commit := range commits {
		got = append(got, commit.GetCommitHash())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("commit hashes = %#v, want %#v", got, want)
	}
}

func appendMergeEventForTest(t *testing.T, ctx context.Context, st storage.MergeEventStore, homeID, changesetID, sourceSliceID, sourceCommitHash, parentCommitHash, filePath string, createdAt time.Time, message string) {
	t.Helper()
	shardID := mergeEventShardID(homeID)
	seq, err := st.NextMergeEventSequence(ctx, shardID)
	if err != nil {
		t.Fatalf("NextMergeEventSequence failed: %v", err)
	}
	event := &models.MergeEvent{
		HomeID:           homeID,
		ShardID:          shardID,
		MergeSeq:         seq,
		EventID:          common.GenerateMergeEventID(),
		ChangesetID:      changesetID,
		SourceSliceID:    sourceSliceID,
		SourceCommitHash: sourceCommitHash,
		Author:           "tester",
		Message:          message,
		TouchedPaths:     []string{filePath},
		PathUpdates: []*models.MergePathUpdate{{
			Path:             filePath,
			SourceSliceID:    sourceSliceID,
			SourceCommitHash: sourceCommitHash,
			ParentCommitHash: parentCommitHash,
			NewVersion:       seq,
			ManifestHash:     sourceCommitHash,
		}},
		CreatedAt: createdAt,
	}
	if err := st.AppendMergeEvent(ctx, event); err != nil {
		t.Fatalf("AppendMergeEvent failed: %v", err)
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

func TestCheckoutHomeSliceIgnoresEntriesOutsideHomeRoot(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User nicholas"))
	st := storage.NewInMemoryStorage()

	sliceID := homeslice.IDForUsername("nicholas")
	slice := &models.Slice{
		ID:        sliceID,
		Name:      "nicholas",
		Owners:    []string{"nicholas"},
		CreatedBy: "nicholas",
		Files:     []string{"nicholas/inside.txt", "outside.txt"},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       sliceID + ":outside.txt",
		Path:     "outside.txt",
		Type:     "file",
		ParentID: sliceID,
	}); err != nil {
		t.Fatalf("AddEntry(outside) failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       sliceID + ":nicholas",
		Path:     "nicholas",
		Type:     "directory",
		ParentID: sliceID,
	}); err != nil {
		t.Fatalf("AddEntry(home dir) failed: %v", err)
	}
	content := []byte("inside\n")
	hash := mustWriteSliceManifest(t, ctx, st, sliceID, "nicholas/inside.txt", content)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       sliceID + ":nicholas/inside.txt",
		Path:     "nicholas/inside.txt",
		Type:     "file",
		ParentID: sliceID + ":nicholas",
		Size:     int64(len(content)),
		Hash:     hash,
	}); err != nil {
		t.Fatalf("AddEntry(inside) failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{SliceId: sliceID})
	if err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}

	metadata := resp.GetManifest().GetFileMetadata()
	if got, want := len(metadata), 1; got != want {
		t.Fatalf("expected %d file metadata entries, got %d: %#v", want, got, metadata)
	}
	if got, want := metadata[0].GetFileId(), "nicholas/inside.txt"; got != want {
		t.Fatalf("metadata file id = %q, want %q", got, want)
	}
	if got := string(mustAssembleCheckoutContent(t, resp, metadata[0])); got != string(content) {
		t.Fatalf("checkout content = %q, want %q", got, string(content))
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

func TestCheckoutSliceSkipsFilesWithMissingDirectContent(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-checkout-dirty-metadata", Name: "slice-checkout-dirty-metadata", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	goodPath := "docs/good.txt"
	goodContent := []byte("good\n")
	goodHash := mustWriteSliceManifest(t, ctx, st, slice.ID, goodPath, goodContent)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + goodPath,
		Path:     goodPath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(goodContent)),
		Hash:     goodHash,
	}); err != nil {
		t.Fatalf("AddEntry good failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, goodPath, slice.ID); err != nil {
		t.Fatalf("AddFileToSlice good failed: %v", err)
	}

	missingPath := "docs/missing.txt"
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + missingPath,
		Path:     missingPath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len("missing\n")),
		Hash:     "missing-hash",
	}); err != nil {
		t.Fatalf("AddEntry missing failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, missingPath, slice.ID); err != nil {
		t.Fatalf("AddFileToSlice missing failed: %v", err)
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
	if _, ok := byPath[missingPath]; ok {
		t.Fatalf("checkout included missing file metadata: %#v", resp.GetManifest().GetFileMetadata())
	}
	if got := string(mustAssembleCheckoutContent(t, resp, byPath[goodPath])); got != string(goodContent) {
		t.Fatalf("checkout good content = %q, want %q", got, string(goodContent))
	}

	recorder := &checkoutStreamRecorder{ctx: ctx}
	if err := srv.StreamCheckoutSlice(&slicev1.CheckoutRequest{SliceId: slice.ID}, recorder); err != nil {
		t.Fatalf("StreamCheckoutSlice failed: %v", err)
	}
	for _, chunk := range recorder.chunks {
		for _, meta := range chunk.GetManifest().GetFileMetadata() {
			if meta.GetPath() == missingPath {
				t.Fatalf("stream checkout included missing file metadata: %#v", chunk.GetManifest().GetFileMetadata())
			}
		}
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
	profile := newMergeProfile("chg_123", "slice-123", 256)
	profile.markRevertApply(15 * time.Millisecond)
	profile.markFinalize(230 * time.Millisecond)
	profile.markProjection(40 * time.Millisecond)
	profile.markConfig(5 * time.Millisecond)
	profile.finish()

	summary := profile.summary()
	for _, want := range []string{
		"changeset_id=chg_123",
		"slice_id=slice-123",
		"modified_files=256",
		"revert_ms=15",
		"finalize_ms=230",
		"projection_ms=40",
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
			ID:            "chg_pending",
			SliceID:       slice.ID,
			Status:        models.ChangesetStatusPending,
			ModifiedFiles: []string{"file1"},
			CreatedAt:     now,
		},
		{
			ID:            "chg_merged",
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
		if resp.Changesets[0].ChangesetId != "chg_pending" {
			t.Fatalf("expected chg_pending, got %s", resp.Changesets[0].ChangesetId)
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
		if resp.Changesets[0].ChangesetId != "chg_merged" {
			t.Fatalf("expected chg_merged, got %s", resp.Changesets[0].ChangesetId)
		}
	})
}

func TestListChangesetsAllowsAnonymousPublicSlice(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:         "slice-public-changesets",
		Name:       "slice-public-changesets",
		Owners:     []string{"tester"},
		CreatedBy:  "tester",
		Visibility: models.VisibilityPublic,
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.CreateChangeset(ctx, &models.Changeset{
		ID:            "chg_public",
		SliceID:       slice.ID,
		Status:        models.ChangesetStatusPending,
		ModifiedFiles: []string{"README.md"},
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.ListChangesets(context.Background(), &slicev1.ListChangesetsRequest{
		SliceId:      slice.ID,
		StatusFilter: statusFilterAll,
	})
	if err != nil {
		t.Fatalf("ListChangesets failed: %v", err)
	}
	if got, want := len(resp.GetChangesets()), 1; got != want {
		t.Fatalf("len(changesets) = %d, want %d", got, want)
	}
	if got, want := resp.GetChangesets()[0].GetChangesetId(), "chg_public"; got != want {
		t.Fatalf("changeset id = %q, want %q", got, want)
	}
}

func TestListChangesetsRequiresLoginForAnonymousPrivateSlice(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-private-changesets", Name: "slice-private-changesets", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	_, err := srv.ListChangesets(context.Background(), &slicev1.ListChangesetsRequest{
		SliceId:      slice.ID,
		StatusFilter: statusFilterAll,
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("ListChangesets error = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}

func TestListChangesetsIncludesProactiveReviewStatus(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	sliceA := &models.Slice{ID: "slice-list-review-a", Name: "slice-list-review-a", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, sliceA); err != nil {
		t.Fatalf("failed to create slice A: %v", err)
	}

	writePath := func(filePath, content string, version int64) {
		t.Helper()
		hash := mustWriteSliceManifest(t, ctx, st, sliceA.ID, filePath, []byte(content))
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(sliceA.ID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: sliceA.ID,
			Hash:     hash,
			Size:     int64(len(content)),
		}); err != nil {
			t.Fatalf("failed to add entry for %s: %v", filePath, err)
		}
		if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
			HomeID:       "tester",
			Path:         filePath,
			PathVersion:  version,
			ManifestHash: hash,
			ContentHash:  hash,
		}}); err != nil {
			t.Fatalf("failed to seed path head for %s: %v", filePath, err)
		}
	}
	writePath("tester/ready.txt", "ready\n", 1)
	writePath("tester/stale.txt", "stale\n", 1)

	now := time.Now()
	srv := newSliceServiceServer(st)
	for _, cs := range []*models.Changeset{
		{
			ID:            "chg_ready-list",
			SliceID:       sliceA.ID,
			ModifiedFiles: []string{"tester/ready.txt"},
			Status:        models.ChangesetStatusPending,
			CreatedAt:     now,
		},
		{
			ID:            "chg_stale-list",
			SliceID:       sliceA.ID,
			ModifiedFiles: []string{"tester/stale.txt"},
			Status:        models.ChangesetStatusPending,
			CreatedAt:     now.Add(time.Second),
		},
	} {
		if err := st.CreateChangeset(ctx, cs); err != nil {
			t.Fatalf("failed to create changeset %s: %v", cs.ID, err)
		}
		if err := srv.createChangesetSnapshot(ctx, cs); err != nil {
			t.Fatalf("createChangesetSnapshot(%s) failed: %v", cs.ID, err)
		}
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:      "tester",
		Path:        "tester/stale.txt",
		PathVersion: 2,
	}}); err != nil {
		t.Fatalf("failed to advance stale path head: %v", err)
	}

	resp, err := srv.ListChangesets(ctx, &slicev1.ListChangesetsRequest{SliceId: sliceA.ID, StatusFilter: slicev1.ChangesetStatus_PENDING})
	if err != nil {
		t.Fatalf("ListChangesets failed: %v", err)
	}

	got := make(map[string]slicev1.ReviewStatus)
	for _, cs := range resp.GetChangesets() {
		got[cs.GetChangesetId()] = cs.GetReviewStatus()
	}
	if got["chg_ready-list"] != slicev1.ReviewStatus_READY_FOR_MERGE {
		t.Fatalf("ready review status = %v", got["chg_ready-list"])
	}
	if got["chg_stale-list"] != slicev1.ReviewStatus_NEEDS_SYNC {
		t.Fatalf("stale review status = %v", got["chg_stale-list"])
	}
}

func TestCreateSliceAutoGeneratesID(t *testing.T) {
	st := storage.NewInMemoryStorage()
	ctx := adminAuthContextForUser(t, st, "tester")
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	if err := st.AddFileToSlice(ctx, "tester/o/genesis/projects/repo/main.go", "root"); err != nil {
		t.Fatalf("failed to add root file: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root",
		FolderPaths:   []string{"o/genesis/projects/repo"},
		Name:          "my-slice",
		Description:   "auto id test",
	})
	if err != nil {
		t.Fatalf("CreateSliceFromFolder failed: %v", err)
	}

	if !strings.HasPrefix(resp.SliceId, "sl_") {
		t.Fatalf("expected auto-generated ID with sl_ prefix, got %q", resp.SliceId)
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

func TestHomeSliceExternalSlugUsesUsername(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	home, err := homeslice.EnsureUserHomeSlice(ctx, st, "alice")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.GetSliceBySlug(ctx, &slicev1.GetSliceBySlugRequest{Slug: "alice"})
	if err != nil {
		t.Fatalf("GetSliceBySlug failed: %v", err)
	}
	if resp.GetSliceId() != home.ID {
		t.Fatalf("expected home slice %q, got %q", home.ID, resp.GetSliceId())
	}
	if resp.GetSlug() != "alice" {
		t.Fatalf("expected home slice slug alice, got %q", resp.GetSlug())
	}
}

func TestCreateSliceUsesFolderPathsAsDefaultName(t *testing.T) {
	st := storage.NewInMemoryStorage()
	ctx := adminAuthContextForUser(t, st, "tester")
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	if err := st.AddFileToSlice(ctx, "tester/org/project/service/main.go", "root"); err != nil {
		t.Fatalf("failed to add root file: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root",
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

func TestCreateSliceFromRootScopesRelativeFolderPathsToUserHome(t *testing.T) {
	st := storage.NewInMemoryStorage()
	ctx := adminAuthContextForUser(t, st, "tester")
	if _, err := homeslice.EnsureUserHomeSlice(ctx, st, "tester"); err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}
	root, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}

	filePath := "tester/shared/README.md"
	hash := mustWriteSliceManifest(t, ctx, st, root.ID, filePath, []byte("home readme"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(root.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: common.GenerateEntryID(root.ID, "tester/shared"),
		Size:     int64(len("home readme")),
		Hash:     hash,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: root.ID,
		NewSliceId:    "root-relative-slice",
		FolderPaths:   []string{"shared"},
		Name:          "Shared",
	})
	if err != nil {
		t.Fatalf("CreateSliceFromFolder failed: %v", err)
	}
	if got, want := resp.GetFiles(), []string{filePath}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected selected files %v, got %v", want, got)
	}

	slice, err := st.GetSlice(ctx, resp.GetSliceId())
	if err != nil {
		t.Fatalf("GetSlice failed: %v", err)
	}
	if got, want := slice.ParentSlice, root.ID; got != want {
		t.Fatalf("parent slice = %q, want %q", got, want)
	}
	if got, want := slice.FolderMounts, []models.SliceFolderMount{{SourcePath: "tester/shared", Alias: "shared"}}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("folder mounts = %#v, want %#v", got, want)
	}
}

func TestCreateSliceFromFolderRejectsMissingFolder(t *testing.T) {
	st := storage.NewInMemoryStorage()
	ctx := adminAuthContextForUser(t, st, "tester")
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	if err := st.AddFileToSlice(ctx, "docs/README.md", "root"); err != nil {
		t.Fatalf("failed to add root file: %v", err)
	}

	srv := newSliceServiceServer(st)
	_, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root",
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
	st := storage.NewInMemoryStorage()
	ctx := adminAuthContextForUser(t, st, "tester")
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	filePath := "tester/o/genesis/projects/repo/README.md"
	displayPath := "o/genesis/projects/repo/README.md"
	content := []byte("repo readme")
	source := &models.Slice{ID: "source-entry-backed-root", Name: "source-entry-backed-root", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, source); err != nil {
		t.Fatalf("CreateSlice(source) failed: %v", err)
	}
	hash := mustWriteSliceManifest(t, ctx, st, source.ID, filePath, content)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(source.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: source.ID,
		Size:     int64(len(content)),
		Hash:     hash,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           "tester",
		Path:             filePath,
		EntryType:        "file",
		PathVersion:      1,
		SourceSliceID:    source.ID,
		SourceCommitHash: "source-entry-backed-root-commit",
		ManifestHash:     hash,
		ContentHash:      hash,
	}}); err != nil {
		t.Fatalf("UpsertHomePathHeads failed: %v", err)
	}

	rootSlice, err := st.GetSlice(ctx, "root")
	if err != nil {
		t.Fatalf("failed to load root slice: %v", err)
	}
	if got := len(rootSlice.Files); got != 0 {
		t.Fatalf("expected prod-like empty root file index, got %d entries", got)
	}

	srv := NewService(st)
	createResp, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root",
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
	if got, want := meta.GetPath(), displayPath; got != want {
		t.Fatalf("expected checkout path %q, got %q", want, got)
	}
	if got, want := meta.GetSize(), int64(len(content)); got != want {
		t.Fatalf("expected checkout size %d, got %d", want, got)
	}
	if got := string(mustAssembleCheckoutContent(t, checkoutResp, meta)); got != string(content) {
		t.Fatalf("expected checkout content %q, got %q", string(content), got)
	}

	childMeta, err := st.GetSliceMetadata(ctx, createResp.GetSliceId())
	if err != nil {
		t.Fatalf("GetSliceMetadata child failed: %v", err)
	}
	childCommits, err := st.ListSliceCommits(ctx, createResp.GetSliceId(), 10, "")
	if err != nil {
		t.Fatalf("ListSliceCommits child failed: %v", err)
	}
	if len(childCommits) != 1 || childCommits[0].CommitHash != childMeta.HeadCommitHash {
		t.Fatalf("expected initial child commit %q, got %#v", childMeta.HeadCommitHash, childCommits)
	}
	childSnapshot, err := st.GetCommitSnapshot(ctx, childMeta.HeadCommitHash)
	if err != nil {
		t.Fatalf("GetCommitSnapshot child failed: %v", err)
	}
	if got := strings.TrimSpace(childSnapshot.Files[displayPath]); got == "" {
		t.Fatalf("expected child snapshot to include parent manifest hash for %s", displayPath)
	}
	if _, err := storage.BuildSliceSearchArtifact(ctx, st, createResp.GetSliceId(), childMeta.HeadCommitHash); err != nil {
		t.Fatalf("BuildSliceSearchArtifact child failed: %v", err)
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
	st := storage.NewInMemoryStorage()
	ctx := adminAuthContextForUser(t, st, "tester")
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	writeBackingFile := func(filePath, content string) {
		t.Helper()
		hash := mustWriteSliceManifest(t, ctx, st, "root", filePath, []byte(content))
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       "root:" + filePath,
			Path:     filePath,
			Type:     "file",
			ParentID: "root",
			Size:     int64(len(content)),
			Hash:     hash,
		}); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", filePath, err)
		}
		if err := st.AddFileToSlice(ctx, filePath, "root"); err != nil {
			t.Fatalf("AddFileToSlice(%s) failed: %v", filePath, err)
		}
	}

	writeBackingFile("tester/shared/README.md", "old readme")
	writeBackingFile("tester/other/secret.txt", "secret")

	srv := NewService(st)
	for _, sliceID := range []string{"shared-a", "shared-b"} {
		if _, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
			ParentSliceId: "root",
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
	st := storage.NewInMemoryStorage()
	ctx := adminAuthContextForUser(t, st, "tester")
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	if _, err := storage.WriteSliceFileManifest(ctx, st, "root", "tester/legacy/README.md", []byte("legacy readme")); err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "tester/legacy/README.md", "root"); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	srv := NewService(st)
	if _, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root",
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

	slice := &models.Slice{ID: "sl_test-rename", Name: "old-name", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.RenameSlice(ctx, &slicev1.RenameSliceRequest{
		SliceId: "sl_test-rename",
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
	updated, err := st.GetSlice(ctx, "sl_test-rename")
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

	slice := &models.Slice{ID: "sl_delete-test", Name: "delete-me", Owners: []string{"tester"}, CreatedBy: "tester"}
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

	slice := &models.Slice{ID: "sl_delete-open", Name: "open-work", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	if err := st.CreateChangeset(ctx, &models.Changeset{
		ID:             "chg_open",
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

func TestArtifactLinksExposeAgentConversationAndMergeCommit(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	srv := NewService(st)

	now := time.Now().UTC()
	slice := &models.Slice{ID: "sl_artifact_links", Name: "artifact-links", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	mergedAt := now.Add(time.Minute)
	cs := &models.Changeset{
		ID:             "chg_artifact_links",
		Hash:           "hash-artifact-links",
		SliceID:        slice.ID,
		BaseCommitHash: "base-artifact-links",
		ModifiedFiles:  []string{"README.md"},
		Status:         models.ChangesetStatusMerged,
		Author:         "tester",
		Message:        "agent export",
		CreatedAt:      now,
		MergedAt:       &mergedAt,
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	snapshot := &models.ChangesetSnapshot{
		ID:             "snap_artifact_links",
		ChangesetID:    cs.ID,
		Version:        3,
		Hash:           "snapshot-artifact-links",
		BaseCommitHash: cs.BaseCommitHash,
		ModifiedFiles:  []string{"README.md"},
		Author:         "tester",
		Message:        "export",
		CreatedAt:      now,
	}
	if err := st.CreateChangesetSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("CreateChangesetSnapshot failed: %v", err)
	}
	session := &models.AgentSession{
		SessionID: "sess_artifact_links",
		SliceID:   slice.ID,
		RunnerID:  "runner_artifact_links",
		UserID:    "tester",
		State:     models.AgentSessionStateRunning,
		Provider:  "local",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := st.CreateAgentSession(ctx, session); err != nil {
		t.Fatalf("CreateAgentSession failed: %v", err)
	}
	if err := st.RecordAgentSessionChangeset(ctx, &models.AgentSessionChangeset{
		SessionID:       session.SessionID,
		ChangesetID:     cs.ID,
		SnapshotID:      snapshot.ID,
		SnapshotVersion: snapshot.Version,
		SnapshotHash:    snapshot.Hash,
		BaseCommitHash:  snapshot.BaseCommitHash,
		ExportedFromSeq: 42,
		RunnerID:        session.RunnerID,
		Source:          "local_export",
		ExportedAt:      now,
	}); err != nil {
		t.Fatalf("RecordAgentSessionChangeset failed: %v", err)
	}
	if err := st.AppendMergeEvent(ctx, &models.MergeEvent{
		HomeID:           "home-tester",
		ShardID:          1,
		MergeSeq:         1,
		EventID:          "me_artifact_links",
		ChangesetID:      cs.ID,
		SourceSliceID:    slice.ID,
		SourceCommitHash: "commit-artifact-links",
		Author:           "tester",
		Message:          "merge artifact links",
		TouchedPaths:     []string{"README.md"},
		PathUpdates: []*models.MergePathUpdate{{
			Path:             "README.md",
			BaseVersion:      1,
			NewVersion:       2,
			ContentHash:      "sha256:content-artifact-links",
			ManifestHash:     "sha256:manifest-artifact-links",
			SourceSliceID:    slice.ID,
			SourceCommitHash: "commit-artifact-links",
		}},
		CreatedAt: mergedAt,
	}); err != nil {
		t.Fatalf("AppendMergeEvent failed: %v", err)
	}

	changesetLinks, err := srv.GetChangesetArtifactLinks(ctx, &slicev1.GetChangesetArtifactLinksRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("GetChangesetArtifactLinks failed: %v", err)
	}
	if changesetLinks.GetChangeset().GetStatus() != slicev1.ChangesetStatus_MERGED {
		t.Fatalf("expected merged changeset status, got %v", changesetLinks.GetChangeset().GetStatus())
	}
	if changesetLinks.GetMerge().GetCommitHash() != "commit-artifact-links" {
		t.Fatalf("unexpected merge link: %#v", changesetLinks.GetMerge())
	}
	if got := changesetLinks.GetAgentSessions(); len(got) != 1 || got[0].GetSessionId() != session.SessionID || got[0].GetSliceId() != slice.ID {
		t.Fatalf("unexpected agent session links: %#v", got)
	}

	commitLinks, err := srv.GetCommitArtifactLinks(ctx, &slicev1.GetCommitArtifactLinksRequest{CommitHash: "commit-artifact-links"})
	if err != nil {
		t.Fatalf("GetCommitArtifactLinks failed: %v", err)
	}
	if commitLinks.GetChangeset().GetChangesetId() != cs.ID {
		t.Fatalf("unexpected commit changeset link: %#v", commitLinks.GetChangeset())
	}
	if got := commitLinks.GetAgentSessions(); len(got) != 1 || got[0].GetSessionId() != session.SessionID {
		t.Fatalf("unexpected commit agent session links: %#v", got)
	}
}

func TestGetSliceByName(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "sl_lookup-test", Name: "my-project", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.GetSliceByName(ctx, &slicev1.GetSliceByNameRequest{Name: "my-project"})
	if err != nil {
		t.Fatalf("GetSliceByName failed: %v", err)
	}

	if resp.SliceId != "sl_lookup-test" {
		t.Fatalf("expected ID %q, got %q", "sl_lookup-test", resp.SliceId)
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

	slice := &models.Slice{ID: "sl_slug-test", Name: "my-project", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.GetSliceBySlug(ctx, &slicev1.GetSliceBySlugRequest{Slug: "tester/my-project"})
	if err != nil {
		t.Fatalf("GetSliceBySlug failed: %v", err)
	}

	if resp.SliceId != "sl_slug-test" {
		t.Fatalf("expected ID %q, got %q", "sl_slug-test", resp.SliceId)
	}
	if resp.Slug != "tester/my-project" {
		t.Fatalf("expected slug %q, got %q", "tester/my-project", resp.Slug)
	}
}

func TestGetSliceBySlugUsesAuthenticatedOwnerNamespaceForLocalSlug(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	for _, slice := range []*models.Slice{
		{ID: "sl_slug-tester", Name: "my-project", Owners: []string{"tester"}, CreatedBy: "tester"},
		{ID: "sl_slug-other", Name: "my-project", Owners: []string{"other"}, CreatedBy: "other"},
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
	if got, want := resp.GetSliceId(), "sl_slug-tester"; got != want {
		t.Fatalf("slice id = %q, want %q", got, want)
	}
	if got, want := resp.GetSlug(), "tester/my-project"; got != want {
		t.Fatalf("slug = %q, want %q", got, want)
	}
}

func TestCreateSliceFromMultipleFoldersRemapsCheckoutPaths(t *testing.T) {
	st := storage.NewInMemoryStorage()
	ctx := adminAuthContextForUser(t, st, "tester")
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	seedFiles := map[string][]byte{
		"tester/o/genesis/projects/repo-a/README.md":   []byte("repo a"),
		"tester/o/genesis/projects/repo-a/pkg/util.go": []byte("package repoa"),
		"tester/o/genesis/projects/repo-b/main.go":     []byte("package main"),
	}
	for filePath, content := range seedFiles {
		if err := st.AddFileToSlice(ctx, filePath, "root"); err != nil {
			t.Fatalf("failed to add root file %s: %v", filePath, err)
		}
		mustWriteSliceManifest(t, ctx, st, "root", filePath, content)
	}

	srv := NewService(st)
	createResp, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root",
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
	if mounts["tester/o/genesis/projects/repo-a"] != "o/genesis/projects/repo-a" {
		t.Fatalf("unexpected alias for repo-a: %q", mounts["tester/o/genesis/projects/repo-a"])
	}
	if mounts["tester/o/genesis/projects/repo-b"] != "o/genesis/projects/repo-b" {
		t.Fatalf("unexpected alias for repo-b: %q", mounts["tester/o/genesis/projects/repo-b"])
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

func (c *addFileToSliceCounter) NextMergeEventSequence(ctx context.Context, shardID int32) (int64, error) {
	return forwardNextMergeEventSequence(ctx, c.Storage, shardID)
}

func (c *addFileToSliceCounter) AppendMergeEvent(ctx context.Context, event *models.MergeEvent) error {
	return forwardAppendMergeEvent(ctx, c.Storage, event)
}

func (c *addFileToSliceCounter) AppendMergeEventWithPathHeadCAS(ctx context.Context, event *models.MergeEvent) error {
	return forwardAppendMergeEventWithPathHeadCAS(ctx, c.Storage, event)
}

func (c *addFileToSliceCounter) GetMergeEventByChangeset(ctx context.Context, changesetID string) (*models.MergeEvent, error) {
	return forwardGetMergeEventByChangeset(ctx, c.Storage, changesetID)
}

func (c *addFileToSliceCounter) ListMergeEvents(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*models.MergeEvent, error) {
	return forwardListMergeEvents(ctx, c.Storage, shardID, afterSeq, limit)
}

func (c *addFileToSliceCounter) UpdateProjectionOffset(ctx context.Context, offset *models.ProjectionOffset) error {
	return forwardUpdateProjectionOffset(ctx, c.Storage, offset)
}

func (c *addFileToSliceCounter) GetProjectionOffset(ctx context.Context, projectionName string, shardID int32) (*models.ProjectionOffset, error) {
	return forwardGetProjectionOffset(ctx, c.Storage, projectionName, shardID)
}

func (c *addFileToSliceCounter) UpsertHomePathHeads(ctx context.Context, heads []*models.HomePathHead) error {
	return forwardUpsertHomePathHeads(ctx, c.Storage, heads)
}

func (c *addFileToSliceCounter) GetHomePathHeads(ctx context.Context, homeID string, paths []string) (map[string]*models.HomePathHead, error) {
	return forwardGetHomePathHeads(ctx, c.Storage, homeID, paths)
}

func (c *addFileToSliceCounter) ListHomePathHeads(ctx context.Context, homeID string, limit int) ([]*models.HomePathHead, error) {
	return forwardListHomePathHeads(ctx, c.Storage, homeID, limit)
}

func (c *addFileToSliceCounter) BackfillHomePathHeads(ctx context.Context, homeID string) (*models.HomePathHeadBackfillResult, error) {
	return forwardBackfillHomePathHeads(ctx, c.Storage, homeID)
}

func (c *addFileToSliceCounter) ValidateHomePathHeads(ctx context.Context, homeID string) (*models.HomePathHeadValidationResult, error) {
	return forwardValidateHomePathHeads(ctx, c.Storage, homeID)
}

func forwardNextMergeEventSequence(ctx context.Context, st storage.Storage, shardID int32) (int64, error) {
	events, ok := st.(storage.MergeEventStore)
	if !ok {
		return 0, storage.ErrInvalidInput
	}
	return events.NextMergeEventSequence(ctx, shardID)
}

func forwardAppendMergeEvent(ctx context.Context, st storage.Storage, event *models.MergeEvent) error {
	events, ok := st.(storage.MergeEventStore)
	if !ok {
		return storage.ErrInvalidInput
	}
	return events.AppendMergeEvent(ctx, event)
}

func forwardAppendMergeEventWithPathHeadCAS(ctx context.Context, st storage.Storage, event *models.MergeEvent) error {
	cas, ok := st.(storage.MergeEventPathHeadCASStore)
	if !ok {
		return storage.ErrInvalidInput
	}
	return cas.AppendMergeEventWithPathHeadCAS(ctx, event)
}

func forwardGetMergeEventByChangeset(ctx context.Context, st storage.Storage, changesetID string) (*models.MergeEvent, error) {
	events, ok := st.(storage.MergeEventStore)
	if !ok {
		return nil, storage.ErrInvalidInput
	}
	return events.GetMergeEventByChangeset(ctx, changesetID)
}

func forwardListMergeEvents(ctx context.Context, st storage.Storage, shardID int32, afterSeq int64, limit int) ([]*models.MergeEvent, error) {
	events, ok := st.(storage.MergeEventStore)
	if !ok {
		return nil, storage.ErrInvalidInput
	}
	return events.ListMergeEvents(ctx, shardID, afterSeq, limit)
}

func forwardUpdateProjectionOffset(ctx context.Context, st storage.Storage, offset *models.ProjectionOffset) error {
	events, ok := st.(storage.MergeEventStore)
	if !ok {
		return storage.ErrInvalidInput
	}
	return events.UpdateProjectionOffset(ctx, offset)
}

func forwardGetProjectionOffset(ctx context.Context, st storage.Storage, projectionName string, shardID int32) (*models.ProjectionOffset, error) {
	events, ok := st.(storage.MergeEventStore)
	if !ok {
		return nil, storage.ErrInvalidInput
	}
	return events.GetProjectionOffset(ctx, projectionName, shardID)
}

func forwardUpsertHomePathHeads(ctx context.Context, st storage.Storage, heads []*models.HomePathHead) error {
	headStore, ok := st.(storage.HomePathHeadStore)
	if !ok {
		return storage.ErrInvalidInput
	}
	return headStore.UpsertHomePathHeads(ctx, heads)
}

func forwardGetHomePathHeads(ctx context.Context, st storage.Storage, homeID string, paths []string) (map[string]*models.HomePathHead, error) {
	headStore, ok := st.(storage.HomePathHeadStore)
	if !ok {
		return nil, storage.ErrInvalidInput
	}
	return headStore.GetHomePathHeads(ctx, homeID, paths)
}

func forwardListHomePathHeads(ctx context.Context, st storage.Storage, homeID string, limit int) ([]*models.HomePathHead, error) {
	headStore, ok := st.(storage.HomePathHeadStore)
	if !ok {
		return nil, storage.ErrInvalidInput
	}
	return headStore.ListHomePathHeads(ctx, homeID, limit)
}

func forwardBackfillHomePathHeads(ctx context.Context, st storage.Storage, homeID string) (*models.HomePathHeadBackfillResult, error) {
	headStore, ok := st.(storage.HomePathHeadStore)
	if !ok {
		return nil, storage.ErrInvalidInput
	}
	return headStore.BackfillHomePathHeads(ctx, homeID)
}

func forwardValidateHomePathHeads(ctx context.Context, st storage.Storage, homeID string) (*models.HomePathHeadValidationResult, error) {
	headStore, ok := st.(storage.HomePathHeadStore)
	if !ok {
		return nil, storage.ErrInvalidInput
	}
	return headStore.ValidateHomePathHeads(ctx, homeID)
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
		ID:            "chg_dup",
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
	mustCreateChangesetSnapshot(t, ctx, srv, cs)

	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", resp.GetStatus())
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedProjections(waitCtx); err != nil {
		t.Fatalf("timed out waiting for root projection queue: %v", err)
	}

	if got := countingStorage.counts["slice-dup:dup.txt"]; got != 1 {
		t.Fatalf("expected one ownership write for slice file, got %d", got)
	}
	if got := countingStorage.counts["root:dup.txt"]; got != 0 {
		t.Fatalf("expected root ownership to stay projection-only, got %d writes", got)
	}

	updatedCS, err := base.GetChangeset(ctx, cs.ID)
	if err != nil {
		t.Fatalf("failed to load merged changeset: %v", err)
	}
	if len(updatedCS.ModifiedFiles) != 1 || updatedCS.ModifiedFiles[0] != "dup.txt" {
		t.Fatalf("expected deduplicated modified files, got %#v", updatedCS.ModifiedFiles)
	}
}

func TestMergeChangesetAppendsAcceptedMergeEvent(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	filePath := "alice/app/main.go"
	slice := &models.Slice{
		ID:        "slice-merge-event",
		Name:      "slice-merge-event",
		Files:     []string{filePath},
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	content := []byte("package main\n")
	manifestHash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, content)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Hash:     manifestHash,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("failed to add entry: %v", err)
	}
	cs := &models.Changeset{
		ID:            "chg_merge-event",
		SliceID:       slice.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		Author:        "alice",
		Message:       "merge event",
		CreatedAt:     time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(st)
	mustCreateChangesetSnapshot(t, ctx, srv, cs)
	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", resp.GetStatus())
	}
	if resp.GetMergeHomeId() != "alice" || resp.GetMergeSeq() <= 0 {
		t.Fatalf("expected merge freshness token, got home=%q shard=%d seq=%d", resp.GetMergeHomeId(), resp.GetMergeShard(), resp.GetMergeSeq())
	}
	projections := projectionStatusByName(resp.GetProjections())
	historyProjection := projections[historyProjectionName]
	if historyProjection == nil {
		t.Fatalf("expected history projection status, got %#v", resp.GetProjections())
	}
	if historyProjection.GetState() != slicev1.ProjectionState_PROJECTION_STATE_PENDING {
		t.Fatalf("expected history projection to be pending before queued projection drains, got %v", historyProjection.GetState())
	}

	event, err := st.GetMergeEventByChangeset(ctx, cs.ID)
	if err != nil {
		t.Fatalf("expected accepted merge event: %v", err)
	}
	if event.HomeID != "alice" {
		t.Fatalf("expected home alice, got %q", event.HomeID)
	}
	if event.SourceSliceID != slice.ID || event.SourceCommitHash != resp.GetNewCommitHash() {
		t.Fatalf("unexpected event source: %#v", event)
	}
	if event.ShardID != resp.GetMergeShard() || event.MergeSeq != resp.GetMergeSeq() {
		t.Fatalf("response token does not match event: resp shard=%d seq=%d event=%#v", resp.GetMergeShard(), resp.GetMergeSeq(), event)
	}
	if len(event.TouchedPaths) != 1 || event.TouchedPaths[0] != filePath {
		t.Fatalf("unexpected touched paths: %#v", event.TouchedPaths)
	}
	if len(event.PathUpdates) != 1 {
		t.Fatalf("expected one path update, got %#v", event.PathUpdates)
	}
	update := event.PathUpdates[0]
	if update.Path != filePath || update.ManifestHash != manifestHash || update.SourceCommitHash != resp.GetNewCommitHash() || update.Deleted {
		t.Fatalf("unexpected path update: %#v", update)
	}
	if update.ParentCommitHash == "" {
		t.Fatalf("expected path update to carry parent commit hash")
	}
	if update.NewVersion != event.MergeSeq {
		t.Fatalf("expected path update new version to match merge seq, got update=%d event=%d", update.NewVersion, event.MergeSeq)
	}
	listed, err := st.ListMergeEvents(ctx, event.ShardID, 0, 10)
	if err != nil {
		t.Fatalf("ListMergeEvents failed: %v", err)
	}
	if len(listed) != 1 || listed[0].ChangesetID != cs.ID {
		t.Fatalf("expected event in shard list, got %#v", listed)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedHistoryProjections(waitCtx); err != nil {
		t.Fatalf("timed out waiting for history projection: %v", err)
	}
	changes, err := st.GetCommitChanges(ctx, resp.GetNewCommitHash())
	if err != nil {
		t.Fatalf("GetCommitChanges failed: %v", err)
	}
	if len(changes) != 1 || changes[0].Path != filePath || changes[0].NewHash != manifestHash {
		t.Fatalf("expected projected file change for %q, got %#v", filePath, changes)
	}
}

func TestMergeChangesetMissingPathHeadSnapshotDoesNotAppendMergeEvent(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()

	const sharedFile = "alice/shared.txt"
	slice := &models.Slice{ID: "slice-event-missing-path-head", Name: "slice-event-missing-path-head", Files: []string{sharedFile}, Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	cs := &models.Changeset{
		ID:            "chg_merge-event-missing-path-head",
		SliceID:       slice.ID,
		ModifiedFiles: []string{sharedFile},
		Status:        models.ChangesetStatusPending,
		Author:        "alice",
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
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_STALE_BASE {
		t.Fatalf("expected stale base, got %v", resp.GetStatus())
	}
	if _, err := st.GetMergeEventByChangeset(ctx, cs.ID); !errors.Is(err, storage.ErrMergeEventNotFound) {
		t.Fatalf("expected no merge event for changeset without path-head snapshot, got %v", err)
	}
}

func TestMergeChangesetRejectsMissingChangesetContentRef(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	filePath := "tester/docs/missing.txt"
	slice := &models.Slice{
		ID:        "slice-missing-content-ref",
		Name:      "slice-missing-content-ref",
		Files:     []string{filePath},
		Owners:    []string{"tester"},
		CreatedBy: "tester",
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	cs := &models.Changeset{
		ID:            "chg_missing-content-ref",
		SliceID:       slice.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		Author:        "tester",
		CreatedAt:     time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}
	if err := st.CreateChangesetSnapshot(ctx, &models.ChangesetSnapshot{
		ID:               common.GenerateChangesetSnapshotID(cs.ID, 1),
		ChangesetID:      cs.ID,
		Version:          1,
		ModifiedFiles:    []string{filePath},
		BasePathVersions: map[string]int64{filePath: 0},
		FileHashes: map[string]string{
			filePath: "missing-manifest-hash",
		},
		Author:    "tester",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("failed to create changeset snapshot: %v", err)
	}

	srv := newSliceServiceServer(st)
	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err == nil {
		t.Fatalf("expected MergeChangeset to fail, got response %#v", resp)
	}
	if code := status.Code(err); code != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v: %v", code, err)
	}

	updated, err := st.GetChangeset(ctx, cs.ID)
	if err != nil {
		t.Fatalf("failed to reload changeset: %v", err)
	}
	if updated.Status == models.ChangesetStatusMerged {
		t.Fatalf("changeset should not be marked merged after missing content ref")
	}
}

func TestMergeChangesetAcceptsRepeatedSnapshotContentRefs(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	firstPath := "tester/docs/a.txt"
	secondPath := "tester/docs/b.txt"
	slice := &models.Slice{
		ID:        "slice-repeated-content-ref",
		Name:      "slice-repeated-content-ref",
		Files:     []string{firstPath, secondPath},
		Owners:    []string{"tester"},
		CreatedBy: "tester",
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	content := []byte("shared content\n")
	firstHash := mustWriteSliceManifest(t, ctx, st, slice.ID, firstPath, content)
	secondHash := mustWriteSliceManifest(t, ctx, st, slice.ID, secondPath, content)
	if firstHash != secondHash {
		t.Fatalf("expected identical content to reuse manifest hash, got %q and %q", firstHash, secondHash)
	}
	for _, filePath := range []string{firstPath, secondPath} {
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(slice.ID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: slice.ID,
			Hash:     firstHash,
			Size:     int64(len(content)),
		}); err != nil {
			t.Fatalf("failed to add entry %s: %v", filePath, err)
		}
	}

	cs := &models.Changeset{
		ID:            "chg_repeated-content-ref",
		SliceID:       slice.ID,
		ModifiedFiles: []string{firstPath, secondPath},
		Status:        models.ChangesetStatusPending,
		Author:        "tester",
		CreatedAt:     time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}
	if err := st.CreateChangesetSnapshot(ctx, &models.ChangesetSnapshot{
		ID:            common.GenerateChangesetSnapshotID(cs.ID, 1),
		ChangesetID:   cs.ID,
		Version:       1,
		ModifiedFiles: []string{firstPath, secondPath},
		BasePathVersions: map[string]int64{
			firstPath:  0,
			secondPath: 0,
		},
		FileHashes: map[string]string{
			firstPath:  firstHash,
			secondPath: firstHash,
		},
		Author:    "tester",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("failed to create changeset snapshot: %v", err)
	}

	srv := newSliceServiceServer(st)
	mustCreateChangesetSnapshot(t, ctx, srv, cs)
	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", resp.GetStatus())
	}
}

func TestMergeChangesetProjectsRootFileTreeFromPathHeads(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()
	if err := base.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	if err := base.AddEntry(ctx, &models.DirectoryEntry{
		ID:       "root:existing.txt",
		Path:     "existing.txt",
		Type:     "file",
		ParentID: "root",
		Size:     8,
	}); err != nil {
		t.Fatalf("failed to seed root entry: %v", err)
	}
	if err := base.AddFileToSlice(ctx, "existing.txt", "root"); err != nil {
		t.Fatalf("failed to seed root file ownership: %v", err)
	}

	slice := &models.Slice{ID: "slice-tree-projection", Name: "slice-tree-projection", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := base.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	filePath := "docs/new.md"
	content := []byte("new file\n")
	contentHash := mustWriteSliceManifest(t, ctx, base, slice.ID, filePath, content)
	if err := base.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(content)),
		Hash:     contentHash,
	}); err != nil {
		t.Fatalf("failed to add slice file entry: %v", err)
	}

	cs := &models.Changeset{
		ID:            "chg_tree-projection",
		SliceID:       slice.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	if err := base.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(base)
	mustCreateChangesetSnapshot(t, ctx, srv, cs)
	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", resp.GetStatus())
	}

	rootSlice, err := base.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("failed to load root slice: %v", err)
	}
	if containsString(rootSlice.Files, filePath) {
		t.Fatalf("expected root ownership to stay projection-only, got %#v", rootSlice.Files)
	}
	projected, err := storage.ReadSliceFileContent(ctx, base, "root", filePath)
	if err != nil {
		t.Fatalf("ReadSliceFileContent(root, %s) failed: %v", filePath, err)
	}
	if string(projected.Content) != string(content) || projected.Hash != contentHash {
		t.Fatalf("projected root content mismatch: got hash=%q content=%q", projected.Hash, projected.Content)
	}

	fileSvc := fileservice.NewService(base)
	adminCtx := adminAuthContextForUser(t, base, "admin")
	listResp, err := fileSvc.ListEntries(adminCtx, &filev1.ListEntriesRequest{
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: "root"},
		},
	})
	if err != nil {
		t.Fatalf("ListEntries(root) failed: %v", err)
	}
	if !listEntriesContainPath(listResp.GetEntries(), "docs") {
		t.Fatalf("expected root file tree to include projected directory %q, got paths %#v", "docs", listEntryPaths(listResp.GetEntries()))
	}
}

func TestMergeChangesetDoesNotMaterializeRootFiles(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()
	if err := base.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	slice := &models.Slice{ID: "slice-async-projection", Name: "slice-async-projection", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := base.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	filePath := "async/file.txt"
	contentHash := mustWriteSliceManifest(t, ctx, base, slice.ID, filePath, []byte("async\n"))
	if err := base.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len("async\n")),
		Hash:     contentHash,
	}); err != nil {
		t.Fatalf("failed to add slice file entry: %v", err)
	}

	cs := &models.Changeset{
		ID:            "chg_async-projection",
		SliceID:       slice.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	if err := base.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(base)
	mustCreateChangesetSnapshot(t, ctx, srv, cs)
	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", resp.GetStatus())
	}

	rootSlice, err := base.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("failed to load root slice: %v", err)
	}
	if containsString(rootSlice.Files, filePath) {
		t.Fatalf("expected root files to stay projection-only immediately after merge")
	}
	projected, err := storage.ReadSliceFileContent(ctx, base, "root", filePath)
	if err != nil {
		t.Fatalf("ReadSliceFileContent(root, %s) failed: %v", filePath, err)
	}
	if projected.Hash != contentHash {
		t.Fatalf("projected root hash=%q want %q", projected.Hash, contentHash)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedProjections(waitCtx); err != nil {
		t.Fatalf("timed out waiting for queued projections: %v", err)
	}
	rootSlice, err = base.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("failed to load root slice after projections: %v", err)
	}
	if containsString(rootSlice.Files, filePath) {
		t.Fatalf("expected root files to remain projection-only after queued projections, got %#v", rootSlice.Files)
	}
}

func TestMergeChangesetMountedSliceListsMergedFile(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()
	if err := base.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	mountedDir := "nicholas/test2"
	if err := base.AddEntry(ctx, &models.DirectoryEntry{
		ID:       "root:" + mountedDir,
		Path:     mountedDir,
		Type:     "directory",
		ParentID: "root",
	}); err != nil {
		t.Fatalf("failed to seed mounted root directory: %v", err)
	}

	slice := &models.Slice{
		ID:          "nicholas/api-cross-b-20260504232313",
		Name:        "api-cross-b-20260504232313",
		Owners:      []string{"tester"},
		CreatedBy:   "tester",
		ParentSlice: "root",
		FolderMounts: []models.SliceFolderMount{{
			SourcePath: mountedDir,
			Alias:      mountedDir,
		}},
	}
	if err := base.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create mounted slice: %v", err)
	}

	filePath := mountedDir + "/hello.txt"
	content := []byte("hello\n")
	contentHash := mustWriteSliceManifest(t, ctx, base, slice.ID, filePath, content)
	if err := base.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(content)),
		Hash:     contentHash,
	}); err != nil {
		t.Fatalf("failed to add mounted slice file entry: %v", err)
	}

	cs := &models.Changeset{
		ID:            "chg_mounted-slice-file",
		SliceID:       slice.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	if err := base.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(base)
	mustCreateChangesetSnapshot(t, ctx, srv, cs)
	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", resp.GetStatus())
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedProjections(waitCtx); err != nil {
		t.Fatalf("timed out waiting for root projection queue: %v", err)
	}

	fileSvc := fileservice.NewService(base)
	listResp, err := fileSvc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Path: mountedDir,
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: slice.ID},
		},
	})
	if err != nil {
		t.Fatalf("ListEntries(%s) failed: %v", mountedDir, err)
	}
	if !listEntriesContainPath(listResp.GetEntries(), filePath) {
		t.Fatalf("expected mounted slice file tree to include merged file %q, got paths %#v", filePath, listEntryPaths(listResp.GetEntries()))
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
		ID:            "chg_locked",
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
		ID:            "chg_concurrent-1",
		SliceID:       slice.ID,
		ModifiedFiles: []string{"a.txt"},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	cs2 := &models.Changeset{
		ID:            "chg_concurrent-2",
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
	mustCreateChangesetSnapshot(t, ctx, srv, cs1)
	mustCreateChangesetSnapshot(t, ctx, srv, cs2)

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
		ID:            "chg_overlap-a",
		SliceID:       sliceA.ID,
		ModifiedFiles: []string{sharedFile},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	csB := &models.Changeset{
		ID:            "chg_overlap-b",
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
	mustCreateChangesetSnapshot(t, ctx, srv, csA)
	mustCreateChangesetSnapshot(t, ctx, srv, csB)

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

func TestCreateChangesetFiltersHomeAddsOutsideUserRoot(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	home, err := homeslice.EnsureUserHomeSlice(ctx, st, "alice")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}

	srv := NewService(st)
	createResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       home.ID,
		ModifiedFiles: []string{"outside.txt", "alice/inside.txt"},
		Message:       "filter outside home root",
		FileContents: []*slicev1.FileContentChange{
			{Path: "outside.txt", Content: []byte("outside\n")},
			{Path: "alice/inside.txt", Content: []byte("inside\n")},
		},
	})
	if err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}

	cs, err := st.GetChangeset(ctx, createResp.GetChangesetId())
	if err != nil {
		t.Fatalf("GetChangeset failed: %v", err)
	}
	if got, want := cs.ModifiedFiles, []string{"alice/inside.txt"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected only in-home modified file, got %#v", got)
	}
	got, err := storage.ReadSliceFileContent(ctx, st, home.ID, "alice/inside.txt")
	if err != nil {
		t.Fatalf("ReadSliceFileContent inside failed: %v", err)
	}
	if string(got.Content) != "inside\n" {
		t.Fatalf("expected inside content, got %q", string(got.Content))
	}
	if _, err := storage.ReadSliceFileContent(ctx, st, home.ID, "outside.txt"); !errors.Is(err, storage.ErrEntryNotFound) {
		t.Fatalf("expected outside content to be ignored, got %v", err)
	}
}

func TestCreateChangesetRejectsOnlyHomeAddsOutsideUserRoot(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	home, err := homeslice.EnsureUserHomeSlice(ctx, st, "alice")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}

	srv := NewService(st)
	_, err = srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       home.ID,
		ModifiedFiles: []string{"outside.txt"},
		Message:       "filter outside home root",
		FileContents: []*slicev1.FileContentChange{
			{Path: "outside.txt", Content: []byte("outside\n")},
		},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for outside-only changeset, got %v", err)
	}
}

func TestCreateChangesetFiltersMountedSliceAddsOutsideMountRoots(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}
	slice := &models.Slice{
		ID:          "mounted-filter",
		Name:        "mounted-filter",
		Owners:      []string{"alice"},
		CreatedBy:   "alice",
		ParentSlice: "root",
		FolderMounts: []models.SliceFolderMount{{
			SourcePath: "alice/project",
			Alias:      "project",
		}},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	srv := NewService(st)
	createResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       slice.ID,
		ModifiedFiles: []string{"outside.txt", "project/inside.txt"},
		Message:       "filter outside mount root",
		FileContents: []*slicev1.FileContentChange{
			{Path: "outside.txt", Content: []byte("outside\n")},
			{Path: "project/inside.txt", Content: []byte("inside\n")},
		},
	})
	if err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	cs, err := st.GetChangeset(ctx, createResp.GetChangesetId())
	if err != nil {
		t.Fatalf("GetChangeset failed: %v", err)
	}
	if got, want := cs.ModifiedFiles, []string{"project/inside.txt"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected only mounted modified file, got %#v", got)
	}
}

func TestCreateChangesetRejectsParentSliceWithoutMounts(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}
	slice := &models.Slice{
		ID:          "legacy-parent-shape",
		Name:        "legacy-parent-shape",
		Owners:      []string{"alice"},
		CreatedBy:   "alice",
		ParentSlice: "root",
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	srv := NewService(st)
	_, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       slice.ID,
		ModifiedFiles: []string{"untracked.txt"},
		Message:       "reject legacy shape",
		FileContents: []*slicev1.FileContentChange{
			{Path: "untracked.txt", Content: []byte("ignored\n")},
		},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for parent slice without mounts, got %v", err)
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

	if got, want := first.GetChangesetId(), "chg_1"; got != want {
		t.Fatalf("expected first changeset id %q, got %q", want, got)
	}
	if got, want := second.GetChangesetId(), "chg_2"; got != want {
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

func TestReviewChangesetSkipsInlinePatchesForLargeStandardChangeset(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-large-standard-review", Name: "slice-large-standard-review", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	const baseCommit = "commit-large-standard-base"
	baseFiles := make(map[string]string)
	modifiedFiles := make([]string, 0, maxReviewPatchableChanges+1)
	for i := 0; i < maxReviewPatchableChanges+1; i++ {
		filePath := fmt.Sprintf("src/file-%03d.txt", i)
		baseContent := []byte(fmt.Sprintf("line %d\n", i))
		headContent := []byte(fmt.Sprintf("line %d\nupdated\n", i))
		baseHash := hashBytes(baseContent)
		mustWriteVersionedManifest(t, ctx, st, filePath, baseHash, baseContent)
		baseFiles[filePath] = baseHash
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
		modifiedFiles = append(modifiedFiles, filePath)
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: baseCommit,
		SliceID:    slice.ID,
		Files:      baseFiles,
		Timestamp:  time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatalf("failed to save base commit snapshot: %v", err)
	}

	srv := NewService(st)
	createResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        slice.ID,
		BaseCommitHash: baseCommit,
		ModifiedFiles:  modifiedFiles,
		Message:        "large standard patch coverage",
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
	if got, want := len(reviewResp.GetChanges()), len(modifiedFiles); got != want {
		t.Fatalf("expected %d review changes, got %d", want, got)
	}
	for _, change := range reviewResp.GetChanges() {
		if strings.TrimSpace(change.GetPatch()) != "" {
			t.Fatalf("expected empty patch for large changeset, got %q", change.GetPatch())
		}
	}
	if reviewResp.GetDiff().GetFilesModified() != int32(len(modifiedFiles)) {
		t.Fatalf("expected files_modified=%d, got %d", len(modifiedFiles), reviewResp.GetDiff().GetFilesModified())
	}
	if len(reviewResp.GetWarnings()) == 0 || !strings.Contains(reviewResp.GetWarnings()[0], "inline patches skipped") {
		t.Fatalf("expected inline patch warning, got %#v", reviewResp.GetWarnings())
	}
}

func TestListChangesetsCanOmitModifiedFiles(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-list-changeset-summary", Name: "slice-list-changeset-summary", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	createResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       slice.ID,
		ModifiedFiles: []string{"a.txt", "b.txt"},
		Message:       "summary list",
	})
	if err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}

	resp, err := srv.ListChangesets(ctx, &slicev1.ListChangesetsRequest{
		SliceId:            slice.ID,
		IncludeAllStatuses: true,
		OmitModifiedFiles:  true,
	})
	if err != nil {
		t.Fatalf("ListChangesets failed: %v", err)
	}
	if len(resp.GetChangesets()) != 1 {
		t.Fatalf("expected one changeset, got %d", len(resp.GetChangesets()))
	}
	got := resp.GetChangesets()[0]
	if got.GetChangesetId() != createResp.GetChangesetId() {
		t.Fatalf("expected changeset %q, got %q", createResp.GetChangesetId(), got.GetChangesetId())
	}
	if len(got.GetModifiedFiles()) != 0 {
		t.Fatalf("expected modified files omitted, got %#v", got.GetModifiedFiles())
	}
	if got.GetModifiedFileCount() != 2 {
		t.Fatalf("expected modified_file_count=2, got %d", got.GetModifiedFileCount())
	}
}

func TestListChangesetsDefersLargeReviewStateWhenOmittingFiles(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-large-list-summary", Name: "slice-large-list-summary", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	modifiedFiles := make([]string, 0, maxChangesetListReviewPaths+1)
	for i := 0; i < maxChangesetListReviewPaths+1; i++ {
		modifiedFiles = append(modifiedFiles, fmt.Sprintf("large/file-%03d.txt", i))
	}

	srv := NewService(st)
	if _, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       slice.ID,
		ModifiedFiles: modifiedFiles,
		Message:       "large summary list",
	}); err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}

	resp, err := srv.ListChangesets(ctx, &slicev1.ListChangesetsRequest{
		SliceId:            slice.ID,
		IncludeAllStatuses: true,
		OmitModifiedFiles:  true,
	})
	if err != nil {
		t.Fatalf("ListChangesets failed: %v", err)
	}
	got := resp.GetChangesets()[0]
	if got.GetModifiedFileCount() != int32(len(modifiedFiles)) {
		t.Fatalf("expected modified_file_count=%d, got %d", len(modifiedFiles), got.GetModifiedFileCount())
	}
	if got.GetReviewStatus() != slicev1.ReviewStatus_REVIEW_STATUS_UNKNOWN {
		t.Fatalf("expected unknown review status for large summary row, got %v", got.GetReviewStatus())
	}
}

func TestReviewChangesetMissingPathHeadSnapshotNeedsSync(t *testing.T) {
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
		ID:             "chg_stale-review",
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
	if resp.GetReviewStatus() != slicev1.ReviewStatus_NEEDS_SYNC {
		t.Fatalf("expected NEEDS_SYNC, got %v", resp.GetReviewStatus())
	}
	if len(resp.GetWarnings()) == 0 || !strings.Contains(resp.GetWarnings()[0], "path-head") {
		t.Fatalf("expected path-head warning, got %#v", resp.GetWarnings())
	}
	if len(resp.GetIssues()) == 0 || resp.GetIssues()[0].GetType() != slicev1.ReviewIssueType_REVIEW_ISSUE_TYPE_STALE_BASE {
		t.Fatalf("expected stale-base issue, got %#v", resp.GetIssues())
	}
}

func TestMergeChangesetRejectsMissingPathHeadSnapshot(t *testing.T) {
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
		ID:             "chg_stale-merge",
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
	if !strings.Contains(resp.GetMessage(), "path-head") {
		t.Fatalf("expected path-head message, got %q", resp.GetMessage())
	}
}

func TestReviewChangesetUsesSnapshotBasePathVersions(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	filePath := "tester/app/main.go"
	slice := &models.Slice{
		ID:        "slice-path-head-review",
		Name:      "slice-path-head-review",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{filePath},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	content := []byte("package main\n")
	manifestHash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, content)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Hash:     manifestHash,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("failed to add entry: %v", err)
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:       "tester",
		Path:         filePath,
		PathVersion:  3,
		ManifestHash: manifestHash,
		ContentHash:  manifestHash,
	}}); err != nil {
		t.Fatalf("failed to seed home path head: %v", err)
	}

	cs := &models.Changeset{
		ID:            "chg_path-head-review",
		SliceID:       slice.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		Author:        "tester",
		CreatedAt:     time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(st)
	if err := srv.createChangesetSnapshot(ctx, cs); err != nil {
		t.Fatalf("createChangesetSnapshot failed: %v", err)
	}
	snapshot, err := st.GetChangesetSnapshot(ctx, cs.ID, 0)
	if err != nil {
		t.Fatalf("failed to load snapshot: %v", err)
	}
	if snapshot.BasePathVersions[filePath] != 3 {
		t.Fatalf("expected snapshot base path version 3, got %#v", snapshot.BasePathVersions)
	}

	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:       "tester",
		Path:         filePath,
		PathVersion:  4,
		ManifestHash: manifestHash,
		ContentHash:  manifestHash,
	}}); err != nil {
		t.Fatalf("failed to advance home path head: %v", err)
	}

	resp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if resp.GetReviewStatus() != slicev1.ReviewStatus_NEEDS_SYNC {
		t.Fatalf("expected NEEDS_SYNC from path head version drift, got %v", resp.GetReviewStatus())
	}
	if len(resp.GetIssues()) != 1 || resp.GetIssues()[0].GetType() != slicev1.ReviewIssueType_REVIEW_ISSUE_TYPE_STALE_BASE {
		t.Fatalf("expected stale-base path issue, got %#v", resp.GetIssues())
	}
}

func TestCreateChangesetSnapshotSynthesizesMissingBasePathHead(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	filePath := "tester/app/main.go"
	slice := &models.Slice{
		ID:        "home_tester",
		Name:      "tester",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{filePath},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	content := []byte("package main\n")
	manifestHash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, content)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Hash:     manifestHash,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("failed to add entry: %v", err)
	}
	baseCommit := "commit-synth-base-head"
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: baseCommit,
		SliceID:    slice.ID,
		Files: map[string]string{
			filePath: manifestHash,
		},
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, slice.ID, &models.Commit{
		CommitHash: baseCommit,
		Message:    "seed",
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}
	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	meta.HeadCommitHash = baseCommit
	if err := st.UpdateSliceMetadata(ctx, slice.ID, meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	cs := &models.Changeset{
		ID:             "chg_synth-base-path-head",
		SliceID:        slice.ID,
		BaseCommitHash: baseCommit,
		ModifiedFiles:  []string{filePath},
		Status:         models.ChangesetStatusPending,
		Author:         "tester",
		CreatedAt:      time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(st)
	if err := srv.createChangesetSnapshot(ctx, cs); err != nil {
		t.Fatalf("createChangesetSnapshot failed: %v", err)
	}
	snapshot, err := st.GetChangesetSnapshot(ctx, cs.ID, 0)
	if err != nil {
		t.Fatalf("failed to load snapshot: %v", err)
	}
	if snapshot.BasePathVersions[filePath] != 1 {
		t.Fatalf("expected synthesized base path version 1, got %#v", snapshot.BasePathVersions)
	}
	heads, err := st.GetHomePathHeads(ctx, "tester", []string{filePath})
	if err != nil {
		t.Fatalf("GetHomePathHeads failed: %v", err)
	}
	if heads[filePath] == nil || heads[filePath].PathVersion != 1 {
		t.Fatalf("expected synthesized stored path head version 1, got %#v", heads[filePath])
	}
}

func TestMergeChangesetPathHeadAuthorityRejectsDrift(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	filePath := "tester/app/shadow.go"
	slice := &models.Slice{
		ID:        "slice-path-head-shadow",
		Name:      "slice-path-head-shadow",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{filePath},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	content := []byte("package shadow\nconst Value = 0\n")
	manifestHash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, content)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Hash:     manifestHash,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("failed to add entry: %v", err)
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:       "tester",
		Path:         filePath,
		PathVersion:  10,
		ManifestHash: manifestHash,
		ContentHash:  manifestHash,
	}}); err != nil {
		t.Fatalf("failed to seed path head: %v", err)
	}
	baseCommitHash := "cmt_path_head_conflict_base"
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: baseCommitHash,
		SliceID:    slice.ID,
		Files: map[string]string{
			filePath: manifestHash,
		},
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("failed to save base commit snapshot: %v", err)
	}

	cs := &models.Changeset{
		ID:             "chg_path-head-shadow",
		SliceID:        slice.ID,
		BaseCommitHash: baseCommitHash,
		ModifiedFiles:  []string{filePath},
		Status:         models.ChangesetStatusPending,
		Author:         "tester",
		CreatedAt:      time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(st)
	oursHash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, []byte("package shadow\nconst Value = 1\n"))
	if err := srv.createChangesetSnapshot(ctx, cs); err != nil {
		t.Fatalf("createChangesetSnapshot failed: %v", err)
	}
	currentHash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, []byte("package shadow\nconst Value = 2\n"))
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:       "tester",
		Path:         filePath,
		PathVersion:  11,
		ManifestHash: currentHash,
		ContentHash:  currentHash,
	}}); err != nil {
		t.Fatalf("failed to advance path head: %v", err)
	}

	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_STALE_BASE {
		t.Fatalf("expected stale-base status from path-head drift, got %v", resp.GetStatus())
	}
	if len(resp.GetConflicts()) != 1 {
		t.Fatalf("expected one conflict artifact, got %#v", resp.GetConflicts())
	}
	conflict := resp.GetConflicts()[0]
	if conflict.GetChangesetId() != cs.ID || conflict.GetPath() != filePath || conflict.GetType() != slicev1.ConflictType_CONFLICT_TYPE_STALE_BASE {
		t.Fatalf("unexpected conflict artifact: %#v", conflict)
	}
	if conflict.GetBaseVersion() != 10 || conflict.GetCurrentVersion() != 11 {
		t.Fatalf("conflict versions = %d/%d, want 10/11", conflict.GetBaseVersion(), conflict.GetCurrentVersion())
	}
	if conflict.GetBaseHash() != manifestHash || conflict.GetOursHash() != oursHash || conflict.GetTheirsHash() != currentHash {
		t.Fatalf("conflict hashes = base %q ours %q theirs %q", conflict.GetBaseHash(), conflict.GetOursHash(), conflict.GetTheirsHash())
	}
	if !strings.Contains(conflict.GetPatch(), "const Value = 2") {
		t.Fatalf("expected conflict patch to include current content, got %q", conflict.GetPatch())
	}
	listResp, err := srv.ListChangesetConflicts(ctx, &slicev1.ListChangesetConflictsRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("ListChangesetConflicts failed: %v", err)
	}
	if listResp.GetTotalConflicts() != 1 || listResp.GetConflicts()[0].GetConflictId() != conflict.GetConflictId() {
		t.Fatalf("listed conflicts = %#v, want stored conflict %s", listResp.GetConflicts(), conflict.GetConflictId())
	}
	if _, err := st.GetMergeEventByChangeset(ctx, cs.ID); !errors.Is(err, storage.ErrMergeEventNotFound) {
		t.Fatalf("expected no merge event for rejected path-head drift, got %v", err)
	}
}

func TestMergeChangesetAutoMergesDisjointPathHeadDrift(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	filePath := "tester/app/automerge.go"
	slice := &models.Slice{
		ID:        "slice-path-head-automerge",
		Name:      "slice-path-head-automerge",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{filePath},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	baseContent := []byte("package app\nconst A = 0\nconst B = 0\n")
	baseHash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, baseContent)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Hash:     baseHash,
		Size:     int64(len(baseContent)),
	}); err != nil {
		t.Fatalf("failed to add entry: %v", err)
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:       "tester",
		Path:         filePath,
		PathVersion:  10,
		ManifestHash: baseHash,
		ContentHash:  baseHash,
	}}); err != nil {
		t.Fatalf("failed to seed path head: %v", err)
	}
	baseCommitHash := "cmt_path_head_automerge_base"
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: baseCommitHash,
		SliceID:    slice.ID,
		Files: map[string]string{
			filePath: baseHash,
		},
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("failed to save base commit snapshot: %v", err)
	}

	cs := &models.Changeset{
		ID:             "chg_path-head-automerge",
		SliceID:        slice.ID,
		BaseCommitHash: baseCommitHash,
		ModifiedFiles:  []string{filePath},
		Status:         models.ChangesetStatusPending,
		Author:         "tester",
		CreatedAt:      time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(st)
	mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, []byte("package app\nconst A = 1\nconst B = 0\n"))
	if err := srv.createChangesetSnapshot(ctx, cs); err != nil {
		t.Fatalf("createChangesetSnapshot failed: %v", err)
	}
	theirsHash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, []byte("package app\nconst A = 0\nconst B = 2\n"))
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:       "tester",
		Path:         filePath,
		PathVersion:  11,
		ManifestHash: theirsHash,
		ContentHash:  theirsHash,
	}}); err != nil {
		t.Fatalf("failed to advance path head: %v", err)
	}

	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("merge status = %v, want success: %s", resp.GetStatus(), resp.GetMessage())
	}
	if len(resp.GetConflicts()) != 0 {
		t.Fatalf("expected auto-merge without conflicts, got %#v", resp.GetConflicts())
	}
	mergedContent, err := storage.ReadSliceFileContent(ctx, st, slice.ID, filePath)
	if err != nil {
		t.Fatalf("ReadSliceFileContent failed: %v", err)
	}
	if got := string(mergedContent.Content); got != "package app\nconst A = 1\nconst B = 2\n" {
		t.Fatalf("merged content = %q", got)
	}
	conflicts, err := srv.ListChangesetConflicts(ctx, &slicev1.ListChangesetConflictsRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("ListChangesetConflicts failed: %v", err)
	}
	if conflicts.GetTotalConflicts() != 0 {
		t.Fatalf("expected no stored conflicts, got %#v", conflicts.GetConflicts())
	}
}

func TestMergeChangesetPathHeadCASUpdatesHeadAndEventVersions(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	filePath := "tester/app/cas.go"
	slice := &models.Slice{
		ID:        "slice-path-head-cas",
		Name:      "slice-path-head-cas",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{filePath},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	content := []byte("package cas\n")
	manifestHash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, content)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Hash:     manifestHash,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("failed to add entry: %v", err)
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           "tester",
		Path:             filePath,
		PathVersion:      4,
		ManifestHash:     "sha256:old-manifest",
		ContentHash:      "sha256:old-manifest",
		SourceSliceID:    slice.ID,
		SourceCommitHash: "old-commit",
		LastMergeSeq:     1,
	}}); err != nil {
		t.Fatalf("failed to seed path head: %v", err)
	}

	cs := &models.Changeset{
		ID:            "chg_path-head-cas",
		SliceID:       slice.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		Author:        "tester",
		Message:       "path head cas",
		CreatedAt:     time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(st)
	if err := srv.createChangesetSnapshot(ctx, cs); err != nil {
		t.Fatalf("createChangesetSnapshot failed: %v", err)
	}

	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", resp.GetStatus())
	}

	event, err := st.GetMergeEventByChangeset(ctx, cs.ID)
	if err != nil {
		t.Fatalf("expected accepted merge event: %v", err)
	}
	if len(event.PathUpdates) != 1 {
		t.Fatalf("expected one path update, got %#v", event.PathUpdates)
	}
	update := event.PathUpdates[0]
	if update.BaseVersion != 4 || update.NewVersion != 5 || update.ManifestHash != manifestHash {
		t.Fatalf("unexpected path update: %#v", update)
	}

	heads, err := st.GetHomePathHeads(ctx, "tester", []string{filePath})
	if err != nil {
		t.Fatalf("GetHomePathHeads failed: %v", err)
	}
	head := heads[filePath]
	if head == nil || head.PathVersion != 5 || head.ManifestHash != manifestHash || head.LastMergeSeq != event.MergeSeq {
		t.Fatalf("unexpected updated path head: %#v", head)
	}
}

func TestMergeChangesetPathHeadAuthorityAllowsDisjointStaleSliceHead(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:        "slice-path-head-disjoint",
		Name:      "slice-path-head-disjoint",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{"tester/app/a.go", "tester/app/b.go"},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	for _, filePath := range slice.Files {
		content := []byte(filePath + "\n")
		hash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, content)
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(slice.ID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: slice.ID,
			Hash:     hash,
			Size:     int64(len(content)),
		}); err != nil {
			t.Fatalf("failed to add entry %s: %v", filePath, err)
		}
		if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
			HomeID:       "tester",
			Path:         filePath,
			PathVersion:  1,
			ManifestHash: hash,
			ContentHash:  hash,
		}}); err != nil {
			t.Fatalf("failed to seed path head %s: %v", filePath, err)
		}
	}
	metadata, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		t.Fatalf("failed to load slice metadata: %v", err)
	}
	baseHead := metadata.HeadCommitHash

	srv := newSliceServiceServer(st)
	createChangeset := func(id, filePath string) {
		t.Helper()
		cs := &models.Changeset{
			ID:             id,
			SliceID:        slice.ID,
			BaseCommitHash: baseHead,
			ModifiedFiles:  []string{filePath},
			Status:         models.ChangesetStatusPending,
			Author:         "tester",
			CreatedAt:      time.Now(),
		}
		if err := st.CreateChangeset(ctx, cs); err != nil {
			t.Fatalf("failed to create changeset %s: %v", id, err)
		}
		if err := srv.createChangesetSnapshot(ctx, cs); err != nil {
			t.Fatalf("createChangesetSnapshot(%s) failed: %v", id, err)
		}
	}
	createChangeset("chg_path-head-disjoint-a", "tester/app/a.go")
	createChangeset("chg_path-head-disjoint-b", "tester/app/b.go")

	first, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: "chg_path-head-disjoint-a"})
	if err != nil {
		t.Fatalf("first MergeChangeset failed: %v", err)
	}
	if first.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected first merge success, got %v", first.GetStatus())
	}
	second, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: "chg_path-head-disjoint-b"})
	if err != nil {
		t.Fatalf("second MergeChangeset failed: %v", err)
	}
	if second.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected path-head authority to allow disjoint stale slice-head merge, got %v", second.GetStatus())
	}
}

func TestMergeChangesetPathHeadAuthorityIgnoresLegacyActiveSliceConflict(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	filePath := "tester/app/shared.go"
	sliceA := &models.Slice{
		ID:        "slice-path-head-active-a",
		Name:      "slice-path-head-active-a",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{filePath},
	}
	sliceB := &models.Slice{
		ID:        "slice-path-head-active-b",
		Name:      "slice-path-head-active-b",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{filePath},
	}
	for _, sl := range []*models.Slice{sliceA, sliceB} {
		if err := st.CreateSlice(ctx, sl); err != nil {
			t.Fatalf("failed to create slice %s: %v", sl.ID, err)
		}
		if err := st.AddFileToSlice(ctx, filePath, sl.ID); err != nil {
			t.Fatalf("failed to index file for %s: %v", sl.ID, err)
		}
	}
	hashA := mustWriteSliceManifest(t, ctx, st, sliceA.ID, filePath, []byte("package app\n\nconst Value = \"a\"\n"))
	hashB := mustWriteSliceManifest(t, ctx, st, sliceB.ID, filePath, []byte("package app\n\nconst Value = \"b\"\n"))
	for _, setup := range []struct {
		sliceID string
		hash    string
	}{
		{sliceA.ID, hashA},
		{sliceB.ID, hashB},
	} {
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(setup.sliceID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: setup.sliceID,
			Hash:     setup.hash,
			Size:     32,
		}); err != nil {
			t.Fatalf("failed to add entry for %s: %v", setup.sliceID, err)
		}
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           "tester",
		Path:             filePath,
		PathVersion:      3,
		ManifestHash:     hashA,
		ContentHash:      hashA,
		SourceSliceID:    sliceA.ID,
		SourceCommitHash: "commit-a",
		LastMergeSeq:     2,
	}}); err != nil {
		t.Fatalf("failed to seed path head: %v", err)
	}

	cs := &models.Changeset{
		ID:            "chg_path-head-active-conflict",
		SliceID:       sliceB.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		Author:        "tester",
		Message:       "path head is authority",
		CreatedAt:     time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(st)
	if err := srv.createChangesetSnapshot(ctx, cs); err != nil {
		t.Fatalf("createChangesetSnapshot failed: %v", err)
	}

	review, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if review.GetReviewStatus() != slicev1.ReviewStatus_READY_FOR_MERGE {
		t.Fatalf("expected path-head clean changeset to be ready, got %v issues=%#v", review.GetReviewStatus(), review.GetIssues())
	}

	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected path-head authority to ignore active-slice index divergence, got %v", resp.GetStatus())
	}
	event, err := st.GetMergeEventByChangeset(ctx, cs.ID)
	if err != nil {
		t.Fatalf("expected merge event: %v", err)
	}
	if len(event.PathUpdates) != 1 || event.PathUpdates[0].BaseVersion != 3 || event.PathUpdates[0].NewVersion != 4 {
		t.Fatalf("unexpected path-head event update: %#v", event.PathUpdates)
	}
	heads, err := st.GetHomePathHeads(ctx, "tester", []string{filePath})
	if err != nil {
		t.Fatalf("GetHomePathHeads failed: %v", err)
	}
	if head := heads[filePath]; head == nil || head.PathVersion != 4 || head.ManifestHash != hashB {
		t.Fatalf("unexpected updated head: %#v", head)
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
		ID:            "chg_normalized-merge",
		SliceID:       sliceA.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(st)
	mustCreateChangesetSnapshot(t, ctx, srv, cs)
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
		ID:             "chg_rebase-head",
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
	summarySnapshotsResp, err := srv.ListChangesetSnapshots(ctx, &slicev1.ListChangesetSnapshotsRequest{
		ChangesetId:       createResp.GetChangesetId(),
		OmitModifiedFiles: true,
	})
	if err != nil {
		t.Fatalf("ListChangesetSnapshots summary failed: %v", err)
	}
	if len(summarySnapshotsResp.GetSnapshots()) != 2 {
		t.Fatalf("expected 2 summary snapshots, got %d", len(summarySnapshotsResp.GetSnapshots()))
	}
	if len(summarySnapshotsResp.GetSnapshots()[0].GetModifiedFiles()) != 0 || summarySnapshotsResp.GetSnapshots()[0].GetModifiedFileCount() != 1 {
		t.Fatalf("expected latest summary snapshot count=1 with files omitted, got %#v", summarySnapshotsResp.GetSnapshots()[0])
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

func TestStreamChangesetSnapshotReturnsMetadataAndOnlyMissingContent(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-snapshot-stream", Name: "slice-snapshot-stream", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	createResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       slice.ID,
		ModifiedFiles: []string{"a.txt", "b.txt"},
		FileContents: []*slicev1.FileContentChange{
			{Path: "a.txt", Content: []byte("a v1\n")},
			{Path: "b.txt", Content: []byte("b v1\n")},
		},
		Message: "snapshot v1",
	})
	if err != nil {
		t.Fatalf("CreateChangeset v1 failed: %v", err)
	}
	if _, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		ChangesetId:   createResp.GetChangesetId(),
		ModifiedFiles: []string{"a.txt", "b.txt"},
		FileContents: []*slicev1.FileContentChange{
			{Path: "a.txt", Content: []byte("a v2\n")},
			{Path: "b.txt", Deleted: true},
		},
		Message: "snapshot v2",
	}); err != nil {
		t.Fatalf("CreateChangeset append failed: %v", err)
	}

	metadataRecorder := &changesetSnapshotStreamRecorder{ctx: ctx}
	if err := srv.StreamChangesetSnapshot(&slicev1.ChangesetSnapshotRequest{
		ChangesetId:     createResp.GetChangesetId(),
		SnapshotVersion: 2,
		MetadataOnly:    true,
	}, metadataRecorder); err != nil {
		t.Fatalf("StreamChangesetSnapshot metadata failed: %v", err)
	}
	manifest := mergeChangesetSnapshotManifestChunks(metadataRecorder.chunks)
	if manifest.GetSnapshot().GetVersion() != 2 {
		t.Fatalf("expected snapshot version 2, got %#v", manifest.GetSnapshot())
	}
	if manifest.GetSliceId() != slice.ID {
		t.Fatalf("expected slice id %q, got %q", slice.ID, manifest.GetSliceId())
	}
	if got := len(manifest.GetFileMetadata()); got != 1 {
		t.Fatalf("expected one file metadata entry, got %d", got)
	}
	if got := manifest.GetFileMetadata()[0].GetPath(); got != "a.txt" {
		t.Fatalf("expected a.txt metadata, got %q", got)
	}
	if got := manifest.GetDeletedPaths(); len(got) != 1 || got[0] != "b.txt" {
		t.Fatalf("expected deleted b.txt, got %#v", got)
	}

	contentRecorder := &changesetSnapshotStreamRecorder{ctx: ctx}
	if err := srv.StreamChangesetSnapshot(&slicev1.ChangesetSnapshotRequest{
		ChangesetId:     createResp.GetChangesetId(),
		SnapshotVersion: 2,
		Paths:           []string{"a.txt"},
		KnownHashes:     []string{manifest.GetFileMetadata()[0].GetHash()},
	}, contentRecorder); err != nil {
		t.Fatalf("StreamChangesetSnapshot known content failed: %v", err)
	}
	if got := countChangesetSnapshotPayloadChunks(contentRecorder.chunks); got != 0 {
		t.Fatalf("expected known hash to suppress content payload chunks, got %d", got)
	}

	missingContentRecorder := &changesetSnapshotStreamRecorder{ctx: ctx}
	if err := srv.StreamChangesetSnapshot(&slicev1.ChangesetSnapshotRequest{
		ChangesetId:     createResp.GetChangesetId(),
		SnapshotVersion: 2,
		Paths:           []string{"a.txt"},
	}, missingContentRecorder); err != nil {
		t.Fatalf("StreamChangesetSnapshot content failed: %v", err)
	}
	if got := countChangesetSnapshotPayloadChunks(missingContentRecorder.chunks); got == 0 {
		t.Fatalf("expected missing content payload chunks")
	}
}

func TestStreamChangesetSnapshotResolvesHashBeyondListLimit(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-snapshot-hash-limit", Name: "slice-snapshot-hash-limit", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	cs := &models.Changeset{
		ID:             "cs-snapshot-hash-limit",
		Hash:           "hash-1001",
		SliceID:        slice.ID,
		BaseCommitHash: "base-1",
		Status:         models.ChangesetStatusPending,
		Author:         "tester",
		Message:        "many snapshots",
		CreatedAt:      time.Now(),
	}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	for version := int32(1); version <= 1001; version++ {
		snapshot := &models.ChangesetSnapshot{
			ID:             common.GenerateChangesetSnapshotID(cs.ID, int64(version)),
			ChangesetID:    cs.ID,
			Version:        version,
			Hash:           fmt.Sprintf("hash-%04d", version),
			BaseCommitHash: cs.BaseCommitHash,
			Author:         "tester",
			Message:        fmt.Sprintf("snapshot %d", version),
			CreatedAt:      time.Now().Add(time.Duration(version) * time.Millisecond),
		}
		if err := st.CreateChangesetSnapshot(ctx, snapshot); err != nil {
			t.Fatalf("CreateChangesetSnapshot v%d failed: %v", version, err)
		}
	}

	srv := NewService(st)
	recorder := &changesetSnapshotStreamRecorder{ctx: ctx}
	if err := srv.StreamChangesetSnapshot(&slicev1.ChangesetSnapshotRequest{
		ChangesetId:  cs.ID,
		SnapshotHash: "hash-0001",
		MetadataOnly: true,
	}, recorder); err != nil {
		t.Fatalf("StreamChangesetSnapshot by old hash failed: %v", err)
	}
	manifest := mergeChangesetSnapshotManifestChunks(recorder.chunks)
	if manifest.GetSnapshot().GetVersion() != 1 || manifest.GetSnapshot().GetHash() != "hash-0001" {
		t.Fatalf("expected snapshot v1 by hash, got %#v", manifest.GetSnapshot())
	}
}

func TestCreateChangesetExportEnqueuesCIAndSupersedesOlderRuns(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	homeSliceID := homeslice.IDForUsername("alice")
	slice := &models.Slice{ID: homeSliceID, Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, homeSliceID, "alice/.gitslice/ci.yaml", []byte(`
version: 1
triggers:
  changeset_export: true
defaults:
  runner_pool: default
  shell: bash
runner_pools:
  default:
    executor: shell
`))
	mustWriteSliceManifest(t, ctx, st, homeSliceID, "alice/api/.gs-ci.yaml", []byte(`
version: 1
name: api
watch:
  - "**/*.go"
jobs:
  unit:
    required: true
    commands:
      - go test ./...
`))
	mustWriteSliceManifest(t, ctx, st, homeSliceID, "alice/api/main.go", []byte("package main\n"))

	srv := NewService(st)
	createResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        homeSliceID,
		BaseCommitHash: "base-v1",
		ModifiedFiles:  []string{"alice/api/main.go"},
		Message:        "snapshot v1",
	})
	if err != nil {
		t.Fatalf("CreateChangeset v1 failed: %v", err)
	}
	if createResp.GetCiRunId() == "" || createResp.GetCiStatus() != "queued" {
		t.Fatalf("CI response = run %q status %q, want queued run", createResp.GetCiRunId(), createResp.GetCiStatus())
	}

	reviewResp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if reviewResp.GetChangeset().GetCi().GetStatus() != "queued" || reviewResp.GetChangeset().GetCi().GetRequiredTotal() != 1 {
		t.Fatalf("CI summary = %#v, want one queued required check", reviewResp.GetChangeset().GetCi())
	}

	mustWriteSliceManifest(t, ctx, st, homeSliceID, "alice/api/main.go", []byte("package main\nfunc main() {}\n"))
	appendResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		ChangesetId:    createResp.GetChangesetId(),
		BaseCommitHash: "base-v2",
		ModifiedFiles:  []string{"alice/api/main.go"},
		Message:        "snapshot v2",
	})
	if err != nil {
		t.Fatalf("CreateChangeset append failed: %v", err)
	}
	if appendResp.GetCiRunId() == "" || appendResp.GetCiRunId() == createResp.GetCiRunId() {
		t.Fatalf("append CI run = %q, want new run different from %q", appendResp.GetCiRunId(), createResp.GetCiRunId())
	}
	runs, err := st.ListCIRuns(ctx, storage.CIRunListFilter{ChangesetID: createResp.GetChangesetId(), Limit: 10})
	if err != nil {
		t.Fatalf("ListCIRuns failed: %v", err)
	}
	statuses := map[string]string{}
	for _, run := range runs {
		statuses[run.ID] = run.Status
	}
	if statuses[createResp.GetCiRunId()] != "cancelled" {
		t.Fatalf("first run status = %q, want cancelled; all statuses %#v", statuses[createResp.GetCiRunId()], statuses)
	}
	if statuses[appendResp.GetCiRunId()] != "queued" {
		t.Fatalf("second run status = %q, want queued; all statuses %#v", statuses[appendResp.GetCiRunId()], statuses)
	}
}

func TestMergeChangesetRequiresCurrentCIPass(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	srv, changesetID, runID := setupCIMergeGateChangeset(t, ctx, st, `
version: 1
triggers:
  changeset_export: true
defaults:
  runner_pool: default
  shell: bash
runner_pools:
  default:
    executor: shell
merge_policy:
  require_success: true
  allow_force_merge: true
`)

	if _, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: changesetID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("MergeChangeset queued CI error = %v, want FailedPrecondition", err)
	}

	passRequiredCIChecks(t, ctx, st, runID)
	mergeResp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: changesetID})
	if err != nil {
		t.Fatalf("MergeChangeset after CI pass failed: %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("merge status = %v, want success", mergeResp.GetStatus())
	}
}

func TestMergeChangesetForceBypassesCIAuditedWhenPolicyAllows(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	srv, changesetID, _ := setupCIMergeGateChangeset(t, ctx, st, `
version: 1
triggers:
  changeset_export: true
defaults:
  runner_pool: default
  shell: bash
runner_pools:
  default:
    executor: shell
merge_policy:
  require_success: true
  allow_force_merge: true
`)

	mergeResp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
		ChangesetId: changesetID,
		Force:       true,
		ForceReason: "incident mitigation",
	})
	if err != nil {
		t.Fatalf("force MergeChangeset failed: %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("force merge status = %v, want success", mergeResp.GetStatus())
	}
	event, err := st.GetMergeEventByChangeset(ctx, changesetID)
	if err != nil {
		t.Fatalf("GetMergeEventByChangeset failed: %v", err)
	}
	if !event.Forced || event.ForceReason != "incident mitigation" || event.ForcedBy != "alice" {
		t.Fatalf("force audit = forced=%t reason=%q by=%q", event.Forced, event.ForceReason, event.ForcedBy)
	}
}

func TestCreateAndMergeChangesetBypassesCIGateWithoutForce(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	homeSliceID := homeslice.IDForUsername("alice")
	slice := &models.Slice{ID: homeSliceID, Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, homeSliceID, "alice/.gitslice/ci.yaml", []byte(`
version: 1
triggers:
  changeset_export: true
defaults:
  runner_pool: default
  shell: bash
runner_pools:
  default:
    executor: shell
merge_policy:
  require_success: true
  allow_force_merge: false
`))
	mustWriteSliceManifest(t, ctx, st, homeSliceID, "alice/api/.gs-ci.yaml", []byte(`
version: 1
name: api
watch:
  - "**/*.go"
jobs:
  unit:
    required: true
    commands:
      - go test ./...
`))

	srv := NewService(st)
	mergeResp, err := srv.CreateAndMergeChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       homeSliceID,
		ModifiedFiles: []string{"alice/api/from-tree.go"},
		Message:       "create file from tree",
		FileContents: []*slicev1.FileContentChange{{
			Path:    "alice/api/from-tree.go",
			Content: []byte("package main\n"),
		}},
	})
	if err != nil {
		t.Fatalf("CreateAndMergeChangeset failed: %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("merge status = %v, want success", mergeResp.GetStatus())
	}
	cs, err := st.GetChangeset(ctx, mergeResp.GetChangesetId())
	if err != nil {
		t.Fatalf("GetChangeset failed: %v", err)
	}
	if cs.Status != models.ChangesetStatusMerged {
		t.Fatalf("changeset status = %v, want merged", cs.Status)
	}
	event, err := st.GetMergeEventByChangeset(ctx, mergeResp.GetChangesetId())
	if err != nil {
		t.Fatalf("GetMergeEventByChangeset failed: %v", err)
	}
	if event.Forced {
		t.Fatalf("direct file-tree commit should not be recorded as force merge")
	}
	if len(event.TouchedPaths) != 1 || event.TouchedPaths[0] != "alice/api/from-tree.go" {
		t.Fatalf("merge event touched paths = %#v, want alice/api/from-tree.go", event.TouchedPaths)
	}
	if len(event.PathUpdates) != 1 {
		t.Fatalf("merge event path updates = %d, want 1", len(event.PathUpdates))
	}
	update := event.PathUpdates[0]
	if update.Path != "alice/api/from-tree.go" || update.Deleted || update.ContentHash == "" {
		t.Fatalf("merge event path update = %#v, want created file with content hash", update)
	}
	file, err := storage.ReadVersionedFileContent(ctx, st, update.ContentHash)
	if err != nil {
		t.Fatalf("ReadVersionedFileContent failed: %v", err)
	}
	if got := string(file.Content); got != "package main\n" {
		t.Fatalf("committed file content = %q, want package main", got)
	}
}

func TestCreateAndMergeChangesetRejectsStaleExpectedPathBase(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	homeSliceID := homeslice.IDForUsername("alice")
	filePath := "alice/api/main.go"
	oldContent := []byte("package main\n")

	slice := &models.Slice{ID: homeSliceID, Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	oldHash := mustWriteSliceManifest(t, ctx, st, homeSliceID, filePath, oldContent)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(homeSliceID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: homeSliceID,
		Hash:     oldHash,
		Size:     int64(len(oldContent)),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           "alice",
		Path:             filePath,
		PathVersion:      2,
		ContentHash:      oldHash,
		ManifestHash:     oldHash,
		SourceSliceID:    homeSliceID,
		SourceCommitHash: "cmt_current",
	}}); err != nil {
		t.Fatalf("UpsertHomePathHeads failed: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.CreateAndMergeChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        homeSliceID,
		BaseCommitHash: "cmt_loaded",
		ModifiedFiles:  []string{filePath},
		Message:        "stale edit",
		FileContents: []*slicev1.FileContentChange{{
			Path:    filePath,
			Content: []byte("package main\n\nfunc main() {}\n"),
		}},
		ExpectedPathBases: []*filev1.PathBase{{
			Path:             filePath,
			Exists:           true,
			ContentHash:      oldHash,
			PathVersion:      1,
			SourceSliceId:    homeSliceID,
			SourceCommitHash: "cmt_loaded",
		}},
	})
	if err != nil {
		t.Fatalf("CreateAndMergeChangeset returned error instead of stale response: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_STALE_BASE {
		t.Fatalf("merge status = %v, want stale base", resp.GetStatus())
	}
	if !strings.Contains(resp.GetMessage(), "Reload before saving") {
		t.Fatalf("stale message = %q, want reload guidance", resp.GetMessage())
	}
	entry, err := st.GetEntryByPath(ctx, homeSliceID, filePath)
	if err != nil {
		t.Fatalf("GetEntryByPath failed: %v", err)
	}
	if entry.Hash != oldHash {
		t.Fatalf("entry hash changed despite stale base: got %q want %q", entry.Hash, oldHash)
	}
	heads, err := st.GetHomePathHeads(ctx, "alice", []string{filePath})
	if err != nil {
		t.Fatalf("GetHomePathHeads failed: %v", err)
	}
	if head := heads[filePath]; head == nil || head.PathVersion != 2 || head.ManifestHash != oldHash {
		t.Fatalf("path head changed despite stale base: %#v", head)
	}
}

func TestCreateAndMergeChangesetFileRenameRecordsRenameIntent(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	homeSliceID := homeslice.IDForUsername("alice")
	oldPath := "alice/api/old.go"
	newPath := "alice/api/new.go"
	oldContent := []byte("package api\n\nfunc Old() {}\n")
	baseCommitHash := "cmt_loaded"

	slice := &models.Slice{ID: homeSliceID, Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	oldHash := mustWriteSliceManifest(t, ctx, st, homeSliceID, oldPath, oldContent)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(homeSliceID, oldPath),
		Path:     oldPath,
		Type:     "file",
		ParentID: homeSliceID,
		Hash:     oldHash,
		Size:     int64(len(oldContent)),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, oldPath, homeSliceID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           "alice",
		Path:             oldPath,
		PathVersion:      3,
		ContentHash:      oldHash,
		ManifestHash:     oldHash,
		SourceSliceID:    homeSliceID,
		SourceCommitHash: baseCommitHash,
	}}); err != nil {
		t.Fatalf("UpsertHomePathHeads failed: %v", err)
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: baseCommitHash,
		SliceID:    homeSliceID,
		Files: map[string]string{
			oldPath: oldHash,
		},
		Timestamp: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}
	metadata, err := st.GetSliceMetadata(ctx, homeSliceID)
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	metadata.HeadCommitHash = baseCommitHash
	if err := st.UpdateSliceMetadata(ctx, homeSliceID, metadata); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	mergeResp, err := srv.CreateAndMergeChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        homeSliceID,
		BaseCommitHash: baseCommitHash,
		ModifiedFiles:  []string{oldPath, newPath},
		Message:        "rename api file",
		FileRenames: []*slicev1.FileRename{{
			SourcePath:      oldPath,
			DestinationPath: newPath,
		}},
		ExpectedPathBases: []*filev1.PathBase{
			{
				Path:             oldPath,
				Exists:           true,
				ContentHash:      oldHash,
				PathVersion:      3,
				SourceSliceId:    homeSliceID,
				SourceCommitHash: baseCommitHash,
			},
			{
				Path:             newPath,
				Exists:           false,
				PathVersion:      0,
				SourceSliceId:    homeSliceID,
				SourceCommitHash: baseCommitHash,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateAndMergeChangeset failed: %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("merge status = %v, want success: %s", mergeResp.GetStatus(), mergeResp.GetMessage())
	}
	if _, err := st.GetEntryByPath(ctx, homeSliceID, oldPath); !errors.Is(err, storage.ErrEntryNotFound) {
		t.Fatalf("old path entry error = %v, want ErrEntryNotFound", err)
	}
	renamedContent, err := storage.ReadSliceFileContent(ctx, st, homeSliceID, newPath)
	if err != nil {
		t.Fatalf("ReadSliceFileContent(new path) failed: %v", err)
	}
	if got := string(renamedContent.Content); got != string(oldContent) {
		t.Fatalf("renamed content = %q, want %q", got, oldContent)
	}

	snapshot, err := st.GetChangesetSnapshot(ctx, mergeResp.GetChangesetId(), 0)
	if err != nil {
		t.Fatalf("GetChangesetSnapshot failed: %v", err)
	}
	if snapshot.RenameSources[newPath] != oldPath {
		t.Fatalf("snapshot rename sources = %#v, want %s -> %s", snapshot.RenameSources, newPath, oldPath)
	}
	if got := snapshot.FileHashes[newPath]; got != oldHash {
		t.Fatalf("snapshot new path hash = %q, want %q", got, oldHash)
	}
	if _, ok := snapshot.FileHashes[oldPath]; ok {
		t.Fatalf("snapshot should not retain deleted source path hash: %#v", snapshot.FileHashes)
	}

	event, err := st.GetMergeEventByChangeset(ctx, mergeResp.GetChangesetId())
	if err != nil {
		t.Fatalf("GetMergeEventByChangeset failed: %v", err)
	}
	updates := mergeEventPathUpdatesByPath(event)
	if update := updates[newPath]; update == nil || update.OldPath != oldPath || update.Deleted || update.ManifestHash != oldHash || update.BaseVersion != 0 || update.NewVersion != 1 {
		t.Fatalf("new path update = %#v, want rename from %s", update, oldPath)
	}
	if update := updates[oldPath]; update == nil || !update.Deleted || update.BaseVersion != 3 || update.NewVersion != 4 {
		t.Fatalf("old path update = %#v, want deleted source", update)
	}

	reviewResp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: mergeResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if len(reviewResp.GetChanges()) != 1 {
		t.Fatalf("review changes = %d, want 1: %#v", len(reviewResp.GetChanges()), reviewResp.GetChanges())
	}
	reviewChange := reviewResp.GetChanges()[0]
	if reviewChange.GetChangeType() != filev1.ChangeType_CHANGE_TYPE_RENAME || reviewChange.GetPath() != newPath || reviewChange.GetOldPath() != oldPath {
		t.Fatalf("review change = %#v, want rename %s -> %s", reviewChange, oldPath, newPath)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedHistoryProjections(waitCtx); err != nil {
		t.Fatalf("waitForQueuedHistoryProjections failed: %v", err)
	}
	history, err := st.GetFileHistory(ctx, homeSliceID, newPath, 10, "")
	if err != nil {
		t.Fatalf("GetFileHistory(new path) failed: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history entries = %d, want 1: %#v", len(history), history)
	}
	if history[0].ChangeType != models.ChangeTypeRename || history[0].OldPath != oldPath || history[0].Path != newPath || history[0].OldHash != oldHash || history[0].NewHash != oldHash {
		t.Fatalf("history rename = %#v, want rename record", history[0])
	}
}

func TestCreateAndMergeChangesetFileRenameRejectsStaleTargetBase(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	homeSliceID := homeslice.IDForUsername("alice")
	oldPath := "alice/api/old.go"
	newPath := "alice/api/new.go"
	baseCommitHash := "cmt_loaded"
	oldContent := []byte("package api\n\nfunc Old() {}\n")
	targetContent := []byte("package api\n\nfunc New() {}\n")

	slice := &models.Slice{ID: homeSliceID, Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	oldHash := mustWriteSliceManifest(t, ctx, st, homeSliceID, oldPath, oldContent)
	targetHash := mustWriteSliceManifest(t, ctx, st, homeSliceID, newPath, targetContent)
	for filePath, hash := range map[string]string{oldPath: oldHash, newPath: targetHash} {
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(homeSliceID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: homeSliceID,
			Hash:     hash,
		}); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", filePath, err)
		}
		if err := st.AddFileToSlice(ctx, filePath, homeSliceID); err != nil {
			t.Fatalf("AddFileToSlice(%s) failed: %v", filePath, err)
		}
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{
		{
			HomeID:           "alice",
			Path:             oldPath,
			PathVersion:      3,
			ContentHash:      oldHash,
			ManifestHash:     oldHash,
			SourceSliceID:    homeSliceID,
			SourceCommitHash: baseCommitHash,
		},
		{
			HomeID:           "alice",
			Path:             newPath,
			PathVersion:      1,
			ContentHash:      targetHash,
			ManifestHash:     targetHash,
			SourceSliceID:    homeSliceID,
			SourceCommitHash: "cmt_target",
		},
	}); err != nil {
		t.Fatalf("UpsertHomePathHeads failed: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.CreateAndMergeChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        homeSliceID,
		BaseCommitHash: baseCommitHash,
		ModifiedFiles:  []string{oldPath, newPath},
		Message:        "rename over stale target",
		FileRenames: []*slicev1.FileRename{{
			SourcePath:      oldPath,
			DestinationPath: newPath,
		}},
		ExpectedPathBases: []*filev1.PathBase{
			{
				Path:             oldPath,
				Exists:           true,
				ContentHash:      oldHash,
				PathVersion:      3,
				SourceSliceId:    homeSliceID,
				SourceCommitHash: baseCommitHash,
			},
			{
				Path:             newPath,
				Exists:           false,
				PathVersion:      0,
				SourceSliceId:    homeSliceID,
				SourceCommitHash: baseCommitHash,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateAndMergeChangeset returned error instead of stale response: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_STALE_BASE {
		t.Fatalf("merge status = %v, want stale base", resp.GetStatus())
	}
	if !strings.Contains(resp.GetMessage(), newPath) {
		t.Fatalf("stale message = %q, want target path", resp.GetMessage())
	}
	oldEntry, err := st.GetEntryByPath(ctx, homeSliceID, oldPath)
	if err != nil {
		t.Fatalf("old path should remain after stale rename: %v", err)
	}
	if oldEntry.Hash != oldHash {
		t.Fatalf("old path hash = %q, want %q", oldEntry.Hash, oldHash)
	}
	targetEntry, err := st.GetEntryByPath(ctx, homeSliceID, newPath)
	if err != nil {
		t.Fatalf("target path should remain after stale rename: %v", err)
	}
	if targetEntry.Hash != targetHash {
		t.Fatalf("target path hash = %q, want %q", targetEntry.Hash, targetHash)
	}
}

func TestCreateAndMergeChangesetDirectoryRenameExpandsAndRecordsMove(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	homeSliceID := homeslice.IDForUsername("alice")
	oldPrefix := "alice/app"
	newPrefix := "alice/lib"
	firstOldPath := "alice/app/main.go"
	secondOldPath := "alice/app/internal/util.go"
	firstNewPath := "alice/lib/main.go"
	secondNewPath := "alice/lib/internal/util.go"
	baseCommitHash := "cmt_loaded"

	slice := &models.Slice{ID: homeSliceID, Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	firstHash := mustWriteSliceManifest(t, ctx, st, homeSliceID, firstOldPath, []byte("package app\n"))
	secondHash := mustWriteSliceManifest(t, ctx, st, homeSliceID, secondOldPath, []byte("package internal\n"))
	for filePath, hash := range map[string]string{
		firstOldPath:  firstHash,
		secondOldPath: secondHash,
	} {
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(homeSliceID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: homeSliceID,
			Hash:     hash,
		}); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", filePath, err)
		}
		if err := st.AddFileToSlice(ctx, filePath, homeSliceID); err != nil {
			t.Fatalf("AddFileToSlice(%s) failed: %v", filePath, err)
		}
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{
		{
			HomeID:           "alice",
			Path:             firstOldPath,
			PathVersion:      3,
			ContentHash:      firstHash,
			ManifestHash:     firstHash,
			SourceSliceID:    homeSliceID,
			SourceCommitHash: baseCommitHash,
		},
		{
			HomeID:           "alice",
			Path:             secondOldPath,
			PathVersion:      4,
			ContentHash:      secondHash,
			ManifestHash:     secondHash,
			SourceSliceID:    homeSliceID,
			SourceCommitHash: baseCommitHash,
		},
	}); err != nil {
		t.Fatalf("UpsertHomePathHeads failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	mergeResp, err := srv.CreateAndMergeChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        homeSliceID,
		BaseCommitHash: baseCommitHash,
		ModifiedFiles:  []string{oldPrefix, newPrefix},
		Message:        "rename app directory",
		DirectoryRenames: []*slicev1.DirectoryRename{{
			SourcePath:      oldPrefix,
			DestinationPath: newPrefix,
		}},
	})
	if err != nil {
		t.Fatalf("CreateAndMergeChangeset failed: %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("merge status = %v, want success: %s", mergeResp.GetStatus(), mergeResp.GetMessage())
	}
	if _, err := st.GetEntryByPath(ctx, homeSliceID, firstOldPath); !errors.Is(err, storage.ErrEntryNotFound) {
		t.Fatalf("old first path entry error = %v, want ErrEntryNotFound", err)
	}
	firstContent, err := storage.ReadSliceFileContent(ctx, st, homeSliceID, firstNewPath)
	if err != nil {
		t.Fatalf("ReadSliceFileContent(first new path) failed: %v", err)
	}
	if got := string(firstContent.Content); got != "package app\n" {
		t.Fatalf("first renamed content = %q", got)
	}
	secondContent, err := storage.ReadSliceFileContent(ctx, st, homeSliceID, secondNewPath)
	if err != nil {
		t.Fatalf("ReadSliceFileContent(second new path) failed: %v", err)
	}
	if got := string(secondContent.Content); got != "package internal\n" {
		t.Fatalf("second renamed content = %q", got)
	}

	snapshot, err := st.GetChangesetSnapshot(ctx, mergeResp.GetChangesetId(), 0)
	if err != nil {
		t.Fatalf("GetChangesetSnapshot failed: %v", err)
	}
	if len(snapshot.DirectoryMoves) != 1 {
		t.Fatalf("snapshot directory moves = %#v, want 1", snapshot.DirectoryMoves)
	}
	move := snapshot.DirectoryMoves[0]
	if move.OldPrefix != oldPrefix || move.NewPrefix != newPrefix || move.BaseSubtreeVersion != 4 || move.BaseSubtreeDigest == "" {
		t.Fatalf("snapshot directory move = %#v, want %s -> %s", move, oldPrefix, newPrefix)
	}
	if snapshot.RenameSources[firstNewPath] != firstOldPath || snapshot.RenameSources[secondNewPath] != secondOldPath {
		t.Fatalf("snapshot rename sources = %#v", snapshot.RenameSources)
	}

	event, err := st.GetMergeEventByChangeset(ctx, mergeResp.GetChangesetId())
	if err != nil {
		t.Fatalf("GetMergeEventByChangeset failed: %v", err)
	}
	updates := mergeEventPathUpdatesByPath(event)
	if update := updates[oldPrefix]; update == nil || update.EntryType != "directory" || !update.Deleted || update.BaseVersion != 0 || update.NewVersion != 1 {
		t.Fatalf("old directory update = %#v", update)
	}
	if update := updates[newPrefix]; update == nil || update.EntryType != "directory" || update.Deleted || update.OldPath != oldPrefix || update.BaseVersion != 0 || update.NewVersion != 1 {
		t.Fatalf("new directory update = %#v", update)
	}
	if update := updates[firstNewPath]; update == nil || update.OldPath != firstOldPath || update.ManifestHash != firstHash {
		t.Fatalf("first file rename update = %#v", update)
	}
	moves, err := st.ListDirectoryMoves(ctx, "alice")
	if err != nil {
		t.Fatalf("ListDirectoryMoves failed: %v", err)
	}
	if len(moves) != 1 || moves[0].OldPrefix != oldPrefix || moves[0].NewPrefix != newPrefix || moves[0].MergeSeq != event.MergeSeq {
		t.Fatalf("stored directory moves = %#v", moves)
	}
}

func TestMergeChangesetDirectoryRenameRejectsStaleSubtree(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	homeSliceID := homeslice.IDForUsername("alice")
	oldPrefix := "alice/app"
	newPrefix := "alice/lib"
	oldPath := "alice/app/main.go"
	baseCommitHash := "cmt_loaded"

	slice := &models.Slice{ID: homeSliceID, Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	oldHash := mustWriteSliceManifest(t, ctx, st, homeSliceID, oldPath, []byte("package app\n"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(homeSliceID, oldPath),
		Path:     oldPath,
		Type:     "file",
		ParentID: homeSliceID,
		Hash:     oldHash,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, oldPath, homeSliceID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           "alice",
		Path:             oldPath,
		PathVersion:      3,
		ContentHash:      oldHash,
		ManifestHash:     oldHash,
		SourceSliceID:    homeSliceID,
		SourceCommitHash: baseCommitHash,
	}}); err != nil {
		t.Fatalf("UpsertHomePathHeads failed: %v", err)
	}

	srv := newSliceServiceServer(st)
	createResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        homeSliceID,
		BaseCommitHash: baseCommitHash,
		ModifiedFiles:  []string{oldPrefix, newPrefix},
		Message:        "rename app directory",
		DirectoryRenames: []*slicev1.DirectoryRename{{
			SourcePath:      oldPrefix,
			DestinationPath: newPrefix,
		}},
	})
	if err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}

	concurrentPath := "alice/app/new.go"
	concurrentHash := mustWriteSliceManifest(t, ctx, st, homeSliceID, concurrentPath, []byte("package app\n"))
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           "alice",
		Path:             concurrentPath,
		PathVersion:      1,
		ContentHash:      concurrentHash,
		ManifestHash:     concurrentHash,
		SourceSliceID:    homeSliceID,
		SourceCommitHash: "cmt_concurrent",
	}}); err != nil {
		t.Fatalf("UpsertHomePathHeads(concurrent) failed: %v", err)
	}

	mergeResp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
		ChangesetId: createResp.GetChangesetId(),
	})
	if err != nil {
		t.Fatalf("MergeChangeset returned error instead of stale response: %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_STALE_BASE {
		t.Fatalf("merge status = %v, want stale base", mergeResp.GetStatus())
	}
	if !strings.Contains(mergeResp.GetMessage(), oldPrefix) {
		t.Fatalf("stale message = %q, want source directory", mergeResp.GetMessage())
	}
}

func TestCreateAndMergeMountedHomeSliceUpdatesFileServiceHeadRead(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	homeSliceID := homeslice.IDForUsername("alice")
	filePath := "alice/public-demo/README.md"

	home := &models.Slice{ID: homeSliceID, Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, home); err != nil {
		t.Fatalf("CreateSlice(home) failed: %v", err)
	}
	mounted := &models.Slice{
		ID:          "alice-mounted-public-demo",
		Name:        "Public Demo",
		Owners:      []string{"alice"},
		CreatedBy:   "alice",
		ParentSlice: homeSliceID,
		Files:       []string{filePath},
		FolderMounts: []models.SliceFolderMount{{
			SourcePath: "alice/public-demo",
			Alias:      "public-demo",
		}},
	}
	if err := st.CreateSlice(ctx, mounted); err != nil {
		t.Fatalf("CreateSlice(mounted) failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, homeSliceID, filePath, []byte("old materialized\n"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(homeSliceID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: homeSliceID,
		Size:     int64(len("old materialized\n")),
	}); err != nil {
		t.Fatalf("AddEntry(home file) failed: %v", err)
	}
	if err := storage.UpdateHomePathHeadsFromSlicePaths(ctx, st, homeSliceID, "cmt_old", time.Now(), []string{filePath}); err != nil {
		t.Fatalf("UpdateHomePathHeadsFromSlicePaths failed: %v", err)
	}

	srv := NewService(st)
	mergeResp, err := srv.CreateAndMergeChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       mounted.ID,
		ModifiedFiles: []string{filePath},
		Message:       "Edit README",
		FileContents: []*slicev1.FileContentChange{{
			Path:    filePath,
			Content: []byte("new path head\n"),
		}},
	})
	if err != nil {
		t.Fatalf("CreateAndMergeChangeset failed: %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("merge status = %v, want success", mergeResp.GetStatus())
	}

	fileSvc := fileservice.NewService(st)
	fileResp, err := fileSvc.GetFile(ctx, &filev1.GetFileRequest{
		Path: filePath,
		Version: &filev1.GetFileRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: mounted.ID},
		},
	})
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	if got := string(fileResp.GetFile().GetContent()); got != "new path head\n" {
		t.Fatalf("GetFile content = %q, want merged path-head content", got)
	}
}

func TestMergeChangesetEnqueuesMergeRequestedRunWhenChecksMissing(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()
	srv, changesetID, runID := setupCIMergeGateChangeset(t, ctx, st, `
version: 1
triggers:
  merge_requested: true
defaults:
  runner_pool: default
  shell: bash
runner_pools:
  default:
    executor: shell
merge_policy:
  require_success: true
  allow_force_merge: true
`)
	if runID != "" {
		t.Fatalf("changeset_export disabled, got unexpected run %q", runID)
	}

	if _, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: changesetID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("MergeChangeset missing CI error = %v, want FailedPrecondition", err)
	}
	runs, err := st.ListCIRuns(ctx, storage.CIRunListFilter{ChangesetID: changesetID, Limit: 10})
	if err != nil {
		t.Fatalf("ListCIRuns failed: %v", err)
	}
	if len(runs) != 1 || runs[0].TriggerEvent != "merge_requested" || runs[0].Status != "queued" {
		t.Fatalf("merge_requested runs = %#v, want one queued run", runs)
	}
}

func setupCIMergeGateChangeset(t *testing.T, ctx context.Context, st storage.Storage, platformYAML string) (slicev1.SliceServiceServer, string, string) {
	t.Helper()
	homeSliceID := homeslice.IDForUsername("alice")
	slice := &models.Slice{ID: homeSliceID, Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, homeSliceID, "alice/.gitslice/ci.yaml", []byte(platformYAML))
	mustWriteSliceManifest(t, ctx, st, homeSliceID, "alice/api/.gs-ci.yaml", []byte(`
version: 1
name: api
watch:
  - "**/*.go"
jobs:
  unit:
    required: true
    commands:
      - go test ./...
`))
	mustWriteSliceManifest(t, ctx, st, homeSliceID, "alice/api/main.go", []byte("package main\nfunc main() {}\n"))

	srv := NewService(st)
	createResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        homeSliceID,
		BaseCommitHash: "base-ci-gate",
		ModifiedFiles:  []string{"alice/api/main.go"},
		Message:        "ci gate",
	})
	if err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	return srv, createResp.GetChangesetId(), createResp.GetCiRunId()
}

func passRequiredCIChecks(t *testing.T, ctx context.Context, st storage.Storage, runID string) {
	t.Helper()
	run, err := st.GetCIRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetCIRun(%s) failed: %v", runID, err)
	}
	checks, err := st.ListCIChecks(ctx, run.ChangesetID, run.ChangesetVersionID, run.PlanHash)
	if err != nil {
		t.Fatalf("ListCIChecks failed: %v", err)
	}
	if len(checks) == 0 {
		t.Fatal("expected CI checks")
	}
	now := time.Now()
	for _, check := range checks {
		if !check.Required {
			continue
		}
		check.Status = "passed"
		check.UpdatedAt = now
		if err := st.UpsertCICheck(ctx, check); err != nil {
			t.Fatalf("UpsertCICheck failed: %v", err)
		}
	}
	if err := st.UpdateCIRunStatus(ctx, runID, "success", &now); err != nil {
		t.Fatalf("UpdateCIRunStatus failed: %v", err)
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
		ID:             "chg_synthetic-snapshot",
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

func TestMergeEventPathHeadProjectionReadsRootWithoutProjectionWorker(t *testing.T) {
	ctx := context.Background()
	base := storage.NewInMemoryStorage()
	if err := base.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	filePath := "tester/docs/durable-worker.txt"
	source := &models.Slice{ID: "slice-durable-worker", Name: "slice-durable-worker", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := base.CreateSlice(ctx, source); err != nil {
		t.Fatalf("failed to create source slice: %v", err)
	}
	content := []byte("durable worker\n")
	manifestHash := mustWriteSliceManifest(t, ctx, base, source.ID, filePath, content)
	if err := base.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(source.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: source.ID,
		Hash:     manifestHash,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("failed to add source entry: %v", err)
	}
	event := &models.MergeEvent{
		HomeID:           "tester",
		ShardID:          0,
		MergeSeq:         1,
		EventID:          common.GenerateMergeEventID(),
		ChangesetID:      "chg_durable-worker",
		SourceSliceID:    source.ID,
		SourceCommitHash: "commit-durable-worker",
		Author:           "tester",
		Message:          "durable projection worker",
		TouchedPaths:     []string{filePath},
		PathUpdates: []*models.MergePathUpdate{{
			Path:             filePath,
			NewVersion:       1,
			ManifestHash:     manifestHash,
			SourceSliceID:    source.ID,
			SourceCommitHash: "commit-durable-worker",
		}},
		CreatedAt: time.Now(),
	}
	if err := base.AppendMergeEventWithPathHeadCAS(ctx, event); err != nil {
		t.Fatalf("failed to append merge event with path head projection: %v", err)
	}

	rootSlice, err := base.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("failed to load root slice: %v", err)
	}
	if containsString(rootSlice.Files, filePath) {
		t.Fatalf("expected root files to remain projection-only, got %#v", rootSlice.Files)
	}
	projted, err := storage.ReadSliceFileContent(ctx, base, "root", filePath)
	if err != nil {
		t.Fatalf("failed to read projected root file: %v", err)
	}
	if string(projted.Content) != string(content) || projted.Hash != manifestHash {
		t.Fatalf("projected content mismatch: got hash=%q content=%q", projted.Hash, projted.Content)
	}
}

func TestDurableHistoryProjectionWorkerProcessesMergeEvents(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()

	const filePath = "docs/history.txt"
	source := &models.Slice{ID: "slice-history-worker", Name: "slice-history-worker", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := base.CreateSlice(ctx, source); err != nil {
		t.Fatalf("failed to create source slice: %v", err)
	}
	contentV1 := []byte("history v1\n")
	hashV1 := mustWriteSliceManifest(t, ctx, base, source.ID, filePath, contentV1)
	if err := base.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(source.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: source.ID,
		Hash:     hashV1,
		Size:     int64(len(contentV1)),
	}); err != nil {
		t.Fatalf("failed to add source entry: %v", err)
	}

	event1 := &models.MergeEvent{
		HomeID:           "tester",
		ShardID:          0,
		MergeSeq:         1,
		EventID:          common.GenerateMergeEventID(),
		ChangesetID:      "chg_history-worker-1",
		SourceSliceID:    source.ID,
		SourceCommitHash: "commit-history-worker-1",
		Author:           "tester",
		Message:          "history add",
		TouchedPaths:     []string{filePath},
		PathUpdates: []*models.MergePathUpdate{{
			Path:             filePath,
			NewVersion:       1,
			ManifestHash:     hashV1,
			SourceSliceID:    source.ID,
			SourceCommitHash: "commit-history-worker-1",
		}},
		CreatedAt: time.Now().Add(-time.Second),
	}
	if err := base.AppendMergeEvent(ctx, event1); err != nil {
		t.Fatalf("failed to append first merge event: %v", err)
	}

	contentV2 := []byte("history v2\n")
	hashV2 := mustWriteSliceManifest(t, ctx, base, source.ID, filePath, contentV2)
	if err := base.UpdateEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(source.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: source.ID,
		Hash:     hashV2,
		Size:     int64(len(contentV2)),
	}); err != nil {
		t.Fatalf("failed to update source entry: %v", err)
	}

	event2 := &models.MergeEvent{
		HomeID:           "tester",
		ShardID:          0,
		MergeSeq:         2,
		EventID:          common.GenerateMergeEventID(),
		ChangesetID:      "chg_history-worker-2",
		SourceSliceID:    source.ID,
		SourceCommitHash: "commit-history-worker-2",
		Author:           "tester",
		Message:          "history modify",
		TouchedPaths:     []string{filePath},
		PathUpdates: []*models.MergePathUpdate{{
			Path:             filePath,
			BaseVersion:      1,
			NewVersion:       2,
			ManifestHash:     hashV2,
			SourceSliceID:    source.ID,
			SourceCommitHash: "commit-history-worker-2",
			ParentCommitHash: "commit-history-worker-1",
		}},
		CreatedAt: time.Now(),
	}
	if err := base.AppendMergeEvent(ctx, event2); err != nil {
		t.Fatalf("failed to append second merge event: %v", err)
	}

	srv := newSliceServiceServer(base)
	for i := 0; i < 2; i++ {
		processed, err := srv.processDurableHistoryProjectionOnce(ctx, DurableProjectionConfig{ShardCount: 1, BatchSize: 1})
		if err != nil {
			t.Fatalf("processDurableHistoryProjectionOnce %d failed: %v", i, err)
		}
		if !processed {
			t.Fatalf("expected history projection batch %d to process", i)
		}
	}

	commits, err := base.ListSliceCommits(ctx, source.ID, 10, "")
	if err != nil {
		t.Fatalf("ListSliceCommits failed: %v", err)
	}
	if len(commits) != 2 || commits[0].CommitHash != "commit-history-worker-2" || commits[1].CommitHash != "commit-history-worker-1" {
		t.Fatalf("expected projected commit history, got %#v", commits)
	}
	snapshot, err := base.GetCommitSnapshot(ctx, "commit-history-worker-2")
	if err != nil {
		t.Fatalf("GetCommitSnapshot failed: %v", err)
	}
	if got := snapshot.Files[filePath]; got != hashV2 {
		t.Fatalf("expected head snapshot hash %q, got %q", hashV2, got)
	}
	changes, err := base.GetCommitChanges(ctx, "commit-history-worker-2")
	if err != nil {
		t.Fatalf("GetCommitChanges failed: %v", err)
	}
	if len(changes) != 1 || changes[0].ChangeType != models.ChangeTypeModify || changes[0].OldHash != hashV1 || changes[0].NewHash != hashV2 {
		t.Fatalf("expected projected modify change %q -> %q, got %#v", hashV1, hashV2, changes)
	}
	offset, err := base.GetProjectionOffset(ctx, historyProjectionName, event2.ShardID)
	if err != nil {
		t.Fatalf("GetProjectionOffset failed: %v", err)
	}
	if offset.MergeSeq != event2.MergeSeq {
		t.Fatalf("expected history projection offset %d, got %d", event2.MergeSeq, offset.MergeSeq)
	}
}

func TestHistoryProjectionKeepsExistingSourceCommitSnapshot(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()
	srv := newSliceServiceServer(base)

	sourceCommit := "commit-existing-source-snapshot"
	appHash := strings.Repeat("a", 64)
	configHash := strings.Repeat("b", 64)
	if err := base.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: sourceCommit,
		SliceID:    "home_tester",
		Files: map[string]string{
			"tester/app/app.txt":       appHash,
			"tester/.gitslice/ci.yaml": configHash,
		},
		Timestamp: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot(source) failed: %v", err)
	}
	if err := base.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: "root-parent",
		SliceID:    "root",
		Files: map[string]string{
			"tester/.gitslice/ci.yaml": configHash,
		},
		Timestamp: time.Now().Add(-2 * time.Second),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot(parent) failed: %v", err)
	}

	event := &models.MergeEvent{
		HomeID:           "tester",
		ShardID:          0,
		MergeSeq:         1,
		EventID:          common.GenerateMergeEventID(),
		ChangesetID:      "chg_projection-existing-snapshot",
		SourceSliceID:    "home_tester",
		SourceCommitHash: sourceCommit,
		Author:           "tester",
		Message:          "project config",
		TouchedPaths:     []string{"tester/.gitslice/ci.yaml"},
		PathUpdates: []*models.MergePathUpdate{{
			Path:             "tester/.gitslice/ci.yaml",
			NewVersion:       1,
			ManifestHash:     configHash,
			SourceSliceID:    "home_tester",
			SourceCommitHash: sourceCommit,
		}},
		CreatedAt: time.Now(),
	}
	if _, err := srv.createCommitSnapshotFromMergeEvent(ctx, base, event, "root-parent", time.Now()); err != nil {
		t.Fatalf("createCommitSnapshotFromMergeEvent failed: %v", err)
	}

	snapshot, err := base.GetCommitSnapshot(ctx, sourceCommit)
	if err != nil {
		t.Fatalf("GetCommitSnapshot(source) failed: %v", err)
	}
	if got := snapshot.Files["tester/app/app.txt"]; got != appHash {
		t.Fatalf("source snapshot app hash = %q, want %q", got, appHash)
	}
	if got := len(snapshot.Files); got != 2 {
		t.Fatalf("source snapshot was overwritten: %#v", snapshot.Files)
	}
}

func TestMergeChangesetDurableProjectionFlagSkipsInProcessHistoryProjection(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()
	if err := base.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	filePath := "docs/durable-merge.txt"
	source := &models.Slice{ID: "slice-durable-merge", Name: "slice-durable-merge", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := base.CreateSlice(ctx, source); err != nil {
		t.Fatalf("failed to create source slice: %v", err)
	}
	content := []byte("durable merge\n")
	manifestHash := mustWriteSliceManifest(t, ctx, base, source.ID, filePath, content)
	if err := base.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(source.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: source.ID,
		Hash:     manifestHash,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("failed to add source entry: %v", err)
	}
	cs := &models.Changeset{
		ID:            "chg_durable-merge",
		SliceID:       source.ID,
		ModifiedFiles: []string{filePath},
		Status:        models.ChangesetStatusPending,
		Author:        "tester",
		CreatedAt:     time.Now(),
	}
	if err := base.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	srv := newSliceServiceServer(base)
	srv.durableProjection = true
	mustCreateChangesetSnapshot(t, ctx, srv, cs)
	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", resp.GetStatus())
	}
	rootSlice, err := base.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("failed to load root slice: %v", err)
	}
	if containsString(rootSlice.Files, filePath) {
		t.Fatalf("expected root files to stay projection-only, got %#v", rootSlice.Files)
	}
	projected, err := storage.ReadSliceFileContent(ctx, base, "root", filePath)
	if err != nil {
		t.Fatalf("failed to read projected root file: %v", err)
	}
	if string(projected.Content) != string(content) || projected.Hash != manifestHash {
		t.Fatalf("projected root content mismatch: hash=%q content=%q", projected.Hash, projected.Content)
	}

	event, err := base.GetMergeEventByChangeset(ctx, cs.ID)
	if err != nil {
		t.Fatalf("expected merge event for durable projection: %v", err)
	}
	statusResp, err := srv.GetProjectionStatus(ctx, &slicev1.GetProjectionStatusRequest{
		ProjectionName: historyProjectionName,
		ShardId:        event.ShardID,
		MergeSeq:       event.MergeSeq,
	})
	if err != nil {
		t.Fatalf("GetProjectionStatus before history worker failed: %v", err)
	}
	if statusResp.GetState() != slicev1.ProjectionState_PROJECTION_STATE_PENDING {
		t.Fatalf("expected history projection pending before durable worker, got %v", statusResp.GetState())
	}
	processed, err := srv.processDurableHistoryProjectionOnce(ctx, DurableProjectionConfig{})
	if err != nil {
		t.Fatalf("processDurableHistoryProjectionOnce failed: %v", err)
	}
	if !processed {
		t.Fatalf("expected durable history projection worker to process merge event")
	}
	offset, err := base.GetProjectionOffset(ctx, historyProjectionName, event.ShardID)
	if err != nil {
		t.Fatalf("failed to load projection offset: %v", err)
	}
	if offset.MergeSeq != event.MergeSeq {
		t.Fatalf("expected projection offset %d, got %d", event.MergeSeq, offset.MergeSeq)
	}
	changes, err := base.GetCommitChanges(ctx, resp.GetNewCommitHash())
	if err != nil {
		t.Fatalf("GetCommitChanges failed: %v", err)
	}
	if len(changes) != 1 || changes[0].Path != filePath || changes[0].NewHash != manifestHash {
		t.Fatalf("expected projected commit change, got %#v", changes)
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
	if err := srv.waitForQueuedProjections(waitCtx); err != nil {
		t.Fatalf("timed out waiting for root projection queue: %v", err)
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
	if err := srv.waitForQueuedProjections(waitCtx); err != nil {
		t.Fatalf("timed out waiting for root projection queue: %v", err)
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
	if err := srv.waitForQueuedProjections(waitCtx); err != nil {
		t.Fatalf("timed out waiting for root projection queue: %v", err)
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
