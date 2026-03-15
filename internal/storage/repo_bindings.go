package storage

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/niczy/gitslice/internal/models"
)

func copyRepoBinding(binding *models.RepoBinding) *models.RepoBinding {
	if binding == nil {
		return nil
	}
	out := *binding
	return &out
}

func repoBindingPathKey(sliceID, rootPath string) string {
	return strings.TrimSpace(sliceID) + ":" + cleanRelativePath(rootPath)
}

func (s *InMemoryStorage) PutRepoBinding(ctx context.Context, binding *models.RepoBinding) error {
	_ = ctx
	if binding == nil {
		return ErrInvalidInput
	}

	sliceID := strings.TrimSpace(binding.SliceID)
	owner := strings.TrimSpace(binding.OwnerUsername)
	rootPath := cleanRelativePath(binding.RootPath)
	if sliceID == "" || owner == "" || rootPath == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.slices[sliceID]; !ok {
		return ErrSliceNotFound
	}
	if _, ok := s.users[owner]; !ok {
		return ErrEntryNotFound
	}

	now := time.Now()
	stored := copyRepoBinding(binding)
	if strings.TrimSpace(stored.BindingID) == "" {
		stored.BindingID = repoBindingPathKey(sliceID, rootPath)
	}
	stored.SliceID = sliceID
	stored.OwnerUsername = owner
	stored.RootPath = rootPath
	if strings.TrimSpace(stored.Provider) == "" {
		stored.Provider = "github"
	}
	if stored.CreatedAt.IsZero() {
		if existingID, ok := s.repoBindingsByPath[repoBindingPathKey(sliceID, rootPath)]; ok {
			if existing := s.repoBindings[existingID]; existing != nil {
				stored.CreatedAt = existing.CreatedAt
			}
		}
		if stored.CreatedAt.IsZero() {
			stored.CreatedAt = now
		}
	}
	stored.UpdatedAt = now

	pathKey := repoBindingPathKey(sliceID, rootPath)
	if existingID, ok := s.repoBindingsByPath[pathKey]; ok && existingID != stored.BindingID {
		if existing := s.repoBindings[existingID]; existing != nil {
			if ownerBindings := s.repoBindingsByOwner[existing.OwnerUsername]; ownerBindings != nil {
				delete(ownerBindings, existingID)
			}
		}
		delete(s.repoBindings, existingID)
	}

	if previous := s.repoBindings[stored.BindingID]; previous != nil && previous.OwnerUsername != stored.OwnerUsername {
		if ownerBindings := s.repoBindingsByOwner[previous.OwnerUsername]; ownerBindings != nil {
			delete(ownerBindings, stored.BindingID)
		}
	}

	s.repoBindings[stored.BindingID] = stored
	s.repoBindingsByPath[pathKey] = stored.BindingID
	if s.repoBindingsByOwner[owner] == nil {
		s.repoBindingsByOwner[owner] = make(map[string]bool)
	}
	s.repoBindingsByOwner[owner][stored.BindingID] = true
	return nil
}

func (s *InMemoryStorage) GetRepoBinding(ctx context.Context, sliceID, rootPath string) (*models.RepoBinding, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	bindingID, ok := s.repoBindingsByPath[repoBindingPathKey(sliceID, rootPath)]
	if !ok {
		return nil, ErrRepoBindingNotFound
	}
	binding := s.repoBindings[bindingID]
	if binding == nil {
		return nil, ErrRepoBindingNotFound
	}
	return copyRepoBinding(binding), nil
}

func (s *InMemoryStorage) ListRepoBindingsByOwner(ctx context.Context, username string) ([]*models.RepoBinding, error) {
	_ = ctx
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	ownerBindings := s.repoBindingsByOwner[username]
	if len(ownerBindings) == 0 {
		return []*models.RepoBinding{}, nil
	}

	bindings := make([]*models.RepoBinding, 0, len(ownerBindings))
	for bindingID := range ownerBindings {
		binding := s.repoBindings[bindingID]
		if binding == nil {
			continue
		}
		bindings = append(bindings, copyRepoBinding(binding))
	}
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].UpdatedAt.Equal(bindings[j].UpdatedAt) {
			return bindings[i].RootPath < bindings[j].RootPath
		}
		return bindings[i].UpdatedAt.After(bindings[j].UpdatedAt)
	})
	return bindings, nil
}

func (s *InMemoryStorage) DeleteRepoBinding(ctx context.Context, sliceID, rootPath string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	pathKey := repoBindingPathKey(sliceID, rootPath)
	bindingID, ok := s.repoBindingsByPath[pathKey]
	if !ok {
		return ErrRepoBindingNotFound
	}
	binding := s.repoBindings[bindingID]
	delete(s.repoBindingsByPath, pathKey)
	delete(s.repoBindings, bindingID)
	if binding != nil {
		if ownerBindings := s.repoBindingsByOwner[binding.OwnerUsername]; ownerBindings != nil {
			delete(ownerBindings, bindingID)
		}
	}
	return nil
}

