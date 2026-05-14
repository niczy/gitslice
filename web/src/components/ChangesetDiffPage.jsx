import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  cancelCIRun,
  closeChangeset,
  getChangesetDiff,
  listChangesetChecks,
  listChangesetSnapshots,
  mergeChangeset,
  rerunCI,
} from '../utils/api.js';
import { formatTimestamp } from '../utils/format.js';
import {
  normalizeChangesetDiffResponse,
  normalizeChangesetSnapshotListResponse,
} from '../utils/normalize.js';
import { renderDiffPatch, renderSplitDiffPatch } from '../utils/diff.jsx';
import {
  DiffFileItemHeader,
  DiffFilePanel,
  DiffSummary,
  DiffTopBar,
  DiffViewToggle,
  getChangesetDiffFileKey,
  scrollDiffFileIntoView,
} from './diff/DiffDetailLayout.jsx';
import { Button } from './ui/button.jsx';

// ---------------------------------------------------------------------------
// Changeset Diff Page Component
// ---------------------------------------------------------------------------

const INITIAL_VISIBLE_CHANGE_COUNT = 120;
const VISIBLE_CHANGE_INCREMENT = 120;

function reviewStatusLabel(reviewStatus) {
  if (reviewStatus === 'needs_sync') return 'sync';
  if (reviewStatus === 'has_conflicts') return 'conflict';
  return 'ready';
}

function changesetStatusText(changeset) {
  const lifecycle = changeset?.status || 'pending';
  if (lifecycle !== 'pending' && lifecycle !== 'approved') {
    return lifecycle;
  }
  return `${lifecycle} · ${reviewStatusLabel(changeset?.review_status)}`;
}

function ciTone(status) {
  if (status === 'success') return 'ready';
  if (status === 'failed' || status === 'error') return 'conflict';
  if (status === 'cancelled' || status === 'superseded') return 'closed';
  return 'needs-sync';
}

function ciSummaryText(ci) {
  if (!ci) return '';
  const status = ci.stale ? 'stale' : ci.status || 'missing';
  const requiredTotal = Number(ci.required_total || 0);
  if (requiredTotal > 0) {
    return `CI ${status} · ${Number(ci.required_passed || 0)}/${requiredTotal} required passed`;
  }
  return `CI ${status}`;
}

