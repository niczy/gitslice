import { useEffect, useMemo, useState } from 'react';
import { getChangesetDiff } from '../utils/api.js';
import { formatChangeType, formatTimestamp } from '../utils/format.js';
import { normalizeChangeType, normalizeChangesetDiffResponse } from '../utils/normalize.js';
import { renderDiffPatch, renderSplitDiffPatch } from '../utils/diff.jsx';

// ---------------------------------------------------------------------------
// Changeset Diff Page Component
// ---------------------------------------------------------------------------

export default function ChangesetDiffPage({ changesetId, onBack }) {
  const [payload, setPayload] = useState(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState('');
  const [viewMode, setViewMode] = useState('unified');

  useEffect(() => {
    if (!changesetId) return;
    let active = true;
    const load = async () => {
      setIsLoading(true);
      setError('');
      try {
        const response = await getChangesetDiff(changesetId);
        if (active) {
          setPayload(normalizeChangesetDiffResponse(response));
        }
      } catch (err) {
        if (active) {
          setError(err?.message || 'Unable to load changeset diff.');
        }
      } finally {
        if (active) {
          setIsLoading(false);
        }
      }
    };
    load();
    return () => { active = false; };
  }, [changesetId]);

  const changeset = payload?.changeset || null;
  const diff = payload?.diff || null;
  const changes = useMemo(() => payload?.changes || [], [payload]);

  return (
    <section className="commit-diff-page" data-testid="changeset-diff-page">
      <div className="diff-top-bar">
        <button type="button" className="ghost diff-back-btn" onClick={onBack} data-testid="changeset-back-btn">
          Back
        </button>
        <div className="diff-top-title">
          <p className="eyebrow">Changeset diff</p>
          <h2 data-testid="changeset-title">
            Changeset <span className="commit-hash">{changesetId ? changesetId.slice(0, 18) : ''}</span>
          </h2>
          {changeset && (
            <p className="changeset-title-meta">
              {changeset.status || 'pending'} on {changeset.slice_id || changeset.sliceId} by {changeset.author || 'unknown'} · {formatTimestamp(changeset.created_at || changeset.createdAt)}
            </p>
          )}
        </div>
        {diff && (
          <div className="diff-summary" data-testid="changeset-summary">
            <span className="diff-stat diff-stat-added">+{diff.files_added || diff.filesAdded || 0} added</span>
            <span className="diff-stat diff-stat-modified">{diff.files_modified || diff.filesModified || 0} modified</span>
            <span className="diff-stat diff-stat-deleted">-{diff.files_deleted || diff.filesDeleted || 0} deleted</span>
          </div>
        )}
        <div className="diff-view-toggle" data-testid="changeset-view-toggle">
          <button
            type="button"
            className={`diff-view-btn ${viewMode === 'unified' ? 'diff-view-btn-active' : ''}`}
            onClick={() => setViewMode('unified')}
          >
            Unified
          </button>
          <button
            type="button"
            className={`diff-view-btn ${viewMode === 'split' ? 'diff-view-btn-active' : ''}`}
            onClick={() => setViewMode('split')}
          >
            Side-by-side
          </button>
        </div>
      </div>

      <div className="diff-content">
        {isLoading && <div className="diff-loading">Loading changeset diff...</div>}
        {!isLoading && error && <div className="panel-error">{error}</div>}

        {!isLoading && !error && changeset?.message && (
          <div className="changeset-message" data-testid="changeset-message">
            {changeset.message}
          </div>
        )}

        {!isLoading && !error && (
          <ul className="diff-file-list" data-testid="changeset-file-list">
            {changes.map((change) => (
              <li key={change.id || `${change.path}-${change.old_path || ''}`} className="diff-file-item" data-testid="changeset-file-item">
                <div className="diff-file-header">
                  <span className={`change-type change-type-${normalizeChangeType(change.change_type || change.changeType)}`}>
                    {formatChangeType(change.change_type || change.changeType)}
                  </span>
                  <span className="diff-file-path">{change.path}</span>
                  {change.old_path && change.old_path !== change.path && (
                    <span className="diff-file-old-path">(was: {change.old_path})</span>
                  )}
                </div>
                <div className="diff-file-stats">
                  {(change.lines_added > 0 || change.lines_deleted > 0) && (
                    <span className="history-lines">
                      <span className="lines-added">+{change.lines_added || 0}</span>
                      <span className="lines-deleted">-{change.lines_deleted || 0}</span>
                    </span>
                  )}
                </div>
                {!change.patch && (
                  <div className="changeset-no-patch">No inline patch is available for this changeset entry.</div>
                )}
                {change.patch && viewMode === 'unified' && (
                  <pre className="diff-patch">{renderDiffPatch(change.patch)}</pre>
                )}
                {change.patch && viewMode === 'split' && (
                  <div className="diff-split-container">{renderSplitDiffPatch(change.patch)}</div>
                )}
              </li>
            ))}
          </ul>
        )}

        {!isLoading && !error && changes.length === 0 && (
          <div className="panel-empty">No entries found for this changeset.</div>
        )}
      </div>
    </section>
  );
}
