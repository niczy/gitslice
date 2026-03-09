package filesystemservice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/storage"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func authContext(username string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User "+username))
}

func TestWorkspaceFileLifecycle(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)

	workspace, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-demo",
		Name:        "Demo Workspace",
		Description: "test workspace",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if got, want := workspace.GetWorkspaceId(), "ws-demo"; got != want {
		t.Fatalf("workspace_id mismatch: got %q want %q", got, want)
	}

	if _, err := svc.MakeDirectory(ctx, &filesystemv1.MakeDirectoryRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides",
	}); err != nil {
		t.Fatalf("MakeDirectory failed: %v", err)
	}

	writeResp, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
		Content:     []byte("hello world\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if writeResp.GetCommitHash() == "" {
		t.Fatalf("expected commit hash after write")
	}

	readResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if got, want := string(readResp.GetContent()), "hello world\n"; got != want {
		t.Fatalf("content mismatch: got %q want %q", got, want)
	}

	rootList, err := svc.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: "ws-demo",
	})
	if err != nil {
		t.Fatalf("ListDirectory(root) failed: %v", err)
	}
	if len(rootList.GetEntries()) != 1 || rootList.GetEntries()[0].GetPath() != "docs" {
		t.Fatalf("unexpected root entries: %#v", rootList.GetEntries())
	}

	guidesList, err := svc.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides",
	})
	if err != nil {
		t.Fatalf("ListDirectory(docs/guides) failed: %v", err)
	}
	if len(guidesList.GetEntries()) != 1 || guidesList.GetEntries()[0].GetPath() != "docs/guides/README.md" {
		t.Fatalf("unexpected guides entries: %#v", guidesList.GetEntries())
	}

	statResp, err := svc.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
	})
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if !statResp.GetExists() || statResp.GetEntry().GetType() != filesystemv1.EntryType_ENTRY_TYPE_FILE {
		t.Fatalf("unexpected stat response: %#v", statResp)
	}

	existsResp, err := svc.Exists(ctx, &filesystemv1.ExistsRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
	})
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !existsResp.GetExists() {
		t.Fatalf("expected file to exist")
	}

	if _, err := svc.DeleteFile(ctx, &filesystemv1.DeleteFileRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
	}); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}

	existsAfterDelete, err := svc.Exists(ctx, &filesystemv1.ExistsRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
	})
	if err != nil {
		t.Fatalf("Exists after delete failed: %v", err)
	}
	if existsAfterDelete.GetExists() {
		t.Fatalf("expected file to be deleted")
	}

	workspaces, err := svc.ListWorkspaces(ctx, &filesystemv1.ListWorkspacesRequest{})
	if err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}
	if workspaces.GetTotal() != 2 {
		t.Fatalf("expected root + created workspace, got total=%d", workspaces.GetTotal())
	}
}

func TestWorkspaceAccessControl(t *testing.T) {
	ownerCtx := authContext("owner")
	otherCtx := authContext("other")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ownerCtx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "private-ws",
		Name:        "Private",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	if _, err := svc.ReadFile(otherCtx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "private-ws",
		Path:        "secret.txt",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}

	if _, err := svc.WriteFile(otherCtx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "private-ws",
		Path:        "secret.txt",
		Content:     []byte("nope"),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied on write, got %v", err)
	}
}

