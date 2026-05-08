package filesystemservice

import (
	"context"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/gitrepo"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type repoSubtreeEntry struct {
	path  string
	entry *models.DirectoryEntry
}

func (s *filesystemServiceServer) ImportRepo(ctx context.Context, req *filesystemv1.ImportRepoRequest) (*filesystemv1.ImportRepoResponse, error) {
	username, workspace, storedPath, displayPath, err := s.resolveRepoBindingTarget(ctx, req.GetPath())
	if err != nil {
		return nil, err
	}

	repoURL := strings.TrimSpace(req.GetRepoUrl())
	if repoURL == "" {
		return nil, status.Error(codes.InvalidArgument, "repo_url is required")
	}
	if existing, err := s.storage.GetRepoBinding(ctx, workspace.ID, storedPath); err == nil && existing != nil && !req.GetAllowOverwrite() {
		return nil, status.Error(codes.AlreadyExists, "repo binding already exists for path")
	} else if err != nil && err != storage.ErrRepoBindingNotFound {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to inspect existing binding: %v", err))
	}

	cloneDir, branch, remoteCommit, cleanup, err := gitrepo.Clone(ctx, repoURL, req.GetBranch(), req.GetGithubToken())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	defer cleanup()

	files, err := gitrepo.SnapshotWorktree(cloneDir)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to read cloned repository: %v", err))
	}

	commitHash, fileCount, err := s.syncRepoSnapshotToHomePath(ctx, workspace, storedPath, displayPath, files, req.GetAllowOverwrite(), fmt.Sprintf("import repo %s -> %s", repoURL, displayPath))
	if err != nil {
		return nil, err
	}

	if err := s.storage.PutRepoBinding(ctx, &models.RepoBinding{
		OwnerUsername:        username,
		SliceID:              workspace.ID,
		RootPath:             storedPath,
		Provider:             gitrepo.ProviderForURL(repoURL),
		RepoURL:              repoURL,
		Branch:               branch,
		PushEnabled:          req.GetPushEnabled(),
		LastImportedCommit:   remoteCommit,
		LastSeenRemoteCommit: remoteCommit,
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to store repo binding: %v", err))
	}
	binding, err := s.storage.GetRepoBinding(ctx, workspace.ID, storedPath)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load repo binding: %v", err))
	}
	s.refreshRepoBindingSearchArtifact(ctx, workspace.ID, commitHash)

	return &filesystemv1.ImportRepoResponse{
		Binding:      repoBindingToProto(binding),
		CommitHash:   commitHash,
		RemoteCommit: remoteCommit,
		FileCount:    int32(fileCount),
	}, nil
}

func (s *filesystemServiceServer) ListRepoBindings(ctx context.Context, req *filesystemv1.ListRepoBindingsRequest) (*filesystemv1.ListRepoBindingsResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	bindings, err := s.storage.ListRepoBindingsByOwner(ctx, username)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list repo bindings: %v", err))
	}
	resp := &filesystemv1.ListRepoBindingsResponse{
		Bindings: make([]*filesystemv1.RepoBinding, 0, len(bindings)),
	}
	for _, binding := range bindings {
		resp.Bindings = append(resp.Bindings, repoBindingToProto(binding))
	}
	return resp, nil
}

