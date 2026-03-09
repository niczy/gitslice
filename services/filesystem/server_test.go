package filesystemservice

import (
	"context"
	"testing"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/storage"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	"google.golang.org/grpc/codes"
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
