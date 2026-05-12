package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/searchindex"
)

func mustWriteSliceManifest(tb testing.TB, ctx context.Context, st Storage, sliceID, filePath string, content []byte) *models.FileManifest {
	tb.Helper()
	manifest, err := WriteSliceFileManifest(ctx, st, sliceID, filePath, content)
	if err != nil {
		tb.Fatalf("WriteSliceFileManifest(%s) failed: %v", filePath, err)
	}
	return manifest
}

func mustReadSliceFile(tb testing.TB, ctx context.Context, st Storage, sliceID, filePath string) *models.FileContent {
	tb.Helper()
	content, err := ReadSliceFileContent(ctx, st, sliceID, filePath)
	if err != nil {
		tb.Fatalf("ReadSliceFileContent(%s) failed: %v", filePath, err)
	}
	return content
}

func storageTestCases(ctx context.Context) []struct {
	name    string
	factory func(t *testing.T) Storage
} {
	cases := []struct {
		name    string
		factory func(t *testing.T) Storage
	}{
		{
			name: "in-memory",
			factory: func(t *testing.T) Storage {
				t.Helper()
				return NewInMemoryStorage()
			},
		},
	}
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		cases = append(cases, struct {
			name    string
			factory func(t *testing.T) Storage
		}{
			name: "postgres-native",
			factory: func(t *testing.T) Storage {
				t.Helper()
				st, err := NewPostgresNativeStorage(ctx, dsn, NewInMemoryObjectStore(), fmt.Sprintf("test-native-%d", time.Now().UnixNano()))
				if err != nil {
					t.Fatalf("NewPostgresNativeStorage failed: %v", err)
				}
				t.Cleanup(func() { _ = st.Close() })
				return st
			},
		})
	}
	return cases
}

func TestStorageCompliance(t *testing.T) {
	ctx := context.Background()

	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			runStorageContract(ctx, t, tc.factory(t))
		})
	}
}

func TestCIManifestIndexStoreCompliance(t *testing.T) {
	ctx := context.Background()

	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			runCIManifestIndexStoreContract(ctx, t, tc.factory(t))
		})
	}
}

func TestCIJobRequeueStoreCompliance(t *testing.T) {
	ctx := context.Background()

	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			runCIJobRequeueStoreContract(ctx, t, tc.factory(t))
		})
	}
}

func runCIJobRequeueStoreContract(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()
	slice := &models.Slice{ID: "home_alice_ci_requeue", Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	changeset := &models.Changeset{
		ID:             "chg_ci_requeue",
		SliceID:        slice.ID,
		BaseCommitHash: "base-ci-requeue",
		Hash:           "hash-ci-requeue",
		Status:         models.ChangesetStatusPending,
		Author:         "alice",
		CreatedAt:      time.Now(),
	}
	if err := st.CreateChangeset(ctx, changeset); err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	now := time.Now().UTC()
	if err := st.CreateCIRunner(ctx, &CIRunner{
		ID:        "ci_runner_requeue",
		HomeID:    "alice",
		Name:      "runner",
		Pool:      "default",
		Executor:  "shell",
		Status:    "idle",
		TokenHash: "hash",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateCIRunner failed: %v", err)
	}
	if err := st.CreateCIPlan(ctx, &CIPlan{
		Run: &CIRun{
			ID:                 "ci_run_requeue",
			HomeID:             "alice",
			SliceID:            slice.ID,
			ChangesetID:        changeset.ID,
			ChangesetVersionID: "snap-1",
			PlanHash:           "plan",
			Status:             "queued",
			CreatedAt:          now,
		},
		Jobs: []*CIJob{{
			ID:         "ci_job_requeue",
			RunID:      "ci_run_requeue",
			JobKey:     "unit",
			CheckName:  "unit",
			RunnerPool: "default",
			Status:     "queued",
		}},
	}); err != nil {
		t.Fatalf("CreateCIPlan failed: %v", err)
	}
	claimed, err := st.ClaimCIJob(ctx, "ci_job_requeue", "ci_runner_requeue", "lease-1", now.Add(time.Minute), now)
	if err != nil {
		t.Fatalf("ClaimCIJob failed: %v", err)
	}
	if claimed.AttemptCount != 1 || claimed.MaxAttempts != DefaultCIJobMaxAttempts {
		t.Fatalf("claimed retry metadata = attempt %d max %d, want 1/%d", claimed.AttemptCount, claimed.MaxAttempts, DefaultCIJobMaxAttempts)
	}
	requeued, err := st.RequeueCIJob(ctx, "ci_job_requeue", "lease-1", now.Add(time.Second))
	if err != nil {
		t.Fatalf("RequeueCIJob failed: %v", err)
	}
	if requeued.Status != "queued" || requeued.RunnerID != "" || requeued.LeaseID != "" || requeued.LeaseExpiresAt != nil {
		t.Fatalf("requeued job = %#v, want queued with cleared lease", requeued)
	}
	if requeued.AttemptCount != 1 || requeued.MaxAttempts != DefaultCIJobMaxAttempts {
		t.Fatalf("requeued retry metadata = attempt %d max %d, want preserved", requeued.AttemptCount, requeued.MaxAttempts)
	}
	runner, err := st.GetCIRunner(ctx, "ci_runner_requeue")
	if err != nil {
		t.Fatalf("GetCIRunner failed: %v", err)
	}
	if runner.Status != "idle" {
		t.Fatalf("runner status = %q, want idle", runner.Status)
	}
}

func TestMergeEventStoreCompliance(t *testing.T) {
	ctx := context.Background()

	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			store, ok := tc.factory(t).(MergeEventStore)
			if !ok {
				t.Fatalf("storage implementation does not implement MergeEventStore")
			}
			runMergeEventStoreContract(ctx, t, store)
		})
	}
}

func runCIManifestIndexStoreContract(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()
	now := time.Now().UTC()
	first := []*CIManifestIndexEntry{
		{
			HomeID:         "alice",
			HomeCommitHash: "commit-1",
			ManifestPath:   "/api/.gs-ci.yaml",
			ManifestDir:    "/api",
			ManifestHash:   "hash-api",
			WatchGlobs:     []string{"**/*.go"},
			IgnoreGlobs:    []string{"vendor/**"},
			AppliesToGlobs: []string{"/shared/lib/**"},
			ParseStatus:    "ok",
			UpdatedAt:      now,
		},
		{
			HomeID:         "alice",
			HomeCommitHash: "commit-1",
			ManifestPath:   "/web/.gs-ci.yaml",
			ManifestDir:    "/web",
			ManifestHash:   "hash-web",
			ParseStatus:    "error",
			ParseError:     "bad yaml",
			UpdatedAt:      now,
		},
	}
	if err := st.ReplaceCIManifestIndex(ctx, "alice", "commit-1", first); err != nil {
		t.Fatalf("ReplaceCIManifestIndex failed: %v", err)
	}
	listed, err := st.ListCIManifestIndex(ctx, "alice", "commit-1")
	if err != nil {
		t.Fatalf("ListCIManifestIndex failed: %v", err)
	}
	if len(listed) != 2 || listed[0].ManifestPath != "/api/.gs-ci.yaml" || listed[1].ManifestPath != "/web/.gs-ci.yaml" {
		t.Fatalf("listed manifest index = %#v, want api and web", listed)
	}
	if got := listed[0].AppliesToGlobs; len(got) != 1 || got[0] != "/shared/lib/**" {
		t.Fatalf("applies_to globs = %#v, want shared lib", got)
	}
	replacement := []*CIManifestIndexEntry{{
		ManifestPath: "/cli/.gs-ci.yaml",
		ManifestDir:  "/cli",
		ManifestHash: "hash-cli",
		ParseStatus:  "ok",
	}}
	if err := st.ReplaceCIManifestIndex(ctx, "alice", "commit-1", replacement); err != nil {
		t.Fatalf("ReplaceCIManifestIndex replacement failed: %v", err)
	}
	listed, err = st.ListCIManifestIndex(ctx, "alice", "commit-1")
	if err != nil {
		t.Fatalf("ListCIManifestIndex replacement failed: %v", err)
	}
	if len(listed) != 1 || listed[0].ManifestPath != "/cli/.gs-ci.yaml" || listed[0].HomeID != "alice" || listed[0].HomeCommitHash != "commit-1" {
		t.Fatalf("replacement index = %#v, want only cli for alice commit-1", listed)
	}
	other, err := st.ListCIManifestIndex(ctx, "alice", "commit-missing")
	if err != nil {
		t.Fatalf("ListCIManifestIndex missing failed: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("missing commit index = %#v, want empty", other)
	}
}

func TestHomePathHeadStoreCompliance(t *testing.T) {
	ctx := context.Background()

	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			store, ok := tc.factory(t).(HomePathHeadStore)
			if !ok {
				t.Fatalf("storage implementation does not implement HomePathHeadStore")
			}
			runHomePathHeadStoreContract(ctx, t, store)
		})
	}
}

func TestMergeEventPathHeadCASStoreCompliance(t *testing.T) {
	ctx := context.Background()

	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.factory(t)
			store, ok := st.(MergeEventPathHeadCASStore)
			if !ok {
				t.Fatalf("storage implementation does not implement MergeEventPathHeadCASStore")
			}
			heads, ok := st.(HomePathHeadStore)
			if !ok {
				t.Fatalf("storage implementation does not implement HomePathHeadStore")
			}
			events, ok := st.(MergeEventStore)
			if !ok {
				t.Fatalf("storage implementation does not implement MergeEventStore")
			}
			runMergeEventPathHeadCASStoreContract(ctx, t, store, heads, events)
		})
	}
}

func runHomePathHeadStoreContract(ctx context.Context, t *testing.T, store HomePathHeadStore) {
	t.Helper()
	st, ok := store.(Storage)
	if !ok {
		t.Fatalf("home path head store does not implement Storage")
	}

	homeSlice := &models.Slice{
		ID:        "home_alice",
		Name:      "alice",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}
	if err := st.CreateSlice(ctx, homeSlice); err != nil {
		t.Fatalf("CreateSlice(home) failed: %v", err)
	}
	firstHash := writeHomePathHeadFile(t, ctx, st, homeSlice.ID, "alice/app/main.go", []byte("v1\n"))

	backfill, err := store.BackfillHomePathHeads(ctx, "alice")
	if err != nil {
		t.Fatalf("BackfillHomePathHeads failed: %v", err)
	}
	if backfill.HomeID != "alice" || backfill.SourceSliceID != homeSlice.ID || backfill.Upserted != 1 {
		t.Fatalf("unexpected backfill result: %#v", backfill)
	}

	heads, err := store.GetHomePathHeads(ctx, "alice", []string{"alice/app/main.go", "alice/missing.txt"})
	if err != nil {
		t.Fatalf("GetHomePathHeads failed: %v", err)
	}
	head := heads["alice/app/main.go"]
	if head == nil {
		t.Fatalf("expected head for alice/app/main.go")
	}
	if head.HomeID != "alice" || head.PathVersion != 1 || head.ManifestHash != firstHash || head.ContentHash != firstHash || head.SourceSliceID != homeSlice.ID {
		t.Fatalf("unexpected path head: %#v", head)
	}
	if _, ok := heads["alice/missing.txt"]; ok {
		t.Fatalf("did not expect missing path head")
	}

	validation, err := store.ValidateHomePathHeads(ctx, "alice")
	if err != nil {
		t.Fatalf("ValidateHomePathHeads failed: %v", err)
	}
	if validation.Checked != 1 || len(validation.Drifts) != 0 {
		t.Fatalf("expected no validation drift, got %#v", validation)
	}

	secondHash := writeHomePathHeadFile(t, ctx, st, homeSlice.ID, "alice/app/main.go", []byte("v2\n"))
	validation, err = store.ValidateHomePathHeads(ctx, "alice")
	if err != nil {
		t.Fatalf("ValidateHomePathHeads after materialized update failed: %v", err)
	}
	if len(validation.Drifts) != 1 || validation.Drifts[0].Reason != "manifest_mismatch" || validation.Drifts[0].MaterializedManifestHash != secondHash {
		t.Fatalf("expected manifest mismatch drift, got %#v", validation.Drifts)
	}

	backfill, err = store.BackfillHomePathHeads(ctx, "alice")
	if err != nil {
		t.Fatalf("second BackfillHomePathHeads failed: %v", err)
	}
	if backfill.Upserted != 1 {
		t.Fatalf("expected second backfill to upsert one head, got %#v", backfill)
	}
	heads, err = store.GetHomePathHeads(ctx, "alice", []string{"alice/app/main.go"})
	if err != nil {
		t.Fatalf("GetHomePathHeads after second backfill failed: %v", err)
	}
	if got := heads["alice/app/main.go"]; got == nil || got.ManifestHash != secondHash || got.PathVersion != 1 {
		t.Fatalf("expected backfilled head to match materialized state without version bump, got %#v", got)
	}

	if err := store.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:       "alice",
		Path:         "alice/stale.txt",
		PathVersion:  7,
		ManifestHash: "stale-hash",
		ContentHash:  "stale-hash",
	}}); err != nil {
		t.Fatalf("UpsertHomePathHeads extra head failed: %v", err)
	}
	if err := store.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:       "alice",
		Path:         "alice/stale.txt",
		PathVersion:  3,
		ManifestHash: "older-stale-hash",
		ContentHash:  "older-stale-hash",
	}}); err != nil {
		t.Fatalf("UpsertHomePathHeads stale head failed: %v", err)
	}
	heads, err = store.GetHomePathHeads(ctx, "alice", []string{"alice/stale.txt"})
	if err != nil {
		t.Fatalf("GetHomePathHeads stale head failed: %v", err)
	}
	if got := heads["alice/stale.txt"]; got == nil || got.PathVersion != 7 || got.ManifestHash != "stale-hash" {
		t.Fatalf("expected stale upsert to preserve newer head, got %#v", got)
	}
	listed, err := store.ListHomePathHeads(ctx, "alice", 10)
	if err != nil {
		t.Fatalf("ListHomePathHeads failed: %v", err)
	}
	if len(listed) != 2 || listed[0].Path != "alice/app/main.go" || listed[1].Path != "alice/stale.txt" {
		t.Fatalf("expected sorted home path heads, got %#v", listed)
	}
	validation, err = store.ValidateHomePathHeads(ctx, "alice")
	if err != nil {
		t.Fatalf("ValidateHomePathHeads extra head failed: %v", err)
	}
	if len(validation.Drifts) != 1 || validation.Drifts[0].Reason != "extra_head" || validation.Drifts[0].Path != "alice/stale.txt" {
		t.Fatalf("expected extra head drift, got %#v", validation.Drifts)
	}

	prefixedUserHome := &models.Slice{
		ID:        "home_home_alice",
		Name:      "home_alice",
		Owners:    []string{"home_alice"},
		CreatedBy: "home_alice",
	}
	if err := st.CreateSlice(ctx, prefixedUserHome); err != nil {
		t.Fatalf("CreateSlice(home_ prefixed user) failed: %v", err)
	}
	prefixedHash := writeHomePathHeadFile(t, ctx, st, prefixedUserHome.ID, "home_alice/app/main.go", []byte("prefixed\n"))
	backfill, err = store.BackfillHomePathHeads(ctx, "home_alice")
	if err != nil {
		t.Fatalf("BackfillHomePathHeads home_ prefixed user failed: %v", err)
	}
	if backfill.HomeID != "home_alice" || backfill.SourceSliceID != prefixedUserHome.ID || backfill.Upserted != 1 {
		t.Fatalf("unexpected home_ prefixed backfill result: %#v", backfill)
	}
	heads, err = store.GetHomePathHeads(ctx, "home_alice", []string{"home_alice/app/main.go"})
	if err != nil {
		t.Fatalf("GetHomePathHeads home_ prefixed user failed: %v", err)
	}
	if got := heads["home_alice/app/main.go"]; got == nil || got.ManifestHash != prefixedHash || got.HomeID != "home_alice" {
		t.Fatalf("expected distinct home_ prefixed user head, got %#v", got)
	}
}

