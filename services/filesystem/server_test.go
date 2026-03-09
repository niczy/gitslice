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
