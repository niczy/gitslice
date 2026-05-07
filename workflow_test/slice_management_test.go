package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSliceCheckoutByID(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "slice-metadata-checkout"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceArg, "--here")
	if resp.SliceID != sliceID {
		t.Fatalf("expected checkout JSON output, got: %+v", resp)
	}
}

func TestSliceInitStoresSliceID(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "test-init-slice"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	output := runCLIOrFail(t, workdir, "init", sliceArg)
	if !strings.Contains(output, "Initialized empty gitslice checkout") {
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

	if strings.TrimSpace(string(content)) != sliceArg {
		t.Errorf("expected config to contain slice ID '%s', got: %s", sliceArg, string(content))
	}
}

func TestSliceInitWithPath(t *testing.T) {
	sliceArg := sliceIDArg("init-unsupported")
	assertUnsupportedCommand(t, "init", sliceArg, "--path", "./work/my-team")
}

func TestSliceInitForce(t *testing.T) {
	sliceArg := sliceIDArg("init-force")
	assertUnsupportedCommand(t, "init", sliceArg, "--force")
}

func TestSliceInitDescription(t *testing.T) {
	sliceArg := sliceIDArg("init-description")
	assertUnsupportedCommand(t, "init", sliceArg, "--description", "My team's services")
}
