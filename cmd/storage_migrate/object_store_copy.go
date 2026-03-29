package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niczy/gitslice/internal/config"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type objectStoreFlagSet struct {
	storeType          *string
	filesystemDir      *string
	gcsBucket          *string
	gcsEndpoint        *string
	gcsCredentialsFile *string
	gcsCredentialsJSON *string
	gcsDisableAuth     *bool
	r2Bucket           *string
	r2Prefix           *string
	r2Endpoint         *string
	r2Region           *string
	r2AccessKeyID      *string
	r2SecretAccessKey  *string
	r2UsePathStyle     *bool
}

type referencedObjectPlan struct {
	CurrentManifestKeys   []string
	VersionedManifestKeys []string
	BlockKeys             []string
}

type objectCopyStats struct {
	CurrentManifestCopied    int
	CurrentManifestSkipped   int
	VersionedManifestCopied  int
	VersionedManifestSkipped int
	BlocksCopied             int
	BlocksSkipped            int
}

func cmdCopyObjectStore(args []string) {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	fs := flag.NewFlagSet("copy-object-store", flag.ExitOnError)
	dsn := fs.String("dsn", firstNonEmpty(os.Getenv("POSTGRES_DSN"), cfg.PostgresDSN), "Postgres DSN")
	namespace := fs.String("namespace", "core", "Native storage namespace")
	sourceEnv := fs.String("source-env", os.Getenv("SOURCE_ENV"), "Source object-store environment name (required for R2 sources)")
	targetEnv := fs.String("target-env", firstNonEmpty(os.Getenv("TARGET_ENV"), cfg.DeployEnv), "Target object-store environment name")
	verify := fs.Bool("verify", true, "Verify copied objects after upload")

	sourceFlags := bindObjectStoreFlags(fs, "source", storage.ObjectStoreConfigFromAppConfig(cfg))
	targetDefaults := storage.ObjectStoreConfig{
		Type:              firstNonEmpty(os.Getenv("TARGET_OBJECT_STORE_TYPE"), "r2"),
		R2Bucket:          firstNonEmpty(os.Getenv("TARGET_R2_BUCKET"), cfg.R2Bucket),
		R2Prefix:          firstNonEmpty(os.Getenv("TARGET_R2_PREFIX"), cfg.R2Prefix),
		R2Endpoint:        firstNonEmpty(os.Getenv("TARGET_R2_ENDPOINT"), cfg.R2Endpoint),
		R2Region:          firstNonEmpty(os.Getenv("TARGET_R2_REGION"), cfg.R2Region),
		R2AccessKeyID:     firstNonEmpty(os.Getenv("TARGET_R2_ACCESS_KEY_ID"), cfg.R2AccessKeyID),
		R2SecretAccessKey: firstNonEmpty(os.Getenv("TARGET_R2_SECRET_ACCESS_KEY"), cfg.R2SecretAccessKey),
		R2UsePathStyle:    envBoolWithDefault("TARGET_R2_USE_PATH_STYLE", cfg.R2UsePathStyle),
	}
	targetFlags := bindObjectStoreFlags(fs, "target", targetDefaults)
	fs.Parse(args)

	ctx := context.Background()
	pool := mustPool(ctx, *dsn)
	defer pool.Close()

	sourceCfg := sourceFlags.Config()
	targetCfg := targetFlags.Config()
	if err := validateObjectStoreMigrationConfig("source", sourceCfg, *sourceEnv); err != nil {
		log.Fatalf("source object store: %v", err)
	}
	if err := validateObjectStoreMigrationConfig("target", targetCfg, *targetEnv); err != nil {
		log.Fatalf("target object store: %v", err)
	}
	if sameObjectStoreConfig(sourceCfg, targetCfg) {
		log.Fatalf("source and target object-store configurations are identical")
	}
	if !strings.EqualFold(targetCfg.Type, "r2") {
		log.Fatalf("target object-store type must be r2 for cutover")
	}

	sourceStore, closeSource, err := storage.BuildObjectStore(ctx, sourceCfg)
	if err != nil {
		log.Fatalf("build source object store: %v", err)
	}
	defer closeSource()

	targetStore, closeTarget, err := storage.BuildObjectStore(ctx, targetCfg)
	if err != nil {
		log.Fatalf("build target object store: %v", err)
	}
	defer closeTarget()

	plan, err := buildReferencedObjectPlan(ctx, pool, *namespace, sourceStore)
	if err != nil {
		log.Fatalf("build object reference plan: %v", err)
	}

	stats, err := copyReferencedObjects(ctx, sourceStore, targetStore, plan)
	if err != nil {
		log.Fatalf("copy object store: %v", err)
	}
	log.Printf("Copy complete: current_manifests copied=%d skipped=%d versioned_manifests copied=%d skipped=%d blocks copied=%d skipped=%d",
		stats.CurrentManifestCopied,
		stats.CurrentManifestSkipped,
		stats.VersionedManifestCopied,
		stats.VersionedManifestSkipped,
		stats.BlocksCopied,
		stats.BlocksSkipped,
	)

	if *verify {
		if err := verifyReferencedObjects(ctx, sourceStore, targetStore, plan); err != nil {
			log.Fatalf("verify copied object store: %v", err)
		}
		log.Printf("Verification complete")
	}
}

