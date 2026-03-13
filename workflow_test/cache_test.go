package workflow

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func TestCheckoutRegistryAndCacheCommands(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir home dir: %v", err)
	}

	username := fmt.Sprintf("cache-user-%d", time.Now().UnixNano())
	env := map[string]string{"HOME": homeDir}
	runCLIForUser := func(workdir string, args ...string) string {
		t.Helper()
		output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", env, true, username, args...)
		if err != nil {
			t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
		}
		return output
	}

	ctx := context.Background()
	content := append([]byte{}, bytes.Repeat([]byte("A"), storage.DefaultFileBlockSize)...)
	content = append(content, bytes.Repeat([]byte("B"), 97)...)

	homeSlice, err := homeslice.EnsureUserHomeSlice(ctx, testStorage, username)
	if err != nil {
		t.Fatalf("ensure home slice: %v", err)
	}

	storedDir := fmt.Sprintf("%s/cache-command-%d", username, time.Now().UnixNano())
	storedPath := storedDir + "/tracked.txt"
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(homeSlice.ID, storedPath),
		Path:     storedPath,
		Type:     "file",
		ParentID: homeSlice.ID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("add entry: %v", err)
	}
	if _, err := storage.WriteSliceFileManifest(ctx, testStorage, homeSlice.ID, storedPath, content); err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	if err := testStorage.AddFileToSlice(ctx, storedPath, homeSlice.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	checkoutDir := filepath.Join(t.TempDir(), "checkout")
	if err := os.MkdirAll(checkoutDir, 0o755); err != nil {
		t.Fatalf("mkdir checkout dir: %v", err)
	}

	output := runCLIForUser(checkoutDir, "slice", "checkout", homeslice.IDForUsername(username))
	if !strings.Contains(output, "Checked out slice: "+homeslice.IDForUsername(username)) {
		t.Fatalf("expected checkout output, got: %s", output)
	}

	output = runCLIForUser("", "slice", "checkouts")
	if !strings.Contains(output, "Tracked checkouts: 1") {
		t.Fatalf("expected tracked checkout count, got: %s", output)
	}
	if !strings.Contains(output, homeslice.IDForUsername(username)) {
		t.Fatalf("expected slice ID in checkout registry output, got: %s", output)
	}
	if !strings.Contains(output, checkoutDir) {
		t.Fatalf("expected checkout path in registry output, got: %s", output)
	}

	output = runCLIForUser("", "cache", "stats", "--checkouts")
	if !strings.Contains(output, "Tracked checkouts: 1") {
		t.Fatalf("expected tracked checkout count in cache stats, got: %s", output)
	}
	if !strings.Contains(output, "Unique slices: 1") {
		t.Fatalf("expected unique slice count in cache stats, got: %s", output)
	}
	if !strings.Contains(output, "Cached objects:") {
		t.Fatalf("expected cache object summary, got: %s", output)
	}

	if err := os.RemoveAll(checkoutDir); err != nil {
		t.Fatalf("remove checkout dir: %v", err)
	}

	output = runCLIForUser("", "cache", "prune")
	if !strings.Contains(output, "Pruned stale checkout records: 1") {
		t.Fatalf("expected stale checkout prune output, got: %s", output)
	}

	output = runCLIForUser("", "slice", "checkouts")
	if !strings.Contains(output, "Tracked checkouts: 0") {
		t.Fatalf("expected empty checkout registry after stale cleanup, got: %s", output)
	}

	output = runCLIForUser("", "cache", "clear", "--objects")
	if !strings.Contains(output, "Removed cached objects:") {
		t.Fatalf("expected object cleanup output, got: %s", output)
	}

	output = runCLIForUser("", "cache", "stats")
	if !strings.Contains(output, "Cached objects: 0") {
		t.Fatalf("expected empty cache after clear, got: %s", output)
	}
}