func TestWorkspaceStreamReadAndWrite(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	client, conn := newFilesystemTestClient(t, st)
	defer conn.Close()

	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "User tester")
	if _, err := client.CreateWorkspace(authCtx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-stream",
		Name:        "ws-stream",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	original := bytes.Repeat([]byte("stream-data-"), 32768)
	writeStream, err := client.StreamWrite(authCtx)
	if err != nil {
		t.Fatalf("StreamWrite start failed: %v", err)
	}
	if err := writeStream.Send(&filesystemv1.StreamWriteRequest{
		Chunk: &filesystemv1.StreamWriteRequest_Metadata{
			Metadata: &filesystemv1.StreamWriteMetadata{
				WorkspaceId: "ws-stream",
				Path:        "large.bin",
			},
		},
	}); err != nil {
		t.Fatalf("send metadata: %v", err)
	}
	for offset := 0; offset < len(original); offset += 65536 {
		end := offset + 65536
		if end > len(original) {
			end = len(original)
		}
		if err := writeStream.Send(&filesystemv1.StreamWriteRequest{
			Chunk: &filesystemv1.StreamWriteRequest_Content{
				Content: append([]byte(nil), original[offset:end]...),
			},
		}); err != nil {
			t.Fatalf("send content chunk: %v", err)
		}
	}
	writeResp, err := writeStream.CloseAndRecv()
	if err != nil {
		t.Fatalf("StreamWrite failed: %v", err)
	}
	if writeResp.GetCommitHash() == "" || writeResp.GetHash() == "" || writeResp.GetSize() != int64(len(original)) {
		t.Fatalf("unexpected StreamWrite response: %#v", writeResp)
	}

	readStream, err := client.StreamRead(authCtx, &filesystemv1.StreamReadRequest{
		WorkspaceId: "ws-stream",
		Path:        "large.bin",
		ChunkSize:   131072,
	})
	if err != nil {
		t.Fatalf("StreamRead failed: %v", err)
	}

	var streamed bytes.Buffer
	chunks := 0
	for {
		resp, err := readStream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("recv stream chunk: %v", err)
		}
		chunks++
		if resp.GetPath() != "large.bin" || resp.GetWorkspaceId() != "ws-stream" {
			t.Fatalf("unexpected stream chunk metadata: %#v", resp)
		}
		if _, err := streamed.Write(resp.GetContent()); err != nil {
			t.Fatalf("buffer stream chunk: %v", err)
		}
	}
	if chunks < 2 {
		t.Fatalf("expected multiple stream chunks, got %d", chunks)
	}
	if !bytes.Equal(streamed.Bytes(), original) {
		t.Fatalf("streamed content mismatch")
	}
}

func TestWorkspaceStreamWriteRequiresMetadataFirst(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	client, conn := newFilesystemTestClient(t, st)
	defer conn.Close()

	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "User tester")
	if _, err := client.CreateWorkspace(authCtx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-stream-invalid",
		Name:        "ws-stream-invalid",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	writeStream, err := client.StreamWrite(authCtx)
	if err != nil {
		t.Fatalf("StreamWrite start failed: %v", err)
	}
	if err := writeStream.Send(&filesystemv1.StreamWriteRequest{
		Chunk: &filesystemv1.StreamWriteRequest_Content{Content: []byte("oops")},
	}); err != nil {
		t.Fatalf("send content: %v", err)
	}
	_, err = writeStream.CloseAndRecv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestWorkspaceFileContentsAreIsolatedPerWorkspace(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	for _, workspaceID := range []string{"ws-one", "ws-two"} {
		if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
			WorkspaceId: workspaceID,
			Name:        workspaceID,
		}); err != nil {
			t.Fatalf("CreateWorkspace(%s) failed: %v", workspaceID, err)
		}
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-one",
		Path:        "README.md",
		Content:     []byte("workspace one\n"),
	}); err != nil {
		t.Fatalf("WriteFile(ws-one) failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-two",
		Path:        "README.md",
		Content:     []byte("workspace two\n"),
	}); err != nil {
		t.Fatalf("WriteFile(ws-two) failed: %v", err)
	}

	readOne, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-one",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(ws-one) failed: %v", err)
	}
	readTwo, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-two",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(ws-two) failed: %v", err)
	}

	if got, want := string(readOne.GetContent()), "workspace one\n"; got != want {
		t.Fatalf("ws-one content mismatch: got %q want %q", got, want)
	}
	if got, want := string(readTwo.GetContent()), "workspace two\n"; got != want {
		t.Fatalf("ws-two content mismatch: got %q want %q", got, want)
	}
}

