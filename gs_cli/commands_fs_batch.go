package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
)

type filesystemBatchInput struct {
	ID              string                     `json:"id"`
	Op              string                     `json:"op"`
	Path            string                     `json:"path"`
	SourcePath      string                     `json:"source_path"`
	Source          string                     `json:"source"`
	DestinationPath string                     `json:"destination_path"`
	Destination     string                     `json:"destination"`
	From            string                     `json:"from"`
	Content         string                     `json:"content"`
	ExpectedHash    string                     `json:"expected_hash"`
	Edits           []filesystemBatchEditInput `json:"edits"`
}

type filesystemBatchEditInput struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

func handleFilesystemBatch(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	fs := flag.NewFlagSet("fs batch", flag.ExitOnError)
	fileFlag := fs.String("f", "", "Read batch operations from a JSON or JSONL file")
	message := fs.String("m", "", "Commit message for the batch")
	parseFlagSetInterspersed(fs, args)

	if fs.NArg() != 0 {
		log.Println("Usage: gs fs batch [-f <ops.jsonl>] [-m <message>]")
		return
	}

	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		log.Fatal(err)
	}

	data, baseDir, err := readFilesystemBatchInput(strings.TrimSpace(*fileFlag))
	if err != nil {
		log.Fatal(err)
	}
	operations, err := parseFilesystemBatchOperations(data, baseDir)
	if err != nil {
		log.Fatal(err)
	}

	resp, err := cli.filesystemClient.Batch(ctx, &filesystemv1.BatchRequest{
		WorkspaceId: workspaceID,
		Operations:  operations,
		Message:     strings.TrimSpace(*message),
	})
	if err != nil {
		log.Fatalf("Failed to execute filesystem batch: %v", err)
	}

	fmt.Printf("Batch commit: %s\n", resp.GetCommitHash())
	fmt.Printf("Operations: %d\n", len(resp.GetResults()))
	for _, result := range resp.GetResults() {
		if result == nil {
			continue
		}
		fmt.Println(formatFilesystemBatchResult(result))
	}
}

func readFilesystemBatchInput(filePath string) ([]byte, string, error) {
	if filePath != "" {
		data, err := os.ReadFile(filepath.Clean(filePath))
		if err != nil {
			return nil, "", err
		}
		return data, filepath.Dir(filepath.Clean(filePath)), nil
	}
	if stdinIsTerminal(os.Stdin) {
		return nil, "", errors.New("batch input is required via -f or stdin")
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, "", err
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	return data, wd, nil
}

func parseFilesystemBatchOperations(data []byte, baseDir string) ([]*filesystemv1.BatchOperation, error) {
	inputs, err := decodeFilesystemBatchInputs(data)
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, errors.New("batch input is empty")
	}

	operations := make([]*filesystemv1.BatchOperation, 0, len(inputs))
	for index, input := range inputs {
		operation, err := filesystemBatchOperationFromInput(input, baseDir)
		if err != nil {
			return nil, fmt.Errorf("operation %d: %w", index+1, err)
		}
		operations = append(operations, operation)
	}
	return operations, nil
}