func cmdVerifyObjectStore(args []string) {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	fs := flag.NewFlagSet("verify-object-store", flag.ExitOnError)
	dsn := fs.String("dsn", firstNonEmpty(os.Getenv("POSTGRES_DSN"), cfg.PostgresDSN), "Postgres DSN")
	namespace := fs.String("namespace", "core", "Native storage namespace")
	sourceEnv := fs.String("source-env", os.Getenv("SOURCE_ENV"), "Source object-store environment name (required for R2 sources)")
	targetEnv := fs.String("target-env", firstNonEmpty(os.Getenv("TARGET_ENV"), cfg.DeployEnv), "Target object-store environment name")

	sourceFlags := bindObjectStoreFlags(fs, "source", storage.ObjectStoreConfigFromAppConfig(cfg))
	targetDefaults := storage.ObjectStoreConfig{
		Type:              firstNonEmpty(os.Getenv("TARGET_OBJECT_STORE_TYPE"), "r2"),
		R2Bucket:          firstNonEmpty(os.Getenv("TARGET_R2_BUCKET"), cfg.R2Bucket),
		R2Prefix:          firstNonEmpty(os.Getenv("TARGET_R2_PREFIX"), cfg.R2Prefix),
		R2Endpoint:        firstNonEmpty(os.Getenv("TARGET_R2_ENDPOINT"), cfg.R2Endpoint),
		R2Region:          firstNonEmpty(os.Getenv("TARGET_R2_REGION"), cfg.R2Region),
		R2AccessKeyID:     firstNonEmpty(os.Getenv("TARGET_R2_ACCESS_KEY_ID"), cfg.R2AccessKeyID),
		R2SecretAccessKey: firstNonEmpty(os.Getenv("TARGET_R2_SECRET_ACCESS_KEY"), cfg.R2SecretAccessKey),
		R2UsePathStyle:    envBoolWithDefault("TARGET_R2_USE_PATH_STYLE", cfg.R2UsePathStyle),
	}
	targetFlags := bindObjectStoreFlags(fs, "target", targetDefaults)
	fs.Parse(args)

	ctx := context.Background()
	pool := mustPool(ctx, *dsn)
	defer pool.Close()

	sourceCfg := sourceFlags.Config()
	targetCfg := targetFlags.Config()
	if err := validateObjectStoreMigrationConfig("source", sourceCfg, *sourceEnv); err != nil {
		log.Fatalf("source object store: %v", err)
	}
	if err := validateObjectStoreMigrationConfig("target", targetCfg, *targetEnv); err != nil {
		log.Fatalf("target object store: %v", err)
	}
	if !strings.EqualFold(targetCfg.Type, "r2") {
		log.Fatalf("target object-store type must be r2 for cutover")
	}

	sourceStore, closeSource, err := storage.BuildObjectStore(ctx, sourceCfg)
	if err != nil {
		log.Fatalf("build source object store: %v", err)
	}
	defer closeSource()

	targetStore, closeTarget, err := storage.BuildObjectStore(ctx, targetCfg)
	if err != nil {
		log.Fatalf("build target object store: %v", err)
	}
	defer closeTarget()

	plan, err := buildReferencedObjectPlan(ctx, pool, *namespace, sourceStore)
	if err != nil {
		log.Fatalf("build object reference plan: %v", err)
	}
	if err := verifyReferencedObjects(ctx, sourceStore, targetStore, plan); err != nil {
		log.Fatalf("verify object store: %v", err)
	}
	log.Printf("Verification complete: current_manifests=%d versioned_manifests=%d blocks=%d",
		len(plan.CurrentManifestKeys), len(plan.VersionedManifestKeys), len(plan.BlockKeys))
}