func newFilesystemTestClient(t *testing.T, st storage.Storage) (filesystemv1.FilesystemServiceClient, *grpc.ClientConn) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := grpc.NewServer()
	RegisterGRPCServer(srv, st)
	go func() {
		if err := srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Logf("grpc serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		srv.GracefulStop()
	})

	conn, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc server: %v", err)
	}
	return filesystemv1.NewFilesystemServiceClient(conn), conn
}

func TestWorkspaceBatchAndPosixOperations(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-batch",
		Name:        "Batch Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	writeResp, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: "ws-batch",
		Files: []*filesystemv1.WriteFileInput{
			{Path: "README.md", Content: []byte("batch workspace\n")},
			{Path: "src/main.py", Content: []byte("print('hello')\n")},
			{Path: "src/lib/helper.py", Content: []byte("def helper():\n    return 'hello'\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteFiles failed: %v", err)
	}
	if writeResp.GetCommitHash() == "" {
		t.Fatalf("expected batch commit hash")
	}
	if len(writeResp.GetFiles()) != 3 {
		t.Fatalf("expected 3 write results, got %d", len(writeResp.GetFiles()))
	}

	commits, err := st.ListSliceCommits(ctx, "ws-batch", 10, "")
	if err != nil {
		t.Fatalf("ListSliceCommits failed: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("expected create + batch commit, got %d", len(commits))
	}

	readMany, err := svc.ReadFiles(ctx, &filesystemv1.ReadFilesRequest{
		WorkspaceId: "ws-batch",
		Paths:       []string{"src/main.py", "missing.py"},
	})
	if err != nil {
		t.Fatalf("ReadFiles failed: %v", err)
	}
	if len(readMany.GetFiles()) != 2 {
		t.Fatalf("expected 2 read results, got %d", len(readMany.GetFiles()))
	}
	if !readMany.GetFiles()[0].GetFound() || string(readMany.GetFiles()[0].GetContent()) != "print('hello')\n" {
		t.Fatalf("unexpected found read result: %#v", readMany.GetFiles()[0])
	}
	if readMany.GetFiles()[1].GetFound() || readMany.GetFiles()[1].GetError() != "file not found" {
		t.Fatalf("unexpected missing read result: %#v", readMany.GetFiles()[1])
	}

	globResp, err := svc.Glob(ctx, &filesystemv1.GlobRequest{
		WorkspaceId: "ws-batch",
		Pattern:     "src/**/*.py",
	})
	if err != nil {
		t.Fatalf("Glob failed: %v", err)
	}
	if got, want := len(globResp.GetPaths()), 2; got != want {
		t.Fatalf("glob count mismatch: got %d want %d (%#v)", got, want, globResp.GetPaths())
	}
	if globResp.GetPaths()[0] != "src/lib/helper.py" || globResp.GetPaths()[1] != "src/main.py" {
		t.Fatalf("unexpected glob results: %#v", globResp.GetPaths())
	}

	searchResp, err := svc.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId: "ws-batch",
		Query:       "hello",
		Glob:        "src/**/*.py",
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if got := len(searchResp.GetMatches()); got != 2 {
		t.Fatalf("expected 2 search matches, got %d", got)
	}
	if searchResp.GetMatches()[0].GetPath() != "src/lib/helper.py" || searchResp.GetMatches()[1].GetPath() != "src/main.py" {
		t.Fatalf("unexpected search results: %#v", searchResp.GetMatches())
	}

	copyResp, err := svc.CopyFile(ctx, &filesystemv1.CopyFileRequest{
		WorkspaceId:     "ws-batch",
		SourcePath:      "src/main.py",
		DestinationPath: "src/main_copy.py",
	})
	if err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}
	if copyResp.GetCommitHash() == "" {
		t.Fatalf("expected copy commit hash")
	}
	copyRead, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-batch",
		Path:        "src/main_copy.py",
	})
	if err != nil {
		t.Fatalf("ReadFile(copy) failed: %v", err)
	}
	if got, want := string(copyRead.GetContent()), "print('hello')\n"; got != want {
		t.Fatalf("copy content mismatch: got %q want %q", got, want)
	}

	moveResp, err := svc.MoveFile(ctx, &filesystemv1.MoveFileRequest{
		WorkspaceId:     "ws-batch",
		SourcePath:      "src/main_copy.py",
		DestinationPath: "archive/main_copy.py",
	})
	if err != nil {
		t.Fatalf("MoveFile failed: %v", err)
	}
	if moveResp.GetCommitHash() == "" {
		t.Fatalf("expected move commit hash")
	}

	movedRead, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-batch",
		Path:        "archive/main_copy.py",
	})
	if err != nil {
		t.Fatalf("ReadFile(moved) failed: %v", err)
	}
	if got, want := string(movedRead.GetContent()), "print('hello')\n"; got != want {
		t.Fatalf("moved content mismatch: got %q want %q", got, want)
	}
	if _, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-batch",
		Path:        "src/main_copy.py",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected moved source to be missing, got %v", err)
	}

	commits, err = st.ListSliceCommits(ctx, "ws-batch", 10, "")
	if err != nil {
		t.Fatalf("ListSliceCommits(after copy/move) failed: %v", err)
	}
	if len(commits) != 4 {
		t.Fatalf("expected create + batch + copy + move commits, got %d", len(commits))
	}
}

