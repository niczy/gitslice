package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"strings"

	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
)

func handleFilesystemShell(ctx context.Context, cli *CLI, args []string) {
	fs := flag.NewFlagSet("fs shell", flag.ExitOnError)
	parseFlagSetInterspersed(fs, args)

	if fs.NArg() != 1 {
		log.Println("Usage: gs fs shell <workspace>")
		return
	}

	workspaceID := strings.TrimSpace(fs.Arg(0))
	if workspaceID == "" {
		log.Println("Workspace name is required")
		return
	}

	shell := &filesystemShell{
		workspaceID: workspaceID,
		client:      cli.filesystemClient,
		interactive: stdinIsTerminal(os.Stdin),
	}
	if err := shell.run(ctx, os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

type filesystemShell struct {
	workspaceID string
	client      filesystemv1.FilesystemServiceClient
	cwd         string
	interactive bool
}

func (s *filesystemShell) run(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		if s.interactive {
			if _, err := fmt.Fprint(output, s.prompt()); err != nil {
				return err
			}
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			return nil
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		shouldExit, err := s.executeLine(ctx, line, output)
		if err != nil {
			if s.interactive {
				if _, writeErr := fmt.Fprintf(output, "error: %v\n", err); writeErr != nil {
					return writeErr
				}
				continue
			}
			return err
		}
		if shouldExit {
			return nil
		}
	}
}

func (s *filesystemShell) executeLine(ctx context.Context, line string, output io.Writer) (bool, error) {
	if content, filePath, ok, err := parseFilesystemShellEchoRedirect(line); ok || err != nil {
		if err != nil {
			return false, err
		}
		resolvedPath, err := resolveFilesystemShellPath(s.cwd, filePath)
		if err != nil {
			return false, err
		}
		_, err = s.client.WriteFile(ctx, &filesystemv1.WriteFileRequest{
			WorkspaceId: s.workspaceID,
			Path:        resolvedPath,
			Content:     []byte(content + "\n"),
		})
		return false, err
	}

	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false, nil
	}

	switch fields[0] {
	case "exit", "quit":
		return true, nil
	case "help":
		return false, s.printHelp(output)
	case "pwd":
		_, err := fmt.Fprintln(output, shellDisplayPath(s.cwd))
		return false, err
	case "ls":
		targetPath := s.cwd
		if len(fields) > 2 {
			return false, fmt.Errorf("usage: ls [path]")
		}
		if len(fields) == 2 {
			var err error
			targetPath, err = resolveFilesystemShellPath(s.cwd, fields[1])
			if err != nil {
				return false, err
			}
		}
		resp, err := s.client.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
			WorkspaceId: s.workspaceID,
			Path:        targetPath,
		})
		if err != nil {
			return false, err
		}
		return false, printFilesystemShellEntries(output, resp.GetEntries())
	case "cd":
		if len(fields) != 2 {
			return false, fmt.Errorf("usage: cd <path>")
		}
		targetPath, err := resolveFilesystemShellPath(s.cwd, fields[1])
		if err != nil {
			return false, err
		}
		if targetPath == "" {
			s.cwd = ""
			return false, nil
		}
		statResp, err := s.client.Stat(ctx, &filesystemv1.StatRequest{
			WorkspaceId: s.workspaceID,
			Path:        targetPath,
		})
		if err != nil {
			return false, err
		}
		if !statResp.GetExists() {
			return false, fmt.Errorf("path not found: %s", targetPath)
		}
		if statResp.GetEntry().GetType() != filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY {
			return false, fmt.Errorf("not a directory: %s", targetPath)
		}
		s.cwd = targetPath
		return false, nil
	case "cat":
		if len(fields) != 2 {
			return false, fmt.Errorf("usage: cat <path>")
		}
		filePath, err := resolveFilesystemShellPath(s.cwd, fields[1])
		if err != nil {
			return false, err
		}
		resp, err := s.client.ReadFile(ctx, &filesystemv1.ReadFileRequest{
			WorkspaceId: s.workspaceID,
			Path:        filePath,
		})
		if err != nil {
			return false, err
		}
		if _, err := output.Write(resp.GetContent()); err != nil {
			return false, err
		}
		if len(resp.GetContent()) == 0 || resp.GetContent()[len(resp.GetContent())-1] != '\n' {
			_, err = fmt.Fprintln(output)
			return false, err
		}
		return false, nil
	case "mkdir":
		if len(fields) != 2 {
			return false, fmt.Errorf("usage: mkdir <path>")
		}
		dirPath, err := resolveFilesystemShellPath(s.cwd, fields[1])
		if err != nil {
			return false, err
		}
		_, err = s.client.MakeDirectory(ctx, &filesystemv1.MakeDirectoryRequest{
			WorkspaceId: s.workspaceID,
			Path:        dirPath,
		})
		return false, err
	case "rm":
		if len(fields) != 2 {
			return false, fmt.Errorf("usage: rm <path>")
		}
		filePath, err := resolveFilesystemShellPath(s.cwd, fields[1])
		if err != nil {
			return false, err
		}
		_, err = s.client.DeleteFile(ctx, &filesystemv1.DeleteFileRequest{
			WorkspaceId: s.workspaceID,
			Path:        filePath,
		})
		return false, err
	case "mv":
		if len(fields) != 3 {
			return false, fmt.Errorf("usage: mv <source> <destination>")
		}
		sourcePath, err := resolveFilesystemShellPath(s.cwd, fields[1])
		if err != nil {
			return false, err
		}
		destinationPath, err := resolveFilesystemShellPath(s.cwd, fields[2])
		if err != nil {
			return false, err
		}
		_, err = s.client.MoveFile(ctx, &filesystemv1.MoveFileRequest{
			WorkspaceId:     s.workspaceID,
			SourcePath:      sourcePath,
			DestinationPath: destinationPath,
		})
		return false, err
	case "cp":
		if len(fields) != 3 {
			return false, fmt.Errorf("usage: cp <source> <destination>")
		}
		sourcePath, err := resolveFilesystemShellPath(s.cwd, fields[1])
		if err != nil {
			return false, err
		}
		destinationPath, err := resolveFilesystemShellPath(s.cwd, fields[2])
		if err != nil {
			return false, err
		}
		_, err = s.client.CopyFile(ctx, &filesystemv1.CopyFileRequest{
			WorkspaceId:     s.workspaceID,
			SourcePath:      sourcePath,
			DestinationPath: destinationPath,
		})
		return false, err
	case "snapshot":
		message := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		message = trimFilesystemShellQuotes(message)
		if message == "" {
			return false, fmt.Errorf("usage: snapshot <message>")
		}
		resp, err := s.client.Snapshot(ctx, &filesystemv1.SnapshotRequest{
			WorkspaceId: s.workspaceID,
			Message:     message,
		})
		if err != nil {
			return false, err
		}
		_, err = fmt.Fprintf(output, "Snapshot created: %s\n", resp.GetSnapshot().GetSnapshotId())
		return false, err
	case "history", "snapshots":
		resp, err := s.client.ListSnapshots(ctx, &filesystemv1.ListSnapshotsRequest{
			WorkspaceId: s.workspaceID,
			Limit:       20,
		})
		if err != nil {
			return false, err
		}
		for _, snapshot := range resp.GetSnapshots() {
			if _, err := fmt.Fprintf(output, "%s  %s  %q\n", snapshot.GetSnapshotId(), formatTimestamp(snapshot.GetCreatedAt()), snapshot.GetMessage()); err != nil {
				return false, err
			}
		}
		return false, nil
	case "restore":
		if len(fields) != 2 {
			return false, fmt.Errorf("usage: restore <snapshot-id>")
		}
		resp, err := s.client.RestoreSnapshot(ctx, &filesystemv1.RestoreSnapshotRequest{
			WorkspaceId: s.workspaceID,
			SnapshotId:  strings.TrimSpace(fields[1]),
		})
		if err != nil {
			return false, err
		}
		_, err = fmt.Fprintf(output, "Restored workspace %s to %s\n", resp.GetWorkspaceId(), resp.GetRestoredSnapshotId())
		return false, err
	case "diff":
		if len(fields) > 2 {
			return false, fmt.Errorf("usage: diff [snapshot-id]")
		}
		fromSnapshotID := ""
		if len(fields) == 2 {
			fromSnapshotID = strings.TrimSpace(fields[1])
		} else {
			listResp, err := s.client.ListSnapshots(ctx, &filesystemv1.ListSnapshotsRequest{
				WorkspaceId: s.workspaceID,
				Limit:       1,
			})
			if err != nil {
				return false, err
			}
			if len(listResp.GetSnapshots()) == 0 {
				return false, fmt.Errorf("no snapshots found; create one first")
			}
			fromSnapshotID = listResp.GetSnapshots()[0].GetSnapshotId()
		}
		resp, err := s.client.Diff(ctx, &filesystemv1.DiffRequest{
			WorkspaceId:    s.workspaceID,
			FromSnapshotId: fromSnapshotID,
			IncludePatches: true,
		})
		if err != nil {
			return false, err
		}
		if _, err := fmt.Fprintf(output, "Diff %s -> %s\n", resp.GetFromSnapshotId(), blankOrCurrent(resp.GetToSnapshotId())); err != nil {
			return false, err
		}
		for _, file := range resp.GetFiles() {
			if _, err := fmt.Fprintf(output, "%s %s (+%d -%d)\n", filesystemDiffTypeLabel(file.GetChangeType()), file.GetPath(), file.GetLinesAdded(), file.GetLinesDeleted()); err != nil {
				return false, err
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("unknown shell command: %s", fields[0])
	}
}

func (s *filesystemShell) prompt() string {
	return fmt.Sprintf("gitslice:%s:%s> ", s.workspaceID, shellDisplayPath(s.cwd))
}

func (s *filesystemShell) printHelp(output io.Writer) error {
	_, err := fmt.Fprintln(output, "Commands: ls, cd, pwd, cat, mkdir, rm, mv, cp, snapshot, history, restore, diff, help, exit")
	return err
}

func stdinIsTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func parseFilesystemShellEchoRedirect(line string) (content, filePath string, ok bool, err error) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "echo ") {
		return "", "", false, nil
	}
	separator := strings.LastIndex(trimmed, " > ")
	if separator < 0 {
		return "", "", false, nil
	}
	content = strings.TrimSpace(trimmed[len("echo "):separator])
	filePath = strings.TrimSpace(trimmed[separator+3:])
	if filePath == "" {
		return "", "", true, fmt.Errorf("echo redirect target is required")
	}
	return trimFilesystemShellQuotes(content), filePath, true, nil
}

func trimFilesystemShellQuotes(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func resolveFilesystemShellPath(cwd, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if cwd != "" {
			return cwd, nil
		}
		return "", fmt.Errorf("path is required")
	}

	cleaned := raw
	if strings.HasPrefix(cleaned, "/") {
		cleaned = path.Clean(cleaned)
	} else {
		cleaned = path.Clean(path.Join("/", cwd, cleaned))
	}

	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		return "", nil
	}
	return cleaned, nil
}

func shellDisplayPath(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return "/"
	}
	return "/" + strings.TrimPrefix(cwd, "/")
}

func printFilesystemShellEntries(output io.Writer, entries []*filesystemv1.WorkspaceEntry) error {
	rendered := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.GetName())
		if name == "" {
			name = path.Base(entry.GetPath())
		}
		if entry.GetType() == filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY {
			name += "/"
		}
		rendered = append(rendered, name)
	}
	_, err := fmt.Fprintln(output, strings.Join(rendered, "  "))
	return err
}
