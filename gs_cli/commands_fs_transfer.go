package gscli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/niczy/gitslice/internal/storage"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
)

const filesystemTransferChunkSize = 64 * 1024
const filesystemTransferManifestBatchSize = 100
const filesystemTransferBlockUploadBatchSize = 16
const filesystemTransferBlockUploadConcurrency = 8

type filesystemUploadFile struct {
	localPath  string
	remotePath string
	manifest   *filesystemv1.UploadFileManifest
	blocks     []filesystemUploadBlockSource
}

type filesystemUploadBlockSource struct {
	localPath string
	hash      string
	offset    int64
	size      int64
}

type filesystemUploadInventory struct {
	files       []*filesystemUploadFile
	directories []string
}

type filesystemUploadInventoryOptions struct {
	includeIgnored bool
}

type filesystemUploadIgnoreRule struct {
	pattern       string
	directoryOnly bool
	anchored      bool
}

type filesystemUploadIgnoreMatcher struct {
	enabled bool
	rules   []filesystemUploadIgnoreRule
}

func handleFilesystemSync(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	args, detachRequested := consumeBoolFlag(args, "detach")
	fs := newCommandFlagSet("fs sync")
	direction := fs.String("direction", "", "Sync direction: push or pull")
	dryRun := fs.Bool("dry-run", false, "Preview the sync without applying it")
	detach := fs.Bool("detach", false, "Run the sync as a detached local CLI job")
	includeIgnored := fs.Bool("include-ignored", false, "Upload files that match .gsignore or default excludes when pushing")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	detachEnabled := detachRequested || *detach

	if fs.NArg() != 2 {
		commandUsage("Usage: gs fs sync --direction <push|pull> <local-dir> </absolute/path> [--dry-run] [--detach] [--include-ignored] [--json]\n   or: gs fs sync --direction pull </absolute/path> <local-dir> [--dry-run] [--detach] [--json]")
		return
	}
	if detachEnabled {
		record, err := startDetachedCLIJob("fs sync", append([]string{"fs", "sync"}, args...))
		if err != nil {
			commandFatalf("JOB_START_FAILED", false, "", "Failed to start detached fs sync job: %v", err)
		}
		emitDetachedJobStarted(record, jsonEnabled)
		return
	}

	extraArgs := []string{fs.Arg(0), fs.Arg(1)}
	if *dryRun {
		extraArgs = append(extraArgs, "--dry-run")
	}
	if *includeIgnored {
		extraArgs = append(extraArgs, "--include-ignored")
	}
	if jsonEnabled {
		extraArgs = append(extraArgs, "--json")
	}
	switch strings.ToLower(strings.TrimSpace(*direction)) {
	case "push":
		handleFilesystemUpload(ctx, cli, authConfig, extraArgs)
	case "pull":
		handleFilesystemDownload(ctx, cli, authConfig, extraArgs)
	default:
		commandFatal("INVALID_ARGUMENT", "fs sync requires --direction push or --direction pull", false, "")
	}
}

