package workflow

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/searchindex"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func TestSliceStateConsistencyVerification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	username := workflowUsername(t)
	homeID := homeslice.IDForUsername(username)
	if _, err := homeslice.EnsureUserHomeSlice(ctx, testStorage, username); err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}

	sliceClient := newSliceClient(t)
	fileClient := newFileClient(t)
	fsClient := newFilesystemClient(t)

	appPath := path.Join(username, "consistency", "app.go")
	initialContent := []byte("package app\nconst A = 0\nconst B = 0\n")
	initial := createAndMergeFileTreeChange(t, ctx, sliceClient, homeID, "initial consistency file", []*slicev1.FileContentChange{{
		Path:    appPath,
		Content: initialContent,
	}}, nil, nil, nil)

	initialRead := readConsistencyFile(t, ctx, fileClient, homeID, appPath)
	initialBase := clonePathBase(initialRead.GetFile().GetPathBase())
	if initialBase.GetPathVersion() <= 0 || initialBase.GetContentHash() == "" {
		t.Fatalf("initial file path base is incomplete: %#v", initialBase)
	}
	assertCommitHistoryIncludesOnly(t, ctx, sliceClient, homeID, initialRead.GetStateToken(), initial.GetNewCommitHash())

	other := createAndMergeFileTreeChange(t, ctx, sliceClient, homeID, "other actor edit", []*slicev1.FileContentChange{{
		Path:    appPath,
		Content: []byte("package app\nconst A = 0\nconst B = 2\n"),
	}}, []*filev1.PathBase{initialBase}, nil, nil)

	stale := createAndMergeFileTreeChangeAllowStale(t, ctx, sliceClient, homeID, "stale browser edit", []*slicev1.FileContentChange{{
		Path:    appPath,
		Content: []byte("package app\nconst A = 1\nconst B = 0\n"),
	}}, []*filev1.PathBase{initialBase}, nil, nil)
	if stale.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_STALE_BASE {
		t.Fatalf("stale edit status = %v, want STALE_BASE", stale.GetStatus())
	}
	afterStale := readConsistencyFile(t, ctx, fileClient, homeID, appPath)
	if got := string(afterStale.GetFile().GetContent()); got != "package app\nconst A = 0\nconst B = 2\n" {
		t.Fatalf("stale edit changed file content: %q", got)
	}
	assertCommitHistoryIncludesOnly(t, ctx, sliceClient, homeID, initialRead.GetStateToken(), initial.GetNewCommitHash())

	autoBaseRead := readConsistencyFile(t, ctx, fileClient, homeID, appPath)
	autoBase := clonePathBase(autoBaseRead.GetFile().GetPathBase())
	pendingAuto, err := sliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        homeID,
		BaseCommitHash: autoBaseRead.GetStateToken().GetSliceHash(),
		Message:        "pending disjoint edit",
		ModifiedFiles:  []string{appPath},
		FileContents: []*slicev1.FileContentChange{{
			Path:    appPath,
			Content: []byte("package app\nconst A = 1\nconst B = 2\n"),
		}},
		ExpectedPathBases: []*filev1.PathBase{autoBase},
	})
	if err != nil {
		t.Fatalf("CreateChangeset(pending auto merge) failed: %v", err)
	}
	third := createAndMergeFileTreeChange(t, ctx, sliceClient, homeID, "third actor edit", []*slicev1.FileContentChange{{
		Path:    appPath,
		Content: []byte("package app\nconst A = 0\nconst B = 3\n"),
	}}, []*filev1.PathBase{autoBase}, nil, nil)
	autoMerge, err := sliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: pendingAuto.GetChangesetId()})
	if err != nil {
		t.Fatalf("MergeChangeset(auto merge) failed: %v", err)
	}
	if autoMerge.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("auto merge status = %v, want success: %s", autoMerge.GetStatus(), autoMerge.GetMessage())
	}
	afterAutoMerge := readConsistencyFile(t, ctx, fileClient, homeID, appPath)
	if got := string(afterAutoMerge.GetFile().GetContent()); got != "package app\nconst A = 1\nconst B = 3\n" {
		t.Fatalf("auto-merged content = %q", got)
	}
	assertCommitHistoryContains(t, ctx, sliceClient, homeID, afterAutoMerge.GetStateToken(), autoMerge.GetNewCommitHash(), third.GetNewCommitHash(), other.GetNewCommitHash(), initial.GetNewCommitHash())

	conflictBaseRead := readConsistencyFile(t, ctx, fileClient, homeID, appPath)
	conflictBase := clonePathBase(conflictBaseRead.GetFile().GetPathBase())
	pendingConflict, err := sliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        homeID,
		BaseCommitHash: conflictBaseRead.GetStateToken().GetSliceHash(),
		Message:        "pending overlapping edit",
		ModifiedFiles:  []string{appPath},
		FileContents: []*slicev1.FileContentChange{{
			Path:    appPath,
			Content: []byte("package app\nconst A = 5\nconst B = 3\n"),
		}},
		ExpectedPathBases: []*filev1.PathBase{conflictBase},
	})
	if err != nil {
		t.Fatalf("CreateChangeset(pending conflict) failed: %v", err)
	}
	conflictingHead := createAndMergeFileTreeChange(t, ctx, sliceClient, homeID, "conflicting head edit", []*slicev1.FileContentChange{{
		Path:    appPath,
		Content: []byte("package app\nconst A = 6\nconst B = 3\n"),
	}}, []*filev1.PathBase{conflictBase}, nil, nil)
	conflictResp, err := sliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: pendingConflict.GetChangesetId()})
	if err != nil {
		t.Fatalf("MergeChangeset(conflict) failed: %v", err)
	}
	if conflictResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_STALE_BASE || len(conflictResp.GetConflicts()) == 0 {
		t.Fatalf("conflict merge response = %#v, want stale with conflict artifacts", conflictResp)
	}
	conflicts, err := sliceClient.ListChangesetConflicts(ctx, &slicev1.ListChangesetConflictsRequest{ChangesetId: pendingConflict.GetChangesetId()})
	if err != nil {
		t.Fatalf("ListChangesetConflicts failed: %v", err)
	}
	if conflicts.GetTotalConflicts() != 1 || conflicts.GetConflicts()[0].GetPath() != appPath {
		t.Fatalf("stored conflict artifacts = %#v", conflicts.GetConflicts())
	}

	renameOldPath := path.Join(username, "consistency", "rename-old.txt")
	renameNewPath := path.Join(username, "consistency", "rename-new.txt")
	renameSeed := createAndMergeFileTreeChange(t, ctx, sliceClient, homeID, "seed rename file", []*slicev1.FileContentChange{{
		Path:    renameOldPath,
		Content: []byte("rename me\n"),
	}}, nil, nil, nil)
	renameRead := readConsistencyFile(t, ctx, fileClient, homeID, renameOldPath)
	renameResp := createAndMergeFileTreeChange(t, ctx, sliceClient, homeID, "rename file", nil, []*filev1.PathBase{
		clonePathBase(renameRead.GetFile().GetPathBase()),
		{
			Path:             renameNewPath,
			Exists:           false,
			PathVersion:      0,
			SourceSliceId:    homeID,
			SourceCommitHash: renameSeed.GetNewCommitHash(),
		},
	}, []*slicev1.FileRename{{
		SourcePath:      renameOldPath,
		DestinationPath: renameNewPath,
	}}, nil)
	renamedRead := readConsistencyFile(t, ctx, fileClient, homeID, renameNewPath)
	if !bytes.Equal(renamedRead.GetFile().GetContent(), []byte("rename me\n")) {
		t.Fatalf("renamed content = %q", string(renamedRead.GetFile().GetContent()))
	}
	assertMergeEventHasRename(t, ctx, renameResp.GetChangesetId(), renameOldPath, renameNewPath)

	dirOld := path.Join(username, "consistency", "old-dir")
	dirNew := path.Join(username, "consistency", "new-dir")
	dirFile := path.Join(dirOld, "note.txt")
	createAndMergeFileTreeChange(t, ctx, sliceClient, homeID, "seed directory move", []*slicev1.FileContentChange{{
		Path:    dirFile,
		Content: []byte("move me\n"),
	}}, nil, nil, nil)
	moveResp := createAndMergeFileTreeChange(t, ctx, sliceClient, homeID, "move directory", nil, nil, nil, []*slicev1.DirectoryRename{{
		SourcePath:      dirOld,
		DestinationPath: dirNew,
	}})
	movedRead := readConsistencyFile(t, ctx, fileClient, homeID, path.Join(dirNew, "note.txt"))
	if !bytes.Equal(movedRead.GetFile().GetContent(), []byte("move me\n")) {
		t.Fatalf("moved directory file content = %q", string(movedRead.GetFile().GetContent()))
	}
	assertDirectoryMoveRecorded(t, ctx, username, moveResp.GetMergeSeq(), dirOld, dirNew)

	currentRead := readConsistencyFile(t, ctx, fileClient, homeID, appPath)
	if err := testStorage.DeleteWorkspaceSearchArtifact(ctx, homeID, searchindex.CurrentArtifactVersion); err != nil && err != storage.ErrEntryNotFound {
		t.Fatalf("DeleteWorkspaceSearchArtifact failed: %v", err)
	}
	var searchResp *filesystemv1.SearchResponse
	if err := waitForCondition(5*time.Second, 100*time.Millisecond, func() (bool, error) {
		resp, err := fsClient.Search(ctx, &filesystemv1.SearchRequest{
			WorkspaceId:        homeID,
			Query:              "A = 6",
			RequiredStateToken: currentRead.GetStateToken(),
		})
		if err != nil {
			return false, err
		}
		if resp.GetStatus() == filesystemv1.SearchStatus_SEARCH_STATUS_READY {
			searchResp = resp
			return true, nil
		}
		if resp.GetStatus() == filesystemv1.SearchStatus_SEARCH_STATUS_INDEX_NOT_READY {
			return false, nil
		}
		return false, fmt.Errorf("unexpected search status %v: %s", resp.GetStatus(), resp.GetMessage())
	}); err != nil {
		t.Fatalf("search index did not catch up to required state: %v", err)
	}
	if searchResp.GetIndexedStateToken().GetSliceHash() != currentRead.GetStateToken().GetSliceHash() {
		t.Fatalf("indexed token = %#v, want slice hash %s", searchResp.GetIndexedStateToken(), currentRead.GetStateToken().GetSliceHash())
	}
	if !searchMatchesPath(searchResp.GetMatches(), "/"+appPath) {
		t.Fatalf("search matches = %#v, want %s", searchResp.GetMatches(), "/"+appPath)
	}
	if !strings.HasPrefix(strings.TrimSpace(conflictingHead.GetNewCommitHash()), "cmt_") {
		t.Fatalf("unexpected conflicting head commit hash %q", conflictingHead.GetNewCommitHash())
	}
}

