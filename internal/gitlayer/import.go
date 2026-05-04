package gitlayer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/gitrepo"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

const gitSearchIndexTimeout = 10 * time.Minute

type fileManifestHashGetter interface {
	GetFileManifestHashes(ctx context.Context, sliceID string, paths []string) (map[string]string, error)
}

type sliceTreeEntry struct {
	path  string
	entry *models.DirectoryEntry
}

func (h *Handler) importBareRepo(ctx context.Context, slice *models.Slice, repoPath, gitCommit, author string) error {
	parentDir, err := os.MkdirTemp(h.cacheDir, "import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(parentDir)

	worktree := filepath.Join(parentDir, "worktree")
	if _, err := runGit(ctx, parentDir, "clone", "--branch", defaultBranch, repoPath, worktree); err != nil {
		return fmt.Errorf("clone pushed repository: %w", err)
	}
	files, err := gitrepo.SnapshotWorktree(worktree)
	if err != nil {
		return fmt.Errorf("snapshot pushed repository: %w", err)
	}

	message := gitCommitLogField(ctx, repoPath, "%s")
	if strings.TrimSpace(message) == "" {
		message = "git push"
	}
	commitAuthor := strings.TrimSpace(author)
	if commitAuthor == "" {
		commitAuthor = gitCommitLogField(ctx, repoPath, "%an")
	}
	if commitAuthor == "" {
		commitAuthor = "git"
	}
	commitTime := time.Now()
	if rawTimestamp := gitCommitLogField(ctx, repoPath, "%ct"); rawTimestamp != "" {
		if unixSeconds, parseErr := strconv.ParseInt(rawTimestamp, 10, 64); parseErr == nil {
			commitTime = time.Unix(unixSeconds, 0)
		}
	}

	if err := h.applyFilesToSlice(ctx, slice, files, gitSliceCommitHash(slice.ID, gitCommit), message, commitAuthor, commitTime); err != nil {
		return fmt.Errorf("apply pushed repository to slice: %w", err)
	}
	return nil
}

func (h *Handler) applyFilesToSlice(ctx context.Context, slice *models.Slice, files []gitrepo.File, commitHash, message, author string, commitTime time.Time) error {
	if slice == nil {
		return fmt.Errorf("slice is nil")
	}
	projection := newGitProjection(slice)
	targetSliceID := projection.target()
	if targetSliceID == "" {
		return fmt.Errorf("target slice is empty")
	}

	meta, err := h.st.GetSliceMetadata(ctx, targetSliceID)
	if err != nil {
		return err
	}
	previousFiles := make(map[string]string)
	if strings.TrimSpace(meta.HeadCommitHash) != "" {
		if snapshot, snapshotErr := h.st.GetCommitSnapshot(ctx, meta.HeadCommitHash); snapshotErr == nil && snapshot != nil {
			for filePath, fileHash := range snapshot.Files {
				cleaned := common.CleanRelativePath(filePath)
				if cleaned != "" && strings.TrimSpace(fileHash) != "" {
					previousFiles[cleaned] = strings.TrimSpace(fileHash)
				}
			}
		} else if snapshotErr != nil && snapshotErr != storage.ErrCommitNotFound && snapshotErr != storage.ErrEntryNotFound {
			return snapshotErr
		}
	}

	currentEntries, err := collectEntries(ctx, h.st, targetSliceID)
	if err != nil {
		return err
	}
	desiredTypes := make(map[string]string, len(files))
	desiredFiles := make(map[string]gitrepo.File, len(files))
	for _, mount := range projection.mounts {
		desiredTypes[mount.SourcePath] = "directory"
	}
	for _, file := range files {
		cleanedPath, err := cleanGitPath(file.Path)
		if err != nil {
			return err
		}
		if cleanedPath == "" {
			continue
		}
		storedPath, err := projection.storedPathForGitFile(cleanedPath)
		if err != nil {
			return err
		}
		if storedPath == "" {
			continue
		}
		if _, exists := desiredFiles[storedPath]; exists {
			return fmt.Errorf("duplicate git path %q", cleanedPath)
		}
		if existing := desiredTypes[storedPath]; existing == "directory" {
			return fmt.Errorf("path %q is both file and directory", cleanedPath)
		}
		file.Path = storedPath
		if file.SymlinkTarget != "" {
			file.Content = []byte(file.SymlinkTarget)
			file.Executable = false
		}
		desiredFiles[storedPath] = file
		desiredTypes[storedPath] = "file"
		for dir := path.Dir(storedPath); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
			if existing := desiredTypes[dir]; existing == "file" {
				return fmt.Errorf("path %q is both file and directory", dir)
			}
			desiredTypes[dir] = "directory"
		}
	}

	modifiedSet := make(map[string]struct{})
	deleteEntries := make([]sliceTreeEntry, 0)
	deletedPaths := make(map[string]struct{})
	for _, entry := range currentEntries {
		if entry == nil || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		if !projection.managesStoredPath(entry.Path) {
			continue
		}
		desiredType, keep := desiredTypes[entry.Path]
		if !keep || desiredType != entry.Type {
			deleteEntries = append(deleteEntries, sliceTreeEntry{path: entry.Path, entry: entry})
		}
	}
	sort.Slice(deleteEntries, func(i, j int) bool {
		if len(deleteEntries[i].path) == len(deleteEntries[j].path) {
			return deleteEntries[i].path > deleteEntries[j].path
		}
		return len(deleteEntries[i].path) > len(deleteEntries[j].path)
	})
	for _, current := range deleteEntries {
		if current.entry == nil {
			continue
		}
		if projection.protectedStoredDir(current.path) {
			continue
		}
		if current.entry.Type == "file" {
			if err := h.st.DeleteFileManifest(ctx, targetSliceID, current.path); err != nil && err != storage.ErrEntryNotFound {
				return err
			}
			if err := h.st.RemoveFileFromSlice(ctx, current.path, targetSliceID); err != nil {
				return err
			}
		}
		if err := h.st.DeleteEntry(ctx, current.entry.ID); err != nil && err != storage.ErrEntryNotFound {
			return err
		}
		deletedPaths[current.path] = struct{}{}
		modifiedSet[current.path] = struct{}{}
	}

	directories := make([]string, 0)
	for p, typ := range desiredTypes {
		if typ == "directory" {
			directories = append(directories, p)
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		if strings.Count(directories[i], "/") == strings.Count(directories[j], "/") {
			return directories[i] < directories[j]
		}
		return strings.Count(directories[i], "/") < strings.Count(directories[j], "/")
	})
	for _, dirPath := range directories {
		entry, err := h.st.GetEntryByPath(ctx, targetSliceID, dirPath)
		if err == nil && entry != nil && entry.Type == "directory" {
			continue
		}
		if err == nil && entry != nil {
			return fmt.Errorf("%s is not a directory", dirPath)
		}
		if err != nil && err != storage.ErrEntryNotFound {
			return err
		}
		if err := h.st.AddEntry(ctx, &models.DirectoryEntry{
			ID:   common.GenerateEntryID(targetSliceID, dirPath),
			Path: dirPath,
			Type: "directory",
		}); err != nil {
			return err
		}
		modifiedSet[dirPath] = struct{}{}
	}

	filePaths := make([]string, 0, len(desiredFiles))
	for filePath := range desiredFiles {
		filePaths = append(filePaths, filePath)
	}
	sort.Strings(filePaths)
	for _, filePath := range filePaths {
		file := desiredFiles[filePath]
		nextHash := storage.HashFileManifestContent(file.Content, file.Executable, file.SymlinkTarget)
		currentEntry, err := h.st.GetEntryByPath(ctx, targetSliceID, filePath)
		if err == nil && currentEntry != nil && currentEntry.Type == "file" && currentEntry.Executable == file.Executable && currentEntry.SymlinkTarget == file.SymlinkTarget {
			if manifest, manifestErr := h.st.GetFileManifest(ctx, targetSliceID, filePath); manifestErr == nil && manifest != nil && strings.TrimSpace(manifest.Hash) == nextHash {
				continue
			}
		} else if err != nil && err != storage.ErrEntryNotFound {
			return err
		}
		manifest, err := storage.WriteSliceFileManifestWithMetadata(ctx, h.st, targetSliceID, filePath, file.Content, file.Executable, file.SymlinkTarget)
		if err != nil {
			return err
		}
		if err := h.st.AddEntry(ctx, &models.DirectoryEntry{
			ID:            common.GenerateEntryID(targetSliceID, filePath),
			Path:          filePath,
			Type:          "file",
			Size:          int64(len(file.Content)),
			Hash:          manifest.Hash,
			Executable:    file.Executable,
			SymlinkTarget: file.SymlinkTarget,
		}); err != nil {
			return err
		}
		if err := h.st.AddFileToSlice(ctx, filePath, targetSliceID); err != nil {
			return err
		}
		modifiedSet[filePath] = struct{}{}
	}

	if len(modifiedSet) == 0 {
		return nil
	}
	currentFiles, allPaths, err := h.updatedSliceSnapshot(ctx, targetSliceID, currentEntries, previousFiles, desiredTypes, desiredFiles, deletedPaths)
	if err != nil {
		return err
	}
	if strings.TrimSpace(commitHash) == "" {
		commitHash = fmt.Sprintf("git-%d", time.Now().UnixNano())
	}
	if commitTime.IsZero() {
		commitTime = time.Now()
	}
	if err := h.st.AddSliceCommit(ctx, targetSliceID, &models.Commit{
		CommitHash: commitHash,
		ParentHash: meta.HeadCommitHash,
		Timestamp:  commitTime,
		Message:    strings.TrimSpace(message),
	}); err != nil {
		return err
	}
	if err := h.st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    targetSliceID,
		Files:      currentFiles,
		Timestamp:  commitTime,
	}); err != nil {
		return err
	}
	if err := h.st.UpdateSliceMetadata(ctx, targetSliceID, &models.SliceMetadata{
		SliceID:            targetSliceID,
		HeadCommitHash:     commitHash,
		ModifiedFiles:      allPaths,
		LastModified:       commitTime,
		ModifiedFilesCount: len(allPaths),
	}); err != nil {
		return err
	}
	if err := h.recordFileChanges(ctx, targetSliceID, commitHash, message, author, commitTime, previousFiles, currentFiles); err != nil {
		log.Printf("gitlayer: failed to record file changes for commit %s in %s: %v", commitHash, targetSliceID, err)
	}
	h.refreshWorkspaceSearchArtifactAsync(targetSliceID, commitHash)
	return nil
}