func handleFilesystemUpload(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	args, detachRequested := consumeBoolFlag(args, "detach")
	fs := newCommandFlagSet("fs upload")
	dryRun := fs.Bool("dry-run", false, "Preview the upload without applying it")
	detach := fs.Bool("detach", false, "Run the upload as a detached local CLI job")
	includeIgnored := fs.Bool("include-ignored", false, "Upload files that match .gsignore or default excludes")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	detachEnabled := detachRequested || *detach

	if fs.NArg() != 2 {
		commandUsage("Usage: gs fs upload <local-dir> </absolute/path> [--dry-run] [--detach] [--include-ignored] [--json]")
		return
	}
	if detachEnabled {
		record, err := startDetachedCLIJob("fs upload", append([]string{"fs", "upload"}, args...))
		if err != nil {
			commandFatalf("JOB_START_FAILED", false, "", "Failed to start detached fs upload job: %v", err)
		}
		emitDetachedJobStarted(record, jsonEnabled)
		return
	}

	localRoot := filepath.Clean(strings.TrimSpace(fs.Arg(0)))
	info, err := os.Stat(localRoot)
	if err != nil {
		commandFatalf("FS_UPLOAD_FAILED", false, "", "Failed to stat local path: %v", err)
	}
	if !info.IsDir() {
		commandFatal("INVALID_ARGUMENT", "Local source must be a directory.", false, "")
	}

	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("FS_UPLOAD_FAILED", true, "", "Failed to resolve home workspace: %v", err)
	}
	remoteBase, err := parseAbsoluteFilesystemPathArg(fs.Arg(1), true)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid absolute path: %v", err)
	}

	inventory, err := buildFilesystemUploadInventory(localRoot, remoteBase, filesystemUploadInventoryOptions{
		includeIgnored: *includeIgnored,
	})
	if err != nil {
		commandFatalf("FS_UPLOAD_FAILED", false, "", "Failed to plan upload directory tree: %v", err)
	}
	if *dryRun {
		out := jsonFilesystemTransferOutput{
			Action:         "upload",
			Status:         "would_upload",
			DryRun:         true,
			LocalPath:      localRoot,
			RemotePath:     filesystemDisplayPath(remoteBase),
			FileCount:      len(inventory.files),
			DirectoryCount: len(inventory.directories),
		}
		if jsonEnabled {
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Would upload %d files and %d directories to %s\n", len(inventory.files), len(inventory.directories), filesystemDisplayPath(remoteBase))
		return
	}

	missingBlocks := make(map[string]struct{})
	fileBatches := splitFilesystemUploadFiles(inventory.files, filesystemTransferManifestBatchSize)
	for _, batch := range fileBatches {
		planResp, err := cli.filesystemClient.PlanUpload(ctx, &filesystemv1.PlanUploadRequest{
			WorkspaceId: workspaceID,
			Files:       collectFilesystemUploadManifests(batch),
		})
		if err != nil {
			commandFatalf("FS_UPLOAD_FAILED", true, "", "Failed to plan upload directory tree: %v", err)
		}

		for _, hash := range planResp.GetMissingBlockHashes() {
			hash = strings.TrimSpace(hash)
			if hash != "" {
				missingBlocks[hash] = struct{}{}
			}
		}
	}
	if _, err := uploadFilesystemMissingBlocks(ctx, cli.filesystemClient, workspaceID, inventory, missingBlocks); err != nil {
		commandFatalf("FS_UPLOAD_FAILED", true, "", "Failed to upload missing file blocks: %v", err)
	}
	if len(missingBlocks) != 0 {
		commandFatalf("FS_UPLOAD_FAILED", true, "", "Failed to upload all missing file blocks; %d blocks still missing", len(missingBlocks))
	}

	_, err = cli.filesystemClient.FinalizeUpload(ctx, &filesystemv1.FinalizeUploadRequest{
		WorkspaceId: workspaceID,
		Directories: append([]string(nil), inventory.directories...),
		Files:       collectFilesystemUploadManifests(inventory.files),
	})
	if err != nil {
		commandFatalf("FS_UPLOAD_FAILED", true, "", "Failed to upload directory tree: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonFilesystemTransferOutput{
			Action:         "upload",
			Status:         "uploaded",
			LocalPath:      localRoot,
			RemotePath:     filesystemDisplayPath(remoteBase),
			FileCount:      len(inventory.files),
			DirectoryCount: len(inventory.directories),
		})
		return
	}

	fmt.Printf(
		"Uploaded %d files and %d directories to %s\n",
		len(inventory.files),
		len(inventory.directories),
		filesystemDisplayPath(remoteBase),
	)
}