func createAndMergeFileTreeChange(
	t *testing.T,
	ctx context.Context,
	client slicev1.SliceServiceClient,
	sliceID string,
	message string,
	contents []*slicev1.FileContentChange,
	expectedBases []*filev1.PathBase,
	fileRenames []*slicev1.FileRename,
	directoryRenames []*slicev1.DirectoryRename,
) *slicev1.MergeChangesetResponse {
	t.Helper()
	resp := createAndMergeFileTreeChangeWithStatus(t, ctx, client, sliceID, message, contents, expectedBases, fileRenames, directoryRenames)
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("CreateAndMergeChangeset(%s) status = %v, want success: %s", message, resp.GetStatus(), resp.GetMessage())
	}
	return resp
}

func createAndMergeFileTreeChangeAllowStale(
	t *testing.T,
	ctx context.Context,
	client slicev1.SliceServiceClient,
	sliceID string,
	message string,
	contents []*slicev1.FileContentChange,
	expectedBases []*filev1.PathBase,
	fileRenames []*slicev1.FileRename,
	directoryRenames []*slicev1.DirectoryRename,
) *slicev1.MergeChangesetResponse {
	t.Helper()
	resp := createAndMergeFileTreeChangeWithStatus(t, ctx, client, sliceID, message, contents, expectedBases, fileRenames, directoryRenames)
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS && resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_STALE_BASE {
		t.Fatalf("CreateAndMergeChangeset(%s) status = %v: %s", message, resp.GetStatus(), resp.GetMessage())
	}
	return resp
}

