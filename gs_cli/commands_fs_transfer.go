package main

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

	"github.com/niczy/gitslice/internal/storage"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
)

const filesystemTransferChunkSize = 64 * 1024

type filesystemUploadFile struct {
	localPath  string
	remotePath string
	manifest   *filesystemv1.UploadFileManifest
}

type filesystemUploadInventory struct {
	files       []*filesystemUploadFile
	directories []string
}

func handleFilesystemSync(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs sync")
	direction := fs.String("direction", "", "Sync direction: push or pull")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 2 {
		commandUsage("Usage: gs fs sync --direction <push|pull> <local-dir> </absolute/path> [--json]\n   or: gs fs sync --direction pull </absolute/path> <local-dir> [--json]")
		return
	}

	extraArgs := []string{fs.Arg(0), fs.Arg(1)}
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
	fs := newCommandFlagSet("fs upload")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 2 {
		commandUsage("Usage: gs fs upload <local-dir> </absolute/path> [--json]")
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

	inventory, err := buildFilesystemUploadInventory(localRoot, remoteBase)
	if err != nil {
		commandFatalf("FS_UPLOAD_FAILED", false, "", "Failed to plan upload directory tree: %v", err)
	}

	planResp, err := cli.filesystemClient.PlanUpload(ctx, &filesystemv1.PlanUploadRequest{
		WorkspaceId: workspaceID,
		Files:       collectFilesystemUploadManifests(inventory.files),
	})
	if err != nil {
		commandFatalf("FS_UPLOAD_FAILED", true, "", "Failed to plan upload directory tree: %v", err)
	}

	missingBlocks := make(map[string]struct{}, len(planResp.GetMissingBlockHashes()))
	for _, hash := range planResp.GetMissingBlockHashes() {
		hash = strings.TrimSpace(hash)
		if hash != "" {
			missingBlocks[hash] = struct{}{}
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
	fs := newCommandFlagSet("fs download")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 2 {
		commandUsage("Usage: gs fs download </absolute/path> <local-dir> [--json]")
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

func buildFilesystemUploadInventory(localRoot, remoteBase string) (*filesystemUploadInventory, error) {
	inventory := &filesystemUploadInventory{
		files:       make([]*filesystemUploadFile, 0),
		directories: make([]string, 0),
	}
	err := filepath.WalkDir(localRoot, func(current string, entry fs.DirEntry, walkErr error) error {
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
			inventory.directories = append(inventory.directories, remotePath)
			return nil
		}

		manifest, err := buildFilesystemUploadManifest(remotePath, current)
		if err != nil {
			return err
		}
		inventory.files = append(inventory.files, &filesystemUploadFile{
			localPath:  current,
			remotePath: remotePath,
			manifest:   manifest,
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

func buildFilesystemUploadManifest(remotePath, localPath string) (*filesystemv1.UploadFileManifest, error) {
	file, err := os.Open(filepath.Clean(localPath))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	hasher := sha256.New()
	manifest := &filesystemv1.UploadFileManifest{
		Path:   remotePath,
		Blocks: make([]*filesystemv1.UploadBlockRef, 0),
	}
	buffer := make([]byte, storage.DefaultFileBlockSize)
	for {
		readBytes, err := file.Read(buffer)
		if readBytes > 0 {
			chunk := buffer[:readBytes]
			if _, err := hasher.Write(chunk); err != nil {
				return nil, err
			}
			blockHash := sha256.Sum256(chunk)
			manifest.Blocks = append(manifest.Blocks, &filesystemv1.UploadBlockRef{
				Hash: hex.EncodeToString(blockHash[:]),
				Size: int64(readBytes),
			})
			manifest.Size += int64(readBytes)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	manifest.Hash = hex.EncodeToString(hasher.Sum(nil))
	return manifest, nil
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
	stream, err := client.UploadBlocks(ctx)
	if err != nil {
		return nil, err
	}

	sent := make(map[string]struct{}, len(missing))
	buffer := make([]byte, storage.DefaultFileBlockSize)
	for _, fileSpec := range inventory.files {
		if fileSpec == nil {
			continue
		}
		if len(missing) == 0 {
			break
		}
		file, err := os.Open(filepath.Clean(fileSpec.localPath))
		if err != nil {
			return nil, err
		}
		for {
			readBytes, err := file.Read(buffer)
			if readBytes > 0 {
				chunk := append([]byte(nil), buffer[:readBytes]...)
				blockHash := sha256.Sum256(chunk)
				hash := hex.EncodeToString(blockHash[:])
				if _, needed := missing[hash]; needed {
					if _, alreadySent := sent[hash]; !alreadySent {
						if err := stream.Send(&filesystemv1.UploadBlocksRequest{
							Chunk: &filesystemv1.UploadBlocksRequest_Metadata{
								Metadata: &filesystemv1.UploadBlockMetadata{
									WorkspaceId: workspaceID,
									Hash:        hash,
									Size:        int64(len(chunk)),
								},
							},
						}); err != nil {
							_ = file.Close()
							return nil, err
						}
						if err := stream.Send(&filesystemv1.UploadBlocksRequest{
							Chunk: &filesystemv1.UploadBlocksRequest_Content{Content: chunk},
						}); err != nil {
							_ = file.Close()
							return nil, err
						}
						sent[hash] = struct{}{}
						delete(missing, hash)
					}
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				_ = file.Close()
				return nil, err
			}
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
	}

	return stream.CloseAndRecv()
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
