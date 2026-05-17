package homeslice

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

const idPrefix = "home_"

type BackfillResult struct {
	Username          string
	HomeSliceID       string
	Created           bool
	Seeded            bool
	FilesCopied       int
	DirectoriesCopied int
}

type PathHeadPromotionOptions struct {
	CommitHash    string
	ParentHash    string
	Message       string
	ModifiedPaths []string
	CommittedAt   time.Time
}

type PathHeadPromotionResult struct {
	HomeID        string
	HomeSliceID   string
	CommitHash    string
	ParentHash    string
	ModifiedPaths []string
	Changed       bool
}

// IDForUsername returns the deterministic home-slice ID for a user.
func IDForUsername(username string) string {
	return idPrefix + strings.TrimSpace(username)
}

func IsHomeSliceID(sliceID string) bool {
	return strings.HasPrefix(strings.TrimSpace(sliceID), idPrefix)
}

func UsernameFromSliceID(sliceID string) string {
	if !IsHomeSliceID(sliceID) {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(sliceID), idPrefix)
}

// ExternalSlugForSlice returns the public slug for a home slice.
func ExternalSlugForSlice(slice *models.Slice) (string, bool) {
	if slice == nil {
		return "", false
	}
	sliceID := strings.TrimSpace(slice.ID)
	if !strings.HasPrefix(sliceID, idPrefix) {
		return "", false
	}
	username := strings.TrimSpace(strings.TrimPrefix(sliceID, idPrefix))
	if username == "" {
		return "", false
	}
	if createdBy := strings.TrimSpace(slice.CreatedBy); createdBy != "" && createdBy != username {
		return "", false
	}
	return username, true
}

// VisibleRootPath returns the absolute user-visible home path for a user.
func VisibleRootPath(username string) string {
	username = strings.TrimSpace(username)
	if username == "" {
		return ""
	}
	return "/" + username
}

// RelativeRootPath returns the stored relative path used for a user's top-level directory.
func RelativeRootPath(username string) string {
	return strings.TrimPrefix(VisibleRootPath(username), "/")
}

func rootPathForUsername(ctx context.Context, st storage.Storage, username string) string {
	username = strings.TrimSpace(username)
	if username == "" {
		return ""
	}
	user, err := st.GetUser(ctx, username)
	if err == nil {
		rootPath := strings.TrimPrefix(strings.TrimSpace(user.RootPath), "/")
		if rootPath != "" {
			return rootPath
		}
	}
	return RelativeRootPath(username)
}

// ResolveLiveBackingSliceID returns a user's home slice when a mounted slice was
// created from root paths that all live under that user's home root.
// This lets read-side projections see the same live backing tree that the git
// layer writes to before asynchronous root projection catches up.
func ResolveLiveBackingSliceID(ctx context.Context, st storage.Storage, slice *models.Slice) (string, bool, error) {
	if st == nil || slice == nil || strings.TrimSpace(slice.ParentSlice) == "" || len(slice.FolderMounts) == 0 {
		return "", false, nil
	}
	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) || errors.Is(err, storage.ErrEntryNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if rootSlice == nil || strings.TrimSpace(rootSlice.ID) == "" || strings.TrimSpace(rootSlice.ID) != strings.TrimSpace(slice.ParentSlice) {
		return "", false, nil
	}

	for _, username := range homeProjectionCandidates(slice) {
		homeID := IDForUsername(username)
		homeSlice, err := st.GetSlice(ctx, homeID)
		if err != nil {
			if errors.Is(err, storage.ErrSliceNotFound) {
				continue
			}
			return "", false, err
		}
		if homeSlice == nil {
			continue
		}
		rootPath := rootPathForUsername(ctx, st, username)
		if rootPath == "" {
			continue
		}
		allUnderHomeRoot := true
		for _, mount := range slice.FolderMounts {
			source := common.CleanRelativePath(mount.SourcePath)
			if source != rootPath && !strings.HasPrefix(source, rootPath+"/") {
				allUnderHomeRoot = false
				break
			}
		}
		if allUnderHomeRoot {
			return homeSlice.ID, true, nil
		}
	}
	return "", false, nil
}

