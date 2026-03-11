package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	gcsstorage "cloud.google.com/go/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/config"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	"google.golang.org/api/option"
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
	case "verify-native":
		cmdVerifyNative(os.Args[2:])
	case "drop-snapshot":
		cmdDropSnapshot(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	log.Printf("Usage:")
	log.Printf("  storage_migrate backfill-native --dsn <dsn> --namespace <ns>")
	log.Printf("  storage_migrate verify-native --dsn <dsn> --namespace <ns>")
	log.Printf("  storage_migrate drop-snapshot --dsn <dsn> --namespace <ns>")
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

func buildObjectStore(ctx context.Context, cfg *config.Config) (storage.ObjectStore, func(), error) {
	switch strings.ToLower(cfg.ObjectStoreType) {
	case "filesystem", "fs", "file":
		store, err := storage.NewFilesystemObjectStore(cfg.ObjectStoreDir)
		if err != nil {
			return nil, nil, err
		}
		return store, func() {}, nil
	case "", "gcs":
		// Continue below.
	default:
		return nil, nil, fmt.Errorf("unsupported OBJECT_STORE_TYPE: %s", cfg.ObjectStoreType)
	}

	if cfg.GCSBucket == "" {
		return nil, nil, fmt.Errorf("GCS_BUCKET is required")
	}

	clientOpts := []option.ClientOption{}
	if cfg.GCSEndpoint != "" {
		clientOpts = append(clientOpts, option.WithEndpoint(cfg.GCSEndpoint))
	}
	if cfg.GCSDisableAuth {
		clientOpts = append(clientOpts, option.WithoutAuthentication())
	}
	if cfg.GCSCredentialsFile != "" {
		clientOpts = append(clientOpts, option.WithCredentialsFile(cfg.GCSCredentialsFile))
	}
	if cfg.GCSCredentialsJSON != "" {
		clientOpts = append(clientOpts, option.WithCredentialsJSON([]byte(cfg.GCSCredentialsJSON)))
	}

	client, err := gcsstorage.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, nil, err
	}

	return storage.NewGCSObjectStore(client, cfg.GCSBucket), func() { _ = client.Close() }, nil
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

	cfg := config.LoadConfig()
	objectStore, closeObjectStore, err := buildObjectStore(ctx, cfg)
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
			INSERT INTO global_state (id, root_slice_id, global_commit_hash, updated_at, state_json)
			VALUES (true, $1, $2, $3, $4)
			ON CONFLICT (id) DO UPDATE SET root_slice_id = EXCLUDED.root_slice_id, global_commit_hash = EXCLUDED.global_commit_hash, updated_at = EXCLUDED.updated_at, state_json = EXCLUDED.state_json
		`, "root_slice", gs.GlobalCommitHash, gs.Timestamp, stateJSON)
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