func TestWorkspaceSnapshotsAndRestore(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-snapshots",
		Name:        "Snapshot Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-snapshots",
		Path:        "README.md",
		Content:     []byte("v1\n"),
	}); err != nil {
		t.Fatalf("WriteFile(v1) failed: %v", err)
	}

	snapshotResp, err := svc.Snapshot(ctx, &filesystemv1.SnapshotRequest{
		WorkspaceId: "ws-snapshots",
		Message:     "checkpoint one",
	})
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if snapshotResp.GetSnapshot() == nil || snapshotResp.GetSnapshot().GetSnapshotId() == "" {
		t.Fatalf("expected snapshot metadata, got %#v", snapshotResp)
	}
	if snapshotResp.GetSnapshot().GetMessage() != "checkpoint one" {
		t.Fatalf("unexpected snapshot message: %#v", snapshotResp.GetSnapshot())
	}
	if snapshotResp.GetSnapshot().GetFileCount() != 1 {
		t.Fatalf("expected snapshot file count 1, got %#v", snapshotResp.GetSnapshot())
	}

	if _, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: "ws-snapshots",
		Files: []*filesystemv1.WriteFileInput{
			{Path: "README.md", Content: []byte("v2\n")},
			{Path: "notes/todo.txt", Content: []byte("later\n")},
		},
	}); err != nil {
		t.Fatalf("WriteFiles(v2) failed: %v", err)
	}

	listResp, err := svc.ListSnapshots(ctx, &filesystemv1.ListSnapshotsRequest{
		WorkspaceId: "ws-snapshots",
		Limit:       4,
	})
	if err != nil {
		t.Fatalf("ListSnapshots failed: %v", err)
	}
	if len(listResp.GetSnapshots()) != 4 {
		t.Fatalf("expected 4 snapshots, got %#v", listResp.GetSnapshots())
	}
	if listResp.GetSnapshots()[0].GetFileCount() != 2 {
		t.Fatalf("expected newest snapshot to include 2 files, got %#v", listResp.GetSnapshots()[0])
	}
	if listResp.GetSnapshots()[1].GetSnapshotId() != snapshotResp.GetSnapshot().GetSnapshotId() {
		t.Fatalf("expected explicit snapshot second, got %#v", listResp.GetSnapshots())
	}

	olderResp, err := svc.ListSnapshots(ctx, &filesystemv1.ListSnapshotsRequest{
		WorkspaceId:    "ws-snapshots",
		Limit:          2,
		FromSnapshotId: snapshotResp.GetSnapshot().GetSnapshotId(),
	})
	if err != nil {
		t.Fatalf("ListSnapshots(from) failed: %v", err)
	}
	if len(olderResp.GetSnapshots()) != 2 {
		t.Fatalf("expected 2 older snapshots, got %#v", olderResp.GetSnapshots())
	}
	if olderResp.GetSnapshots()[0].GetMessage() != "write README.md" {
		t.Fatalf("unexpected paginated snapshot list: %#v", olderResp.GetSnapshots())
	}

	restoreResp, err := svc.RestoreSnapshot(ctx, &filesystemv1.RestoreSnapshotRequest{
		WorkspaceId: "ws-snapshots",
		SnapshotId:  snapshotResp.GetSnapshot().GetSnapshotId(),
	})
	if err != nil {
		t.Fatalf("RestoreSnapshot failed: %v", err)
	}
	if restoreResp.GetSnapshot() == nil || restoreResp.GetSnapshot().GetSnapshotId() == "" {
		t.Fatalf("expected restore snapshot metadata, got %#v", restoreResp)
	}
	if restoreResp.GetRestoredSnapshotId() != snapshotResp.GetSnapshot().GetSnapshotId() {
		t.Fatalf("unexpected restored snapshot id: %#v", restoreResp)
	}
	if restoreResp.GetSnapshot().GetFileCount() != 1 {
		t.Fatalf("expected restored workspace to have 1 file, got %#v", restoreResp.GetSnapshot())
	}

	readResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-snapshots",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(after restore) failed: %v", err)
	}
	if got, want := string(readResp.GetContent()), "v1\n"; got != want {
		t.Fatalf("restored content mismatch: got %q want %q", got, want)
	}
	if _, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-snapshots",
		Path:        "notes/todo.txt",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected restored workspace to remove notes/todo.txt, got %v", err)
	}
}

