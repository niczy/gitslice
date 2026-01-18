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

const apiBaseUrl = import.meta.env.VITE_FILE_API_BASE_URL || 'http://localhost:8080';

const pageOptions = [
  { id: 'overview', label: 'Overview' },
  { id: 'browser', label: 'Repo Browser' },
];

function App() {
  const [activePage, setActivePage] = useState('overview');

  return (
    <div className="page">
      <header className="hero">
        <div className="eyebrow">Introducing Git Slice</div>
        <h1>Slice-based workflows for shipping more confidently.</h1>
        <p className="lede">
          Git Slice lets teams carve out focused slices of work, run them end-to-end, and merge back with clarity. No more
          sprawling branches—just fast, predictable delivery.
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

      {activePage === 'overview' ? <OverviewPage /> : <RepoBrowser />}

      <footer className="footer">
        <p>Git Slice • Slice smart. Ship faster.</p>
      </footer>
    </div>
  );
}

function OverviewPage() {
  return (
    <>
      <section id="overview" className="section card">
        <div className="section-header">
          <p className="eyebrow">Slice-first development</p>
          <h2>Run isolated slices from idea to production</h2>
          <p>
            Start by defining a slice around a task. Git Slice provisions the context, fetches dependencies, and wires up
            tooling so you can develop, test, and preview changes without disturbing the rest of the repo. When you are ready,
            merge the slice with full traceability.
          </p>
        </div>
        <div className="steps">
          <div className="step">
            <div className="step-number">1</div>
            <div>
              <h3>Carve out the slice</h3>
              <p>Pin the exact files and services you need. Spin up environments that mirror production with minimal setup.</p>
            </div>
          </div>
          <div className="step">
            <div className="step-number">2</div>
            <div>
              <h3>Iterate quickly</h3>
              <p>Use the CLI to run tests, preview changes, and share the slice URL so reviewers can validate updates in minutes.</p>
            </div>
          </div>
          <div className="step">
            <div className="step-number">3</div>
            <div>
              <h3>Merge with confidence</h3>
              <p>Every slice comes with reproducible logs, checks, and diffs so merging back is predictable and low-risk.</p>
            </div>
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

function RepoBrowser() {
  const [sliceId, setSliceId] = useState('root_slice');
  const [currentPath, setCurrentPath] = useState('');
  const [entries, setEntries] = useState([]);
  const [selectedFile, setSelectedFile] = useState(null);
  const [fileContent, setFileContent] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');

  const breadcrumbs = useMemo(() => {
    if (!currentPath) {
      return [{ name: 'root', path: '' }];
    }
    const parts = currentPath.split('/');
    return [
      { name: 'root', path: '' },
      ...parts.map((part, index) => ({
        name: part,
        path: parts.slice(0, index + 1).join('/'),
      })),
    ];
  }, [currentPath]);

  useEffect(() => {
    let active = true;
    const controller = new AbortController();

    const loadEntries = async () => {
      setIsLoading(true);
      setError('');
      setSelectedFile(null);
      setFileContent('');

      try {
        const params = new URLSearchParams();
        if (currentPath) {
          params.set('path', currentPath);
        }
        const response = await fetch(`${apiBaseUrl}/v1/slices/${sliceId}/entries?${params.toString()}`, {
          signal: controller.signal,
        });
        if (!response.ok) {
          throw new Error(`Request failed (${response.status})`);
        }
        const payload = await response.json();
        if (!active) {
          return;
        }
        setEntries(payload.entries || []);
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

    loadEntries();

    return () => {
      active = false;
      controller.abort();
    };
  }, [currentPath, sliceId]);

  const handleEntryClick = async (entry) => {
    const entryKind = normalizeEntryType(entry.type);
    if (entryKind === 'directory') {
      setCurrentPath(entry.path);
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

  const handleBreadcrumbClick = (path) => {
    setCurrentPath(path);
  };

  return (
    <section className="section repo-browser card">
      <div className="section-header">
        <p className="eyebrow">Monorepo navigator</p>
        <h2>Browse the fetched code</h2>
        <p>
          The file service streams content directly from slice storage. Select a directory to explore, or open a file to view
          its contents.
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
            <h3>Entries</h3>
            {isLoading && <span className="status">Loading…</span>}
          </div>
          {error && <div className="panel-error">{error}</div>}
          {!isLoading && !error && entries.length === 0 && <div className="panel-empty">No entries found.</div>}
          <ul className="entry-list">
            {entries.map((entry) => {
              const entryKind = normalizeEntryType(entry.type);
              return (
                <li key={entry.path}>
                  <button type="button" className="entry" onClick={() => handleEntryClick(entry)}>
                    <span className={`entry-icon ${entryKind}`}>{entryKind === 'directory' ? '📁' : '📄'}</span>
                    <span className="entry-name">{entry.name}</span>
                    {entryKind === 'file' && <span className="entry-meta">{formatBytes(entry.size)}</span>}
                  </button>
                </li>
              );
            })}
          </ul>
        </div>

        <div className="browser-panel">
          <div className="panel-header">
            <h3>{selectedFile ? selectedFile : 'Select a file'}</h3>
            {selectedFile && <span className="status">{formatBytes(fileContent.length)}</span>}
          </div>
          {!selectedFile && <div className="panel-empty">Choose a file from the list to preview its contents.</div>}
          {selectedFile && (
            <pre className="file-preview">{fileContent || 'No content available yet.'}</pre>
          )}
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
