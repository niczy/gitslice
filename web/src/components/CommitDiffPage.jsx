import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { apiBaseUrl, createRevertChangeset, fetchWithAuth } from '../utils/api.js';
import { formatChangeType } from '../utils/format.js';
import { normalizeChangeType, normalizeDiffResponse } from '../utils/normalize.js';
import { renderDiffPatch, renderSplitDiffPatch } from '../utils/diff.jsx';
import { decodeBase64 } from '../utils/highlight.js';

function isBinaryPatchText(patch = '') {
  return /GIT binary patch|Binary files .* differ/i.test(patch);
}

function mimeTypeFromPath(path = '') {
  const extension = path.split('.').pop()?.toLowerCase();
  if (!extension || extension === path.toLowerCase()) {
    return 'application/octet-stream';
  }
  const map = {
    png: 'image/png',
    jpg: 'image/jpeg',
    jpeg: 'image/jpeg',
    gif: 'image/gif',
    webp: 'image/webp',
    svg: 'image/svg+xml',
    pdf: 'application/pdf',
  };
  return map[extension] || 'application/octet-stream';
}

function detectBinaryFromBase64(encoded = '') {
  if (!encoded) {
    return false;
  }
  try {
    const raw = window.atob(encoded);
    const sampleSize = Math.min(raw.length, 2048);
    let controlChars = 0;
    for (let index = 0; index < sampleSize; index += 1) {
      const code = raw.charCodeAt(index);
      if (code === 0) {
        return true;
      }
      if ((code < 9) || (code > 13 && code < 32)) {
        controlChars += 1;
      }
    }
    return (controlChars / sampleSize) > 0.12;
  } catch {
    return false;
  }
}


const LAZY_PATCH_FILE_COUNT_THRESHOLD = 80;
const LAZY_PATCH_LINE_THRESHOLD = 1200;
const LAZY_PATCH_SCROLL_TRIGGER = 24;

// ---------------------------------------------------------------------------
// Commit Diff Page Component
// ---------------------------------------------------------------------------