func (s *filesystemServiceServer) PullRepoBinding(ctx context.Context, req *filesystemv1.PullRepoBindingRequest) (*filesystemv1.PullRepoBindingResponse, error) {
	_, workspace, storedPath, _, err := s.resolveRepoBindingTarget(ctx, req.GetPath())
	if err != nil {
		return nil, err
	}

	binding, err := s.storage.GetRepoBinding(ctx, workspace.ID, storedPath)
	if err != nil {
		if err == storage.ErrRepoBindingNotFound {
			return nil, status.Error(codes.NotFound, "repo binding not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load repo binding: %v", err))
	}
	cloneDir, branch, remoteCommit, cleanup, err := gitrepo.Clone(ctx, binding.RepoURL, binding.Branch, req.GetGithubToken())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	defer cleanup()
	if strings.TrimSpace(branch) != "" && strings.TrimSpace(binding.Branch) == "" {
		binding.Branch = branch
	}

	if strings.TrimSpace(binding.LastSeenRemoteCommit) != "" && binding.LastSeenRemoteCommit == remoteCommit {
		return &filesystemv1.PullRepoBindingResponse{
			Binding:      repoBindingToProto(binding),
			CommitHash:   "",
			RemoteCommit: remoteCommit,
			FileCount:    0,
		}, nil
	}

	files, err := gitrepo.SnapshotWorktree(cloneDir)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to read cloned repository: %v", err))
	}

	commitHash, fileCount, err := s.syncRepoSnapshotToHomePath(ctx, workspace, storedPath, homeslice.VisiblePathForStored(storedPath), files, true, fmt.Sprintf("pull repo %s -> %s", binding.RepoURL, homeslice.VisiblePathForStored(storedPath)))
	if err != nil {
		return nil, err
	}

	binding.LastImportedCommit = remoteCommit
	binding.LastSeenRemoteCommit = remoteCommit
	binding.UpdatedAt = time.Now()
	if err := s.storage.PutRepoBinding(ctx, binding); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update repo binding: %v", err))
	}
	s.refreshRepoBindingSearchArtifact(ctx, workspace.ID, commitHash)
	fresh, err := s.storage.GetRepoBinding(ctx, workspace.ID, storedPath)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to reload repo binding: %v", err))
	}
	return &filesystemv1.PullRepoBindingResponse{
		Binding:      repoBindingToProto(fresh),
		CommitHash:   commitHash,
		RemoteCommit: remoteCommit,
		FileCount:    int32(fileCount),
	}, nil
}

func (s *filesystemServiceServer) PushRepoBinding(ctx context.Context, req *filesystemv1.PushRepoBindingRequest) (*filesystemv1.PushRepoBindingResponse, error) {
	username, workspace, storedPath, _, err := s.resolveRepoBindingTarget(ctx, req.GetPath())
	if err != nil {
		return nil, err
	}

	binding, err := s.storage.GetRepoBinding(ctx, workspace.ID, storedPath)
	if err != nil {
		if err == storage.ErrRepoBindingNotFound {
			return nil, status.Error(codes.NotFound, "repo binding not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load repo binding: %v", err))
	}
	if !binding.PushEnabled {
		return nil, status.Error(codes.FailedPrecondition, "push is disabled for this binding")
	}
	if binding.Provider == "github" && strings.TrimSpace(req.GetGithubToken()) == "" {
		return nil, status.Error(codes.InvalidArgument, "github_token is required to push a GitHub binding")
	}

	files, err := s.exportRepoBindingFiles(ctx, workspace.ID, storedPath)
	if err != nil {
		return nil, err
	}

	cloneDir, branch, remoteCommit, cleanup, err := gitrepo.Clone(ctx, binding.RepoURL, binding.Branch, req.GetGithubToken())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	defer cleanup()
	if strings.TrimSpace(binding.LastSeenRemoteCommit) != "" && binding.LastSeenRemoteCommit != remoteCommit {
		return nil, status.Error(codes.FailedPrecondition, "remote has moved; run gs repo pull first")
	}
	if err := gitrepo.ResetWorktree(cloneDir); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to reset temporary worktree: %v", err))
	}
	if err := gitrepo.WriteFiles(cloneDir, files); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to write bound files into temporary worktree: %v", err))
	}

	message := strings.TrimSpace(req.GetMessage())
	if message == "" {
		message = fmt.Sprintf("sync %s from gitslice", homeslice.VisiblePathForStored(storedPath))
	}
	pushedCommit, pushed, err := gitrepo.CommitAndPush(ctx, cloneDir, branch, message, username, req.GetGithubToken())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	binding.Branch = branch
	binding.LastSeenRemoteCommit = pushedCommit
	if pushed {
		binding.LastPushedCommit = pushedCommit
	}
	binding.UpdatedAt = time.Now()
	if err := s.storage.PutRepoBinding(ctx, binding); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update repo binding: %v", err))
	}
	fresh, err := s.storage.GetRepoBinding(ctx, workspace.ID, storedPath)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to reload repo binding: %v", err))
	}
	return &filesystemv1.PushRepoBindingResponse{
		Binding:      repoBindingToProto(fresh),
		RemoteCommit: pushedCommit,
		Pushed:       pushed,
	}, nil
}