func homeProjectionCandidates(slice *models.Slice) []string {
	if slice == nil {
		return nil
	}
	seen := make(map[string]struct{})
	result := make([]string, 0, len(slice.Owners)+1)
	add := func(username string) {
		username = strings.TrimSpace(username)
		if username == "" {
			return
		}
		if _, ok := seen[username]; ok {
			return
		}
		seen[username] = struct{}{}
		result = append(result, username)
	}
	for _, owner := range slice.Owners {
		add(owner)
	}
	add(slice.CreatedBy)
	return result
}

// EnsureUserHomeSlice provisions the user's deterministic home slice and reserves the
// user's top-level directory in both the home slice and the root slice.
func EnsureUserHomeSlice(ctx context.Context, st storage.Storage, username string) (*models.Slice, error) {
	if st == nil {
		return nil, fmt.Errorf("storage is nil")
	}

	user, err := st.EnsureUser(ctx, strings.TrimSpace(username))
	if err != nil {
		return nil, err
	}
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		return nil, err
	}

	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		return nil, err
	}

	homeSlice, _, err := ensureHomeSliceRecord(ctx, st, user)
	if err != nil {
		return nil, err
	}
	if err := ensureInitialHeadArtifacts(ctx, st, homeSlice, "create home slice"); err != nil {
		return nil, err
	}

	rootPath := strings.TrimPrefix(strings.TrimSpace(user.RootPath), "/")
	if rootPath == "" {
		rootPath = RelativeRootPath(user.Username)
	}
	if _, err := ensureDirectory(ctx, st, homeSlice.ID, rootPath); err != nil {
		return nil, err
	}
	if _, err := ensureDirectory(ctx, st, rootSlice.ID, rootPath); err != nil {
		return nil, err
	}
	if err := ensureHomeRootPathHead(ctx, st, user.Username, homeSlice.ID, rootPath); err != nil {
		return nil, err
	}
	return homeSlice, nil
}

func BackfillUserHomeSlice(ctx context.Context, st storage.Storage, username string) (*BackfillResult, error) {
	if st == nil {
		return nil, fmt.Errorf("storage is nil")
	}

	user, err := st.GetUser(ctx, strings.TrimSpace(username))
	if err != nil {
		return nil, err
	}
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		return nil, err
	}

	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		return nil, err
	}

	homeSlice, created, err := ensureHomeSliceRecord(ctx, st, user)
	if err != nil {
		return nil, err
	}
	if err := ensureInitialHeadArtifacts(ctx, st, homeSlice, "create home slice"); err != nil {
		return nil, err
	}

	rootPath := strings.TrimPrefix(strings.TrimSpace(user.RootPath), "/")
	if rootPath == "" {
		rootPath = RelativeRootPath(user.Username)
	}
	if _, err := ensureDirectory(ctx, st, homeSlice.ID, rootPath); err != nil {
		return nil, err
	}
	if _, err := ensureDirectory(ctx, st, rootSlice.ID, rootPath); err != nil {
		return nil, err
	}
	if err := ensureHomeRootPathHead(ctx, st, user.Username, homeSlice.ID, rootPath); err != nil {
		return nil, err
	}

	filesCopied, directoriesCopied, err := copyRootSubtreeToHomeSlice(ctx, st, rootSlice.ID, homeSlice.ID, rootPath)
	if err != nil {
		return nil, err
	}
	if filesCopied > 0 || directoriesCopied > 0 {
		if err := recordSliceStateCommit(ctx, st, homeSlice, "backfill home slice from root"); err != nil {
			return nil, err
		}
	}

	return &BackfillResult{
		Username:          user.Username,
		HomeSliceID:       homeSlice.ID,
		Created:           created,
		Seeded:            filesCopied > 0 || directoriesCopied > 0,
		FilesCopied:       filesCopied,
		DirectoriesCopied: directoriesCopied,
	}, nil
}

