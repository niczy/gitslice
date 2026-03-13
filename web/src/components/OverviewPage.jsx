import { Button } from './ui/button.jsx';
import { Badge } from './ui/badge.jsx';

const SIGNALS = [
  { label: 'Primary surface', value: '`gs fs`' },
  { label: 'History model', value: 'snapshots + changesets' },
  { label: 'Checkout model', value: 'git-compatible slice checkout' },
];

const REMOTE_COMMANDS = `gs login
gs fs mkdir /$USER/app
gs fs write /$USER/app/README.md --text "hello from gitslice"
gs fs cat /$USER/app/README.md
gs fs snapshot -m "baseline"
gs fs diff <snapshot-id>`;

const LOCAL_COMMANDS = `mkdir work && cd work
gs slice checkout home.$USER
git status
$EDITOR README.md
gs changeset create --message "update readme" --files README.md
gs changeset merge <changeset-id>`;

const API_ROUTES = [
  'fs write /path',
  'fs read /path',
  'fs snapshot',
  'fs diff <snapshot>',
  'slice checkout',
  'changeset merge',
];

const BENEFITS = [
  {
    title: 'Direct cloud editing',
    detail: 'Read, write, move, batch, snapshot, diff, and restore without cloning first.',
  },
  {
    title: 'Versioned from the first command',
    detail: 'Every remote mutation lands in slice history with commit metadata and patchable changes.',
  },
  {
    title: 'Local when you need it',
    detail: 'Check out the same slice into a worktree when editor tooling or review flow matters more.',
  },
];

export default function OverviewPage({ onBrowseRepo }) {
  return (
    <>
      <section className="hero landing-cloud">
        <div className="hero-content landing-cloud-copy">
          <Badge variant="outline" className="eyebrow landing-cloud-badge">
            Cloud versioned file system
          </Badge>
          <div className="landing-cloud-headline">
            <h1>Cloud files first. Local checkout only when you need it.</h1>
            <p className="lede">
              Use <code>gs fs</code> to edit versioned files directly in the cloud. When the job needs a local tree,
              check out the slice, work in your normal editor, and merge back through changesets.
            </p>
          </div>
          <div className="cta-row landing-cloud-actions">
            <Button type="button" onClick={onBrowseRepo}>
              Open repo browser
            </Button>
            <Button asChild variant="outline">
              <a className="ghost" href="https://github.com/agenttools-dev/gitslice" target="_blank" rel="noreferrer">
                Install the CLI
              </a>
            </Button>
          </div>
          <dl className="landing-signal-grid">
            {SIGNALS.map((signal) => (
              <div key={signal.label} className="landing-signal">
                <dt>{signal.label}</dt>
                <dd>{signal.value}</dd>
              </div>
            ))}
          </dl>
        </div>

        <div className="landing-cloud-shell">
          <div className="landing-command-panel">
            <div className="landing-panel-header">
              <Badge variant="secondary" className="landing-panel-badge">Quickstart</Badge>
              <span className="landing-panel-caption">Remote workflow</span>
            </div>
            <pre className="code-block landing-code-block">
              <code>{REMOTE_COMMANDS}</code>
            </pre>
          </div>

          <div className="landing-meta-grid">
            <div className="landing-meta-card">
              <p className="landing-meta-label">Why teams start here</p>
              <ul className="landing-inline-list">
                <li>No local checkout required for the first edit</li>
                <li>Home slice by default under `/$USER`</li>
                <li>Snapshots, diffs, and restores from the same CLI surface</li>
              </ul>
            </div>

            <div className="landing-meta-card landing-meta-card--routes">
              <p className="landing-meta-label">Core command surface</p>
              <div className="landing-route-list">
                {API_ROUTES.map((route) => (
                  <code key={route}>{route}</code>
                ))}
              </div>
            </div>
          </div>
        </div>
      </section>

      <section className="section landing-compare-shell">
        <div className="landing-section-intro">
          <Badge variant="outline" className="eyebrow landing-cloud-badge">
            Two paths, one history
          </Badge>
          <h2>Start with `gs fs`. Drop into a checkout only when local tools are the better interface.</h2>
        </div>

        <div className="landing-track-grid">
          <article className="landing-track">
            <div className="landing-track-header">
              <span className="landing-track-index">01</span>
              <div>
                <h3>Stay remote</h3>
                <p>The shortest route from intent to versioned change.</p>
              </div>
            </div>
            <ul className="landing-track-list">
              <li>Absolute paths in your home slice</li>
              <li>Single-command file edits and batch operations</li>
              <li>Snapshots and diffs without leaving the CLI</li>
            </ul>
            <pre className="code-block landing-code-block">
              <code>{REMOTE_COMMANDS}</code>
            </pre>
          </article>

          <article className="landing-track">
            <div className="landing-track-header">
              <span className="landing-track-index">02</span>
              <div>
                <h3>Check out locally</h3>
                <p>Use a worktree when editor tooling, tests, or review flow matter more.</p>
              </div>
            </div>
            <ul className="landing-track-list">
              <li>Checkout the same slice history into a local directory</li>
              <li>Use git status, your editor, and local tools as usual</li>
              <li>Merge back through explicit changesets</li>
            </ul>
            <pre className="code-block landing-code-block">
              <code>{LOCAL_COMMANDS}</code>
            </pre>
          </article>
        </div>
      </section>

      <section className="section landing-benefit-shell">
        <div className="landing-benefit-grid">
          <div className="landing-benefit-copy">
            <Badge variant="outline" className="eyebrow landing-cloud-badge">
              What users get
            </Badge>
            <h2>A docs-first surface for file operations, with version control built in.</h2>
            <p>
              The landing page should answer one question fast: what can I do right now? The answer is direct cloud
              file operations with <code>gs fs</code>, backed by slice history and a checkout path for deeper local
              work.
            </p>
          </div>
          <div className="landing-benefit-list">
            {BENEFITS.map((benefit) => (
              <div key={benefit.title} className="landing-benefit-item">
                <h3>{benefit.title}</h3>
                <p>{benefit.detail}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      <section className="landing-cta-band landing-cta-band--dark">
        <div>
          <p className="landing-cta-kicker">Shortest path first</p>
          <h2>Read and edit files in the cloud. Check out later.</h2>
        </div>
        <Button type="button" onClick={onBrowseRepo}>
          Open repo browser
        </Button>
      </section>
    </>
  );
}
