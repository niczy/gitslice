import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { apiBaseUrl, fetchWithAuth } from '../utils/api.js';
import { formatBytes } from '../utils/format.js';
import { formatChangeType, formatTimestamp } from '../utils/format.js';
import { normalizeChange, normalizeChangeType, normalizeEntryType } from '../utils/normalize.js';
import { decodeBase64, highlightCode } from '../utils/highlight.js';

// ---------------------------------------------------------------------------
// Repo Browser Component
// ---------------------------------------------------------------------------

export default function RepoBrowser({ slices, currentSliceId, onSliceChange, onNavigateToDiff, isActive }) {
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
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');
  const [sidebarOpen, setSidebarOpen] = useState(() => window.innerWidth > 900);
  const [showHistory, setShowHistory] = useState(false);
  const [fileHistory, setFileHistory] = useState([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState('');

  // File to restore after root tree entries load (from URL hash)
  const pendingFileRef = useRef(initialBrowserState?.file || null);
  const highlightedContent = useMemo(() => highlightCode(fileContent), [fileContent]);

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

  // Update slice from URL if present
  useEffect(() => {
    if (initialBrowserState?.slice && initialBrowserState.slice !== currentSliceId) {
      const sliceExists = slices.some(s => s.slice_id === initialBrowserState.slice);
      if (sliceExists) {
        onSliceChange(initialBrowserState.slice);
      }
    }
  }, [initialBrowserState, slices, currentSliceId, onSliceChange]);

  // Reset tree when slice changes
  useEffect(() => {
    setTreeEntries({});
    setExpandedPaths(['']);
    setSelectedFile(null);
    setFileContent('');
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
            if (fileResp.ok && active) {
              const fileData = await fileResp.json();
              setFileContent(decodeBase64(fileData?.file?.content || ''));
            }
          } catch (e) {
            if (active && e?.name !== 'AbortError') {
              setError('Unable to load the file.');
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
    setIsLoading(true);
    setError('');
    setShowHistory(false);
    setFileHistory([]);
    setHistoryError('');

    try {
      const response = await fetchWithAuth(buildFileUrl(entry.path));
      if (!response.ok) {
        throw new Error(`Request failed (${response.status})`);
      }
      const payload = await response.json();
      const content = payload?.file?.content || '';
      setFileContent(decodeBase64(content));
    } catch (err) {
      setError('Unable to load the file.');
    } finally {
      setIsLoading(false);
    }
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
              <h3>File tree</h3>
              <div className="panel-header-actions">
                {isLoading && <span className="status">Loading…</span>}
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
              {selectedFile && !showHistory && (
                <pre className="file-preview">
                  <code dangerouslySetInnerHTML={{ __html: highlightedContent || 'No content available yet.' }} />
                </pre>
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