// PromoteHomePathHeadsToSliceCommit records the current path-head view as the
// trusted home slice head. Path heads are the merge-time authority; this commit
// snapshot is the stable read cursor used by file listing, pinned reads, and CI.
func PromoteHomePathHeadsToSliceCommit(ctx context.Context, st storage.Storage, homeID string, opts PathHeadPromotionOptions) (*PathHeadPromotionResult, error) {
	if st == nil {
		return nil, fmt.Errorf("storage is nil")
	}
	headStore, ok := st.(storage.HomePathHeadStore)
	if !ok {
		return nil, nil
	}
	homeID = normalizePromotionHomeID(homeID)
	if homeID == "" {
		return nil, nil
	}
	homeSliceID := IDForUsername(homeID)
	result := &PathHeadPromotionResult{
		HomeID:      homeID,
		HomeSliceID: homeSliceID,
	}

	homeSlice, err := st.GetSlice(ctx, homeSliceID)
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return result, nil
		}
		return nil, err
	}
	if homeSlice == nil {
		return result, nil
	}

	meta, err := st.GetSliceMetadata(ctx, homeSliceID)
	if err != nil {
		return nil, err
	}
	parentHash := strings.TrimSpace(opts.ParentHash)
	if parentHash == "" {
		parentHash = strings.TrimSpace(meta.HeadCommitHash)
	}
	result.ParentHash = parentHash

	heads, err := headStore.ListHomePathHeads(ctx, homeID, 100000)
	if err != nil {
		return nil, err
	}
	if len(heads) >= 100000 {
		return nil, fmt.Errorf("home %s has too many path heads to promote in one commit", homeID)
	}

	snapshotFiles := make(map[string]string, len(heads))
	for _, head := range heads {
		if head == nil || head.Deleted {
			continue
		}
		if strings.TrimSpace(head.EntryType) != "file" {
			continue
		}
		filePath := common.CleanRelativePath(head.Path)
		if filePath == "" {
			continue
		}
		contentHash := strings.TrimSpace(head.ManifestHash)
		if contentHash == "" {
			contentHash = strings.TrimSpace(head.ContentHash)
		}
		if contentHash == "" {
			continue
		}
		snapshotFiles[filePath] = contentHash
	}

	var parentFiles map[string]string
	if parentHash != "" {
		parentSnapshot, err := st.GetCommitSnapshot(ctx, parentHash)
		if err != nil {
			if !errors.Is(err, storage.ErrCommitNotFound) && !errors.Is(err, storage.ErrEntryNotFound) {
				return nil, err
			}
		} else if parentSnapshot != nil {
			parentFiles = parentSnapshot.Files
		}
	}
	if parentFiles == nil {
		parentFiles = map[string]string{}
	}

	commitHash := strings.TrimSpace(opts.CommitHash)
	if commitHash == "" && snapshotFileMapsEqual(parentFiles, snapshotFiles) {
		result.CommitHash = parentHash
		result.ModifiedPaths = normalizePromotionModifiedPaths(opts.ModifiedPaths)
		return result, nil
	}
	if commitHash == "" {
		commitHash = common.GenerateCommitID()
	}
	result.CommitHash = commitHash

	if existingSnapshot, err := st.GetCommitSnapshot(ctx, commitHash); err == nil && existingSnapshot != nil && snapshotFileMapsEqual(existingSnapshot.Files, snapshotFiles) && strings.TrimSpace(meta.HeadCommitHash) == commitHash {
		result.ModifiedPaths = normalizePromotionModifiedPaths(opts.ModifiedPaths)
		return result, nil
	} else if err != nil && !errors.Is(err, storage.ErrCommitNotFound) && !errors.Is(err, storage.ErrEntryNotFound) {
		return nil, err
	}

	committedAt := opts.CommittedAt
	if committedAt.IsZero() {
		committedAt = time.Now()
	}
	message := strings.TrimSpace(opts.Message)
	if message == "" {
		message = "sync home slice from path heads"
	}
	modifiedPaths := normalizePromotionModifiedPaths(opts.ModifiedPaths)
	if len(modifiedPaths) == 0 {
		modifiedPaths = diffSnapshotFilePaths(parentFiles, snapshotFiles)
	}

	if err := st.AddSliceCommit(ctx, homeSliceID, &models.Commit{
		CommitHash: commitHash,
		ParentHash: parentHash,
		Timestamp:  committedAt,
		Message:    message,
	}); err != nil {
		return nil, err
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    homeSliceID,
		Files:      snapshotFiles,
		Timestamp:  committedAt,
	}); err != nil {
		return nil, err
	}
	if err := st.UpdateSliceMetadata(ctx, homeSliceID, &models.SliceMetadata{
		SliceID:            homeSliceID,
		HeadCommitHash:     commitHash,
		ModifiedFiles:      modifiedPaths,
		LastModified:       committedAt,
		ModifiedFilesCount: len(modifiedPaths),
	}); err != nil {
		return nil, err
	}

	result.ModifiedPaths = modifiedPaths
	result.Changed = true
	return result, nil
}

