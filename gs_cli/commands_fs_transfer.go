package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
)

const filesystemTransferChunkSize = 64 * 1024

func handleFilesystemUpload(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	fs := flag.NewFlagSet("fs upload", flag.ExitOnError)
	parseFlagSetInterspersed(fs, args)

	if fs.NArg() != 2 {
		log.Println("Usage: gs fs upload <local-dir> </absolute/path>")
		return
	}

	localRoot := filepath.Clean(strings.TrimSpace(fs.Arg(0)))
	info, err := os.Stat(localRoot)
	if err != nil {
		log.Fatalf("Failed to stat local path: %v", err)
	}
	if !info.IsDir() {
		log.Fatal("local source must be a directory")
	}

	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		log.Fatal(err)
	}
	remoteBase, err := parseAbsoluteFilesystemPathArg(fs.Arg(1), true)
	if err != nil {
		log.Fatal(err)
	}

	filesUploaded, dirsUploaded, err := uploadFilesystemTree(ctx, cli.filesystemClient, workspaceID, remoteBase, localRoot)
	if err != nil {
		log.Fatalf("Failed to upload directory tree: %v", err)
	}

	fmt.Printf(
		"Uploaded %d files and %d directories to %s\n",
		filesUploaded,
		dirsUploaded,
		filesystemDisplayPath(remoteBase),
	)
}

func handleFilesystemDownload(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	fs := flag.NewFlagSet("fs download", flag.ExitOnError)
	parseFlagSetInterspersed(fs, args)

	if fs.NArg() != 2 {
		log.Println("Usage: gs fs download </absolute/path> <local-dir>")
		return
	}

	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		log.Fatal(err)
	}
	remoteBase, err := parseAbsoluteFilesystemPathArg(fs.Arg(0), true)
	if err != nil {
		log.Fatal(err)
	}

	localRoot := filepath.Clean(strings.TrimSpace(fs.Arg(1)))
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		log.Fatalf("Failed to create local directory: %v", err)
	}

	filesDownloaded, dirsDownloaded, err := downloadFilesystemTree(ctx, cli.filesystemClient, workspaceID, remoteBase, localRoot)
	if err != nil {
		log.Fatalf("Failed to download directory tree: %v", err)
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