func handleFilesystemDownload(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	args, detachRequested := consumeBoolFlag(args, "detach")
	fs := newCommandFlagSet("fs download")
	dryRun := fs.Bool("dry-run", false, "Preview the download without applying it")
	detach := fs.Bool("detach", false, "Run the download as a detached local CLI job")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	detachEnabled := detachRequested || *detach

	if fs.NArg() != 2 {
		commandUsage("Usage: gs fs download </absolute/path> <local-dir> [--dry-run] [--detach] [--json]")
		return
	}
	if detachEnabled {
		record, err := startDetachedCLIJob("fs download", append([]string{"fs", "download"}, args...))
		if err != nil {
			commandFatalf("JOB_START_FAILED", false, "", "Failed to start detached fs download job: %v", err)
		}
		emitDetachedJobStarted(record, jsonEnabled)
		return
	}

	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("FS_DOWNLOAD_FAILED", true, "", "Failed to resolve home workspace: %v", err)
	}
	remoteBase, err := parseAbsoluteFilesystemPathArg(fs.Arg(0), true)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid absolute path: %v", err)
	}
	if *dryRun {
		filesPlanned, dirsPlanned, err := describeFilesystemRemoteTree(ctx, cli.filesystemClient, workspaceID, remoteBase)
		if err != nil {
			commandFatalf("FS_DOWNLOAD_FAILED", true, "", "Failed to inspect remote tree: %v", err)
		}
		out := jsonFilesystemTransferOutput{
			Action:         "download",
			Status:         "would_download",
			DryRun:         true,
			LocalPath:      filepath.Clean(strings.TrimSpace(fs.Arg(1))),
			RemotePath:     filesystemDisplayPath(remoteBase),
			FileCount:      filesPlanned,
			DirectoryCount: dirsPlanned,
		}
		if jsonEnabled {
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Would download %d files and %d directories from %s to %s\n", filesPlanned, dirsPlanned, filesystemDisplayPath(remoteBase), out.LocalPath)
		return
	}

	localRoot := filepath.Clean(strings.TrimSpace(fs.Arg(1)))
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		commandFatalf("FS_DOWNLOAD_FAILED", false, "", "Failed to create local directory: %v", err)
	}

	filesDownloaded, dirsDownloaded, err := downloadFilesystemTree(ctx, cli.filesystemClient, workspaceID, remoteBase, localRoot)
	if err != nil {
		commandFatalf("FS_DOWNLOAD_FAILED", true, "", "Failed to download directory tree: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonFilesystemTransferOutput{
			Action:         "download",
			Status:         "downloaded",
			LocalPath:      localRoot,
			RemotePath:     filesystemDisplayPath(remoteBase),
			FileCount:      filesDownloaded,
			DirectoryCount: dirsDownloaded,
		})
		return
	}

	fmt.Printf(
		"Downloaded %d files and %d directories from %s to %s\n",
		filesDownloaded,
		dirsDownloaded,
		filesystemDisplayPath(remoteBase),
		localRoot,
	)
}

func uploadFilesystemTree(
	ctx context.Context,
	client filesystemv1.FilesystemServiceClient,
	workspaceID, remoteBase, localRoot string,
) (filesUploaded, dirsUploaded int, err error) {
	if err := ensureFilesystemRemoteDirectory(ctx, client, workspaceID, remoteBase); err != nil {
		return 0, 0, err
	}

	err = filepath.WalkDir(localRoot, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(localRoot, current)
		if err != nil {
			return err
		}
		if relativePath == "." {
			return nil
		}

		remotePath := path.Join(remoteBase, filepath.ToSlash(relativePath))
		if entry.IsDir() {
			if err := ensureFilesystemRemoteDirectory(ctx, client, workspaceID, remotePath); err != nil {
				return err
			}
			dirsUploaded++
			return nil
		}

		if err := streamWriteFilesystemFile(ctx, client, workspaceID, remotePath, current); err != nil {
			return err
		}
		filesUploaded++
		return nil
	})
	if err != nil {
		return 0, 0, err
	}

	return filesUploaded, dirsUploaded, nil
}