func normalizePromotionHomeID(homeID string) string {
	homeID = strings.Trim(strings.TrimSpace(homeID), "/")
	if homeID == "" || homeID == "global" {
		return ""
	}
	if username := UsernameFromSliceID(homeID); username != "" {
		return username
	}
	return homeID
}

func normalizePromotionModifiedPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, rawPath := range paths {
		filePath := common.CleanRelativePath(rawPath)
		if filePath == "" {
			continue
		}
		if _, ok := seen[filePath]; ok {
			continue
		}
		seen[filePath] = struct{}{}
		out = append(out, filePath)
	}
	sort.Strings(out)
	return out
}

func snapshotFileMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for path, aHash := range a {
		if strings.TrimSpace(aHash) != strings.TrimSpace(b[path]) {
			return false
		}
	}
	return true
}

func diffSnapshotFilePaths(before, after map[string]string) []string {
	seen := make(map[string]struct{}, len(before)+len(after))
	for filePath := range before {
		seen[filePath] = struct{}{}
	}
	for filePath := range after {
		seen[filePath] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for filePath := range seen {
		if strings.TrimSpace(before[filePath]) != strings.TrimSpace(after[filePath]) {
			out = append(out, filePath)
		}
	}
	sort.Strings(out)
	return out
}

func ensureHomeRootPathHead(ctx context.Context, st storage.Storage, username, homeSliceID, rootPath string) error {
	heads, ok := st.(storage.HomePathHeadStore)
	if !ok {
		return nil
	}
	rootPath = common.CleanRelativePath(rootPath)
	username = strings.TrimSpace(username)
	homeSliceID = strings.TrimSpace(homeSliceID)
	if username == "" || homeSliceID == "" || rootPath == "" {
		return nil
	}
	sourceCommitHash := ""
	if metadata, err := st.GetSliceMetadata(ctx, homeSliceID); err == nil && metadata != nil {
		sourceCommitHash = strings.TrimSpace(metadata.HeadCommitHash)
	} else if err != nil && !errors.Is(err, storage.ErrSliceNotFound) && !errors.Is(err, storage.ErrEntryNotFound) {
		return err
	}
	return heads.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           username,
		Path:             rootPath,
		EntryType:        "directory",
		PathVersion:      1,
		SourceSliceID:    homeSliceID,
		SourceCommitHash: sourceCommitHash,
		UpdatedAt:        time.Now(),
	}})
}

func ensureHomeSliceRecord(ctx context.Context, st storage.Storage, user *models.User) (*models.Slice, bool, error) {
	if user == nil {
		return nil, false, fmt.Errorf("user is nil")
	}

	sliceID := IDForUsername(user.Username)
	slice, err := st.GetSlice(ctx, sliceID)
	if err == nil {
		return slice, false, nil
	}
	if err != storage.ErrSliceNotFound {
		return nil, false, err
	}

	now := time.Now()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:          sliceID,
		Name:        user.Username,
		Description: fmt.Sprintf("Home slice for %s", user.Username),
		Visibility:  models.VisibilityPrivate,
		Files:       []string{},
		Owners:      []string{user.Username},
		CreatedBy:   user.Username,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil && err != storage.ErrSliceAlreadyExists {
		return nil, false, err
	}
	slice, err = st.GetSlice(ctx, sliceID)
	return slice, true, err
}

