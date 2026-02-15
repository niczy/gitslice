import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { apiBaseUrl, fetchWithAuth } from '../utils/api.js';
import { formatBytes } from '../utils/format.js';
import { formatChangeType, formatTimestamp } from '../utils/format.js';
import { normalizeChange, normalizeChangeType, normalizeEntryType } from '../utils/normalize.js';
import { decodeBase64, highlightCode } from '../utils/highlight.js';
import SliceDropdown from './SliceDropdown.jsx';

const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico', 'avif']);

const IMAGE_MIME_TYPES = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  svg: 'image/svg+xml',
  bmp: 'image/bmp',
  ico: 'image/x-icon',
  avif: 'image/avif',
};

const getFileExtension = (filePath) => {
  if (!filePath || !filePath.includes('.')) {
    return '';
  }
  return filePath.split('.').pop()?.toLowerCase() || '';
};

const getPreviewMeta = (filePath, encodedContent) => {
  const extension = getFileExtension(filePath);
  if (extension === 'pdf') {
    return {
      mode: 'pdf',
      src: `data:application/pdf;base64,${encodedContent}`,
    };
  }

  if (IMAGE_EXTENSIONS.has(extension)) {
    return {
      mode: 'image',
      src: `data:${IMAGE_MIME_TYPES[extension] || 'image/*'};base64,${encodedContent}`,
    };
  }

  return { mode: 'text', src: '' };
};

// ---------------------------------------------------------------------------
// Repo Browser Component
// ---------------------------------------------------------------------------

