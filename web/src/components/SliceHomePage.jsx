import { useMemo, useState } from 'react';
import { ArrowRight, FolderTree, GitBranch, Globe2, Home, Lock, Plus, RefreshCcw, Search, X } from 'lucide-react';

import { createSliceFromFolder } from '../utils/api.js';
import { formatTimestamp } from '../utils/format.js';
import { getSliceDisplayName } from '../utils/slices.js';
import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';

function getSliceName(slice) {
  return getSliceDisplayName(slice?.name || slice?.slice_id || 'Untitled slice');
}

function getSliceMeta(slice) {
  return slice?.slug || slice?.slice_id || '';
}

function getSliceUpdatedAt(slice) {
  return slice?.updated_at || slice?.updatedAt || slice?.created_at || slice?.createdAt || 0;
}

function getSliceFileCount(slice) {
  const value = slice?.file_count ?? slice?.fileCount;
  return Number.isFinite(Number(value)) ? Number(value) : 0;
}

function isHomeSlice(slice, homeSliceId) {
  const sliceId = String(slice?.slice_id || '').trim().toLowerCase();
  const normalizedHomeSliceId = String(homeSliceId || '').trim().toLowerCase();
  return Boolean(normalizedHomeSliceId && sliceId === normalizedHomeSliceId);
}

function sortSlices(slices, homeSliceId) {
  return [...slices].sort((left, right) => {
    const leftIsHome = isHomeSlice(left, homeSliceId);
    const rightIsHome = isHomeSlice(right, homeSliceId);
    if (leftIsHome !== rightIsHome) {
      return leftIsHome ? -1 : 1;
    }
    if (left.is_root !== right.is_root) {
      return left.is_root ? -1 : 1;
    }
    return getSliceName(left).localeCompare(getSliceName(right), undefined, { sensitivity: 'base' });
  });
}