func bindObjectStoreFlags(fs *flag.FlagSet, label string, defaults storage.ObjectStoreConfig) objectStoreFlagSet {
	upper := strings.ToUpper(label)
	flagPrefix := label + "-"
	return objectStoreFlagSet{
		storeType:          fs.String(flagPrefix+"object-store-type", firstNonEmpty(os.Getenv(upper+"_OBJECT_STORE_TYPE"), defaults.Type), "Object-store type"),
		filesystemDir:      fs.String(flagPrefix+"object-store-dir", firstNonEmpty(os.Getenv(upper+"_OBJECT_STORE_DIR"), defaults.FilesystemDir), "Filesystem object-store root"),
		gcsBucket:          fs.String(flagPrefix+"gcs-bucket", firstNonEmpty(os.Getenv(upper+"_GCS_BUCKET"), defaults.GCSBucket), "GCS bucket"),
		gcsEndpoint:        fs.String(flagPrefix+"gcs-endpoint", firstNonEmpty(os.Getenv(upper+"_GCS_ENDPOINT"), defaults.GCSEndpoint), "GCS endpoint"),
		gcsCredentialsFile: fs.String(flagPrefix+"gcs-credentials-file", firstNonEmpty(os.Getenv(upper+"_GCS_CREDENTIALS_FILE"), defaults.GCSCredentialsFile), "GCS credentials file"),
		gcsCredentialsJSON: fs.String(flagPrefix+"gcs-credentials-json", firstNonEmpty(os.Getenv(upper+"_GCS_CREDENTIALS_JSON"), defaults.GCSCredentialsJSON), "GCS credentials JSON"),
		gcsDisableAuth:     fs.Bool(flagPrefix+"gcs-disable-auth", envBoolWithDefault(upper+"_GCS_DISABLE_AUTH", defaults.GCSDisableAuth), "Disable GCS auth"),
		r2Bucket:           fs.String(flagPrefix+"r2-bucket", firstNonEmpty(os.Getenv(upper+"_R2_BUCKET"), defaults.R2Bucket), "R2 bucket"),
		r2Prefix:           fs.String(flagPrefix+"r2-prefix", firstNonEmpty(os.Getenv(upper+"_R2_PREFIX"), defaults.R2Prefix), "R2 prefix"),
		r2Endpoint:         fs.String(flagPrefix+"r2-endpoint", firstNonEmpty(os.Getenv(upper+"_R2_ENDPOINT"), defaults.R2Endpoint), "R2 S3 endpoint"),
		r2Region:           fs.String(flagPrefix+"r2-region", firstNonEmpty(os.Getenv(upper+"_R2_REGION"), defaults.R2Region), "R2 region"),
		r2AccessKeyID:      fs.String(flagPrefix+"r2-access-key-id", firstNonEmpty(os.Getenv(upper+"_R2_ACCESS_KEY_ID"), defaults.R2AccessKeyID), "R2 access key id"),
		r2SecretAccessKey:  fs.String(flagPrefix+"r2-secret-access-key", firstNonEmpty(os.Getenv(upper+"_R2_SECRET_ACCESS_KEY"), defaults.R2SecretAccessKey), "R2 secret access key"),
		r2UsePathStyle:     fs.Bool(flagPrefix+"r2-use-path-style", envBoolWithDefault(upper+"_R2_USE_PATH_STYLE", defaults.R2UsePathStyle), "Use path-style R2 requests"),
	}
}

func (f objectStoreFlagSet) Config() storage.ObjectStoreConfig {
	return storage.ObjectStoreConfig{
		Type:               strings.TrimSpace(*f.storeType),
		FilesystemDir:      strings.TrimSpace(*f.filesystemDir),
		GCSBucket:          strings.TrimSpace(*f.gcsBucket),
		GCSEndpoint:        strings.TrimSpace(*f.gcsEndpoint),
		GCSCredentialsFile: strings.TrimSpace(*f.gcsCredentialsFile),
		GCSCredentialsJSON: strings.TrimSpace(*f.gcsCredentialsJSON),
		GCSDisableAuth:     *f.gcsDisableAuth,
		R2Bucket:           strings.TrimSpace(*f.r2Bucket),
		R2Prefix:           strings.TrimSpace(*f.r2Prefix),
		R2Endpoint:         strings.TrimSpace(*f.r2Endpoint),
		R2Region:           strings.TrimSpace(*f.r2Region),
		R2AccessKeyID:      strings.TrimSpace(*f.r2AccessKeyID),
		R2SecretAccessKey:  strings.TrimSpace(*f.r2SecretAccessKey),
		R2UsePathStyle:     *f.r2UsePathStyle,
	}
}

