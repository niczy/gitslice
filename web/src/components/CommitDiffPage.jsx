import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Undo2 } from 'lucide-react';
import { apiBaseUrl, createRevertChangeset, fetchWithAuth } from '../utils/api.js';
import { normalizeChangeType, normalizeDiffResponse } from '../utils/normalize.js';
import { renderDiffPatch, renderSplitDiffPatch } from '../utils/diff.jsx';
import { decodeBase64 } from '../utils/highlight.js';
import { base64ToBytes } from '../../shared/runtime.js';
import { useCommitDiffData } from '../features/diff/useCommitDiffData.js';
import {
  DiffFileItemHeader,
  DiffFilePanel,
  DiffSummary,
  DiffTopBar,
  DiffViewToggle,
  getDiffFileKey,
  scrollDiffFileIntoView,
} from './diff/DiffDetailLayout.jsx';
import { Button } from './ui/button.jsx';

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
    const raw = base64ToBytes(encoded);
    const sampleSize = Math.min(raw.length, 2048);
    let controlChars = 0;
    for (let index = 0; index < sampleSize; index += 1) {
      const code = raw[index];
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

function shouldDeferPatchLoad(changes = []) {
  if (changes.length === 0) {
    return false;
  }
  if (changes.length > LAZY_PATCH_FILE_COUNT_THRESHOLD) {
    return true;
  }
  return changes.some((change) => ((change.lines_added || 0) + (change.lines_deleted || 0)) > LAZY_PATCH_LINE_THRESHOLD);
}

// ---------------------------------------------------------------------------
// Commit Diff Page Component
// ---------------------------------------------------------------------------

export default function CommitDiffPage({
  commitHash,
  onBack,
  onOpenChangesetDiff,
  initialCommitHash = '',
  initialDiffData = null,
  initialDiffError = '',
}) {
  const {
    dataRevision,
    diffData,
    error,
    isLoading,
  } = useCommitDiffData({
    commitHash,
    initialCommitHash,
    initialDiffData,
    initialDiffError,
  });
  const [selectedFileId, setSelectedFileId] = useState(null);
  const [viewMode, setViewMode] = useState('unified'); // 'unified' | 'split'
  const [fallbackContentByFile, setFallbackContentByFile] = useState({});
  const [binaryVisibleByFile, setBinaryVisibleByFile] = useState({});
  const [patchByFile, setPatchByFile] = useState({});
  const [hasLoadedPatches, setHasLoadedPatches] = useState(false);
  const [isPatchLoading, setIsPatchLoading] = useState(false);
  const [patchLoadError, setPatchLoadError] = useState('');
  const [isRevertingDiff, setIsRevertingDiff] = useState(false);
  const [actionError, setActionError] = useState('');
  const fileRefs = useRef({});
  const panelItemRefs = useRef({});
  const diffContentRef = useRef(null);

  const encodePath = useCallback((value) => value.split('/').map(encodeURIComponent).join('/'), []);

  useEffect(() => {
    setFallbackContentByFile({});
    setBinaryVisibleByFile({});
    setPatchByFile({});
    setHasLoadedPatches(false);
    setPatchLoadError('');
  }, [dataRevision]);

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
    return shouldDeferPatchLoad(diffData?.changes || []);
  }, [diffData]);


  useEffect(() => {
    if (!diffData || hasLoadedPatches || isPatchLoading) {
      return;
    }

    const shouldDefer = shouldDeferPatchLoad(diffData?.changes || []);
    if (!shouldDefer) {
      loadPatches();
      return;
    }

    const diffContentEl = diffContentRef.current;
    if (!diffContentEl) {
      return;
    }

    const handleWheel = (event) => {
      if (Math.abs(event.deltaY) > 0 && diffContentEl.scrollTop + event.deltaY >= LAZY_PATCH_SCROLL_TRIGGER) {
        loadPatches();
      }
    };

    const handleTouchMove = () => {
      loadPatches();
    };

    const handleKeyboardScroll = (event) => {
      if (['ArrowDown', 'PageDown', 'End', ' '].includes(event.key)) {
        loadPatches();
      }
    };

    diffContentEl.addEventListener('wheel', handleWheel, { passive: true });
    diffContentEl.addEventListener('touchmove', handleTouchMove, { passive: true });
    diffContentEl.addEventListener('keydown', handleKeyboardScroll);
    return () => {
      diffContentEl.removeEventListener('wheel', handleWheel);
      diffContentEl.removeEventListener('touchmove', handleTouchMove);
      diffContentEl.removeEventListener('keydown', handleKeyboardScroll);
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

    scrollDiffFileIntoView(fileKey, panelItemRefs, fileRefs, diffContentRef);
  }, [loadPatches]);

  const revertSliceId = useMemo(() => {
    const sliceIDs = new Set((diffData?.changes || [])
      .map((change) => change?.slice_id)
      .filter((value) => typeof value === 'string' && value.trim() !== ''));
    if (sliceIDs.size === 1) {
      return [...sliceIDs][0];
    }
    return '';
  }, [diffData]);

  const handleRevertDiff = useCallback(async () => {
    if (!commitHash || isRevertingDiff) {
      return;
    }
    setActionError('');
    setIsRevertingDiff(true);
    try {
      const response = await createRevertChangeset(commitHash, revertSliceId);
      const changesetID = response?.changesetId || response?.changeset_id;
      if (!changesetID) {
        throw new Error('missing changeset id');
      }
      onOpenChangesetDiff?.(changesetID);
    } catch (err) {
      setActionError(err?.message || 'Unable to create revert changeset.');
    } finally {
      setIsRevertingDiff(false);
    }
  }, [commitHash, isRevertingDiff, onOpenChangesetDiff, revertSliceId]);

  return (
    <section className="commit-diff-page diff-detail-page" data-testid="commit-diff-page">
      <DiffTopBar
        backLabel="Back to browser"
        backTestId="diff-back-btn"
        controls={(
          <div className="diff-detail-controls">
            <DiffSummary data={diffData} includeRenamed testId="diff-summary" />
            <DiffViewToggle
              viewMode={viewMode}
              onChange={setViewMode}
              testId="diff-view-toggle"
              unifiedButtonTestId="diff-view-unified-btn"
              splitButtonTestId="diff-view-split-btn"
            />
            <div className="changeset-actions" data-testid="diff-actions">
              <Button
                type="button"
                variant="secondary"
                className="diff-revert-button"
                onClick={handleRevertDiff}
                disabled={isLoading || isRevertingDiff || !commitHash}
                data-testid="diff-revert-btn"
              >
                <Undo2 size={15} aria-hidden="true" />
                {isRevertingDiff ? 'Reverting...' : 'Revert'}
              </Button>
            </div>
          </div>
        )}
        eyebrow="Commit diff"
        onBack={onBack}
        title={(
          <>
            Commit <span className="commit-hash">{commitHash ? commitHash.slice(0, 12) : ''}</span>
          </>
        )}
        titleTestId="diff-commit-title"
      />

      <div className="diff-layout">
        {/* Left file panel */}
        {!isLoading && !error && changes.length > 0 && (
          <DiffFilePanel
            changes={changes}
            getFileKey={getDiffFileKey}
            headerLabel={`Files (${changes.length})`}
            itemTestId="diff-file-panel-item"
            onFileSelect={handleFileSelect}
            panelItemRefs={panelItemRefs}
            selectedFileId={selectedFileId}
            testId="diff-file-panel"
          />
        )}

        {/* Main diff content */}
        <div className="diff-content" ref={diffContentRef}>
          {actionError && <div className="panel-error diff-action-error">{actionError}</div>}
          {isLoading && <div className="diff-loading">Loading commit changes...</div>}
          {error && <div className="panel-error">{error}</div>}
          {!isLoading && !error && shouldLazyLoadPatches && !hasLoadedPatches && (
            <div className="diff-loading diff-patch-lazy-state" data-testid="diff-patch-lazy-state">
              <p>{isPatchLoading ? 'Loading patch content…' : 'Patch content will load as you scroll.'}</p>
              {patchLoadError && (
                <>
                  <p className="panel-error">{patchLoadError}</p>
                  <Button type="button" variant="ghost" onClick={loadPatches} data-testid="diff-retry-load-patches-btn" disabled={isPatchLoading}>
                    Retry patch load
                  </Button>
                </>
              )}
            </div>
          )}
          {!isLoading && !error && diffData && (
            <ul className="diff-file-list" data-testid="diff-file-list">
              {changes.map((change) => {
                const fileKey = getDiffFileKey(change);
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
                    <DiffFileItemHeader change={change} pathTestId="diff-file-path" />
                    {change.patch && viewMode === 'unified' && (
                      isBinaryPatch && !showBinary ? (
                        <div className="diff-binary-block" data-testid="diff-file-binary-block">
                          <p>This file contains binary content. Click to view.</p>
                          <Button
                            type="button"
                            variant="ghost"
                            data-testid="diff-file-view-binary-btn"
                            onClick={() => setBinaryVisibleByFile((previous) => ({ ...previous, [fileKey]: true }))}
                          >
                            View binary
                          </Button>
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
                          <Button
                            type="button"
                            variant="ghost"
                            data-testid="diff-file-view-binary-btn"
                            onClick={() => setBinaryVisibleByFile((previous) => ({ ...previous, [fileKey]: true }))}
                          >
                            View binary
                          </Button>
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
                            <Button
                              type="button"
                              variant="ghost"
                              data-testid="diff-file-view-binary-btn"
                              onClick={() => setBinaryVisibleByFile((previous) => ({ ...previous, [fileKey]: true }))}
                            >
                              View binary
                            </Button>
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
