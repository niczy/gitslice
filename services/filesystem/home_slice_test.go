package filesystemservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/searchindex"
	"github.com/niczy/gitslice/internal/storage"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func parseFilesystemMetricMap(t *testing.T, raw string) map[string]int64 {
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

func TestHomeSliceAbsolutePathLifecycle(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	homeID := homeslice.IDForUsername("tester")

	if _, err := svc.MakeDirectory(ctx, &filesystemv1.MakeDirectoryRequest{
		WorkspaceId: homeID,
		Path:        "/tester/docs/guides",
	}); err != nil {
		t.Fatalf("MakeDirectory failed: %v", err)
	}

	writeResp, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: homeID,
		Path:        "/tester/docs/guides/README.md",
		Content:     []byte("hello home\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if writeResp.GetWorkspaceId() != homeID || writeResp.GetPath() != "/tester/docs/guides/README.md" {
		t.Fatalf("unexpected write response: %#v", writeResp)
	}

	writeManyResp, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: homeID,
		Files: []*filesystemv1.WriteFileInput{
			{Path: "/tester/src/main.py", Content: []byte("print('hello')\n")},
			{Path: "/tester/src/lib/helper.py", Content: []byte("def helper():\n    return 'hello'\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteFiles failed: %v", err)
	}
	if len(writeManyResp.GetFiles()) != 2 || writeManyResp.GetFiles()[0].GetPath() != "/tester/src/main.py" {
		t.Fatalf("unexpected writeMany response: %#v", writeManyResp)
	}

	readResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: homeID,
		Path:        "/tester/docs/guides/README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if got, want := string(readResp.GetContent()), "hello home\n"; got != want {
		t.Fatalf("content mismatch: got %q want %q", got, want)
	}

	rootList, err := svc.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: homeID,
		Path:        "/tester",
	})
	if err != nil {
		t.Fatalf("ListDirectory(root) failed: %v", err)
	}
	if len(rootList.GetEntries()) != 2 || rootList.GetEntries()[0].GetPath() != "/tester/docs" || rootList.GetEntries()[1].GetPath() != "/tester/src" {
		t.Fatalf("unexpected home root entries: %#v", rootList.GetEntries())
	}

	readMany, err := svc.ReadFiles(ctx, &filesystemv1.ReadFilesRequest{
		WorkspaceId: homeID,
		Paths:       []string{"/tester/src/main.py", "/tester/missing.py"},
	})
	if err != nil {
		t.Fatalf("ReadFiles failed: %v", err)
	}
	if len(readMany.GetFiles()) != 2 || readMany.GetFiles()[0].GetPath() != "/tester/src/main.py" {
		t.Fatalf("unexpected ReadFiles response: %#v", readMany)
	}
	if !readMany.GetFiles()[0].GetFound() || readMany.GetFiles()[1].GetFound() {
		t.Fatalf("unexpected ReadFiles found flags: %#v", readMany.GetFiles())
	}

	statResp, err := svc.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: homeID,
		Path:        "/tester/docs/guides/README.md",
	})
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if !statResp.GetExists() || statResp.GetEntry().GetPath() != "/tester/docs/guides/README.md" {
		t.Fatalf("unexpected stat response: %#v", statResp)
	}

	existsResp, err := svc.Exists(ctx, &filesystemv1.ExistsRequest{
		WorkspaceId: homeID,
		Path:        "/tester/src/main.py",
	})
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !existsResp.GetExists() {
		t.Fatal("expected /tester/src/main.py to exist")
	}

	globResp, err := svc.Glob(ctx, &filesystemv1.GlobRequest{
		WorkspaceId: homeID,
		Pattern:     "/tester/**/*.py",
	})
	if err != nil {
		t.Fatalf("Glob failed: %v", err)
	}
	if len(globResp.GetPaths()) != 2 || globResp.GetPaths()[0] != "/tester/src/lib/helper.py" || globResp.GetPaths()[1] != "/tester/src/main.py" {
		t.Fatalf("unexpected glob response: %#v", globResp)
	}

	waitForSearchIndexForTest(t, svc)
	searchResp, err := svc.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId: homeID,
		Query:       "hello",
		Glob:        "/tester/**/*.py",
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(searchResp.GetMatches()) != 2 || searchResp.GetMatches()[0].GetPath() != "/tester/src/lib/helper.py" {
		t.Fatalf("unexpected search response: %#v", searchResp)
	}

	if _, err := svc.DeleteFile(ctx, &filesystemv1.DeleteFileRequest{
		WorkspaceId: homeID,
		Path:        "/tester/docs/guides/README.md",
	}); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}
}

func TestHomeSliceRejectsForeignAndRelativePaths(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	svc := NewService(st)
	homeID := homeslice.IDForUsername("tester")

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: homeID,
		Path:        "tester/README.md",
		Content:     []byte("bad\n"),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for relative path, got %v", err)
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: homeID,
		Path:        "/other/README.md",
		Content:     []byte("bad\n"),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for foreign path, got %v", err)
	}
}

