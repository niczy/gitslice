import { useEffect, useMemo, useState } from 'react';
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
  onOpenCode,
  onOpenCommits,
  onOpenChangesetDiff,
}) {
  const [changesets, setChangesets] = useState([]);
  const [statusFilter, setStatusFilter] = useState('all');
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState('');

  const currentSlice = useMemo(() => (
    (slices || []).find((slice) => slice.slice_id === sliceId) || null
  ), [sliceId, slices]);
  const sliceLabel = getSliceDisplayName(currentSlice?.name || sliceId || 'Slice');

  useEffect(() => {
    if (!sliceId) {
      setChangesets([]);
      setIsLoading(false);
      setError('Choose a slice to view changesets.');
      return undefined;
    }

    let active = true;
    setIsLoading(true);
    setError('');

    listSliceChangesets(sliceId, { limit: CHANGESET_PAGE_SIZE, statusFilter })
      .then((nextChangesets) => {
        if (!active) return;
        setChangesets(nextChangesets);
      })
      .catch((err) => {
        if (!active) return;
        setChangesets([]);
        setError(err?.message || 'Unable to load changesets.');
      })
      .finally(() => {
        if (active) setIsLoading(false);
      });

    return () => {
      active = false;
    };
  }, [sliceId, statusFilter]);

  return (
    <section className="slice-activity-page" data-testid="slice-changesets-page">
      <SliceDetailNav
        activeTab="changesets"
        sliceId={sliceId}
        sliceLabel={sliceLabel}
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
            {isLoading ? 'Loading changesets' : `${changesets.length} shown`}
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

        <div className="slice-activity-panel">
          {isLoading && (
            <div className="slice-activity-list" data-testid="slice-activity-loading">
              {[0, 1, 2, 3].map((item) => (
                <div className="slice-activity-skeleton" key={item} />
              ))}
            </div>
          )}

          {!isLoading && error && <div className="panel-error">{error}</div>}

          {!isLoading && !error && changesets.length === 0 && (
            <div className="panel-empty" data-testid="slice-changesets-empty">
              No changesets match this filter.
            </div>
          )}

          {!isLoading && !error && changesets.length > 0 && (
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
                      <span className={`slice-activity-status slice-activity-status--${status}`}>
                        {status}
                      </span>
                      <span className="slice-activity-row-files">
                        <FileCode2 size={14} aria-hidden="true" />
                        {files.length} {files.length === 1 ? 'file' : 'files'}
                      </span>
                      <span className="slice-activity-row-meta">
                        {changeset.created_at ? formatTimestamp(changeset.created_at) : 'Unknown time'}
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