func TestWorkspaceDiff(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-diff",
		Name:        "Diff Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-diff",
		Path:        "README.md",
		Content:     []byte("v1\n"),
	}); err != nil {
		t.Fatalf("WriteFile(v1) failed: %v", err)
	}

	snapshotResp, err := svc.Snapshot(ctx, &filesystemv1.SnapshotRequest{
		WorkspaceId: "ws-diff",
		Message:     "checkpoint one",
	})
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	writeResp, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: "ws-diff",
		Files: []*filesystemv1.WriteFileInput{
			{Path: "README.md", Content: []byte("v2\n")},
			{Path: "notes/todo.txt", Content: []byte("later\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteFiles(v2) failed: %v", err)
	}

	diffResp, err := svc.Diff(ctx, &filesystemv1.DiffRequest{
		WorkspaceId:    "ws-diff",
		FromSnapshotId: snapshotResp.GetSnapshot().GetSnapshotId(),
		ToSnapshotId:   writeResp.GetCommitHash(),
		IncludePatches: true,
	})
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	if diffResp.GetSummary().GetFilesAdded() != 1 || diffResp.GetSummary().GetFilesModified() != 1 || diffResp.GetSummary().GetFilesDeleted() != 0 {
		t.Fatalf("unexpected diff summary counts: %#v", diffResp.GetSummary())
	}
	if diffResp.GetSummary().GetLinesAdded() != 2 || diffResp.GetSummary().GetLinesDeleted() != 1 {
		t.Fatalf("unexpected diff summary line counts: %#v", diffResp.GetSummary())
	}
	if len(diffResp.GetFiles()) != 2 {
		t.Fatalf("expected 2 file diffs, got %#v", diffResp.GetFiles())
	}

	byPath := make(map[string]*filesystemv1.FileDiff, len(diffResp.GetFiles()))
	for _, file := range diffResp.GetFiles() {
		byPath[file.GetPath()] = file
	}
	readmeDiff := byPath["README.md"]
	if readmeDiff == nil || readmeDiff.GetChangeType() != filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_MODIFY {
		t.Fatalf("expected README.md modify diff, got %#v", diffResp.GetFiles())
	}
	if !strings.Contains(readmeDiff.GetPatch(), "-v1") || !strings.Contains(readmeDiff.GetPatch(), "+v2") {
		t.Fatalf("unexpected README diff patch: %q", readmeDiff.GetPatch())
	}

	notesDiff := byPath["notes/todo.txt"]
	if notesDiff == nil || notesDiff.GetChangeType() != filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_ADD {
		t.Fatalf("expected notes/todo.txt add diff, got %#v", diffResp.GetFiles())
	}
	if !strings.Contains(notesDiff.GetPatch(), "+later") {
		t.Fatalf("unexpected notes diff patch: %q", notesDiff.GetPatch())
	}
}