func runMergeEventPathHeadCASStoreContract(ctx context.Context, t *testing.T, store MergeEventPathHeadCASStore, heads HomePathHeadStore, events MergeEventStore) {
	t.Helper()
	if err := heads.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           "alice",
		Path:             "alice/app/main.go",
		PathVersion:      7,
		ContentHash:      "sha256:old-content",
		ManifestHash:     "sha256:old-manifest",
		SourceSliceID:    "sl-old",
		SourceCommitHash: "commit-old",
		LastMergeSeq:     4,
	}}); err != nil {
		t.Fatalf("seed path head failed: %v", err)
	}

	event := sampleMergeEvent("alice", 11, 5, "evt-cas-ok", "chg-cas-ok")
	event.PathUpdates[0].BaseVersion = 7
	event.PathUpdates[0].NewVersion = 8
	event.PathUpdates[0].SourceSliceID = event.SourceSliceID
	event.PathUpdates[0].SourceCommitHash = event.SourceCommitHash
	if err := store.AppendMergeEventWithPathHeadCAS(ctx, event); err != nil {
		t.Fatalf("AppendMergeEventWithPathHeadCAS success failed: %v", err)
	}

	storedEvent, err := events.GetMergeEventByChangeset(ctx, event.ChangesetID)
	if err != nil {
		t.Fatalf("GetMergeEventByChangeset after CAS failed: %v", err)
	}
	if storedEvent.MergeSeq != event.MergeSeq || len(storedEvent.PathUpdates) != 1 || storedEvent.PathUpdates[0].NewVersion != 8 {
		t.Fatalf("unexpected stored CAS event: %#v", storedEvent)
	}

	storedHeads, err := heads.GetHomePathHeads(ctx, "alice", []string{"alice/app/main.go"})
	if err != nil {
		t.Fatalf("GetHomePathHeads after CAS failed: %v", err)
	}
	head := storedHeads["alice/app/main.go"]
	if head == nil || head.PathVersion != 8 || head.LastMergeSeq != 5 || head.ManifestHash != event.PathUpdates[0].ManifestHash {
		t.Fatalf("unexpected CAS path head: %#v", head)
	}

	conflictEvent := sampleMergeEvent("alice", 11, 6, "evt-cas-conflict", "chg-cas-conflict")
	conflictEvent.PathUpdates[0].BaseVersion = 7
	conflictEvent.PathUpdates[0].NewVersion = 8
	if err := store.AppendMergeEventWithPathHeadCAS(ctx, conflictEvent); !errors.Is(err, ErrHomePathHeadConflict) {
		t.Fatalf("expected path-head conflict, got %v", err)
	}
	if _, err := events.GetMergeEventByChangeset(ctx, conflictEvent.ChangesetID); !errors.Is(err, ErrMergeEventNotFound) {
		t.Fatalf("conflicting CAS event should not be appended, got %v", err)
	}
}

func runMergeEventStoreContract(ctx context.Context, t *testing.T, store MergeEventStore) {
	t.Helper()

	missing, err := store.GetProjectionOffset(ctx, "home-tree", 1)
	if err != nil {
		t.Fatalf("GetProjectionOffset missing failed: %v", err)
	}
	if missing.MergeSeq != 0 {
		t.Fatalf("expected missing projection offset seq 0, got %d", missing.MergeSeq)
	}

	event2 := sampleMergeEvent("home-alice", 1, 2, "evt-2", "chg-2")
	if err := store.AppendMergeEvent(ctx, event2); err != nil {
		t.Fatalf("AppendMergeEvent seq2 failed: %v", err)
	}
	event1 := sampleMergeEvent("home-alice", 1, 1, "evt-1", "chg-1")
	if err := store.AppendMergeEvent(ctx, event1); err != nil {
		t.Fatalf("AppendMergeEvent seq1 failed: %v", err)
	}
	otherShard := sampleMergeEvent("home-bob", 2, 1, "evt-shard-2", "chg-shard-2")
	if err := store.AppendMergeEvent(ctx, otherShard); err != nil {
		t.Fatalf("AppendMergeEvent other shard failed: %v", err)
	}

	fetched, err := store.GetMergeEventByChangeset(ctx, "chg-1")
	if err != nil {
		t.Fatalf("GetMergeEventByChangeset failed: %v", err)
	}
	if fetched.EventID != "evt-1" || fetched.MergeSeq != 1 || len(fetched.PathUpdates) != 1 {
		t.Fatalf("unexpected fetched event: %#v", fetched)
	}
	if fetched.PathUpdates[0].Path != "alice/app/main.go" || fetched.PathUpdates[0].NewVersion != 2 {
		t.Fatalf("unexpected path update: %#v", fetched.PathUpdates[0])
	}
	if fetched.CreatedAt.IsZero() {
		t.Fatalf("expected CreatedAt to be populated")
	}

	listed, err := store.ListMergeEvents(ctx, 1, 0, 10)
	if err != nil {
		t.Fatalf("ListMergeEvents failed: %v", err)
	}
	if len(listed) != 2 || listed[0].MergeSeq != 1 || listed[1].MergeSeq != 2 {
		t.Fatalf("expected shard events in sequence order, got %#v", listed)
	}
	afterFirst, err := store.ListMergeEvents(ctx, 1, 1, 10)
	if err != nil {
		t.Fatalf("ListMergeEvents after seq failed: %v", err)
	}
	if len(afterFirst) != 1 || afterFirst[0].MergeSeq != 2 {
		t.Fatalf("expected one event after seq 1, got %#v", afterFirst)
	}
	limited, err := store.ListMergeEvents(ctx, 1, 0, 1)
	if err != nil {
		t.Fatalf("ListMergeEvents limit failed: %v", err)
	}
	if len(limited) != 1 || limited[0].MergeSeq != 1 {
		t.Fatalf("expected limited first event, got %#v", limited)
	}
	shardTwo, err := store.ListMergeEvents(ctx, 2, 0, 10)
	if err != nil {
		t.Fatalf("ListMergeEvents shard 2 failed: %v", err)
	}
	if len(shardTwo) != 1 || shardTwo[0].ChangesetID != "chg-shard-2" {
		t.Fatalf("expected shard isolation, got %#v", shardTwo)
	}

	duplicateChangeset := sampleMergeEvent("home-alice", 1, 3, "evt-3", "chg-1")
	if err := store.AppendMergeEvent(ctx, duplicateChangeset); !errors.Is(err, ErrMergeEventConflict) {
		t.Fatalf("expected duplicate changeset conflict, got %v", err)
	}
	duplicateSeq := sampleMergeEvent("home-alice", 1, 1, "evt-duplicate-seq", "chg-duplicate-seq")
	if err := store.AppendMergeEvent(ctx, duplicateSeq); !errors.Is(err, ErrMergeEventConflict) {
		t.Fatalf("expected duplicate shard sequence conflict, got %v", err)
	}

	if err := store.UpdateProjectionOffset(ctx, &models.ProjectionOffset{ProjectionName: "home-tree", ShardID: 1, MergeSeq: 2}); err != nil {
		t.Fatalf("UpdateProjectionOffset seq2 failed: %v", err)
	}
	if err := store.UpdateProjectionOffset(ctx, &models.ProjectionOffset{ProjectionName: "home-tree", ShardID: 1, MergeSeq: 1}); err != nil {
		t.Fatalf("UpdateProjectionOffset stale seq failed: %v", err)
	}
	offset, err := store.GetProjectionOffset(ctx, "home-tree", 1)
	if err != nil {
		t.Fatalf("GetProjectionOffset failed: %v", err)
	}
	if offset.MergeSeq != 2 {
		t.Fatalf("expected stale offset update to be ignored, got %d", offset.MergeSeq)
	}
	if err := store.UpdateProjectionOffset(ctx, &models.ProjectionOffset{ProjectionName: "home-tree", ShardID: 1, MergeSeq: 3}); err != nil {
		t.Fatalf("UpdateProjectionOffset seq3 failed: %v", err)
	}
	offset, err = store.GetProjectionOffset(ctx, "home-tree", 1)
	if err != nil {
		t.Fatalf("GetProjectionOffset after seq3 failed: %v", err)
	}
	if offset.MergeSeq != 3 {
		t.Fatalf("expected offset seq 3, got %d", offset.MergeSeq)
	}

	processor, ok := store.(MergeEventProjectionBatchProcessor)
	if !ok {
		t.Fatalf("storage implementation does not implement MergeEventProjectionBatchProcessor")
	}
	var processed []*models.MergeEvent
	claimed, err := processor.ProcessMergeEventProjectionBatch(ctx, "root-promotion-test", 4, 10, func(_ context.Context, events []*models.MergeEvent) error {
		processed = append(processed, events...)
		return nil
	})
	if err != nil {
		t.Fatalf("ProcessMergeEventProjectionBatch failed: %v", err)
	}
	if !claimed {
		t.Fatalf("expected projection batch to be claimed")
	}
	if len(processed) != 2 || processed[0].MergeSeq != 1 || processed[1].MergeSeq != 2 {
		t.Fatalf("expected shard 1 events in order, got %#v", processed)
	}
	offset, err = store.GetProjectionOffset(ctx, "root-promotion-test", 1)
	if err != nil {
		t.Fatalf("GetProjectionOffset after projection batch failed: %v", err)
	}
	if offset.MergeSeq != 2 {
		t.Fatalf("expected projection offset seq 2, got %d", offset.MergeSeq)
	}
	claimed, err = processor.ProcessMergeEventProjectionBatch(ctx, "root-promotion-test", 4, 10, func(_ context.Context, events []*models.MergeEvent) error {
		processed = append(processed, events...)
		return nil
	})
	if err != nil {
		t.Fatalf("ProcessMergeEventProjectionBatch second claim failed: %v", err)
	}
	if !claimed {
		t.Fatalf("expected shard 2 projection batch to be claimed")
	}
	offset, err = store.GetProjectionOffset(ctx, "root-promotion-test", 2)
	if err != nil {
		t.Fatalf("GetProjectionOffset shard 2 after projection batch failed: %v", err)
	}
	if offset.MergeSeq != 1 {
		t.Fatalf("expected shard 2 projection offset seq 1, got %d", offset.MergeSeq)
	}
}

func writeHomePathHeadFile(t *testing.T, ctx context.Context, st Storage, sliceID, filePath string, content []byte) string {
	t.Helper()
	manifest := mustWriteSliceManifest(t, ctx, st, sliceID, filePath, content)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       generateEntryID(sliceID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: nativeParentID(sliceID, filePath),
		Hash:     manifest.Hash,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("AddEntry(%s) failed: %v", filePath, err)
	}
	return manifest.Hash
}

func sampleMergeEvent(homeID string, shardID int32, mergeSeq int64, eventID, changesetID string) *models.MergeEvent {
	return &models.MergeEvent{
		HomeID:           homeID,
		ShardID:          shardID,
		MergeSeq:         mergeSeq,
		EventID:          eventID,
		ChangesetID:      changesetID,
		SourceSliceID:    "sl-source",
		SourceCommitHash: fmt.Sprintf("commit-%d", mergeSeq),
		Author:           "alice",
		Message:          "merge event test",
		TouchedPaths:     []string{"alice/app/main.go"},
		PathUpdates: []*models.MergePathUpdate{{
			Path:             "alice/app/main.go",
			BaseVersion:      mergeSeq,
			NewVersion:       mergeSeq + 1,
			ContentHash:      fmt.Sprintf("sha256:content-%d", mergeSeq),
			ManifestHash:     fmt.Sprintf("sha256:manifest-%d", mergeSeq),
			SourceSliceID:    "sl-source",
			SourceCommitHash: fmt.Sprintf("commit-%d", mergeSeq),
		}},
	}
}

func TestStoragePrefersSliceScopedFileContentOverSharedPathContent(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			runSliceScopedContentPreferenceTest(ctx, t, tc.factory(t))
		})
	}
}

func TestStoragePrefersManifestOverSharedPathContent(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			runManifestPreferenceTest(ctx, t, tc.factory(t))
		})
	}
}

func TestSearchIndexStorageRoundTrip(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			runSearchIndexStorageRoundTrip(ctx, t, tc.factory(t))
		})
	}
}

func TestBuildSliceSearchArtifactFromCommitSnapshot(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			runBuildSliceSearchArtifactFromCommitSnapshot(ctx, t, tc.factory(t))
		})
	}
}

func TestSliceVisibilityStorageRoundTrip(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			runSliceVisibilityStorageRoundTrip(ctx, t, tc.factory(t))
		})
	}
}

func TestPathVisibilityStorageRoundTrip(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			runPathVisibilityStorageRoundTrip(ctx, t, tc.factory(t))
		})
	}
}

func TestDirectoryEntryAggregatesSubtreeSizes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			runDirectoryEntryAggregatesSubtreeSizes(ctx, t, tc.factory(t))
		})
	}
}

func runSliceScopedContentPreferenceTest(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	filePath := "README.md"
	root := &models.Slice{ID: "root", Name: "Root", Files: []string{filePath}, Owners: []string{"system"}, CreatedBy: "system", IsRoot: true}
	home := &models.Slice{ID: "home_alice", Name: "alice", Files: []string{filePath}, Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, root); err != nil {
		t.Fatalf("CreateSlice root failed: %v", err)
	}
	if err := st.CreateSlice(ctx, home); err != nil {
		t.Fatalf("CreateSlice home failed: %v", err)
	}

	rootEntryID := generateEntryID(root.ID, filePath)
	homeEntryID := generateEntryID(home.ID, filePath)

	if err := st.UpdateEntry(ctx, &models.DirectoryEntry{ID: rootEntryID, Path: filePath, Type: "file", ParentID: root.ID, Size: 12}); err != nil {
		t.Fatalf("AddEntry root failed: %v", err)
	}
	rootManifest := mustWriteSliceManifest(t, ctx, st, root.ID, filePath, []byte("root version"))
	if err := st.UpdateEntry(ctx, &models.DirectoryEntry{ID: homeEntryID, Path: filePath, Type: "file", ParentID: home.ID, Size: 12}); err != nil {
		t.Fatalf("AddEntry home failed: %v", err)
	}
	homeManifest := mustWriteSliceManifest(t, ctx, st, home.ID, filePath, []byte("home version"))
	content := mustReadSliceFile(t, ctx, st, home.ID, filePath)
	if got := string(content.Content); got != "home version" {
		t.Fatalf("expected home content, got %q", got)
	}
	if content.Hash != homeManifest.Hash {
		t.Fatalf("expected home hash, got %q", content.Hash)
	}

	files, err := ListSliceFileContents(ctx, st, home.ID)
	if err != nil {
		t.Fatalf("ListSliceFileContents home failed: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one home file, got %#v", files)
	}
	if got := string(files[0].Content); got != "home version" {
		t.Fatalf("expected home file list content, got %q", got)
	}
	if files[0].Hash != homeManifest.Hash {
		t.Fatalf("expected home file list hash, got %q", files[0].Hash)
	}

	entry, err := st.GetEntry(ctx, homeEntryID)
	if err != nil {
		t.Fatalf("GetEntry home failed: %v", err)
	}
	if entry.Hash != homeManifest.Hash {
		t.Fatalf("expected home entry hash, got %q", entry.Hash)
	}

	byPath, err := st.GetEntryByPath(ctx, home.ID, filePath)
	if err != nil {
		t.Fatalf("GetEntryByPath home failed: %v", err)
	}
	if byPath.Hash != homeManifest.Hash {
		t.Fatalf("expected home path hash, got %q", byPath.Hash)
	}

	entries, err := st.ListEntries(ctx, home.ID, home.ID)
	if err != nil {
		t.Fatalf("ListEntries home failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one root child entry, got %#v", entries)
	}
	if entries[0].Hash != homeManifest.Hash {
		t.Fatalf("expected home list hash, got %q", entries[0].Hash)
	}
	if rootManifest.Hash == homeManifest.Hash {
		t.Fatalf("expected distinct root and home manifests for preference test")
	}
}

func runManifestPreferenceTest(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	filePath := "alice/README.md"
	root := &models.Slice{ID: "root", Name: "Root", Files: []string{filePath}, Owners: []string{"system"}, CreatedBy: "system", IsRoot: true}
	home := &models.Slice{ID: "home_alice", Name: "alice", Files: []string{filePath}, Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, root); err != nil {
		t.Fatalf("CreateSlice root failed: %v", err)
	}
	if err := st.CreateSlice(ctx, home); err != nil {
		t.Fatalf("CreateSlice home failed: %v", err)
	}

	homeEntryID := generateEntryID(home.ID, filePath)
	if err := st.UpdateEntry(ctx, &models.DirectoryEntry{ID: homeEntryID, Path: filePath, Type: "file", ParentID: home.ID}); err != nil {
		t.Fatalf("UpdateEntry home failed: %v", err)
	}
	_ = mustWriteSliceManifest(t, ctx, st, root.ID, filePath, []byte("root version"))

	putManifest := func(content string) *models.FileManifest {
		return mustWriteSliceManifest(t, ctx, st, home.ID, filePath, []byte(content))
	}

	firstManifest := putManifest("home version v1")
	first := mustReadSliceFile(t, ctx, st, home.ID, filePath)
	if got, want := string(first.Content), "home version v1"; got != want {
		t.Fatalf("expected manifest-backed home content, got %q want %q", got, want)
	}
	if first.Hash != firstManifest.Hash {
		t.Fatalf("expected manifest-backed home hash %q, got %q", firstManifest.Hash, first.Hash)
	}

	secondManifest := putManifest("home version v2")
	second := mustReadSliceFile(t, ctx, st, home.ID, filePath)
	if got, want := string(second.Content), "home version v2"; got != want {
		t.Fatalf("expected updated manifest-backed home content, got %q want %q", got, want)
	}
	if second.Hash != secondManifest.Hash {
		t.Fatalf("expected updated manifest-backed home hash %q, got %q", secondManifest.Hash, second.Hash)
	}
}

