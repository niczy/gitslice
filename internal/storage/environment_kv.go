package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/niczy/gitslice/internal/models"
)

var (
	envKVProfileRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
	envKVKeyRE     = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]{0,127}$`)
)

func normalizeEnvironmentKVEntry(entry *models.EnvironmentKVEntry, existing *models.EnvironmentKVEntry) (*models.EnvironmentKVEntry, error) {
	if entry == nil {
		return nil, ErrInvalidInput
	}
	homeID := strings.TrimSpace(entry.HomeID)
	if homeID == "" {
		return nil, ErrInvalidInput
	}
	sliceID := strings.TrimSpace(entry.SliceID)
	profile, err := normalizeEnvironmentKVProfile(entry.Profile)
	if err != nil {
		return nil, err
	}
	key, err := normalizeEnvironmentKVKey(entry.Key)
	if err != nil {
		return nil, err
	}
	class, err := normalizeEnvironmentKVClass(entry.Class)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	out := *entry
	out.HomeID = homeID
	out.SliceID = sliceID
	out.SliceSlug = strings.TrimSpace(entry.SliceSlug)
	if sliceID == "" {
		out.SliceSlug = ""
	}
	out.Profile = profile
	out.Key = key
	out.Class = class
	out.ValueHash = hashEnvironmentKVValue(entry.Value)
	out.DeletedAt = nil
	if existing != nil {
		out.ID = existing.ID
		out.CreatedBy = existing.CreatedBy
		out.CreatedAt = existing.CreatedAt
		out.Version = existing.Version + 1
	} else {
		out.ID = strings.TrimSpace(entry.ID)
		if out.ID == "" {
			out.ID = "envkv_" + strings.ReplaceAll(uuid.New().String(), "-", "")
		}
		out.CreatedBy = strings.TrimSpace(entry.CreatedBy)
		if out.CreatedAt.IsZero() {
			out.CreatedAt = now
		}
		if out.Version <= 0 {
			out.Version = 1
		}
	}
	out.UpdatedBy = strings.TrimSpace(entry.UpdatedBy)
	if out.UpdatedBy == "" {
		out.UpdatedBy = out.CreatedBy
	}
	if out.UpdatedAt.IsZero() {
		out.UpdatedAt = now
	}
	return &out, nil
}

func normalizeEnvironmentKVFilter(filter models.EnvironmentKVFilter) (models.EnvironmentKVFilter, error) {
	filter.HomeID = strings.TrimSpace(filter.HomeID)
	filter.SliceID = strings.TrimSpace(filter.SliceID)
	if filter.HomeID == "" {
		return filter, ErrInvalidInput
	}
	if strings.TrimSpace(filter.Profile) != "" {
		profile, err := normalizeEnvironmentKVProfile(filter.Profile)
		if err != nil {
			return filter, err
		}
		filter.Profile = profile
	}
	if strings.TrimSpace(string(filter.Class)) != "" {
		class, err := normalizeEnvironmentKVClass(filter.Class)
		if err != nil {
			return filter, err
		}
		filter.Class = class
	}
	if strings.TrimSpace(filter.Key) != "" {
		key, err := normalizeEnvironmentKVKey(filter.Key)
		if err != nil {
			return filter, err
		}
		filter.Key = key
	}
	return filter, nil
}

func normalizeEnvironmentKVProfile(profile string) (string, error) {
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" {
		profile = "default"
	}
	if !envKVProfileRE.MatchString(profile) {
		return "", ErrInvalidInput
	}
	return profile, nil
}

func normalizeEnvironmentKVKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if !envKVKeyRE.MatchString(key) {
		return "", ErrInvalidInput
	}
	return key, nil
}

func normalizeEnvironmentKVClass(class models.EnvironmentKVClass) (models.EnvironmentKVClass, error) {
	switch models.EnvironmentKVClass(strings.ToLower(strings.TrimSpace(string(class)))) {
	case models.EnvironmentKVClassSecret:
		return models.EnvironmentKVClassSecret, nil
	case models.EnvironmentKVClassValue:
		return models.EnvironmentKVClassValue, nil
	default:
		return "", ErrInvalidInput
	}
}

func environmentKVMapKey(homeID, sliceID, profile string, class models.EnvironmentKVClass, key string) string {
	return strings.Join([]string{
		strings.TrimSpace(homeID),
		strings.TrimSpace(sliceID),
		strings.TrimSpace(profile),
		string(class),
		strings.TrimSpace(key),
	}, "\x00")
}

func hashEnvironmentKVValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func cloneEnvironmentKVEntry(entry *models.EnvironmentKVEntry) *models.EnvironmentKVEntry {
	if entry == nil {
		return nil
	}
	out := *entry
	if entry.DeletedAt != nil {
		deletedAt := *entry.DeletedAt
		out.DeletedAt = &deletedAt
	}
	return &out
}

func (s *InMemoryStorage) UpsertEnvironmentKV(ctx context.Context, entry *models.EnvironmentKVEntry) (*models.EnvironmentKVEntry, error) {
	_ = ctx
	if entry == nil {
		return nil, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	profile, err := normalizeEnvironmentKVProfile(entry.Profile)
	if err != nil {
		return nil, err
	}
	class, err := normalizeEnvironmentKVClass(entry.Class)
	if err != nil {
		return nil, err
	}
	key, err := normalizeEnvironmentKVKey(entry.Key)
	if err != nil {
		return nil, err
	}
	existing := s.environmentKV[environmentKVMapKey(entry.HomeID, entry.SliceID, profile, class, key)]
	normalized, err := normalizeEnvironmentKVEntry(entry, existing)
	if err != nil {
		return nil, err
	}
	s.environmentKV[environmentKVMapKey(normalized.HomeID, normalized.SliceID, normalized.Profile, normalized.Class, normalized.Key)] = cloneEnvironmentKVEntry(normalized)
	return cloneEnvironmentKVEntry(normalized), nil
}

func (s *InMemoryStorage) ListEnvironmentKV(ctx context.Context, filter models.EnvironmentKVFilter) ([]*models.EnvironmentKVEntry, error) {
	_ = ctx
	normalized, err := normalizeEnvironmentKVFilter(filter)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*models.EnvironmentKVEntry, 0)
	for _, entry := range s.environmentKV {
		if entry == nil || entry.DeletedAt != nil {
			continue
		}
		if entry.HomeID != normalized.HomeID {
			continue
		}
		if normalized.IncludeHome {
			if entry.SliceID != "" && entry.SliceID != normalized.SliceID {
				continue
			}
		} else if entry.SliceID != normalized.SliceID {
			continue
		}
		if normalized.Profile != "" && entry.Profile != normalized.Profile {
			continue
		}
		if normalized.Class != "" && entry.Class != normalized.Class {
			continue
		}
		if normalized.Key != "" && entry.Key != normalized.Key {
			continue
		}
		out = append(out, cloneEnvironmentKVEntry(entry))
	}
	sortEnvironmentKVEntries(out)
	return out, nil
}

func (s *InMemoryStorage) DeleteEnvironmentKV(ctx context.Context, filter models.EnvironmentKVFilter) error {
	_ = ctx
	normalized, err := normalizeEnvironmentKVFilter(filter)
	if err != nil {
		return err
	}
	if normalized.Profile == "" || normalized.Class == "" || normalized.Key == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := environmentKVMapKey(normalized.HomeID, normalized.SliceID, normalized.Profile, normalized.Class, normalized.Key)
	if _, ok := s.environmentKV[key]; !ok {
		return ErrEntryNotFound
	}
	delete(s.environmentKV, key)
	return nil
}

func (s *InMemoryStorage) ResolveEnvironmentKV(ctx context.Context, homeID, sliceID, profile string, class models.EnvironmentKVClass, key string) (*models.EnvironmentKVEntry, error) {
	filter := models.EnvironmentKVFilter{
		HomeID:      homeID,
		SliceID:     sliceID,
		Class:       class,
		Key:         key,
		IncludeHome: true,
	}
	candidates, err := environmentKVResolutionCandidates(filter, profile)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, candidate := range candidates {
		entry := s.environmentKV[environmentKVMapKey(candidate.HomeID, candidate.SliceID, candidate.Profile, candidate.Class, candidate.Key)]
		if entry != nil && entry.DeletedAt == nil {
			return cloneEnvironmentKVEntry(entry), nil
		}
	}
	return nil, ErrEntryNotFound
}

func environmentKVResolutionCandidates(filter models.EnvironmentKVFilter, profile string) ([]models.EnvironmentKVFilter, error) {
	normalized, err := normalizeEnvironmentKVFilter(filter)
	if err != nil {
		return nil, err
	}
	if normalized.Class == "" || normalized.Key == "" {
		return nil, ErrInvalidInput
	}
	activeProfile, err := normalizeEnvironmentKVProfile(profile)
	if err != nil {
		return nil, err
	}
	profiles := []string{activeProfile}
	if activeProfile != "default" {
		profiles = append(profiles, "default")
	}
	candidates := make([]models.EnvironmentKVFilter, 0, 4)
	for _, candidateProfile := range profiles {
		if normalized.SliceID != "" {
			candidate := normalized
			candidate.Profile = candidateProfile
			candidate.IncludeHome = false
			candidates = append(candidates, candidate)
		}
	}
	for _, candidateProfile := range profiles {
		candidate := normalized
		candidate.SliceID = ""
		candidate.Profile = candidateProfile
		candidate.IncludeHome = false
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func sortEnvironmentKVEntries(entries []*models.EnvironmentKVEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		left := entries[i]
		right := entries[j]
		if left == nil || right == nil {
			return left != nil
		}
		for _, pair := range [][2]string{
			{left.HomeID, right.HomeID},
			{left.SliceID, right.SliceID},
			{left.Profile, right.Profile},
			{string(left.Class), string(right.Class)},
			{left.Key, right.Key},
		} {
			if pair[0] != pair[1] {
				return pair[0] < pair[1]
			}
		}
		return left.UpdatedAt.After(right.UpdatedAt)
	})
}

type environmentKVRow interface {
	Scan(dest ...any) error
}

type environmentKVExecutor interface {
	queryable
	execable
}

func (s *PostgresNativeStorage) UpsertEnvironmentKV(ctx context.Context, entry *models.EnvironmentKVEntry) (*models.EnvironmentKVEntry, error) {
	return s.upsertEnvironmentKV(ctx, s.pool, entry)
}

func (s *postgresNativeTxView) UpsertEnvironmentKV(ctx context.Context, entry *models.EnvironmentKVEntry) (*models.EnvironmentKVEntry, error) {
	return s.PostgresNativeStorage.upsertEnvironmentKV(ctx, s.tx, entry)
}

func (s *PostgresNativeStorage) upsertEnvironmentKV(ctx context.Context, exec environmentKVExecutor, entry *models.EnvironmentKVEntry) (*models.EnvironmentKVEntry, error) {
	ctx = ensureCtx(ctx)
	if entry == nil {
		return nil, ErrInvalidInput
	}
	var existing *models.EnvironmentKVEntry
	filter := models.EnvironmentKVFilter{
		HomeID:  entry.HomeID,
		SliceID: entry.SliceID,
		Profile: entry.Profile,
		Class:   entry.Class,
		Key:     entry.Key,
	}
	if current, err := s.getEnvironmentKV(ctx, exec, filter); err == nil {
		existing = current
	} else if !errors.Is(err, ErrEntryNotFound) {
		return nil, err
	}
	normalized, err := normalizeEnvironmentKVEntry(entry, existing)
	if err != nil {
		return nil, err
	}
	row := exec.QueryRow(ctx, `
		INSERT INTO environment_kv_entries (
			id, home_id, slice_id, slice_slug, profile, key, class, encrypted_value,
			value_hash, version, created_by, updated_by, created_at, updated_at, deleted_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::bytea, $9, $10, $11, $12, $13, $14, NULL)
		ON CONFLICT (home_id, slice_id, profile, key, class) WHERE deleted_at IS NULL
		DO UPDATE SET
			slice_slug = EXCLUDED.slice_slug,
			encrypted_value = EXCLUDED.encrypted_value,
			value_hash = EXCLUDED.value_hash,
			version = environment_kv_entries.version + 1,
			updated_by = EXCLUDED.updated_by,
			updated_at = EXCLUDED.updated_at,
			deleted_at = NULL
		RETURNING id, home_id, slice_id, slice_slug, profile, key, class, encrypted_value,
		          value_hash, version, created_by, updated_by, created_at, updated_at, deleted_at
	`, normalized.ID, normalized.HomeID, normalized.SliceID, normalized.SliceSlug, normalized.Profile, normalized.Key,
		string(normalized.Class), []byte(normalized.Value), normalized.ValueHash, normalized.Version, normalized.CreatedBy,
		normalized.UpdatedBy, normalized.CreatedAt, normalized.UpdatedAt)
	return scanEnvironmentKVEntry(row)
}

func (s *PostgresNativeStorage) ListEnvironmentKV(ctx context.Context, filter models.EnvironmentKVFilter) ([]*models.EnvironmentKVEntry, error) {
	return s.listEnvironmentKV(ctx, s.pool, filter)
}

func (s *postgresNativeTxView) ListEnvironmentKV(ctx context.Context, filter models.EnvironmentKVFilter) ([]*models.EnvironmentKVEntry, error) {
	return s.PostgresNativeStorage.listEnvironmentKV(ctx, s.tx, filter)
}

func (s *PostgresNativeStorage) listEnvironmentKV(ctx context.Context, q queryable, filter models.EnvironmentKVFilter) ([]*models.EnvironmentKVEntry, error) {
	ctx = ensureCtx(ctx)
	normalized, err := normalizeEnvironmentKVFilter(filter)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `
		SELECT id, home_id, slice_id, slice_slug, profile, key, class, encrypted_value,
		       value_hash, version, created_by, updated_by, created_at, updated_at, deleted_at
		FROM environment_kv_entries
		WHERE deleted_at IS NULL
		  AND home_id = $1
		  AND (($2::boolean AND (slice_id = $3 OR slice_id = '')) OR (NOT $2::boolean AND slice_id = $3))
		  AND ($4 = '' OR profile = $4)
		  AND ($5 = '' OR class = $5)
		  AND ($6 = '' OR key = $6)
		ORDER BY home_id, slice_id, profile, class, key
	`, normalized.HomeID, normalized.IncludeHome, normalized.SliceID, normalized.Profile, string(normalized.Class), normalized.Key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*models.EnvironmentKVEntry, 0)
	for rows.Next() {
		entry, err := scanEnvironmentKVEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *PostgresNativeStorage) DeleteEnvironmentKV(ctx context.Context, filter models.EnvironmentKVFilter) error {
	return s.deleteEnvironmentKV(ctx, s.pool, filter)
}

func (s *postgresNativeTxView) DeleteEnvironmentKV(ctx context.Context, filter models.EnvironmentKVFilter) error {
	return s.PostgresNativeStorage.deleteEnvironmentKV(ctx, s.tx, filter)
}

func (s *PostgresNativeStorage) deleteEnvironmentKV(ctx context.Context, exec execable, filter models.EnvironmentKVFilter) error {
	ctx = ensureCtx(ctx)
	normalized, err := normalizeEnvironmentKVFilter(filter)
	if err != nil {
		return err
	}
	if normalized.Profile == "" || normalized.Class == "" || normalized.Key == "" {
		return ErrInvalidInput
	}
	tag, err := exec.Exec(ctx, `
		UPDATE environment_kv_entries
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE deleted_at IS NULL
		  AND home_id = $1
		  AND slice_id = $2
		  AND profile = $3
		  AND class = $4
		  AND key = $5
	`, normalized.HomeID, normalized.SliceID, normalized.Profile, string(normalized.Class), normalized.Key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) ResolveEnvironmentKV(ctx context.Context, homeID, sliceID, profile string, class models.EnvironmentKVClass, key string) (*models.EnvironmentKVEntry, error) {
	return s.resolveEnvironmentKV(ctx, s.pool, homeID, sliceID, profile, class, key)
}

func (s *postgresNativeTxView) ResolveEnvironmentKV(ctx context.Context, homeID, sliceID, profile string, class models.EnvironmentKVClass, key string) (*models.EnvironmentKVEntry, error) {
	return s.PostgresNativeStorage.resolveEnvironmentKV(ctx, s.tx, homeID, sliceID, profile, class, key)
}

func (s *PostgresNativeStorage) resolveEnvironmentKV(ctx context.Context, q queryable, homeID, sliceID, profile string, class models.EnvironmentKVClass, key string) (*models.EnvironmentKVEntry, error) {
	ctx = ensureCtx(ctx)
	candidates, err := environmentKVResolutionCandidates(models.EnvironmentKVFilter{
		HomeID:      homeID,
		SliceID:     sliceID,
		Class:       class,
		Key:         key,
		IncludeHome: true,
	}, profile)
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		entry, err := s.getEnvironmentKV(ctx, q, candidate)
		if err == nil {
			return entry, nil
		}
		if !errors.Is(err, ErrEntryNotFound) {
			return nil, err
		}
	}
	return nil, ErrEntryNotFound
}

func (s *PostgresNativeStorage) getEnvironmentKV(ctx context.Context, q queryable, filter models.EnvironmentKVFilter) (*models.EnvironmentKVEntry, error) {
	normalized, err := normalizeEnvironmentKVFilter(filter)
	if err != nil {
		return nil, err
	}
	if normalized.Profile == "" || normalized.Class == "" || normalized.Key == "" {
		return nil, ErrInvalidInput
	}
	row := q.QueryRow(ctx, `
		SELECT id, home_id, slice_id, slice_slug, profile, key, class, encrypted_value,
		       value_hash, version, created_by, updated_by, created_at, updated_at, deleted_at
		FROM environment_kv_entries
		WHERE deleted_at IS NULL
		  AND home_id = $1
		  AND slice_id = $2
		  AND profile = $3
		  AND class = $4
		  AND key = $5
	`, normalized.HomeID, normalized.SliceID, normalized.Profile, string(normalized.Class), normalized.Key)
	entry, err := scanEnvironmentKVEntry(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return entry, nil
}

func scanEnvironmentKVEntry(row environmentKVRow) (*models.EnvironmentKVEntry, error) {
	var entry models.EnvironmentKVEntry
	var class string
	var value []byte
	if err := row.Scan(
		&entry.ID, &entry.HomeID, &entry.SliceID, &entry.SliceSlug, &entry.Profile, &entry.Key, &class, &value,
		&entry.ValueHash, &entry.Version, &entry.CreatedBy, &entry.UpdatedBy, &entry.CreatedAt, &entry.UpdatedAt, &entry.DeletedAt,
	); err != nil {
		return nil, err
	}
	entry.Class = models.EnvironmentKVClass(class)
	entry.Value = string(value)
	return &entry, nil
}
