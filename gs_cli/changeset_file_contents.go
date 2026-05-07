package gscli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func buildLocalChangesetFileContents(dir string, files []string) ([]*slicev1.FileContentChange, error) {
	if len(files) == 0 {
		return nil, nil
	}

	var trackedFiles map[string]checkoutTrackedFile
	index, err := readCheckoutIndex(dir)
	if err != nil {
		return nil, err
	}
	if index != nil {
		trackedFiles = newCheckoutIndexLookup(index).files
	}

	changes := make([]*slicev1.FileContentChange, 0, len(files))
	for _, rawPath := range files {
		cleanedPath, err := cleanLocalChangesetFilePath(rawPath)
		if err != nil {
			return nil, err
		}
		fullPath := filepath.Join(dir, filepath.FromSlash(cleanedPath))
		info, err := os.Lstat(fullPath)
		if os.IsNotExist(err) {
			if _, tracked := trackedFiles[filepath.Clean(filepath.FromSlash(cleanedPath))]; !tracked {
				continue
			}
			changes = append(changes, &slicev1.FileContentChange{
				Path:    cleanedPath,
				Deleted: true,
			})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", cleanedPath, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("%s is a directory; pass file paths when creating a changeset", cleanedPath)
		}

		mode := info.Mode()
		if mode&os.ModeSymlink != 0 {
			target, err := os.Readlink(fullPath)
			if err != nil {
				return nil, fmt.Errorf("read symlink %s: %w", cleanedPath, err)
			}
			changes = append(changes, &slicev1.FileContentChange{
				Path:          cleanedPath,
				Content:       []byte(target),
				SymlinkTarget: target,
			})
			continue
		}
		if !mode.IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file", cleanedPath)
		}

		content, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", cleanedPath, err)
		}
		changes = append(changes, &slicev1.FileContentChange{
			Path:       cleanedPath,
			Content:    content,
			Executable: mode.Perm()&0o111 != 0,
		})
	}
	return changes, nil
}

func cleanLocalChangesetFilePath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("file path is empty")
	}
	if filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("file path must be relative: %s", trimmed)
	}

	cleaned := filepath.ToSlash(filepath.Clean(trimmed))
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("file path is empty")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("file path must stay inside the checkout: %s", raw)
	}
	if cleaned == ".git" || strings.HasPrefix(cleaned, ".git/") ||
		cleaned == ".gs" || strings.HasPrefix(cleaned, ".gs/") {
		return "", fmt.Errorf("file path is metadata, not slice content: %s", raw)
	}
	return cleaned, nil
}
