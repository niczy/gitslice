package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/config"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	sliceservice "github.com/niczy/gitslice/services/slice"
)

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "backfill-native":
		cmdBackfillNative(os.Args[2:])
	case "backfill-search-index":
		cmdBackfillSearchIndex(os.Args[2:])
	case "backfill-history-projection":
		cmdBackfillHistoryProjection(os.Args[2:])
	case "repair-native-content":
		cmdRepairNativeContent(os.Args[2:])
	case "repair-search-index":
		cmdRepairSearchIndex(os.Args[2:])
	case "prune-broken-entries":
		cmdPruneBrokenEntries(os.Args[2:])
	case "rebuild-directory-sizes":
		cmdRebuildDirectorySizes(os.Args[2:])
	case "verify-native":
		cmdVerifyNative(os.Args[2:])
	case "drop-snapshot":
		cmdDropSnapshot(os.Args[2:])
	case "copy-object-store":
		cmdCopyObjectStore(os.Args[2:])
	case "verify-object-store":
		cmdVerifyObjectStore(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	log.Printf("Usage:")
	log.Printf("  storage_migrate backfill-native --dsn <dsn> --namespace <ns>")
	log.Printf("  storage_migrate backfill-search-index --dsn <dsn> --namespace <ns> [--slice <slice-id>] [--commits <n>] [--workspace-heads]")
	log.Printf("  storage_migrate backfill-history-projection --dsn <dsn> --namespace <ns> [--batch-size <n>] [--shards <n>] [--max-batches <n>]")
	log.Printf("  storage_migrate repair-native-content --dsn <dsn> --namespace <ns>")
	log.Printf("  storage_migrate repair-search-index --dsn <dsn> --namespace <ns> [--slice <slice-id>] [--commit <hash>] [--workspace <slice-id>] [--commits <n>]")
	log.Printf("  storage_migrate prune-broken-entries --dsn <dsn> --namespace <ns> [--dry-run]")
	log.Printf("  storage_migrate rebuild-directory-sizes --dsn <dsn> --namespace <ns>")
	log.Printf("  storage_migrate verify-native --dsn <dsn> --namespace <ns>")
	log.Printf("  storage_migrate drop-snapshot --dsn <dsn> --namespace <ns>")
	log.Printf("  storage_migrate copy-object-store --dsn <dsn> --namespace <ns> --target-env <env> --source-object-store-type <type> --target-object-store-type r2")
	log.Printf("  storage_migrate verify-object-store --dsn <dsn> --namespace <ns> --target-env <env> --source-object-store-type <type> --target-object-store-type r2")
}

