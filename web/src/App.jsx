import { useEffect, useMemo, useState } from 'react';
import './styles.css';

const features = [
  {
    title: 'Bounded Context',
    description:
      'Agents work on defined slices, not entire repos. Checkout only what you need—faster operations, reduced cognitive load.',
  },
  {
    title: 'Parallel Operations',
    description:
      'Thousands of agents can work simultaneously on different slices. Conflicts detected proactively at slice boundaries, not after merge.',
  },
  {
    title: 'Fast Feedback',
    description:
      'CI runs only on slice changes. 10x faster iteration cycles mean agents can test, retry, and converge on solutions quickly.',
  },
  {
    title: 'Clear Ownership',
    description:
      'Slices define ownership boundaries. Map services to slices, assign agents to specific domains, reduce coordination overhead.',
  },
];

const apiBaseUrl = import.meta.env.VITE_FILE_API_BASE_URL || '';

const pageOptions = [
  { id: 'overview', label: 'Overview' },
  { id: 'browser', label: 'Repo Browser' },
  { id: 'slices', label: 'Slices' },
];

function App() {
  const [activePage, setActivePage] = useState('overview');

  return (
    <div className="page">
      <header className="hero">
        <div className="eyebrow">Research Prototype</div>
        <h1>Version control designed for AI coding agents at scale.</h1>
        <p className="lede">
          Git Slice reimagines version control for massive monorepos with thousands of autonomous agents. Bounded contexts,
          parallel operations, and proactive conflict detection—built for the next generation of software development.
        </p>
        <nav className="page-tabs">
          {pageOptions.map((option) => (
            <button
              key={option.id}
              className={activePage === option.id ? 'tab active' : 'tab'}
              type="button"
              onClick={() => setActivePage(option.id)}
            >
              {option.label}
            </button>
          ))}
        </nav>
      </header>

      {activePage === 'overview' ? <OverviewPage /> : activePage === 'browser' ? <RepoBrowser /> : <SlicesPage />}

      <footer className="footer">
        <p>Git Slice • Research prototype for AI-scale version control</p>
      </footer>
    </div>
  );
}

function OverviewPage() {
  return (
    <>
      <section id="overview" className="section card">
        <div className="section-header">
          <p className="eyebrow">Why slices for coding agents?</p>
          <h2>Scale beyond Git's limits</h2>
          <p>
            Traditional Git workflows struggle when thousands of agents work on massive monorepos. Gitslice introduces slices—defined
            subsets of code that agents can own, modify, and merge independently. Designed for billions of files and millions of
            commits per day.
          </p>
        </div>
        <div className="steps">
          <div className="step">
            <div className="step-number">1</div>
            <div>
              <h3>Define slice boundaries</h3>
              <p>Map your monorepo to logical slices (services, packages, modules). Agents check out only what they need—faster than cloning entire repos.</p>
            </div>
          </div>
          <div className="step">
            <div className="step-number">2</div>
            <div>
              <h3>Parallel agent workflows</h3>
              <p>Fleet of agents work simultaneously. Conflicts detected at slice merge time, not globally. Proactive detection means faster retries.</p>
            </div>
          </div>
          <div className="step">
            <div className="step-number">3</div>
            <div>
              <h3>Batch merge to global</h3>
              <p>Changes merge to global state in batches, avoiding per-commit bottlenecks. Built for high-volume autonomous operations.</p>
            </div>
          </div>
        </div>
      </section>

      <section id="features" className="section features">
        <div className="section-header">
          <p className="eyebrow">Built for autonomous agents</p>
          <h2>Key advantages over GitHub</h2>
          <p>Optimized for massive-scale parallel development with AI coding agents.</p>
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

      <section id="comparison" className="section card">
        <div className="section-header">
          <p className="eyebrow">When to use gitslice</p>
          <h2>Gitslice vs GitHub: Use case fit</h2>
          <p>Designed for specific scale challenges. Evaluate if your use case justifies the complexity.</p>
        </div>
        <div className="comparison-grid">
          <div className="comparison-card good">
            <h3>✅ Ideal for Gitslice</h3>
            <ul>
              <li><strong>Massive monorepos</strong> (10M+ files, thousands of services)</li>
              <li><strong>Well-defined boundaries</strong> (microservices, clear ownership)</li>
              <li><strong>High-volume autonomous changes</strong> (fleet of coding agents)</li>
              <li><strong>Parallel, independent operations</strong> (minimal cross-cutting changes)</li>
              <li><strong>Scale challenges</strong> (Git checkout/CI too slow)</li>
            </ul>
          </div>
          <div className="comparison-card warning">
            <h3>⚠️ Stick with GitHub</h3>
            <ul>
              <li><strong>Small-medium projects</strong> (&lt;1M LOC, traditional team sizes)</li>
              <li><strong>Cross-cutting agent tasks</strong> ("upgrade all packages", "fix linter errors everywhere")</li>
              <li><strong>Integration-heavy workflows</strong> (need GitHub Actions, Apps, webhooks)</li>
              <li><strong>Production requirements</strong> (auth, security, reliability needed now)</li>
              <li><strong>Established ecosystem</strong> (mature tooling, community support)</li>
            </ul>
          </div>
        </div>
      </section>

      <section className="section cta card">
        <div>
          <p className="eyebrow">Current status: Prototype</p>
          <h2>In-memory implementation, proving the model</h2>
          <p>
            This is a research prototype demonstrating slice-based version control. Production deployment would require Redis + S3 backend,
            authentication, durability, and ecosystem integrations. Not yet ready for production use.
          </p>
        </div>
        <a className="primary" href="https://github.com/niczy/gitslice" target="_blank" rel="noopener noreferrer">
          View on GitHub
        </a>
      </section>
    </>
  );
}

