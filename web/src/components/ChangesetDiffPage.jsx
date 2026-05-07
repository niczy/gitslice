import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { closeChangeset, getChangesetDiff, listChangesetSnapshots, mergeChangeset } from '../utils/api.js';
import { formatChangeType, formatTimestamp } from '../utils/format.js';
import {
  normalizeChangeType,
  normalizeChangesetDiffResponse,
  normalizeChangesetSnapshotListResponse,
} from '../utils/normalize.js';
import { renderDiffPatch, renderSplitDiffPatch } from '../utils/diff.jsx';
import { Button } from './ui/button.jsx';

// ---------------------------------------------------------------------------
// Changeset Diff Page Component
// ---------------------------------------------------------------------------

export default function ChangesetDiffPage({
  changesetId,
  onBack,
  onMerged,
  onClosed,
  initialChangesetId = '',
  initialSnapshots = null,
  initialSnapshotsError = '',
  initialSnapshotVersion = 0,
  initialDiffData = null,
  initialDiffError = '',
}) {
  const hasInitialSnapshots = initialChangesetId === changesetId && Array.isArray(initialSnapshots);
  const hasInitialDiff = initialChangesetId === changesetId && Boolean(initialDiffData);
  const initialSelectedSnapshotVersion = hasInitialSnapshots
    ? initialSnapshotVersion || initialSnapshots[0]?.version || 0
    : 0;
  const [payload, setPayload] = useState(() => (hasInitialDiff ? initialDiffData : null));
  const [loadedDiffKey, setLoadedDiffKey] = useState(() => (
    hasInitialDiff ? `${changesetId}:${initialSelectedSnapshotVersion}` : ''
  ));
  const [isLoading, setIsLoading] = useState(() => !hasInitialDiff && !initialDiffError);
  const [error, setError] = useState(() => (initialChangesetId === changesetId ? initialDiffError : ''));
  const [viewMode, setViewMode] = useState('unified');
  const [actionLoading, setActionLoading] = useState('');
  const [actionError, setActionError] = useState('');
  const [snapshots, setSnapshots] = useState(() => (hasInitialSnapshots ? initialSnapshots : []));
  const [snapshotsLoaded, setSnapshotsLoaded] = useState(() => hasInitialSnapshots);
  const [loadedSnapshotsChangesetId, setLoadedSnapshotsChangesetId] = useState(() => (hasInitialSnapshots ? changesetId : ''));
  const [snapshotsError, setSnapshotsError] = useState(() => (hasInitialSnapshots ? initialSnapshotsError : ''));
  const [selectedSnapshotVersion, setSelectedSnapshotVersion] = useState(() => initialSelectedSnapshotVersion);
  const [selectedFileId, setSelectedFileId] = useState(null);
  const clientRefreshSnapshotsRef = useRef('');
  const clientRefreshDiffRef = useRef('');
  const fileRefs = useRef({});
  const panelItemRefs = useRef({});
  const diffContentRef = useRef(null);

  useEffect(() => {
    if (
      initialChangesetId === changesetId
      && Array.isArray(initialSnapshots)
      && loadedSnapshotsChangesetId !== changesetId
    ) {
      const nextVersion = initialSnapshotVersion || initialSnapshots[0]?.version || 0;
      setSnapshots(initialSnapshots);
      setSnapshotsLoaded(true);
      setSnapshotsError(initialSnapshotsError || '');
      setSelectedSnapshotVersion(nextVersion);
      setLoadedSnapshotsChangesetId(changesetId);
      return undefined;
    }
    if (!changesetId) {
      setSnapshots([]);
      setSnapshotsLoaded(false);
      setSnapshotsError('');
      setSelectedSnapshotVersion(0);
      setLoadedSnapshotsChangesetId('');
      return;
    }
    if (loadedSnapshotsChangesetId === changesetId && clientRefreshSnapshotsRef.current === changesetId) {
      return undefined;
    }
    const hasSeededSnapshots = loadedSnapshotsChangesetId === changesetId && snapshotsLoaded;
    clientRefreshSnapshotsRef.current = changesetId;

    let active = true;
    const loadSnapshots = async () => {
      if (!hasSeededSnapshots) {
        setSnapshotsLoaded(false);
        setSnapshotsError('');
      }
      try {
        const response = await listChangesetSnapshots(changesetId);
        if (!active) {
          return;
        }
        const normalized = normalizeChangesetSnapshotListResponse(response);
        setSnapshots(normalized);
        setSelectedSnapshotVersion(normalized[0]?.version || 0);
        setLoadedSnapshotsChangesetId(changesetId);
      } catch (err) {
        if (!active) {
          return;
        }
        setSnapshots([]);
        setSelectedSnapshotVersion(0);
        setSnapshotsError(err?.message || 'Unable to load snapshot versions.');
        setLoadedSnapshotsChangesetId(changesetId);
      } finally {
        if (active) {
          setSnapshotsLoaded(true);
        }
      }
    };
    loadSnapshots();
    return () => { active = false; };
  }, [
    changesetId,
    initialChangesetId,
    initialSnapshotVersion,
    initialSnapshots,
    initialSnapshotsError,
    loadedSnapshotsChangesetId,
  ]);

  useEffect(() => {
    const nextDiffKey = `${changesetId || ''}:${selectedSnapshotVersion || 0}`;
    if (
      initialChangesetId === changesetId
      && initialDiffData
      && nextDiffKey === `${changesetId || ''}:${initialSnapshotVersion || 0}`
      && loadedDiffKey !== nextDiffKey
    ) {
      setPayload(initialDiffData);
      setError('');
      setIsLoading(false);
      setLoadedDiffKey(nextDiffKey);
      return undefined;
    }
    if (!changesetId || !snapshotsLoaded) return;
    if (loadedDiffKey === nextDiffKey && clientRefreshDiffRef.current === nextDiffKey) {
      return undefined;
    }
    const hasSeededDiff = loadedDiffKey === nextDiffKey && Boolean(payload);
    clientRefreshDiffRef.current = nextDiffKey;
    let active = true;
    const load = async () => {
      if (!hasSeededDiff) {
        setIsLoading(true);
        setError('');
      }
      try {
        const response = await getChangesetDiff(changesetId, selectedSnapshotVersion || undefined);
        if (active) {
          setPayload(normalizeChangesetDiffResponse(response));
          setLoadedDiffKey(nextDiffKey);
        }
      } catch (err) {
        if (active) {
          setError(err?.message || 'Unable to load changeset diff.');
          setLoadedDiffKey(nextDiffKey);
        }
      } finally {
        if (active) {
          setIsLoading(false);
        }
      }
    };
    load();
    return () => { active = false; };
  }, [
    changesetId,
    initialChangesetId,
    initialDiffData,
    initialSnapshotVersion,
    loadedDiffKey,
    selectedSnapshotVersion,
    snapshotsLoaded,
  ]);

  const changeset = payload?.changeset || null;
  const selectedSnapshot = payload?.snapshot || null;
  const diff = payload?.diff || null;
  const activeMessage = selectedSnapshot?.message || changeset?.message || '';
  const changes = useMemo(() => payload?.changes || [], [payload]);

  const handleFileSelect = useCallback((fileKey) => {
    setSelectedFileId(fileKey);

    const panelItemEl = panelItemRefs.current[fileKey];
    if (panelItemEl) {
      const listEl = panelItemEl.closest('.diff-file-panel-list');
      if (listEl) {
        const itemTop = panelItemEl.offsetTop;
        const itemBottom = itemTop + panelItemEl.offsetHeight;
        const visibleTop = listEl.scrollTop;
        const visibleBottom = visibleTop + listEl.clientHeight;

        if (itemTop < visibleTop) {
          listEl.scrollTo({ top: itemTop, behavior: 'smooth' });
        } else if (itemBottom > visibleBottom) {
          listEl.scrollTo({ top: itemBottom - listEl.clientHeight, behavior: 'smooth' });
        }
      }
    }

    const fileEl = fileRefs.current[fileKey];
    const diffContentEl = diffContentRef.current;
    if (fileEl && diffContentEl) {
      const fileRect = fileEl.getBoundingClientRect();
      const containerRect = diffContentEl.getBoundingClientRect();
      const targetTop = diffContentEl.scrollTop + (fileRect.top - containerRect.top) - 16;
      diffContentEl.scrollTo({
        top: Math.max(targetTop, 0),
        behavior: 'smooth',
      });
    }
  }, []);

  const handleMerge = async () => {
    if (!changesetId || actionLoading) return;
    setActionError('');
    setActionLoading('merge');
    try {
      const result = await mergeChangeset(changesetId);
      onMerged?.(result);
    } catch (err) {
      setActionError(err?.message || 'Unable to merge changeset.');
    } finally {
      setActionLoading('');
    }
  };

  const handleClose = async () => {
    if (!changesetId || actionLoading) return;
    setActionError('');
    setActionLoading('close');
    try {
      const result = await closeChangeset(changesetId);
      onClosed?.(result);
    } catch (err) {
      setActionError(err?.message || 'Unable to close changeset.');
    } finally {
      setActionLoading('');
    }
  };

  return (
    <section className="commit-diff-page diff-detail-page changeset-detail-page" data-testid="changeset-diff-page">
      <div className="diff-top-bar">
        <Button type="button" variant="ghost" className="diff-back-btn" onClick={onBack} data-testid="changeset-back-btn">
          Back to changesets
        </Button>
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
          {selectedSnapshot?.version > 0 && (
            <p className="changeset-title-meta" data-testid="changeset-snapshot-meta">
              snapshot v{selectedSnapshot.version} by {selectedSnapshot.author || changeset?.author || 'unknown'} · {formatTimestamp(selectedSnapshot.created_at || selectedSnapshot.createdAt)}
            </p>
          )}
        </div>
        <div className="diff-detail-controls changeset-detail-controls">
          {diff && (
            <div className="diff-summary" data-testid="changeset-summary">
              <span className="diff-stat diff-stat-added">+{diff.files_added || diff.filesAdded || 0} added</span>
              <span className="diff-stat diff-stat-modified">{diff.files_modified || diff.filesModified || 0} modified</span>
              <span className="diff-stat diff-stat-deleted">-{diff.files_deleted || diff.filesDeleted || 0} deleted</span>
            </div>
          )}
          {snapshots.length > 0 && (
            <div className="changeset-snapshot-picker" data-testid="changeset-snapshot-picker">
              <label htmlFor="changeset-snapshot-select">Snapshot</label>
              <select
                id="changeset-snapshot-select"
                data-testid="changeset-snapshot-select"
                value={selectedSnapshotVersion || snapshots[0].version}
                onChange={(event) => setSelectedSnapshotVersion(Number(event.target.value) || 0)}
                disabled={isLoading || actionLoading !== ''}
              >
                {snapshots.map((snapshot) => (
                  <option key={snapshot.snapshot_id || `${snapshot.changeset_id}-${snapshot.version}`} value={snapshot.version}>
                    v{snapshot.version} - {formatTimestamp(snapshot.created_at || snapshot.createdAt)}
                  </option>
                ))}
              </select>
            </div>
          )}
          <div className="diff-view-toggle" data-testid="changeset-view-toggle">
            <Button
              type="button"
              variant="ghost"
              className={`diff-view-btn ${viewMode === 'unified' ? 'diff-view-btn-active' : ''}`}
              onClick={() => setViewMode('unified')}
            >
              Unified
            </Button>
            <Button
              type="button"
              variant="ghost"
              className={`diff-view-btn ${viewMode === 'split' ? 'diff-view-btn-active' : ''}`}
              onClick={() => setViewMode('split')}
            >
              Side-by-side
            </Button>
          </div>
          <div className="changeset-actions" data-testid="changeset-actions">
            <Button
              type="button"
              className="primary changeset-action-merge"
              onClick={handleMerge}
              disabled={isLoading || actionLoading !== '' || changeset?.status === 'merged'}
              data-testid="changeset-merge-btn"
            >
              {actionLoading === 'merge' ? 'Merging…' : 'Merge'}
            </Button>
            <Button
              type="button"
              variant="ghost"
              className="changeset-action-close"
              onClick={handleClose}
              disabled={isLoading || actionLoading !== '' || changeset?.status === 'merged' || changeset?.status === 'rejected'}
              data-testid="changeset-close-btn"
            >
              {actionLoading === 'close' ? 'Closing…' : 'Close'}
            </Button>
          </div>
        </div>
      </div>

      <div className="diff-layout">
        {!isLoading && !error && changes.length > 0 && (
          <nav className="diff-file-panel" data-testid="changeset-file-panel">
            <div className="diff-file-panel-header">Files ({changes.length})</div>
            <ul className="diff-file-panel-list">
              {changes.map((change) => {
                const fileKey = change.id || `${change.path}-${change.old_path || ''}`;
                const fileName = String(change.path || '').split('/').pop();
                const dirPath = String(change.path || '').split('/').slice(0, -1).join('/');
                const normalizedType = normalizeChangeType(change.change_type || change.changeType);
                return (
                  <li key={fileKey}>
                    <Button
                      ref={(el) => { panelItemRefs.current[fileKey] = el; }}
                      type="button"
                      variant="ghost"
                      className={`diff-file-panel-item w-full justify-start ${selectedFileId === fileKey ? 'diff-file-panel-item-active' : ''}`}
                      onClick={() => handleFileSelect(fileKey)}
                      title={change.path}
                      data-testid="changeset-file-panel-item"
                    >
                      <span className={`diff-file-panel-badge change-type-${normalizedType}`}>
                        {normalizedType.charAt(0).toUpperCase()}
                      </span>
                      <span className="diff-file-panel-name">
                        {dirPath && <span className="diff-file-panel-dir">{dirPath}/</span>}
                        {fileName}
                      </span>
                      {(change.lines_added > 0 || change.lines_deleted > 0) && (
                        <span className="diff-file-panel-stats">
                          {change.lines_added > 0 && <span className="lines-added">+{change.lines_added}</span>}
                          {change.lines_deleted > 0 && <span className="lines-deleted">-{change.lines_deleted}</span>}
                        </span>
                      )}
                    </Button>
                  </li>
                );
              })}
            </ul>
          </nav>
        )}

        <div className="diff-content" ref={diffContentRef}>
          {actionError && <div className="panel-error diff-action-error">{actionError}</div>}
          {snapshotsError && <div className="panel-error diff-action-error">{snapshotsError}</div>}
          {isLoading && <div className="diff-loading">Loading changeset diff...</div>}
          {!isLoading && error && <div className="panel-error">{error}</div>}

          {!isLoading && !error && activeMessage && (
            <div className="changeset-message" data-testid="changeset-message">
              {activeMessage}
            </div>
          )}

          {!isLoading && !error && (
            <ul className="diff-file-list" data-testid="changeset-file-list">
              {changes.map((change) => {
                const fileKey = change.id || `${change.path}-${change.old_path || ''}`;
                const normalizedType = normalizeChangeType(change.change_type || change.changeType);
                return (
                  <li
                    key={fileKey}
                    ref={(el) => { fileRefs.current[fileKey] = el; }}
                    className={`diff-file-item ${selectedFileId === fileKey ? 'diff-file-item-selected' : ''}`}
                    data-testid="changeset-file-item"
                  >
                    <div className="diff-file-header">
                      <div className="diff-file-header-main">
                        <span className={`change-type change-type-${normalizedType}`}>
                          {formatChangeType(change.change_type || change.changeType)}
                        </span>
                        <span className="diff-file-path">{change.path}</span>
                        {change.old_path && change.old_path !== change.path && (
                          <span className="diff-file-old-path">(was: {change.old_path})</span>
                        )}
                      </div>
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
                );
              })}
            </ul>
          )}

          {!isLoading && !error && changes.length === 0 && (
            <div className="panel-empty">No entries found for this changeset.</div>
          )}
        </div>
      </div>
    </section>
  );
}
