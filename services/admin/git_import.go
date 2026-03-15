package adminservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type resettableStorage interface {
	Reset(ctx context.Context) error
}

type bulkWriter interface {
	BulkWrite(ctx context.Context, fn func(st storage.Storage) error) error
}

type gitImportResult struct {
	ImportedCommits int
	HeadCommitHash  string
	Warnings        []string
}

type gitCommitMeta struct {
	Hash      string
	Author    string
	Timestamp time.Time
	Message   string
}

type gitDiffEntry struct {
	Kind    string // "A", "M", "D", "R"
	OldPath string
	NewPath string
}

func gitCombinedOutput(ctx context.Context, repoDir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return out, nil
}

func gitRepoRoot(ctx context.Context, repoPath string) (string, error) {
	if repoPath == "" {
		repoPath = "."
	}
	out, err := gitCombinedOutput(ctx, repoPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func defaultMountPathFromName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		name = "repo"
	}
	return "/o/genesis/projects/" + name
}

func repoNameFromURL(repoURL string) string {
	s := strings.TrimSpace(repoURL)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	if s == "" {
		return "repo"
	}
	// git@github.com:org/repo -> split on ':' too.
	s = strings.ReplaceAll(s, ":", "/")
	parts := strings.Split(s, "/")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return "repo"
	}
	return last
}

func prepareRepo(ctx context.Context, repoPath string, repoURL string, maxCommits int) (repoDir string, repoName string, cleanup func(), err error) {
	if strings.TrimSpace(repoURL) != "" {
		parent, err := os.MkdirTemp("", "gitslice-import-")
		if err != nil {
			return "", "", func() {}, err
		}
		cleanup = func() { _ = os.RemoveAll(parent) }

		cloneDir := filepath.Join(parent, "repo.git")
		// Avoid cloning full history when the caller only wants a bounded number of commits.
		// This keeps imports fast and reduces memory/disk pressure on the server.
		cloneArgs := []string{"clone", "--bare", "--quiet"}
		if maxCommits > 0 {
			// Default clone behavior is single-branch; keep it that way to minimize data transferred.
			cloneArgs = append(cloneArgs, "--depth", strconv.Itoa(maxCommits))
		}
		cloneArgs = append(cloneArgs, repoURL, cloneDir)
		_, err = gitCombinedOutput(ctx, parent, cloneArgs...)
		if err != nil {
			cleanup()
			return "", "", func() {}, err
		}

		return cloneDir, repoNameFromURL(repoURL), cleanup, nil
	}

	root, err := gitRepoRoot(ctx, repoPath)
	if err != nil {
		return "", "", func() {}, err
	}
	name := path.Base(filepath.ToSlash(root))
	return root, name, func() {}, nil
}

func mountFile(mountPath string, repoRel string) (string, error) {
	repoRel = strings.TrimSpace(repoRel)
	if repoRel == "" {
		return "", fmt.Errorf("empty repo path")
	}
	repoRel = filepath.ToSlash(repoRel)
	repoRel = strings.TrimPrefix(repoRel, "/")
	if err := common.ValidateFilePath(repoRel); err != nil {
		return "", err
	}

	mountPath = path.Clean("/" + strings.TrimSpace(mountPath))
	mountPath = strings.TrimSuffix(mountPath, "/")
	mounted := path.Join(mountPath, repoRel)
	mounted = strings.TrimPrefix(mounted, "/")
	if err := common.ValidateFilePath(mounted); err != nil {
		return "", err
	}
	return mounted, nil
}

func parseRevList(out []byte, maxCommits int) []string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var commits []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		commits = append(commits, line)
		if maxCommits > 0 && len(commits) >= maxCommits {
			break
		}
	}
	return commits
}