func ensureInitialHeadArtifacts(ctx context.Context, st storage.Storage, slice *models.Slice, message string) error {
	if slice == nil {
		return fmt.Errorf("slice is nil")
	}

	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		return err
	}

	expectedHead := common.GenerateInitialCommitID(slice.ID)
	if strings.TrimSpace(meta.HeadCommitHash) != expectedHead {
		return nil
	}

	createdAt := slice.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	if _, err := st.GetCommitByHash(ctx, slice.ID, expectedHead); err != nil {
		if err != storage.ErrCommitNotFound {
			return err
		}
		if err := st.AddSliceCommit(ctx, slice.ID, &models.Commit{
			CommitHash: expectedHead,
			ParentHash: "",
			Timestamp:  createdAt,
			Message:    strings.TrimSpace(message),
		}); err != nil && !isDuplicateWrite(err) {
			return err
		}
	}

	if _, err := st.GetCommitSnapshot(ctx, expectedHead); err != nil {
		if err != storage.ErrCommitNotFound && err != storage.ErrEntryNotFound {
			return err
		}
		if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
			CommitHash: expectedHead,
			SliceID:    slice.ID,
			Files:      map[string]string{},
			Timestamp:  createdAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func ensureDirectory(ctx context.Context, st storage.Storage, sliceID, dirPath string) (bool, error) {
	dirPath = strings.TrimSpace(dirPath)
	if dirPath == "" {
		return false, nil
	}

	entry, err := st.GetEntryByPath(ctx, sliceID, dirPath)
	if err == nil {
		if entry.Type != "directory" {
			return false, fmt.Errorf("path %q already exists as %s in slice %s", dirPath, entry.Type, sliceID)
		}
		return false, nil
	}
	if err != storage.ErrEntryNotFound {
		return false, err
	}

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(sliceID, dirPath),
		Path:     dirPath,
		Type:     "directory",
		ParentID: sliceID,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func copyRootSubtreeToHomeSlice(ctx context.Context, st storage.Storage, rootSliceID, homeSliceID, rootPath string) (int, int, error) {
	entries, err := collectSubtreeEntries(ctx, st, rootSliceID, rootPath)
	if err != nil {
		return 0, 0, err
	}

	filesCopied := 0
	directoriesCopied := 0
	for _, entry := range entries {
		if entry == nil || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		switch entry.Type {
		case "directory":
			created, err := ensureDirectory(ctx, st, homeSliceID, entry.Path)
			if err != nil {
				return 0, 0, err
			}
			if created {
				directoriesCopied++
			}
		default:
			copied, err := copyFile(ctx, st, rootSliceID, homeSliceID, entry.Path)
			if err != nil {
				return 0, 0, err
			}
			if copied {
				filesCopied++
			}
		}
	}
	return filesCopied, directoriesCopied, nil
}

func collectSubtreeEntries(ctx context.Context, st storage.Storage, sliceID, rootPath string) ([]*models.DirectoryEntry, error) {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return nil, nil
	}

	rootEntry, err := st.GetEntryByPath(ctx, sliceID, rootPath)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, nil
		}
		return nil, err
	}

	entries := []*models.DirectoryEntry{rootEntry}
	queue := []string{rootEntry.ID}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]

		children, err := st.ListEntries(ctx, sliceID, parentID)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child == nil {
				continue
			}
			entries = append(entries, child)
			if child.Type == "directory" {
				queue = append(queue, child.ID)
			}
		}
	}
	return entries, nil
}