func (s *PostgresNativeStorage) PutRepoBinding(ctx context.Context, binding *models.RepoBinding) error {
	ctx = ensureCtx(ctx)
	if binding == nil {
		return ErrInvalidInput
	}

	sliceID := strings.TrimSpace(binding.SliceID)
	owner := strings.TrimSpace(binding.OwnerUsername)
	rootPath := cleanRelativePath(binding.RootPath)
	if sliceID == "" || owner == "" || rootPath == "" {
		return ErrInvalidInput
	}

	now := time.Now()
	bindingID := strings.TrimSpace(binding.BindingID)
	if bindingID == "" {
		bindingID = repoBindingPathKey(sliceID, rootPath)
	}
	provider := strings.TrimSpace(binding.Provider)
	if provider == "" {
		provider = "github"
	}

	createdAt := binding.CreatedAt
	if createdAt.IsZero() {
		if existing, err := s.GetRepoBinding(ctx, sliceID, rootPath); err == nil && existing != nil {
			createdAt = existing.CreatedAt
		} else {
			createdAt = now
		}
	}

	tag, err := s.pool.Exec(ctx, `
		INSERT INTO repo_bindings (
			binding_id, owner_username, slice_id, root_path, provider, repo_url, branch, push_enabled,
			last_imported_commit, last_pushed_commit, last_seen_remote_commit, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (slice_id, root_path) DO UPDATE SET
			binding_id = EXCLUDED.binding_id,
			owner_username = EXCLUDED.owner_username,
			provider = EXCLUDED.provider,
			repo_url = EXCLUDED.repo_url,
			branch = EXCLUDED.branch,
			push_enabled = EXCLUDED.push_enabled,
			last_imported_commit = EXCLUDED.last_imported_commit,
			last_pushed_commit = EXCLUDED.last_pushed_commit,
			last_seen_remote_commit = EXCLUDED.last_seen_remote_commit,
			updated_at = EXCLUDED.updated_at
	`, bindingID, owner, sliceID, rootPath, provider, strings.TrimSpace(binding.RepoURL), strings.TrimSpace(binding.Branch), binding.PushEnabled, strings.TrimSpace(binding.LastImportedCommit), strings.TrimSpace(binding.LastPushedCommit), strings.TrimSpace(binding.LastSeenRemoteCommit), createdAt.UTC(), now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidInput
	}
	return nil
}

func (s *PostgresNativeStorage) GetRepoBinding(ctx context.Context, sliceID, rootPath string) (*models.RepoBinding, error) {
	ctx = ensureCtx(ctx)
	row := s.pool.QueryRow(ctx, `
		SELECT binding_id, owner_username, slice_id, root_path, provider, repo_url, branch, push_enabled,
		       last_imported_commit, last_pushed_commit, last_seen_remote_commit, created_at, updated_at
		FROM repo_bindings
		WHERE slice_id = $1 AND root_path = $2
	`, strings.TrimSpace(sliceID), cleanRelativePath(rootPath))

	var binding models.RepoBinding
	if err := row.Scan(
		&binding.BindingID,
		&binding.OwnerUsername,
		&binding.SliceID,
		&binding.RootPath,
		&binding.Provider,
		&binding.RepoURL,
		&binding.Branch,
		&binding.PushEnabled,
		&binding.LastImportedCommit,
		&binding.LastPushedCommit,
		&binding.LastSeenRemoteCommit,
		&binding.CreatedAt,
		&binding.UpdatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrRepoBindingNotFound
		}
		return nil, err
	}
	return &binding, nil
}

func (s *PostgresNativeStorage) ListRepoBindingsByOwner(ctx context.Context, username string) ([]*models.RepoBinding, error) {
	ctx = ensureCtx(ctx)
	rows, err := s.pool.Query(ctx, `
		SELECT binding_id, owner_username, slice_id, root_path, provider, repo_url, branch, push_enabled,
		       last_imported_commit, last_pushed_commit, last_seen_remote_commit, created_at, updated_at
		FROM repo_bindings
		WHERE owner_username = $1
		ORDER BY updated_at DESC, root_path ASC
	`, strings.TrimSpace(username))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bindings := make([]*models.RepoBinding, 0)
	for rows.Next() {
		binding := &models.RepoBinding{}
		if err := rows.Scan(
			&binding.BindingID,
			&binding.OwnerUsername,
			&binding.SliceID,
			&binding.RootPath,
			&binding.Provider,
			&binding.RepoURL,
			&binding.Branch,
			&binding.PushEnabled,
			&binding.LastImportedCommit,
			&binding.LastPushedCommit,
			&binding.LastSeenRemoteCommit,
			&binding.CreatedAt,
			&binding.UpdatedAt,
		); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bindings, nil
}

func (s *PostgresNativeStorage) DeleteRepoBinding(ctx context.Context, sliceID, rootPath string) error {
	ctx = ensureCtx(ctx)
	tag, err := s.pool.Exec(ctx, `DELETE FROM repo_bindings WHERE slice_id = $1 AND root_path = $2`, strings.TrimSpace(sliceID), cleanRelativePath(rootPath))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRepoBindingNotFound
	}
	return nil
}