func parseCommitMeta(commitHash string, out []byte) (*gitCommitMeta, error) {
	// format: author\nunix\nsubject\n
	lines := strings.Split(string(out), "\n")
	if len(lines) < 3 {
		return nil, fmt.Errorf("unexpected git meta output for %s", commitHash)
	}

	author := strings.TrimSpace(lines[0])
	secStr := strings.TrimSpace(lines[1])
	subject := strings.TrimSpace(lines[2])

	secs, err := strconv.ParseInt(secStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse commit timestamp for %s: %w", commitHash, err)
	}

	return &gitCommitMeta{
		Hash:      commitHash,
		Author:    author,
		Timestamp: time.Unix(secs, 0).UTC(),
		Message:   subject,
	}, nil
}

func parseDiffEntries(out []byte) (entries []gitDiffEntry, warnings []string) {
	// git diff-tree --name-status -r outputs:
	//  A\tpath
	//  M\tpath
	//  D\tpath
	//  R100\told\tnew
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			warnings = append(warnings, "unrecognized diff line: "+line)
			continue
		}

		status := parts[0]
		switch {
		case strings.HasPrefix(status, "R") && len(parts) >= 3:
			entries = append(entries, gitDiffEntry{
				Kind:    "R",
				OldPath: parts[1],
				NewPath: parts[2],
			})
		case status == "A" || status == "M":
			entries = append(entries, gitDiffEntry{
				Kind:    status,
				NewPath: parts[1],
			})
		case status == "D":
			entries = append(entries, gitDiffEntry{
				Kind:    "D",
				OldPath: parts[1],
			})
		default:
			warnings = append(warnings, "unsupported diff status: "+line)
		}
	}

	return entries, warnings
}

func gitPathMode(ctx context.Context, repoDir, commitHash, repoRel string) (string, error) {
	out, err := gitCombinedOutput(ctx, repoDir, "ls-tree", commitHash, "--", repoRel)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", fmt.Errorf("path %q not found in %s", repoRel, commitHash)
	}
	if tab := strings.IndexByte(line, '\t'); tab >= 0 {
		line = line[:tab]
	}
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return "", fmt.Errorf("unexpected ls-tree output for %s:%s", commitHash, repoRel)
	}
	return strings.TrimSpace(fields[0]), nil
}

func ensureDirectoryEntry(ctx context.Context, st storage.Storage, sliceID, dirPath string) error {
	dirPath = common.CleanRelativePath(dirPath)
	if dirPath == "" {
		return nil
	}

	parentPath := path.Dir(dirPath)
	if parentPath == "." || parentPath == "/" {
		parentPath = ""
	}

	parentID := sliceID
	if parentPath != "" {
		parentID = common.GenerateEntryID(sliceID, parentPath)
	}

	entry := &models.DirectoryEntry{
		ID:       common.GenerateEntryID(sliceID, dirPath),
		Path:     dirPath,
		Type:     "directory",
		ParentID: parentID,
		Size:     0,
	}

	if err := st.AddEntry(ctx, entry); err != nil {
		if errors.Is(err, storage.ErrEntryExists) {
			// Keep parent links up to date.
			return st.UpdateEntry(ctx, entry)
		}
		return err
	}
	return nil
}

func ensureParentDirectories(ctx context.Context, st storage.Storage, sliceID, filePath string) (string, error) {
	filePath = common.CleanRelativePath(filePath)
	if filePath == "" {
		return sliceID, nil
	}
	dirPath := path.Dir(filePath)
	if dirPath == "." || dirPath == "/" {
		return sliceID, nil
	}

	// Create parents from shallow to deep.
	parts := strings.Split(dirPath, "/")
	for i := 1; i <= len(parts); i++ {
		p := strings.Join(parts[:i], "/")
		if err := ensureDirectoryEntry(ctx, st, sliceID, p); err != nil {
			return "", err
		}
	}
	return common.GenerateEntryID(sliceID, dirPath), nil
}