export default function RepoBrowser({
  slices,
  currentSliceId,
  onSliceChange,
  onNavigateToDiff,
  isActive,
  slicesLoading,
  slicesError,
  onRefreshSlices,
}) {
  // Parse initial browser state from URL hash on mount
  const initialBrowserState = useMemo(() => {
    const raw = window.location.hash.replace(/^#\/?/, '');
    if (raw.startsWith('browser?')) {
      const params = new URLSearchParams(raw.slice(raw.indexOf('?') + 1));
      return {
        file: params.get('file') || '',
        slice: params.get('slice') || '',
        sliceHash: params.get('sliceHash') || '',
      };
    }
    return null;
  }, []);

  const [sliceHash, setSliceHash] = useState(initialBrowserState?.sliceHash || '');
  const [treeEntries, setTreeEntries] = useState({});
  const [expandedPaths, setExpandedPaths] = useState(['']);
  const [selectedFile, setSelectedFile] = useState(null);
  const [fileContent, setFileContent] = useState('');
  const [encodedFileContent, setEncodedFileContent] = useState('');
  const [draftContent, setDraftContent] = useState('');
  const [fileDrafts, setFileDrafts] = useState({});
  const [isEditingFile, setIsEditingFile] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');
  const [fileError, setFileError] = useState('');
  const [sidebarOpen, setSidebarOpen] = useState(() => window.innerWidth > 900);
  const [showHistory, setShowHistory] = useState(false);
  const [fileHistory, setFileHistory] = useState([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState('');

  // File to restore after root tree entries load (from URL hash)
  const pendingFileRef = useRef(initialBrowserState?.file || null);
  const hasAppliedInitialSliceRef = useRef(false);
  const highlightedContent = useMemo(() => highlightCode(fileContent), [fileContent]);
  const previewMeta = useMemo(() => getPreviewMeta(selectedFile, encodedFileContent), [selectedFile, encodedFileContent]);

  const breadcrumbs = useMemo(() => {
    if (!selectedFile) {
      return [{ name: 'root', path: '' }];
    }
    const parts = selectedFile.split('/');
    return [
      { name: 'root', path: '' },
      ...parts.map((part, index) => ({
        name: part,
        path: parts.slice(0, index + 1).join('/'),
      })),
    ];
  }, [selectedFile]);

  const sliceId = currentSliceId;

  const currentSliceLabel = useMemo(() => {
    const selectedSlice = slices.find((slice) => slice.slice_id === sliceId);
    return selectedSlice?.name || sliceId;
  }, [slices, sliceId]);

  // Update slice from URL if present
  useEffect(() => {
    if (hasAppliedInitialSliceRef.current) {
      return;
    }

    if (!initialBrowserState?.slice) {
      hasAppliedInitialSliceRef.current = true;
      return;
    }

    const sliceExists = slices.some((s) => s.slice_id === initialBrowserState.slice);
    if (sliceExists) {
      if (initialBrowserState.slice !== currentSliceId) {
        onSliceChange(initialBrowserState.slice);
      }
      hasAppliedInitialSliceRef.current = true;
    }
  }, [initialBrowserState, slices, currentSliceId, onSliceChange]);

  // Reset tree when slice changes
  useEffect(() => {
    setTreeEntries({});
    setExpandedPaths(['']);
    setSelectedFile(null);
    setFileContent('');
    setEncodedFileContent('');
    setDraftContent('');
    setFileDrafts({});
    setIsEditingFile(false);
    setFileError('');
  }, [sliceId, sliceHash]);

  const encodePath = (value) => value.split('/').map(encodeURIComponent).join('/');

  // Build URL for entries endpoint based on mode
  const buildEntriesUrl = (path) => {
    const params = new URLSearchParams();
    const encodedPath = path ? encodePath(path) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';

    if (sliceHash) {
      params.set('slice_version.slice_hash', sliceHash);
    }
    const queryString = params.toString();
    return `${apiBaseUrl}/v1/slices/${sliceId}/entries${pathSuffix}${queryString ? `?${queryString}` : ''}`;
  };

  // Build URL for file endpoint based on mode
  const buildFileUrl = (filePath) => {
    const encodedPath = filePath ? encodePath(filePath) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';
    const params = new URLSearchParams();

    if (sliceHash) {
      params.set('slice_version.slice_hash', sliceHash);
    }
    const queryString = params.toString();
    return `${apiBaseUrl}/v1/slices/${sliceId}/files${pathSuffix}${queryString ? `?${queryString}` : ''}`;
  };

  // Build URL for file history endpoint based on mode
  const buildHistoryUrl = (filePath) => {
    const encodedPath = filePath ? encodePath(filePath) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';
    return `${apiBaseUrl}/v1/slices/${sliceId}/files/history${pathSuffix}`;
  };

  // Fetch file history from the API
  const readErrorMessage = async (response, fallbackMessage) => {
    let detail = '';
    try {
      const text = await response.text();
      if (text) {
        try {
          const payload = JSON.parse(text);
          detail = payload?.message || payload?.error || '';
        } catch {
          detail = text;
        }
      }
    } catch {
      detail = '';
    }
    if (!detail) {
      return `${fallbackMessage} (${response.status})`;
    }
    return `${fallbackMessage}: ${detail}`;
  };

  // Fetch file history from the API
  const fetchFileHistory = async (filePath) => {
    if (!filePath) {
      return;
    }

    setHistoryLoading(true);
    setHistoryError('');

    try {
      const response = await fetchWithAuth(buildHistoryUrl(filePath));
      if (!response.ok) {
        throw new Error(`Request failed (${response.status})`);
      }
      const payload = await response.json();
      setFileHistory((payload.changes || []).map(normalizeChange));
    } catch (err) {
      setHistoryError('Unable to load file history.');
      setFileHistory([]);
    } finally {
      setHistoryLoading(false);
    }
  };

  // Toggle history panel
  const toggleHistory = () => {
    const newShowHistory = !showHistory;
    setShowHistory(newShowHistory);
    if (newShowHistory && selectedFile && fileHistory.length === 0) {
      fetchFileHistory(selectedFile);
    }
  };

  const canLoad = sliceId !== '';

  useEffect(() => {
    if (!canLoad) {
      return;
    }

    let active = true;
    const controller = new AbortController();

    const loadRoot = async () => {
      setIsLoading(true);
      setError('');
      setFileError('');

      try {
        const response = await fetchWithAuth(buildEntriesUrl(''), {
          signal: controller.signal,
        });
        if (!response.ok) {
          throw new Error(`Request failed (${response.status})`);
        }
        const payload = await response.json();
        if (!active) {
          return;
        }

        const rootEntries = payload.entries || [];

        // Restore file selection from URL hash if pending
        const pendingFile = pendingFileRef.current;
        if (pendingFile) {
          pendingFileRef.current = null;

          const parts = pendingFile.split('/');
          const allEntries = { '': rootEntries };
          const pathsToExpand = [''];

          // Load parent directory entries along the file path
          for (let i = 1; i < parts.length; i++) {
            if (!active) return;
            const parentPath = parts.slice(0, i).join('/');
            pathsToExpand.push(parentPath);
            try {
              const dirResp = await fetchWithAuth(buildEntriesUrl(parentPath), { signal: controller.signal });
              if (dirResp.ok) {
                const dirData = await dirResp.json();
                allEntries[parentPath] = dirData.entries || [];
              }
            } catch (e) {
              if (e?.name === 'AbortError') return;
              break;
            }
          }

          if (!active) return;
          setTreeEntries(allEntries);
          setExpandedPaths(pathsToExpand);
          setSelectedFile(pendingFile);

          // Load file content
          try {
            const fileResp = await fetchWithAuth(buildFileUrl(pendingFile), { signal: controller.signal });
            if (!fileResp.ok) {
              throw new Error(await readErrorMessage(fileResp, 'Unable to load file content'));
            }
            if (active) {
              const fileData = await fileResp.json();
              setFileError('');
              const content = fileData?.file?.content || '';
              setEncodedFileContent(content);
              setFileContent(decodeBase64(content));
            }
          } catch (e) {
            if (active && e?.name !== 'AbortError') {
              setFileContent('');
              setEncodedFileContent('');
              setFileError(e?.message || 'Unable to load file content.');
            }
          }
        } else {
          setTreeEntries({ '': rootEntries });
        }
      } catch (err) {
        if (!active || err?.name === 'AbortError') {
          return;
        }
        setError('Unable to load entries. Confirm the file gateway is running and the slice exists.');
      } finally {
        if (active) {
          setIsLoading(false);
        }
      }
    };

    loadRoot();

    return () => {
      active = false;
      controller.abort();
    };
  }, [sliceId, sliceHash]);

  const fetchEntries = async (path) => {
    if (!canLoad) {
      return;
    }

    setIsLoading(true);
    setError('');

    try {
      const response = await fetchWithAuth(buildEntriesUrl(path));
      if (!response.ok) {
        throw new Error(`Request failed (${response.status})`);
      }
      const payload = await response.json();
      setTreeEntries((prev) => ({
        ...prev,
        [path]: payload.entries || [],
      }));
    } catch (err) {
      setError('Unable to load entries. Confirm the file gateway is running and the slice exists.');
    } finally {
      setIsLoading(false);
    }
  };

  const toggleDirectory = async (entry) => {
    const isExpanded = expandedPaths.includes(entry.path);
    if (isExpanded) {
      setExpandedPaths((prev) => prev.filter((path) => path !== entry.path));
      return;
    }

    if (!treeEntries[entry.path]) {
      await fetchEntries(entry.path);
    }

    setExpandedPaths((prev) => [...prev, entry.path]);
  };

  // Push current browser state to navigation history
  const pushBrowserState = useCallback((file) => {
    const params = new URLSearchParams();
    if (file) params.set('file', file);
    if (sliceId) params.set('slice', sliceId);
    if (sliceHash) params.set('sliceHash', sliceHash);
    const qs = params.toString();
    window.history.replaceState(null, '', qs ? `#/browser?${qs}` : '#/browser');
  }, [sliceId, sliceHash]);

  // Update URL when browser state changes or page becomes active again
  useEffect(() => {
    if (isActive && sliceId) {
      pushBrowserState(selectedFile || '');
    }
  }, [isActive, pushBrowserState, selectedFile, sliceId, sliceHash]);

  const handleEntryClick = async (entry) => {
    const entryKind = normalizeEntryType(entry.type);
    if (entryKind === 'directory') {
      await toggleDirectory(entry);
      return;
    }

    // Close sidebar on mobile after selecting a file
    if (window.innerWidth <= 900) {
      setSidebarOpen(false);
    }

    setSelectedFile(entry.path);
    setFileContent('');
    setEncodedFileContent('');
    setDraftContent('');
    setIsLoading(true);
    setError('');
    setFileError('');
    setShowHistory(false);
    setFileHistory([]);
    setHistoryError('');

    if (Object.prototype.hasOwnProperty.call(fileDrafts, entry.path)) {
      setFileContent(fileDrafts[entry.path]);
      setEncodedFileContent('');
      setDraftContent(fileDrafts[entry.path]);
      setIsEditingFile(false);
      setIsLoading(false);
      return;
    }

    try {
      const response = await fetchWithAuth(buildFileUrl(entry.path));
      if (!response.ok) {
        throw new Error(await readErrorMessage(response, 'Unable to load file content'));
      }
      const payload = await response.json();
      const content = payload?.file?.content || '';
      setFileError('');
      setEncodedFileContent(content);
      const decodedContent = decodeBase64(content);
      setFileContent(decodedContent);
      setDraftContent(decodedContent);
      setIsEditingFile(false);
    } catch (err) {
      setFileContent('');
      setEncodedFileContent('');
      setFileError(err?.message || 'Unable to load file content.');
    } finally {
      setIsLoading(false);
    }
  };

  const createFolder = () => {
    if (!canLoad) {
      return;
    }

    const rawPath = window.prompt('Enter folder path (for example: docs/guides):', '');
    if (!rawPath) {
      return;
    }

    const cleanPath = rawPath.trim().replace(/^\/+|\/+$/g, '');
    if (!cleanPath) {
      return;
    }

    const parts = cleanPath.split('/').filter(Boolean);
    setTreeEntries((prev) => {
      const next = { ...prev };
      let parentPath = '';

      for (const part of parts) {
        const path = parentPath ? `${parentPath}/${part}` : part;
        const parentEntries = next[parentPath] || [];
        const alreadyExists = parentEntries.some((entry) => entry.path === path);
        if (!alreadyExists) {
          next[parentPath] = [
            ...parentEntries,
            { name: part, path, type: 'directory' },
          ];
        }
        if (!next[path]) {
          next[path] = [];
        }
        parentPath = path;
      }

      return next;
    });

    setExpandedPaths((prev) => [...new Set([...prev, '', ...parts.map((_, index) => parts.slice(0, index + 1).join('/'))])]);
  };

  const createFile = () => {
    if (!canLoad) {
      return;
    }

    const rawPath = window.prompt('Enter file path (for example: docs/notes.txt):', selectedFile ? `${selectedFile.split('/').slice(0, -1).join('/')}/` : '');
    if (!rawPath) {
      return;
    }

    const cleanPath = rawPath.trim().replace(/^\/+/, '').replace(/\/+$/, '');
    if (!cleanPath) {
      return;
    }

    const parts = cleanPath.split('/').filter(Boolean);
    const fileName = parts[parts.length - 1];
    const parentParts = parts.slice(0, -1);

    setTreeEntries((prev) => {
      const next = { ...prev };
      let parentPath = '';

      for (const part of parentParts) {
        const path = parentPath ? `${parentPath}/${part}` : part;
        const parentEntries = next[parentPath] || [];
        const exists = parentEntries.some((entry) => entry.path === path);
        if (!exists) {
          next[parentPath] = [...parentEntries, { name: part, path, type: 'directory' }];
        }
        if (!next[path]) {
          next[path] = [];
        }
        parentPath = path;
      }

      const parentEntries = next[parentPath] || [];
      const alreadyExists = parentEntries.some((entry) => entry.path === cleanPath);
      if (!alreadyExists) {
        next[parentPath] = [...parentEntries, { name: fileName, path: cleanPath, type: 'file', size: 0 }];
      }

      return next;
    });

    const pathsToExpand = parentParts.map((_, index) => parentParts.slice(0, index + 1).join('/'));
    setExpandedPaths((prev) => [...new Set([...prev, '', ...pathsToExpand])]);
    setSelectedFile(cleanPath);
    setFileContent('');
    setEncodedFileContent('');
    setDraftContent('');
    setFileError('');
    setShowHistory(false);
    setFileHistory([]);
    setHistoryError('');
    setIsEditingFile(true);
    setFileDrafts((prev) => ({ ...prev, [cleanPath]: '' }));
  };

  const confirmFileEdit = () => {
    if (!selectedFile) {
      return;
    }
    setFileDrafts((prev) => ({ ...prev, [selectedFile]: draftContent }));
    setFileContent(draftContent);
    setEncodedFileContent('');
    setIsEditingFile(false);
    const parentPath = selectedFile.includes('/') ? selectedFile.split('/').slice(0, -1).join('/') : '';
    setTreeEntries((prev) => {
      const entries = prev[parentPath] || [];
      const nextEntries = entries.map((entry) => {
        if (entry.path !== selectedFile) {
          return entry;
        }
        return { ...entry, size: draftContent.length };
      });
      return { ...prev, [parentPath]: nextEntries };
    });
  };

  const handleBreadcrumbClick = async (path) => {
    if (path && !treeEntries[path]) {
      await fetchEntries(path);
    }
    if (path) {
      setExpandedPaths((prev) => (prev.includes(path) ? prev : [...prev, path]));
      return;
    }
    setExpandedPaths(['']);
  };

  const renderTree = (path, depth = 0) => {
    const entries = treeEntries[path] || [];
    return (
      <ul className="tree-list">
        {entries.map((entry) => {
          const entryKind = normalizeEntryType(entry.type);
          const isExpanded = expandedPaths.includes(entry.path);
          return (
            <li key={entry.path}>
              <button
                type="button"
                className={`tree-entry ${entryKind}`}
                style={{ paddingLeft: `${depth * 14 + 8}px` }}
                onClick={() => handleEntryClick(entry)}
              >
                <span className="tree-caret">{entryKind === 'directory' ? (isExpanded ? '▼' : '▶') : '•'}</span>
                <span className="entry-icon">{entryKind === 'directory' ? '📁' : '📄'}</span>
                <span className="entry-name">{entry.name}</span>
                {entryKind === 'file' && <span className="entry-meta">{formatBytes(entry.size)}</span>}
              </button>
              {entryKind === 'directory' && isExpanded && renderTree(entry.path, depth + 1)}
            </li>
          );
        })}
      </ul>
    );
  };

  return (
    <section className="repo-browser">
      <div className="repo-main">
        <div className={`repo-layout ${sidebarOpen ? '' : 'sidebar-collapsed'}`}>
          <div
            className={`sidebar-overlay${sidebarOpen ? ' visible' : ''}`}
            onClick={() => setSidebarOpen(false)}
          />
          <aside className={`repo-sidebar ${sidebarOpen ? 'open' : 'closed'}`}>
            <div className="panel-header">
              <h3>File tree {currentSliceLabel ? `· ${currentSliceLabel}` : ''}</h3>
              <div className="panel-header-actions">
                <span
                  className={`tree-loading-indicator${isLoading ? ' visible' : ''}`}
                  role="status"
                  aria-live="polite"
                  aria-label={isLoading ? 'Loading folders' : undefined}
                >
                  <span className="tree-loading-dot" aria-hidden="true" />
                </span>
                <button
                  type="button"
                  className="tree-action-btn"
                  onClick={createFolder}
                  title="Create folder"
                >
                  + Folder
                </button>
                <button
                  type="button"
                  className="tree-action-btn"
                  onClick={createFile}
                  title="Create file"
                >
                  + File
                </button>
                <button
                  type="button"
                  className="sidebar-toggle"
                  onClick={() => setSidebarOpen(false)}
                  aria-label="Close sidebar"
                  title="Close sidebar"
                >
                  ✕
                </button>
              </div>
            </div>
            <div className="sidebar-content">
              <div className="sidebar-slice-switcher">
                <SliceDropdown
                  slices={slices}
                  currentSliceId={currentSliceId}
                  onSelectSlice={onSliceChange}
                  loading={slicesLoading}
                  error={slicesError}
                  onRefresh={onRefreshSlices}
                  className="slice-dropdown--sidebar"
                />
              </div>
              {error && <div className="panel-error">{error}</div>}
              {!canLoad && <div className="panel-empty">Choose a slice to browse files.</div>}
              {canLoad && !isLoading && !error && (treeEntries[''] || []).length === 0 && (
                <div className="panel-empty">No entries found.</div>
              )}
              {canLoad && renderTree('')}
            </div>
          </aside>

          <div className="repo-code">
            <div className="code-header">
              <div className="code-header-left">
                {!sidebarOpen && (
                  <button
                    type="button"
                    className="sidebar-toggle open-btn"
                    onClick={() => setSidebarOpen(true)}
                    aria-label="Open sidebar"
                    title="Open file tree"
                    data-testid="sidebar-toggle"
                  >
                    ☰
                  </button>
                )}
                <div>
                  <h3>{selectedFile ? selectedFile : 'Select a file'}</h3>
                  <div className="breadcrumbs">
                    {breadcrumbs.map((crumb, index) => (
                      <button
                        key={crumb.path || 'root'}
                        type="button"
                        className="breadcrumb"
                        onClick={() => handleBreadcrumbClick(crumb.path)}
                      >
                        {crumb.name}
                        {index < breadcrumbs.length - 1 && <span className="separator">/</span>}
                      </button>
                    ))}
                  </div>
                </div>
              </div>
              <div className="code-header-actions">
                {selectedFile && <span className="status">{formatBytes(fileContent.length)}</span>}
                {selectedFile && !showHistory && (
                  <>
                    <button
                      type="button"
                      className={`history-toggle ${isEditingFile ? 'active' : ''}`}
                      onClick={() => {
                        if (isEditingFile) {
                          setDraftContent(fileContent);
                          setIsEditingFile(false);
                        } else {
                          setDraftContent(fileContent);
                          setIsEditingFile(true);
                        }
                      }}
                    >
                      {isEditingFile ? 'Cancel' : '✏️ Edit'}
                    </button>
                    {isEditingFile && (
                      <button
                        type="button"
                        className="history-toggle active"
                        onClick={confirmFileEdit}
                      >
                        ✅ Confirm
                      </button>
                    )}
                  </>
                )}
                {selectedFile && (
                  <button
                    type="button"
                    className={`history-toggle ${showHistory ? 'active' : ''}`}
                    onClick={toggleHistory}
                    data-testid="history-toggle"
                    title={showHistory ? 'Show file content' : 'Show commit history'}
                  >
                    {showHistory ? '📄 Content' : '📜 History'}
                  </button>
                )}
              </div>
            </div>
            <div className="code-content">
              {!selectedFile && <div className="panel-empty">Choose a file from the tree to preview its contents.</div>}
              {selectedFile && !showHistory && fileError && <div className="panel-error">{fileError}</div>}
              {selectedFile && !showHistory && (
                !fileError && (
                  isEditingFile ? (
                    <textarea
                      className="file-editor"
                      value={draftContent}
                      onChange={(event) => setDraftContent(event.target.value)}
                      spellCheck={false}
                    />
                  ) : previewMeta.mode === 'image' ? (
                    <div className="media-preview-wrapper">
                      <img className="media-preview-image" src={previewMeta.src} alt={selectedFile} />
                    </div>
                  ) : previewMeta.mode === 'pdf' ? (
                    <iframe
                      className="media-preview-pdf"
                      src={previewMeta.src}
                      title={`${selectedFile} PDF preview`}
                    />
                  ) : (
                    <pre className="file-preview">
                      <code dangerouslySetInnerHTML={{ __html: highlightedContent || 'File is empty.' }} />
                    </pre>
                  )
                )
              )}
              {selectedFile && showHistory && (
                <div className="history-panel" data-testid="history-panel">
                  {historyLoading && <div className="history-loading">Loading history...</div>}
                  {historyError && <div className="panel-error">{historyError}</div>}
                  {!historyLoading && !historyError && fileHistory.length === 0 && (
                    <div className="panel-empty">No history available for this file.</div>
                  )}
                  {!historyLoading && fileHistory.length > 0 && (
                    <ul className="history-list">
                      {fileHistory.map((change) => (
                        <li key={change.id} className="history-item" data-testid="history-item">
                          <div className="history-item-header">
                            <span className={`change-type change-type-${normalizeChangeType(change.change_type)}`}>
                              {formatChangeType(change.change_type)}
                            </span>
                            <a
                              className="commit-hash commit-diff-link"
                              title={change.commit_hash}
                              href="#"
                              data-testid="commit-diff-link"
                              onClick={(e) => {
                                e.preventDefault();
                                if (change.commit_hash && onNavigateToDiff) {
                                  onNavigateToDiff(change.commit_hash);
                                }
                              }}
                            >
                              {change.commit_hash ? change.commit_hash.slice(0, 7) : 'unknown'}
                            </a>
                          </div>
                          <div className="history-item-message">{change.message || 'No message'}</div>
                          <div className="history-item-meta">
                            <span className="history-author">{change.author || 'Unknown'}</span>
                            <span className="history-date">{formatTimestamp(change.timestamp)}</span>
                            {(change.lines_added > 0 || change.lines_deleted > 0) && (
                              <span className="history-lines">
                                <span className="lines-added">+{change.lines_added || 0}</span>
                                <span className="lines-deleted">-{change.lines_deleted || 0}</span>
                              </span>
                            )}
                          </div>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