func TestWorkspaceFork(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-source",
		Name:        "Source Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	writeResp, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: "ws-source",
		Files: []*filesystemv1.WriteFileInput{
			{Path: "README.md", Content: []byte("v1\n")},
			{Path: "docs/info.txt", Content: []byte("source info\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteFiles(source) failed: %v", err)
	}

	forkResp, err := svc.Fork(ctx, &filesystemv1.ForkRequest{
		WorkspaceId:     "ws-source",
		ForkWorkspaceId: "ws-fork",
		Name:            "Fork Workspace",
	})
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}
	if forkResp.GetWorkspace().GetWorkspaceId() != "ws-fork" {
		t.Fatalf("unexpected fork workspace: %#v", forkResp.GetWorkspace())
	}
	if forkResp.GetSourceWorkspaceId() != "ws-source" || forkResp.GetSourceSnapshotId() != writeResp.GetCommitHash() {
		t.Fatalf("unexpected fork response metadata: %#v", forkResp)
	}
	if forkResp.GetWorkspace().GetFileCount() != 2 {
		t.Fatalf("expected 2 forked files, got %#v", forkResp.GetWorkspace())
	}

	readmeResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-fork",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(fork README) failed: %v", err)
	}
	if got, want := string(readmeResp.GetContent()), "v1\n"; got != want {
		t.Fatalf("unexpected fork README content: got %q want %q", got, want)
	}

	infoResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-fork",
		Path:        "docs/info.txt",
	})
	if err != nil {
		t.Fatalf("ReadFile(fork docs/info.txt) failed: %v", err)
	}
	if got, want := string(infoResp.GetContent()), "source info\n"; got != want {
		t.Fatalf("unexpected fork info content: got %q want %q", got, want)
	}

	rootList, err := svc.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: "ws-fork",
	})
	if err != nil {
		t.Fatalf("ListDirectory(fork root) failed: %v", err)
	}
	if len(rootList.GetEntries()) != 2 {
		t.Fatalf("expected 2 root entries in fork, got %#v", rootList.GetEntries())
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-source",
		Path:        "README.md",
		Content:     []byte("v2\n"),
	}); err != nil {
		t.Fatalf("WriteFile(source v2) failed: %v", err)
	}

	forkReadmeAfterSourceWrite, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-fork",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(fork README after source write) failed: %v", err)
	}
	if got, want := string(forkReadmeAfterSourceWrite.GetContent()), "v1\n"; got != want {
		t.Fatalf("fork README should remain independent: got %q want %q", got, want)
	}
}