function checkField(check, snakeName, camelName, fallback = '') {
  return check?.[snakeName] ?? check?.[camelName] ?? fallback;
}

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
  const [ciChecks, setCIChecks] = useState([]);
  const [ciChecksLoading, setCIChecksLoading] = useState(false);
  const [ciChecksError, setCIChecksError] = useState('');
  const [ciActionLoading, setCIActionLoading] = useState('');
  const [visibleChangeCount, setVisibleChangeCount] = useState(INITIAL_VISIBLE_CHANGE_COUNT);
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
  const changesetCI = changeset?.ci || null;
  const selectedSnapshot = payload?.snapshot || null;
  const changesetCILabel = ciSummaryText(changesetCI);
  const selectedChangesetVersionID = selectedSnapshot?.snapshot_id || changesetCI?.changeset_version_id || '';
  const currentCIVersionID = changesetCI?.changeset_version_id || '';
  const selectedCIIsStale = Boolean(changesetCI?.stale || (selectedChangesetVersionID && currentCIVersionID && selectedChangesetVersionID !== currentCIVersionID));
  const diff = payload?.diff || null;
  const activeMessage = selectedSnapshot?.message || changeset?.message || '';
  const changes = useMemo(() => payload?.changes || [], [payload]);
  const visibleChanges = useMemo(() => changes.slice(0, visibleChangeCount), [changes, visibleChangeCount]);
  const hiddenChangeCount = Math.max(changes.length - visibleChanges.length, 0);

  useEffect(() => {
    setVisibleChangeCount(INITIAL_VISIBLE_CHANGE_COUNT);
    setSelectedFileId(null);
  }, [loadedDiffKey]);

  const refreshCIChecks = useCallback(async () => {
    if (!changesetId || !selectedChangesetVersionID) {
      setCIChecks([]);
      setCIChecksError('');
      return;
    }
    setCIChecksLoading(true);
    setCIChecksError('');
    try {
      const checks = await listChangesetChecks(changesetId, selectedChangesetVersionID);
      setCIChecks(checks);
    } catch (err) {
      setCIChecks([]);
      setCIChecksError(err?.message || 'Unable to load CI checks.');
    } finally {
      setCIChecksLoading(false);
    }
  }, [changesetId, selectedChangesetVersionID]);

  useEffect(() => {
    let active = true;
    const loadChecks = async () => {
      if (!changesetId || !selectedChangesetVersionID) {
        if (active) {
          setCIChecks([]);
          setCIChecksError('');
          setCIChecksLoading(false);
        }
        return;
      }
      setCIChecksLoading(true);
      setCIChecksError('');
      try {
        const checks = await listChangesetChecks(changesetId, selectedChangesetVersionID);
        if (active) {
          setCIChecks(checks);
        }
      } catch (err) {
        if (active) {
          setCIChecks([]);
          setCIChecksError(err?.message || 'Unable to load CI checks.');
        }
      } finally {
        if (active) {
          setCIChecksLoading(false);
        }
      }
    };
    loadChecks();
    return () => { active = false; };
  }, [changesetId, selectedChangesetVersionID]);

  const handleCIAction = async (action) => {
    const runID = changesetCI?.run_id;
    if (!runID || ciActionLoading) return;
    setCIActionLoading(action);
    setCIChecksError('');
    try {
      if (action === 'cancel') {
        await cancelCIRun(runID, 'Cancelled from changeset page');
      } else {
        await rerunCI(runID, { failedOnly: action === 'rerun-failed' });
      }
      await refreshCIChecks();
    } catch (err) {
      setCIChecksError(err?.message || 'Unable to update CI run.');
    } finally {
      setCIActionLoading('');
    }
  };

  const handleFileSelect = useCallback((fileKey) => {
    setSelectedFileId(fileKey);

    scrollDiffFileIntoView(fileKey, panelItemRefs, fileRefs, diffContentRef);
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
      <DiffTopBar
        backLabel="Back to changesets"
        backTestId="changeset-back-btn"
        controls={(
          <div className="diff-detail-controls changeset-detail-controls">
            <DiffSummary data={diff} testId="changeset-summary" />
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
            <DiffViewToggle
              viewMode={viewMode}
              onChange={setViewMode}
              testId="changeset-view-toggle"
            />
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
        )}
        eyebrow="Changeset diff"
        meta={(
          <>
            {changeset && (
              <p className="changeset-title-meta">
                {changesetStatusText(changeset)} on {changeset.slice_id || changeset.sliceId} by {changeset.author || 'unknown'} · {formatTimestamp(changeset.created_at || changeset.createdAt)}
              </p>
            )}
            {selectedSnapshot?.version > 0 && (
              <p className="changeset-title-meta" data-testid="changeset-snapshot-meta">
                snapshot v{selectedSnapshot.version} by {selectedSnapshot.author || changeset?.author || 'unknown'} · {formatTimestamp(selectedSnapshot.created_at || selectedSnapshot.createdAt)}
              </p>
            )}
            {changesetCILabel && (
              <p className="changeset-title-meta changeset-title-ci">
                <span className={`changeset-ci-badge changeset-ci-badge--${ciTone(changesetCI.stale ? 'stale' : changesetCI.status)}`} data-testid="changeset-ci-status">
                  {changesetCILabel}
                </span>
                {changesetCI.run_id && <span className="commit-hash">{changesetCI.run_id.slice(0, 18)}</span>}
              </p>
            )}
          </>
        )}
        onBack={onBack}
        title={(
          <>
            Changeset <span className="commit-hash">{changesetId ? changesetId.slice(0, 18) : ''}</span>
          </>
        )}
        titleTestId="changeset-title"
      />

      <div className="diff-layout">
        {!isLoading && !error && changes.length > 0 && (
          <DiffFilePanel
            changes={visibleChanges}
            getFileKey={getChangesetDiffFileKey}
            headerLabel={`Files (${visibleChanges.length}${hiddenChangeCount > 0 ? `/${changes.length}` : ''})`}
            itemTestId="changeset-file-panel-item"
            onFileSelect={handleFileSelect}
            panelItemRefs={panelItemRefs}
            selectedFileId={selectedFileId}
            testId="changeset-file-panel"
          />
        )}

        <div className="diff-content" ref={diffContentRef}>
          {actionError && <div className="panel-error diff-action-error">{actionError}</div>}
          {snapshotsError && <div className="panel-error diff-action-error">{snapshotsError}</div>}
          {isLoading && <div className="diff-loading">Loading changeset diff...</div>}
          {!isLoading && error && <div className="panel-error">{error}</div>}

          {!isLoading && !error && (changesetCI || ciChecks.length > 0 || ciChecksLoading || ciChecksError) && (
            <section className="rounded-lg border border-border/70 bg-card p-4 shadow-soft" data-testid="changeset-ci-panel">
              <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                <div>
                  <div className="text-sm font-semibold text-foreground">CI checks</div>
                  <div className="mt-1 text-xs text-muted-foreground">
                    Version <code>{selectedChangesetVersionID || 'none'}</code>
                    {changesetCI?.plan_hash && <> · plan <code>{changesetCI.plan_hash}</code></>}
                    {changesetCI?.run_id && <> · run <code>{changesetCI.run_id}</code></>}
                  </div>
                </div>
                {changesetCI?.run_id && (
                  <div className="flex flex-wrap gap-2">
                    <Button type="button" size="sm" variant="outline" disabled={ciActionLoading !== ''} onClick={() => handleCIAction('rerun')}>
                      {ciActionLoading === 'rerun' ? 'Rerunning...' : 'Rerun'}
                    </Button>
                    <Button type="button" size="sm" variant="outline" disabled={ciActionLoading !== ''} onClick={() => handleCIAction('rerun-failed')}>
                      {ciActionLoading === 'rerun-failed' ? 'Rerunning...' : 'Rerun failed'}
                    </Button>
                    <Button type="button" size="sm" variant="ghost" disabled={ciActionLoading !== ''} onClick={() => handleCIAction('cancel')}>
                      {ciActionLoading === 'cancel' ? 'Cancelling...' : 'Cancel'}
                    </Button>
                  </div>
                )}
              </div>
              {selectedCIIsStale && (
                <div className="mt-3 rounded-md border border-amber-300/60 bg-amber-50 px-3 py-2 text-sm text-amber-900">
                  CI is stale for this selected version. Export a new version or rerun checks before merging.
                </div>
              )}
              {ciChecksError && <div className="panel-error mt-3">{ciChecksError}</div>}
              {ciChecksLoading && <div className="panel-empty mt-3">Loading CI checks...</div>}
              {!ciChecksLoading && !ciChecksError && ciChecks.length === 0 && (
                <div className="panel-empty mt-3">No CI checks are recorded for this changeset version.</div>
              )}
              {!ciChecksLoading && ciChecks.length > 0 && (
                <div className="mt-3 overflow-auto">
                  <table className="w-full min-w-[760px] text-left text-sm">
                    <thead className="border-b border-border/70 text-xs uppercase text-muted-foreground">
                      <tr>
                        <th className="py-2 pr-3">Check</th>
                        <th className="py-2 pr-3">Required</th>
                        <th className="py-2 pr-3">Status</th>
                        <th className="py-2 pr-3">Manifest</th>
                        <th className="py-2 pr-3">Plan</th>
                        <th className="py-2 pr-3">Run</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-border/60">
                      {ciChecks.map((check) => {
                        const status = checkField(check, 'status', 'status') || 'missing';
                        const key = `${checkField(check, 'run_id', 'runId')}-${checkField(check, 'manifest_path', 'manifestPath')}-${checkField(check, 'job_key', 'jobKey')}`;
                        return (
                          <tr key={key}>
                            <td className="py-2 pr-3 font-medium">{checkField(check, 'check_name', 'checkName') || checkField(check, 'job_key', 'jobKey')}</td>
                            <td className="py-2 pr-3">{check.required ? 'yes' : 'no'}</td>
                            <td className="py-2 pr-3">
                              <span className={`changeset-ci-badge changeset-ci-badge--${ciTone(status)}`}>{status}</span>
                            </td>
                            <td className="py-2 pr-3 font-mono text-xs">{checkField(check, 'manifest_path', 'manifestPath') || 'unknown'}</td>
                            <td className="py-2 pr-3 font-mono text-xs">{checkField(check, 'plan_hash', 'planHash') || 'none'}</td>
                            <td className="py-2 pr-3 font-mono text-xs">{checkField(check, 'run_id', 'runId') || 'none'}</td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}
            </section>
          )}

          {!isLoading && !error && activeMessage && (
            <div className="changeset-message" data-testid="changeset-message">
              {activeMessage}
            </div>
          )}

          {!isLoading && !error && (
            <ul className="diff-file-list" data-testid="changeset-file-list">
              {visibleChanges.map((change) => {
                const fileKey = getChangesetDiffFileKey(change);
                return (
                  <li
                    key={fileKey}
                    ref={(el) => { fileRefs.current[fileKey] = el; }}
                    className={`diff-file-item ${selectedFileId === fileKey ? 'diff-file-item-selected' : ''}`}
                    data-testid="changeset-file-item"
                  >
                    <DiffFileItemHeader change={change} />
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

          {!isLoading && !error && hiddenChangeCount > 0 && (
            <div className="diff-load-more">
              <Button
                type="button"
                variant="ghost"
                onClick={() => setVisibleChangeCount((count) => count + VISIBLE_CHANGE_INCREMENT)}
                data-testid="changeset-show-more-files"
              >
                Show {Math.min(hiddenChangeCount, VISIBLE_CHANGE_INCREMENT)} more files
              </Button>
            </div>
          )}

          {!isLoading && !error && changes.length === 0 && (
            <div className="panel-empty">No entries found for this changeset.</div>
          )}
        </div>
      </div>
    </section>
  );
}
