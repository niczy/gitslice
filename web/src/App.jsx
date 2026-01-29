import { useEffect, useMemo, useState } from 'react';
import './styles.css';

const features = [
  {
    title: 'Speed',
    description:
      'Slice only what you need, reuse the rest. Move from idea to review faster with focused diffs and reproducible runs.',
  },
  {
    title: 'Safety',
    description:
      'Keep changes isolated. Guardrails make it easy to test, share, and roll back without risking the rest of your repo.',
  },
  {
    title: 'Tooling',
    description:
      'First-class CLI and services for orchestrating slices, automations, and integrations with your existing workflows.',
  },
];

const apiBaseUrl = import.meta.env.VITE_FILE_API_BASE_URL || '';

function App() {
  const [activePage, setActivePage] = useState('landing');
  const [diffCommitHash, setDiffCommitHash] = useState('');
  const githubUrl = 'https://github.com/niczy/gitslice';

  const navigateToDiff = (commitHash) => {
    setDiffCommitHash(commitHash);
    setActivePage('diff');
  };

  return (
    <div className="app-shell">
      <header className="top-bar">
        <button type="button" className="brand" onClick={() => setActivePage('landing')}>
          <span className="brand-icon">◆</span>
          <span className="brand-text">Git Slice</span>
        </button>
        <div className="top-bar-actions">
          <a className="ghost" href={githubUrl} target="_blank" rel="noreferrer" data-testid="topbar-github-link">
            GitHub
          </a>
          <button
            type="button"
            className="primary"
            data-testid="topbar-repo-browser"
            onClick={() => setActivePage('browser')}
          >
            Repo Browser
          </button>
        </div>
      </header>

      <main className="page">
        {activePage === 'landing' && <OverviewPage onBrowseRepo={() => setActivePage('browser')} />}
        {activePage === 'browser' && <RepoBrowser onNavigateToDiff={navigateToDiff} />}
        {activePage === 'diff' && (
          <CommitDiffPage commitHash={diffCommitHash} onBack={() => setActivePage('browser')} />
        )}
      </main>

      <footer className="footer">
        <p>
          Git Slice • Slice smart. Ship faster. •{' '}
          <a href={githubUrl} target="_blank" rel="noreferrer">
            GitHub
          </a>
        </p>
      </footer>
    </div>
  );
}