export default function CommitDiffPage({ commitHash, onBack, onOpenChangesetDiff }) {
  const [diffData, setDiffData] = useState(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState('');
  const [selectedFileId, setSelectedFileId] = useState(null);
  const [viewMode, setViewMode] = useState('unified'); // 'unified' | 'split'
  const [fallbackContentByFile, setFallbackContentByFile] = useState({});
  const [binaryVisibleByFile, setBinaryVisibleByFile] = useState({});
  const [patchByFile, setPatchByFile] = useState({});
  const [hasLoadedPatches, setHasLoadedPatches] = useState(false);
  const [isPatchLoading, setIsPatchLoading] = useState(false);
  const [patchLoadError, setPatchLoadError] = useState('');
  const [revertingChangeId, setRevertingChangeId] = useState('');
  const [actionError, setActionError] = useState('');
  const fileRefs = useRef({});
  const panelItemRefs = useRef({});
  const diffContentRef = useRef(null);

  const encodePath = useCallback((value) => value.split('/').map(encodeURIComponent).join('/'), []);

  useEffect(() => {
    if (!commitHash) return;
    let active = true;
    const controller = new AbortController();

    const loadDiff = async () => {
      setIsLoading(true);
      setError('');
      try {
        const response = await fetchWithAuth(`${apiBaseUrl}/v1/commits/${encodeURIComponent(commitHash)}/changes`, {
          signal: controller.signal,
        });
        if (!response.ok) throw new Error(`Request failed (${response.status})`);
        const payload = await response.json();
        if (active) {
          setDiffData(normalizeDiffResponse(payload));
          setFallbackContentByFile({});
          setBinaryVisibleByFile({});
          setPatchByFile({});
          setHasLoadedPatches(false);
          setPatchLoadError('');
        }
      } catch (err) {
        if (active && err?.name !== 'AbortError') {
          setError('Unable to load commit changes.');
        }
      } finally {
        if (active) setIsLoading(false);
      }
    };

    loadDiff();
    return () => { active = false; controller.abort(); };
  }, [commitHash]);

  const loadPatches = useCallback(async () => {
    if (!commitHash || isPatchLoading || hasLoadedPatches) {
      return;
    }
    setIsPatchLoading(true);
    setPatchLoadError('');
    try {
      const response = await fetchWithAuth(`${apiBaseUrl}/v1/commits/${encodeURIComponent(commitHash)}/changes?include_patches=true`);
      if (!response.ok) {
        throw new Error(`Request failed (${response.status})`);
      }
      const payload = normalizeDiffResponse(await response.json());
      const nextPatchByFile = {};
      payload?.changes?.forEach((change) => {
        const fileKey = change.id || change.path;
        nextPatchByFile[fileKey] = change.patch || '';
      });
      setPatchByFile(nextPatchByFile);
      setHasLoadedPatches(true);
    } catch {
      setPatchLoadError('Unable to load patch content.');
    } finally {
      setIsPatchLoading(false);
    }
  }, [commitHash, hasLoadedPatches, isPatchLoading]);

  const changes = useMemo(() => {
    const base = diffData?.changes || [];
    return base.map((change) => {
      const fileKey = change.id || change.path;
      if (!hasLoadedPatches) {
        return { ...change, patch: '' };
      }
      return { ...change, patch: patchByFile[fileKey] || '' };
    });
  }, [diffData, hasLoadedPatches, patchByFile]);


  const shouldLazyLoadPatches = useMemo(() => {
    const baseChanges = diffData?.changes || [];
    if (baseChanges.length === 0) {
      return false;
    }
    if (baseChanges.length > LAZY_PATCH_FILE_COUNT_THRESHOLD) {
      return true;
    }
    return baseChanges.some((change) => ((change.lines_added || 0) + (change.lines_deleted || 0)) > LAZY_PATCH_LINE_THRESHOLD);
  }, [diffData]);


  useEffect(() => {
    if (!diffData || hasLoadedPatches || isPatchLoading) {
      return;
    }

    if (!shouldLazyLoadPatches) {
      loadPatches();
      return;
    }

    const diffContentEl = diffContentRef.current;
    if (!diffContentEl) {
      return;
    }

    const handleDiffScroll = () => {
      if (diffContentEl.scrollTop >= LAZY_PATCH_SCROLL_TRIGGER) {
        loadPatches();
      }
    };

    diffContentEl.addEventListener('scroll', handleDiffScroll, { passive: true });
    return () => {
      diffContentEl.removeEventListener('scroll', handleDiffScroll);
    };
  }, [diffData, hasLoadedPatches, isPatchLoading, loadPatches, shouldLazyLoadPatches]);

  useEffect(() => {
    if (!hasLoadedPatches || changes.length === 0) {
      return;
    }

    const controller = new AbortController();
    let active = true;

    const loadFallbackContent = async () => {
      const nextContentByFile = {};

      const loadQueue = changes
        .filter((change) => (!change.patch || isBinaryPatchText(change.patch)) && change.path && change.slice_id && normalizeChangeType(change.change_type) !== 'delete')
        .slice(0, 30);

      await Promise.all(loadQueue.map(async (change) => {
        const fileKey = change.id || change.path;
        const encodedPath = encodePath(change.path);
        const params = new URLSearchParams();
        params.set('slice_version.slice_hash', commitHash);
        const url = `${apiBaseUrl}/v1/slices/${encodeURIComponent(change.slice_id)}/files/${encodedPath}?${params.toString()}`;

        try {
          const response = await fetchWithAuth(url, { signal: controller.signal });
          if (!response.ok) {
            return;
          }
          const payload = await response.json();
          const encodedContent = payload?.file?.content || '';
          if (detectBinaryFromBase64(encodedContent)) {
            nextContentByFile[fileKey] = {
              kind: 'binary',
              base64: encodedContent,
              mimeType: mimeTypeFromPath(change.path),
            };
            return;
          }
          const decodedContent = decodeBase64(encodedContent);
          if (decodedContent) {
            nextContentByFile[fileKey] = {
              kind: 'text',
              content: decodedContent,
            };
          }
        } catch (err) {
          if (err?.name !== 'AbortError') {
            // no-op: this fallback is best-effort
          }
        }
      }));

      if (active && Object.keys(nextContentByFile).length > 0) {
        setFallbackContentByFile((previous) => ({
          ...previous,
          ...nextContentByFile,
        }));
      }
    };

    loadFallbackContent();

    return () => {
      active = false;
      controller.abort();
    };
  }, [changes, commitHash, encodePath, hasLoadedPatches]);

  const handleFileSelect = useCallback((fileKey) => {
    setSelectedFileId(fileKey);
    loadPatches();

    const panelItemEl = panelItemRefs.current[fileKey];
    if (panelItemEl) {
      panelItemEl.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'nearest' });
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
  }, [loadPatches]);

  const handleRevertDiff = useCallback(async (change) => {
    const changeID = change?.id || '';
    if (!changeID) {
      setActionError('This diff cannot be reverted because it has no change id.');
      return;
    }

    setActionError('');
    setRevertingChangeId(changeID);
    try {
      const response = await createRevertChangeset(commitHash, changeID, change?.slice_id || '');
      const changesetID = response?.changesetId || response?.changeset_id;
      if (!changesetID) {
        throw new Error('missing changeset id');
      }
      onOpenChangesetDiff?.(changesetID);
    } catch (err) {
      setActionError(err?.message || 'Unable to create revert changeset.');
    } finally {
      setRevertingChangeId('');
    }
  }, [commitHash, onOpenChangesetDiff]);

  return (
    <section className="commit-diff-page" data-testid="commit-diff-page">
      <div className="diff-top-bar">
        <button type="button" className="ghost diff-back-btn" onClick={onBack} data-testid="diff-back-btn">
          Back to browser
        </button>
        <div className="diff-top-title">
          <p className="eyebrow">Commit diff</p>
          <h2 data-testid="diff-commit-title">
            Commit <span className="commit-hash">{commitHash ? commitHash.slice(0, 12) : ''}</span>
          </h2>
        </div>
        {diffData && (
          <div className="diff-summary" data-testid="diff-summary">
            <span className="diff-stat diff-stat-added">+{diffData.files_added || 0} added</span>
            <span className="diff-stat diff-stat-modified">{diffData.files_modified || 0} modified</span>
            <span className="diff-stat diff-stat-deleted">-{diffData.files_deleted || 0} deleted</span>
            {(diffData.files_renamed || 0) > 0 && (
              <span className="diff-stat diff-stat-renamed">{diffData.files_renamed} renamed</span>
            )}
          </div>
        )}
        <div className="diff-view-toggle" data-testid="diff-view-toggle">
          <button
            type="button"
            className={`diff-view-btn ${viewMode === 'unified' ? 'diff-view-btn-active' : ''}`}
            onClick={() => setViewMode('unified')}
            data-testid="diff-view-unified-btn"
          >
            Unified
          </button>
          <button
            type="button"
            className={`diff-view-btn ${viewMode === 'split' ? 'diff-view-btn-active' : ''}`}
            onClick={() => setViewMode('split')}
            data-testid="diff-view-split-btn"
          >
            Side-by-side
          </button>
        </div>
      </div>
      {actionError && <div className="panel-error diff-action-error">{actionError}</div>}

      <div className="diff-layout">
        {/* Left file panel */}
        {!isLoading && !error && changes.length > 0 && (
          <nav className="diff-file-panel" data-testid="diff-file-panel">
            <div className="diff-file-panel-header">Files ({changes.length})</div>
            <ul className="diff-file-panel-list">
              {changes.map((change) => {
                const fileKey = change.id || change.path;
                const fileName = change.path.split('/').pop();
                const dirPath = change.path.split('/').slice(0, -1).join('/');
                return (
                  <li key={fileKey}>
                    <button
                      ref={(el) => { panelItemRefs.current[fileKey] = el; }}
                      type="button"
                      className={`diff-file-panel-item ${selectedFileId === fileKey ? 'diff-file-panel-item-active' : ''}`}
                      onClick={() => handleFileSelect(fileKey)}
                      title={change.path}
                      data-testid="diff-file-panel-item"
                    >
                      <span className={`diff-file-panel-badge change-type-${normalizeChangeType(change.change_type)}`}>
                        {normalizeChangeType(change.change_type).charAt(0).toUpperCase()}
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
                    </button>
                  </li>
                );
              })}
            </ul>
          </nav>
        )}

        {/* Main diff content */}
        <div className="diff-content" ref={diffContentRef}>
          {isLoading && <div className="diff-loading">Loading commit changes...</div>}
          {error && <div className="panel-error">{error}</div>}
          {!isLoading && !error && shouldLazyLoadPatches && !hasLoadedPatches && (
            <div className="diff-loading diff-patch-lazy-state" data-testid="diff-patch-lazy-state">
              <p>{isPatchLoading ? 'Loading patch content…' : 'Patch content will load as you scroll.'}</p>
              {patchLoadError && (
                <>
                  <p className="panel-error">{patchLoadError}</p>
                  <button type="button" className="ghost" onClick={loadPatches} data-testid="diff-retry-load-patches-btn" disabled={isPatchLoading}>
                    Retry patch load
                  </button>
                </>
              )}
            </div>
          )}
          {!isLoading && !error && diffData && (
            <ul className="diff-file-list" data-testid="diff-file-list">
              {changes.map((change) => {
                const fileKey = change.id || change.path;
                const fallbackContent = fallbackContentByFile[fileKey];
                const isBinaryPatch = isBinaryPatchText(change.patch || '');
                const hasBinaryFallback = fallbackContent?.kind === 'binary';
                const showBinary = binaryVisibleByFile[fileKey];
                return (
                  <li
                    key={fileKey}
                    ref={(el) => { fileRefs.current[fileKey] = el; }}
                    className={`diff-file-item ${selectedFileId === fileKey ? 'diff-file-item-selected' : ''}`}
                    data-testid="diff-file-item"
                  >
                    <div className="diff-file-header">
                      <div className="diff-file-header-main">
                        <span className={`change-type change-type-${normalizeChangeType(change.change_type)}`}>
                          {formatChangeType(change.change_type)}
                        </span>
                        <span className="diff-file-path" data-testid="diff-file-path">{change.path}</span>
                        {change.old_path && change.old_path !== change.path && (
                          <span className="diff-file-old-path">(was: {change.old_path})</span>
                        )}
                      </div>
                      <details className="diff-file-actions" data-testid="diff-file-actions-menu">
                        <summary className="ghost diff-file-actions-trigger">Actions ▾</summary>
                        <div className="diff-file-actions-menu">
                          <button
                            type="button"
                            className="ghost diff-file-action-btn"
                            onClick={(event) => {
                              event.preventDefault();
                              handleRevertDiff(change);
                            }}
                            disabled={revertingChangeId === fileKey}
                            data-testid="diff-file-revert-btn"
                          >
                            {revertingChangeId === fileKey ? 'Reverting…' : 'Revert in new changeset'}
                          </button>
                        </div>
                      </details>
                    </div>
                    <div className="diff-file-stats">
                      {(change.lines_added > 0 || change.lines_deleted > 0) && (
                        <span className="history-lines">
                          <span className="lines-added">+{change.lines_added || 0}</span>
                          <span className="lines-deleted">-{change.lines_deleted || 0}</span>
                        </span>
                      )}
                    </div>
                    {change.patch && viewMode === 'unified' && (
                      isBinaryPatch && !showBinary ? (
                        <div className="diff-binary-block" data-testid="diff-file-binary-block">
                          <p>This file contains binary content. Click to view.</p>
                          <button
                            type="button"
                            className="ghost"
                            data-testid="diff-file-view-binary-btn"
                            onClick={() => setBinaryVisibleByFile((previous) => ({ ...previous, [fileKey]: true }))}
                          >
                            View binary
                          </button>
                        </div>
                      ) : (
                        <pre className="diff-patch" data-testid="diff-file-patch">
                          {renderDiffPatch(change.patch)}
                        </pre>
                      )
                    )}
                    {change.patch && viewMode === 'split' && (
                      isBinaryPatch && !showBinary ? (
                        <div className="diff-binary-block" data-testid="diff-file-binary-block">
                          <p>This file contains binary content. Click to view.</p>
                          <button
                            type="button"
                            className="ghost"
                            data-testid="diff-file-view-binary-btn"
                            onClick={() => setBinaryVisibleByFile((previous) => ({ ...previous, [fileKey]: true }))}
                          >
                            View binary
                          </button>
                        </div>
                      ) : (
                        <div className="diff-split-container" data-testid="diff-file-patch">
                          {renderSplitDiffPatch(change.patch)}
                        </div>
                      )
                    )}
                    {!change.patch && fallbackContent?.kind === 'text' && viewMode === 'unified' && (
                      <pre className="diff-patch" data-testid="diff-file-fallback-content">
                        {renderDiffPatch(`--- /dev/null\n+++ b/${change.path}\n@@\n${fallbackContent.content.split('\n').map((line) => `+${line}`).join('\n')}`)}
                      </pre>
                    )}
                    {!change.patch && hasBinaryFallback && (
                      <div className="diff-binary-block" data-testid="diff-file-binary-block">
                        {!showBinary && (
                          <>
                            <p>Binary file hidden by default.</p>
                            <button
                              type="button"
                              className="ghost"
                              data-testid="diff-file-view-binary-btn"
                              onClick={() => setBinaryVisibleByFile((previous) => ({ ...previous, [fileKey]: true }))}
                            >
                              View binary
                            </button>
                          </>
                        )}
                        {showBinary && (
                          <div className="diff-binary-preview" data-testid="diff-file-binary-preview">
                            {fallbackContent.mimeType.startsWith('image/') ? (
                              <img
                                src={`data:${fallbackContent.mimeType};base64,${fallbackContent.base64}`}
                                alt={change.path}
                                className="diff-binary-image"
                              />
                            ) : (
                              <a href={`data:${fallbackContent.mimeType};base64,${fallbackContent.base64}`} download={change.path}>
                                Download binary file
                              </a>
                            )}
                          </div>
                        )}
                      </div>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
          {!isLoading && !error && diffData && changes.length === 0 && (
            <div className="panel-empty">No changes found in this commit.</div>
          )}
        </div>
      </div>
    </section>
  );
}
