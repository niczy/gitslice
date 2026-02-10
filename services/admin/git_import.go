package adminservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	BulkWrite(ctx context.Context, fn func(mem *storage.InMemoryStorage) error) error
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

func defaultMountPath(repoRoot string) string {
	name := path.Base(filepath.ToSlash(repoRoot))
	if name == "" || name == "." || name == "/" {
		name = "repo"
	}
	return "/o/genesis/projects/" + name
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

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func upsertFile(ctx context.Context, st storage.Storage, sliceID string, mountedPath string, content []byte, contentHash string) error {
	entry := &models.DirectoryEntry{
		ID:       common.GenerateEntryID(sliceID, mountedPath),
		Path:     mountedPath,
		Type:     "file",
		ParentID: sliceID,
		Content:  content,
		Size:     int64(len(content)),
	}
	if err := st.AddEntry(ctx, entry); err != nil {
		if errors.Is(err, storage.ErrEntryExists) {
			if err := st.UpdateEntry(ctx, entry); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	if err := st.AddFileToSlice(ctx, mountedPath, sliceID); err != nil {
		return err
	}

	return st.AddFileContent(ctx, &models.FileContent{
		FileID:  mountedPath,
		Path:    mountedPath,
		Content: content,
		Size:    int64(len(content)),
		Hash:    contentHash,
	})
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

func importGitRepo(ctx context.Context, st storage.Storage, repoPath string, ref string, sliceID string, mountPath string, reset bool, firstParent bool, maxCommits int) (*gitImportResult, error) {
	if ref == "" {
		ref = "HEAD"
	}

	repoRoot, err := gitRepoRoot(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	if mountPath == "" {
		mountPath = defaultMountPath(repoRoot)
	}
	if sliceID == "" {
		sliceID = "root_slice"
	}

	warnings := []string{}
	if reset {
		if rs, ok := st.(resettableStorage); ok {
			if err := rs.Reset(ctx); err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("storage backend does not support reset")
		}
	}

	args := []string{"rev-list", "--reverse"}
	if firstParent {
		args = append(args, "--first-parent")
	}
	args = append(args, ref)

	out, err := gitCombinedOutput(ctx, repoRoot, args...)
	if err != nil {
		return nil, err
	}
	commits := parseRevList(out, maxCommits)
	if len(commits) == 0 {
		return &gitImportResult{ImportedCommits: 0, HeadCommitHash: "", Warnings: []string{"no commits found"}}, nil
	}

	// Fast path: run the full import in a single Postgres BulkWrite so we persist once.
	if bw, ok := st.(bulkWriter); ok {
		var head string
		err := bw.BulkWrite(ctx, func(mem *storage.InMemoryStorage) error {
			if err := common.EnsureRootSliceInitialized(ctx, mem); err != nil {
				return err
			}
			if _, err := mem.GetSlice(ctx, sliceID); err != nil {
				if errors.Is(err, storage.ErrSliceNotFound) && sliceID == "root_slice" {
					if err := mem.InitializeRootSlice(ctx); err != nil {
						return err
					}
				} else {
					return err
				}
			}

			currentFileHashes := make(map[string]string)
			var prevCommit string

			for idx, commitHash := range commits {
				metaOut, err := gitCombinedOutput(ctx, repoRoot, "show", "-s", "--format=%an <%ae>%n%ct%n%s", commitHash)
				if err != nil {
					return err
				}
				meta, err := parseCommitMeta(commitHash, metaOut)
				if err != nil {
					return err
				}

				var diffOut []byte
				if idx == 0 {
					diffOut, err = gitCombinedOutput(ctx, repoRoot, "diff-tree", "--root", "--no-commit-id", "--name-status", "-r", commitHash)
				} else {
					// Use a two-tree diff so merge commits include the diff vs their first parent.
					diffOut, err = gitCombinedOutput(ctx, repoRoot, "diff", "--name-status", "-M", prevCommit, commitHash)
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
						content, err := gitCombinedOutput(ctx, repoRoot, "cat-file", "-p", blobSpec)
						if err != nil {
							addWarning(fmt.Sprintf("skip missing blob %q in %s: %v", repoRel, commitHash, err))
							continue
						}

						oldHash := beforeHashes[mountedPath]
						newHash := sha256Hex(content)
						if err := upsertFile(ctx, mem, sliceID, mountedPath, content, newHash); err != nil {
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
						if err := deleteFileFromSlice(ctx, mem, sliceID, mountedPath); err != nil {
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
						if err := deleteFileFromSlice(ctx, mem, sliceID, oldMounted); err != nil {
							return err
						}

						blobSpec := fmt.Sprintf("%s:%s", commitHash, newRel)
						content, err := gitCombinedOutput(ctx, repoRoot, "cat-file", "-p", blobSpec)
						if err != nil {
							addWarning(fmt.Sprintf("skip missing blob %q in %s: %v", newRel, commitHash, err))
							continue
						}
						newHash := sha256Hex(content)
						if err := upsertFile(ctx, mem, sliceID, newMounted, content, newHash); err != nil {
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

				if err := mem.AddSliceCommit(ctx, sliceID, &models.Commit{
					CommitHash: meta.Hash,
					ParentHash: prevCommit,
					Timestamp:  meta.Timestamp,
					Message:    meta.Message,
				}); err != nil {
					return err
				}

				sliceMeta, err := mem.GetSliceMetadata(ctx, sliceID)
				if err != nil {
					return err
				}
				sliceMeta.HeadCommitHash = meta.Hash
				sliceMeta.ModifiedFiles = modifiedMountedPaths
				sliceMeta.ModifiedFilesCount = len(modifiedMountedPaths)
				sliceMeta.LastModified = meta.Timestamp
				if err := mem.UpdateSliceMetadata(ctx, sliceID, sliceMeta); err != nil {
					return err
				}

				if state, err := mem.GetGlobalState(ctx); err == nil && state != nil {
					state.GlobalCommitHash = meta.Hash
					state.Timestamp = meta.Timestamp
					state.History = append([]*models.GlobalCommit{{
						CommitHash:     meta.Hash,
						Timestamp:      meta.Timestamp,
						MergedSliceIDs: []string{sliceID},
					}}, state.History...)
					_ = mem.UpdateGlobalState(ctx, state)
				}

				if err := saveCommitSnapshot(ctx, mem, sliceID, meta.Hash, meta.Timestamp, currentFileHashes); err != nil {
					return err
				}
				if err := addFileChangeRecords(ctx, mem, changeRecs); err != nil {
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
	head := ""

	for idx, commitHash := range commits {
		metaOut, err := gitCombinedOutput(ctx, repoRoot, "show", "-s", "--format=%an <%ae>%n%ct%n%s", commitHash)
		if err != nil {
			return nil, err
		}
		meta, err := parseCommitMeta(commitHash, metaOut)
		if err != nil {
			return nil, err
		}

		var diffOut []byte
		if idx == 0 {
			diffOut, err = gitCombinedOutput(ctx, repoRoot, "diff-tree", "--root", "--no-commit-id", "--name-status", "-r", commitHash)
		} else {
			diffOut, err = gitCombinedOutput(ctx, repoRoot, "diff", "--name-status", "-M", prevCommit, commitHash)
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
				content, err := gitCombinedOutput(ctx, repoRoot, "cat-file", "-p", blobSpec)
				if err != nil {
					addWarning(fmt.Sprintf("skip missing blob %q in %s: %v", repoRel, commitHash, err))
					continue
				}

				oldHash := beforeHashes[mountedPath]
				newHash := sha256Hex(content)
				if err := upsertFile(ctx, st, sliceID, mountedPath, content, newHash); err != nil {
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
				content, err := gitCombinedOutput(ctx, repoRoot, "cat-file", "-p", blobSpec)
				if err != nil {
					addWarning(fmt.Sprintf("skip missing blob %q in %s: %v", newRel, commitHash, err))
					continue
				}
				newHash := sha256Hex(content)
				if err := upsertFile(ctx, st, sliceID, newMounted, content, newHash); err != nil {
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