function OverviewPage({ onBrowseRepo }) {
  return (
    <>
      <section className="hero">
        <div className="hero-content">
          <p className="eyebrow">Introducing Git Slice</p>
          <h1>Slice-based workflows for shipping more confidently.</h1>
          <p className="lede">
            Git Slice lets teams carve out focused slices of work, run them end-to-end, and merge back with clarity. Each slice is
            defined by a metadata file path, so teams can standardize how a given area of the repo is scoped and tested.
          </p>
          <div className="cta-row">
            <button type="button" className="primary" onClick={onBrowseRepo}>
              Open repo browser
            </button>
            <a className="ghost" href="mailto:team@gitslice.dev">
              Contact the team
            </a>
          </div>
        </div>
        <div className="hero-panel">
          <div className="hero-card">
            <p className="eyebrow">Slice-first development</p>
            <h2>Run isolated slices from idea to production</h2>
            <p>
              Define a slice around a task, pull the dependencies you need, and keep every change traceable. Git Slice keeps
              delivery focused so teams can move without long-lived branches.
            </p>
          </div>
        </div>
      </section>

      <section id="overview" className="section card">
        <div className="section-header">
          <h2>How slices keep changes focused</h2>
          <p>
            A slice captures only the files and services you specify, plus any required dependencies. That means slimmer clones,
            deterministic test runs, and a clean diff that can be merged without dragging unrelated changes along for the ride.
          </p>
        </div>
        <div className="steps">
          <div className="step">
            <div className="step-number">1</div>
            <div>
              <h3>Carve out the slice</h3>
              <p>Define the slice in a metadata file that lists the files, directories, and services required for the task.</p>
            </div>
          </div>
          <div className="step">
            <div className="step-number">2</div>
            <div>
              <h3>Iterate quickly</h3>
              <p>Use the CLI to check out the slice by its metadata file path and run targeted tests.</p>
            </div>
          </div>
          <div className="step">
            <div className="step-number">3</div>
            <div>
              <h3>Merge with confidence</h3>
              <p>Every slice ships with reproducible logs, checks, and diffs so merging back is predictable and low-risk.</p>
            </div>
          </div>
        </div>
      </section>

      <section className="section card quickstart">
        <div className="section-header">
          <p className="eyebrow">Quick start</p>
          <h2>Go from repo to slice in minutes</h2>
          <p>Use the CLI to check out a slice using its metadata file path.</p>
        </div>
        <div className="quickstart-grid">
          <div className="quickstart-step">
            <h3>1. Define a slice file</h3>
            <pre className="code-block">
              <code>cat slices/auth-refresh.toml</code>
            </pre>
            <p>Store the slice definition in a file path such as <code>slices/auth-refresh.toml</code>.</p>
          </div>
          <div className="quickstart-step">
            <h3>2. Check out the slice</h3>
            <pre className="code-block">
              <code>gs slice checkout /abs/path/to/slices/auth-refresh.toml</code>
            </pre>
            <p>Use the slice metadata path to pull just the required scope into a local workspace.</p>
          </div>
          <div className="quickstart-step">
            <h3>3. Validate the slice</h3>
            <pre className="code-block">
              <code>gs slice checkout /abs/path/to/slices/auth-refresh.toml --commit HEAD</code>
            </pre>
            <p>Pin a commit when needed so the slice scope is reproducible for reviewers and CI.</p>
          </div>
        </div>
      </section>

      <section id="features" className="section features">
        <div className="section-header">
          <p className="eyebrow">Built for teams</p>
          <h2>Feature highlights</h2>
          <p>Everything you need to move fast without losing control.</p>
        </div>
        <div className="feature-grid">
          {features.map((feature) => (
            <div key={feature.title} className="feature card">
              <h3>{feature.title}</h3>
              <p>{feature.description}</p>
            </div>
          ))}
        </div>
      </section>

      <section className="section cta card">
        <div>
          <p className="eyebrow">Ready to slice?</p>
          <h2>Bring slice-based delivery to your team.</h2>
          <p>Start with the CLI and wire it into your CI/CD. Git Slice is built to plug into your existing workflows.</p>
        </div>
        <a className="primary" href="mailto:team@gitslice.dev">
          Contact the team
        </a>
      </section>
    </>
  );
}