func runSearchIndexStorageRoundTrip(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	blobV1 := []byte("blob-v1")
	blobV2 := []byte("blob-v2")
	if err := st.PutSearchIndexFileBlob(ctx, 1, "hash-1", blobV1); err != nil {
		t.Fatalf("PutSearchIndexFileBlob v1 failed: %v", err)
	}
	if err := st.PutSearchIndexFileBlob(ctx, 2, "hash-1", blobV2); err != nil {
		t.Fatalf("PutSearchIndexFileBlob v2 failed: %v", err)
	}
	gotBlobV1, err := st.GetSearchIndexFileBlob(ctx, 1, "hash-1")
	if err != nil {
		t.Fatalf("GetSearchIndexFileBlob v1 failed: %v", err)
	}
	gotBlobV2, err := st.GetSearchIndexFileBlob(ctx, 2, "hash-1")
	if err != nil {
		t.Fatalf("GetSearchIndexFileBlob v2 failed: %v", err)
	}
	if !bytes.Equal(gotBlobV1, blobV1) || !bytes.Equal(gotBlobV2, blobV2) {
		t.Fatalf("unexpected blob payloads: v1=%q v2=%q", string(gotBlobV1), string(gotBlobV2))
	}
	if _, err := st.GetSearchIndexFileBlob(ctx, 3, "hash-1"); err != ErrEntryNotFound {
		t.Fatalf("expected missing blob version to return ErrEntryNotFound, got %v", err)
	}

	artifactV1 := []byte("artifact-v1")
	artifactV2 := []byte("artifact-v2")
	if err := st.PutSliceSearchArtifact(ctx, "slice-1", "commit-1", 1, artifactV1); err != nil {
		t.Fatalf("PutSliceSearchArtifact v1 failed: %v", err)
	}
	if err := st.PutSliceSearchArtifact(ctx, "slice-1", "commit-1", 2, artifactV2); err != nil {
		t.Fatalf("PutSliceSearchArtifact v2 failed: %v", err)
	}
	gotArtifactV1, err := st.GetSliceSearchArtifact(ctx, "slice-1", "commit-1", 1)
	if err != nil {
		t.Fatalf("GetSliceSearchArtifact v1 failed: %v", err)
	}
	gotArtifactV2, err := st.GetSliceSearchArtifact(ctx, "slice-1", "commit-1", 2)
	if err != nil {
		t.Fatalf("GetSliceSearchArtifact v2 failed: %v", err)
	}
	if !bytes.Equal(gotArtifactV1, artifactV1) || !bytes.Equal(gotArtifactV2, artifactV2) {
		t.Fatalf("unexpected artifact payloads: v1=%q v2=%q", string(gotArtifactV1), string(gotArtifactV2))
	}
	if _, err := st.GetSliceSearchArtifact(ctx, "slice-1", "commit-1", 3); err != ErrEntryNotFound {
		t.Fatalf("expected missing artifact version to return ErrEntryNotFound, got %v", err)
	}

	workspaceArtifact := []byte("workspace-artifact-v1")
	if err := st.PutWorkspaceSearchArtifact(ctx, "workspace-1", 1, workspaceArtifact); err != nil {
		t.Fatalf("PutWorkspaceSearchArtifact failed: %v", err)
	}
	gotWorkspaceArtifact, err := st.GetWorkspaceSearchArtifact(ctx, "workspace-1", 1)
	if err != nil {
		t.Fatalf("GetWorkspaceSearchArtifact failed: %v", err)
	}
	if !bytes.Equal(gotWorkspaceArtifact, workspaceArtifact) {
		t.Fatalf("unexpected workspace artifact payload: got=%q want=%q", string(gotWorkspaceArtifact), string(workspaceArtifact))
	}
	if err := st.DeleteWorkspaceSearchArtifact(ctx, "workspace-1", 1); err != nil {
		t.Fatalf("DeleteWorkspaceSearchArtifact failed: %v", err)
	}
	if _, err := st.GetWorkspaceSearchArtifact(ctx, "workspace-1", 1); err != ErrEntryNotFound {
		t.Fatalf("expected deleted workspace artifact to return ErrEntryNotFound, got %v", err)
	}
}

func runDirectoryEntryAggregatesSubtreeSizes(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	slice := &models.Slice{
		ID:        "home_alice",
		Name:      "alice",
		Files:     []string{"docs/readme.md", "docs/guides/setup.md"},
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	readmeID := generateEntryID(slice.ID, "docs/readme.md")
	if err := st.UpdateEntry(ctx, &models.DirectoryEntry{
		ID:       readmeID,
		Path:     "docs/readme.md",
		Type:     "file",
		ParentID: slice.ID,
		Size:     5,
	}); err != nil {
		t.Fatalf("UpdateEntry(readme) failed: %v", err)
	}
	setupID := generateEntryID(slice.ID, "docs/guides/setup.md")
	if err := st.UpdateEntry(ctx, &models.DirectoryEntry{
		ID:       setupID,
		Path:     "docs/guides/setup.md",
		Type:     "file",
		ParentID: slice.ID,
		Size:     7,
	}); err != nil {
		t.Fatalf("UpdateEntry(setup) failed: %v", err)
	}

	docsEntry, err := st.GetEntryByPath(ctx, slice.ID, "docs")
	if err != nil {
		t.Fatalf("GetEntryByPath(docs) failed: %v", err)
	}
	if got, want := docsEntry.Size, int64(12); got != want {
		t.Fatalf("docs size = %d, want %d", got, want)
	}
	guidesEntry, err := st.GetEntryByPath(ctx, slice.ID, "docs/guides")
	if err != nil {
		t.Fatalf("GetEntryByPath(docs/guides) failed: %v", err)
	}
	if got, want := guidesEntry.Size, int64(7); got != want {
		t.Fatalf("docs/guides size = %d, want %d", got, want)
	}
	rootEntry, err := st.GetEntryByPath(ctx, slice.ID, "")
	if err != nil {
		t.Fatalf("GetEntryByPath(root) failed: %v", err)
	}
	if got, want := rootEntry.Size, int64(12); got != want {
		t.Fatalf("root size = %d, want %d", got, want)
	}

	if err := st.DeleteEntry(ctx, readmeID); err != nil {
		t.Fatalf("DeleteEntry(readme) failed: %v", err)
	}
	docsEntry, err = st.GetEntryByPath(ctx, slice.ID, "docs")
	if err != nil {
		t.Fatalf("GetEntryByPath(docs after delete) failed: %v", err)
	}
	if got, want := docsEntry.Size, int64(7); got != want {
		t.Fatalf("docs size after delete = %d, want %d", got, want)
	}

	if err := st.RebuildIndexes(ctx); err != nil {
		t.Fatalf("RebuildIndexes failed: %v", err)
	}
	docsEntry, err = st.GetEntryByPath(ctx, slice.ID, "docs")
	if err != nil {
		t.Fatalf("GetEntryByPath(docs after rebuild) failed: %v", err)
	}
	if got, want := docsEntry.Size, int64(7); got != want {
		t.Fatalf("docs size after rebuild = %d, want %d", got, want)
	}
}

func runSliceVisibilityStorageRoundTrip(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	slice := &models.Slice{
		ID:        "slice-visibility",
		Name:      "slice-visibility",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	stored, err := st.GetSlice(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSlice failed: %v", err)
	}
	if got, want := stored.Visibility, models.VisibilityPrivate; got != want {
		t.Fatalf("default visibility = %q, want %q", got, want)
	}

	if err := st.UpdateSliceVisibility(ctx, slice.ID, models.VisibilityPublic); err != nil {
		t.Fatalf("UpdateSliceVisibility failed: %v", err)
	}

	stored, err = st.GetSlice(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSlice(after update) failed: %v", err)
	}
	if got, want := stored.Visibility, models.VisibilityPublic; got != want {
		t.Fatalf("updated visibility = %q, want %q", got, want)
	}
}

func runPathVisibilityStorageRoundTrip(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	if _, err := st.GetPathVisibilityRule(ctx, "/alice/docs"); err != ErrEntryNotFound {
		t.Fatalf("GetPathVisibilityRule(missing) = %v, want %v", err, ErrEntryNotFound)
	}

	dirRule := &models.PathVisibilityRule{
		Path:       "/alice/docs",
		EntryType:  models.PathVisibilityEntryTypeDirectory,
		Visibility: models.VisibilityPublic,
		UpdatedBy:  "alice",
	}
	if err := st.UpsertPathVisibilityRule(ctx, dirRule); err != nil {
		t.Fatalf("UpsertPathVisibilityRule(dir) failed: %v", err)
	}

	fileRule := &models.PathVisibilityRule{
		Path:       "/alice/docs/README.md",
		EntryType:  models.PathVisibilityEntryTypeFile,
		Visibility: models.VisibilityPrivate,
		UpdatedBy:  "alice",
	}
	if err := st.UpsertPathVisibilityRule(ctx, fileRule); err != nil {
		t.Fatalf("UpsertPathVisibilityRule(file) failed: %v", err)
	}

	gotDir, err := st.GetPathVisibilityRule(ctx, "/alice/docs")
	if err != nil {
		t.Fatalf("GetPathVisibilityRule(dir) failed: %v", err)
	}
	if got, want := gotDir.Visibility, models.VisibilityPublic; got != want {
		t.Fatalf("dir visibility = %q, want %q", got, want)
	}

	gotFile, err := st.GetPathVisibilityRule(ctx, "/alice/docs/README.md")
	if err != nil {
		t.Fatalf("GetPathVisibilityRule(file) failed: %v", err)
	}
	if got, want := gotFile.Visibility, models.VisibilityPrivate; got != want {
		t.Fatalf("file visibility = %q, want %q", got, want)
	}

	rules, err := st.ListPathVisibilityRules(ctx, "/alice/docs")
	if err != nil {
		t.Fatalf("ListPathVisibilityRules failed: %v", err)
	}
	if got, want := len(rules), 2; got != want {
		t.Fatalf("ListPathVisibilityRules len = %d, want %d", got, want)
	}
	if rules[0].Path != "/alice/docs" || rules[1].Path != "/alice/docs/README.md" {
		t.Fatalf("unexpected rule order: %#v", rules)
	}

	if err := st.DeletePathVisibilityRule(ctx, "/alice/docs/README.md"); err != nil {
		t.Fatalf("DeletePathVisibilityRule failed: %v", err)
	}
	if _, err := st.GetPathVisibilityRule(ctx, "/alice/docs/README.md"); err != ErrEntryNotFound {
		t.Fatalf("GetPathVisibilityRule(after delete) = %v, want %v", err, ErrEntryNotFound)
	}
}

func runBuildSliceSearchArtifactFromCommitSnapshot(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	sliceID := "slice-search-" + suffix
	commitHash := "commit-search-" + suffix
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Search",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	readmeManifest, err := WriteSliceFileManifest(ctx, st, sliceID, "docs/readme.md", []byte("alpha beta gamma\n"))
	if err != nil {
		t.Fatalf("WriteSliceFileManifest readme failed: %v", err)
	}
	execManifest, err := WriteSliceFileManifestWithMetadata(ctx, st, sliceID, "bin/run.sh", []byte("#!/bin/sh\necho hi\n"), true, "")
	if err != nil {
		t.Fatalf("WriteSliceFileManifestWithMetadata executable failed: %v", err)
	}
	duplicateExecManifest, err := WriteSliceFileManifestWithMetadata(ctx, st, sliceID, "bin/run-copy.sh", []byte("#!/bin/sh\necho hi\n"), false, "")
	if err != nil {
		t.Fatalf("WriteSliceFileManifestWithMetadata duplicate failed: %v", err)
	}
	binaryManifest, err := WriteSliceFileManifest(ctx, st, sliceID, "bin/data.bin", []byte{0x00, 0x01, 0x02})
	if err != nil {
		t.Fatalf("WriteSliceFileManifest binary failed: %v", err)
	}
	symlinkManifest, err := WriteSliceFileManifestWithMetadata(ctx, st, sliceID, "link.txt", []byte("docs/readme.md"), false, "docs/readme.md")
	if err != nil {
		t.Fatalf("WriteSliceFileManifestWithMetadata symlink failed: %v", err)
	}

	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    sliceID,
		Timestamp:  time.Now(),
		Files: map[string]string{
			"docs/readme.md":  readmeManifest.Hash,
			"bin/run.sh":      execManifest.Hash,
			"bin/run-copy.sh": duplicateExecManifest.Hash,
			"bin/data.bin":    binaryManifest.Hash,
			"link.txt":        symlinkManifest.Hash,
		},
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}

	artifact, err := BuildAndStoreSliceSearchArtifact(ctx, st, sliceID, commitHash)
	if err != nil {
		t.Fatalf("BuildAndStoreSliceSearchArtifact failed: %v", err)
	}
	if artifact.Version != searchindex.CurrentArtifactVersion {
		t.Fatalf("expected artifact version %d, got %d", searchindex.CurrentArtifactVersion, artifact.Version)
	}
	if got := len(artifact.Files); got != 3 {
		t.Fatalf("expected 3 indexed text files, got %d", got)
	}

	searchHashesByPath := make(map[string]string, len(artifact.Files))
	for _, file := range artifact.Files {
		searchHashesByPath[file.Path] = file.SearchContentHash
	}
	if searchHashesByPath["bin/run.sh"] != searchHashesByPath["bin/run-copy.sh"] {
		t.Fatalf("expected identical content paths to share search hash, got %#v", searchHashesByPath)
	}
	if _, ok := searchHashesByPath["bin/data.bin"]; ok {
		t.Fatalf("expected binary file to be skipped, got %#v", searchHashesByPath)
	}
	if _, ok := searchHashesByPath["link.txt"]; ok {
		t.Fatalf("expected symlink file to be skipped, got %#v", searchHashesByPath)
	}

	payload, err := st.GetSliceSearchArtifact(ctx, sliceID, commitHash, searchindex.CurrentArtifactVersion)
	if err != nil {
		t.Fatalf("GetSliceSearchArtifact failed: %v", err)
	}
	decodedArtifact, err := searchindex.DecodeSliceArtifact(payload)
	if err != nil {
		t.Fatalf("DecodeSliceArtifact failed: %v", err)
	}
	if got := len(decodedArtifact.Files); got != 3 {
		t.Fatalf("expected 3 decoded files, got %d", got)
	}

	runHash := searchindex.SearchContentHash([]byte("#!/bin/sh\necho hi\n"))
	blobPayload, err := st.GetSearchIndexFileBlob(ctx, searchindex.CurrentBlobVersion, runHash)
	if err != nil {
		t.Fatalf("GetSearchIndexFileBlob duplicate content failed: %v", err)
	}
	blob, err := searchindex.DecodeFileBlob(blobPayload)
	if err != nil {
		t.Fatalf("DecodeFileBlob failed: %v", err)
	}
	if blob.SearchContentHash != runHash {
		t.Fatalf("expected blob search hash %q, got %q", runHash, blob.SearchContentHash)
	}
	if _, err := st.GetSearchIndexFileBlob(ctx, searchindex.CurrentBlobVersion, searchindex.SearchContentHash([]byte{0x00, 0x01, 0x02})); err != ErrEntryNotFound {
		t.Fatalf("expected binary content to be skipped, got %v", err)
	}
}

func TestLoadOrBuildSliceSearchArtifactRepairsCorruptPayload(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.factory(t)
			sliceID := fmt.Sprintf("slice-search-repair-%d", time.Now().UnixNano())
			commitHash := fmt.Sprintf("commit-search-repair-%d", time.Now().UnixNano())
			if err := st.CreateSlice(ctx, &models.Slice{ID: sliceID, Name: sliceID, Owners: []string{"tester"}, CreatedBy: "tester"}); err != nil {
				t.Fatalf("CreateSlice failed: %v", err)
			}
			manifest, err := WriteSliceFileManifest(ctx, st, sliceID, "docs/readme.md", []byte("hello repair\n"))
			if err != nil {
				t.Fatalf("WriteSliceFileManifest failed: %v", err)
			}
			if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
				CommitHash: commitHash,
				SliceID:    sliceID,
				Timestamp:  time.Now(),
				Files: map[string]string{
					"docs/readme.md": manifest.Hash,
				},
			}); err != nil {
				t.Fatalf("SaveCommitSnapshot failed: %v", err)
			}
			if err := st.PutSliceSearchArtifact(ctx, sliceID, commitHash, searchindex.CurrentArtifactVersion, []byte("corrupt")); err != nil {
				t.Fatalf("PutSliceSearchArtifact(corrupt) failed: %v", err)
			}

			artifact, outcome, err := LoadOrBuildSliceSearchArtifact(ctx, st, sliceID, commitHash)
			if err != nil {
				t.Fatalf("LoadOrBuildSliceSearchArtifact failed: %v", err)
			}
			if outcome != SearchArtifactOutcomeRebuilt {
				t.Fatalf("expected rebuilt outcome, got %q", outcome)
			}
			if got := len(artifact.Files); got != 1 {
				t.Fatalf("expected 1 indexed file, got %d", got)
			}
			payload, err := st.GetSliceSearchArtifact(ctx, sliceID, commitHash, searchindex.CurrentArtifactVersion)
			if err != nil {
				t.Fatalf("GetSliceSearchArtifact failed: %v", err)
			}
			if _, err := searchindex.DecodeSliceArtifact(payload); err != nil {
				t.Fatalf("DecodeSliceArtifact after repair failed: %v", err)
			}
		})
	}
}