func decodeFilesystemBatchInputs(data []byte) ([]filesystemBatchInput, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}

	if trimmed[0] == '[' {
		var inputs []filesystemBatchInput
		if err := json.Unmarshal(trimmed, &inputs); err != nil {
			return nil, fmt.Errorf("decode batch JSON array: %w", err)
		}
		return inputs, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	inputs := make([]filesystemBatchInput, 0)
	for {
		var input filesystemBatchInput
		if err := decoder.Decode(&input); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode batch JSON object: %w", err)
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

func filesystemBatchOperationFromInput(input filesystemBatchInput, baseDir string) (*filesystemv1.BatchOperation, error) {
	opName := strings.ToLower(strings.TrimSpace(input.Op))
	if opName == "" {
		return nil, errors.New(`"op" is required`)
	}

	operation := &filesystemv1.BatchOperation{Id: strings.TrimSpace(input.ID)}
	switch opName {
	case "write":
		filePath, err := parseAbsoluteFilesystemPathArg(input.Path, true)
		if err != nil {
			return nil, fmt.Errorf("write path: %w", err)
		}
		content, err := filesystemBatchWriteContent(input, baseDir)
		if err != nil {
			return nil, err
		}
		operation.Operation = &filesystemv1.BatchOperation_Write{
			Write: &filesystemv1.BatchWriteOperation{
				Path:    filePath,
				Content: content,
			},
		}
	case "edit":
		filePath, err := parseAbsoluteFilesystemPathArg(input.Path, true)
		if err != nil {
			return nil, fmt.Errorf("edit path: %w", err)
		}
		if len(input.Edits) == 0 {
			return nil, errors.New("edit operation requires at least one edit")
		}
		edits := make([]*filesystemv1.FileEdit, 0, len(input.Edits))
		for _, edit := range input.Edits {
			edits = append(edits, &filesystemv1.FileEdit{
				OldText: edit.OldText,
				NewText: edit.NewText,
			})
		}
		operation.Operation = &filesystemv1.BatchOperation_Edit{
			Edit: &filesystemv1.BatchEditOperation{
				Path:         filePath,
				Edits:        edits,
				ExpectedHash: strings.TrimSpace(input.ExpectedHash),
			},
		}
	case "rm", "delete":
		filePath, err := parseAbsoluteFilesystemPathArg(input.Path, true)
		if err != nil {
			return nil, fmt.Errorf("delete path: %w", err)
		}
		operation.Operation = &filesystemv1.BatchOperation_Delete{
			Delete: &filesystemv1.BatchDeleteOperation{Path: filePath},
		}
	case "mv", "move":
		sourcePath, err := parseAbsoluteFilesystemPathArg(firstNonEmpty(input.SourcePath, input.Source), true)
		if err != nil {
			return nil, fmt.Errorf("move source_path: %w", err)
		}
		destinationPath, err := parseAbsoluteFilesystemPathArg(firstNonEmpty(input.DestinationPath, input.Destination), true)
		if err != nil {
			return nil, fmt.Errorf("move destination_path: %w", err)
		}
		operation.Operation = &filesystemv1.BatchOperation_Move{
			Move: &filesystemv1.BatchMoveOperation{
				SourcePath:      sourcePath,
				DestinationPath: destinationPath,
			},
		}
	case "cp", "copy":
		sourcePath, err := parseAbsoluteFilesystemPathArg(firstNonEmpty(input.SourcePath, input.Source), true)
		if err != nil {
			return nil, fmt.Errorf("copy source_path: %w", err)
		}
		destinationPath, err := parseAbsoluteFilesystemPathArg(firstNonEmpty(input.DestinationPath, input.Destination), true)
		if err != nil {
			return nil, fmt.Errorf("copy destination_path: %w", err)
		}
		operation.Operation = &filesystemv1.BatchOperation_Copy{
			Copy: &filesystemv1.BatchCopyOperation{
				SourcePath:      sourcePath,
				DestinationPath: destinationPath,
			},
		}
	case "mkdir":
		dirPath, err := parseAbsoluteFilesystemPathArg(input.Path, true)
		if err != nil {
			return nil, fmt.Errorf("mkdir path: %w", err)
		}
		operation.Operation = &filesystemv1.BatchOperation_Mkdir{
			Mkdir: &filesystemv1.BatchMkdirOperation{Path: dirPath},
		}
	default:
		return nil, fmt.Errorf("unsupported op %q", input.Op)
	}
	return operation, nil
}

func filesystemBatchWriteContent(input filesystemBatchInput, baseDir string) ([]byte, error) {
	fromPath := strings.TrimSpace(input.From)
	if fromPath == "" {
		return []byte(input.Content), nil
	}
	if !filepath.IsAbs(fromPath) {
		fromPath = filepath.Join(baseDir, fromPath)
	}
	content, err := os.ReadFile(filepath.Clean(fromPath))
	if err != nil {
		return nil, fmt.Errorf("read write source %q: %w", input.From, err)
	}
	return content, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func formatFilesystemBatchResult(result *filesystemv1.BatchResult) string {
	if result == nil {
		return ""
	}

	switch result.GetOpType() {
	case filesystemv1.BatchOperationType_BATCH_OPERATION_TYPE_WRITE:
		return fmt.Sprintf("- WRITE %s (%d bytes)", result.GetPath(), result.GetSize())
	case filesystemv1.BatchOperationType_BATCH_OPERATION_TYPE_EDIT:
		return fmt.Sprintf("- EDIT %s (%d bytes)", result.GetPath(), result.GetSize())
	case filesystemv1.BatchOperationType_BATCH_OPERATION_TYPE_DELETE:
		return fmt.Sprintf("- DELETE %s", result.GetPath())
	case filesystemv1.BatchOperationType_BATCH_OPERATION_TYPE_MOVE:
		return fmt.Sprintf("- MOVE %s -> %s", result.GetSourcePath(), result.GetDestinationPath())
	case filesystemv1.BatchOperationType_BATCH_OPERATION_TYPE_COPY:
		return fmt.Sprintf("- COPY %s -> %s (%d bytes)", result.GetSourcePath(), result.GetDestinationPath(), result.GetSize())
	case filesystemv1.BatchOperationType_BATCH_OPERATION_TYPE_MKDIR:
		return fmt.Sprintf("- MKDIR %s", result.GetPath())
	default:
		return "- UNKNOWN"
	}
}