func TestWorkspaceMerge(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-merge-main",
		Name:        "Merge Main",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	baseWrite, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: "ws-merge-main",
		Files: []*filesystemv1.WriteFileInput{
			{Path: "README.md", Content: []byte("v1\n")},
			{Path: "docs/info.txt", Content: []byte("base info\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteFiles(base) failed: %v", err)
	}

	if _, err := svc.Fork(ctx, &filesystemv1.ForkRequest{
		WorkspaceId:     "ws-merge-main",
		ForkWorkspaceId: "ws-merge-fork",
		Name:            "Merge Fork",
	}); err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	targetOnlyWrite, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-merge-main",
		Path:        "LOCAL.md",
		Content:     []byte("keep me\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile(target-only) failed: %v", err)
	}

	sourceWrite, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: "ws-merge-fork",
		Files: []*filesystemv1.WriteFileInput{
			{Path: "README.md", Content: []byte("v2\n")},
			{Path: "notes/todo.txt", Content: []byte("later\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteFiles(source changes) failed: %v", err)
	}

	mergeResp, err := svc.Merge(ctx, &filesystemv1.MergeRequest{
		WorkspaceId:       "ws-merge-main",
		SourceWorkspaceId: "ws-merge-fork",
	})
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if mergeResp.GetStatus() != filesystemv1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %#v", mergeResp)
	}
	if mergeResp.GetBaseWorkspaceId() != "ws-merge-main" || mergeResp.GetBaseSnapshotId() != baseWrite.GetCommitHash() {
		t.Fatalf("unexpected inferred base: %#v", mergeResp)
	}
	if mergeResp.GetTargetSnapshotId() != targetOnlyWrite.GetCommitHash() || mergeResp.GetSourceSnapshotId() != sourceWrite.GetCommitHash() {
		t.Fatalf("unexpected merge snapshot ids: %#v", mergeResp)
	}
	if mergeResp.GetSummary().GetFilesAdded() != 1 || mergeResp.GetSummary().GetFilesModified() != 1 || mergeResp.GetSummary().GetFilesDeleted() != 0 {
		t.Fatalf("unexpected merge summary counts: %#v", mergeResp.GetSummary())
	}
	if mergeResp.GetSummary().GetLinesAdded() != 2 || mergeResp.GetSummary().GetLinesDeleted() != 1 {
		t.Fatalf("unexpected merge summary lines: %#v", mergeResp.GetSummary())
	}
	if len(mergeResp.GetMergedPaths()) != 2 {
		t.Fatalf("expected 2 merged paths, got %#v", mergeResp.GetMergedPaths())
	}

	readmeResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-merge-main",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(merged README) failed: %v", err)
	}
	if got, want := string(readmeResp.GetContent()), "v2\n"; got != want {
		t.Fatalf("unexpected merged README content: got %q want %q", got, want)
	}

	localResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-merge-main",
		Path:        "LOCAL.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(target-only LOCAL.md) failed: %v", err)
	}
	if got, want := string(localResp.GetContent()), "keep me\n"; got != want {
		t.Fatalf("unexpected LOCAL.md content after merge: got %q want %q", got, want)
	}

	notesResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-merge-main",
		Path:        "notes/todo.txt",
	})
	if err != nil {
		t.Fatalf("ReadFile(merged notes/todo.txt) failed: %v", err)
	}
	if got, want := string(notesResp.GetContent()), "later\n"; got != want {
		t.Fatalf("unexpected notes/todo.txt content after merge: got %q want %q", got, want)
	}
}

func TestWorkspaceMergeConflicts(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-merge-conflict-main",
		Name:        "Merge Conflict Main",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	baseWrite, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-merge-conflict-main",
		Path:        "README.md",
		Content:     []byte("base\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile(base) failed: %v", err)
	}

	if _, err := svc.Fork(ctx, &filesystemv1.ForkRequest{
		WorkspaceId:     "ws-merge-conflict-main",
		ForkWorkspaceId: "ws-merge-conflict-fork",
		Name:            "Merge Conflict Fork",
	}); err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-merge-conflict-main",
		Path:        "README.md",
		Content:     []byte("main change\n"),
	}); err != nil {
		t.Fatalf("WriteFile(main change) failed: %v", err)
	}

	sourceWrite, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-merge-conflict-fork",
		Path:        "README.md",
		Content:     []byte("fork change\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile(fork change) failed: %v", err)
	}

	mergeResp, err := svc.Merge(ctx, &filesystemv1.MergeRequest{
		WorkspaceId:       "ws-merge-conflict-main",
		SourceWorkspaceId: "ws-merge-conflict-fork",
	})
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if mergeResp.GetStatus() != filesystemv1.MergeStatus_MERGE_STATUS_CONFLICT {
		t.Fatalf("expected merge conflict, got %#v", mergeResp)
	}
	if mergeResp.GetCommitHash() != "" {
		t.Fatalf("expected no commit hash on conflict, got %#v", mergeResp)
	}
	if mergeResp.GetBaseWorkspaceId() != "ws-merge-conflict-main" || mergeResp.GetBaseSnapshotId() != baseWrite.GetCommitHash() {
		t.Fatalf("unexpected inferred conflict base: %#v", mergeResp)
	}
	if mergeResp.GetSourceSnapshotId() != sourceWrite.GetCommitHash() {
		t.Fatalf("unexpected source snapshot on conflict: %#v", mergeResp)
	}
	if len(mergeResp.GetConflicts()) != 1 {
		t.Fatalf("expected 1 merge conflict, got %#v", mergeResp.GetConflicts())
	}
	conflict := mergeResp.GetConflicts()[0]
	if conflict.GetPath() != "README.md" {
		t.Fatalf("unexpected conflict path: %#v", conflict)
	}
	if conflict.GetSourceChangeType() != filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_MODIFY || conflict.GetTargetChangeType() != filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_MODIFY {
		t.Fatalf("unexpected conflict change types: %#v", conflict)
	}
	if !strings.Contains(conflict.GetSourcePatch(), "+fork change") || !strings.Contains(conflict.GetTargetPatch(), "+main change") {
		t.Fatalf("unexpected conflict patches: %#v", conflict)
	}

	readmeResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-merge-conflict-main",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(main after conflict) failed: %v", err)
	}
	if got, want := string(readmeResp.GetContent()), "main change\n"; got != want {
		t.Fatalf("target workspace should remain unchanged on conflict: got %q want %q", got, want)
	}
}