func (s *filesystemServiceServer) DeleteRepoBinding(ctx context.Context, req *filesystemv1.DeleteRepoBindingRequest) (*filesystemv1.DeleteRepoBindingResponse, error) {
	_, workspace, storedPath, displayPath, err := s.resolveRepoBindingTarget(ctx, req.GetPath())
	if err != nil {
		return nil, err
	}
	if err := s.storage.DeleteRepoBinding(ctx, workspace.ID, storedPath); err != nil {
		if err == storage.ErrRepoBindingNotFound {
			return nil, status.Error(codes.NotFound, "repo binding not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to delete repo binding: %v", err))
	}
	return &filesystemv1.DeleteRepoBindingResponse{Path: displayPath}, nil
}

func (s *filesystemServiceServer) refreshRepoBindingSearchArtifact(ctx context.Context, workspaceID, commitHash string) {
	workspaceID = strings.TrimSpace(workspaceID)
	commitHash = strings.TrimSpace(commitHash)
	if workspaceID == "" || commitHash == "" {
		return
	}
	if meta, err := s.storage.GetSliceMetadata(ctx, workspaceID); err == nil && strings.TrimSpace(meta.HeadCommitHash) != "" {
		commitHash = strings.TrimSpace(meta.HeadCommitHash)
	}
	if _, err := storage.BuildAndStoreWorkspaceSearchArtifact(ctx, s.storage, workspaceID, commitHash); err != nil {
		log.Printf("filesystem: failed to refresh repo binding search artifact for commit %s in %s: %v", commitHash, workspaceID, err)
		s.enqueueWorkspaceSearchIndex(workspaceID, commitHash)
	}
}

func (s *filesystemServiceServer) resolveRepoBindingTarget(ctx context.Context, rawPath string) (string, *models.Slice, string, string, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return "", nil, "", "", err
	}
	workspace, err := homeslice.EnsureUserHomeSlice(ctx, s.storage, username)
	if err != nil {
		return "", nil, "", "", status.Error(codes.Internal, fmt.Sprintf("failed to ensure home slice: %v", err))
	}
	storedPath, displayPath, err := homeslice.ResolveVisiblePath(username, rawPath, true)
	if err != nil {
		return "", nil, "", "", status.Error(codes.InvalidArgument, err.Error())
	}
	if storedPath == homeslice.RelativeRootPath(username) {
		return "", nil, "", "", status.Error(codes.InvalidArgument, "path must be a directory under your home root")
	}
	return username, workspace, storedPath, displayPath, nil
}

func repoBindingToProto(binding *models.RepoBinding) *filesystemv1.RepoBinding {
	if binding == nil {
		return nil
	}
	return &filesystemv1.RepoBinding{
		BindingId:            binding.BindingID,
		Provider:             binding.Provider,
		RepoUrl:              binding.RepoURL,
		Branch:               binding.Branch,
		Path:                 homeslice.VisiblePathForStored(binding.RootPath),
		PushEnabled:          binding.PushEnabled,
		LastImportedCommit:   binding.LastImportedCommit,
		LastPushedCommit:     binding.LastPushedCommit,
		LastSeenRemoteCommit: binding.LastSeenRemoteCommit,
		CreatedAt:            binding.CreatedAt.Format(time.RFC3339),
		UpdatedAt:            binding.UpdatedAt.Format(time.RFC3339),
	}
}

