import { useEffect, useMemo, useState } from 'react';
import {
  ChevronDown,
  ChevronRight,
  Folder,
  Globe2,
  Lock,
  Plus,
  Search,
  X,
} from 'lucide-react';

import { createSliceFromFolder, fetchSliceEntries } from '../utils/api.js';
import { formatTimestamp } from '../utils/format.js';
import { normalizeEntryType } from '../utils/normalize.js';
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

function isHomeSlice(slice, homeSliceId) {
  const sliceId = String(slice?.slice_id || '').trim().toLowerCase();
  const normalizedHomeSliceId = String(homeSliceId || '').trim().toLowerCase();
  return Boolean(normalizedHomeSliceId && sliceId === normalizedHomeSliceId);
}

function getSliceVisibility(slice) {
  const value = slice?.visibility ?? slice?.Visibility;
  if (value === 2 || value === 'VISIBILITY_PUBLIC' || value === 'PUBLIC' || value === 'public') {
    return 'public';
  }
  if (value === 1 || value === 'VISIBILITY_PRIVATE' || value === 'PRIVATE' || value === 'private') {
    return 'private';
  }
  return slice?.is_root ? 'public' : 'private';
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

function cleanFolderPath(value) {
  const trimmed = String(value || '').trim();
  const withoutRoot = trimmed.replace(/^\/+/, '').replace(/\/+$/, '');
  return withoutRoot.split('/').filter(Boolean).join('/');
}

function validateFolderPath(value) {
  const cleaned = cleanFolderPath(value);
  if (!cleaned) {
    return { path: '', error: 'At least one tracked folder is required.' };
  }
  if (cleaned.includes('\0')) {
    return { path: cleaned, error: 'Folder paths cannot contain null bytes.' };
  }
  const invalidSegment = cleaned.split('/').find((segment) => {
    const normalized = segment.trim();
    return normalized === '' || normalized === '.' || normalized === '..' || normalized === '~';
  });
  if (invalidSegment) {
    return { path: cleaned, error: `Folder path contains an invalid segment: ${invalidSegment}` };
  }
  return { path: cleaned, error: '' };
}

function getEntryName(entry) {
  return entry?.name || String(entry?.path || '').split('/').filter(Boolean).pop() || entry?.path || '';
}

function sortDirectoryEntries(entries) {
  return [...(entries || [])]
    .filter((entry) => normalizeEntryType(entry?.type) === 'directory')
    .sort((left, right) => getEntryName(left).localeCompare(getEntryName(right), undefined, { sensitivity: 'base' }));
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
  const [selectedFolders, setSelectedFolders] = useState([]);
  const [folderInput, setFolderInput] = useState('');
  const [folderSelectionError, setFolderSelectionError] = useState('');
  const [folderBrowserEntries, setFolderBrowserEntries] = useState({});
  const [expandedFolderPaths, setExpandedFolderPaths] = useState(['']);
  const [loadingFolderPaths, setLoadingFolderPaths] = useState({});
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
    setFolderSelectionError('');
    setIsCreateOpen(true);
  };

  const closeCreateDialog = () => {
    setIsCreateOpen(false);
  };

  const loadFolderEntries = async (path = '') => {
    if (Object.prototype.hasOwnProperty.call(folderBrowserEntries, path)) {
      return;
    }
    setLoadingFolderPaths((prev) => ({ ...prev, [path]: true }));
    setFolderSelectionError('');
    try {
      const entries = await fetchSliceEntries('root_slice', path);
      setFolderBrowserEntries((prev) => ({ ...prev, [path]: entries }));
    } catch (error) {
      setFolderSelectionError(error?.message || 'Unable to load folders.');
    } finally {
      setLoadingFolderPaths((prev) => ({ ...prev, [path]: false }));
    }
  };

  useEffect(() => {
    if (isCreateOpen) {
      loadFolderEntries('');
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isCreateOpen]);

  const addFolderSelection = (rawPath) => {
    const { path, error } = validateFolderPath(rawPath);
    if (error) {
      setFolderSelectionError(error);
      return;
    }

    if (selectedFolders.includes(path)) {
      setFolderSelectionError('That folder is already tracked.');
      return;
    }
    if (selectedFolders.some((folder) => path.startsWith(`${folder}/`))) {
      setFolderSelectionError('A parent folder is already tracked.');
      return;
    }

    setSelectedFolders((prev) => [
      ...prev.filter((folder) => !folder.startsWith(`${path}/`)),
      path,
    ]);
    setFolderInput('');
    setFolderSelectionError('');
    setCreateError('');
  };

  const removeFolderSelection = (folderPath) => {
    setSelectedFolders((prev) => prev.filter((folder) => folder !== folderPath));
  };

  const toggleFolderExpansion = async (folderPath) => {
    const isExpanded = expandedFolderPaths.includes(folderPath);
    if (isExpanded) {
      setExpandedFolderPaths((prev) => prev.filter((path) => path !== folderPath));
      return;
    }
    await loadFolderEntries(folderPath);
    setExpandedFolderPaths((prev) => [...prev, folderPath]);
  };

  const handleFolderInputSubmit = () => {
    addFolderSelection(folderInput);
  };

  const handleCreateSubmit = async (event) => {
    event.preventDefault();
    const name = sliceName.trim();
    if (!name) {
      setCreateError('Slice name is required.');
      return;
    }
    if (selectedFolders.length === 0) {
      setCreateError('At least one tracked folder is required.');
      return;
    }

    setCreateLoading(true);
    setCreateError('');
    try {
      const created = await createSliceFromFolder({
        parentSliceId: 'root_slice',
        folderPaths: selectedFolders,
        name,
        description: sliceDescription.trim(),
      });
      setSliceName('');
      setSliceDescription('');
      setSelectedFolders([]);
      setFolderInput('');
      setIsCreateOpen(false);
      await onRefresh?.();
      onOpenSlice(created.slice_id);
    } catch (error) {
      setCreateError(error?.message || 'Unable to create slice.');
    } finally {
      setCreateLoading(false);
    }
  };

  const renderFolderTree = (path = '', depth = 0) => {
    const entries = sortDirectoryEntries(folderBrowserEntries[path] || []);
    if (!entries.length && loadingFolderPaths[path]) {
      return (
        <div className="slice-create-folder-loading" role="status">
          Loading folders...
        </div>
      );
    }
    if (!entries.length) {
      return null;
    }

    return (
      <ul className="slice-create-folder-tree">
        {entries.map((entry) => {
          const folderPath = cleanFolderPath(entry.path);
          const entryName = getEntryName(entry);
          const isExpanded = expandedFolderPaths.includes(folderPath);
          const isSelected = selectedFolders.includes(folderPath);
          const isCovered = selectedFolders.some((selected) => folderPath.startsWith(`${selected}/`));
          const showFolderPath = folderPath !== entryName;
          return (
            <li key={folderPath}>
              <div
                className={`slice-create-folder-row${isSelected ? ' selected' : ''}${isCovered ? ' covered' : ''}`}
                style={{ '--folder-depth': depth }}
              >
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="slice-create-folder-toggle"
                  onClick={() => toggleFolderExpansion(folderPath)}
                  aria-label={isExpanded ? `Collapse ${folderPath}` : `Expand ${folderPath}`}
                >
                  {isExpanded ? <ChevronDown size={14} aria-hidden="true" /> : <ChevronRight size={14} aria-hidden="true" />}
                </Button>
                <button
                  type="button"
                  className={`slice-create-folder-option${showFolderPath ? '' : ' compact'}`}
                  onClick={() => addFolderSelection(folderPath)}
                  title={folderPath}
                  data-testid="slice-create-folder-option"
                >
                  <Folder size={15} aria-hidden="true" />
                  <span>{entryName}</span>
                  {showFolderPath && <small>{folderPath}</small>}
                </button>
              </div>
              {isExpanded && renderFolderTree(folderPath, depth + 1)}
            </li>
          );
        })}
      </ul>
    );
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
          <Button
            type="button"
            className="slice-create-button"
            onClick={openCreateDialog}
            data-testid="slice-create-open"
          >
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
              const updatedAt = getSliceUpdatedAt(slice);
              const isHome = isHomeSlice(slice, homeSliceId);
              const visibility = getSliceVisibility(slice);
              return (
                <li key={slice.slice_id}>
                  <Button
                    type="button"
                    variant="ghost"
                    className={`slice-home-row${isHome ? ' slice-home-row--home' : ''}`}
                    onClick={() => onOpenSlice(slice.slice_id)}
                    data-testid="slice-home-row"
                  >
                    <span className="slice-home-row-main">
                      <span className="slice-home-row-title">{getSliceName(slice)}</span>
                      <span className="slice-home-row-subtitle">{getSliceMeta(slice)}</span>
                    </span>
                    <span className="slice-home-row-updated">
                      {updatedAt ? formatTimestamp(updatedAt) : 'No updates yet'}
                    </span>
                    <span className={`slice-home-chip slice-home-chip--visibility slice-home-chip--${visibility}`}>
                      {visibility === 'public' ? (
                        <Globe2 size={13} aria-hidden="true" />
                      ) : (
                        <Lock size={13} aria-hidden="true" />
                      )}
                      {visibility === 'public' ? 'Public' : 'Private'}
                    </span>
                  </Button>
                </li>
              );
            })}
          </ul>
        )}
      </div>

      {isCreateOpen && (
        <div className="slice-create-backdrop" role="presentation" onClick={closeCreateDialog}>
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
                <p>Select the root folders this workspace slice should track.</p>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="slice-create-close"
                onClick={closeCreateDialog}
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
            <section className="slice-create-folders" aria-label="Tracked folders">
              <div className="slice-create-section-heading">
                <span>Tracked folders</span>
                <small>{selectedFolders.length} selected</small>
              </div>
              {selectedFolders.length > 0 ? (
                <ul className="slice-create-selected-folders" data-testid="slice-create-selected-folders">
                  {selectedFolders.map((folderPath) => (
                    <li key={folderPath}>
                      <span>{folderPath}</span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        className="slice-create-folder-remove"
                        onClick={() => removeFolderSelection(folderPath)}
                        aria-label={`Remove ${folderPath}`}
                      >
                        <X size={12} aria-hidden="true" />
                      </Button>
                    </li>
                  ))}
                </ul>
              ) : (
                <div className="slice-create-folder-empty">Choose at least one folder from the root slice.</div>
              )}
              <div className="slice-create-folder-input">
                <input
                  type="text"
                  value={folderInput}
                  onChange={(event) => setFolderInput(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') {
                      event.preventDefault();
                      handleFolderInputSubmit();
                    }
                  }}
                  placeholder="apps/web"
                  data-testid="slice-create-folder-input"
                />
                <Button type="button" variant="secondary" onClick={handleFolderInputSubmit}>
                  Add
                </Button>
              </div>
              <div className="slice-create-folder-browser" data-testid="slice-create-folder-browser">
                {renderFolderTree('')}
              </div>
              {folderSelectionError && <div className="panel-error">{folderSelectionError}</div>}
            </section>
            {createError && <div className="panel-error">{createError}</div>}
            <div className="slice-create-actions">
              <Button type="button" variant="ghost" onClick={closeCreateDialog}>
                Cancel
              </Button>
              <Button
                type="submit"
                className="slice-create-button"
                disabled={createLoading}
                data-testid="slice-create-submit"
              >
                {createLoading ? 'Creating...' : 'Create slice'}
              </Button>
            </div>
          </form>
        </div>
      )}
    </section>
  );
}
