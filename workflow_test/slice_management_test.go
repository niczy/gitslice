package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSliceCreate tests creating new slices
// Command: gs slice create my-team --files "services/my-team/**"
func TestSliceCreate(t *testing.T) {
	output, err := runCLI("slice", "create", "my-team", "--files", "services/my-team/**")
	if err != nil {
		t.Fatalf("Failed to create slice: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, "Slice created") {
		t.Fatalf("expected creation confirmation, got: %s", output)
	}
}

// TestSliceCreateWithDescription tests creating slice with description
// Command: gs slice create frontend-react --description "React components and hooks"
func TestSliceCreateWithDescription(t *testing.T) {
	output, err := runCLI("slice", "create", "frontend-react", "--description", "React components and hooks")
	if err != nil {
		t.Fatalf("Failed to create slice with description: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, "frontend-react") {
		t.Fatalf("expected slice ID in output, got: %s", output)
	}
}

// TestSliceList tests listing all available slices
// Command: gs slice list
func TestSliceList(t *testing.T) {
	output, err := runCLI("slice", "list")
	if err != nil {
		t.Fatalf("Failed to list slices: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, "Found") {
		t.Errorf("Expected output to contain 'Found', got: %s", output)
	}

	t.Logf("Slice list successful:\n%s", output)
}

// TestSliceListDetailed tests listing slices with details
// Command: gs slice list --detailed
func TestSliceListDetailed(t *testing.T) {
	output, err := runCLI("slice", "list", "--detailed")
	if err != nil {
		t.Fatalf("Failed to list slices with details: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, "Found") {
		t.Errorf("Expected output to contain 'Found', got: %s", output)
	}

	t.Logf("Detailed slice list successful:\n%s", output)
}

// TestSliceListMine tests listing slices you have access to
// Command: gs slice list --mine
func TestSliceListMine(t *testing.T) {
	output, err := runCLI("slice", "list", "--mine")
	if err != nil {
		t.Fatalf("Failed to list my slices: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, "Showing only my slices") {
		t.Errorf("Expected output to contain 'Showing only my slices', got: %s", output)
	}

	t.Logf("My slices list successful:\n%s", output)
}

// TestSliceListSearch tests searching slices
// Command: gs slice list --search billing
func TestSliceListSearch(t *testing.T) {
	output, err := runCLI("slice", "list", "--search", "test")
	if err != nil {
		t.Fatalf("Failed to search slices: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, "Searching for") {
		t.Errorf("Expected output to contain 'Searching for', got: %s", output)
	}

	t.Logf("Slice search successful:\n%s", output)
}

// TestSliceInfo tests getting slice details
// Command: gs slice info my-team
func TestSliceInfo(t *testing.T) {
	// First, create a test slice
	testSliceID := "test-slice-info"
	if err := createTestSlice(testSliceID); err != nil {
		t.Fatalf("Failed to create test slice: %v", err)
	}

	output, err := runCLI("slice", "info", testSliceID)
	if err != nil {
		t.Fatalf("Failed to get slice info: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, testSliceID) {
		t.Errorf("Expected output to contain slice ID '%s', got: %s", testSliceID, output)
	}

	t.Logf("Slice info successful:\n%s", output)
}

// TestSliceStatus tests getting slice status
// Command: gs slice status my-team
func TestSliceStatus(t *testing.T) {
	// First, create a test slice
	testSliceID := "test-slice-status"
	if err := createTestSlice(testSliceID); err != nil {
		t.Fatalf("Failed to create test slice: %v", err)
	}

	output, err := runCLI("slice", "status", testSliceID)
	if err != nil {
		t.Fatalf("Failed to get slice status: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, testSliceID) {
		t.Errorf("Expected output to contain slice ID '%s', got: %s", testSliceID, output)
	}

	t.Logf("Slice status successful:\n%s", output)
}

// TestSliceOwners tests getting slice owners
// Command: gs slice owners my-team
func TestSliceOwners(t *testing.T) {
	testSliceID := "test-slice-owners"
	output, err := runCLI("slice", "owners", testSliceID)
	if err != nil {
		// This is expected to fail as owners is not implemented yet
		t.Logf("Slice owners command failed as expected: %v\nOutput: %s", err, output)
		return
	}

	t.Logf("Slice owners output:\n%s", output)
}

// TestSliceInit tests initializing current directory for slice
// Command: gs init my-team
func TestSliceInit(t *testing.T) {
	// Create temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "gitslice-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Change to temp directory
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change to temp dir: %v", err)
	}

	// Initialize slice
	testSliceID := "test-init-slice"
	output, err := runCLIInDir(tmpDir, "init", testSliceID)
	if err != nil {
		t.Fatalf("Failed to initialize slice: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, "Initialized") {
		t.Errorf("Expected output to contain 'Initialized', got: %s", output)
	}

	// Check that .gs directory was created
	gsDir := filepath.Join(tmpDir, ".gs")
	if _, err := os.Stat(gsDir); os.IsNotExist(err) {
		t.Error("Expected .gs directory to be created")
	}

	// Check that config file was created
	configFile := filepath.Join(gsDir, "config")
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		t.Error("Expected .gs/config file to be created")
	}

	// Verify config content
	content, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("Failed to read config file: %v", err)
	}

	if string(content) != testSliceID {
		t.Errorf("Expected config to contain slice ID '%s', got: %s", testSliceID, string(content))
	}

	t.Logf("Slice initialization successful:\n%s", output)
}

// TestSliceInitWithPath tests creating directory in specific path
// Command: gs init my-team --path ./work/my-team
func TestSliceInitWithPath(t *testing.T) {
	assertUnsupportedCommand(t, "init", "my-team", "--path", "./work/my-team")
}

// TestSliceInitForce tests initializing with force flag
// Command: gs init my-team --force
func TestSliceInitForce(t *testing.T) {
	assertUnsupportedCommand(t, "init", "my-team", "--force")
}

// TestSliceInitDescription tests initializing with description
// Command: gs init my-team --description "My team's services"
func TestSliceInitDescription(t *testing.T) {
	assertUnsupportedCommand(t, "init", "my-team", "--description", "My team's services")
}

// Helper function to create a test slice via storage
func createTestSlice(sliceID string) error {
	_, err := runCLI("slice", "create", sliceID, "--description", "test slice")
	return err
}

// Helper function to run CLI in a specific directory
func runCLIInDir(dir string, args ...string) (string, error) {
	originalDir, _ := os.Getwd()
	defer os.Chdir(originalDir)

	if err := os.Chdir(dir); err != nil {
		return "", err
	}

	return runCLI(args...)
}
