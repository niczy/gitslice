// ---------------------------------------------------------------------------
// Overview / Landing Page Component
// ---------------------------------------------------------------------------
import { Button } from './ui/button.jsx';
import { Badge } from './ui/badge.jsx';

const PROOF_POINTS = [
  {
    label: 'Direct remote edits',
    value: 'Read and write versioned files without cloning first.',
  },
  {
    label: 'Home slice by default',
    value: 'Every command works against your cloud home path and commit history.',
  },
  {
    label: 'Local checkout when needed',
    value: 'Drop into a git-compatible worktree only for the edits that need it.',
  },
];

const WORK_MODES = [
  {
    step: '01',
    title: 'Stay in the cloud with `gs fs`',
    copy: 'Use the filesystem directly when you want the shortest path from idea to versioned change.',
    highlights: [
      'Absolute paths under your home slice',
      'Read, write, list, snapshot, diff, and restore from the CLI',
      'Versioned history without opening a local checkout',
    ],
    command: `gs login
gs fs mkdir /$USER/app
gs fs write /$USER/app/README.md --text "hello from gitslice"
gs fs cat /$USER/app/README.md
gs fs snapshot -m "first remote edit"`,
  },
  {
    step: '02',
    title: 'Check out the slice and merge with changesets',
    copy: 'Switch to a local worktree when your editor, tooling, or git flow is the better interface.',
    highlights: [
      'Check out the exact slice you want to edit',
      'Use your normal editor and local git inspection tools',
      'Merge back through explicit changeset review',
    ],
    command: `mkdir checkout-demo && cd checkout-demo
gs slice checkout <slice-id>
git status
$EDITOR README.md
gs changeset create --message "update readme" --files README.md
gs changeset merge <changeset-id>`,
  },
];

const OUTCOMES = [
  {
    title: 'One filesystem surface',
    detail: 'Remote commands and local checkout both operate on the same versioned slice model.',
  },
  {
    title: 'Shorter happy path',
    detail: 'Most edits can happen with `gs fs` alone, so users do not have to clone before they can start.',
  },
  {
    title: 'Review when it matters',
    detail: 'When a change needs local tooling or explicit merge review, changesets are already in the flow.',
  },
];

export default function OverviewPage({ onBrowseRepo }) {
  return (
    <>
      <section className="hero hero--landing-redesign landing-hero bg-gradient-to-br from-secondary/60 via-background to-card">
        <div className="hero-content landing-hero-copy py-8">
          <Badge variant="secondary" className="eyebrow border border-border/60">Cloud versioned file system</Badge>
          <h1>Edit remote files directly from the CLI.</h1>
          <p className="lede">
            Use <code>gs fs</code> to read and write versioned files in the cloud. When you need a local tree, check out the slice, edit locally, and merge back with changesets.
          </p>
          <div className="cta-row flex flex-wrap gap-3">
            <Button type="button" onClick={onBrowseRepo}>
              Open repo browser
            </Button>
            <Button asChild variant="outline">
              <a className="ghost" href="https://github.com/agenttools-dev/gitslice" target="_blank" rel="noreferrer">
                Install the CLI
              </a>
            </Button>
          </div>
          <dl className="landing-proof-strip">
            {PROOF_POINTS.map((point) => (
              <div key={point.label} className="landing-proof-item">
                <dt>{point.label}</dt>
                <dd>{point.value}</dd>
              </div>
            ))}
          </dl>
        </div>

        <div className="landing-hero-shell">
          <div className="landing-command-ribbon" aria-hidden="true">
            <span>gs fs</span>
            <span>versioned</span>
            <span>home slice</span>
          </div>
          <div className="hero-card hero-card--api landing-terminal border-border/70 bg-card/95">
            <div className="landing-terminal-header">
              <Badge variant="outline" className="w-fit">Quickstart</Badge>
              <p>Write a remote file in under a minute</p>
            </div>
            <pre className="code-block">
              <code>{`gs login
gs fs mkdir /$USER/app
gs fs write /$USER/app/hello.txt --text "hello from gitslice"
gs fs cat /$USER/app/hello.txt
gs fs snapshot -m "first remote edit"`}</code>
            </pre>
          </div>
          <div className="landing-terminal-note">
            <span>Need a full local tree?</span>
            <code>gs slice checkout &lt;slice-id&gt;</code>
          </div>
        </div>
      </section>

      <section className="section landing-comparison space-y-6">
        <div className="section-header space-y-2">
          <Badge variant="secondary" className="eyebrow">Choose your surface</Badge>
          <h2>Stay remote until local work is actually faster.</h2>
          <p>Both paths operate on the same slice history. The difference is just how much local tooling you need for the task in front of you.</p>
        </div>
        <div className="landing-mode-grid">
          {WORK_MODES.map((item) => (
            <article key={item.title} className="landing-mode">
              <div className="landing-mode-top">
                <div className="landing-mode-step">{item.step}</div>
                <div className="landing-mode-copy">
                  <h3>{item.title}</h3>
                  <p>{item.copy}</p>
                </div>
              </div>
              <ul className="landing-mode-list">
                {item.highlights.map((highlight) => (
                  <li key={highlight}>{highlight}</li>
                ))}
              </ul>
              <div className="landing-mode-code">
                <pre className="code-block">
                  <code>{item.command}</code>
                </pre>
              </div>
            </article>
          ))}
        </div>
      </section>

      <section className="section landing-outcome">
        <div className="landing-outcome-grid">
          <div className="landing-outcome-copy">
            <Badge variant="secondary" className="eyebrow">Why it converts</Badge>
            <h2>Lead with simple filesystem commands. Keep review and merge in reserve.</h2>
            <p>
              The product entry point is now obvious: start with <code>gs fs</code>. When a change deserves a full local tree or explicit review, move into slice checkout and changesets without leaving the same versioned system.
            </p>
          </div>
          <div className="landing-outcome-rail">
            {OUTCOMES.map((item) => (
              <div key={item.title} className="landing-outcome-item">
                <h3>{item.title}</h3>
                <p>{item.detail}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      <section className="landing-cta-band">
        <div>
          <p className="landing-cta-kicker">Start with the shortest path.</p>
          <h2>Use `gs fs` first. Check out later.</h2>
        </div>
        <Button type="button" onClick={onBrowseRepo}>
          Open repo browser
        </Button>
      </section>
    </>
  );
}
