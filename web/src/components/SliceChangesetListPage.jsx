import { useEffect, useMemo, useRef, useState } from 'react';
import { ArrowRight, FileCode2, GitPullRequest } from 'lucide-react';

import { listSliceChangesets } from '../utils/api.js';
import { formatTimestamp } from '../utils/format.js';
import { getSliceDisplayName } from '../utils/slices.js';
import SliceDetailNav from './SliceDetailNav.jsx';
import { Button } from './ui/button.jsx';

const CHANGESET_PAGE_SIZE = 100;
const STATUS_FILTERS = [
  { id: 'all', label: 'All' },
  { id: 'pending', label: 'Pending' },
  { id: 'approved', label: 'Approved' },
  { id: 'merged', label: 'Merged' },
  { id: 'rejected', label: 'Closed' },
];

function getChangesetTitle(changeset) {
  const message = String(changeset?.message || '').trim();
  return message || 'No changeset message';
}

function shortId(value, length = 18) {
  return value ? value.slice(0, length) : 'unknown';
}

function statusLabel(status) {
  if (status === 'rejected') return 'closed';
  return status || 'pending';
}

export default function SliceChangesetListPage({
  sliceId,
  slices,
  publicApiBaseUrl = '',
  onOpenCode,
  onOpenCommits,
  onOpenChangesetDiff,
  initialChangesets,
  initialChangesetsError = '',
  initialChangesetsSliceId = '',
  initialStatusFilter = 'all',
}) {
  const initialMatchesSlice = initialChangesetsSliceId === sliceId
    && initialStatusFilter === 'all'
    && Array.isArray(initialChangesets);
  const [changesets, setChangesets] = useState(() => (initialMatchesSlice ? initialChangesets : []));
  const [statusFilter, setStatusFilter] = useState('all');
  const [loadedKey, setLoadedKey] = useState(() => (initialMatchesSlice ? `${sliceId}:all` : ''));
  const [isLoading, setIsLoading] = useState(() => !initialMatchesSlice && !initialChangesetsError);
  const [error, setError] = useState(() => (initialMatchesSlice ? initialChangesetsError : ''));
  const clientRefreshKeyRef = useRef('');

  const currentSlice = useMemo(() => (
    (slices || []).find((slice) => slice.slice_id === sliceId) || null
  ), [sliceId, slices]);
  const sliceLabel = getSliceDisplayName(currentSlice?.name || sliceId || 'Slice');

  useEffect(() => {
    const nextKey = `${sliceId || ''}:${statusFilter}`;
    if (
      statusFilter === 'all'
      && initialChangesetsSliceId === sliceId
      && initialStatusFilter === 'all'
      && Array.isArray(initialChangesets)
      && loadedKey !== nextKey
    ) {
      setChangesets(initialChangesets);
      setError(initialChangesetsError || '');
      setIsLoading(false);
      setLoadedKey(nextKey);
      return undefined;
    }
    if (!sliceId) {
      setChangesets([]);
      setIsLoading(false);
      setError('Choose a slice to view changesets.');
      setLoadedKey('');
      return undefined;
    }
    if (loadedKey === nextKey && clientRefreshKeyRef.current === nextKey) {
      return undefined;
    }
    clientRefreshKeyRef.current = nextKey;

    let active = true;
    if (loadedKey !== nextKey) {
      setIsLoading(true);
      setError('');
    }

    listSliceChangesets(sliceId, { limit: CHANGESET_PAGE_SIZE, statusFilter })
      .then((nextChangesets) => {
        if (!active) return;
        setChangesets(nextChangesets);
        setLoadedKey(nextKey);
      })
      .catch((err) => {
        if (!active) return;
        setChangesets([]);
        setError(err?.message || 'Unable to load changesets.');
        setLoadedKey(nextKey);
      })
      .finally(() => {
        if (active) setIsLoading(false);
      });

    return () => {
      active = false;
    };
  }, [
    initialChangesets,
    initialChangesetsError,
    initialChangesetsSliceId,
    initialStatusFilter,
    loadedKey,
    sliceId,
    statusFilter,
  ]);

  const showInitialLoading = isLoading && !loadedKey && changesets.length === 0 && !error;
  const showChangesets = !error && changesets.length > 0;
  const showEmpty = !isLoading && !error && changesets.length === 0;

  return (
    <section className="slice-activity-page" data-testid="slice-changesets-page">
      <SliceDetailNav
        activeTab="changesets"
        sliceId={sliceId}
        sliceLabel={sliceLabel}
        slice={currentSlice}
        publicApiBaseUrl={publicApiBaseUrl}
        onOpenCode={onOpenCode}
        onOpenCommits={onOpenCommits}
        onOpenChangesets={() => {}}
      />

      <div className="slice-activity-content">
        <div className="slice-activity-header">
          <div>
            <p className="eyebrow">Review queue</p>
            <h1>Changesets</h1>
          </div>
          <div className="slice-activity-summary" data-testid="slice-changesets-summary">
            {`${changesets.length} shown`}
          </div>
        </div>

        <div className="slice-activity-toolbar">
          <div className="slice-activity-filter" role="tablist" aria-label="Filter changesets">
            {STATUS_FILTERS.map((filter) => (
              <Button
                key={filter.id}
                type="button"
                variant="ghost"
                className={`slice-activity-filter-btn${statusFilter === filter.id ? ' active' : ''}`}
                onClick={() => setStatusFilter(filter.id)}
                role="tab"
                aria-selected={statusFilter === filter.id}
                data-testid={`changeset-filter-${filter.id}`}
              >
                {filter.label}
              </Button>
            ))}
          </div>
        </div>

        <div className="slice-activity-panel" aria-busy={isLoading ? 'true' : 'false'}>
          {showInitialLoading && (
            <div className="slice-activity-list" data-testid="slice-activity-loading">
              {[0, 1, 2, 3].map((item) => (
                <div className="slice-activity-skeleton" key={item} />
              ))}
            </div>
          )}

          {!isLoading && error && <div className="panel-error">{error}</div>}

          {showEmpty && (
            <div className="panel-empty" data-testid="slice-changesets-empty">
              No changesets match this filter.
            </div>
          )}

          {showChangesets && (
            <ul className="slice-activity-list" data-testid="slice-changesets-list">
              {changesets.map((changeset) => {
                const files = changeset.modified_files || [];
                const status = statusLabel(changeset.status);
                return (
                  <li key={changeset.changeset_id}>
                    <Button
                      type="button"
                      variant="ghost"
                      className="slice-activity-row slice-activity-row--changeset"
                      onClick={() => onOpenChangesetDiff?.(changeset.changeset_id)}
                      data-testid="slice-changeset-row"
                    >
                      <span className="slice-activity-row-icon" aria-hidden="true">
                        <GitPullRequest size={17} />
                      </span>
                      <span className="slice-activity-row-main">
                        <span className="slice-activity-row-title">{getChangesetTitle(changeset)}</span>
                        <span className="slice-activity-row-subtitle">
                          <span className="commit-hash">{shortId(changeset.changeset_id)}</span>
                          {changeset.base_commit_hash && (
                            <span className="slice-activity-muted">base {shortId(changeset.base_commit_hash, 7)}</span>
                          )}
                        </span>
                      </span>
                      <span className="slice-activity-row-files">
                        <FileCode2 size={14} aria-hidden="true" />
                        {files.length} {files.length === 1 ? 'file' : 'files'}
                      </span>
                      <span className="slice-activity-row-meta">
                        {changeset.created_at ? formatTimestamp(changeset.created_at) : 'Unknown time'}
                      </span>
                      <span className={`slice-activity-status slice-activity-status--${status}`}>
                        {status}
                      </span>
                      <ArrowRight size={16} aria-hidden="true" className="slice-activity-row-arrow" />
                    </Button>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </div>
    </section>
  );
}