func (s *filesystemServiceServer) syncRepoSnapshotToHomePath(ctx context.Context, workspace *models.Slice, rootPath, displayPath string, files []gitrepo.File, allowOverwrite bool, message string) (string, int, error) {
	if workspace == nil {
		return "", 0, status.Error(codes.Internal, "workspace is nil")
	}

	currentEntries, err := s.repoBindingSubtreeEntries(ctx, workspace.ID, rootPath)
	if err != nil {
		return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to inspect existing subtree: %v", err))
	}
	if len(currentEntries) > 0 && !allowOverwrite {
		return "", 0, status.Error(codes.AlreadyExists, "target path already contains files; use --force to overwrite")
	}

	desiredTypes := make(map[string]string, len(files)+1)
	desiredTypes[rootPath] = "directory"
	for _, file := range files {
		rel := cleanRepoRelativePath(file.Path)
		if rel == "" {
			continue
		}
		targetPath := path.Join(rootPath, rel)
		desiredTypes[targetPath] = "file"
		parent := path.Dir(targetPath)
		for parent != "." && parent != "/" && parent != "" && parent != rootPath {
			desiredTypes[parent] = "directory"
			parent = path.Dir(parent)
		}
	}

	deletePaths := make([]repoSubtreeEntry, 0)
	for _, current := range currentEntries {
		desiredType, ok := desiredTypes[current.path]
		if !ok || desiredType != current.entry.Type {
			deletePaths = append(deletePaths, current)
		}
	}
	sort.Slice(deletePaths, func(i, j int) bool {
		if len(deletePaths[i].path) == len(deletePaths[j].path) {
			return deletePaths[i].path > deletePaths[j].path
		}
		return len(deletePaths[i].path) > len(deletePaths[j].path)
	})

	modifiedPaths := make([]string, 0)
	for _, current := range deletePaths {
		if current.entry == nil {
			continue
		}
		if current.entry.Type == "file" {
			if err := s.storage.DeleteFileManifest(ctx, workspace.ID, current.path); err != nil && err != storage.ErrEntryNotFound {
				return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to delete file manifest: %v", err))
			}
			if err := s.storage.RemoveFileFromSlice(ctx, current.path, workspace.ID); err != nil {
				return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to update file index: %v", err))
			}
		}
		if err := s.storage.DeleteEntry(ctx, current.entry.ID); err != nil && err != storage.ErrEntryNotFound {
			return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to delete subtree entry: %v", err))
		}
		modifiedPaths = append(modifiedPaths, current.path)
	}

	directories := make([]string, 0)
	for p, typ := range desiredTypes {
		if typ == "directory" {
			directories = append(directories, p)
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		if len(directories[i]) == len(directories[j]) {
			return directories[i] < directories[j]
		}
		return len(directories[i]) < len(directories[j])
	})

	for _, dirPath := range directories {
		entry, err := s.storage.GetEntryByPath(ctx, workspace.ID, dirPath)
		switch {
		case err == nil && entry != nil && entry.Type == "directory":
			continue
		case err == nil && entry != nil && entry.Type != "directory":
			return "", 0, status.Error(codes.FailedPrecondition, fmt.Sprintf("%s is not a directory", homeslice.VisiblePathForStored(dirPath)))
		case err != nil && err != storage.ErrEntryNotFound:
			return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to inspect directory entry: %v", err))
		}
		if err := s.storage.AddEntry(ctx, &models.DirectoryEntry{
			ID:   common.GenerateEntryID(workspace.ID, dirPath),
			Path: dirPath,
			Type: "directory",
		}); err != nil {
			return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to create directory entry: %v", err))
		}
		modifiedPaths = append(modifiedPaths, dirPath)
	}

	for _, file := range files {
		rel := cleanRepoRelativePath(file.Path)
		if rel == "" {
			continue
		}
		targetPath := path.Join(rootPath, rel)
		nextHash := storage.HashFileManifestContent(file.Content, file.Executable, file.SymlinkTarget)
		currentEntry, err := s.storage.GetEntryByPath(ctx, workspace.ID, targetPath)
		if err == nil && currentEntry != nil && currentEntry.Type == "file" && currentEntry.Executable == file.Executable && currentEntry.SymlinkTarget == file.SymlinkTarget {
			if manifest, manifestErr := s.storage.GetFileManifest(ctx, workspace.ID, targetPath); manifestErr == nil && manifest != nil && strings.TrimSpace(manifest.Hash) == nextHash {
				continue
			}
		} else if err != nil && err != storage.ErrEntryNotFound {
			return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to inspect current file entry: %v", err))
		}

		manifest, err := storage.WriteSliceFileManifestWithMetadata(ctx, s.storage, workspace.ID, targetPath, file.Content, file.Executable, file.SymlinkTarget)
		if err != nil {
			return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to persist file manifest: %v", err))
		}
		if err := s.storage.AddEntry(ctx, &models.DirectoryEntry{
			ID:            common.GenerateEntryID(workspace.ID, targetPath),
			Path:          targetPath,
			Type:          "file",
			Size:          int64(len(file.Content)),
			Hash:          manifest.Hash,
			Executable:    file.Executable,
			SymlinkTarget: file.SymlinkTarget,
		}); err != nil {
			return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to write file entry: %v", err))
		}
		if err := s.storage.AddFileToSlice(ctx, targetPath, workspace.ID); err != nil {
			return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to update file index: %v", err))
		}
		modifiedPaths = append(modifiedPaths, targetPath)
	}

	modifiedPaths = normalizePromotionPaths(modifiedPaths)
	if len(modifiedPaths) == 0 {
		return "", len(files), nil
	}
	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, true, message, modifiedPaths)
	if err != nil {
		return "", 0, err
	}
	return commitHash, len(files), nil
}