func validateObjectStoreMigrationConfig(label string, cfg storage.ObjectStoreConfig, env string) error {
	storeType := strings.ToLower(strings.TrimSpace(cfg.Type))
	if storeType == "" {
		return fmt.Errorf("%s object-store type is required", label)
	}
	if storeType != "r2" {
		return nil
	}
	normalizedEnv := strings.ToLower(strings.TrimSpace(env))
	if normalizedEnv == "" {
		return fmt.Errorf("%s env is required for R2 cutover", label)
	}
	prefix := strings.Trim(strings.TrimSpace(cfg.R2Prefix), "/")
	if prefix == "" {
		return fmt.Errorf("%s R2 prefix is required", label)
	}
	if normalizedEnv != "" && prefix != normalizedEnv && !strings.HasPrefix(prefix, normalizedEnv+"/") {
		return fmt.Errorf("%s R2 prefix must match env namespace", label)
	}
	return nil
}

func sameObjectStoreConfig(left, right storage.ObjectStoreConfig) bool {
	return strings.EqualFold(strings.TrimSpace(left.Type), strings.TrimSpace(right.Type)) &&
		strings.TrimSpace(left.FilesystemDir) == strings.TrimSpace(right.FilesystemDir) &&
		strings.TrimSpace(left.GCSBucket) == strings.TrimSpace(right.GCSBucket) &&
		strings.TrimSpace(left.GCSEndpoint) == strings.TrimSpace(right.GCSEndpoint) &&
		strings.TrimSpace(left.R2Bucket) == strings.TrimSpace(right.R2Bucket) &&
		strings.Trim(strings.TrimSpace(left.R2Prefix), "/") == strings.Trim(strings.TrimSpace(right.R2Prefix), "/") &&
		strings.TrimSpace(left.R2Endpoint) == strings.TrimSpace(right.R2Endpoint)
}