func buildFilesystemUploadInventory(localRoot, remoteBase string, options filesystemUploadInventoryOptions) (*filesystemUploadInventory, error) {
	inventory := &filesystemUploadInventory{
		files:       make([]*filesystemUploadFile, 0),
		directories: make([]string, 0),
	}
	ignoreMatcher, err := newFilesystemUploadIgnoreMatcher(localRoot, options)
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(localRoot, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(localRoot, current)
		if err != nil {
			return err
		}
		if relativePath == "." {
			return nil
		}
		relativeSlash := filepath.ToSlash(relativePath)
		if ignoreMatcher.ignored(relativeSlash, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		remotePath := path.Join(remoteBase, relativeSlash)
		if entry.IsDir() {
			inventory.directories = append(inventory.directories, remotePath)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		manifest, blocks, err := buildFilesystemUploadManifest(remotePath, current)
		if err != nil {
			return err
		}
		inventory.files = append(inventory.files, &filesystemUploadFile{
			localPath:  current,
			remotePath: remotePath,
			manifest:   manifest,
			blocks:     blocks,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(inventory.directories)
	sort.Slice(inventory.files, func(i, j int) bool {
		return inventory.files[i].remotePath < inventory.files[j].remotePath
	})
	return inventory, nil
}

func newFilesystemUploadIgnoreMatcher(localRoot string, options filesystemUploadInventoryOptions) (*filesystemUploadIgnoreMatcher, error) {
	if options.includeIgnored {
		return &filesystemUploadIgnoreMatcher{}, nil
	}

	matcher := &filesystemUploadIgnoreMatcher{
		enabled: true,
		rules:   defaultFilesystemUploadIgnoreRules(),
	}
	ignorePath := filepath.Join(localRoot, ".gsignore")
	data, err := os.ReadFile(ignorePath)
	if err != nil {
		if os.IsNotExist(err) {
			return matcher, nil
		}
		return nil, err
	}
	matcher.rules = append(matcher.rules, parseFilesystemUploadIgnoreRules(string(data))...)
	return matcher, nil
}

func defaultFilesystemUploadIgnoreRules() []filesystemUploadIgnoreRule {
	patterns := []string{
		".git/",
		".hg/",
		".svn/",
		"node_modules/",
		".pnpm-store/",
		".yarn/cache/",
		".next/",
		".nuxt/",
		".svelte-kit/",
		".turbo/",
		".vite/",
		".cache/",
		".parcel-cache/",
		"coverage/",
		"dist/",
		"build/",
		"out/",
		"tmp/",
		"temp/",
		"__pycache__/",
		".pytest_cache/",
		".gradle/",
		".m2/",
		".DS_Store",
	}
	rules := make([]filesystemUploadIgnoreRule, 0, len(patterns))
	for _, pattern := range patterns {
		rules = append(rules, newFilesystemUploadIgnoreRule(pattern))
	}
	return rules
}

func parseFilesystemUploadIgnoreRules(data string) []filesystemUploadIgnoreRule {
	lines := strings.Split(data, "\n")
	rules := make([]filesystemUploadIgnoreRule, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		rule := newFilesystemUploadIgnoreRule(line)
		if rule.pattern != "" {
			rules = append(rules, rule)
		}
	}
	return rules
}

func newFilesystemUploadIgnoreRule(raw string) filesystemUploadIgnoreRule {
	pattern := filepath.ToSlash(strings.TrimSpace(raw))
	rule := filesystemUploadIgnoreRule{}
	if strings.HasPrefix(pattern, "/") {
		rule.anchored = true
		pattern = strings.TrimLeft(pattern, "/")
	}
	if strings.HasSuffix(pattern, "/") {
		rule.directoryOnly = true
		pattern = strings.TrimRight(pattern, "/")
	}
	rule.pattern = strings.TrimSpace(path.Clean(pattern))
	if rule.pattern == "." {
		rule.pattern = ""
	}
	return rule
}

func (m *filesystemUploadIgnoreMatcher) ignored(relativePath string, isDir bool) bool {
	if m == nil || !m.enabled {
		return false
	}
	relativePath = strings.Trim(strings.TrimSpace(filepath.ToSlash(relativePath)), "/")
	if relativePath == "" || relativePath == "." {
		return false
	}
	for _, rule := range m.rules {
		if rule.matches(relativePath, isDir) {
			return true
		}
	}
	return false
}

func (r filesystemUploadIgnoreRule) matches(relativePath string, isDir bool) bool {
	if r.pattern == "" {
		return false
	}
	if r.directoryOnly && !isDir && !pathContainsDirectory(relativePath, r.pattern) {
		return false
	}
	if r.anchored {
		return filesystemUploadPatternMatches(r.pattern, relativePath) || strings.HasPrefix(relativePath, r.pattern+"/")
	}
	if !strings.Contains(r.pattern, "/") {
		for _, segment := range strings.Split(relativePath, "/") {
			if filesystemUploadPatternMatches(r.pattern, segment) {
				return true
			}
		}
		return false
	}
	if filesystemUploadPatternMatches(r.pattern, relativePath) || strings.HasPrefix(relativePath, r.pattern+"/") {
		return true
	}
	suffix := "/" + r.pattern
	return strings.Contains(relativePath, suffix+"/") || strings.HasSuffix(relativePath, suffix)
}

func pathContainsDirectory(relativePath, directoryPattern string) bool {
	segments := strings.Split(relativePath, "/")
	for index := 0; index < len(segments)-1; index++ {
		if filesystemUploadPatternMatches(directoryPattern, segments[index]) {
			return true
		}
	}
	if len(segments) > 0 && filesystemUploadPatternMatches(directoryPattern, segments[0]) {
		return true
	}
	return strings.Contains(relativePath, directoryPattern+"/")
}

func filesystemUploadPatternMatches(pattern, value string) bool {
	if matched, err := path.Match(pattern, value); err == nil && matched {
		return true
	}
	return pattern == value
}

func buildFilesystemUploadManifest(remotePath, localPath string) (*filesystemv1.UploadFileManifest, []filesystemUploadBlockSource, error) {
	file, err := os.Open(filepath.Clean(localPath))
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	hasher := sha256.New()
	manifest := &filesystemv1.UploadFileManifest{
		Path:   remotePath,
		Blocks: make([]*filesystemv1.UploadBlockRef, 0),
	}
	blocks := make([]filesystemUploadBlockSource, 0)
	buffer := make([]byte, storage.DefaultFileBlockSize)
	var offset int64
	for {
		readBytes, err := file.Read(buffer)
		if readBytes > 0 {
			chunk := buffer[:readBytes]
			if _, err := hasher.Write(chunk); err != nil {
				return nil, nil, err
			}
			blockHash := sha256.Sum256(chunk)
			hash := hex.EncodeToString(blockHash[:])
			manifest.Blocks = append(manifest.Blocks, &filesystemv1.UploadBlockRef{
				Hash: hash,
				Size: int64(readBytes),
			})
			blocks = append(blocks, filesystemUploadBlockSource{
				localPath: localPath,
				hash:      hash,
				offset:    offset,
				size:      int64(readBytes),
			})
			manifest.Size += int64(readBytes)
			offset += int64(readBytes)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
	}
	manifest.Hash = hex.EncodeToString(hasher.Sum(nil))
	return manifest, blocks, nil
}

func collectFilesystemUploadManifests(files []*filesystemUploadFile) []*filesystemv1.UploadFileManifest {
	manifests := make([]*filesystemv1.UploadFileManifest, 0, len(files))
	for _, file := range files {
		if file == nil || file.manifest == nil {
			continue
		}
		manifests = append(manifests, file.manifest)
	}
	return manifests
}

func splitFilesystemUploadFiles(files []*filesystemUploadFile, batchSize int) [][]*filesystemUploadFile {
	if len(files) == 0 {
		return nil
	}
	if batchSize <= 0 || batchSize >= len(files) {
		return [][]*filesystemUploadFile{files}
	}

	batches := make([][]*filesystemUploadFile, 0, (len(files)+batchSize-1)/batchSize)
	for start := 0; start < len(files); start += batchSize {
		end := start + batchSize
		if end > len(files) {
			end = len(files)
		}
		batches = append(batches, files[start:end])
	}
	return batches
}

func uploadFilesystemMissingBlocks(
	ctx context.Context,
	client filesystemv1.FilesystemServiceClient,
	workspaceID string,
	inventory *filesystemUploadInventory,
	missing map[string]struct{},
) (*filesystemv1.UploadBlocksResponse, error) {
	if len(missing) == 0 {
		return &filesystemv1.UploadBlocksResponse{WorkspaceId: workspaceID}, nil
	}

	sources := collectFilesystemUploadBlockSources(inventory, missing)
	resp := &filesystemv1.UploadBlocksResponse{WorkspaceId: workspaceID}
	if len(sources) == 0 {
		return resp, nil
	}

	batches := splitFilesystemUploadBlockSources(sources, filesystemTransferBlockUploadBatchSize)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, filesystemTransferBlockUploadConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	sentHashes := make([]string, 0, len(sources))

	for _, batch := range batches {
		batch := batch
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			batchResp, err := uploadFilesystemBlockSourceBatch(ctx, client, workspaceID, batch)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				return
			}
			resp.BlocksReceived += batchResp.GetBlocksReceived()
			resp.BytesReceived += batchResp.GetBytesReceived()
			resp.BlocksWritten += batchResp.GetBlocksWritten()
			resp.BlocksReused += batchResp.GetBlocksReused()
			for _, source := range batch {
				sentHashes = append(sentHashes, source.hash)
			}
		}()
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	for _, hash := range sentHashes {
		delete(missing, hash)
	}
	return resp, nil
}

func collectFilesystemUploadBlockSources(inventory *filesystemUploadInventory, missing map[string]struct{}) []filesystemUploadBlockSource {
	if inventory == nil || len(missing) == 0 {
		return nil
	}
	sources := make([]filesystemUploadBlockSource, 0, len(missing))
	seen := make(map[string]struct{}, len(missing))
	for _, fileSpec := range inventory.files {
		if fileSpec == nil {
			continue
		}
		for _, source := range fileSpec.blocks {
			if _, needed := missing[source.hash]; !needed {
				continue
			}
			if _, ok := seen[source.hash]; ok {
				continue
			}
			seen[source.hash] = struct{}{}
			sources = append(sources, source)
		}
	}
	return sources
}

func splitFilesystemUploadBlockSources(sources []filesystemUploadBlockSource, batchSize int) [][]filesystemUploadBlockSource {
	if len(sources) == 0 {
		return nil
	}
	if batchSize <= 0 || batchSize >= len(sources) {
		return [][]filesystemUploadBlockSource{sources}
	}

	batches := make([][]filesystemUploadBlockSource, 0, (len(sources)+batchSize-1)/batchSize)
	for start := 0; start < len(sources); start += batchSize {
		end := start + batchSize
		if end > len(sources) {
			end = len(sources)
		}
		batches = append(batches, sources[start:end])
	}
	return batches
}

func uploadFilesystemBlockSourceBatch(
	ctx context.Context,
	client filesystemv1.FilesystemServiceClient,
	workspaceID string,
	sources []filesystemUploadBlockSource,
) (*filesystemv1.UploadBlocksResponse, error) {
	stream, err := client.UploadBlocks(ctx)
	if err != nil {
		return nil, err
	}

	for _, source := range sources {
		if source.hash == "" || source.size < 0 || source.localPath == "" {
			continue
		}
		chunk, err := readFilesystemUploadBlockSource(source)
		if err != nil {
			return nil, err
		}
		if err := stream.Send(&filesystemv1.UploadBlocksRequest{
			Chunk: &filesystemv1.UploadBlocksRequest_Metadata{
				Metadata: &filesystemv1.UploadBlockMetadata{
					WorkspaceId: workspaceID,
					Hash:        source.hash,
					Size:        int64(len(chunk)),
				},
			},
		}); err != nil {
			return nil, err
		}
		if err := stream.Send(&filesystemv1.UploadBlocksRequest{
			Chunk: &filesystemv1.UploadBlocksRequest_Content{Content: chunk},
		}); err != nil {
			return nil, err
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func readFilesystemUploadBlockSource(source filesystemUploadBlockSource) ([]byte, error) {
	file, err := os.Open(filepath.Clean(source.localPath))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if source.size == 0 {
		return nil, nil
	}
	if source.size > int64(storage.DefaultFileBlockSize) {
		return nil, fmt.Errorf("upload block %s size exceeds transfer chunk size", source.hash)
	}
	chunk := make([]byte, int(source.size))
	if _, err := file.ReadAt(chunk, source.offset); err != nil {
		return nil, err
	}
	blockHash := sha256.Sum256(chunk)
	if got := hex.EncodeToString(blockHash[:]); got != source.hash {
		return nil, fmt.Errorf("upload block hash changed for %s", source.localPath)
	}
	return chunk, nil
}

func downloadFilesystemTree(
	ctx context.Context,
	client filesystemv1.FilesystemServiceClient,
	workspaceID, remoteBase, localRoot string,
) (filesDownloaded, dirsDownloaded int, err error) {
	statResp, err := client.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: workspaceID,
		Path:        remoteBase,
	})
	if err != nil {
		return 0, 0, err
	}
	if !statResp.GetExists() {
		return 0, 0, fmt.Errorf("remote path not found: %s", filesystemDisplayPath(remoteBase))
	}

	entry := statResp.GetEntry()
	if entry.GetType() == filesystemv1.EntryType_ENTRY_TYPE_FILE {
		targetPath := filepath.Join(localRoot, path.Base(entry.GetPath()))
		if err := streamReadFilesystemFile(ctx, client, workspaceID, entry.GetPath(), targetPath); err != nil {
			return 0, 0, err
		}
		return 1, 0, nil
	}

	filesDownloaded, dirsDownloaded, err = downloadFilesystemDirectory(ctx, client, workspaceID, remoteBase, localRoot)
	if err != nil {
		return 0, 0, err
	}
	return filesDownloaded, dirsDownloaded, nil
}

func downloadFilesystemDirectory(
	ctx context.Context,
	client filesystemv1.FilesystemServiceClient,
	workspaceID, remotePath, localPath string,
) (filesDownloaded, dirsDownloaded int, err error) {
	if err := os.MkdirAll(localPath, 0o755); err != nil {
		return 0, 0, err
	}

	resp, err := client.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: workspaceID,
		Path:        remotePath,
	})
	if err != nil {
		return 0, 0, err
	}

	for _, entry := range resp.GetEntries() {
		targetPath := filepath.Join(localPath, filepath.FromSlash(entry.GetName()))
		switch entry.GetType() {
		case filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY:
			childFiles, childDirs, err := downloadFilesystemDirectory(ctx, client, workspaceID, entry.GetPath(), targetPath)
			if err != nil {
				return 0, 0, err
			}
			dirsDownloaded += 1 + childDirs
			filesDownloaded += childFiles
		default:
			if err := streamReadFilesystemFile(ctx, client, workspaceID, entry.GetPath(), targetPath); err != nil {
				return 0, 0, err
			}
			filesDownloaded++
		}
	}

	return filesDownloaded, dirsDownloaded, nil
}

func ensureFilesystemRemoteDirectory(
	ctx context.Context,
	client filesystemv1.FilesystemServiceClient,
	workspaceID, remotePath string,
) error {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return nil
	}

	statResp, err := client.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: workspaceID,
		Path:        remotePath,
	})
	if err != nil {
		return err
	}
	if statResp.GetExists() {
		if statResp.GetEntry().GetType() != filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY {
			return fmt.Errorf("remote path is not a directory: %s", remotePath)
		}
		return nil
	}

	_, err = client.MakeDirectory(ctx, &filesystemv1.MakeDirectoryRequest{
		WorkspaceId: workspaceID,
		Path:        remotePath,
	})
	return err
}

func describeFilesystemRemoteTree(
	ctx context.Context,
	client filesystemv1.FilesystemServiceClient,
	workspaceID, remoteBase string,
) (files, dirs int, err error) {
	statResp, err := client.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: workspaceID,
		Path:        remoteBase,
	})
	if err != nil {
		return 0, 0, err
	}
	if !statResp.GetExists() {
		return 0, 0, fmt.Errorf("remote path not found: %s", filesystemDisplayPath(remoteBase))
	}
	if statResp.GetEntry().GetType() == filesystemv1.EntryType_ENTRY_TYPE_FILE {
		return 1, 0, nil
	}
	return describeFilesystemRemoteDirectory(ctx, client, workspaceID, remoteBase)
}