func createAndMergeFileTreeChangeWithStatus(
	t *testing.T,
	ctx context.Context,
	client slicev1.SliceServiceClient,
	sliceID string,
	message string,
	contents []*slicev1.FileContentChange,
	expectedBases []*filev1.PathBase,
	fileRenames []*slicev1.FileRename,
	directoryRenames []*slicev1.DirectoryRename,
) *slicev1.MergeChangesetResponse {
	t.Helper()
	modified := make([]string, 0, len(contents)+len(fileRenames)*2+len(directoryRenames)*2)
	for _, content := range contents {
		if content != nil {
			modified = append(modified, content.GetPath())
		}
	}
	for _, rename := range fileRenames {
		if rename != nil {
			modified = append(modified, rename.GetSourcePath(), rename.GetDestinationPath())
		}
	}
	for _, rename := range directoryRenames {
		if rename != nil {
			modified = append(modified, rename.GetSourcePath(), rename.GetDestinationPath())
		}
	}
	resp, err := client.CreateAndMergeChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:           sliceID,
		ModifiedFiles:     modified,
		Message:           message,
		FileContents:      contents,
		ExpectedPathBases: expectedBases,
		FileRenames:       fileRenames,
		DirectoryRenames:  directoryRenames,
	})
	if err != nil {
		t.Fatalf("CreateAndMergeChangeset(%s) failed: %v", message, err)
	}
	return resp
}