func buildReferencedObjectPlan(ctx context.Context, pool *pgxpool.Pool, namespace string, sourceStore storage.ObjectStore) (*referencedObjectPlan, error) {
	currentManifestKeys := make(map[string]struct{})
	versionedManifestHashes := make(map[string]struct{})
	blockKeys := make(map[string]struct{})

	rows, err := pool.Query(ctx, `
		SELECT slice_id, path, hash
		FROM file_manifests
		ORDER BY slice_id, path
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var sliceID string
		var filePath string
		var manifestHash string
		if err := rows.Scan(&sliceID, &filePath, &manifestHash); err != nil {
			return nil, err
		}
		key := objectKey(namespace, "manifests", sliceID, filePath)
		manifest, err := readManifestFromStore(ctx, sourceStore, key)
		if err != nil {
			return nil, fmt.Errorf("read current manifest %s: %w", key, err)
		}
		currentManifestKeys[key] = struct{}{}
		addManifestBlockKeys(blockKeys, namespace, manifest)
		if hash := strings.TrimSpace(manifest.Hash); hash != "" {
			versionedManifestHashes[hash] = struct{}{}
		}
		if hash := strings.TrimSpace(manifestHash); hash != "" {
			versionedManifestHashes[hash] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	changeRows, err := pool.Query(ctx, `
		SELECT old_hash, new_hash
		FROM file_changes
	`)
	if err != nil {
		return nil, err
	}
	defer changeRows.Close()

	for changeRows.Next() {
		var oldHash string
		var newHash string
		if err := changeRows.Scan(&oldHash, &newHash); err != nil {
			return nil, err
		}
		if hash := strings.TrimSpace(oldHash); hash != "" {
			versionedManifestHashes[hash] = struct{}{}
		}
		if hash := strings.TrimSpace(newHash); hash != "" {
			versionedManifestHashes[hash] = struct{}{}
		}
	}
	if err := changeRows.Err(); err != nil {
		return nil, err
	}

	versionedKeys := make([]string, 0, len(versionedManifestHashes))
	for hash := range versionedManifestHashes {
		key := objectKey(namespace, "versioned_manifests", hash)
		manifest, err := readManifestFromStore(ctx, sourceStore, key)
		if err != nil {
			return nil, fmt.Errorf("read versioned manifest %s: %w", key, err)
		}
		addManifestBlockKeys(blockKeys, namespace, manifest)
		versionedKeys = append(versionedKeys, key)
	}

	plan := &referencedObjectPlan{
		CurrentManifestKeys:   sortedStringSet(currentManifestKeys),
		VersionedManifestKeys: versionedKeys,
		BlockKeys:             sortedStringSet(blockKeys),
	}
	sort.Strings(plan.VersionedManifestKeys)
	return plan, nil
}

func copyReferencedObjects(ctx context.Context, sourceStore, targetStore storage.ObjectStore, plan *referencedObjectPlan) (*objectCopyStats, error) {
	stats := &objectCopyStats{}
	for _, key := range plan.CurrentManifestKeys {
		copied, err := copyObjectIfNeeded(ctx, sourceStore, targetStore, key)
		if err != nil {
			return nil, err
		}
		if copied {
			stats.CurrentManifestCopied++
		} else {
			stats.CurrentManifestSkipped++
		}
	}
	for _, key := range plan.VersionedManifestKeys {
		copied, err := copyObjectIfNeeded(ctx, sourceStore, targetStore, key)
		if err != nil {
			return nil, err
		}
		if copied {
			stats.VersionedManifestCopied++
		} else {
			stats.VersionedManifestSkipped++
		}
	}
	for _, key := range plan.BlockKeys {
		copied, err := copyObjectIfNeeded(ctx, sourceStore, targetStore, key)
		if err != nil {
			return nil, err
		}
		if copied {
			stats.BlocksCopied++
		} else {
			stats.BlocksSkipped++
		}
	}
	return stats, nil
}

func verifyReferencedObjects(ctx context.Context, sourceStore, targetStore storage.ObjectStore, plan *referencedObjectPlan) error {
	for _, key := range plan.CurrentManifestKeys {
		if err := verifyObjectEqual(ctx, sourceStore, targetStore, key); err != nil {
			return err
		}
	}
	for _, key := range plan.VersionedManifestKeys {
		if err := verifyObjectEqual(ctx, sourceStore, targetStore, key); err != nil {
			return err
		}
	}
	for _, key := range plan.BlockKeys {
		if err := verifyObjectEqual(ctx, sourceStore, targetStore, key); err != nil {
			return err
		}
	}
	return nil
}

func copyObjectIfNeeded(ctx context.Context, sourceStore, targetStore storage.ObjectStore, key string) (bool, error) {
	sourceBody, err := sourceStore.GetObject(ctx, key)
	if err != nil {
		return false, fmt.Errorf("read source %s: %w", key, err)
	}
	targetBody, err := targetStore.GetObject(ctx, key)
	if err == nil {
		if bytes.Equal(sourceBody, targetBody) {
			return false, nil
		}
	} else if err != storage.ErrEntryNotFound {
		return false, fmt.Errorf("read target %s: %w", key, err)
	}
	if err := targetStore.PutObject(ctx, key, sourceBody); err != nil {
		return false, fmt.Errorf("write target %s: %w", key, err)
	}
	return true, nil
}

func verifyObjectEqual(ctx context.Context, sourceStore, targetStore storage.ObjectStore, key string) error {
	sourceBody, err := sourceStore.GetObject(ctx, key)
	if err != nil {
		return fmt.Errorf("read source %s: %w", key, err)
	}
	targetBody, err := targetStore.GetObject(ctx, key)
	if err != nil {
		return fmt.Errorf("read target %s: %w", key, err)
	}
	if !bytes.Equal(sourceBody, targetBody) {
		return fmt.Errorf("payload mismatch for %s", key)
	}
	return nil
}

func readManifestFromStore(ctx context.Context, store storage.ObjectStore, key string) (*models.FileManifest, error) {
	raw, err := store.GetObject(ctx, key)
	if err != nil {
		return nil, err
	}
	var manifest models.FileManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func addManifestBlockKeys(blockKeys map[string]struct{}, namespace string, manifest *models.FileManifest) {
	if manifest == nil {
		return
	}
	for _, block := range manifest.Blocks {
		hash := strings.TrimSpace(block.Hash)
		if hash == "" {
			continue
		}
		blockKeys[objectKey(namespace, "blocks", hash)] = struct{}{}
	}
}

func sortedStringSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func envBoolWithDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return strings.EqualFold(value, "1") || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes")
}