func TestHomeSliceRegexSearchUsesIndexedWorkspaceArtifact(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	homeID := homeslice.IDForUsername("tester")

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: homeID,
		Path:        "/tester/docs/README.md",
		Content:     []byte("hello agent world\n"),
	}); err != nil {
		t.Fatalf("WriteFile(README) failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: homeID,
		Path:        "/tester/notes/todo.txt",
		Content:     []byte("hello todo\n"),
	}); err != nil {
		t.Fatalf("WriteFile(todo) failed: %v", err)
	}

	waitForSearchIndexForTest(t, svc)
	searchResp, err := svc.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId: homeID,
		Query:       "hello.*world",
		Glob:        "/tester/**/*.md",
		Regex:       true,
	})
	if err != nil {
		t.Fatalf("Search(regex) failed: %v", err)
	}
	if len(searchResp.GetMatches()) != 1 || searchResp.GetMatches()[0].GetPath() != "/tester/docs/README.md" {
		t.Fatalf("unexpected regex search response: %#v", searchResp)
	}

	if err := st.PutWorkspaceSearchArtifact(context.Background(), homeID, searchindex.CurrentArtifactVersion, []byte("corrupt")); err != nil {
		t.Fatalf("PutWorkspaceSearchArtifact(corrupt) failed: %v", err)
	}
	before := snapshotFilesystemMetrics()
	searchResp, err = svc.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId: homeID,
		Query:       "hello.*world",
		Glob:        "/tester/**/*.md",
		Regex:       true,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected Search(regex corrupt artifact) to report index not ready, got response=%#v err=%v", searchResp, err)
	}
	waitForSearchIndexForTest(t, svc)
	searchResp, err = svc.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId: homeID,
		Query:       "hello.*world",
		Glob:        "/tester/**/*.md",
		Regex:       true,
	})
	if err != nil {
		t.Fatalf("Search(regex repaired index) failed: %v", err)
	}
	if len(searchResp.GetMatches()) != 1 || searchResp.GetMatches()[0].GetPath() != "/tester/docs/README.md" {
		t.Fatalf("unexpected regex repaired index response: %#v", searchResp)
	}
	after := snapshotFilesystemMetrics()

	beforeArtifactLoads := parseFilesystemMetricMap(t, before.SearchArtifactLoads)
	afterArtifactLoads := parseFilesystemMetricMap(t, after.SearchArtifactLoads)
	if afterArtifactLoads["rebuilt"] <= beforeArtifactLoads["rebuilt"] {
		t.Fatalf("expected rebuilt artifact load metric to increase, before=%v after=%v", beforeArtifactLoads, afterArtifactLoads)
	}

	beforeCandidates := parseFilesystemMetricMap(t, before.SearchCandidateCount)
	afterCandidates := parseFilesystemMetricMap(t, after.SearchCandidateCount)
	if afterCandidates["regex_indexed"] <= beforeCandidates["regex_indexed"] {
		t.Fatalf("expected indexed candidate metric to increase, before=%v after=%v", beforeCandidates, afterCandidates)
	}

	beforeFallbacks := parseFilesystemMetricMap(t, before.SearchFallbackTotal)
	afterFallbacks := parseFilesystemMetricMap(t, after.SearchFallbackTotal)
	if afterFallbacks["artifact_error"] != beforeFallbacks["artifact_error"] {
		t.Fatalf("expected corrupt workspace artifact to be repaired without artifact_error fallback, before=%v after=%v", beforeFallbacks, afterFallbacks)
	}
}

func TestHomeSliceStreamReadAndWrite(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	client, conn := newFilesystemTestClient(t, st)
	defer conn.Close()

	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "User tester")
	homeID := homeslice.IDForUsername("tester")
	original := bytes.Repeat([]byte("home-stream-"), 16384)

	writeStream, err := client.StreamWrite(authCtx)
	if err != nil {
		t.Fatalf("StreamWrite start failed: %v", err)
	}
	if err := writeStream.Send(&filesystemv1.StreamWriteRequest{
		Chunk: &filesystemv1.StreamWriteRequest_Metadata{
			Metadata: &filesystemv1.StreamWriteMetadata{
				WorkspaceId: homeID,
				Path:        "/tester/large.bin",
			},
		},
	}); err != nil {
		t.Fatalf("send metadata: %v", err)
	}
	for offset := 0; offset < len(original); offset += 32768 {
		end := offset + 32768
		if end > len(original) {
			end = len(original)
		}
		if err := writeStream.Send(&filesystemv1.StreamWriteRequest{
			Chunk: &filesystemv1.StreamWriteRequest_Content{
				Content: append([]byte(nil), original[offset:end]...),
			},
		}); err != nil {
			t.Fatalf("send content: %v", err)
		}
	}
	writeResp, err := writeStream.CloseAndRecv()
	if err != nil {
		t.Fatalf("StreamWrite failed: %v", err)
	}
	if writeResp.GetWorkspaceId() != homeID || writeResp.GetPath() != "/tester/large.bin" {
		t.Fatalf("unexpected StreamWrite response: %#v", writeResp)
	}

	readStream, err := client.StreamRead(authCtx, &filesystemv1.StreamReadRequest{
		WorkspaceId: homeID,
		Path:        "/tester/large.bin",
		ChunkSize:   65536,
	})
	if err != nil {
		t.Fatalf("StreamRead failed: %v", err)
	}

	var streamed bytes.Buffer
	for {
		resp, err := readStream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("recv stream chunk: %v", err)
		}
		if resp.GetPath() != "/tester/large.bin" {
			t.Fatalf("unexpected stream path: %#v", resp)
		}
		if _, err := streamed.Write(resp.GetContent()); err != nil {
			t.Fatalf("buffer stream chunk: %v", err)
		}
	}
	if !bytes.Equal(streamed.Bytes(), original) {
		t.Fatal("streamed content mismatch")
	}
}