export default function SliceHomePage({
  slices,
  slicesLoading,
  slicesError,
  isAuthenticated,
  homeSliceId,
  onOpenSlice,
  onRefresh,
  onRequireLogin,
}) {
  const [query, setQuery] = useState('');
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [sliceName, setSliceName] = useState('');
  const [sliceDescription, setSliceDescription] = useState('');
  const [createError, setCreateError] = useState('');
  const [createLoading, setCreateLoading] = useState(false);

  const filteredSlices = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase();
    const sorted = sortSlices(slices || [], homeSliceId);
    if (!normalizedQuery) {
      return sorted;
    }
    return sorted.filter((slice) => {
      const fields = [
        slice?.slice_id,
        slice?.slug,
        slice?.name,
        slice?.description,
      ].map((value) => String(value || '').toLowerCase());
      return fields.some((value) => value.includes(normalizedQuery));
    });
  }, [homeSliceId, query, slices]);

  const openCreateDialog = () => {
    if (!isAuthenticated) {
      onRequireLogin?.();
      return;
    }
    setCreateError('');
    setIsCreateOpen(true);
  };

  const handleCreateSubmit = async (event) => {
    event.preventDefault();
    const name = sliceName.trim();
    if (!name) {
      setCreateError('Slice name is required.');
      return;
    }

    setCreateLoading(true);
    setCreateError('');
    try {
      const created = await createSliceFromFolder({
        parentSliceId: 'root_slice',
        name,
        description: sliceDescription.trim(),
      });
      setSliceName('');
      setSliceDescription('');
      setIsCreateOpen(false);
      await onRefresh?.();
      onOpenSlice(created.slice_id);
    } catch (error) {
      setCreateError(error?.message || 'Unable to create slice.');
    } finally {
      setCreateLoading(false);
    }
  };

  return (
    <section className="slice-home" data-testid="slice-home-page">
      <div className="slice-home-header">
        <div>
          <Badge variant="secondary" className="eyebrow">Slices</Badge>
          <h1>Browse slices</h1>
          <p>Choose a root or workspace slice, then open its files in a focused code browser.</p>
        </div>
        <div className="slice-home-actions">
          <Button type="button" variant="secondary" onClick={onRefresh} disabled={slicesLoading}>
            <RefreshCcw size={15} aria-hidden="true" />
            Refresh
          </Button>
          <Button type="button" onClick={openCreateDialog} data-testid="slice-create-open">
            <Plus size={16} aria-hidden="true" />
            Create slice
          </Button>
        </div>
      </div>

      <div className="slice-home-toolbar">
        <label className="slice-home-search">
          <Search size={16} aria-hidden="true" />
          <input
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search slices"
            data-testid="slice-home-search"
          />
        </label>
      </div>

      <div className="slice-home-panel">
        {slicesLoading && (
          <div className="slice-home-list" data-testid="slice-home-loading">
            {[0, 1, 2].map((item) => (
              <div className="slice-home-skeleton" key={item} />
            ))}
          </div>
        )}
        {!slicesLoading && slicesError && <div className="panel-error">{slicesError}</div>}
        {!slicesLoading && !slicesError && filteredSlices.length === 0 && (
          <div className="panel-empty" data-testid="slice-home-empty">
            No slices match this search.
          </div>
        )}
        {!slicesLoading && !slicesError && filteredSlices.length > 0 && (
          <ul className="slice-home-list" data-testid="slice-home-list">
            {filteredSlices.map((slice) => {
              const fileCount = getSliceFileCount(slice);
              const updatedAt = getSliceUpdatedAt(slice);
              const isHome = isHomeSlice(slice, homeSliceId);
              return (
                <li key={slice.slice_id}>
                  <Button
                    type="button"
                    variant="ghost"
                    className={`slice-home-row${isHome ? ' slice-home-row--home' : ''}`}
                    onClick={() => onOpenSlice(slice.slice_id)}
                    data-testid="slice-home-row"
                  >
                    <span className="slice-home-row-icon" aria-hidden="true">
                      {isHome ? <Home size={17} /> : <FolderTree size={17} />}
                    </span>
                    <span className="slice-home-row-main">
                      <span className="slice-home-row-title">{getSliceName(slice)}</span>
                      <span className="slice-home-row-subtitle">{getSliceMeta(slice)}</span>
                    </span>
                    <span className="slice-home-row-badges">
                      <span className={`slice-home-chip${isHome ? ' slice-home-chip--home' : ''}`}>
                        {isHome ? (
                          <Home size={13} aria-hidden="true" />
                        ) : slice.is_root ? (
                          <Globe2 size={13} aria-hidden="true" />
                        ) : (
                          <Lock size={13} aria-hidden="true" />
                        )}
                        {isHome ? 'Home' : slice.is_root ? 'Root' : 'Workspace'}
                      </span>
                      <span className="slice-home-chip">
                        <GitBranch size={13} aria-hidden="true" />
                        {fileCount} files
                      </span>
                    </span>
                    <span className="slice-home-row-updated">
                      {updatedAt ? formatTimestamp(updatedAt) : 'No updates yet'}
                    </span>
                    <ArrowRight size={16} aria-hidden="true" className="slice-home-row-arrow" />
                  </Button>
                </li>
              );
            })}
          </ul>
        )}
      </div>

      {isCreateOpen && (
        <div className="slice-create-backdrop" role="presentation" onClick={() => setIsCreateOpen(false)}>
          <form
            className="slice-create-dialog"
            role="dialog"
            aria-modal="true"
            aria-label="Create slice"
            onSubmit={handleCreateSubmit}
            onClick={(event) => event.stopPropagation()}
          >
            <div className="slice-create-header">
              <div>
                <h2>Create slice</h2>
                <p>Start an empty workspace slice from the current root.</p>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="slice-create-close"
                onClick={() => setIsCreateOpen(false)}
                aria-label="Close create slice dialog"
              >
                <X size={16} aria-hidden="true" />
              </Button>
            </div>
            <label className="slice-create-field">
              <span>Name</span>
              <input
                type="text"
                value={sliceName}
                onChange={(event) => setSliceName(event.target.value)}
                placeholder="Feature workspace"
                data-testid="slice-create-name"
                autoFocus
              />
            </label>
            <label className="slice-create-field">
              <span>Description</span>
              <textarea
                value={sliceDescription}
                onChange={(event) => setSliceDescription(event.target.value)}
                placeholder="What will this slice track?"
                data-testid="slice-create-description"
              />
            </label>
            {createError && <div className="panel-error">{createError}</div>}
            <div className="slice-create-actions">
              <Button type="button" variant="ghost" onClick={() => setIsCreateOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={createLoading} data-testid="slice-create-submit">
                {createLoading ? 'Creating...' : 'Create slice'}
              </Button>
            </div>
          </form>
        </div>
      )}
    </section>
  );
}