func (h *Handler) updatedSliceSnapshot(
	ctx context.Context,
	sliceID string,
	entries []*models.DirectoryEntry,
	previousFiles map[string]string,
	desiredTypes map[string]string,
	desiredFiles map[string]gitrepo.File,
	deletedPaths map[string]struct{},
) (map[string]string, []string, error) {
	pathTypes := make(map[string]string, len(entries)+len(desiredTypes))
	files := make(map[string]string)
	missingHashes := make([]string, 0)

	for _, entry := range entries {
		if entry == nil || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		cleaned := common.CleanRelativePath(entry.Path)
		if cleaned == "" {
			continue
		}
		if _, deleted := deletedPaths[cleaned]; deleted {
			continue
		}
		pathTypes[cleaned] = entry.Type
		if entry.Type != "file" {
			continue
		}
		if hash := strings.TrimSpace(previousFiles[cleaned]); hash != "" {
			files[cleaned] = hash
			continue
		}
		missingHashes = append(missingHashes, cleaned)
	}

	if len(missingHashes) > 0 {
		hashes, err := h.fileManifestHashes(ctx, sliceID, missingHashes)
		if err != nil {
			return nil, nil, err
		}
		for _, filePath := range missingHashes {
			if hash := strings.TrimSpace(hashes[filePath]); hash != "" {
				files[filePath] = hash
			}
		}
	}

	for filePath := range deletedPaths {
		delete(pathTypes, common.CleanRelativePath(filePath))
		delete(files, common.CleanRelativePath(filePath))
	}
	for filePath, typ := range desiredTypes {
		cleaned := common.CleanRelativePath(filePath)
		if cleaned == "" {
			continue
		}
		pathTypes[cleaned] = typ
		if typ != "file" {
			delete(files, cleaned)
		}
	}
	for filePath, file := range desiredFiles {
		cleaned := common.CleanRelativePath(filePath)
		if cleaned == "" {
			continue
		}
		pathTypes[cleaned] = "file"
		files[cleaned] = storage.HashFileManifestContent(file.Content, file.Executable, file.SymlinkTarget)
	}

	paths := make([]string, 0, len(pathTypes))
	for filePath := range pathTypes {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	return files, paths, nil
}

func (h *Handler) fileManifestHashes(ctx context.Context, sliceID string, paths []string) (map[string]string, error) {
	if getter, ok := h.st.(fileManifestHashGetter); ok {
		return getter.GetFileManifestHashes(ctx, sliceID, paths)
	}
	hashes := make(map[string]string, len(paths))
	for _, filePath := range paths {
		manifest, err := h.st.GetFileManifest(ctx, sliceID, filePath)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				continue
			}
			return nil, err
		}
		if hash := strings.TrimSpace(manifest.Hash); hash != "" {
			hashes[common.CleanRelativePath(filePath)] = hash
		}
	}
	return hashes, nil
}