func upsertFile(
	ctx context.Context,
	st storage.Storage,
	sliceID string,
	mountedPath string,
	content []byte,
	contentHash string,
	executable bool,
	symlinkTarget string,
) (string, error) {
	parentID, err := ensureParentDirectories(ctx, st, sliceID, mountedPath)
	if err != nil {
		return "", err
	}

	entry := &models.DirectoryEntry{
		ID:            common.GenerateEntryID(sliceID, mountedPath),
		Path:          mountedPath,
		Type:          "file",
		ParentID:      parentID,
		Size:          int64(len(content)),
		Executable:    executable,
		SymlinkTarget: symlinkTarget,
	}
	if err := st.AddEntry(ctx, entry); err != nil {
		if errors.Is(err, storage.ErrEntryExists) {
			if err := st.UpdateEntry(ctx, entry); err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}

	if err := st.AddFileToSlice(ctx, mountedPath, sliceID); err != nil {
		return "", err
	}

	manifest, err := storage.WriteSliceFileManifestWithMetadata(ctx, st, sliceID, mountedPath, content, executable, symlinkTarget)
	if err != nil {
		return "", err
	}
	if got := strings.TrimSpace(manifest.Hash); got != strings.TrimSpace(contentHash) {
		return "", fmt.Errorf("manifest hash mismatch for %s", mountedPath)
	}
	return strings.TrimSpace(manifest.Hash), nil
}

func saveCommitSnapshot(ctx context.Context, st storage.Storage, sliceID string, commitHash string, commitTime time.Time, fileHashes map[string]string) error {
	files := make(map[string]string, len(fileHashes))
	for p, h := range fileHashes {
		files[p] = h
	}
	return st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    sliceID,
		Files:      files,
		Timestamp:  commitTime,
	})
}

func addFileChangeRecords(ctx context.Context, st storage.Storage, changes []*models.FileChangeRecord) error {
	if len(changes) == 0 {
		return nil
	}
	return st.AddFileChanges(ctx, changes)
}

func deleteFileFromSlice(ctx context.Context, st storage.Storage, sliceID string, mountedPath string) error {
	// Directory entries are keyed by a stable ID derived from slice+path.
	entryID := common.GenerateEntryID(sliceID, mountedPath)
	if err := st.DeleteEntry(ctx, entryID); err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
		return err
	}
	// Best-effort: index might not contain it; RemoveFileFromSlice is a no-op in that case.
	if err := st.RemoveFileFromSlice(ctx, mountedPath, sliceID); err != nil {
		return err
	}
	return nil
}