func TestLoadOrBuildWorkspaceSearchArtifactRepairsCorruptPayload(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storageTestCases(ctx) {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.factory(t)
			workspaceID := fmt.Sprintf("workspace-search-repair-%d", time.Now().UnixNano())
			commitHash := fmt.Sprintf("commit-workspace-repair-%d", time.Now().UnixNano())
			if err := st.CreateSlice(ctx, &models.Slice{ID: workspaceID, Name: workspaceID, Owners: []string{"tester"}, CreatedBy: "tester"}); err != nil {
				t.Fatalf("CreateSlice failed: %v", err)
			}
			manifest, err := WriteSliceFileManifest(ctx, st, workspaceID, "docs/readme.md", []byte("hello workspace repair\n"))
			if err != nil {
				t.Fatalf("WriteSliceFileManifest failed: %v", err)
			}
			if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
				CommitHash: commitHash,
				SliceID:    workspaceID,
				Timestamp:  time.Now(),
				Files: map[string]string{
					"docs/readme.md": manifest.Hash,
				},
			}); err != nil {
				t.Fatalf("SaveCommitSnapshot failed: %v", err)
			}
			meta, err := st.GetSliceMetadata(ctx, workspaceID)
			if err != nil {
				t.Fatalf("GetSliceMetadata failed: %v", err)
			}
			meta.HeadCommitHash = commitHash
			if err := st.UpdateSliceMetadata(ctx, workspaceID, meta); err != nil {
				t.Fatalf("UpdateSliceMetadata failed: %v", err)
			}
			if err := st.PutWorkspaceSearchArtifact(ctx, workspaceID, searchindex.CurrentArtifactVersion, []byte("corrupt")); err != nil {
				t.Fatalf("PutWorkspaceSearchArtifact(corrupt) failed: %v", err)
			}

			artifact, outcome, err := LoadOrBuildWorkspaceSearchArtifact(ctx, st, workspaceID, commitHash)
			if err != nil {
				t.Fatalf("LoadOrBuildWorkspaceSearchArtifact failed: %v", err)
			}
			if outcome != SearchArtifactOutcomeRebuilt {
				t.Fatalf("expected rebuilt outcome, got %q", outcome)
			}
			if got := len(artifact.Files); got != 1 {
				t.Fatalf("expected 1 indexed file, got %d", got)
			}
			payload, err := st.GetWorkspaceSearchArtifact(ctx, workspaceID, searchindex.CurrentArtifactVersion)
			if err != nil {
				t.Fatalf("GetWorkspaceSearchArtifact failed: %v", err)
			}
			if _, err := searchindex.DecodeSliceArtifact(payload); err != nil {
				t.Fatalf("DecodeSliceArtifact workspace after repair failed: %v", err)
			}
		})
	}
}