function RepoBrowser() {
  const [sliceId, setSliceId] = useState('root_slice');
  const [treeEntries, setTreeEntries] = useState({});
  const [expandedPaths, setExpandedPaths] = useState(['']);
  const [selectedFile, setSelectedFile] = useState(null);
  const [fileContent, setFileContent] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');

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

  useEffect(() => {
    setTreeEntries({});
    setExpandedPaths(['']);
    setSelectedFile(null);
    setFileContent('');
  }, [sliceId]);

  useEffect(() => {
    let active = true;
    const controller = new AbortController();

    const loadRoot = async () => {
      setIsLoading(true);
      setError('');

      try {
        const response = await fetch(`${apiBaseUrl}/v1/slices/${sliceId}/entries`, {
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
  }, [sliceId]);

  const fetchEntries = async (path) => {
    setIsLoading(true);
    setError('');

    try {
      const params = new URLSearchParams();
      if (path) {
        params.set('path', path);
      }
      const response = await fetch(`${apiBaseUrl}/v1/slices/${sliceId}/entries?${params.toString()}`);
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
      await toggleDirectory(entry);
      return;
    }

    setSelectedFile(entry.path);
    setFileContent('');
    setIsLoading(true);
    setError('');

    try {
      const params = new URLSearchParams({ path: entry.path });
      const response = await fetch(`${apiBaseUrl}/v1/slices/${sliceId}/files?${params.toString()}`);
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
    <section className="section repo-browser card">
      <div className="section-header">
        <p className="eyebrow">Monorepo navigator</p>
        <h2>Browse the fetched code</h2>
        <p>
          The file service streams content directly from slice storage. Expand folders to explore the tree and open a file to
          preview it.
        </p>
      </div>

      <div className="browser-toolbar">
        <label>
          Slice ID
          <input value={sliceId} onChange={(event) => setSliceId(event.target.value)} placeholder="root_slice" />
        </label>
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

      <div className="browser-grid">
        <div className="browser-panel">
          <div className="panel-header">
            <h3>File tree</h3>
            {isLoading && <span className="status">Loading…</span>}
          </div>
          {error && <div className="panel-error">{error}</div>}
          {!isLoading && !error && (treeEntries[''] || []).length === 0 && (
            <div className="panel-empty">No entries found.</div>
          )}
          {renderTree('')}
        </div>

        <div className="browser-panel">
          <div className="panel-header">
            <h3>{selectedFile ? selectedFile : 'Select a file'}</h3>
            {selectedFile && <span className="status">{formatBytes(fileContent.length)}</span>}
          </div>
          {!selectedFile && <div className="panel-empty">Choose a file from the tree to preview its contents.</div>}
          {selectedFile && <pre className="file-preview">{fileContent || 'No content available yet.'}</pre>}
        </div>
      </div>
    </section>
  );
}

function SlicesPage() {
  const [slices, setSlices] = useState([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');
  const [createStatus, setCreateStatus] = useState({ type: '', message: '' });
  const initialFormState = {
    sliceId: '',
    name: '',
    description: '',
    owners: '',
    files: '',
    createdBy: '',
  };
  const [formState, setFormState] = useState(initialFormState);

  const loadSlices = async () => {
    setIsLoading(true);
    setError('');

    try {
      const response = await fetch(`${apiBaseUrl}/v1/slices?limit=100`);
      if (!response.ok) {
        throw new Error(`Request failed (${response.status})`);
      }
      const payload = await response.json();
      setSlices(payload.slices || []);
    } catch (err) {
      setError('Unable to load slices. Ensure the slice service is running.');
    } finally {
      setIsLoading(false);
    }
  };

  useEffect(() => {
    loadSlices();
  }, []);

  const handleFormChange = (event) => {
    const { name, value } = event.target;
    setFormState((prev) => ({ ...prev, [name]: value }));
  };

  const handleCreateSlice = async (event) => {
    event.preventDefault();
    setCreateStatus({ type: '', message: '' });

    const sliceId = formState.sliceId.trim();
    if (!sliceId) {
      setCreateStatus({ type: 'error', message: 'Slice ID is required.' });
      return;
    }

    const payload = {
      slice_id: sliceId,
      name: formState.name.trim(),
      description: formState.description.trim(),
      owners: formState.owners
        .split(',')
        .map((owner) => owner.trim())
        .filter(Boolean),
      files: formState.files
        .split(',')
        .map((file) => file.trim())
        .filter(Boolean),
      created_by: formState.createdBy.trim(),
    };

    try {
      const response = await fetch(`${apiBaseUrl}/v1/slices`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });

      if (!response.ok) {
        const message = response.status === 409 ? 'Slice already exists.' : 'Unable to create slice.';
        throw new Error(message);
      }

      setCreateStatus({ type: 'success', message: 'Slice created successfully.' });
      setFormState(initialFormState);
      await loadSlices();
    } catch (err) {
      setCreateStatus({ type: 'error', message: err.message || 'Unable to create slice.' });
    }
  };

  return (
    <section className="section slices-page card">
      <div className="section-header">
        <p className="eyebrow">Slice catalog</p>
        <h2>Manage existing slices</h2>
        <p>Review all available slices and create new ones directly from the web console.</p>
      </div>

      <div className="slice-toolbar">
        <button type="button" className="ghost" onClick={loadSlices} disabled={isLoading}>
          Refresh list
        </button>
        {isLoading && <span className="status">Loading…</span>}
      </div>

      {error && <div className="panel-error">{error}</div>}

      <div className="slice-grid">
        <div className="slice-panel">
          <div className="panel-header">
            <h3>All slices</h3>
            <span className="status">{slices.length} total</span>
          </div>
          {slices.length === 0 && !isLoading && !error && <div className="panel-empty">No slices found yet.</div>}
          <ul className="slice-list">
            {slices.map((slice) => (
              <li key={slice.slice_id} className="slice-card">
                <div className="slice-card-header">
                  <div>
                    <h4>{slice.name || slice.slice_id}</h4>
                    <p className="slice-id">{slice.slice_id}</p>
                  </div>
                  <span className="slice-files">{slice.file_count} files</span>
                </div>
                {slice.description && <p className="slice-description">{slice.description}</p>}
                <div className="slice-meta">
                  <span>Owners: {slice.owners?.length ? slice.owners.join(', ') : 'Unassigned'}</span>
                  <span>Updated: {slice.updated_at ? new Date(slice.updated_at).toLocaleString() : '—'}</span>
                </div>
              </li>
            ))}
          </ul>
        </div>

        <div className="slice-panel">
          <div className="panel-header">
            <h3>Create a slice</h3>
          </div>
          <form className="slice-form" onSubmit={handleCreateSlice}>
            <label>
              Slice ID
              <input name="sliceId" value={formState.sliceId} onChange={handleFormChange} placeholder="feature_login" />
            </label>
            <label>
              Name
              <input name="name" value={formState.name} onChange={handleFormChange} placeholder="Login improvements" />
            </label>
            <label>
              Description
              <textarea
                name="description"
                value={formState.description}
                onChange={handleFormChange}
                placeholder="Short summary of the slice goal"
                rows={3}
              />
            </label>
            <label>
              Owners
              <input
                name="owners"
                value={formState.owners}
                onChange={handleFormChange}
                placeholder="alice, bob"
              />
            </label>
            <label>
              Files
              <input name="files" value={formState.files} onChange={handleFormChange} placeholder="src/app.js, api/auth.js" />
            </label>
            <label>
              Created by
              <input
                name="createdBy"
                value={formState.createdBy}
                onChange={handleFormChange}
                placeholder="alice"
              />
            </label>
            <div className="form-actions">
              <button type="submit" className="primary">
                Create slice
              </button>
            </div>
            {createStatus.message && (
              <div className={createStatus.type === 'error' ? 'panel-error' : 'panel-success'}>
                {createStatus.message}
              </div>
            )}
          </form>
        </div>
      </div>
    </section>
  );
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
  if (!value) {
    return '0 B';
  }
  const units = ['B', 'KB', 'MB', 'GB'];
  let size = value;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  return `${size.toFixed(size >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

export default App;