func (h *Handler) refreshWorkspaceSearchArtifactAsync(sliceID, commitHash string) {
	sliceID = strings.TrimSpace(sliceID)
	commitHash = strings.TrimSpace(commitHash)
	if sliceID == "" || commitHash == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), gitSearchIndexTimeout)
		defer cancel()
		if err := h.refreshWorkspaceSearchArtifact(ctx, sliceID, commitHash); err != nil {
			log.Printf("gitlayer: failed to refresh search artifact for commit %s in %s: %v", commitHash, sliceID, err)
		}
	}()
}

func (h *Handler) refreshWorkspaceSearchArtifact(ctx context.Context, sliceID, commitHash string) error {
	current, err := h.sliceCommitIsCurrent(ctx, sliceID, commitHash)
	if err != nil || !current {
		return err
	}
	artifact, err := storage.BuildAndStoreSliceSearchArtifact(ctx, h.st, sliceID, commitHash)
	if err != nil {
		return err
	}
	current, err = h.sliceCommitIsCurrent(ctx, sliceID, commitHash)
	if err != nil || !current {
		return err
	}
	return storage.StoreWorkspaceSearchArtifact(ctx, h.st, sliceID, artifact)
}

func (h *Handler) sliceCommitIsCurrent(ctx context.Context, sliceID, commitHash string) (bool, error) {
	meta, err := h.st.GetSliceMetadata(ctx, strings.TrimSpace(sliceID))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(meta.HeadCommitHash) == strings.TrimSpace(commitHash), nil
}