func readConsistencyFile(t *testing.T, ctx context.Context, client filev1.FileServiceClient, sliceID, filePath string) *filev1.GetFileResponse {
	t.Helper()
	resp, err := client.GetFile(ctx, &filev1.GetFileRequest{
		Path: filePath,
		Version: &filev1.GetFileRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: sliceID},
		},
	})
	if err != nil {
		t.Fatalf("GetFile(%s) failed: %v", filePath, err)
	}
	if resp.GetFile().GetPathBase() == nil || resp.GetStateToken() == nil {
		t.Fatalf("GetFile(%s) missing base/token: %#v", filePath, resp)
	}
	return resp
}

func clonePathBase(base *filev1.PathBase) *filev1.PathBase {
	if base == nil {
		return nil
	}
	return &filev1.PathBase{
		Path:             base.GetPath(),
		Exists:           base.GetExists(),
		ContentHash:      base.GetContentHash(),
		PathVersion:      base.GetPathVersion(),
		SourceSliceId:    base.GetSourceSliceId(),
		SourceCommitHash: base.GetSourceCommitHash(),
		MoveGeneration:   base.GetMoveGeneration(),
	}
}

func assertCommitHistoryIncludesOnly(t *testing.T, ctx context.Context, client slicev1.SliceServiceClient, sliceID string, token *filev1.SliceStateToken, hashes ...string) {
	t.Helper()
	resp, err := client.GetSliceCommits(ctx, &slicev1.CommitHistoryRequest{SliceId: sliceID, Limit: 20, StateToken: token})
	if err != nil {
		t.Fatalf("GetSliceCommits failed: %v", err)
	}
	if len(resp.GetCommits()) != len(hashes) {
		t.Fatalf("commit history = %#v, want exactly %v", resp.GetCommits(), hashes)
	}
	assertCommitHistoryContainsHashes(t, resp.GetCommits(), hashes...)
}

func assertCommitHistoryContains(t *testing.T, ctx context.Context, client slicev1.SliceServiceClient, sliceID string, token *filev1.SliceStateToken, hashes ...string) {
	t.Helper()
	resp, err := client.GetSliceCommits(ctx, &slicev1.CommitHistoryRequest{SliceId: sliceID, Limit: 50, StateToken: token})
	if err != nil {
		t.Fatalf("GetSliceCommits failed: %v", err)
	}
	assertCommitHistoryContainsHashes(t, resp.GetCommits(), hashes...)
}

func assertCommitHistoryContainsHashes(t *testing.T, commits []*slicev1.CommitInfo, hashes ...string) {
	t.Helper()
	seen := make(map[string]bool, len(commits))
	for _, commit := range commits {
		seen[commit.GetCommitHash()] = true
	}
	for _, hash := range hashes {
		if !seen[hash] {
			t.Fatalf("commit history = %#v, missing %s", commits, hash)
		}
	}
}

func assertMergeEventHasRename(t *testing.T, ctx context.Context, changesetID, oldPath, newPath string) {
	t.Helper()
	store, ok := testStorage.(storage.MergeEventStore)
	if !ok {
		t.Fatalf("storage does not implement MergeEventStore")
	}
	event, err := store.GetMergeEventByChangeset(ctx, changesetID)
	if err != nil {
		t.Fatalf("GetMergeEventByChangeset failed: %v", err)
	}
	updates := make(map[string]*storageCompatibleMergePathUpdate, len(event.PathUpdates))
	for _, update := range event.PathUpdates {
		if update != nil {
			updates[update.Path] = &storageCompatibleMergePathUpdate{OldPath: update.OldPath, Deleted: update.Deleted}
		}
	}
	if update := updates[newPath]; update == nil || update.OldPath != oldPath || update.Deleted {
		t.Fatalf("new rename update = %#v in event %#v", update, event.PathUpdates)
	}
	if update := updates[oldPath]; update == nil || !update.Deleted {
		t.Fatalf("old rename update = %#v in event %#v", update, event.PathUpdates)
	}
}

type storageCompatibleMergePathUpdate struct {
	OldPath string
	Deleted bool
}

func assertDirectoryMoveRecorded(t *testing.T, ctx context.Context, homeID string, mergeSeq int64, oldPrefix, newPrefix string) {
	t.Helper()
	store, ok := testStorage.(storage.DirectoryMoveStore)
	if !ok {
		t.Fatalf("storage does not implement DirectoryMoveStore")
	}
	moves, err := store.ListDirectoryMoves(ctx, homeID)
	if err != nil {
		t.Fatalf("ListDirectoryMoves failed: %v", err)
	}
	for _, move := range moves {
		if move.OldPrefix == oldPrefix && move.NewPrefix == newPrefix && move.MergeSeq == mergeSeq {
			return
		}
	}
	t.Fatalf("directory moves = %#v, missing %s -> %s at seq %d", moves, oldPrefix, newPrefix, mergeSeq)
}

func searchMatchesPath(matches []*filesystemv1.SearchMatch, want string) bool {
	for _, match := range matches {
		if match.GetPath() == want {
			return true
		}
	}
	return false
}
