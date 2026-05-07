package workflow

import (
	"bytes"
	"context"
	"encoding/json"
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

	output := runCLIForUser(checkoutDir, "slice", "checkout", homeslice.IDForUsername(username), "--here", "--json")
	var checkoutResp sliceCheckoutJSON
	if err := json.Unmarshal([]byte(output), &checkoutResp); err != nil {
		t.Fatalf("decode checkout JSON: %v\nOutput:\n%s", err, output)
	}
	if checkoutResp.SliceID != homeslice.IDForUsername(username) {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
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

	statsOutput := runCLIForUser("", "cache", "stats", "--checkouts", "--json")
	var statsResp cacheStatsJSON
	if err := json.Unmarshal([]byte(statsOutput), &statsResp); err != nil {
		t.Fatalf("decode cache stats JSON: %v\nOutput:\n%s", err, statsOutput)
	}
	if statsResp.TrackedCheckouts != 1 || statsResp.UniqueSlices != 1 || statsResp.CachedObjects == 0 {
		t.Fatalf("expected cache object summary, got: %+v", statsResp)
	}
	if len(statsResp.Checkouts) != 1 || statsResp.Checkouts[0].SliceID != homeslice.IDForUsername(username) {
		t.Fatalf("expected checkout listing in cache stats, got: %+v", statsResp)
	}

	if err := os.RemoveAll(checkoutDir); err != nil {
		t.Fatalf("remove checkout dir: %v", err)
	}

	pruneOutput := runCLIForUser("", "cache", "prune", "--json")
	var pruneResp cachePruneJSON
	if err := json.Unmarshal([]byte(pruneOutput), &pruneResp); err != nil {
		t.Fatalf("decode cache prune JSON: %v\nOutput:\n%s", err, pruneOutput)
	}
	if pruneResp.Removed != 1 {
		t.Fatalf("expected stale checkout prune output, got: %+v", pruneResp)
	}

	output = runCLIForUser("", "slice", "checkouts")
	if !strings.Contains(output, "Tracked checkouts: 0") {
		t.Fatalf("expected empty checkout registry after stale cleanup, got: %s", output)
	}

	clearOutput := runCLIForUser("", "cache", "clear", "--objects", "--json")
	var clearResp cacheClearJSON
	if err := json.Unmarshal([]byte(clearOutput), &clearResp); err != nil {
		t.Fatalf("decode cache clear JSON: %v\nOutput:\n%s", err, clearOutput)
	}
	if clearResp.RemovedCachedObjects == 0 {
		t.Fatalf("expected object cleanup output, got: %+v", clearResp)
	}

	statsOutput = runCLIForUser("", "cache", "stats", "--json")
	if err := json.Unmarshal([]byte(statsOutput), &statsResp); err != nil {
		t.Fatalf("decode cache stats JSON: %v\nOutput:\n%s", err, statsOutput)
	}
	if statsResp.CachedObjects != 0 {
		t.Fatalf("expected empty cache after clear, got: %+v", statsResp)
	}
}