func (h *Handler) recordFileChanges(ctx context.Context, sliceID, commitHash, message, author string, timestamp time.Time, previousFiles, currentFiles map[string]string) error {
	author = strings.TrimSpace(author)
	if author == "" {
		author = "git"
	}
	pathSet := make(map[string]struct{}, len(previousFiles)+len(currentFiles))
	for filePath := range previousFiles {
		pathSet[filePath] = struct{}{}
	}
	for filePath := range currentFiles {
		pathSet[filePath] = struct{}{}
	}
	paths := make([]string, 0, len(pathSet))
	for filePath := range pathSet {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)

	changes := make([]*models.FileChangeRecord, 0)
	for _, filePath := range paths {
		oldHash := strings.TrimSpace(previousFiles[filePath])
		newHash := strings.TrimSpace(currentFiles[filePath])
		if oldHash == newHash {
			continue
		}
		changeType := models.ChangeTypeModify
		switch {
		case oldHash == "":
			changeType = models.ChangeTypeAdd
		case newHash == "":
			changeType = models.ChangeTypeDelete
		}
		changes = append(changes, &models.FileChangeRecord{
			ID:         fmt.Sprintf("%s-%s", commitHash, filePath),
			SliceID:    sliceID,
			CommitHash: commitHash,
			Path:       filePath,
			ChangeType: changeType,
			OldHash:    oldHash,
			NewHash:    newHash,
			Author:     author,
			Message:    strings.TrimSpace(message),
			Timestamp:  timestamp,
		})
	}
	if len(changes) == 0 {
		return nil
	}
	return h.st.AddFileChanges(ctx, changes)
}

func gitCommitLogField(ctx context.Context, repoPath, format string) string {
	out, err := runGit(ctx, "", "--git-dir="+repoPath, "log", "-1", "--format="+format, "refs/heads/"+defaultBranch)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitSliceCommitHash(sliceID, gitCommit string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(sliceID)))
	return "git-" + hex.EncodeToString(sum[:6]) + "-" + strings.TrimSpace(gitCommit)
}
