package gscli

import (
	"os"
	"path/filepath"
	"testing"

	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
)

func TestDecodeFilesystemBatchInputsJSONL(t *testing.T) {
	inputs, err := decodeFilesystemBatchInputs([]byte("{\"op\":\"mkdir\",\"path\":\"/demo/app\"}\n{\"op\":\"delete\",\"path\":\"/demo/app/old.txt\"}\n"))
	if err != nil {
		t.Fatalf("decodeFilesystemBatchInputs failed: %v", err)
	}
	if len(inputs) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(inputs))
	}
	if inputs[0].Op != "mkdir" || inputs[1].Op != "delete" {
		t.Fatalf("unexpected decoded inputs: %#v", inputs)
	}
}

func TestParseFilesystemBatchOperationsReadsLocalWriteSources(t *testing.T) {
	tmpDir := t.TempDir()
	localFile := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(localFile, []byte("hello from batch\n"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}

	data := []byte("[{\"id\":\"one\",\"op\":\"write\",\"path\":\"/demo/README.md\",\"from\":\"README.md\"},{\"id\":\"two\",\"op\":\"mkdir\",\"path\":\"/demo/docs\"}]")
	operations, err := parseFilesystemBatchOperations(data, tmpDir)
	if err != nil {
		t.Fatalf("parseFilesystemBatchOperations failed: %v", err)
	}
	if len(operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(operations))
	}

	writeOp, ok := operations[0].Operation.(*filesystemv1.BatchOperation_Write)
	if !ok {
		t.Fatalf("expected first op to be write, got %#v", operations[0].Operation)
	}
	if got := string(writeOp.Write.GetContent()); got != "hello from batch\n" {
		t.Fatalf("unexpected write content: %q", got)
	}

	mkdirOp, ok := operations[1].Operation.(*filesystemv1.BatchOperation_Mkdir)
	if !ok {
		t.Fatalf("expected second op to be mkdir, got %#v", operations[1].Operation)
	}
	if mkdirOp.Mkdir.GetPath() != "/demo/docs" {
		t.Fatalf("unexpected mkdir path: %q", mkdirOp.Mkdir.GetPath())
	}
}