func copyFile(ctx context.Context, st storage.Storage, sourceSliceID, targetSliceID, filePath string) (bool, error) {
	if entry, err := st.GetEntryByPath(ctx, targetSliceID, filePath); err == nil {
		if entry.Type != "file" {
			return false, fmt.Errorf("path %q already exists as %s in slice %s", filePath, entry.Type, targetSliceID)
		}
		if _, err := storage.ReadSliceFileContent(ctx, st, targetSliceID, filePath); err == nil {
			return false, nil
		} else if err != storage.ErrEntryNotFound {
			return false, err
		}
	} else if err != storage.ErrEntryNotFound {
		return false, err
	}

	sourceFile, err := storage.ReadSliceFileContent(ctx, st, sourceSliceID, filePath)
	if err != nil {
		return false, err
	}
	sourceEntry, err := st.GetEntryByPath(ctx, sourceSliceID, filePath)
	if err != nil {
		return false, err
	}

	manifest, err := storage.WriteSliceFileManifestWithMetadata(
		ctx,
		st,
		targetSliceID,
		filePath,
		append([]byte(nil), sourceFile.Content...),
		sourceEntry.Executable,
		sourceEntry.SymlinkTarget,
	)
	if err != nil {
		return false, err
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:            common.GenerateEntryID(targetSliceID, filePath),
		Path:          filePath,
		Type:          "file",
		ParentID:      targetSliceID,
		Size:          sourceFile.Size,
		Hash:          manifest.Hash,
		Executable:    sourceEntry.Executable,
		SymlinkTarget: sourceEntry.SymlinkTarget,
	}); err != nil {
		return false, err
	}
	if err := st.AddFileToSlice(ctx, filePath, targetSliceID); err != nil {
		return false, err
	}
	return true, nil
}

func recordSliceStateCommit(ctx context.Context, st storage.Storage, slice *models.Slice, message string) error {
	if slice == nil {
		return fmt.Errorf("slice is nil")
	}

	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		return err
	}

	entries, err := collectSliceEntries(ctx, st, slice.ID)
	if err != nil {
		return err
	}

	snapshotFiles := make(map[string]string, len(entries))
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		paths = append(paths, entry.Path)
		if entry.Type != "file" {
			continue
		}
		manifest, err := st.GetFileManifest(ctx, slice.ID, entry.Path)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				continue
			}
			return err
		}
		if strings.TrimSpace(manifest.Hash) == "" {
			continue
		}
		snapshotFiles[entry.Path] = strings.TrimSpace(manifest.Hash)
	}

	now := time.Now()
	commitHash := common.GenerateCommitID()
	if err := st.AddSliceCommit(ctx, slice.ID, &models.Commit{
		CommitHash: commitHash,
		ParentHash: meta.HeadCommitHash,
		Timestamp:  now,
		Message:    strings.TrimSpace(message),
	}); err != nil {
		return err
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    slice.ID,
		Files:      snapshotFiles,
		Timestamp:  now,
	}); err != nil {
		return err
	}
	return st.UpdateSliceMetadata(ctx, slice.ID, &models.SliceMetadata{
		SliceID:            slice.ID,
		HeadCommitHash:     commitHash,
		ModifiedFiles:      paths,
		LastModified:       now,
		ModifiedFilesCount: len(paths),
	})
}

func collectSliceEntries(ctx context.Context, st storage.Storage, sliceID string) ([]*models.DirectoryEntry, error) {
	rootChildren, err := st.ListEntries(ctx, sliceID, sliceID)
	if err != nil {
		return nil, err
	}

	entries := make([]*models.DirectoryEntry, 0, len(rootChildren))
	queue := make([]string, 0, len(rootChildren))
	for _, child := range rootChildren {
		if child == nil || strings.TrimSpace(child.Path) == "" {
			continue
		}
		entries = append(entries, child)
		if child.Type == "directory" {
			queue = append(queue, child.ID)
		}
	}

	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]

		children, err := st.ListEntries(ctx, sliceID, parentID)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child == nil || strings.TrimSpace(child.Path) == "" {
				continue
			}
			entries = append(entries, child)
			if child.Type == "directory" {
				queue = append(queue, child.ID)
			}
		}
	}
	return entries, nil
}

func collectSlicePaths(ctx context.Context, st storage.Storage, sliceID string) ([]string, error) {
	entries, err := collectSliceEntries(ctx, st, sliceID)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		paths = append(paths, entry.Path)
	}
	return paths, nil
}

func isDuplicateWrite(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "duplicate key") || strings.Contains(text, "unique constraint")
}