func runStorageContract(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	slice1ID := fmt.Sprintf("slice-1-%s", suffix)
	slice2ID := fmt.Sprintf("slice-2-%s", suffix)
	emptySliceID := fmt.Sprintf("slice-empty-%s", suffix)
	file1ID := fmt.Sprintf("file-1-%s", suffix)
	file2ID := fmt.Sprintf("file-2-%s", suffix)
	file9ID := fmt.Sprintf("file-9-%s", suffix)
	file10ID := fmt.Sprintf("file-10-%s", suffix)
	entry1ID := fmt.Sprintf("entry-1-%s", suffix)
	changeset1ID := fmt.Sprintf("chg_1-%s", suffix)
	commit1Hash := fmt.Sprintf("commit-1-%s", suffix)

	// Create primary slice
	slice := &models.Slice{ID: slice1ID, Name: "Alpha", Description: "First", Files: []string{file1ID}, Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	// Verify retrieval
	fetched, err := st.GetSlice(ctx, slice.ID)
	if err != nil || fetched.ID != slice.ID {
		t.Fatalf("GetSlice mismatch: %v", err)
	}

	// Metadata round trip
	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	if meta.HeadCommitHash != "cmt_init_"+strings.ReplaceAll(slice.ID, "-", "_") {
		t.Fatalf("unexpected initial head commit hash: %s", meta.HeadCommitHash)
	}
	meta.HeadCommitHash = commit1Hash
	meta.ModifiedFiles = []string{file1ID}
	meta.ModifiedFilesCount = 1
	if err := st.UpdateSliceMetadata(ctx, slice.ID, meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	// Commit history
	commit := &models.Commit{CommitHash: commit1Hash, ParentHash: "", Message: "init", Timestamp: time.Now()}
	if err := st.AddSliceCommit(ctx, slice.ID, commit); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}
	commits, err := st.ListSliceCommits(ctx, slice.ID, 10, "")
	if err != nil || len(commits) != 1 || commits[0].CommitHash != commit.CommitHash {
		t.Fatalf("ListSliceCommits mismatch: %v len=%d", err, len(commits))
	}
	limitSliceID := fmt.Sprintf("slice-limit-%s", suffix)
	limitSlice := &models.Slice{ID: limitSliceID, Name: "Limit", Description: "Limit", Files: []string{}, Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, limitSlice); err != nil {
		t.Fatalf("CreateSlice limit failed: %v", err)
	}
	for i := 0; i < 120; i++ {
		h := fmt.Sprintf("limit-commit-%03d-%s", i, suffix)
		if err := st.AddSliceCommit(ctx, limitSliceID, &models.Commit{CommitHash: h, ParentHash: "", Message: "m", Timestamp: time.Now()}); err != nil {
			t.Fatalf("AddSliceCommit limit failed at %d: %v", i, err)
		}
	}
	defaultLimited, err := st.ListSliceCommits(ctx, limitSliceID, 0, "")
	if err != nil {
		t.Fatalf("ListSliceCommits default limit failed: %v", err)
	}
	if len(defaultLimited) != 100 {
		t.Fatalf("expected default commit limit 100, got %d", len(defaultLimited))
	}

	// File indexing and conflicts
	if err := st.AddFileToSlice(ctx, file1ID, slice.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, file2ID, slice.ID); err != nil {
		t.Fatalf("AddFileToSlice new file failed: %v", err)
	}
	afterAdd, err := st.GetSlice(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSlice after AddFileToSlice failed: %v", err)
	}
	if len(afterAdd.Files) != 1 || afterAdd.Files[0] != file1ID {
		t.Fatalf("slice files should be immutable, got: %#v", afterAdd.Files)
	}
	slice2 := &models.Slice{ID: slice2ID, Name: "Beta", Description: "Second", Files: []string{file1ID}, Owners: []string{"bob"}, CreatedBy: "bob"}
	if err := st.CreateSlice(ctx, slice2); err != nil {
		t.Fatalf("CreateSlice second failed: %v", err)
	}
	count, err := st.CountSlices(ctx)
	if err != nil {
		t.Fatalf("CountSlices failed: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 slices, got %d", count)
	}
	if err := st.AddFileToSlice(ctx, file1ID, slice2.ID); err != nil {
		t.Fatalf("AddFileToSlice second failed: %v", err)
	}
	if err := st.RemoveFileFromSlice(ctx, "file-unknown", "slice-missing"); err != nil {
		t.Fatalf("RemoveFileFromSlice for missing slice should be no-op: %v", err)
	}

	conflicts, err := st.ListConflicts(ctx)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("ListConflicts unexpected: %v len=%d", err, len(conflicts))
	}
	if _, err := st.ResolveConflict(ctx, file1ID, "slice-unknown"); err != ErrInvalidInput {
		t.Fatalf("expected ErrInvalidInput for unknown preferred slice, got %v", err)
	}
	conflicts, err = st.ListConflicts(ctx)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("ListConflicts after invalid resolve unexpected: %v len=%d", err, len(conflicts))
	}
	resolved, err := st.ResolveConflict(ctx, file1ID, slice.ID)
	if err != nil {
		t.Fatalf("ResolveConflict failed: %v", err)
	}
	if len(resolved.ConflictingSlices) != 1 || resolved.ConflictingSlices[0] != slice.ID {
		t.Fatalf("ResolveConflict result mismatch: %+v", resolved)
	}

	// Locking
	if err := st.LockSliceAndFiles(ctx, slice.ID, []string{file1ID}); err != nil {
		t.Fatalf("LockSliceAndFiles failed: %v", err)
	}
	if err := st.LockSliceAndFiles(ctx, slice.ID, []string{file2ID}); err != ErrLockHeld {
		t.Fatalf("expected ErrLockHeld for concurrent lock on same slice, got %v", err)
	}
	if err := st.LockSliceAndFiles(ctx, slice2.ID, []string{file1ID}); err != ErrLockHeld {
		t.Fatalf("expected ErrLockHeld, got %v", err)
	}
	st.UnlockSliceAndFiles(ctx, slice.ID, []string{file1ID})
	if err := st.LockSliceAndFiles(ctx, slice2.ID, []string{file1ID}); err != nil {
		t.Fatalf("Lock after unlock failed: %v", err)
	}
	st.UnlockSliceAndFiles(ctx, slice2.ID, []string{file1ID})

	// Changesets
	cs := &models.Changeset{ID: changeset1ID, Hash: "h1", SliceID: slice.ID, ModifiedFiles: []string{file1ID}, Status: models.ChangesetStatusPending, Author: "alice", Message: "msg", CreatedAt: time.Now()}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	fetchedCS, err := st.GetChangeset(ctx, cs.ID)
	if err != nil || fetchedCS.ID != cs.ID {
		t.Fatalf("GetChangeset mismatch: %v", err)
	}
	pending := models.ChangesetStatusPending
	listed, err := st.ListChangesets(ctx, slice.ID, &pending, 5)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListChangesets unexpected: %v len=%d", err, len(listed))
	}
	cs.Status = models.ChangesetStatusMerged
	if err := st.UpdateChangeset(ctx, cs); err != nil {
		t.Fatalf("UpdateChangeset failed: %v", err)
	}
	snap1 := &models.ChangesetSnapshot{
		ID:             fmt.Sprintf("%s-snapshot-1", cs.ID),
		ChangesetID:    cs.ID,
		Version:        1,
		Hash:           "h1",
		BaseCommitHash: "base-1",
		ModifiedFiles:  []string{file1ID},
		Author:         "alice",
		Message:        "v1",
		CreatedAt:      time.Now().Add(-time.Minute),
	}
	if err := st.CreateChangesetSnapshot(ctx, snap1); err != nil {
		t.Fatalf("CreateChangesetSnapshot v1 failed: %v", err)
	}
	snap2 := &models.ChangesetSnapshot{
		ID:               fmt.Sprintf("%s-snapshot-2", cs.ID),
		ChangesetID:      cs.ID,
		Version:          2,
		Hash:             "h2",
		BaseCommitHash:   "base-2",
		ModifiedFiles:    []string{file2ID},
		BasePathVersions: map[string]int64{file2ID: 7},
		Author:           "alice",
		Message:          "v2",
		CreatedAt:        time.Now(),
	}
	if err := st.CreateChangesetSnapshot(ctx, snap2); err != nil {
		t.Fatalf("CreateChangesetSnapshot v2 failed: %v", err)
	}
	latestSnap, err := st.GetChangesetSnapshot(ctx, cs.ID, 0)
	if err != nil {
		t.Fatalf("GetChangesetSnapshot latest failed: %v", err)
	}
	if latestSnap.Version != 2 || latestSnap.Hash != "h2" {
		t.Fatalf("unexpected latest snapshot: %#v", latestSnap)
	}
	if latestSnap.BasePathVersions[file2ID] != 7 {
		t.Fatalf("expected latest snapshot base path version 7, got %#v", latestSnap.BasePathVersions)
	}
	version1Snap, err := st.GetChangesetSnapshot(ctx, cs.ID, 1)
	if err != nil {
		t.Fatalf("GetChangesetSnapshot v1 failed: %v", err)
	}
	if version1Snap.Version != 1 || version1Snap.Hash != "h1" {
		t.Fatalf("unexpected version 1 snapshot: %#v", version1Snap)
	}
	limitedSnaps, err := st.ListChangesetSnapshots(ctx, cs.ID, 1)
	if err != nil {
		t.Fatalf("ListChangesetSnapshots limit=1 failed: %v", err)
	}
	if len(limitedSnaps) != 1 || limitedSnaps[0].Version != 2 {
		t.Fatalf("expected latest snapshot in limited list, got %#v", limitedSnaps)
	}
	if limitedSnaps[0].BasePathVersions[file2ID] != 7 {
		t.Fatalf("expected listed snapshot base path version 7, got %#v", limitedSnaps[0].BasePathVersions)
	}
	allSnaps, err := st.ListChangesetSnapshots(ctx, cs.ID, 10)
	if err != nil {
		t.Fatalf("ListChangesetSnapshots failed: %v", err)
	}
	if len(allSnaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(allSnaps))
	}
	if _, err := st.GetChangesetSnapshot(ctx, cs.ID, 99); err != ErrChangesetNotFound {
		t.Fatalf("expected ErrChangesetNotFound for missing snapshot version, got %v", err)
	}

	emptySlice := &models.Slice{ID: emptySliceID, Name: "Empty", Description: "Empty", Files: []string{}, Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, emptySlice); err != nil {
		t.Fatalf("CreateSlice empty failed: %v", err)
	}
	if err := st.SetSliceFiles(ctx, emptySlice.ID, []string{file9ID}); err != nil {
		t.Fatalf("SetSliceFiles failed: %v", err)
	}
	emptyFetched, err := st.GetSlice(ctx, emptySlice.ID)
	if err != nil {
		t.Fatalf("GetSlice after SetSliceFiles failed: %v", err)
	}
	if len(emptyFetched.Files) != 1 || emptyFetched.Files[0] != file9ID {
		t.Fatalf("SetSliceFiles mismatch: %#v", emptyFetched.Files)
	}
	if err := st.SetSliceFiles(ctx, emptySlice.ID, []string{file10ID}); err != ErrSliceFilesImmutable {
		t.Fatalf("expected ErrSliceFilesImmutable, got %v", err)
	}

	// UpdateSliceName
	if err := st.UpdateSliceName(ctx, slice.ID, "Renamed"); err != nil {
		t.Fatalf("UpdateSliceName failed: %v", err)
	}
	renamedSlice, err := st.GetSlice(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSlice after rename failed: %v", err)
	}
	if renamedSlice.Name != "Renamed" {
		t.Fatalf("expected name %q, got %q", "Renamed", renamedSlice.Name)
	}
	if renamedSlice.Slug != "alpha" {
		t.Fatalf("expected local slug to remain stable, got %q", renamedSlice.Slug)
	}
	if err := st.UpdateSliceName(ctx, "nonexistent-slice-"+suffix, "X"); err != ErrSliceNotFound {
		t.Fatalf("expected ErrSliceNotFound, got %v", err)
	}

	// GetSliceByName
	found, err := st.GetSliceByName(ctx, "Renamed")
	if err != nil {
		t.Fatalf("GetSliceByName failed: %v", err)
	}
	if found.ID != slice.ID {
		t.Fatalf("GetSliceByName returned wrong slice: %s", found.ID)
	}
	_, err = st.GetSliceByName(ctx, "nonexistent-name-"+suffix)
	if err != ErrSliceNotFound {
		t.Fatalf("expected ErrSliceNotFound for unknown name, got %v", err)
	}

	foundBySlug, err := st.GetSliceBySlug(ctx, "alice/alpha")
	if err != nil {
		t.Fatalf("GetSliceBySlug failed: %v", err)
	}
	if foundBySlug.ID != slice.ID {
		t.Fatalf("GetSliceBySlug returned wrong slice: %s", foundBySlug.ID)
	}
	foundByOwnerSlug, err := st.GetSliceByOwnerAndSlug(ctx, "alice", "alpha")
	if err != nil {
		t.Fatalf("GetSliceByOwnerAndSlug failed: %v", err)
	}
	if foundByOwnerSlug.ID != slice.ID {
		t.Fatalf("GetSliceByOwnerAndSlug returned wrong slice: %s", foundByOwnerSlug.ID)
	}

	peerSlice := &models.Slice{
		ID:        "peer-slice-" + suffix,
		Name:      "Alpha",
		Files:     []string{},
		Owners:    []string{"bob"},
		CreatedBy: "bob",
	}
	if err := st.CreateSlice(ctx, peerSlice); err != nil {
		t.Fatalf("CreateSlice peer failed: %v", err)
	}
	if peerSlice.Slug != "alpha" {
		t.Fatalf("expected peer local slug %q, got %q", "alpha", peerSlice.Slug)
	}
	if _, err := st.GetSliceBySlug(ctx, "alpha"); err != ErrSliceNotFound {
		t.Fatalf("expected bare local slug lookup to be ambiguous, got %v", err)
	}
	foundPeer, err := st.GetSliceBySlug(ctx, "bob/alpha")
	if err != nil {
		t.Fatalf("GetSliceBySlug peer failed: %v", err)
	}
	if foundPeer.ID != peerSlice.ID {
		t.Fatalf("GetSliceBySlug peer returned wrong slice: %s", foundPeer.ID)
	}

	// Entries
	entry := &models.DirectoryEntry{ID: entry1ID, Path: fmt.Sprintf("app/%s/main.go", suffix), Type: "file", ParentID: slice.ID, Content: []byte("code"), Size: 4}
	if err := st.AddEntry(ctx, entry); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	gotEntry, err := st.GetEntry(ctx, entry.ID)
	if err != nil || gotEntry.Path != entry.Path {
		t.Fatalf("GetEntry mismatch: %v", err)
	}
	byPath, err := st.GetEntryByPath(ctx, slice.ID, entry.Path)
	if err != nil || byPath.ID != entry.ID {
		t.Fatalf("GetEntryByPath mismatch: %v", err)
	}
	entries, err := st.ListEntries(ctx, slice.ID, slice.ID)
	if err != nil {
		t.Fatalf("ListEntries root unexpected: %v", err)
	}
	var appDir *models.DirectoryEntry
	for _, e := range entries {
		if e != nil && e.Path == "app" && e.Type == "directory" {
			appDir = e
			break
		}
	}
	if appDir == nil {
		t.Fatalf("expected directory entry \"app\" at root, got %#v", entries)
	}
	l1, err := st.ListEntries(ctx, slice.ID, appDir.ID)
	if err != nil || len(l1) != 1 || l1[0].Type != "directory" || l1[0].Path != fmt.Sprintf("app/%s", suffix) {
		t.Fatalf("ListEntries app unexpected: %v got=%#v", err, l1)
	}
	l2, err := st.ListEntries(ctx, slice.ID, l1[0].ID)
	if err != nil || len(l2) != 1 || l2[0].Type != "file" || l2[0].Path != entry.Path || l2[0].ID != entry.ID {
		t.Fatalf("ListEntries app/<suffix> unexpected: %v got=%#v", err, l2)
	}
	entry.Size = 8
	if err := st.UpdateEntry(ctx, entry); err != nil {
		t.Fatalf("UpdateEntry failed: %v", err)
	}
	if err := st.DeleteEntry(ctx, entry.ID); err != nil {
		t.Fatalf("DeleteEntry failed: %v", err)
	}

	// Global state
	state := &models.GlobalState{GlobalCommitHash: "global-1-" + suffix, Timestamp: time.Now(), History: []*models.GlobalCommit{{CommitHash: "global-1-" + suffix, Timestamp: time.Now()}}}
	if err := st.UpdateGlobalState(ctx, state); err != nil {
		t.Fatalf("UpdateGlobalState failed: %v", err)
	}
	storedState, err := st.GetGlobalState(ctx)
	if err != nil || storedState.GlobalCommitHash != state.GlobalCommitHash {
		t.Fatalf("GetGlobalState mismatch: %v", err)
	}
	replaced := &models.GlobalState{GlobalCommitHash: "global-2-" + suffix, Timestamp: time.Now(), History: []*models.GlobalCommit{{CommitHash: "global-2-" + suffix, Timestamp: time.Now()}}}
	if err := st.UpdateGlobalState(ctx, replaced); err != nil {
		t.Fatalf("UpdateGlobalState replacement failed: %v", err)
	}
	replacedState, err := st.GetGlobalState(ctx)
	if err != nil {
		t.Fatalf("GetGlobalState replacement failed: %v", err)
	}
	if replacedState.GlobalCommitHash != ("global-2-"+suffix) || len(replacedState.History) != 1 || replacedState.History[0].CommitHash != ("global-2-"+suffix) {
		t.Fatalf("global state should be replaced, got: %#v", replacedState)
	}

	// Versioned content + snapshot lookup
	content := &models.FileContent{
		FileID:  "versioned-file-" + suffix,
		Path:    "src/versioned-" + suffix + ".go",
		Content: []byte("package main"),
		Size:    int64(len("package main")),
	}
	manifest := mustWriteSliceManifest(t, ctx, st, slice.ID, content.Path, content.Content)
	content.Hash = manifest.Hash
	snapshot := &models.CommitSnapshot{
		CommitHash: "commit-snapshot-1-" + suffix,
		SliceID:    slice.ID,
		Files: map[string]string{
			content.Path: content.Hash,
		},
		Timestamp: time.Now(),
	}
	if err := st.SaveCommitSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}
	versioned, err := st.GetFileAtCommit(ctx, snapshot.CommitHash, content.Path)
	if err != nil {
		t.Fatalf("GetFileAtCommit failed: %v", err)
	}
	if versioned.Hash != content.Hash {
		t.Fatalf("GetFileAtCommit hash mismatch: got %s want %s", versioned.Hash, content.Hash)
	}
	filesAtCommit, err := st.ListFilesAtCommit(ctx, snapshot.CommitHash, "src/")
	if err != nil {
		t.Fatalf("ListFilesAtCommit failed: %v", err)
	}
	if len(filesAtCommit) != 1 || filesAtCommit[0] != content.Path {
		t.Fatalf("ListFilesAtCommit mismatch: %#v", filesAtCommit)
	}

	// Root slice init
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}
	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "root-file", rootSlice.ID); err != nil {
		t.Fatalf("AddFileToSlice root failed: %v", err)
	}
	if err := st.RemoveFileFromSlice(ctx, "root-file", rootSlice.ID); err != nil {
		t.Fatalf("RemoveFileFromSlice root failed: %v", err)
	}
	rootAfterRemove, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice after remove failed: %v", err)
	}
	for _, fileID := range rootAfterRemove.Files {
		if fileID == "root-file" {
			t.Fatalf("root slice should not keep removed file, got %#v", rootAfterRemove.Files)
		}
	}

	// Environment registry CRUD
	envName := "node20-" + suffix
	createdAt := time.Now().Add(-time.Minute)
	env := &models.Environment{
		Name:              envName,
		DisplayName:       "Node.js 20",
		Provider:          "e2b",
		ProviderID:        "tmpl-node20-" + suffix,
		ProviderConfig:    map[string]string{"runtime_ws_path": "/ws"},
		Region:            "us-west-2",
		DefaultAgentType:  "codex",
		AllowedAgentTypes: []string{"codex", "claude"},
		CreatedBy:         "alice",
		CreatedAt:         createdAt,
	}
	if err := st.CreateEnvironment(ctx, env); err != nil {
		t.Fatalf("CreateEnvironment failed: %v", err)
	}
	if err := st.CreateEnvironment(ctx, env); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists on duplicate environment, got %v", err)
	}
	fetchedEnv, err := st.GetEnvironment(ctx, envName)
	if err != nil {
		t.Fatalf("GetEnvironment failed: %v", err)
	}
	if fetchedEnv.Name != envName || fetchedEnv.ProviderID != env.ProviderID {
		t.Fatalf("GetEnvironment mismatch: %#v", fetchedEnv)
	}
	if fetchedEnv.ProviderConfig["runtime_ws_path"] != "/ws" {
		t.Fatalf("GetEnvironment provider config mismatch: %#v", fetchedEnv.ProviderConfig)
	}
	if fetchedEnv.DefaultAgentType != "codex" || len(fetchedEnv.AllowedAgentTypes) != 2 {
		t.Fatalf("GetEnvironment agent policy mismatch: %#v", fetchedEnv)
	}
	envs, err := st.ListEnvironments(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListEnvironments failed: %v", err)
	}
	foundEnv := false
	for _, item := range envs {
		if item != nil && item.Name == envName {
			foundEnv = true
			break
		}
	}
	if !foundEnv {
		t.Fatalf("expected environment %q in list", envName)
	}
	fetchedEnv.DisplayName = "Node.js 20 LTS"
	fetchedEnv.ProviderID = "tmpl-node20-updated-" + suffix
	fetchedEnv.Provider = "cloudflare_containers"
	fetchedEnv.ProviderConfig = map[string]string{
		"worker_base_url": "https://edge.example.internal",
		"container_class": "sandbox",
		"instance_type":   "basic",
	}
	fetchedEnv.Region = "us-east-1"
	fetchedEnv.DefaultAgentType = "claude"
	fetchedEnv.AllowedAgentTypes = []string{"claude", "codex"}
	if err := st.UpdateEnvironment(ctx, fetchedEnv); err != nil {
		t.Fatalf("UpdateEnvironment failed: %v", err)
	}
	updatedEnv, err := st.GetEnvironment(ctx, envName)
	if err != nil {
		t.Fatalf("GetEnvironment after update failed: %v", err)
	}
	if updatedEnv.DisplayName != "Node.js 20 LTS" || updatedEnv.ProviderID != fetchedEnv.ProviderID || updatedEnv.Region != "us-east-1" {
		t.Fatalf("updated environment mismatch: %#v", updatedEnv)
	}
	if updatedEnv.Provider != "cloudflare_containers" || updatedEnv.ProviderConfig["worker_base_url"] == "" {
		t.Fatalf("updated environment provider config mismatch: %#v", updatedEnv)
	}
	if updatedEnv.DefaultAgentType != "claude" || len(updatedEnv.AllowedAgentTypes) != 2 {
		t.Fatalf("updated environment agent policy mismatch: %#v", updatedEnv)
	}
	if updatedEnv.CreatedAt.IsZero() || updatedEnv.UpdatedAt.IsZero() {
		t.Fatalf("environment timestamps should be populated: %#v", updatedEnv)
	}
	if _, err := st.GetEnvironment(ctx, "does-not-exist-"+suffix); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound for missing env, got %v", err)
	}
	if err := st.DeleteEnvironment(ctx, envName); err != nil {
		t.Fatalf("DeleteEnvironment failed: %v", err)
	}
	if err := st.DeleteEnvironment(ctx, envName); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound on deleting missing env, got %v", err)
	}
	if err := st.CreateEnvironment(ctx, &models.Environment{
		Name:       "bad-provider-" + suffix,
		Provider:   "unsupported-provider",
		ProviderID: "provider-id",
	}); err != ErrInvalidInput {
		t.Fatalf("expected ErrInvalidInput for unsupported provider, got %v", err)
	}
	localEnvName := "local-agents-" + suffix
	if err := st.CreateEnvironment(ctx, &models.Environment{
		Name:              localEnvName,
		DisplayName:       "Local Agents",
		Provider:          "local",
		DefaultAgentType:  "codex",
		AllowedAgentTypes: []string{"codex", "claude"},
		CreatedBy:         "alice",
	}); err != nil {
		t.Fatalf("CreateEnvironment local failed: %v", err)
	}
	localEnv, err := st.GetEnvironment(ctx, localEnvName)
	if err != nil {
		t.Fatalf("GetEnvironment local failed: %v", err)
	}
	if localEnv.Provider != "local" || localEnv.ProviderID != "" {
		t.Fatalf("unexpected local environment: %#v", localEnv)
	}

	// Account auth + session lifecycle
	accountUsername := "acct" + suffix[len(suffix)-6:]
	accountEmail := "acct+" + suffix + "@example.com"
	user := &models.User{
		Username:     accountUsername,
		Name:         "Account User",
		PrimaryEmail: accountEmail,
		PasswordHash: "hash-v1",
	}
	if err := st.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	if err := st.CreateUser(ctx, user); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists on duplicate user, got %v", err)
	}
	fetchedUser, err := st.GetUser(ctx, accountUsername)
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	if fetchedUser.PrimaryEmail != accountEmail || fetchedUser.Name != user.Name {
		t.Fatalf("fetched user mismatch: %#v", fetchedUser)
	}
	if fetchedUser.RootPath != "/"+accountUsername {
		t.Fatalf("unexpected user root path: %q", fetchedUser.RootPath)
	}
	byEmail, err := st.GetUserByEmail(ctx, accountEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail failed: %v", err)
	}
	if byEmail.Username != accountUsername {
		t.Fatalf("GetUserByEmail mismatch: %#v", byEmail)
	}
	if byEmail.RootPath != "/"+accountUsername {
		t.Fatalf("unexpected user root path from email lookup: %q", byEmail.RootPath)
	}
	ensuredUser, err := st.EnsureUser(ctx, accountUsername)
	if err != nil {
		t.Fatalf("EnsureUser existing user failed: %v", err)
	}
	if ensuredUser.RootPath != "/"+accountUsername {
		t.Fatalf("unexpected ensured user root path: %q", ensuredUser.RootPath)
	}
	fetchedUser.PasswordHash = "hash-v2"
	fetchedUser.Name = "Updated Name"
	if err := st.UpdateUser(ctx, fetchedUser); err != nil {
		t.Fatalf("UpdateUser failed: %v", err)
	}
	updatedUser, err := st.GetUser(ctx, accountUsername)
	if err != nil {
		t.Fatalf("GetUser after update failed: %v", err)
	}
	if updatedUser.PasswordHash != "hash-v2" || updatedUser.Name != "Updated Name" {
		t.Fatalf("updated user mismatch: %#v", updatedUser)
	}
	if updatedUser.RootPath != "/"+accountUsername {
		t.Fatalf("unexpected user root path after update: %q", updatedUser.RootPath)
	}
	users, err := st.ListUsers(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListUsers failed: %v", err)
	}
	if len(users) != 1 || users[0].Username != accountUsername {
		t.Fatalf("unexpected users from ListUsers after first create: %#v", users)
	}

	memberUsername := "member" + suffix[len(suffix)-6:]
	memberEmail := "member+" + suffix + "@example.com"
	if err := st.CreateUser(ctx, &models.User{
		Username:     memberUsername,
		Name:         "Org Member",
		PrimaryEmail: memberEmail,
		PasswordHash: "member-hash",
	}); err != nil {
		t.Fatalf("CreateUser org member failed: %v", err)
	}
	users, err = st.ListUsers(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListUsers after second create failed: %v", err)
	}
	if len(users) != 2 || users[0].Username != accountUsername || users[1].Username != memberUsername {
		t.Fatalf("unexpected users after second create: %#v", users)
	}

	orgSlug := "org" + suffix[len(suffix)-6:]
	org := &models.Organization{
		Slug:      orgSlug,
		Name:      "Org " + suffix,
		CreatedBy: accountUsername,
	}
	if err := st.CreateOrganization(ctx, org); err != nil {
		t.Fatalf("CreateOrganization failed: %v", err)
	}
	if err := st.CreateOrganization(ctx, org); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists on duplicate org, got %v", err)
	}
	createdOrg, err := st.GetOrganization(ctx, orgSlug)
	if err != nil {
		t.Fatalf("GetOrganization failed: %v", err)
	}
	if createdOrg.RootPath != "/"+orgSlug {
		t.Fatalf("unexpected org root path: %q", createdOrg.RootPath)
	}
	if createdOrg.CreatedBy != accountUsername {
		t.Fatalf("unexpected org owner: %q", createdOrg.CreatedBy)
	}
	if err := st.AddOrganizationMember(ctx, &models.OrganizationMember{
		OrgSlug:  orgSlug,
		Username: accountUsername,
		Role:     models.OrganizationRoleOwner,
	}); err != nil {
		t.Fatalf("AddOrganizationMember failed: %v", err)
	}
	userOrgs, err := st.ListOrganizationsForUser(ctx, accountUsername)
	if err != nil {
		t.Fatalf("ListOrganizationsForUser failed: %v", err)
	}
	if len(userOrgs) != 1 || userOrgs[0].Slug != orgSlug {
		t.Fatalf("unexpected organizations for user: %#v", userOrgs)
	}
	if userOrgs[0].RootPath != "/"+orgSlug {
		t.Fatalf("unexpected org root path from user listing: %q", userOrgs[0].RootPath)
	}
	if err := st.AddOrganizationMember(ctx, &models.OrganizationMember{
		OrgSlug:  orgSlug,
		Username: memberUsername,
		Role:     models.OrganizationRoleMember,
	}); err != nil {
		t.Fatalf("AddOrganizationMember second member failed: %v", err)
	}
	if _, err := st.GetOrganizationMember(ctx, orgSlug, memberUsername); err != nil {
		t.Fatalf("GetOrganizationMember failed: %v", err)
	}
	members, err := st.ListOrganizationMembers(ctx, orgSlug)
	if err != nil {
		t.Fatalf("ListOrganizationMembers failed: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 org members, got %d", len(members))
	}
	memberRecord, err := st.GetOrganizationMember(ctx, orgSlug, memberUsername)
	if err != nil {
		t.Fatalf("GetOrganizationMember second fetch failed: %v", err)
	}
	memberRecord.Role = models.OrganizationRoleAdmin
	if err := st.UpdateOrganizationMember(ctx, memberRecord); err != nil {
		t.Fatalf("UpdateOrganizationMember failed: %v", err)
	}
	memberRecord, err = st.GetOrganizationMember(ctx, orgSlug, memberUsername)
	if err != nil {
		t.Fatalf("GetOrganizationMember after update failed: %v", err)
	}
	if memberRecord.Role != models.OrganizationRoleAdmin {
		t.Fatalf("expected updated member role admin, got %q", memberRecord.Role)
	}

	inviteEmail := "invite+" + suffix + "@example.com"
	inviteID := "invite-" + suffix
	if err := st.CreateOrganizationInvite(ctx, &models.OrganizationInvite{
		InviteID:    inviteID,
		OrgSlug:     orgSlug,
		TargetEmail: inviteEmail,
		Role:        models.OrganizationRoleAdmin,
		Status:      models.OrganizationInvitePending,
		CreatedBy:   accountUsername,
	}); err != nil {
		t.Fatalf("CreateOrganizationInvite failed: %v", err)
	}
	if err := st.CreateOrganizationInvite(ctx, &models.OrganizationInvite{
		InviteID:    "invite-dup-" + suffix,
		OrgSlug:     orgSlug,
		TargetEmail: inviteEmail,
		Role:        models.OrganizationRoleMember,
		Status:      models.OrganizationInvitePending,
		CreatedBy:   accountUsername,
	}); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists for duplicate pending invite, got %v", err)
	}
	invite, err := st.GetOrganizationInvite(ctx, orgSlug, inviteID)
	if err != nil {
		t.Fatalf("GetOrganizationInvite failed: %v", err)
	}
	if invite.Status != models.OrganizationInvitePending {
		t.Fatalf("expected pending invite, got %q", invite.Status)
	}
	invite.Status = models.OrganizationInviteAccepted
	if err := st.UpdateOrganizationInvite(ctx, invite); err != nil {
		t.Fatalf("UpdateOrganizationInvite failed: %v", err)
	}
	invite, err = st.GetOrganizationInvite(ctx, orgSlug, inviteID)
	if err != nil {
		t.Fatalf("GetOrganizationInvite after update failed: %v", err)
	}
	if invite.Status != models.OrganizationInviteAccepted {
		t.Fatalf("expected accepted invite, got %q", invite.Status)
	}
	if err := st.CreateOrganizationInvite(ctx, &models.OrganizationInvite{
		InviteID:    "invite-new-" + suffix,
		OrgSlug:     orgSlug,
		TargetEmail: inviteEmail,
		Role:        models.OrganizationRoleMember,
		Status:      models.OrganizationInvitePending,
		CreatedBy:   accountUsername,
	}); err != nil {
		t.Fatalf("expected pending invite recreation after accept, got %v", err)
	}

	teamID := "team-" + suffix
	if err := st.CreateTeam(ctx, &models.Team{
		TeamID:    teamID,
		OrgSlug:   orgSlug,
		Name:      "Platform Team",
		CreatedBy: accountUsername,
	}); err != nil {
		t.Fatalf("CreateTeam failed: %v", err)
	}
	if err := st.CreateTeam(ctx, &models.Team{
		TeamID:    "team-dup-" + suffix,
		OrgSlug:   orgSlug,
		Name:      "Platform Team",
		CreatedBy: accountUsername,
	}); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists for duplicate team name, got %v", err)
	}
	team, err := st.GetTeam(ctx, teamID)
	if err != nil {
		t.Fatalf("GetTeam failed: %v", err)
	}
	if team.OrgSlug != orgSlug {
		t.Fatalf("unexpected team org slug: %#v", team)
	}
	teams, err := st.ListTeams(ctx, orgSlug)
	if err != nil {
		t.Fatalf("ListTeams failed: %v", err)
	}
	if len(teams) != 1 || teams[0].TeamID != teamID {
		t.Fatalf("unexpected teams list: %#v", teams)
	}
	team.Name = "Platform Team Updated"
	if err := st.UpdateTeam(ctx, team); err != nil {
		t.Fatalf("UpdateTeam failed: %v", err)
	}
	if err := st.AddTeamMember(ctx, &models.TeamMember{
		TeamID:   teamID,
		Username: memberUsername,
	}); err != nil {
		t.Fatalf("AddTeamMember failed: %v", err)
	}
	if err := st.AddTeamMember(ctx, &models.TeamMember{
		TeamID:   teamID,
		Username: memberUsername,
	}); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists for duplicate team member, got %v", err)
	}
	if err := st.DeleteTeamMember(ctx, orgSlug, teamID, memberUsername); err != nil {
		t.Fatalf("DeleteTeamMember failed: %v", err)
	}
	if err := st.DeleteTeamMember(ctx, orgSlug, teamID, memberUsername); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound for missing team member, got %v", err)
	}

	if err := st.RemoveOrganizationMember(ctx, orgSlug, memberUsername); err != nil {
		t.Fatalf("RemoveOrganizationMember failed: %v", err)
	}
	if _, err := st.GetOrganizationMember(ctx, orgSlug, memberUsername); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound for removed member, got %v", err)
	}
	if err := st.AddTeamMember(ctx, &models.TeamMember{
		TeamID:   teamID,
		Username: memberUsername,
	}); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound when adding non-org member to team, got %v", err)
	}
	if err := st.DeleteTeam(ctx, orgSlug, teamID); err != nil {
		t.Fatalf("DeleteTeam failed: %v", err)
	}
	if _, err := st.GetTeam(ctx, teamID); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound for deleted team, got %v", err)
	}

	createdOrg.Name = "Org Updated " + suffix
	if err := st.UpdateOrganization(ctx, createdOrg); err != nil {
		t.Fatalf("UpdateOrganization failed: %v", err)
	}
	updatedOrg, err := st.GetOrganization(ctx, orgSlug)
	if err != nil {
		t.Fatalf("GetOrganization after update failed: %v", err)
	}
	if updatedOrg.Name != createdOrg.Name {
		t.Fatalf("updated org mismatch: %#v", updatedOrg)
	}
	if updatedOrg.RootPath != "/"+orgSlug {
		t.Fatalf("unexpected org root path after update: %q", updatedOrg.RootPath)
	}

	if _, err := st.EnsureUser(ctx, orgSlug); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists when ensuring user with org slug, got %v", err)
	}
	if err := st.CreateUser(ctx, &models.User{
		Username:     orgSlug,
		Name:         "Collision User",
		PrimaryEmail: "collision+" + suffix + "@example.com",
		PasswordHash: "hash-collision",
	}); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists when creating user with org slug, got %v", err)
	}
	if err := st.CreateOrganization(ctx, &models.Organization{
		Slug:      accountUsername,
		Name:      "User Collision Org",
		CreatedBy: accountUsername,
	}); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists when creating org with user slug, got %v", err)
	}
	if err := st.DeleteOrganization(ctx, orgSlug); err != nil {
		t.Fatalf("DeleteOrganization failed: %v", err)
	}
	if _, err := st.GetOrganization(ctx, orgSlug); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound for deleted org, got %v", err)
	}
	if _, err := st.GetOrganizationInvite(ctx, orgSlug, inviteID); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound for deleted org invite, got %v", err)
	}
	userOrgsAfterDelete, err := st.ListOrganizationsForUser(ctx, accountUsername)
	if err != nil {
		t.Fatalf("ListOrganizationsForUser after delete failed: %v", err)
	}
	if len(userOrgsAfterDelete) != 0 {
		t.Fatalf("expected no org memberships after delete, got %#v", userOrgsAfterDelete)
	}
	if err := st.DeleteOrganization(ctx, orgSlug); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound on deleting missing org, got %v", err)
	}

	authSession := &models.AuthSession{
		SessionID:  "auth-sess-" + suffix,
		Username:   accountUsername,
		Token:      "auth-token-" + suffix,
		DeviceInfo: "tests",
	}
	if err := st.CreateAuthSession(ctx, authSession); err != nil {
		t.Fatalf("CreateAuthSession failed: %v", err)
	}
	if err := st.CreateAuthSession(ctx, authSession); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists on duplicate auth session, got %v", err)
	}
	sessionByToken, err := st.GetAuthSessionByToken(ctx, authSession.Token)
	if err != nil {
		t.Fatalf("GetAuthSessionByToken failed: %v", err)
	}
	if sessionByToken.SessionID != authSession.SessionID {
		t.Fatalf("GetAuthSessionByToken mismatch: %#v", sessionByToken)
	}
	touchedAt := time.Now().Add(2 * time.Minute)
	if err := st.TouchAuthSession(ctx, authSession.SessionID, touchedAt); err != nil {
		t.Fatalf("TouchAuthSession failed: %v", err)
	}
	sessionsByUser, err := st.ListAuthSessionsByUser(ctx, accountUsername)
	if err != nil {
		t.Fatalf("ListAuthSessionsByUser failed: %v", err)
	}
	if len(sessionsByUser) != 1 {
		t.Fatalf("expected one active auth session, got %d", len(sessionsByUser))
	}
	if err := st.RevokeAuthSession(ctx, accountUsername, authSession.SessionID); err != nil {
		t.Fatalf("RevokeAuthSession failed: %v", err)
	}
	if _, err := st.GetAuthSessionByToken(ctx, authSession.Token); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound for revoked token, got %v", err)
	}

	authSession2 := &models.AuthSession{
		SessionID:  "auth-sess2-" + suffix,
		Username:   accountUsername,
		Token:      "auth-token2-" + suffix,
		DeviceInfo: "tests",
	}
	if err := st.CreateAuthSession(ctx, authSession2); err != nil {
		t.Fatalf("CreateAuthSession second failed: %v", err)
	}
	if err := st.RevokeAuthSessionByToken(ctx, authSession2.Token); err != nil {
		t.Fatalf("RevokeAuthSessionByToken failed: %v", err)
	}
	if activeSessions, err := st.ListAuthSessionsByUser(ctx, accountUsername); err != nil {
		t.Fatalf("ListAuthSessionsByUser after revoke failed: %v", err)
	} else if len(activeSessions) != 0 {
		t.Fatalf("expected zero active sessions after revocation, got %d", len(activeSessions))
	}

	accessExpiresAt := time.Now().Add(15 * time.Minute)
	refreshExpiresAt := time.Now().Add(24 * time.Hour)
	authSession3 := &models.AuthSession{
		SessionID:             "auth-sess3-" + suffix,
		Username:              accountUsername,
		Token:                 "auth-token3-" + suffix,
		RefreshToken:          "refresh-token3-" + suffix,
		DeviceInfo:            "device flow",
		AccessTokenExpiresAt:  &accessExpiresAt,
		RefreshTokenExpiresAt: &refreshExpiresAt,
	}
	if err := st.CreateAuthSession(ctx, authSession3); err != nil {
		t.Fatalf("CreateAuthSession third failed: %v", err)
	}
	sessionByID, err := st.GetAuthSession(ctx, authSession3.SessionID)
	if err != nil {
		t.Fatalf("GetAuthSession failed: %v", err)
	}
	if sessionByID.RefreshToken != authSession3.RefreshToken {
		t.Fatalf("GetAuthSession refresh token mismatch: %#v", sessionByID)
	}
	sessionByRefreshToken, err := st.GetAuthSessionByRefreshToken(ctx, authSession3.RefreshToken)
	if err != nil {
		t.Fatalf("GetAuthSessionByRefreshToken failed: %v", err)
	}
	if sessionByRefreshToken.SessionID != authSession3.SessionID {
		t.Fatalf("GetAuthSessionByRefreshToken mismatch: %#v", sessionByRefreshToken)
	}
	rotatedAccessExpiresAt := time.Now().Add(20 * time.Minute)
	if err := st.UpdateAuthSessionTokens(ctx, authSession3.SessionID, "auth-token3-rotated-"+suffix, &rotatedAccessExpiresAt, authSession3.RefreshToken, authSession3.RefreshTokenExpiresAt); err != nil {
		t.Fatalf("UpdateAuthSessionTokens failed: %v", err)
	}
	if _, err := st.GetAuthSessionByToken(ctx, authSession3.Token); err != ErrEntryNotFound {
		t.Fatalf("expected old access token to be invalid after rotation, got %v", err)
	}
	rotatedSession, err := st.GetAuthSessionByToken(ctx, "auth-token3-rotated-"+suffix)
	if err != nil {
		t.Fatalf("GetAuthSessionByToken rotated failed: %v", err)
	}
	if rotatedSession.SessionID != authSession3.SessionID {
		t.Fatalf("rotated access token session mismatch: %#v", rotatedSession)
	}
	expiredAccessAt := time.Now().Add(-1 * time.Minute)
	if err := st.UpdateAuthSessionTokens(ctx, authSession3.SessionID, "auth-token3-expired-"+suffix, &expiredAccessAt, authSession3.RefreshToken, authSession3.RefreshTokenExpiresAt); err != nil {
		t.Fatalf("UpdateAuthSessionTokens expired failed: %v", err)
	}
	if _, err := st.GetAuthSessionByToken(ctx, "auth-token3-expired-"+suffix); err != ErrEntryNotFound {
		t.Fatalf("expected expired access token lookup to fail, got %v", err)
	}

	agentKey := &models.AgentKey{
		KeyID:       "agent-key-" + suffix,
		Username:    accountUsername,
		Name:        "CI key",
		Algorithm:   "ed25519",
		PublicKey:   []byte("pubkey-" + suffix),
		Fingerprint: "fingerprint-" + suffix,
	}
	if err := st.CreateAgentKey(ctx, agentKey); err != nil {
		t.Fatalf("CreateAgentKey failed: %v", err)
	}
	if err := st.CreateAgentKey(ctx, agentKey); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists on duplicate agent key, got %v", err)
	}
	agentKeyByID, err := st.GetAgentKey(ctx, agentKey.KeyID)
	if err != nil {
		t.Fatalf("GetAgentKey failed: %v", err)
	}
	if agentKeyByID.Username != accountUsername || !bytes.Equal(agentKeyByID.PublicKey, agentKey.PublicKey) {
		t.Fatalf("agent key by ID mismatch: %#v", agentKeyByID)
	}
	agentKeyByFingerprint, err := st.GetAgentKeyByFingerprint(ctx, agentKey.Fingerprint)
	if err != nil {
		t.Fatalf("GetAgentKeyByFingerprint failed: %v", err)
	}
	if agentKeyByFingerprint.KeyID != agentKey.KeyID {
		t.Fatalf("agent key by fingerprint mismatch: %#v", agentKeyByFingerprint)
	}
	agentKeysByUser, err := st.ListAgentKeysByUser(ctx, accountUsername)
	if err != nil {
		t.Fatalf("ListAgentKeysByUser failed: %v", err)
	}
	if len(agentKeysByUser) != 1 || agentKeysByUser[0].KeyID != agentKey.KeyID {
		t.Fatalf("unexpected agent keys by user: %#v", agentKeysByUser)
	}
	lastUsedAt := time.Now().Add(3 * time.Minute).Truncate(time.Microsecond)
	if err := st.TouchAgentKey(ctx, agentKey.KeyID, lastUsedAt); err != nil {
		t.Fatalf("TouchAgentKey failed: %v", err)
	}
	touchedAgentKey, err := st.GetAgentKey(ctx, agentKey.KeyID)
	if err != nil {
		t.Fatalf("GetAgentKey after touch failed: %v", err)
	}
	if touchedAgentKey.LastUsedAt == nil || !touchedAgentKey.LastUsedAt.Equal(lastUsedAt) {
		t.Fatalf("expected touched last_used_at %v, got %#v", lastUsedAt, touchedAgentKey)
	}

	authSessionWithKey := &models.AuthSession{
		SessionID:             "auth-sess-key-" + suffix,
		Username:              accountUsername,
		AgentKeyID:            agentKey.KeyID,
		Token:                 "auth-token-key-" + suffix,
		RefreshToken:          "refresh-token-key-" + suffix,
		DeviceInfo:            "agent login",
		AccessTokenExpiresAt:  &accessExpiresAt,
		RefreshTokenExpiresAt: &refreshExpiresAt,
	}
	if err := st.CreateAuthSession(ctx, authSessionWithKey); err != nil {
		t.Fatalf("CreateAuthSession with agent key failed: %v", err)
	}
	sessionWithKey, err := st.GetAuthSession(ctx, authSessionWithKey.SessionID)
	if err != nil {
		t.Fatalf("GetAuthSession with agent key failed: %v", err)
	}
	if sessionWithKey.AgentKeyID != agentKey.KeyID {
		t.Fatalf("expected agent-key-attributed session, got %#v", sessionWithKey)
	}
	revokedSessions, err := st.RevokeAuthSessionsByAgentKey(ctx, accountUsername, agentKey.KeyID)
	if err != nil {
		t.Fatalf("RevokeAuthSessionsByAgentKey failed: %v", err)
	}
	if revokedSessions != 1 {
		t.Fatalf("expected 1 revoked session, got %d", revokedSessions)
	}
	if _, err := st.GetAuthSessionByToken(ctx, authSessionWithKey.Token); err != ErrEntryNotFound {
		t.Fatalf("expected revoked agent-key session token to be invalid, got %v", err)
	}
	if _, err := st.GetAuthSessionByRefreshToken(ctx, authSessionWithKey.RefreshToken); err != ErrEntryNotFound {
		t.Fatalf("expected revoked agent-key refresh token to be invalid, got %v", err)
	}

	agentChallenge := &models.AgentKeyChallenge{
		ChallengeID: "agent-challenge-" + suffix,
		AgentKeyID:  agentKey.KeyID,
		Username:    accountUsername,
		Challenge:   []byte("challenge-" + suffix),
		DeviceInfo:  "gs-cli",
		ExpiresAt:   time.Now().Add(2 * time.Minute),
	}
	if err := st.CreateAgentKeyChallenge(ctx, agentChallenge); err != nil {
		t.Fatalf("CreateAgentKeyChallenge failed: %v", err)
	}
	if err := st.CreateAgentKeyChallenge(ctx, agentChallenge); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists on duplicate agent key challenge, got %v", err)
	}
	challengeByID, err := st.GetAgentKeyChallenge(ctx, agentChallenge.ChallengeID)
	if err != nil {
		t.Fatalf("GetAgentKeyChallenge failed: %v", err)
	}
	if challengeByID.AgentKeyID != agentKey.KeyID || !bytes.Equal(challengeByID.Challenge, agentChallenge.Challenge) {
		t.Fatalf("agent key challenge mismatch: %#v", challengeByID)
	}
	usedAt := time.Now().Add(4 * time.Minute).Truncate(time.Microsecond)
	if err := st.MarkAgentKeyChallengeUsed(ctx, agentChallenge.ChallengeID, usedAt); err != nil {
		t.Fatalf("MarkAgentKeyChallengeUsed failed: %v", err)
	}
	if err := st.MarkAgentKeyChallengeUsed(ctx, agentChallenge.ChallengeID, usedAt.Add(time.Minute)); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists on replayed challenge, got %v", err)
	}
	usedChallenge, err := st.GetAgentKeyChallenge(ctx, agentChallenge.ChallengeID)
	if err != nil {
		t.Fatalf("GetAgentKeyChallenge after mark used failed: %v", err)
	}
	if usedChallenge.UsedAt == nil || !usedChallenge.UsedAt.Equal(usedAt) {
		t.Fatalf("expected used_at %v, got %#v", usedAt, usedChallenge)
	}
	revokedAt := time.Now().Add(5 * time.Minute).Truncate(time.Microsecond)
	if err := st.RevokeAgentKey(ctx, accountUsername, agentKey.KeyID, revokedAt); err != nil {
		t.Fatalf("RevokeAgentKey failed: %v", err)
	}
	revokedAgentKey, err := st.GetAgentKey(ctx, agentKey.KeyID)
	if err != nil {
		t.Fatalf("GetAgentKey after revoke failed: %v", err)
	}
	if revokedAgentKey.State != models.AgentKeyStateRevoked || revokedAgentKey.RevokedAt == nil || !revokedAgentKey.RevokedAt.Equal(revokedAt) {
		t.Fatalf("revoked agent key mismatch: %#v", revokedAgentKey)
	}

	deviceAuthorization := &models.DeviceAuthorization{
		DeviceCode: "device-code-" + suffix,
		UserCode:   "ABCD-1234",
		DeviceInfo: "cli",
		Status:     models.DeviceAuthorizationPending,
		ExpiresAt:  time.Now().Add(10 * time.Minute),
	}
	if err := st.CreateDeviceAuthorization(ctx, deviceAuthorization); err != nil {
		t.Fatalf("CreateDeviceAuthorization failed: %v", err)
	}
	if err := st.CreateDeviceAuthorization(ctx, deviceAuthorization); err != ErrEntryExists {
		t.Fatalf("expected ErrEntryExists on duplicate device authorization, got %v", err)
	}
	deviceByCode, err := st.GetDeviceAuthorizationByDeviceCode(ctx, deviceAuthorization.DeviceCode)
	if err != nil {
		t.Fatalf("GetDeviceAuthorizationByDeviceCode failed: %v", err)
	}
	if deviceByCode.UserCode != deviceAuthorization.UserCode {
		t.Fatalf("device authorization by code mismatch: %#v", deviceByCode)
	}
	deviceByUserCode, err := st.GetDeviceAuthorizationByUserCode(ctx, deviceAuthorization.UserCode)
	if err != nil {
		t.Fatalf("GetDeviceAuthorizationByUserCode failed: %v", err)
	}
	if deviceByUserCode.DeviceCode != deviceAuthorization.DeviceCode {
		t.Fatalf("device authorization by user code mismatch: %#v", deviceByUserCode)
	}
	approvedAt := time.Now()
	deviceAuthorization.Username = accountUsername
	deviceAuthorization.SessionID = authSession3.SessionID
	deviceAuthorization.Status = models.DeviceAuthorizationApproved
	deviceAuthorization.ApprovedAt = &approvedAt
	if err := st.UpdateDeviceAuthorization(ctx, deviceAuthorization); err != nil {
		t.Fatalf("UpdateDeviceAuthorization failed: %v", err)
	}
	updatedDeviceAuth, err := st.GetDeviceAuthorizationByDeviceCode(ctx, deviceAuthorization.DeviceCode)
	if err != nil {
		t.Fatalf("GetDeviceAuthorizationByDeviceCode after update failed: %v", err)
	}
	if updatedDeviceAuth.Username != accountUsername || updatedDeviceAuth.SessionID != authSession3.SessionID {
		t.Fatalf("updated device authorization mismatch: %#v", updatedDeviceAuth)
	}
	if err := st.DeleteUser(ctx, accountUsername); err != nil {
		t.Fatalf("DeleteUser failed: %v", err)
	}
	if _, err := st.GetUser(ctx, accountUsername); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound for deleted user, got %v", err)
	}
	if _, err := st.GetUserByEmail(ctx, accountEmail); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound for deleted user email, got %v", err)
	}

	// Agent session lifecycle + event persistence
	sessionID := fmt.Sprintf("sess-%s", suffix)
	session := &models.AgentSession{
		SessionID:        sessionID,
		SliceID:          slice.ID,
		EnvironmentName:  "node20",
		AgentType:        "claude",
		UserID:           "alice",
		State:            models.AgentSessionStateCreating,
		Provider:         "e2b",
		RuntimeProvider:  "e2b",
		RuntimeSessionID: "runtime-create",
		RuntimeStatus:    "creating",
		RuntimeErrorCode: "none",
		E2BTemplateID:    "tmpl-test",
		E2BRegion:        "us-west-2",
		IdleTimeoutSec:   1800,
		TTLSec:           14400,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	if err := st.CreateAgentSession(ctx, session); err != nil {
		t.Fatalf("CreateAgentSession failed: %v", err)
	}
	if err := st.CreateAgentSession(ctx, session); err != ErrAgentSessionConflict {
		t.Fatalf("expected ErrAgentSessionConflict on duplicate session, got %v", err)
	}
	active, err := st.GetActiveAgentSessionBySlice(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetActiveAgentSessionBySlice failed: %v", err)
	}
	if active.SessionID != session.SessionID {
		t.Fatalf("active session mismatch: got %s want %s", active.SessionID, session.SessionID)
	}
	if active.EnvironmentName != session.EnvironmentName {
		t.Fatalf("active session environment mismatch: got %s want %s", active.EnvironmentName, session.EnvironmentName)
	}
	if active.AgentType != session.AgentType {
		t.Fatalf("active session agentType mismatch: got %s want %s", active.AgentType, session.AgentType)
	}
	sliceSessions, err := st.ListAgentSessionsBySlice(ctx, slice.ID, 10)
	if err != nil {
		t.Fatalf("ListAgentSessionsBySlice failed: %v", err)
	}
	if len(sliceSessions) != 1 || sliceSessions[0].SessionID != session.SessionID {
		t.Fatalf("ListAgentSessionsBySlice mismatch: %#v", sliceSessions)
	}

	session.State = models.AgentSessionStateRunning
	session.RuntimeProvider = "e2b"
	session.RuntimeSessionID = "sbx-" + suffix
	session.RuntimeStatus = "ready"
	session.RuntimeEndpoint = "wss://runtime.example/ws"
	nowRunning := time.Now()
	session.StartedAt = &nowRunning
	session.LastActivityAt = &nowRunning
	session.UpdatedAt = nowRunning
	if err := st.UpdateAgentSession(ctx, session); err != nil {
		t.Fatalf("UpdateAgentSession running failed: %v", err)
	}
	updatedSession, err := st.GetAgentSession(ctx, session.SessionID)
	if err != nil {
		t.Fatalf("GetAgentSession after update failed: %v", err)
	}
	if updatedSession.RuntimeSessionID != session.RuntimeSessionID || updatedSession.RuntimeProvider != session.RuntimeProvider || updatedSession.RuntimeStatus != session.RuntimeStatus {
		t.Fatalf("runtime metadata mismatch: %#v", updatedSession)
	}

	payload1 := []byte(`{"state":"running"}`)
	if err := st.AppendAgentSessionEvent(ctx, &models.AgentSessionEvent{
		SessionID: session.SessionID,
		Seq:       1,
		TS:        time.Now(),
		Stream:    "status",
		Type:      "state",
		Payload:   payload1,
	}); err != nil {
		t.Fatalf("AppendAgentSessionEvent seq1 failed: %v", err)
	}
	if err := st.AppendAgentSessionEvent(ctx, &models.AgentSessionEvent{
		SessionID: session.SessionID,
		Seq:       2,
		TS:        time.Now(),
		Stream:    "pty",
		Type:      "stdout",
		Payload:   []byte(`{"data":"ok\n"}`),
	}); err != nil {
		t.Fatalf("AppendAgentSessionEvent seq2 failed: %v", err)
	}
	if err := st.AppendAgentSessionEvent(ctx, &models.AgentSessionEvent{
		SessionID: session.SessionID,
		Seq:       2,
		TS:        time.Now(),
		Stream:    "pty",
		Type:      "stdout",
		Payload:   []byte(`{"data":"dup\n"}`),
	}); err != ErrAgentSessionConflict {
		t.Fatalf("expected ErrAgentSessionConflict on duplicate seq, got %v", err)
	}
	events, err := st.ListAgentSessionEvents(ctx, session.SessionID, 1, 10)
	if err != nil {
		t.Fatalf("ListAgentSessionEvents failed: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 2 {
		t.Fatalf("ListAgentSessionEvents mismatch: %#v", events)
	}
	if err := st.AddAgentSessionAudit(ctx, &models.AgentSessionAudit{
		SessionID:   session.SessionID,
		ActorUserID: session.UserID,
		Action:      "session_created",
		Metadata:    []byte(`{"source":"test"}`),
		CreatedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("AddAgentSessionAudit failed: %v", err)
	}

	nowStopped := time.Now()
	session.State = models.AgentSessionStateStopped
	session.StoppedAt = &nowStopped
	session.UpdatedAt = nowStopped
	if err := st.UpdateAgentSession(ctx, session); err != nil {
		t.Fatalf("UpdateAgentSession stopped failed: %v", err)
	}
	if _, err := st.GetActiveAgentSessionBySlice(ctx, session.SliceID); err != ErrAgentSessionNotFound {
		t.Fatalf("expected ErrAgentSessionNotFound for inactive slice session, got %v", err)
	}

	localSession := &models.AgentSession{
		SessionID:       fmt.Sprintf("sess-local-%s", suffix),
		SliceID:         slice.ID,
		EnvironmentName: "local-agents",
		AgentType:       "codex",
		UserID:          "alice",
		State:           models.AgentSessionStateCreating,
		Provider:        "local",
		IdleTimeoutSec:  1800,
		TTLSec:          14400,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := st.CreateAgentSession(ctx, localSession); err != nil {
		t.Fatalf("CreateAgentSession local provider failed: %v", err)
	}
	storedLocalSession, err := st.GetAgentSession(ctx, localSession.SessionID)
	if err != nil {
		t.Fatalf("GetAgentSession local provider failed: %v", err)
	}
	if storedLocalSession.Provider != "local" || storedLocalSession.E2BTemplateID != "" {
		t.Fatalf("local provider session mismatch: %#v", storedLocalSession)
	}
	localStopped := time.Now()
	localSession.State = models.AgentSessionStateStopped
	localSession.StoppedAt = &localStopped
	localSession.UpdatedAt = localStopped
	if err := st.UpdateAgentSession(ctx, localSession); err != nil {
		t.Fatalf("UpdateAgentSession local stopped failed: %v", err)
	}

	deleteSliceID := fmt.Sprintf("slice-delete-%s", suffix)
	deleteCommitHash := fmt.Sprintf("delete-commit-%s", suffix)
	deleteChangesetID := fmt.Sprintf("chg_delete_%s", suffix)
	deleteSessionID := fmt.Sprintf("delete-session-%s", suffix)
	deletePath := "cleanup/README.md"
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        deleteSliceID,
		Name:      "Delete Me",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
		Files:     []string{deletePath},
	}); err != nil {
		t.Fatalf("CreateSlice delete failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       fmt.Sprintf("%s:%s", deleteSliceID, deletePath),
		Path:     deletePath,
		Type:     "file",
		ParentID: deleteSliceID,
		Size:     7,
	}); err != nil {
		t.Fatalf("AddEntry delete failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, deleteSliceID, &models.Commit{
		CommitHash: deleteCommitHash,
		Message:    "delete setup",
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("AddSliceCommit delete failed: %v", err)
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: deleteCommitHash,
		SliceID:    deleteSliceID,
		Files:      map[string]string{deletePath: "cleanup-hash"},
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot delete failed: %v", err)
	}
	if err := st.CreateChangeset(ctx, &models.Changeset{
		ID:             deleteChangesetID,
		Hash:           "cleanup",
		SliceID:        deleteSliceID,
		BaseCommitHash: deleteCommitHash,
		ModifiedFiles:  []string{deletePath},
		Status:         models.ChangesetStatusPending,
	}); err != nil {
		t.Fatalf("CreateChangeset delete failed: %v", err)
	}
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:         fmt.Sprintf("delete-change-%s", suffix),
		SliceID:    deleteSliceID,
		CommitHash: deleteCommitHash,
		Path:       deletePath,
		ChangeType: models.ChangeTypeAdd,
		NewHash:    "cleanup-hash",
		Author:     "alice",
		Message:    "delete setup",
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("AddFileChange delete failed: %v", err)
	}
	deleteSession := &models.AgentSession{
		SessionID:        deleteSessionID,
		SliceID:          deleteSliceID,
		AgentType:        "cleanup",
		UserID:           "alice",
		State:            models.AgentSessionStateStopped,
		Provider:         "e2b",
		RuntimeProvider:  "e2b",
		RuntimeSessionID: "runtime-delete",
		RuntimeStatus:    "stopped",
		RuntimeErrorCode: "none",
		E2BTemplateID:    "template",
		IdleTimeoutSec:   60,
		TTLSec:           60,
	}
	if err := st.CreateAgentSession(ctx, deleteSession); err != nil {
		t.Fatalf("CreateAgentSession delete failed: %v", err)
	}
	if err := st.AppendAgentSessionEvent(ctx, &models.AgentSessionEvent{
		SessionID: deleteSessionID,
		Seq:       1,
		TS:        time.Now(),
		Stream:    "system",
		Type:      "state",
		Payload:   []byte(`{"state":"stopped"}`),
	}); err != nil {
		t.Fatalf("AppendAgentSessionEvent delete failed: %v", err)
	}
	if err := st.AddAgentSessionAudit(ctx, &models.AgentSessionAudit{
		SessionID:   deleteSessionID,
		ActorUserID: "alice",
		Action:      "cleanup",
		Metadata:    []byte(`{"source":"test"}`),
		CreatedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("AddAgentSessionAudit delete failed: %v", err)
	}
	if err := st.DeleteSlice(ctx, deleteSliceID); err != nil {
		t.Fatalf("DeleteSlice failed: %v", err)
	}
	if _, err := st.GetSlice(ctx, deleteSliceID); err != ErrSliceNotFound {
		t.Fatalf("expected ErrSliceNotFound after delete, got %v", err)
	}
	if _, err := st.GetSliceMetadata(ctx, deleteSliceID); err != ErrSliceNotFound {
		t.Fatalf("expected ErrSliceNotFound metadata after delete, got %v", err)
	}
	if _, err := st.GetEntryByPath(ctx, deleteSliceID, deletePath); err != ErrEntryNotFound {
		t.Fatalf("expected ErrEntryNotFound after delete, got %v", err)
	}
	if _, err := st.GetAgentSession(ctx, deleteSessionID); err != ErrAgentSessionNotFound {
		t.Fatalf("expected ErrAgentSessionNotFound after delete, got %v", err)
	}

	// Basic health
	if err := st.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	if err := st.PingMetadata(ctx); err != nil {
		t.Fatalf("PingMetadata failed: %v", err)
	}
}

func TestFileChangeHistory(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		factory func(t *testing.T) Storage
	}{
		{
			name: "in-memory",
			factory: func(t *testing.T) Storage {
				t.Helper()
				return NewInMemoryStorage()
			},
		},
	}
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		cases = append(cases, struct {
			name    string
			factory func(t *testing.T) Storage
		}{
			name: "postgres-native-history",
			factory: func(t *testing.T) Storage {
				t.Helper()
				st, err := NewPostgresNativeStorage(ctx, dsn, NewInMemoryObjectStore(), fmt.Sprintf("test-native-history-%d", time.Now().UnixNano()))
				if err != nil {
					t.Fatalf("NewPostgresNativeStorage failed: %v", err)
				}
				t.Cleanup(func() { _ = st.Close() })
				return st
			},
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFileChangeHistoryTests(ctx, t, tc.factory(t))
		})
	}
}

