package gscli

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	slicev1 "github.com/niczy/gitslice/proto/slice"
)

const checkoutAllowedRootsPageSize = 500

func populateCheckoutAllowedAddRoots(ctx context.Context, cli *CLI, sliceID string, index *localCheckoutIndex) {
	if index == nil {
		return
	}
	roots := checkoutAllowedAddRootsFromRemote(ctx, cli, sliceID)
	if len(roots) == 0 {
		roots = checkoutAllowedAddRoots(index)
	}
	index.AllowedAddRoots = normalizeCheckoutAllowedAddRoots(roots)
}

func ensureCheckoutAllowedAddRootDirs(dir string, index *localCheckoutIndex) error {
	if index == nil {
		return nil
	}
	roots := checkoutAllowedAddRoots(index)
	if len(roots) == 0 {
		return nil
	}

	directoryPaths := make(map[string]struct{}, len(index.Directories)+len(roots)*2)
	for _, existing := range index.Directories {
		directoryPaths[normalizeTrackedDirectoryPath(existing.Path)] = struct{}{}
	}
	for _, root := range roots {
		root = filepath.Clean(strings.Trim(strings.TrimSpace(root), "/"))
		if root == "" || root == "." {
			continue
		}
		if err := os.MkdirAll(filepath.Join(dir, root), 0o755); err != nil {
			return err
		}
		addParentDirectoryPaths(root, directoryPaths)
		directoryPaths[normalizeTrackedDirectoryPath(root)] = struct{}{}
	}

	index.Directories = make([]checkoutTrackedDirectory, 0, len(directoryPaths))
	for dirPath := range directoryPaths {
		record, _, err := currentCheckoutDirectorySnapshot(dir, dirPath)
		if err != nil {
			return err
		}
		index.Directories = append(index.Directories, record)
	}
	sort.Slice(index.Directories, func(i, j int) bool {
		return index.Directories[i].Path < index.Directories[j].Path
	})
	return nil
}

func checkoutAllowedAddRootsFromRemote(ctx context.Context, cli *CLI, sliceID string) []string {
	sliceID = strings.TrimSpace(sliceID)
	if cli == nil || cli.sliceClient == nil || sliceID == "" {
		return nil
	}
	for offset := int32(0); ; offset += checkoutAllowedRootsPageSize {
		resp, err := cli.sliceClient.ListSlices(ctx, &slicev1.ListSlicesRequest{
			Limit:  checkoutAllowedRootsPageSize,
			Offset: offset,
		})
		if err != nil {
			log.Printf("Warning: failed to fetch slice tracked roots: %v", err)
			return nil
		}
		for _, slice := range resp.GetSlices() {
			if strings.TrimSpace(slice.GetSliceId()) == sliceID {
				return checkoutAllowedAddRootsFromSliceInfo(sliceID, slice)
			}
		}
		if len(resp.GetSlices()) == 0 || int(offset)+len(resp.GetSlices()) >= int(resp.GetTotal()) {
			return nil
		}
	}
}

func checkoutAllowedAddRootsFromSliceInfo(sliceID string, slice *slicev1.SliceInfo) []string {
	roots := make([]string, 0, len(slice.GetFolderMounts())+1)
	if root := homeSliceCheckoutRoot(sliceID); root != "" {
		roots = append(roots, root)
	}
	for _, mount := range slice.GetFolderMounts() {
		if mount == nil {
			continue
		}
		alias := strings.TrimSpace(mount.GetAlias())
		if alias == "" {
			alias = strings.TrimSpace(mount.GetSourcePath())
		}
		roots = append(roots, alias)
	}
	return normalizeCheckoutAllowedAddRoots(roots)
}