func mustPool(ctx context.Context, dsn string) *pgxpool.Pool {
	if strings.TrimSpace(dsn) == "" {
		log.Fatalf("--dsn is required (or set POSTGRES_DSN)")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("pgxpool.New: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		log.Fatalf("ping: %v", err)
	}
	return pool
}

func readSnapshotPayload(ctx context.Context, pool *pgxpool.Pool, namespace string) ([]byte, error) {
	var payload []byte
	err := pool.QueryRow(ctx, `SELECT payload FROM public.storage_state WHERE namespace = $1`, namespace).Scan(&payload)
	return payload, err
}

func loadLegacySnapshot(ctx context.Context, pool *pgxpool.Pool, namespace string) (*storage.LegacyPostgresSnapshot, error) {
	raw, err := readSnapshotPayload(ctx, pool, namespace)
	if err != nil {
		return nil, err
	}
	var snap storage.LegacyPostgresSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

type backfillStats struct {
	Slices         int
	Entries        int
	CommitSnaps    int
	FileManifests  int
	Versioned      int
	Blocks         int
	FileChanges    int
	SliceCommits   int
	Changesets     int
	ChangesetSnaps int
	FileIndexRows  int
	Users          int
	Organizations  int
	OrgMemberships int
}

type repairStats struct {
	ManifestsBackfilled   int
	VersionedBackfilled   int
	BlocksWritten         int
	SliceEntrySizesFixed  int
	ParentEntrySizesFixed int
}

type pruneStats struct {
	Candidates           int
	BrokenFiles          int
	AffectedSlices       int
	FileIndexRowsDeleted int
	FileEntriesDeleted   int
	DirectoriesDeleted   int
	SliceRowsUpdated     int
	SliceMetadataUpdated int
}

type searchIndexStats struct {
	Slices         int
	SliceCommits   int
	WorkspaceHeads int
	Hits           int
	Built          int
	Rebuilt        int
}

type searchIndexRunOptions struct {
	SliceID              string
	CommitHash           string
	WorkspaceID          string
	CommitsPerSlice      int
	IncludeWorkspaceHead bool
	Force                bool
}

func cmdBackfillNative(args []string) {
	fs := flag.NewFlagSet("backfill-native", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("POSTGRES_DSN"), "Postgres DSN")
	namespace := fs.String("namespace", "core", "Snapshot namespace (storage_state.namespace)")
	drop := fs.Bool("drop-snapshot", false, "Delete the storage_state row after successful backfill")
	fs.Parse(args)

	ctx := context.Background()
	pool := mustPool(ctx, *dsn)
	defer pool.Close()

	log.Printf("Loading legacy snapshot namespace=%s", *namespace)
	snap, err := loadLegacySnapshot(ctx, pool, *namespace)
	if err != nil {
		log.Fatalf("load snapshot: %v", err)
	}

	log.Printf("Ensuring native schema is migrated")
	if err := storage.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("RunMigrations: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	objectStore, closeObjectStore, err := storage.BuildObjectStore(ctx, storage.ObjectStoreConfigFromAppConfig(cfg))
	if err != nil {
		log.Fatalf("build object store: %v", err)
	}
	defer closeObjectStore()

	stats, err := backfillNative(ctx, pool, objectStore, *namespace, snap)
	if err != nil {
		log.Fatalf("backfill: %v", err)
	}
	log.Printf("Backfill complete: slices=%d entries=%d commit_snapshots=%d manifests=%d versioned_manifests=%d blocks=%d file_changes=%d slice_commits=%d changesets=%d changeset_snapshots=%d file_index=%d users=%d orgs=%d memberships=%d",
		stats.Slices, stats.Entries, stats.CommitSnaps, stats.FileManifests, stats.Versioned, stats.Blocks, stats.FileChanges, stats.SliceCommits, stats.Changesets, stats.ChangesetSnaps, stats.FileIndexRows, stats.Users, stats.Organizations, stats.OrgMemberships)

	if *drop {
		if err := dropSnapshot(ctx, pool, *namespace); err != nil {
			log.Fatalf("drop snapshot: %v", err)
		}
		log.Printf("Dropped snapshot namespace=%s", *namespace)
	}
}

func cmdRepairNativeContent(args []string) {
	fs := flag.NewFlagSet("repair-native-content", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("POSTGRES_DSN"), "Postgres DSN")
	namespace := fs.String("namespace", "core", "Snapshot namespace (storage_state.namespace)")
	fs.Parse(args)

	ctx := context.Background()
	pool := mustPool(ctx, *dsn)
	defer pool.Close()

	log.Printf("Loading legacy snapshot namespace=%s", *namespace)
	snap, err := loadLegacySnapshot(ctx, pool, *namespace)
	if err != nil {
		log.Fatalf("load snapshot: %v", err)
	}

	log.Printf("Ensuring native schema is migrated")
	if err := storage.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("RunMigrations: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	objectStore, closeObjectStore, err := storage.BuildObjectStore(ctx, storage.ObjectStoreConfigFromAppConfig(cfg))
	if err != nil {
		log.Fatalf("build object store: %v", err)
	}
	defer closeObjectStore()

	stats, err := repairNativeContent(ctx, pool, objectStore, *namespace, snap)
	if err != nil {
		log.Fatalf("repair: %v", err)
	}
	log.Printf("Repair complete: manifests=%d versioned_manifests=%d blocks=%d slice_entry_sizes=%d parent_entry_sizes=%d",
		stats.ManifestsBackfilled, stats.VersionedBackfilled, stats.BlocksWritten, stats.SliceEntrySizesFixed, stats.ParentEntrySizesFixed)
}

func cmdBackfillSearchIndex(args []string) {
	fs := flag.NewFlagSet("backfill-search-index", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("POSTGRES_DSN"), "Postgres DSN")
	namespace := fs.String("namespace", "core", "Storage namespace")
	sliceID := fs.String("slice", "", "Optional slice ID to backfill")
	commits := fs.Int("commits", 20, "Recent commits per slice to backfill")
	includeWorkspaceHeads := fs.Bool("workspace-heads", true, "Also refresh current workspace search artifacts for non-root slices")
	fs.Parse(args)

	ctx := context.Background()
	native, closeNative := mustNativeStorage(ctx, *dsn, *namespace)
	defer native.Close()
	defer closeNative()

	stats, err := runSearchIndexMaintenance(ctx, native, searchIndexRunOptions{
		SliceID:              *sliceID,
		CommitsPerSlice:      *commits,
		IncludeWorkspaceHead: *includeWorkspaceHeads,
		Force:                false,
	})
	if err != nil {
		log.Fatalf("backfill search index: %v", err)
	}
	log.Printf("Search index backfill complete: slices=%d commits=%d workspace_heads=%d hits=%d built=%d rebuilt=%d",
		stats.Slices, stats.SliceCommits, stats.WorkspaceHeads, stats.Hits, stats.Built, stats.Rebuilt)
}

func cmdRepairSearchIndex(args []string) {
	fs := flag.NewFlagSet("repair-search-index", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("POSTGRES_DSN"), "Postgres DSN")
	namespace := fs.String("namespace", "core", "Storage namespace")
	sliceID := fs.String("slice", "", "Optional slice ID to repair")
	commitHash := fs.String("commit", "", "Optional commit hash to repair (requires --slice)")
	workspaceID := fs.String("workspace", "", "Optional workspace ID to rebuild current workspace artifact for")
	commits := fs.Int("commits", 20, "Recent commits per slice to rebuild when --commit is not provided")
	fs.Parse(args)

	if strings.TrimSpace(*commitHash) != "" && strings.TrimSpace(*sliceID) == "" {
		log.Fatalf("--commit requires --slice")
	}

	ctx := context.Background()
	native, closeNative := mustNativeStorage(ctx, *dsn, *namespace)
	defer native.Close()
	defer closeNative()

	stats, err := runSearchIndexMaintenance(ctx, native, searchIndexRunOptions{
		SliceID:              *sliceID,
		CommitHash:           *commitHash,
		WorkspaceID:          *workspaceID,
		CommitsPerSlice:      *commits,
		IncludeWorkspaceHead: strings.TrimSpace(*workspaceID) == "",
		Force:                true,
	})
	if err != nil {
		log.Fatalf("repair search index: %v", err)
	}
	log.Printf("Search index repair complete: slices=%d commits=%d workspace_heads=%d hits=%d built=%d rebuilt=%d",
		stats.Slices, stats.SliceCommits, stats.WorkspaceHeads, stats.Hits, stats.Built, stats.Rebuilt)
}

func cmdPruneBrokenEntries(args []string) {
	fs := flag.NewFlagSet("prune-broken-entries", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("POSTGRES_DSN"), "Postgres DSN")
	namespace := fs.String("namespace", "core", "Storage namespace")
	dryRun := fs.Bool("dry-run", false, "Report broken entries without deleting them")
	fs.Parse(args)

	ctx := context.Background()
	pool := mustPool(ctx, *dsn)
	defer pool.Close()

	log.Printf("Ensuring native schema is migrated")
	if err := storage.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("RunMigrations: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	objectStore, closeObjectStore, err := storage.BuildObjectStore(ctx, storage.ObjectStoreConfigFromAppConfig(cfg))
	if err != nil {
		log.Fatalf("build object store: %v", err)
	}
	defer closeObjectStore()

	native, err := storage.NewPostgresNativeStorage(ctx, *dsn, objectStore, *namespace)
	if err != nil {
		log.Fatalf("new native storage: %v", err)
	}
	defer native.Close()

	stats, err := pruneBrokenEntries(ctx, pool, native, *dryRun)
	if err != nil {
		log.Fatalf("prune broken entries: %v", err)
	}
	log.Printf("Prune complete: candidates=%d broken_files=%d affected_slices=%d file_index_rows=%d file_entries=%d directories=%d slice_rows=%d slice_metadata_rows=%d dry_run=%t",
		stats.Candidates, stats.BrokenFiles, stats.AffectedSlices, stats.FileIndexRowsDeleted, stats.FileEntriesDeleted, stats.DirectoriesDeleted, stats.SliceRowsUpdated, stats.SliceMetadataUpdated, *dryRun)
}

func cmdRebuildDirectorySizes(args []string) {
	fs := flag.NewFlagSet("rebuild-directory-sizes", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("POSTGRES_DSN"), "Postgres DSN")
	namespace := fs.String("namespace", "core", "storage namespace")
	_ = fs.Parse(args)

	ctx := context.Background()
	native, err := storage.NewPostgresNativeStorage(ctx, *dsn, storage.NewInMemoryObjectStore(), *namespace)
	if err != nil {
		log.Fatalf("NewPostgresNativeStorage: %v", err)
	}
	defer native.Close()

	started := time.Now()
	if err := native.RebuildIndexes(ctx); err != nil {
		log.Fatalf("RebuildIndexes: %v", err)
	}
	log.Printf("Rebuilt directory sizes for namespace %s in %s", *namespace, time.Since(started))
}

func cmdVerifyNative(args []string) {
	fs := flag.NewFlagSet("verify-native", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("POSTGRES_DSN"), "Postgres DSN")
	namespace := fs.String("namespace", "core", "Snapshot namespace (storage_state.namespace)")
	fs.Parse(args)

	ctx := context.Background()
	pool := mustPool(ctx, *dsn)
	defer pool.Close()

	snap, err := loadLegacySnapshot(ctx, pool, *namespace)
	if err != nil {
		log.Fatalf("load snapshot: %v", err)
	}

	if err := verifyNative(ctx, pool, snap); err != nil {
		log.Fatalf("verify failed: %v", err)
	}
	log.Printf("Verify OK")
}

func cmdDropSnapshot(args []string) {
	fs := flag.NewFlagSet("drop-snapshot", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("POSTGRES_DSN"), "Postgres DSN")
	namespace := fs.String("namespace", "core", "Snapshot namespace (storage_state.namespace)")
	fs.Parse(args)

	ctx := context.Background()
	pool := mustPool(ctx, *dsn)
	defer pool.Close()

	if err := dropSnapshot(ctx, pool, *namespace); err != nil {
		log.Fatalf("drop snapshot: %v", err)
	}
	log.Printf("Dropped snapshot namespace=%s", *namespace)
}

func cmdBackfillHistoryProjection(args []string) {
	fs := flag.NewFlagSet("backfill-history-projection", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("POSTGRES_DSN"), "Postgres DSN")
	namespace := fs.String("namespace", "core", "Storage namespace")
	batchSize := fs.Int("batch-size", 512, "Projection batch size")
	shards := fs.Int("shards", 64, "Projection shard count")
	maxBatches := fs.Int("max-batches", 0, "Maximum batches to process; 0 means until caught up")
	fs.Parse(args)

	ctx := context.Background()
	native, closeObjectStore := mustNativeStorage(ctx, *dsn, *namespace)
	defer native.Close()
	defer closeObjectStore()

	svc := sliceservice.NewInternalServiceWithProjectionStorage(native, native)
	result, err := svc.BackfillHistoryProjection(ctx, sliceservice.DurableProjectionConfig{
		ShardCount: int32(*shards),
		BatchSize:  *batchSize,
	}, *maxBatches)
	if err != nil {
		log.Fatalf("backfill history projection: %v", err)
	}
	log.Printf("History projection backfill complete: batches=%d events=%d", result.Batches, result.Events)
}

func mustNativeStorage(ctx context.Context, dsn, namespace string) (*storage.PostgresNativeStorage, func()) {
	pool := mustPool(ctx, dsn)
	defer pool.Close()

	log.Printf("Ensuring native schema is migrated")
	if err := storage.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("RunMigrations: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	objectStore, closeObjectStore, err := storage.BuildObjectStore(ctx, storage.ObjectStoreConfigFromAppConfig(cfg))
	if err != nil {
		log.Fatalf("build object store: %v", err)
	}

	native, err := storage.NewPostgresNativeStorage(ctx, dsn, objectStore, namespace)
	if err != nil {
		closeObjectStore()
		log.Fatalf("new native storage: %v", err)
	}

	return native, closeObjectStore
}

func runSearchIndexMaintenance(ctx context.Context, st storage.Storage, opts searchIndexRunOptions) (*searchIndexStats, error) {
	stats := &searchIndexStats{}
	commitsPerSlice := opts.CommitsPerSlice
	if commitsPerSlice <= 0 {
		commitsPerSlice = 20
	}

	if strings.TrimSpace(opts.WorkspaceID) != "" {
		stats.WorkspaceHeads++
		outcome, err := ensureWorkspaceSearchArtifact(ctx, st, strings.TrimSpace(opts.WorkspaceID), strings.TrimSpace(opts.CommitHash), opts.Force)
		if err != nil {
			return nil, err
		}
		recordSearchIndexOutcome(stats, outcome)
		if strings.TrimSpace(opts.SliceID) == "" {
			return stats, nil
		}
	}

	slices, err := resolveSearchIndexSlices(ctx, st, strings.TrimSpace(opts.SliceID))
	if err != nil {
		return nil, err
	}
	for _, slice := range slices {
		if slice == nil {
			continue
		}
		stats.Slices++
		if strings.TrimSpace(opts.CommitHash) != "" {
			stats.SliceCommits++
			outcome, err := ensureSliceSearchArtifact(ctx, st, slice.ID, strings.TrimSpace(opts.CommitHash), opts.Force)
			if err != nil {
				return nil, err
			}
			recordSearchIndexOutcome(stats, outcome)
		} else {
			commits, err := st.ListSliceCommits(ctx, slice.ID, commitsPerSlice, "")
			if err != nil {
				return nil, err
			}
			for _, commit := range commits {
				if commit == nil || strings.TrimSpace(commit.CommitHash) == "" {
					continue
				}
				stats.SliceCommits++
				outcome, err := ensureSliceSearchArtifact(ctx, st, slice.ID, strings.TrimSpace(commit.CommitHash), opts.Force)
				if err != nil {
					return nil, err
				}
				recordSearchIndexOutcome(stats, outcome)
			}
		}

		if !opts.IncludeWorkspaceHead || slice.IsRoot {
			continue
		}
		meta, err := st.GetSliceMetadata(ctx, slice.ID)
		if err != nil {
			return nil, err
		}
		headCommit := strings.TrimSpace(meta.HeadCommitHash)
		if headCommit == "" {
			continue
		}
		stats.WorkspaceHeads++
		outcome, err := ensureWorkspaceSearchArtifact(ctx, st, slice.ID, headCommit, opts.Force)
		if err != nil {
			return nil, err
		}
		recordSearchIndexOutcome(stats, outcome)
	}
	return stats, nil
}

func resolveSearchIndexSlices(ctx context.Context, st storage.Storage, sliceID string) ([]*models.Slice, error) {
	if sliceID != "" {
		slice, err := st.GetSlice(ctx, sliceID)
		if err != nil {
			return nil, err
		}
		return []*models.Slice{slice}, nil
	}

	limit := 500
	offset := 0
	slices := make([]*models.Slice, 0)
	for {
		batch, err := st.ListSlices(ctx, limit, offset)
		if err != nil {
			return nil, err
		}
		slices = append(slices, batch...)
		if len(batch) < limit {
			break
		}
		offset += len(batch)
	}
	return slices, nil
}

func ensureSliceSearchArtifact(ctx context.Context, st storage.Storage, sliceID, commitHash string, force bool) (storage.SearchArtifactLoadOutcome, error) {
	if force {
		if _, err := storage.BuildAndStoreSliceSearchArtifact(ctx, st, sliceID, commitHash); err != nil {
			return "", err
		}
		return storage.SearchArtifactOutcomeRebuilt, nil
	}
	_, outcome, err := storage.LoadOrBuildSliceSearchArtifact(ctx, st, sliceID, commitHash)
	return outcome, err
}

func ensureWorkspaceSearchArtifact(ctx context.Context, st storage.Storage, workspaceID, commitHash string, force bool) (storage.SearchArtifactLoadOutcome, error) {
	if force {
		if _, err := storage.BuildAndStoreWorkspaceSearchArtifact(ctx, st, workspaceID, commitHash); err != nil {
			return "", err
		}
		return storage.SearchArtifactOutcomeRebuilt, nil
	}
	_, outcome, err := storage.LoadOrBuildWorkspaceSearchArtifact(ctx, st, workspaceID, commitHash)
	return outcome, err
}

func recordSearchIndexOutcome(stats *searchIndexStats, outcome storage.SearchArtifactLoadOutcome) {
	if stats == nil {
		return
	}
	switch outcome {
	case storage.SearchArtifactOutcomeHit:
		stats.Hits++
	case storage.SearchArtifactOutcomeBuilt:
		stats.Built++
	case storage.SearchArtifactOutcomeRebuilt:
		stats.Rebuilt++
	}
}

func dropSnapshot(ctx context.Context, pool *pgxpool.Pool, namespace string) error {
	tag, err := pool.Exec(ctx, `DELETE FROM public.storage_state WHERE namespace = $1`, namespace)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no snapshot row found for namespace %q", namespace)
	}
	return nil
}

type entryRow struct {
	id       string
	sliceID  string
	path     string
	typ      string
	parentID string
	size     int64
}

func entryID(sliceID, p string) string {
	return fmt.Sprintf("%s:%s", sliceID, p)
}

func ensureDirs(entries map[string]entryRow, sliceID, filePath string) {
	filePath = common.CleanRelativePath(filePath)
	if filePath == "" {
		return
	}
	dirPath := path.Dir(filePath)
	if dirPath == "." || dirPath == "/" {
		return
	}
	parts := strings.Split(dirPath, "/")
	for i := 1; i <= len(parts); i++ {
		p := strings.Join(parts[:i], "/")
		key := sliceID + "\x00" + p
		if _, ok := entries[key]; ok {
			continue
		}
		parent := sliceID
		if i > 1 {
			parent = entryID(sliceID, strings.Join(parts[:i-1], "/"))
		}
		entries[key] = entryRow{
			id:       entryID(sliceID, p),
			sliceID:  sliceID,
			path:     p,
			typ:      "directory",
			parentID: parent,
			size:     0,
		}
	}
}

func objectKey(namespace string, parts ...string) string {
	if strings.TrimSpace(namespace) == "" {
		return strings.Join(parts, ":")
	}
	return fmt.Sprintf("%s:%s", strings.TrimSpace(namespace), strings.Join(parts, ":"))
}

func hashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func cloneLegacyContent(fc *models.FileContent, path string) *models.FileContent {
	if fc == nil {
		return nil
	}
	content := &models.FileContent{
		FileID: fc.FileID,
		Path:   common.CleanRelativePath(path),
		Size:   fc.Size,
		Hash:   strings.TrimSpace(fc.Hash),
	}
	if content.Path == "" {
		content.Path = common.CleanRelativePath(fc.Path)
	}
	if len(fc.Content) > 0 {
		content.Content = append([]byte(nil), fc.Content...)
		content.Size = int64(len(content.Content))
	}
	return content
}

type legacySnapshotIndexes struct {
	entriesByID    map[string]*models.DirectoryEntry
	headSnapshots  map[string]*models.CommitSnapshot
	versionedByKey map[string]*models.FileContent
}

func buildLegacySnapshotIndexes(snap *storage.LegacyPostgresSnapshot) legacySnapshotIndexes {
	indexes := legacySnapshotIndexes{
		entriesByID:    make(map[string]*models.DirectoryEntry, len(snap.Entries)),
		headSnapshots:  make(map[string]*models.CommitSnapshot, len(snap.SliceMetadata)),
		versionedByKey: make(map[string]*models.FileContent, len(snap.VersionedContent)),
	}
	for _, entry := range snap.Entries {
		if entry == nil || entry.ID == "" {
			continue
		}
		indexes.entriesByID[entry.ID] = entry
	}
	for sliceID, meta := range snap.SliceMetadata {
		if meta == nil || strings.TrimSpace(meta.HeadCommitHash) == "" {
			continue
		}
		if snapshot := snap.CommitSnapshots[meta.HeadCommitHash]; snapshot != nil {
			indexes.headSnapshots[sliceID] = snapshot
		}
	}
	for hash, content := range snap.VersionedContent {
		hash = strings.TrimSpace(hash)
		if hash == "" || content == nil {
			continue
		}
		indexes.versionedByKey[hash] = content
	}
	return indexes
}

func resolveLegacyCurrentContent(row entryRow, snap *storage.LegacyPostgresSnapshot, indexes legacySnapshotIndexes) *models.FileContent {
	if row.typ != "file" {
		return nil
	}
	if fc := cloneLegacyContent(snap.FileContents[row.id], row.path); fc != nil && len(fc.Content) > 0 {
		return fc
	}
	if fc := cloneLegacyContent(snap.FileContents[entryID(row.sliceID, row.path)], row.path); fc != nil && len(fc.Content) > 0 {
		return fc
	}
	if fc := cloneLegacyContent(snap.FileContents[row.path], row.path); fc != nil && len(fc.Content) > 0 {
		return fc
	}
	if entry := indexes.entriesByID[row.id]; entry != nil && len(entry.Content) > 0 {
		return &models.FileContent{
			FileID:  row.id,
			Path:    row.path,
			Content: append([]byte(nil), entry.Content...),
			Size:    int64(len(entry.Content)),
			Hash:    strings.TrimSpace(entry.Hash),
		}
	}
	if entry := indexes.entriesByID[entryID(row.sliceID, row.path)]; entry != nil && len(entry.Content) > 0 {
		return &models.FileContent{
			FileID:  entryID(row.sliceID, row.path),
			Path:    row.path,
			Content: append([]byte(nil), entry.Content...),
			Size:    int64(len(entry.Content)),
			Hash:    strings.TrimSpace(entry.Hash),
		}
	}
	if snapshot := indexes.headSnapshots[row.sliceID]; snapshot != nil {
		if hash := strings.TrimSpace(snapshot.Files[row.path]); hash != "" {
			if vc := cloneLegacyContent(indexes.versionedByKey[hash], row.path); vc != nil && len(vc.Content) > 0 {
				if vc.Hash == "" {
					vc.Hash = hash
				}
				return vc
			}
		}
	}
	return nil
}

func persistLegacyManifest(
	ctx context.Context,
	tx pgx.Tx,
	objectStore storage.ObjectStore,
	namespace, sliceID, filePath string,
	content []byte,
	desiredHash string,
	writtenBlocks map[string]struct{},
	writtenVersioned map[string]struct{},
) (manifestHash string, blocksWritten int, err error) {
	filePath = common.CleanRelativePath(filePath)
	if strings.TrimSpace(sliceID) == "" || filePath == "" {
		return "", 0, storage.ErrInvalidInput
	}
	if content == nil {
		content = []byte{}
	}

	manifestHash = strings.TrimSpace(desiredHash)
	if manifestHash == "" {
		manifestHash = hashContent(content)
	}

	blocks, payloads := storage.ChunkFile(content, storage.DefaultFileBlockSize)
	for blockHash, payload := range payloads {
		if _, ok := writtenBlocks[blockHash]; ok {
			continue
		}
		key := objectKey(namespace, "blocks", blockHash)
		if _, err := objectStore.GetObject(ctx, key); err == nil {
			writtenBlocks[blockHash] = struct{}{}
			continue
		} else if err != storage.ErrEntryNotFound {
			return "", blocksWritten, err
		}
		if err := objectStore.PutObject(ctx, key, payload); err != nil {
			return "", blocksWritten, err
		}
		writtenBlocks[blockHash] = struct{}{}
		blocksWritten++
	}

	manifest := &models.FileManifest{
		Path:      filePath,
		TotalSize: int64(len(content)),
		Hash:      manifestHash,
		Blocks:    blocks,
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", blocksWritten, err
	}

	if err := objectStore.PutObject(ctx, objectKey(namespace, "manifests", sliceID, filePath), raw); err != nil {
		return "", blocksWritten, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO file_manifests (slice_id, path, hash, total_size, block_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		ON CONFLICT (slice_id, path) DO UPDATE SET
			hash = EXCLUDED.hash,
			total_size = EXCLUDED.total_size,
			block_count = EXCLUDED.block_count,
			updated_at = NOW()
	`, sliceID, filePath, manifest.Hash, manifest.TotalSize, len(manifest.Blocks)); err != nil {
		return "", blocksWritten, err
	}

	if _, ok := writtenVersioned[manifest.Hash]; !ok {
		if err := objectStore.PutObject(ctx, objectKey(namespace, "versioned_manifests", manifest.Hash), raw); err != nil {
			return "", blocksWritten, err
		}
		writtenVersioned[manifest.Hash] = struct{}{}
	}

	return manifest.Hash, blocksWritten, nil
}

func repairNativeContent(ctx context.Context, pool *pgxpool.Pool, objectStore storage.ObjectStore, namespace string, snap *storage.LegacyPostgresSnapshot) (*repairStats, error) {
	stats := &repairStats{}
	if snap == nil {
		return stats, fmt.Errorf("nil snapshot")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	indexes := buildLegacySnapshotIndexes(snap)
	writtenBlocks := make(map[string]struct{})
	writtenVersioned := make(map[string]struct{})

	rows, err := tx.Query(ctx, `
		SELECT de.id, de.slice_id, de.path, de.type, de.parent_id, de.size,
			EXISTS(
				SELECT 1 FROM file_manifests fm
				WHERE fm.slice_id = de.slice_id AND fm.path = de.path
			) AS has_manifest
		FROM directory_entries de
		WHERE de.type = 'file'
		ORDER BY de.slice_id, de.path
	`)
	if err != nil {
		return nil, err
	}

	type repairRow struct {
		entryRow
		hasManifest bool
	}

	var repairRows []repairRow
	for rows.Next() {
		var row repairRow
		if err := rows.Scan(&row.id, &row.sliceID, &row.path, &row.typ, &row.parentID, &row.size, &row.hasManifest); err != nil {
			rows.Close()
			return nil, err
		}
		repairRows = append(repairRows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for _, row := range repairRows {
		if row.hasManifest {
			continue
		}
		content := resolveLegacyCurrentContent(row.entryRow, snap, indexes)
		if content == nil {
			continue
		}
		if _, blocksWritten, err := persistLegacyManifest(ctx, tx, objectStore, namespace, row.sliceID, row.path, content.Content, content.Hash, writtenBlocks, writtenVersioned); err != nil {
			return nil, err
		} else {
			stats.ManifestsBackfilled++
			stats.BlocksWritten += blocksWritten
		}
	}

	stats.VersionedBackfilled = len(writtenVersioned)

	tag, err := tx.Exec(ctx, `
		UPDATE directory_entries AS de
		SET size = fm.total_size,
			updated_at = NOW()
		FROM file_manifests AS fm
		WHERE de.slice_id = fm.slice_id
			AND de.path = fm.path
			AND de.type = 'file'
			AND COALESCE(de.size, 0) = 0
			AND fm.total_size > 0
	`)
	if err != nil {
		return nil, err
	}
	stats.SliceEntrySizesFixed = int(tag.RowsAffected())

	tag, err = tx.Exec(ctx, `
		UPDATE directory_entries AS child
		SET size = pfm.total_size,
			updated_at = NOW()
		FROM slices AS s
		JOIN file_manifests AS pfm
			ON pfm.slice_id = s.parent_id
		WHERE child.slice_id = s.id
			AND child.path = pfm.path
			AND child.type = 'file'
			AND COALESCE(child.size, 0) = 0
			AND COALESCE(s.parent_id, '') <> ''
			AND pfm.total_size > 0
	`)
	if err != nil {
		return nil, err
	}
	stats.ParentEntrySizesFixed += int(tag.RowsAffected())

	tag, err = tx.Exec(ctx, `
		UPDATE directory_entries AS child
		SET size = parent.size,
			updated_at = NOW()
		FROM slices AS s
		JOIN directory_entries AS parent
			ON parent.slice_id = s.parent_id
		WHERE child.slice_id = s.id
			AND child.path = parent.path
			AND child.type = 'file'
			AND parent.type = 'file'
			AND COALESCE(child.size, 0) = 0
			AND COALESCE(s.parent_id, '') <> ''
			AND parent.size > 0
	`)
	if err != nil {
		return nil, err
	}
	stats.ParentEntrySizesFixed += int(tag.RowsAffected())

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return stats, nil
}

type pruneCandidate struct {
	entryID string
	sliceID string
	path    string
}

type pruneSliceInfo struct {
	parentID   string
	headCommit string
}

func pruneBrokenEntries(ctx context.Context, pool *pgxpool.Pool, st storage.Storage, dryRun bool) (*pruneStats, error) {
	stats := &pruneStats{}

	sliceInfo, err := loadPruneSliceInfo(ctx, pool)
	if err != nil {
		return nil, err
	}

	candidates, err := loadPruneCandidates(ctx, pool)
	if err != nil {
		return nil, err
	}
	stats.Candidates = len(candidates)
	if len(candidates) == 0 {
		return stats, nil
	}

	broken, err := filterBrokenCandidates(ctx, st, sliceInfo, candidates)
	if err != nil {
		return nil, err
	}
	stats.BrokenFiles = len(broken)
	if len(broken) == 0 || dryRun {
		stats.AffectedSlices = countAffectedPruneSlices(broken)
		return stats, nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE prune_broken_entries (
			entry_id TEXT PRIMARY KEY,
			slice_id TEXT NOT NULL,
			path TEXT NOT NULL
		) ON COMMIT DROP
	`); err != nil {
		return nil, err
	}

	rows := make([][]any, 0, len(broken))
	for _, row := range broken {
		rows = append(rows, []any{row.entryID, row.sliceID, row.path})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"prune_broken_entries"}, []string{"entry_id", "slice_id", "path"}, pgx.CopyFromRows(rows)); err != nil {
		return nil, err
	}

	stats.AffectedSlices = countAffectedPruneSlices(broken)

	tag, err := tx.Exec(ctx, `
		DELETE FROM file_slice_index f
		USING prune_broken_entries p
		WHERE f.file_id = p.entry_id
			AND f.slice_id = p.slice_id
	`)
	if err != nil {
		return nil, err
	}
	stats.FileIndexRowsDeleted = int(tag.RowsAffected())

	tag, err = tx.Exec(ctx, `
		DELETE FROM directory_entries d
		USING prune_broken_entries p
		WHERE d.slice_id = p.slice_id
			AND d.path = p.path
	`)
	if err != nil {
		return nil, err
	}
	stats.FileEntriesDeleted = int(tag.RowsAffected())

	for {
		tag, err = tx.Exec(ctx, `
			DELETE FROM directory_entries d
			WHERE d.type = 'directory'
				AND d.path <> ''
				AND d.slice_id IN (SELECT DISTINCT slice_id FROM prune_broken_entries)
				AND NOT EXISTS (
					SELECT 1
					FROM directory_entries c
					WHERE c.slice_id = d.slice_id
						AND c.parent_id = d.id
				)
		`)
		if err != nil {
			return nil, err
		}
		removed := int(tag.RowsAffected())
		stats.DirectoriesDeleted += removed
		if removed == 0 {
			break
		}
	}

	tag, err = tx.Exec(ctx, `
		WITH affected AS (
			SELECT DISTINCT slice_id FROM prune_broken_entries
		),
		agg AS (
			SELECT a.slice_id,
				COALESCE(jsonb_agg(to_jsonb(de.path) ORDER BY de.path), '[]'::jsonb) AS files
			FROM affected a
			LEFT JOIN directory_entries de
				ON de.slice_id = a.slice_id
				AND de.type = 'file'
			GROUP BY a.slice_id
		)
		UPDATE slices s
		SET files = agg.files,
			updated_at = NOW()
		FROM agg
		WHERE s.id = agg.slice_id
	`)
	if err != nil {
		return nil, err
	}
	stats.SliceRowsUpdated = int(tag.RowsAffected())

	tag, err = tx.Exec(ctx, `
		WITH affected AS (
			SELECT DISTINCT slice_id FROM prune_broken_entries
		),
		filtered AS (
			SELECT sm.slice_id, value AS path
			FROM slice_metadata sm
			JOIN affected a ON a.slice_id = sm.slice_id
			CROSS JOIN LATERAL jsonb_array_elements_text(sm.modified_files) AS value
			WHERE EXISTS (
				SELECT 1
				FROM directory_entries de
				WHERE de.slice_id = sm.slice_id
					AND de.path = value
			)
		),
		agg AS (
			SELECT a.slice_id,
				COALESCE(jsonb_agg(to_jsonb(f.path) ORDER BY f.path), '[]'::jsonb) AS modified_files,
				COUNT(f.path)::INT AS modified_files_count
			FROM affected a
			LEFT JOIN filtered f ON f.slice_id = a.slice_id
			GROUP BY a.slice_id
		)
		UPDATE slice_metadata sm
		SET modified_files = agg.modified_files,
			modified_files_count = agg.modified_files_count,
			last_modified = NOW()
		FROM agg
		WHERE sm.slice_id = agg.slice_id
	`)
	if err != nil {
		return nil, err
	}
	stats.SliceMetadataUpdated = int(tag.RowsAffected())

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return stats, nil
}

func loadPruneSliceInfo(ctx context.Context, pool *pgxpool.Pool) (map[string]pruneSliceInfo, error) {
	rows, err := pool.Query(ctx, `
		SELECT s.id, COALESCE(s.parent_id, ''), COALESCE(sm.head_commit_hash, '')
		FROM slices s
		LEFT JOIN slice_metadata sm ON sm.slice_id = s.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]pruneSliceInfo{}
	for rows.Next() {
		var (
			sliceID string
			info    pruneSliceInfo
		)
		if err := rows.Scan(&sliceID, &info.parentID, &info.headCommit); err != nil {
			return nil, err
		}
		result[sliceID] = info
	}
	return result, rows.Err()
}

func loadPruneCandidates(ctx context.Context, pool *pgxpool.Pool) ([]pruneCandidate, error) {
	rows, err := pool.Query(ctx, `
		WITH RECURSIVE ancestors AS (
			SELECT id AS slice_id, parent_id AS ancestor_id
			FROM slices
			WHERE COALESCE(parent_id, '') <> ''
			UNION ALL
			SELECT a.slice_id, s.parent_id AS ancestor_id
			FROM ancestors a
			JOIN slices s ON s.id = a.ancestor_id
			WHERE COALESCE(s.parent_id, '') <> ''
		)
		SELECT de.id, de.slice_id, de.path
		FROM directory_entries de
		WHERE de.type = 'file'
			AND COALESCE(octet_length(de.content), 0) = 0
			AND NOT EXISTS (
				SELECT 1
				FROM file_manifests fm
				WHERE fm.slice_id = de.slice_id
					AND fm.path = de.path
			)
			AND NOT EXISTS (
				SELECT 1
				FROM ancestors a
				JOIN file_manifests fm
					ON fm.slice_id = a.ancestor_id
					AND fm.path = de.path
				WHERE a.slice_id = de.slice_id
			)
		ORDER BY de.slice_id, de.path
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []pruneCandidate{}
	for rows.Next() {
		var row pruneCandidate
		if err := rows.Scan(&row.entryID, &row.sliceID, &row.path); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func filterBrokenCandidates(ctx context.Context, st storage.Storage, sliceInfo map[string]pruneSliceInfo, candidates []pruneCandidate) ([]pruneCandidate, error) {
	effectiveCommitCache := map[string]string{}
	snapshotCache := map[string]*models.CommitSnapshot{}
	versionedManifestCache := map[string]bool{}

	broken := make([]pruneCandidate, 0, len(candidates))
	for _, row := range candidates {
		effectiveCommit := resolvePruneEffectiveCommit(row.sliceID, sliceInfo, effectiveCommitCache, map[string]bool{})
		if effectiveCommit != "" {
			snapshot, err := getPruneCommitSnapshot(ctx, st, effectiveCommit, snapshotCache)
			if err != nil {
				if !errors.Is(err, storage.ErrCommitNotFound) {
					return nil, err
				}
			} else if snapshot != nil {
				if hash := strings.TrimSpace(snapshot.Files[row.path]); hash != "" {
					ok, err := pruneHasVersionedManifest(ctx, st, hash, versionedManifestCache)
					if err != nil {
						return nil, err
					}
					if ok {
						continue
					}
				}
			}
		}
		broken = append(broken, row)
	}
	return broken, nil
}

func getPruneCommitSnapshot(ctx context.Context, st storage.Storage, commitHash string, cache map[string]*models.CommitSnapshot) (*models.CommitSnapshot, error) {
	if snapshot, ok := cache[commitHash]; ok {
		return snapshot, nil
	}
	snapshot, err := st.GetCommitSnapshot(ctx, commitHash)
	if err != nil {
		return nil, err
	}
	cache[commitHash] = snapshot
	return snapshot, nil
}

func pruneHasVersionedManifest(ctx context.Context, st storage.Storage, hash string, cache map[string]bool) (bool, error) {
	if ok, exists := cache[hash]; exists {
		return ok, nil
	}
	_, err := st.GetVersionedFileManifest(ctx, hash)
	if err == nil {
		cache[hash] = true
		return true, nil
	}
	if errors.Is(err, storage.ErrEntryNotFound) {
		cache[hash] = false
		return false, nil
	}
	return false, err
}

func resolvePruneEffectiveCommit(sliceID string, sliceInfo map[string]pruneSliceInfo, cache map[string]string, visiting map[string]bool) string {
	sliceID = strings.TrimSpace(sliceID)
	if sliceID == "" {
		return ""
	}
	if commit, ok := cache[sliceID]; ok {
		return commit
	}
	if visiting[sliceID] {
		return ""
	}
	info, ok := sliceInfo[sliceID]
	if !ok {
		return ""
	}
	visiting[sliceID] = true
	commit := strings.TrimSpace(info.headCommit)
	if info.parentID != "" && (commit == "" || strings.HasPrefix(commit, "init-")) {
		commit = resolvePruneEffectiveCommit(info.parentID, sliceInfo, cache, visiting)
	}
	delete(visiting, sliceID)
	cache[sliceID] = commit
	return commit
}

func countAffectedPruneSlices(entries []pruneCandidate) int {
	if len(entries) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(entries))
	for _, row := range entries {
		if strings.TrimSpace(row.sliceID) == "" {
			continue
		}
		seen[row.sliceID] = struct{}{}
	}
	return len(seen)
}

func backfillNative(ctx context.Context, pool *pgxpool.Pool, objectStore storage.ObjectStore, namespace string, snap *storage.LegacyPostgresSnapshot) (*backfillStats, error) {
	stats := &backfillStats{}
	if snap == nil {
		return stats, fmt.Errorf("nil snapshot")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	now := time.Now()

	// Accounts
	for username, user := range snap.Users {
		createdAt := now
		updatedAt := now
		if user != nil {
			if !user.CreatedAt.IsZero() {
				createdAt = user.CreatedAt
			}
			if !user.UpdatedAt.IsZero() {
				updatedAt = user.UpdatedAt
			}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO users (username, created_at, updated_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (username) DO UPDATE SET updated_at = EXCLUDED.updated_at
		`, username, createdAt, updatedAt)
		if err != nil {
			return nil, err
		}
		stats.Users++
	}

	for slug, org := range snap.Orgs {
		name := ""
		createdBy := ""
		createdAt := now
		updatedAt := now
		if org != nil {
			name = org.Name
			createdBy = org.CreatedBy
			if !org.CreatedAt.IsZero() {
				createdAt = org.CreatedAt
			}
			if !org.UpdatedAt.IsZero() {
				updatedAt = org.UpdatedAt
			}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO organizations (slug, name, created_by, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name, updated_at = EXCLUDED.updated_at
		`, slug, name, createdBy, createdAt, updatedAt)
		if err != nil {
			return nil, err
		}
		stats.Organizations++
	}

	for orgSlug, members := range snap.OrgMembers {
		for username, member := range members {
			role := "member"
			createdAt := now
			updatedAt := now
			if member != nil {
				if member.Role != "" {
					role = string(member.Role)
				}
				if !member.CreatedAt.IsZero() {
					createdAt = member.CreatedAt
				}
				if !member.UpdatedAt.IsZero() {
					updatedAt = member.UpdatedAt
				}
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO organization_members (org_slug, username, role, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (org_slug, username) DO UPDATE SET role = EXCLUDED.role, updated_at = EXCLUDED.updated_at
			`, orgSlug, username, role, createdAt, updatedAt)
			if err != nil {
				return nil, err
			}
			stats.OrgMemberships++
		}
	}

	// Slices + metadata + locks
	for sliceID, sl := range snap.Slices {
		if sl == nil || sliceID == "" {
			continue
		}

		filesJSON, _ := json.Marshal(sl.Files)
		ownersJSON, _ := json.Marshal(sl.Owners)
		mountsJSON, _ := json.Marshal(sl.FolderMounts)
		_, err := tx.Exec(ctx, `
			INSERT INTO slices (id, name, description, created_by, parent_id, is_root, files, folder_mounts, owners, created_at, updated_at)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9,$10,$11)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				description = EXCLUDED.description,
				created_by = EXCLUDED.created_by,
				parent_id = EXCLUDED.parent_id,
				is_root = EXCLUDED.is_root,
				files = EXCLUDED.files,
				folder_mounts = EXCLUDED.folder_mounts,
				owners = EXCLUDED.owners,
				updated_at = EXCLUDED.updated_at
		`, sl.ID, sl.Name, sl.Description, sl.CreatedBy, sl.ParentSlice, sl.IsRoot, filesJSON, mountsJSON, ownersJSON, sl.CreatedAt, sl.UpdatedAt)
		if err != nil {
			return nil, err
		}
		stats.Slices++

		if snap.LockedSlices != nil && snap.LockedSlices[sliceID] {
			_, err := tx.Exec(ctx, `INSERT INTO slice_locks (slice_id) VALUES ($1) ON CONFLICT DO NOTHING`, sliceID)
			if err != nil {
				return nil, err
			}
		}

		if meta := snap.SliceMetadata[sliceID]; meta != nil {
			modifiedJSON, _ := json.Marshal(meta.ModifiedFiles)
			_, err := tx.Exec(ctx, `
				INSERT INTO slice_metadata (slice_id, head_commit_hash, modified_files, last_modified, modified_files_count)
				VALUES ($1,$2,$3,$4,$5)
				ON CONFLICT (slice_id) DO UPDATE SET
					head_commit_hash = EXCLUDED.head_commit_hash,
					modified_files = EXCLUDED.modified_files,
					last_modified = EXCLUDED.last_modified,
					modified_files_count = EXCLUDED.modified_files_count
			`, sliceID, meta.HeadCommitHash, modifiedJSON, meta.LastModified, meta.ModifiedFilesCount)
			if err != nil {
				return nil, err
			}
		}
	}

	// File locks
	for fileID, ownerSlice := range snap.FileLocks {
		if fileID == "" || ownerSlice == "" {
			continue
		}
		_, err := tx.Exec(ctx, `INSERT INTO file_locks (file_id, owner_slice_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, fileID, ownerSlice)
		if err != nil {
			return nil, err
		}
	}

	// File index
	for fileID, sliceSet := range snap.FileIndex {
		for sliceID, ok := range sliceSet {
			if !ok {
				continue
			}
			_, err := tx.Exec(ctx, `INSERT INTO file_slice_index (file_id, slice_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, fileID, sliceID)
			if err != nil {
				return nil, err
			}
			stats.FileIndexRows++
		}
	}

	// Changesets
	for id, cs := range snap.Changesets {
		if cs == nil || id == "" {
			continue
		}
		modifiedJSON, _ := json.Marshal(cs.ModifiedFiles)
		_, err := tx.Exec(ctx, `
			INSERT INTO changesets (id, hash, slice_id, base_commit_hash, modified_files, status, author, message, created_at, merged_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (id) DO UPDATE SET
				hash = EXCLUDED.hash,
				slice_id = EXCLUDED.slice_id,
				base_commit_hash = EXCLUDED.base_commit_hash,
				modified_files = EXCLUDED.modified_files,
				status = EXCLUDED.status,
				author = EXCLUDED.author,
				message = EXCLUDED.message,
				merged_at = EXCLUDED.merged_at
		`, cs.ID, cs.Hash, cs.SliceID, cs.BaseCommitHash, modifiedJSON, int(cs.Status), cs.Author, cs.Message, cs.CreatedAt, cs.MergedAt)
		if err != nil {
			return nil, err
		}
		stats.Changesets++
	}

	// Changeset snapshots
	for id, snapshot := range snap.ChangesetSnapshots {
		if snapshot == nil || id == "" {
			continue
		}
		modifiedJSON, _ := json.Marshal(snapshot.ModifiedFiles)
		_, err := tx.Exec(ctx, `
			INSERT INTO changeset_snapshots (id, changeset_id, version, hash, base_commit_hash, modified_files, author, message, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (id) DO UPDATE SET
				changeset_id = EXCLUDED.changeset_id,
				version = EXCLUDED.version,
				hash = EXCLUDED.hash,
				base_commit_hash = EXCLUDED.base_commit_hash,
				modified_files = EXCLUDED.modified_files,
				author = EXCLUDED.author,
				message = EXCLUDED.message,
				created_at = EXCLUDED.created_at
		`, snapshot.ID, snapshot.ChangesetID, snapshot.Version, snapshot.Hash, snapshot.BaseCommitHash, modifiedJSON, snapshot.Author, snapshot.Message, snapshot.CreatedAt)
		if err != nil {
			return nil, err
		}
		stats.ChangesetSnaps++
	}

	// Slice commits
	for sliceID, commits := range snap.SliceCommits {
		if sliceID == "" || len(commits) == 0 {
			continue
		}
		for i := len(commits) - 1; i >= 0; i-- {
			c := commits[i]
			if c == nil || c.CommitHash == "" {
				continue
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO slice_commits (slice_id, commit_hash, parent_hash, message, committed_at)
				VALUES ($1,$2,$3,$4,$5)
				ON CONFLICT (slice_id, commit_hash) DO NOTHING
			`, sliceID, c.CommitHash, c.ParentHash, c.Message, c.Timestamp)
			if err != nil {
				return nil, err
			}
			stats.SliceCommits++
		}
	}

	// Directory entries (materialize a tree even if legacy snapshot is flat)
	entrySet := make(map[string]entryRow)
	for sliceID, sl := range snap.Slices {
		if sl == nil {
			continue
		}
		for _, p := range sl.Files {
			p = common.CleanRelativePath(p)
			if p == "" {
				continue
			}
			ensureDirs(entrySet, sliceID, p)
		}
	}
	for _, e := range snap.Entries {
		if e == nil {
			continue
		}
		sliceID := ""
		if parts := strings.SplitN(e.ID, ":", 2); len(parts) == 2 {
			sliceID = parts[0]
		}
		if sliceID == "" {
			sliceID = e.ParentID
		}
		if sliceID == "" {
			continue
		}

		p := common.CleanRelativePath(e.Path)
		if p == "" {
			continue
		}
		ensureDirs(entrySet, sliceID, p)

		typ := e.Type
		if typ == "" {
			typ = "file"
		}
		parent := sliceID
		if dir := path.Dir(p); dir != "." && dir != "/" {
			parent = entryID(sliceID, dir)
		}
		entrySet[sliceID+"\x00"+p] = entryRow{
			id:       entryID(sliceID, p),
			sliceID:  sliceID,
			path:     p,
			typ:      typ,
			parentID: parent,
			size:     e.Size,
		}
	}
	for _, cs := range snap.CommitSnapshots {
		if cs == nil || cs.SliceID == "" || len(cs.Files) == 0 {
			continue
		}
		for rawPath := range cs.Files {
			p := common.CleanRelativePath(rawPath)
			if p == "" {
				continue
			}
			ensureDirs(entrySet, cs.SliceID, p)
			key := cs.SliceID + "\x00" + p
			if _, ok := entrySet[key]; ok {
				continue
			}
			parent := cs.SliceID
			if dir := path.Dir(p); dir != "." && dir != "/" {
				parent = entryID(cs.SliceID, dir)
			}
			size := int64(0)
			if fc := snap.FileContents[p]; fc != nil && fc.Size != 0 {
				size = fc.Size
			}
			entrySet[key] = entryRow{
				id:       entryID(cs.SliceID, p),
				sliceID:  cs.SliceID,
				path:     p,
				typ:      "file",
				parentID: parent,
				size:     size,
			}
		}
	}

	// Deterministic order for debuggability.
	keys := make([]string, 0, len(entrySet))
	for k := range entrySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		row := entrySet[k]
		_, err := tx.Exec(ctx, `
			INSERT INTO directory_entries (id, slice_id, path, type, parent_id, content, size)
			VALUES ($1,$2,$3,$4,$5,NULL,$6)
			ON CONFLICT (slice_id, path) DO UPDATE SET
				id = EXCLUDED.id,
				type = EXCLUDED.type,
				parent_id = EXCLUDED.parent_id,
				size = EXCLUDED.size,
				updated_at = NOW()
		`, row.id, row.sliceID, row.path, row.typ, row.parentID, row.size)
		if err != nil {
			return nil, err
		}
		stats.Entries++
	}

	legacyEntriesByID := make(map[string]*models.DirectoryEntry, len(snap.Entries))
	for _, entry := range snap.Entries {
		if entry == nil || entry.ID == "" {
			continue
		}
		legacyEntriesByID[entry.ID] = entry
	}
	headSnapshots := make(map[string]*models.CommitSnapshot, len(snap.SliceMetadata))
	for sliceID, meta := range snap.SliceMetadata {
		if meta == nil || strings.TrimSpace(meta.HeadCommitHash) == "" {
			continue
		}
		if snapshot := snap.CommitSnapshots[meta.HeadCommitHash]; snapshot != nil {
			headSnapshots[sliceID] = snapshot
		}
	}

	resolveCurrentContent := func(row entryRow) *models.FileContent {
		if row.typ != "file" {
			return nil
		}
		if fc := cloneLegacyContent(snap.FileContents[row.id], row.path); fc != nil && len(fc.Content) > 0 {
			return fc
		}
		if fc := cloneLegacyContent(snap.FileContents[row.path], row.path); fc != nil && len(fc.Content) > 0 {
			return fc
		}
		if entry := legacyEntriesByID[row.id]; entry != nil && len(entry.Content) > 0 {
			return &models.FileContent{
				FileID:  row.id,
				Path:    row.path,
				Content: append([]byte(nil), entry.Content...),
				Size:    int64(len(entry.Content)),
				Hash:    strings.TrimSpace(entry.Hash),
			}
		}
		if snapshot := headSnapshots[row.sliceID]; snapshot != nil {
			if hash := strings.TrimSpace(snapshot.Files[row.path]); hash != "" {
				if vc := cloneLegacyContent(snap.VersionedContent[hash], row.path); vc != nil && len(vc.Content) > 0 {
					if vc.Hash == "" {
						vc.Hash = hash
					}
					return vc
				}
			}
		}
		return nil
	}

	writtenBlocks := make(map[string]struct{})
	writtenVersioned := make(map[string]struct{})
	for _, k := range keys {
		row := entrySet[k]
		content := resolveCurrentContent(row)
		if content == nil {
			continue
		}
		_, blocksWritten, err := persistLegacyManifest(ctx, tx, objectStore, namespace, row.sliceID, row.path, content.Content, content.Hash, writtenBlocks, writtenVersioned)
		if err != nil {
			return nil, err
		}
		stats.FileManifests++
		stats.Blocks += blocksWritten
	}

	for hash, vc := range snap.VersionedContent {
		hash = strings.TrimSpace(hash)
		if vc == nil || hash == "" || len(vc.Content) == 0 {
			continue
		}
		if _, ok := writtenVersioned[hash]; ok {
			continue
		}
		path := common.CleanRelativePath(vc.Path)
		if path == "" {
			path = hash
		}
		blocks, payloads := storage.ChunkFile(vc.Content, storage.DefaultFileBlockSize)
		for blockHash, payload := range payloads {
			if _, ok := writtenBlocks[blockHash]; ok {
				continue
			}
			key := objectKey(namespace, "blocks", blockHash)
			if _, err := objectStore.GetObject(ctx, key); err == nil {
				writtenBlocks[blockHash] = struct{}{}
				continue
			} else if err != storage.ErrEntryNotFound {
				return nil, err
			}
			if err := objectStore.PutObject(ctx, key, payload); err != nil {
				return nil, err
			}
			writtenBlocks[blockHash] = struct{}{}
			stats.Blocks++
		}
		raw, err := json.Marshal(&models.FileManifest{
			Path:      path,
			TotalSize: int64(len(vc.Content)),
			Hash:      hash,
			Blocks:    blocks,
		})
		if err != nil {
			return nil, err
		}
		if err := objectStore.PutObject(ctx, objectKey(namespace, "versioned_manifests", hash), raw); err != nil {
			return nil, err
		}
		writtenVersioned[hash] = struct{}{}
	}
	stats.Versioned = len(writtenVersioned)

	// Commit snapshots
	for commitHash, cs := range snap.CommitSnapshots {
		if cs == nil || commitHash == "" {
			continue
		}
		filesJSON, _ := json.Marshal(cs.Files)
		_, err := tx.Exec(ctx, `
			INSERT INTO commit_snapshots (commit_hash, slice_id, files, committed_at)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (commit_hash) DO UPDATE SET slice_id = EXCLUDED.slice_id, files = EXCLUDED.files, committed_at = EXCLUDED.committed_at
		`, commitHash, cs.SliceID, filesJSON, cs.Timestamp)
		if err != nil {
			return nil, err
		}
		stats.CommitSnaps++
	}

	// Global state
	if gs := snap.GlobalState; gs != nil {
		stateData := struct {
			History []*models.GlobalCommit `json:"history"`
		}{History: gs.History}
		stateJSON, _ := json.Marshal(stateData)
		_, err := tx.Exec(ctx, `
			INSERT INTO global_state (id, root_id, global_commit_hash, updated_at, state_json)
			VALUES (true, $1, $2, $3, $4)
			ON CONFLICT (id) DO UPDATE SET root_id = EXCLUDED.root_id, global_commit_hash = EXCLUDED.global_commit_hash, updated_at = EXCLUDED.updated_at, state_json = EXCLUDED.state_json
		`, common.RootSliceID, gs.GlobalCommitHash, gs.Timestamp, stateJSON)
		if err != nil {
			return nil, err
		}
	}

	// File changes
	for id, ch := range snap.FileChanges {
		if ch == nil || id == "" {
			continue
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO file_changes (id, slice_id, commit_hash, path, old_path, change_type, old_hash, new_hash, lines_added, lines_deleted, author, message, committed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (id) DO NOTHING
		`, id, ch.SliceID, ch.CommitHash, ch.Path, ch.OldPath, string(ch.ChangeType), ch.OldHash, ch.NewHash,
			ch.LinesAdded, ch.LinesDeleted, ch.Author, ch.Message, ch.Timestamp)
		if err != nil {
			return nil, err
		}
		stats.FileChanges++
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return stats, nil
}

func verifyNative(ctx context.Context, pool *pgxpool.Pool, snap *storage.LegacyPostgresSnapshot) error {
	if snap == nil {
		return fmt.Errorf("nil snapshot")
	}

	var slicesCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM slices`).Scan(&slicesCount); err != nil {
		return err
	}
	if slicesCount < len(snap.Slices) {
		return fmt.Errorf("native slices=%d < snapshot slices=%d", slicesCount, len(snap.Slices))
	}

	var commitSnapCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM commit_snapshots`).Scan(&commitSnapCount); err != nil {
		return err
	}
	if commitSnapCount < len(snap.CommitSnapshots) {
		return fmt.Errorf("native commit_snapshots=%d < snapshot commit_snapshots=%d", commitSnapCount, len(snap.CommitSnapshots))
	}

	var changesetSnapshotCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM changeset_snapshots`).Scan(&changesetSnapshotCount); err != nil {
		return err
	}
	if changesetSnapshotCount < len(snap.ChangesetSnapshots) {
		return fmt.Errorf("native changeset_snapshots=%d < snapshot changeset_snapshots=%d", changesetSnapshotCount, len(snap.ChangesetSnapshots))
	}

	// Spot check that a few paths from commit snapshots exist as entries.
	sampled := 0
	for _, cs := range snap.CommitSnapshots {
		if cs == nil || sampled >= 10 {
			continue
		}
		for p := range cs.Files {
			p = common.CleanRelativePath(p)
			if p == "" {
				continue
			}
			var exists bool
			err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM directory_entries WHERE slice_id = $1 AND path = $2)`, cs.SliceID, p).Scan(&exists)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("missing directory_entries row: slice=%s path=%s", cs.SliceID, p)
			}
			sampled++
			break
		}
	}
	return nil
}