func importGitRepo(ctx context.Context, st storage.Storage, repoPath string, repoURL string, ref string, sliceID string, mountPath string, reset bool, firstParent bool, maxCommits int) (*gitImportResult, error) {
	if ref == "" {
		ref = "HEAD"
	}

	repoDir, repoName, cleanup, err := prepareRepo(ctx, repoPath, repoURL, maxCommits)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if mountPath == "" {
		mountPath = defaultMountPathFromName(repoName)
	}
	if sliceID == "" {
		sliceID = "root_slice"
	}

	warnings := []string{}

	args := []string{"rev-list", "--reverse"}
	if firstParent {
		args = append(args, "--first-parent")
	}
	args = append(args, ref)

	out, err := gitCombinedOutput(ctx, repoDir, args...)
	if err != nil {
		return nil, err
	}
	commits := parseRevList(out, maxCommits)
	if len(commits) == 0 {
		return &gitImportResult{ImportedCommits: 0, HeadCommitHash: "", Warnings: []string{"no commits found"}}, nil
	}

	// Fast path: run the full import in a single Postgres BulkWrite so we persist once.
	if bw, ok := st.(bulkWriter); ok {
		// For native storage, reset is already durable and not "all or nothing" like snapshots.
		// Execute it outside BulkWrite to avoid TRUNCATE inside an open transaction.
		if reset {
			if rs, ok := st.(resettableStorage); ok {
				if err := rs.Reset(ctx); err != nil {
					return nil, err
				}
			} else {
				return nil, fmt.Errorf("storage backend does not support reset")
			}
		}

		var head string
		err := bw.BulkWrite(ctx, func(st storage.Storage) error {
			if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
				return err
			}
			if _, err := st.GetSlice(ctx, sliceID); err != nil {
				if errors.Is(err, storage.ErrSliceNotFound) && sliceID == "root_slice" {
					if err := st.InitializeRootSlice(ctx); err != nil {
						return err
					}
				} else {
					return err
				}
			}

			currentFileHashes := make(map[string]string)
			var prevCommit string

			// If we're importing into an existing slice without resetting it, start from
			// the current slice HEAD snapshot so multiple imports accumulate into one
			// slice namespace instead of clobbering the visible tree at HEAD.
			if !reset {
				if meta, err := st.GetSliceMetadata(ctx, sliceID); err == nil && meta != nil {
					prevCommit = strings.TrimSpace(meta.HeadCommitHash)
					if prevCommit != "" {
						if snap, err := st.GetCommitSnapshot(ctx, prevCommit); err == nil && snap != nil {
							for p, h := range snap.Files {
								p = common.CleanRelativePath(p)
								if p == "" || h == "" {
									continue
								}
								currentFileHashes[p] = h
							}
						}
					}
				}
			}

			for idx, commitHash := range commits {
				metaOut, err := gitCombinedOutput(ctx, repoDir, "show", "-s", "--format=%an <%ae>%n%ct%n%s", commitHash)
				if err != nil {
					return err
				}
				meta, err := parseCommitMeta(commitHash, metaOut)
				if err != nil {
					return err
				}

				var diffOut []byte
				if idx == 0 {
					diffOut, err = gitCombinedOutput(ctx, repoDir, "diff-tree", "--root", "--no-commit-id", "--name-status", "-r", commitHash)
				} else {
					// Use a two-tree diff so merge commits include the diff vs their first parent.
					diffOut, err = gitCombinedOutput(ctx, repoDir, "diff", "--name-status", "-M", prevCommit, commitHash)
				}
				if err != nil {
					return err
				}

				diffEntries, diffWarnings := parseDiffEntries(diffOut)
				warnings = append(warnings, diffWarnings...)

				// Keep a stable view of the "before" state for change records.
				beforeHashes := make(map[string]string, len(currentFileHashes))
				for p, h := range currentFileHashes {
					beforeHashes[p] = h
				}
				modifiedSet := make(map[string]struct{})
				changeRecs := make([]*models.FileChangeRecord, 0, len(diffEntries))

				addWarning := func(msg string) {
					warnings = append(warnings, msg)
				}

				for _, de := range diffEntries {
					switch de.Kind {
					case "A", "M":
						repoRel := de.NewPath
						mountedPath, err := mountFile(mountPath, repoRel)
						if err != nil {
							addWarning(fmt.Sprintf("skip invalid path %q in %s: %v", repoRel, commitHash, err))
							continue
						}

						blobSpec := fmt.Sprintf("%s:%s", commitHash, repoRel)
						content, err := gitCombinedOutput(ctx, repoDir, "cat-file", "-p", blobSpec)
						if err != nil {
							addWarning(fmt.Sprintf("skip missing blob %q in %s: %v", repoRel, commitHash, err))
							continue
						}
						mode, err := gitPathMode(ctx, repoDir, commitHash, repoRel)
						if err != nil {
							addWarning(fmt.Sprintf("skip missing tree entry %q in %s: %v", repoRel, commitHash, err))
							continue
						}
						executable := mode == "100755"
						symlinkTarget := ""
						if mode == "120000" {
							symlinkTarget = string(content)
						}

						oldHash := beforeHashes[mountedPath]
						newHash := storage.HashFileManifestContent(content, executable, symlinkTarget)
						if newHash, err = upsertFile(ctx, st, sliceID, mountedPath, content, newHash, executable, symlinkTarget); err != nil {
							return err
						}
						currentFileHashes[mountedPath] = newHash
						modifiedSet[mountedPath] = struct{}{}

						ct := models.ChangeTypeModify
						if de.Kind == "A" {
							ct = models.ChangeTypeAdd
						}
						changeRecs = append(changeRecs, &models.FileChangeRecord{
							ID:         fmt.Sprintf("%s-%s-%s", meta.Hash, ct, mountedPath),
							SliceID:    sliceID,
							CommitHash: meta.Hash,
							Path:       mountedPath,
							ChangeType: ct,
							OldHash:    oldHash,
							NewHash:    newHash,
							Author:     meta.Author,
							Message:    meta.Message,
							Timestamp:  meta.Timestamp,
						})
					case "D":
						repoRel := de.OldPath
						mountedPath, err := mountFile(mountPath, repoRel)
						if err != nil {
							addWarning(fmt.Sprintf("skip invalid path %q in %s: %v", repoRel, commitHash, err))
							continue
						}

						oldHash := beforeHashes[mountedPath]
						delete(currentFileHashes, mountedPath)
						if err := deleteFileFromSlice(ctx, st, sliceID, mountedPath); err != nil {
							return err
						}
						modifiedSet[mountedPath] = struct{}{}

						changeRecs = append(changeRecs, &models.FileChangeRecord{
							ID:         fmt.Sprintf("%s-%s-%s", meta.Hash, models.ChangeTypeDelete, mountedPath),
							SliceID:    sliceID,
							CommitHash: meta.Hash,
							Path:       mountedPath,
							ChangeType: models.ChangeTypeDelete,
							OldHash:    oldHash,
							NewHash:    "",
							Author:     meta.Author,
							Message:    meta.Message,
							Timestamp:  meta.Timestamp,
						})
					case "R":
						oldRel := de.OldPath
						newRel := de.NewPath
						oldMounted, err := mountFile(mountPath, oldRel)
						if err != nil {
							addWarning(fmt.Sprintf("skip invalid rename old path %q in %s: %v", oldRel, commitHash, err))
							continue
						}
						newMounted, err := mountFile(mountPath, newRel)
						if err != nil {
							addWarning(fmt.Sprintf("skip invalid rename new path %q in %s: %v", newRel, commitHash, err))
							continue
						}

						oldHash := beforeHashes[oldMounted]
						delete(currentFileHashes, oldMounted)
						if err := deleteFileFromSlice(ctx, st, sliceID, oldMounted); err != nil {
							return err
						}

						blobSpec := fmt.Sprintf("%s:%s", commitHash, newRel)
						content, err := gitCombinedOutput(ctx, repoDir, "cat-file", "-p", blobSpec)
						if err != nil {
							addWarning(fmt.Sprintf("skip missing blob %q in %s: %v", newRel, commitHash, err))
							continue
						}
						mode, err := gitPathMode(ctx, repoDir, commitHash, newRel)
						if err != nil {
							addWarning(fmt.Sprintf("skip missing tree entry %q in %s: %v", newRel, commitHash, err))
							continue
						}
						executable := mode == "100755"
						symlinkTarget := ""
						if mode == "120000" {
							symlinkTarget = string(content)
						}
						newHash := storage.HashFileManifestContent(content, executable, symlinkTarget)
						if newHash, err = upsertFile(ctx, st, sliceID, newMounted, content, newHash, executable, symlinkTarget); err != nil {
							return err
						}
						currentFileHashes[newMounted] = newHash
						modifiedSet[oldMounted] = struct{}{}
						modifiedSet[newMounted] = struct{}{}

						changeRecs = append(changeRecs, &models.FileChangeRecord{
							ID:         fmt.Sprintf("%s-%s-%s", meta.Hash, models.ChangeTypeRename, newMounted),
							SliceID:    sliceID,
							CommitHash: meta.Hash,
							Path:       newMounted,
							OldPath:    oldMounted,
							ChangeType: models.ChangeTypeRename,
							OldHash:    oldHash,
							NewHash:    newHash,
							Author:     meta.Author,
							Message:    meta.Message,
							Timestamp:  meta.Timestamp,
						})
					default:
						addWarning(fmt.Sprintf("skip unsupported diff kind %q in %s", de.Kind, commitHash))
					}
				}

				modifiedMountedPaths := make([]string, 0, len(modifiedSet))
				for p := range modifiedSet {
					modifiedMountedPaths = append(modifiedMountedPaths, p)
				}
				sort.Strings(modifiedMountedPaths)

				if err := st.AddSliceCommit(ctx, sliceID, &models.Commit{
					CommitHash: meta.Hash,
					ParentHash: prevCommit,
					Timestamp:  meta.Timestamp,
					Message:    meta.Message,
				}); err != nil {
					return err
				}

				sliceMeta, err := st.GetSliceMetadata(ctx, sliceID)
				if err != nil {
					return err
				}
				sliceMeta.HeadCommitHash = meta.Hash
				sliceMeta.ModifiedFiles = modifiedMountedPaths
				sliceMeta.ModifiedFilesCount = len(modifiedMountedPaths)
				sliceMeta.LastModified = meta.Timestamp
				if err := st.UpdateSliceMetadata(ctx, sliceID, sliceMeta); err != nil {
					return err
				}

				if state, err := st.GetGlobalState(ctx); err == nil && state != nil {
					state.GlobalCommitHash = meta.Hash
					state.Timestamp = meta.Timestamp
					state.History = append([]*models.GlobalCommit{{
						CommitHash:     meta.Hash,
						Timestamp:      meta.Timestamp,
						MergedSliceIDs: []string{sliceID},
					}}, state.History...)
					_ = st.UpdateGlobalState(ctx, state)
				}

				if err := saveCommitSnapshot(ctx, st, sliceID, meta.Hash, meta.Timestamp, currentFileHashes); err != nil {
					return err
				}
				if err := addFileChangeRecords(ctx, st, changeRecs); err != nil {
					return err
				}

				prevCommit = meta.Hash
				head = meta.Hash
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return &gitImportResult{
			ImportedCommits: len(commits),
			HeadCommitHash:  head,
			Warnings:        warnings,
		}, nil
	}

	// Fallback: run against the provided storage backend directly (best-effort).
	if reset {
		if rs, ok := st.(resettableStorage); ok {
			if err := rs.Reset(ctx); err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("storage backend does not support reset")
		}
	}

	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		return nil, err
	}
	if _, err := st.GetSlice(ctx, sliceID); err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) && sliceID == "root_slice" {
			if err := st.InitializeRootSlice(ctx); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	currentFileHashes := make(map[string]string)
	var prevCommit string
	// Same accumulation behavior for the non-bulk path.
	head := ""
	if !reset {
		if meta, err := st.GetSliceMetadata(ctx, sliceID); err == nil && meta != nil {
			prevCommit = strings.TrimSpace(meta.HeadCommitHash)
			if prevCommit != "" {
				if snap, err := st.GetCommitSnapshot(ctx, prevCommit); err == nil && snap != nil {
					for p, h := range snap.Files {
						p = common.CleanRelativePath(p)
						if p == "" || h == "" {
							continue
						}
						currentFileHashes[p] = h
					}
				}
			}
		}
	}

	for idx, commitHash := range commits {
		metaOut, err := gitCombinedOutput(ctx, repoDir, "show", "-s", "--format=%an <%ae>%n%ct%n%s", commitHash)
		if err != nil {
			return nil, err
		}
		meta, err := parseCommitMeta(commitHash, metaOut)
		if err != nil {
			return nil, err
		}

		var diffOut []byte
		if idx == 0 {
			diffOut, err = gitCombinedOutput(ctx, repoDir, "diff-tree", "--root", "--no-commit-id", "--name-status", "-r", commitHash)
		} else {
			diffOut, err = gitCombinedOutput(ctx, repoDir, "diff", "--name-status", "-M", prevCommit, commitHash)
		}
		if err != nil {
			return nil, err
		}

		diffEntries, diffWarnings := parseDiffEntries(diffOut)
		warnings = append(warnings, diffWarnings...)

		beforeHashes := make(map[string]string, len(currentFileHashes))
		for p, h := range currentFileHashes {
			beforeHashes[p] = h
		}

		modifiedSet := make(map[string]struct{})
		changeRecs := make([]*models.FileChangeRecord, 0, len(diffEntries))

		addWarning := func(msg string) {
			warnings = append(warnings, msg)
		}

		for _, de := range diffEntries {
			switch de.Kind {
			case "A", "M":
				repoRel := de.NewPath
				mountedPath, err := mountFile(mountPath, repoRel)
				if err != nil {
					addWarning(fmt.Sprintf("skip invalid path %q in %s: %v", repoRel, commitHash, err))
					continue
				}

				blobSpec := fmt.Sprintf("%s:%s", commitHash, repoRel)
				content, err := gitCombinedOutput(ctx, repoDir, "cat-file", "-p", blobSpec)
				if err != nil {
					addWarning(fmt.Sprintf("skip missing blob %q in %s: %v", repoRel, commitHash, err))
					continue
				}
				mode, err := gitPathMode(ctx, repoDir, commitHash, repoRel)
				if err != nil {
					addWarning(fmt.Sprintf("skip missing tree entry %q in %s: %v", repoRel, commitHash, err))
					continue
				}
				executable := mode == "100755"
				symlinkTarget := ""
				if mode == "120000" {
					symlinkTarget = string(content)
				}

				oldHash := beforeHashes[mountedPath]
				newHash := storage.HashFileManifestContent(content, executable, symlinkTarget)
				if newHash, err = upsertFile(ctx, st, sliceID, mountedPath, content, newHash, executable, symlinkTarget); err != nil {
					return nil, err
				}
				currentFileHashes[mountedPath] = newHash
				modifiedSet[mountedPath] = struct{}{}

				ct := models.ChangeTypeModify
				if de.Kind == "A" {
					ct = models.ChangeTypeAdd
				}
				changeRecs = append(changeRecs, &models.FileChangeRecord{
					ID:         fmt.Sprintf("%s-%s-%s", meta.Hash, ct, mountedPath),
					SliceID:    sliceID,
					CommitHash: meta.Hash,
					Path:       mountedPath,
					ChangeType: ct,
					OldHash:    oldHash,
					NewHash:    newHash,
					Author:     meta.Author,
					Message:    meta.Message,
					Timestamp:  meta.Timestamp,
				})
			case "D":
				repoRel := de.OldPath
				mountedPath, err := mountFile(mountPath, repoRel)
				if err != nil {
					addWarning(fmt.Sprintf("skip invalid path %q in %s: %v", repoRel, commitHash, err))
					continue
				}

				oldHash := beforeHashes[mountedPath]
				delete(currentFileHashes, mountedPath)
				if err := deleteFileFromSlice(ctx, st, sliceID, mountedPath); err != nil {
					return nil, err
				}
				modifiedSet[mountedPath] = struct{}{}

				changeRecs = append(changeRecs, &models.FileChangeRecord{
					ID:         fmt.Sprintf("%s-%s-%s", meta.Hash, models.ChangeTypeDelete, mountedPath),
					SliceID:    sliceID,
					CommitHash: meta.Hash,
					Path:       mountedPath,
					ChangeType: models.ChangeTypeDelete,
					OldHash:    oldHash,
					NewHash:    "",
					Author:     meta.Author,
					Message:    meta.Message,
					Timestamp:  meta.Timestamp,
				})
			case "R":
				oldRel := de.OldPath
				newRel := de.NewPath
				oldMounted, err := mountFile(mountPath, oldRel)
				if err != nil {
					addWarning(fmt.Sprintf("skip invalid rename old path %q in %s: %v", oldRel, commitHash, err))
					continue
				}
				newMounted, err := mountFile(mountPath, newRel)
				if err != nil {
					addWarning(fmt.Sprintf("skip invalid rename new path %q in %s: %v", newRel, commitHash, err))
					continue
				}

				oldHash := beforeHashes[oldMounted]
				delete(currentFileHashes, oldMounted)
				if err := deleteFileFromSlice(ctx, st, sliceID, oldMounted); err != nil {
					return nil, err
				}

				blobSpec := fmt.Sprintf("%s:%s", commitHash, newRel)
				content, err := gitCombinedOutput(ctx, repoDir, "cat-file", "-p", blobSpec)
				if err != nil {
					addWarning(fmt.Sprintf("skip missing blob %q in %s: %v", newRel, commitHash, err))
					continue
				}
				mode, err := gitPathMode(ctx, repoDir, commitHash, newRel)
				if err != nil {
					addWarning(fmt.Sprintf("skip missing tree entry %q in %s: %v", newRel, commitHash, err))
					continue
				}
				executable := mode == "100755"
				symlinkTarget := ""
				if mode == "120000" {
					symlinkTarget = string(content)
				}
				newHash := storage.HashFileManifestContent(content, executable, symlinkTarget)
				if newHash, err = upsertFile(ctx, st, sliceID, newMounted, content, newHash, executable, symlinkTarget); err != nil {
					return nil, err
				}
				currentFileHashes[newMounted] = newHash
				modifiedSet[oldMounted] = struct{}{}
				modifiedSet[newMounted] = struct{}{}

				changeRecs = append(changeRecs, &models.FileChangeRecord{
					ID:         fmt.Sprintf("%s-%s-%s", meta.Hash, models.ChangeTypeRename, newMounted),
					SliceID:    sliceID,
					CommitHash: meta.Hash,
					Path:       newMounted,
					OldPath:    oldMounted,
					ChangeType: models.ChangeTypeRename,
					OldHash:    oldHash,
					NewHash:    newHash,
					Author:     meta.Author,
					Message:    meta.Message,
					Timestamp:  meta.Timestamp,
				})
			default:
				addWarning(fmt.Sprintf("skip unsupported diff kind %q in %s", de.Kind, commitHash))
			}
		}

		modifiedMountedPaths := make([]string, 0, len(modifiedSet))
		for p := range modifiedSet {
			modifiedMountedPaths = append(modifiedMountedPaths, p)
		}
		sort.Strings(modifiedMountedPaths)

		if err := st.AddSliceCommit(ctx, sliceID, &models.Commit{
			CommitHash: meta.Hash,
			ParentHash: prevCommit,
			Timestamp:  meta.Timestamp,
			Message:    meta.Message,
		}); err != nil {
			return nil, err
		}

		sliceMeta, err := st.GetSliceMetadata(ctx, sliceID)
		if err != nil {
			return nil, err
		}
		sliceMeta.HeadCommitHash = meta.Hash
		sliceMeta.ModifiedFiles = modifiedMountedPaths
		sliceMeta.ModifiedFilesCount = len(modifiedMountedPaths)
		sliceMeta.LastModified = meta.Timestamp
		if err := st.UpdateSliceMetadata(ctx, sliceID, sliceMeta); err != nil {
			return nil, err
		}

		if state, err := st.GetGlobalState(ctx); err == nil && state != nil {
			state.GlobalCommitHash = meta.Hash
			state.Timestamp = meta.Timestamp
			state.History = append([]*models.GlobalCommit{{
				CommitHash:     meta.Hash,
				Timestamp:      meta.Timestamp,
				MergedSliceIDs: []string{sliceID},
			}}, state.History...)
			_ = st.UpdateGlobalState(ctx, state)
		}

		if err := saveCommitSnapshot(ctx, st, sliceID, meta.Hash, meta.Timestamp, currentFileHashes); err != nil {
			return nil, err
		}
		if err := addFileChangeRecords(ctx, st, changeRecs); err != nil {
			return nil, err
		}

		prevCommit = meta.Hash
		head = meta.Hash
	}

	return &gitImportResult{
		ImportedCommits: len(commits),
		HeadCommitHash:  head,
		Warnings:        warnings,
	}, nil
}
