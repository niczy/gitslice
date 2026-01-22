package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSliceCheckoutFromMetadata(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "slice-metadata-checkout"

	createSliceFromRoot(t, sliceID, "")
	metadataPath := writeSliceMetadataFile(t, t.TempDir(), sliceID)

	output := runCLIOrFail(t, workdir, "slice", "checkout", metadataPath)
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}
}

func TestSliceInitStoresMetadataPath(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "test-init-slice"

	createSliceFromRoot(t, sliceID, "")
	metadataPath := writeSliceMetadataFile(t, t.TempDir(), sliceID)

	output := runCLIOrFail(t, workdir, "init", metadataPath)
	if !strings.Contains(output, "Initialized empty gitslice repository") {
		t.Fatalf("expected init output, got: %s", output)
	}

	gsDir := filepath.Join(workdir, ".gs")
	if _, err := os.Stat(gsDir); os.IsNotExist(err) {
		t.Error("expected .gs directory to be created")
	}

	configFile := filepath.Join(gsDir, "config")
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		t.Error("expected .gs/config file to be created")
	}

	content, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}

	if strings.TrimSpace(string(content)) != metadataPath {
		t.Errorf("expected config to contain metadata path '%s', got: %s", metadataPath, string(content))
	}
}

func TestSliceInitWithPath(t *testing.T) {
	metadataPath := writeSliceMetadataFile(t, t.TempDir(), "init-unsupported")
	assertUnsupportedCommand(t, "init", metadataPath, "--path", "./work/my-team")
}

func TestSliceInitForce(t *testing.T) {
	metadataPath := writeSliceMetadataFile(t, t.TempDir(), "init-force")
	assertUnsupportedCommand(t, "init", metadataPath, "--force")
}

func TestSliceInitDescription(t *testing.T) {
	metadataPath := writeSliceMetadataFile(t, t.TempDir(), "init-description")
	assertUnsupportedCommand(t, "init", metadataPath, "--description", "My team's services")
}
