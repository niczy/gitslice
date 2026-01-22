package workflow

import (
	"strings"
	"testing"
)

func TestCheckoutSliceReturnsManifest(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "checkout-slice"

	createSliceFromRoot(t, sliceID, "")
	metadataPath := writeSliceMetadataFile(t, t.TempDir(), sliceID)

	output := runCLIOrFail(t, workdir, "slice", "checkout", metadataPath)
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}
}

func TestCheckoutSliceNotFound(t *testing.T) {
	workdir := t.TempDir()
	metadataPath := writeSliceMetadataFile(t, t.TempDir(), "nonexistent-slice")

	_, err := runCLIWithDir(workdir, "slice", "checkout", metadataPath)
	if err == nil {
		t.Fatalf("expected checkout to fail for missing slice")
	}
}

func TestCheckoutSliceWithCommitHash(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "checkout-commit"

	createSliceFromRoot(t, sliceID, "")
	metadataPath := writeSliceMetadataFile(t, t.TempDir(), sliceID)

	output := runCLIOrFail(t, workdir, "slice", "checkout", metadataPath, "--commit", "HEAD")
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}
}

func TestCheckoutEmptySlice(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "empty-checkout"

	createSliceFromRoot(t, sliceID, "")
	metadataPath := writeSliceMetadataFile(t, t.TempDir(), sliceID)

	output := runCLIOrFail(t, workdir, "slice", "checkout", metadataPath)
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}
}

func TestStreamCheckoutSlice(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "stream-checkout"

	createSliceFromRoot(t, sliceID, "")
	metadataPath := writeSliceMetadataFile(t, t.TempDir(), sliceID)

	output := runCLIOrFail(t, workdir, "slice", "checkout", metadataPath)
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}
}