func (s *filesystemServiceServer) repoBindingSubtreeEntries(ctx context.Context, workspaceID, rootPath string) ([]repoSubtreeEntry, error) {
	entries, err := s.collectWorkspaceEntries(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	prefix := rootPath + "/"
	filtered := make([]repoSubtreeEntry, 0)
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		if entry.Path == rootPath || strings.HasPrefix(entry.Path, prefix) {
			filtered = append(filtered, repoSubtreeEntry{path: entry.Path, entry: entry})
		}
	}
	return filtered, nil
}

func (s *filesystemServiceServer) exportRepoBindingFiles(ctx context.Context, workspaceID, rootPath string) ([]gitrepo.File, error) {
	entries, err := s.repoBindingSubtreeEntries(ctx, workspaceID, rootPath)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to inspect bound subtree: %v", err))
	}
	if len(entries) == 0 {
		return nil, status.Error(codes.NotFound, "bound directory is empty or missing")
	}
	files := make([]gitrepo.File, 0)
	prefix := rootPath + "/"
	for _, entry := range entries {
		if entry.entry == nil || entry.entry.Type != "file" {
			continue
		}
		content, err := storage.ReadSliceFileContent(ctx, s.storage, workspaceID, entry.path)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to read bound file content: %v", err))
		}
		relPath := entry.path
		if strings.HasPrefix(relPath, prefix) {
			relPath = strings.TrimPrefix(relPath, prefix)
		}
		files = append(files, gitrepo.File{
			Path:          relPath,
			Content:       content.Content,
			Executable:    entry.entry.Executable,
			SymlinkTarget: entry.entry.SymlinkTarget,
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func cleanRepoRelativePath(raw string) string {
	cleaned := path.Clean(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"))
	if cleaned == "." || cleaned == "/" {
		return ""
	}
	return strings.TrimPrefix(cleaned, "/")
}
