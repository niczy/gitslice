import { useEffect, useMemo, useRef, useState } from 'react';
import { ArrowRight, GitCommitHorizontal } from 'lucide-react';

import { listSliceCommits } from '../utils/api.js';
import { formatTimestamp } from '../utils/format.js';
import { getSliceDisplayName } from '../utils/slices.js';
import SliceDetailNav from './SliceDetailNav.jsx';
import { Button } from './ui/button.jsx';

const COMMIT_PAGE_SIZE = 100;

function getCommitTitle(commit) {
  const message = String(commit?.message || '').trim();
  return message || 'No commit message';
}

function shortHash(hash, length = 12) {
  return hash ? hash.slice(0, length) : 'unknown';
}

export default function SliceCommitListPage({
  sliceId,
  slices,
  publicApiBaseUrl = '',
  onOpenCode,
  onOpenChangesets,
  onOpenAgents,
  onOpenCommitDiff,
  initialCommits,
  initialCommitsError = '',
  initialCommitsHasMore = false,
  initialCommitsSliceId = '',
}) {
  const initialMatchesSlice = initialCommitsSliceId === sliceId && Array.isArray(initialCommits);
  const [commits, setCommits] = useState(() => (initialMatchesSlice ? initialCommits : []));
  const [loadedSliceId, setLoadedSliceId] = useState(() => (initialMatchesSlice ? sliceId : ''));
  const [isLoading, setIsLoading] = useState(() => !initialMatchesSlice && !initialCommitsError);
  const [isLoadingMore, setIsLoadingMore] = useState(false);
  const [error, setError] = useState(() => (initialMatchesSlice ? initialCommitsError : ''));
  const [hasMore, setHasMore] = useState(() => (initialMatchesSlice ? initialCommitsHasMore : false));
  const clientRefreshSliceRef = useRef('');

  const currentSlice = useMemo(() => (
    (slices || []).find((slice) => slice.slice_id === sliceId) || null
  ), [sliceId, slices]);
  const sliceLabel = getSliceDisplayName(currentSlice?.name || sliceId || 'Slice');

  useEffect(() => {
    if (initialCommitsSliceId === sliceId && Array.isArray(initialCommits) && loadedSliceId !== sliceId) {
      setCommits(initialCommits);
      setError(initialCommitsError || '');
      setHasMore(Boolean(initialCommitsHasMore));
      setIsLoading(false);
      setLoadedSliceId(sliceId);
      return undefined;
    }
    if (!sliceId) {
      setCommits([]);
      setIsLoading(false);
      setError('Choose a slice to view commits.');
      setHasMore(false);
      setLoadedSliceId('');
      return undefined;
    }
    if (loadedSliceId === sliceId && clientRefreshSliceRef.current === sliceId) {
      return undefined;
    }
    clientRefreshSliceRef.current = sliceId;

    let active = true;
    if (loadedSliceId !== sliceId) {
      setIsLoading(true);
      setError('');
    }
    setHasMore(false);

    listSliceCommits(sliceId, { limit: COMMIT_PAGE_SIZE })
      .then((nextCommits) => {
        if (!active) return;
        setCommits(nextCommits);
        setHasMore(nextCommits.length === COMMIT_PAGE_SIZE);
        setLoadedSliceId(sliceId);
      })
      .catch((err) => {
        if (!active) return;
        setCommits([]);
        setError(err?.message || 'Unable to load commits.');
        setHasMore(false);
        setLoadedSliceId(sliceId);
      })
      .finally(() => {
        if (active) setIsLoading(false);
      });

    return () => {
      active = false;
    };
  }, [
    initialCommits,
    initialCommitsError,
    initialCommitsHasMore,
    initialCommitsSliceId,
    loadedSliceId,
    sliceId,
  ]);

  const loadMore = async () => {
    if (!sliceId || commits.length === 0 || isLoadingMore) {
      return;
    }

    setIsLoadingMore(true);
    setError('');
    try {
      const nextCommits = await listSliceCommits(sliceId, {
        limit: COMMIT_PAGE_SIZE,
        fromCommitHash: commits[commits.length - 1].commit_hash,
      });
      setCommits((previous) => [...previous, ...nextCommits]);
      setHasMore(nextCommits.length === COMMIT_PAGE_SIZE);
    } catch (err) {
      setError(err?.message || 'Unable to load more commits.');
    } finally {
      setIsLoadingMore(false);
    }
  };

  return (
    <section className="slice-activity-page" data-testid="slice-commits-page">
      <SliceDetailNav
        activeTab="commits"
        sliceId={sliceId}
        sliceLabel={sliceLabel}
        slice={currentSlice}
        publicApiBaseUrl={publicApiBaseUrl}
        onOpenCode={onOpenCode}
        onOpenCommits={() => {}}
        onOpenChangesets={onOpenChangesets}
        onOpenAgents={onOpenAgents}
      />

      <div className="slice-activity-content">
        <div className="slice-activity-header">
          <div>
            <p className="eyebrow">Commit history</p>
            <h1>Commits</h1>
          </div>
          <div className="slice-activity-summary" data-testid="slice-commits-summary">
            {isLoading ? 'Loading commits' : `${commits.length} loaded`}
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

          {!isLoading && !error && commits.length === 0 && (
            <div className="panel-empty" data-testid="slice-commits-empty">
              No commits found for this slice.
            </div>
          )}

          {!isLoading && commits.length > 0 && (
            <ul className="slice-activity-list" data-testid="slice-commits-list">
              {commits.map((commit) => (
                <li key={commit.commit_hash}>
                  <Button
                    type="button"
                    variant="ghost"
                    className="slice-activity-row"
                    onClick={() => onOpenCommitDiff?.(commit.commit_hash)}
                    data-testid="slice-commit-row"
                  >
                    <span className="slice-activity-row-icon" aria-hidden="true">
                      <GitCommitHorizontal size={17} />
                    </span>
                    <span className="slice-activity-row-main">
                      <span className="slice-activity-row-title">{getCommitTitle(commit)}</span>
                      <span className="slice-activity-row-subtitle">
                        <span className="commit-hash">{shortHash(commit.commit_hash)}</span>
                        {commit.parent_hash && (
                          <span className="slice-activity-muted">parent {shortHash(commit.parent_hash, 7)}</span>
                        )}
                      </span>
                    </span>
                    <span className="slice-activity-row-meta">
                      {commit.timestamp ? formatTimestamp(commit.timestamp) : 'Unknown time'}
                    </span>
                    <ArrowRight size={16} aria-hidden="true" className="slice-activity-row-arrow" />
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </div>

        {hasMore && (
          <div className="slice-activity-footer">
            <Button type="button" variant="secondary" onClick={loadMore} disabled={isLoadingMore} data-testid="slice-commits-load-more">
              {isLoadingMore ? 'Loading…' : 'Load more'}
            </Button>
          </div>
        )}
      </div>
    </section>
  );
}