func runFileChangeHistoryTests(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	// Setup: Create a slice first
	slice := &models.Slice{ID: "slice-history-" + suffix, Name: "History Test", Description: "For testing file history"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	baseTime := time.Now().Add(-time.Hour)

	// Test 1: AddFileChange and GetFileHistory
	t.Run("AddFileChange", func(t *testing.T) {
		change1 := &models.FileChangeRecord{
			ID:         "change-1-" + suffix,
			SliceID:    slice.ID,
			CommitHash: "commit-abc-" + suffix,
			Path:       "src/main.go",
			ChangeType: models.ChangeTypeAdd,
			NewHash:    "hash123-" + suffix,
			LinesAdded: 50,
			Author:     "alice",
			Message:    "Initial commit",
			Timestamp:  baseTime,
		}
		if err := st.AddFileChange(ctx, change1); err != nil {
			t.Fatalf("AddFileChange failed: %v", err)
		}

		change2 := &models.FileChangeRecord{
			ID:           "change-2-" + suffix,
			SliceID:      slice.ID,
			CommitHash:   "commit-def-" + suffix,
			Path:         "src/main.go",
			ChangeType:   models.ChangeTypeModify,
			OldHash:      "hash123-" + suffix,
			NewHash:      "hash456-" + suffix,
			LinesAdded:   10,
			LinesDeleted: 5,
			Author:       "bob",
			Message:      "Fix bug",
			Timestamp:    baseTime.Add(10 * time.Minute),
		}
		if err := st.AddFileChange(ctx, change2); err != nil {
			t.Fatalf("AddFileChange second failed: %v", err)
		}

		// Verify GetFileHistory returns changes in order (newest first)
		history, err := st.GetFileHistory(ctx, slice.ID, "src/main.go", 10, "")
		if err != nil {
			t.Fatalf("GetFileHistory failed: %v", err)
		}
		if len(history) != 2 {
			t.Fatalf("expected 2 changes, got %d", len(history))
		}
		// Newest first
		if history[0].ID != ("change-2-" + suffix) {
			t.Errorf("expected newest change first, got %s", history[0].ID)
		}
		if history[1].ID != ("change-1-" + suffix) {
			t.Errorf("expected oldest change second, got %s", history[1].ID)
		}
	})

	// Test 2: AddFileChanges batch
	t.Run("AddFileChanges batch", func(t *testing.T) {
		commitGHI := "commit-ghi-" + suffix
		changes := []*models.FileChangeRecord{
			{
				ID:         "change-3-" + suffix,
				SliceID:    slice.ID,
				CommitHash: commitGHI,
				Path:       "src/utils/helper.go",
				ChangeType: models.ChangeTypeAdd,
				NewHash:    "hashutil1-" + suffix,
				Author:     "charlie",
				Message:    "Add helper",
				Timestamp:  baseTime.Add(20 * time.Minute),
			},
			{
				ID:         "change-4-" + suffix,
				SliceID:    slice.ID,
				CommitHash: commitGHI,
				Path:       "src/utils/config.go",
				ChangeType: models.ChangeTypeAdd,
				NewHash:    "hashutil2-" + suffix,
				Author:     "charlie",
				Message:    "Add helper",
				Timestamp:  baseTime.Add(20 * time.Minute),
			},
		}
		if err := st.AddFileChanges(ctx, changes); err != nil {
			t.Fatalf("AddFileChanges failed: %v", err)
		}

		// Verify both were added
		history1, _ := st.GetFileHistory(ctx, slice.ID, "src/utils/helper.go", 10, "")
		history2, _ := st.GetFileHistory(ctx, slice.ID, "src/utils/config.go", 10, "")
		if len(history1) != 1 || len(history2) != 1 {
			t.Errorf("expected 1 change each, got %d and %d", len(history1), len(history2))
		}
	})

	// Test 3: GetDirectoryHistory
	t.Run("GetDirectoryHistory", func(t *testing.T) {
		// Get all changes under src/utils/
		history, err := st.GetDirectoryHistory(ctx, slice.ID, "src/utils", 10, "")
		if err != nil {
			t.Fatalf("GetDirectoryHistory failed: %v", err)
		}
		if len(history) != 2 {
			t.Errorf("expected 2 changes under src/utils/, got %d", len(history))
		}

		// Get all changes under src/
		historyAll, err := st.GetDirectoryHistory(ctx, slice.ID, "src", 10, "")
		if err != nil {
			t.Fatalf("GetDirectoryHistory src/ failed: %v", err)
		}
		if len(historyAll) != 4 {
			t.Errorf("expected 4 changes under src/, got %d", len(historyAll))
		}
	})

	// Test 4: GetCommitChanges
	t.Run("GetCommitChanges", func(t *testing.T) {
		changes, err := st.GetCommitChanges(ctx, "commit-ghi-"+suffix)
		if err != nil {
			t.Fatalf("GetCommitChanges failed: %v", err)
		}
		if len(changes) != 2 {
			t.Errorf("expected 2 changes in commit-ghi, got %d", len(changes))
		}

		// Non-existent commit should return empty
		empty, err := st.GetCommitChanges(ctx, "nonexistent")
		if err != nil {
			t.Fatalf("GetCommitChanges nonexistent failed: %v", err)
		}
		if len(empty) != 0 {
			t.Errorf("expected 0 changes for nonexistent commit, got %d", len(empty))
		}
	})

	// Test 5: Pagination with fromCommit
	t.Run("Pagination", func(t *testing.T) {
		// Get first page
		page1, err := st.GetFileHistory(ctx, slice.ID, "src/main.go", 1, "")
		if err != nil {
			t.Fatalf("GetFileHistory page1 failed: %v", err)
		}
		if len(page1) != 1 {
			t.Fatalf("expected 1 change in page1, got %d", len(page1))
		}

		// Get second page using fromCommit
		page2, err := st.GetFileHistory(ctx, slice.ID, "src/main.go", 1, page1[0].CommitHash)
		if err != nil {
			t.Fatalf("GetFileHistory page2 failed: %v", err)
		}
		if len(page2) != 1 {
			t.Fatalf("expected 1 change in page2, got %d", len(page2))
		}
		if page2[0].ID == page1[0].ID {
			t.Error("page2 should have different change than page1")
		}
	})

	// Test 6: QueryFileHistory with filters
	t.Run("QueryFileHistory", func(t *testing.T) {
		// Query by author
		result, err := st.QueryFileHistory(ctx, &models.FileHistoryQuery{
			SliceID: slice.ID,
			Author:  "alice",
			Limit:   10,
		})
		if err != nil {
			t.Fatalf("QueryFileHistory by author failed: %v", err)
		}
		if len(result.Changes) != 1 {
			t.Errorf("expected 1 change by alice, got %d", len(result.Changes))
		}

		// Query by change type
		result2, err := st.QueryFileHistory(ctx, &models.FileHistoryQuery{
			SliceID:     slice.ID,
			ChangeTypes: []models.ChangeType{models.ChangeTypeAdd},
			Limit:       10,
		})
		if err != nil {
			t.Fatalf("QueryFileHistory by type failed: %v", err)
		}
		if len(result2.Changes) != 3 {
			t.Errorf("expected 3 add changes, got %d", len(result2.Changes))
		}

		// Query by path prefix
		result3, err := st.QueryFileHistory(ctx, &models.FileHistoryQuery{
			SliceID:    slice.ID,
			PathPrefix: "src/utils",
			Limit:      10,
		})
		if err != nil {
			t.Fatalf("QueryFileHistory by prefix failed: %v", err)
		}
		if len(result3.Changes) != 2 {
			t.Errorf("expected 2 changes under src/utils, got %d", len(result3.Changes))
		}

		// Query with time filter
		midTime := baseTime.Add(15 * time.Minute)
		result4, err := st.QueryFileHistory(ctx, &models.FileHistoryQuery{
			SliceID:       slice.ID,
			FromTimestamp: &midTime,
			Limit:         10,
		})
		if err != nil {
			t.Fatalf("QueryFileHistory by time failed: %v", err)
		}
		if len(result4.Changes) != 2 {
			t.Errorf("expected 2 changes after midTime, got %d", len(result4.Changes))
		}
	})

	// Test 7: GetDirectorySummary
	t.Run("GetDirectorySummary", func(t *testing.T) {
		summary, err := st.GetDirectorySummary(ctx, slice.ID, "src")
		if err != nil {
			t.Fatalf("GetDirectorySummary failed: %v", err)
		}
		if summary.TotalChanges != 4 {
			t.Errorf("expected 4 total changes, got %d", summary.TotalChanges)
		}
		if summary.FilesChanged != 3 {
			t.Errorf("expected 3 unique files, got %d", summary.FilesChanged)
		}
		if summary.LastChange == nil {
			t.Error("expected LastChange to be set")
		}
		if summary.ChangesByType[models.ChangeTypeAdd] != 3 {
			t.Errorf("expected 3 add changes, got %d", summary.ChangesByType[models.ChangeTypeAdd])
		}
		if summary.ChangesByType[models.ChangeTypeModify] != 1 {
			t.Errorf("expected 1 modify change, got %d", summary.ChangesByType[models.ChangeTypeModify])
		}
	})

	// Test 8: Empty results
	t.Run("EmptyResults", func(t *testing.T) {
		history, err := st.GetFileHistory(ctx, slice.ID, "nonexistent/path.go", 10, "")
		if err != nil {
			t.Fatalf("GetFileHistory nonexistent failed: %v", err)
		}
		if len(history) != 0 {
			t.Errorf("expected 0 changes for nonexistent path, got %d", len(history))
		}

		summary, err := st.GetDirectorySummary(ctx, slice.ID, "nonexistent")
		if err != nil {
			t.Fatalf("GetDirectorySummary nonexistent failed: %v", err)
		}
		if summary.TotalChanges != 0 {
			t.Errorf("expected 0 changes for nonexistent dir, got %d", summary.TotalChanges)
		}
	})

	// Test 9: Invalid input
	t.Run("InvalidInput", func(t *testing.T) {
		invalidChange := &models.FileChangeRecord{
			ID:   "", // Missing ID
			Path: "test.go",
		}
		if err := st.AddFileChange(ctx, invalidChange); err != ErrInvalidInput {
			t.Errorf("expected ErrInvalidInput, got %v", err)
		}

		invalidChange2 := &models.FileChangeRecord{
			ID:   "valid-id",
			Path: "", // Missing path
		}
		if err := st.AddFileChange(ctx, invalidChange2); err != ErrInvalidInput {
			t.Errorf("expected ErrInvalidInput for missing path, got %v", err)
		}
	})
}

func TestPostgresNativeStoragePersistsAcrossRestart(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run postgres persistence test")
	}

	ctx := context.Background()
	store := NewInMemoryObjectStore()
	namespace := fmt.Sprintf("restart-%d", time.Now().UnixNano())
	rs, err := NewPostgresNativeStorage(ctx, dsn, store, namespace)
	t.Cleanup(func() {
		_ = rs.Close()
	})
	if err != nil {
		t.Fatalf("NewPostgresNativeStorage failed: %v", err)
	}

	slice1 := &models.Slice{ID: "slice-1", Name: "Alpha", Files: []string{"file-1"}}
	slice2 := &models.Slice{ID: "slice-2", Name: "Beta", Files: []string{"file-1", "file-2"}}
	if err := rs.CreateSlice(ctx, slice1); err != nil {
		t.Fatalf("CreateSlice 1 failed: %v", err)
	}
	if err := rs.CreateSlice(ctx, slice2); err != nil {
		t.Fatalf("CreateSlice 2 failed: %v", err)
	}

	cs := &models.Changeset{ID: "chg_rebuild", Hash: "h", SliceID: slice1.ID, ModifiedFiles: []string{"file-1"}, Status: models.ChangesetStatusPending}
	if err := rs.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	entry := &models.DirectoryEntry{ID: "entry-1", Path: "app/main.go", Type: "file", ParentID: slice1.ID, Content: []byte("hi"), Size: 2}
	if err := rs.AddEntry(ctx, entry); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	manifest := mustWriteSliceManifest(t, ctx, rs, slice1.ID, entry.Path, []byte("hi"))
	if err := rs.UpdateGlobalState(ctx, &models.GlobalState{GlobalCommitHash: "gc1", Timestamp: time.Now()}); err != nil {
		t.Fatalf("UpdateGlobalState failed: %v", err)
	}
	if err := rs.AddFileToSlice(ctx, "file-3", slice1.ID); err != nil {
		t.Fatalf("AddFileToSlice file-3 failed: %v", err)
	}

	if err := rs.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	rs, err = NewPostgresNativeStorage(ctx, dsn, store, namespace)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}

	slices, err := rs.ListSlices(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListSlices failed: %v", err)
	}
	if len(slices) != 2 {
		t.Fatalf("expected 2 slices after rebuild, got %d", len(slices))
	}

	mapped, err := rs.GetActiveSlicesForFile(ctx, "file-1")
	if err != nil {
		t.Fatalf("GetActiveSlicesForFile failed: %v", err)
	}
	if len(mapped) != 2 {
		t.Fatalf("expected file-1 to map to 2 slices after rebuild, got %d", len(mapped))
	}
	mappedFile3, err := rs.GetActiveSlicesForFile(ctx, "file-3")
	if err != nil {
		t.Fatalf("GetActiveSlicesForFile file-3 failed: %v", err)
	}
	if len(mappedFile3) != 1 || mappedFile3[0] != slice1.ID {
		t.Fatalf("expected file-3 to map to %s after rebuild, got %#v", slice1.ID, mappedFile3)
	}

	restoredCS, err := rs.GetChangeset(ctx, cs.ID)
	if err != nil || restoredCS.ID != cs.ID {
		t.Fatalf("expected changeset restored after rebuild: %v", err)
	}
	restoredEntry, err := rs.GetEntry(ctx, entry.ID)
	if err != nil || restoredEntry.Path != entry.Path {
		t.Fatalf("expected entry restored after rebuild: %v", err)
	}
	restoredFile := mustReadSliceFile(t, ctx, rs, slice1.ID, entry.Path)
	if got := string(restoredFile.Content); got != "hi" {
		t.Fatalf("expected restored file content hi, got %q", got)
	}
	if restoredFile.Hash != manifest.Hash {
		t.Fatalf("expected restored file hash %q, got %q", manifest.Hash, restoredFile.Hash)
	}
	restoredState, err := rs.GetGlobalState(ctx)
	if err != nil || restoredState.GlobalCommitHash != "gc1" {
		t.Fatalf("expected global state restored, got %#v err=%v", restoredState, err)
	}
}