func describeFilesystemRemoteDirectory(
	ctx context.Context,
	client filesystemv1.FilesystemServiceClient,
	workspaceID, remotePath string,
) (files, dirs int, err error) {
	resp, err := client.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: workspaceID,
		Path:        remotePath,
	})
	if err != nil {
		return 0, 0, err
	}
	for _, entry := range resp.GetEntries() {
		switch entry.GetType() {
		case filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY:
			childFiles, childDirs, err := describeFilesystemRemoteDirectory(ctx, client, workspaceID, entry.GetPath())
			if err != nil {
				return 0, 0, err
			}
			dirs += 1 + childDirs
			files += childFiles
		default:
			files++
		}
	}
	return files, dirs, nil
}

func streamWriteFilesystemFile(
	ctx context.Context,
	client filesystemv1.FilesystemServiceClient,
	workspaceID, remotePath, localPath string,
) error {
	file, err := os.Open(filepath.Clean(localPath))
	if err != nil {
		return err
	}
	defer file.Close()

	stream, err := client.StreamWrite(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&filesystemv1.StreamWriteRequest{
		Chunk: &filesystemv1.StreamWriteRequest_Metadata{
			Metadata: &filesystemv1.StreamWriteMetadata{
				WorkspaceId: workspaceID,
				Path:        remotePath,
			},
		},
	}); err != nil {
		return err
	}

	buffer := make([]byte, filesystemTransferChunkSize)
	for {
		readBytes, err := file.Read(buffer)
		if readBytes > 0 {
			if err := stream.Send(&filesystemv1.StreamWriteRequest{
				Chunk: &filesystemv1.StreamWriteRequest_Content{
					Content: append([]byte(nil), buffer[:readBytes]...),
				},
			}); err != nil {
				return err
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	_, err = stream.CloseAndRecv()
	return err
}

func streamReadFilesystemFile(
	ctx context.Context,
	client filesystemv1.FilesystemServiceClient,
	workspaceID, remotePath, localPath string,
) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}

	file, err := os.Create(filepath.Clean(localPath))
	if err != nil {
		return err
	}
	defer file.Close()

	stream, err := client.StreamRead(ctx, &filesystemv1.StreamReadRequest{
		WorkspaceId: workspaceID,
		Path:        remotePath,
		ChunkSize:   filesystemTransferChunkSize,
	})
	if err != nil {
		return err
	}

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if len(resp.GetContent()) > 0 {
			if _, err := file.Write(resp.GetContent()); err != nil {
				return err
			}
		}
		if resp.GetEof() {
			return nil
		}
	}
}