function RepoBrowser({ onNavigateToDiff }) {
  const [browseMode, setBrowseMode] = useState('root'); // 'root' | 'slice'
  const [sliceId, setSliceId] = useState('');
  const [commitHash, setCommitHash] = useState(''); // For root mode
  const [sliceHash, setSliceHash] = useState(''); // For slice mode
  const [treeEntries, setTreeEntries] = useState({});
  const [expandedPaths, setExpandedPaths] = useState(['']);
  const [selectedFile, setSelectedFile] = useState(null);
  const [selectedDirectory, setSelectedDirectory] = useState(null);
  const [fileContent, setFileContent] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [showHistory, setShowHistory] = useState(false);
  const [fileHistory, setFileHistory] = useState([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState('');
  const highlightedContent = useMemo(() => highlightCode(fileContent), [fileContent]);

  const selectedPath = selectedFile || selectedDirectory;

  const breadcrumbs = useMemo(() => {
    const activePath = selectedFile || selectedDirectory;
    if (!activePath) {
      return [{ name: 'root', path: '' }];
    }
    const parts = activePath.split('/');
    return [
      { name: 'root', path: '' },
      ...parts.map((part, index) => ({
        name: part,
        path: parts.slice(0, index + 1).join('/'),
      })),
    ];
  }, [selectedFile, selectedDirectory]);

  // Reset tree when browse mode or slice changes
  useEffect(() => {
    setTreeEntries({});
    setExpandedPaths(['']);
    setSelectedFile(null);
    setSelectedDirectory(null);
    setFileContent('');
  }, [browseMode, sliceId, commitHash, sliceHash]);

  const encodePath = (value) => value.split('/').map(encodeURIComponent).join('/');

  // Build URL for entries endpoint based on mode
  const buildEntriesUrl = (path) => {
    const params = new URLSearchParams();
    const encodedPath = path ? encodePath(path) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';

    if (browseMode === 'root') {
      if (commitHash) {
        params.set('commit_hash', commitHash);
      }
      const queryString = params.toString();
      return `${apiBaseUrl}/v1/files/entries${pathSuffix}${queryString ? `?${queryString}` : ''}`;
    }
    // Slice mode
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

    if (browseMode === 'root') {
      if (commitHash) {
        params.set('commit_hash', commitHash);
      }
      const queryString = params.toString();
      return `${apiBaseUrl}/v1/files${pathSuffix}${queryString ? `?${queryString}` : ''}`;
    }
    // Slice mode
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

    if (browseMode === 'root') {
      return `${apiBaseUrl}/v1/files/history${pathSuffix}`;
    }
    // Slice mode
    return `${apiBaseUrl}/v1/slices/${sliceId}/files/history${pathSuffix}`;
  };

  // Build URL for directory history endpoint based on mode
  const buildDirectoryHistoryUrl = (dirPath) => {
    const encodedPath = dirPath ? encodePath(dirPath) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';

    if (browseMode === 'root') {
      return `${apiBaseUrl}/v1/directories/history${pathSuffix}`;
    }
    // Slice mode
    return `${apiBaseUrl}/v1/slices/${sliceId}/directories/history${pathSuffix}`;
  };

  // Fetch history from the API (works for both files and directories)
  const fetchHistory = async (path, isDirectory = false) => {
    if (!path) {
      return;
    }

    setHistoryLoading(true);
    setHistoryError('');

    try {
      const url = isDirectory ? buildDirectoryHistoryUrl(path) : buildHistoryUrl(path);
      const response = await fetch(url);
      if (!response.ok) {
        throw new Error(`Request failed (${response.status})`);
      }
      const payload = await response.json();
      setFileHistory(payload.changes || []);
    } catch (err) {
      setHistoryError(isDirectory ? 'Unable to load directory history.' : 'Unable to load file history.');
      setFileHistory([]);
    } finally {
      setHistoryLoading(false);
    }
  };

  const fetchFileHistory = (filePath) => fetchHistory(filePath, false);
  const fetchDirectoryHistory = (dirPath) => fetchHistory(dirPath, true);

  // Toggle history panel
  const toggleHistory = () => {
    const newShowHistory = !showHistory;
    setShowHistory(newShowHistory);
    if (newShowHistory && fileHistory.length === 0) {
      if (selectedFile) {
        fetchFileHistory(selectedFile);
      } else if (selectedDirectory) {
        fetchDirectoryHistory(selectedDirectory);
      }
    }
  };

  // Check if we can load (root mode always ready, slice mode needs sliceId)
  const canLoad = browseMode === 'root' || (browseMode === 'slice' && sliceId);

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
        const response = await fetch(buildEntriesUrl(''), {
          signal: controller.signal,
        });
        if (!response.ok) {
          throw new Error(`Request failed (${response.status})`);
        }
        const payload = await response.json();
        if (!active) {
          return;
        }
        setTreeEntries({ '': payload.entries || [] });
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
  }, [browseMode, sliceId, commitHash, sliceHash]);

  const fetchEntries = async (path) => {
    if (!canLoad) {
      return;
    }

    setIsLoading(true);
    setError('');

    try {
      const response = await fetch(buildEntriesUrl(path));
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

  const handleEntryClick = async (entry) => {
    const entryKind = normalizeEntryType(entry.type);
    if (entryKind === 'directory') {
      // Set selected state immediately before async operations
      setSelectedFile(null);
      setSelectedDirectory(entry.path);
      setFileContent('');
      setShowHistory(false);
      setFileHistory([]);
      setHistoryError('');
      setError('');
      // Expand/collapse directory (fire and forget)
      toggleDirectory(entry);
      return;
    }

    setSelectedFile(entry.path);
    setSelectedDirectory(null);
    setFileContent('');
    setIsLoading(true);
    setError('');
    setShowHistory(false);
    setFileHistory([]);
    setHistoryError('');

    try {
      const response = await fetch(buildFileUrl(entry.path));
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
                style={{ paddingLeft: `${depth * 18 + 12}px` }}
                onClick={() => handleEntryClick(entry)}
                data-testid={`tree-entry-${entryKind}-${entry.name}`}
              >
                <span className="tree-caret">{entryKind === 'directory' ? (isExpanded ? '▾' : '▸') : '•'}</span>
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
      <div className="repo-header">
        <div>
          <p className="eyebrow">Monorepo navigator</p>
          <h2>Browse the fetched code</h2>
          <p>
            The file service streams content directly from slice storage. Expand folders to explore the tree and open a file to
            preview it.
          </p>
        </div>
        <div className="repo-controls">
          <label>
            Browse Mode
            <select
              data-testid="browse-mode"
              value={browseMode}
              onChange={(event) => setBrowseMode(event.target.value)}
            >
              <option value="root">Root Repository</option>
              <option value="slice">Specific Slice</option>
            </select>
          </label>

          {browseMode === 'root' ? (
            <label>
              Commit Hash (optional)
              <input
                data-testid="commit-hash"
                value={commitHash}
                onChange={(event) => setCommitHash(event.target.value)}
                placeholder="Leave empty for HEAD"
              />
            </label>
          ) : (
            <>
              <label>
                Slice ID
                <input
                  data-testid="slice-id"
                  value={sliceId}
                  onChange={(event) => setSliceId(event.target.value)}
                  placeholder="my_slice"
                />
              </label>
              <label>
                Slice Hash (optional)
                <input
                  data-testid="slice-hash"
                  value={sliceHash}
                  onChange={(event) => setSliceHash(event.target.value)}
                  placeholder="Leave empty for HEAD"
                />
              </label>
            </>
          )}
        </div>
      </div>

      <div className={`repo-layout ${sidebarOpen ? '' : 'sidebar-collapsed'}`}>
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
            {!canLoad && browseMode === 'slice' && (
              <div className="panel-empty">Enter a Slice ID to browse files.</div>
            )}
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
                <h3>{selectedPath ? selectedPath : 'Select a file'}</h3>
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
              {selectedPath && (
                <button
                  type="button"
                  className={`history-toggle ${showHistory ? 'active' : ''}`}
                  onClick={toggleHistory}
                  data-testid="history-toggle"
                  title={showHistory ? (selectedFile ? 'Show file content' : 'Hide history') : 'Show commit history'}
                >
                  {showHistory ? (selectedFile ? '📄 Content' : '📄 Hide History') : '📜 History'}
                </button>
              )}
            </div>
          </div>
          <div className="code-content">
            {!selectedPath && <div className="panel-empty">Choose a file from the tree to preview its contents.</div>}
            {selectedDirectory && !showHistory && (
              <div className="panel-empty">Directory selected. Click History to view change history for this folder.</div>
            )}
            {selectedFile && !showHistory && (
              <pre className="file-preview">
                <code dangerouslySetInnerHTML={{ __html: highlightedContent || 'No content available yet.' }} />
              </pre>
            )}
            {selectedPath && showHistory && (
              <div className="history-panel" data-testid="history-panel">
                {historyLoading && <div className="history-loading">Loading history...</div>}
                {historyError && <div className="panel-error">{historyError}</div>}
                {!historyLoading && !historyError && fileHistory.length === 0 && (
                  <div className="panel-empty">No history available for this {selectedDirectory ? 'directory' : 'file'}.</div>
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
    </section>
  );
}

function CommitDiffPage({ commitHash, onBack }) {
  const [diffData, setDiffData] = useState(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!commitHash) return;
    let active = true;
    const controller = new AbortController();

    const loadDiff = async () => {
      setIsLoading(true);
      setError('');
      try {
        const response = await fetch(`${apiBaseUrl}/v1/commits/${encodeURIComponent(commitHash)}/changes`, {
          signal: controller.signal,
        });
        if (!response.ok) throw new Error(`Request failed (${response.status})`);
        const payload = await response.json();
        if (active) setDiffData(payload);
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

  return (
    <section className="commit-diff-page" data-testid="commit-diff-page">
      <div className="diff-header">
        <button type="button" className="ghost diff-back-btn" onClick={onBack} data-testid="diff-back-btn">
          Back to browser
        </button>
        <div>
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
      </div>

      <div className="diff-content">
        {isLoading && <div className="diff-loading">Loading commit changes...</div>}
        {error && <div className="panel-error">{error}</div>}
        {!isLoading && !error && diffData && (
          <ul className="diff-file-list" data-testid="diff-file-list">
            {(diffData.changes || []).map((change) => (
              <li key={change.id || change.path} className="diff-file-item" data-testid="diff-file-item">
                <div className="diff-file-header">
                  <span className={`change-type change-type-${normalizeChangeType(change.change_type)}`}>
                    {formatChangeType(change.change_type)}
                  </span>
                  <span className="diff-file-path" data-testid="diff-file-path">{change.path}</span>
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
              </li>
            ))}
          </ul>
        )}
        {!isLoading && !error && diffData && (diffData.changes || []).length === 0 && (
          <div className="panel-empty">No changes found in this commit.</div>
        )}
      </div>
    </section>
  );
}

function normalizeChangeType(value) {
  if (value === 1 || value === 'CHANGE_TYPE_ADD' || value === 'ADD') {
    return 'add';
  }
  if (value === 2 || value === 'CHANGE_TYPE_MODIFY' || value === 'MODIFY') {
    return 'modify';
  }
  if (value === 3 || value === 'CHANGE_TYPE_DELETE' || value === 'DELETE') {
    return 'delete';
  }
  if (value === 4 || value === 'CHANGE_TYPE_RENAME' || value === 'RENAME') {
    return 'rename';
  }
  return 'unknown';
}

function formatChangeType(value) {
  const type = normalizeChangeType(value);
  return type.charAt(0).toUpperCase() + type.slice(1);
}

function formatTimestamp(timestamp) {
  if (!timestamp) {
    return 'Unknown date';
  }
  const date = new Date(timestamp * 1000);
  return date.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function normalizeEntryType(value) {
  if (value === 2 || value === 'ENTRY_TYPE_DIRECTORY' || value === 'DIRECTORY') {
    return 'directory';
  }
  if (value === 1 || value === 'ENTRY_TYPE_FILE' || value === 'FILE') {
    return 'file';
  }
  return 'file';
}

function decodeBase64(value) {
  if (!value) {
    return '';
  }
  try {
    return decodeURIComponent(escape(window.atob(value)));
  } catch (error) {
    try {
      return window.atob(value);
    } catch (innerError) {
      return value;
    }
  }
}

function formatBytes(value) {
  // Convert to number to handle string values from API
  const numValue = typeof value === 'number' ? value : parseFloat(value);

  if (!numValue || isNaN(numValue)) {
    return '0 B';
  }

  const units = ['B', 'KB', 'MB', 'GB'];
  let size = numValue;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  return `${size.toFixed(size >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

function highlightCode(source) {
  if (!source) {
    return '';
  }

  const tokenRegex =
    /\/\/.*|\/\*[\s\S]*?\*\/|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\b(?:const|let|var|function|return|if|else|for|while|class|import|from|export|async|await|try|catch|throw|new|switch|case|break|default|true|false|null)\b|\b\d+(?:\.\d+)?\b/g;
  let lastIndex = 0;
  let result = '';

  for (const match of source.matchAll(tokenRegex)) {
    const matchIndex = match.index ?? 0;
    result += escapeHtml(source.slice(lastIndex, matchIndex));
    const token = match[0];
    const className = token.startsWith('//') || token.startsWith('/*')
      ? 'token-comment'
      : token.startsWith('"') || token.startsWith("'") || token.startsWith('`')
      ? 'token-string'
      : /^\d/.test(token)
      ? 'token-number'
      : 'token-keyword';
    result += `<span class="${className}">${escapeHtml(token)}</span>`;
    lastIndex = matchIndex + token.length;
  }

  result += escapeHtml(source.slice(lastIndex));
  return result;
}

function escapeHtml(value) {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export default App;