func TestWorkspaceListAndResolveConflicts(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-conflict-main",
		Name:        "Conflict Main",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-conflict-main",
		Path:        "README.md",
		Content:     []byte("shared\n"),
	}); err != nil {
		t.Fatalf("WriteFile(shared) failed: %v", err)
	}

	if _, err := svc.Fork(ctx, &filesystemv1.ForkRequest{
		WorkspaceId:     "ws-conflict-main",
		ForkWorkspaceId: "ws-conflict-fork",
		Name:            "Conflict Fork",
	}); err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	listResp, err := svc.ListConflicts(ctx, &filesystemv1.ListConflictsRequest{
		WorkspaceId: "ws-conflict-main",
	})
	if err != nil {
		t.Fatalf("ListConflicts failed: %v", err)
	}
	if len(listResp.GetConflicts()) != 1 {
		t.Fatalf("expected 1 conflict, got %#v", listResp.GetConflicts())
	}
	conflict := listResp.GetConflicts()[0]
	if conflict.GetPath() != "README.md" {
		t.Fatalf("unexpected conflict path: %#v", conflict)
	}
	if len(conflict.GetWorkspaceIds()) != 2 || conflict.GetWorkspaceIds()[0] != "ws-conflict-fork" || conflict.GetWorkspaceIds()[1] != "ws-conflict-main" {
		t.Fatalf("unexpected conflict workspaces: %#v", conflict)
	}

	resolveResp, err := svc.ResolveConflict(ctx, &filesystemv1.ResolveConflictRequest{
		WorkspaceId:          "ws-conflict-main",
		Path:                 "README.md",
		PreferredWorkspaceId: "ws-conflict-main",
	})
	if err != nil {
		t.Fatalf("ResolveConflict failed: %v", err)
	}
	if resolveResp.GetConflict().GetPath() != "README.md" {
		t.Fatalf("unexpected resolved conflict payload: %#v", resolveResp.GetConflict())
	}
	if len(resolveResp.GetConflict().GetWorkspaceIds()) != 1 || resolveResp.GetConflict().GetWorkspaceIds()[0] != "ws-conflict-main" {
		t.Fatalf("unexpected resolved conflict owners: %#v", resolveResp.GetConflict())
	}

	listAfterResolve, err := svc.ListConflicts(ctx, &filesystemv1.ListConflictsRequest{
		WorkspaceId: "ws-conflict-main",
	})
	if err != nil {
		t.Fatalf("ListConflicts after resolve failed: %v", err)
	}
	if len(listAfterResolve.GetConflicts()) != 0 {
		t.Fatalf("expected conflicts to be resolved, got %#v", listAfterResolve.GetConflicts())
	}

	forkReadme, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-conflict-fork",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(fork after resolve) failed: %v", err)
	}
	if got, want := string(forkReadme.GetContent()), "shared\n"; got != want {
		t.Fatalf("fork content should remain readable after resolve: got %q want %q", got, want)
	}
}
