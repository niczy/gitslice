import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';

const DOC_NAV = [
  { id: 'overview', label: 'Overview' },
  { id: 'mental-model', label: 'Mental model' },
  { id: 'quick-start', label: 'Quick start' },
  { id: 'command-map', label: 'Command map' },
  { id: 'cloud-fs', label: 'Cloud filesystem' },
  { id: 'repo-bindings', label: 'Repo bindings' },
  { id: 'custom-slices', label: 'Custom slices' },
  { id: 'changesets', label: 'Changesets' },
  { id: 'local-cache', label: 'Local cache' },
  { id: 'web-app', label: 'Web app' },
  { id: 'auth', label: 'Auth' },
  { id: 'faq', label: 'FAQ' },
];

const MODEL_CARDS = [
  {
    title: 'Published tree',
    copy: 'The shared repo state lives in the published tree. Git Slice stores that as the root slice and treats it as the default base for collaboration.',
  },
  {
    title: 'Home slice',
    copy: 'Each account gets a home slice. `gs fs` works there by default through absolute paths like `/$USER/project/README.md`.',
  },
  {
    title: 'Custom slice',
    copy: 'Create a focused slice when the job deserves a local checkout, editor workflow, tests, and a normal git-shaped working tree.',
  },
  {
    title: 'Changeset',
    copy: 'A changeset is the explicit publish and merge unit. It is the path back into the published tree for local slice work.',
  },
  {
    title: 'Blocks + manifests',
    copy: 'File transfer is content-addressed. Uploads and checkouts exchange manifests first and only transfer blocks your machine or the server is missing.',
  },
];

const COMMAND_MAP = [
  {
    task: 'Read or patch a remote file directly',
    command: 'gs fs write /$USER/app/NOTICE.txt --text "hotfix shipped remotely"\ngs fs cat /$USER/app/NOTICE.txt\ngs fs snapshot -m "patch notice"',
    note: 'Best for notes, tiny fixes, and direct remote edits.',
  },
  {
    task: 'Create a focused local worktree',
    command: 'gs slice list\ngs slice create ui-refresh apps/web\nmkdir ui-refresh && cd ui-refresh\ngs slice checkout <slice-id-or-slug>',
    note: 'Best for multi-file work, tests, refactors, and editor-heavy tasks.',
  },
  {
    task: 'Bind a GitHub repo into a home-slice directory',
    command: 'gs repo import https://github.com/org/repo.git /$USER/vendor/repo --push-enabled\ngs repo pull /$USER/vendor/repo\ngs repo push /$USER/vendor/repo --message "sync upstream fixes"',
    note: 'Best when you want one directory to stay connected to an upstream repo while still using normal `gs fs` edits.',
  },
  {
    task: 'Publish local work back to the shared tree',
    command: 'gs slice sync\ngs slice publish --message "refresh settings page" --files src/routes/settings.tsx\ngs changeset show',
    note: 'Use this when you are ready to review and publish local slice work.',
  },
];

const FAQS = [
  {
    question: 'When should I use `gs fs` instead of a checkout?',
    answer: 'Use `gs fs` when the change is smaller than the setup cost of a local worktree. If you need an editor session, test loop, or lots of files, use a custom slice checkout.',
  },
  {
    question: 'What does a slice represent?',
    answer: 'A slice is a versioned tree with its own history. Your home slice is your personal default surface. Custom slices are focused branches created for a task or folder.',
  },
  {
    question: 'Are large transfers deduplicated?',
    answer: 'Yes. Uploads and checkouts exchange manifests first and then transfer only missing blocks. Repeated uploads and repeated checkouts get cheaper as cache coverage grows.',
  },
  {
    question: 'Does `gs fs` bypass the merge model?',
    answer: 'No. Remote filesystem mutations create home-slice commits, and publication now goes through the same changeset merge model used by normal slice workflows.',
  },
];

export default function DocsPage({ onBrowseRepo }) {
  return (
    <div className="docs-page">
      <section className="docs-hero">
        <div className="docs-hero-copy">
          <Badge variant="secondary" className="eyebrow">Git Slice docs</Badge>
          <h1>One versioned filesystem, two work surfaces.</h1>
          <p className="lede">
            Use <code>gs fs</code> for direct cloud edits in your home slice. Use custom slices when you want a fast
            local checkout, full editor workflow, and explicit changeset merges back to the published tree.
          </p>
          <div className="cta-row flex flex-wrap gap-3">
            <Button type="button" onClick={onBrowseRepo}>
              Open repo browser
            </Button>
            <Button asChild variant="outline">
              <a href="#quick-start">Jump to quick start</a>
            </Button>
          </div>
        </div>
        <div className="docs-hero-card">
          <div className="docs-hero-card-head">
            <Badge variant="outline" className="w-fit">Install + login</Badge>
            <p>Start with the CLI</p>
          </div>
          <pre className="code-block">
            <code>{`go install github.com/niczy/gitslice/gs@latest
gs login`}</code>
          </pre>
        </div>
      </section>

      <div className="docs-layout">
        <aside className="docs-nav-shell">
          <div className="docs-nav-card">
            <p className="docs-nav-kicker">Navigate</p>
            <nav className="docs-nav" aria-label="Documentation navigation">
              {DOC_NAV.map((item) => (
                <a key={item.id} className="docs-nav-link" href={`#${item.id}`}>
                  {item.label}
                </a>
              ))}
            </nav>
          </div>
        </aside>

        <div className="docs-content">
          <section id="overview" className="docs-section">
            <Badge variant="secondary" className="eyebrow">Overview</Badge>
            <h2>What Git Slice gives you</h2>
            <p>
              Git Slice is a cloud versioned filesystem built for two real workflows. The first is direct remote work
              with <code>gs fs</code>. The second is focused local work through custom slices and fast checkouts. Both
              are slice-backed, both keep commit history, and both converge through the same publish model.
            </p>
            <ul className="docs-bullet-list">
              <li>Cloud reads, writes, snapshots, diffs, upload, download, and batch operations through <code>gs fs</code>.</li>
              <li>Repo bindings that import a GitHub repo into a home-slice path and optionally let you pull and push it later.</li>
              <li>Focused slice creation and fast <code>gs slice checkout</code> for editor-heavy work.</li>
              <li>Explicit publish and merge through <code>gs changeset create</code> and <code>gs changeset merge</code>.</li>
              <li>Repo browser, history, commit diffs, and slice navigation in the web app.</li>
            </ul>
          </section>

          <section id="mental-model" className="docs-section">
            <Badge variant="secondary" className="eyebrow">Mental model</Badge>
            <h2>Understand the system before choosing a workflow</h2>
            <div className="docs-card-grid">
              {MODEL_CARDS.map((card) => (
                <article key={card.title} className="docs-card">
                  <h3>{card.title}</h3>
                  <p>{card.copy}</p>
                </article>
              ))}
            </div>
          </section>

          <section id="quick-start" className="docs-section">
            <Badge variant="secondary" className="eyebrow">Quick start</Badge>
            <h2>Pick the lighter surface first</h2>
            <div className="docs-code-grid">
              <article className="docs-code-card">
                <div className="docs-code-card-head">
                  <h3>Cloud edit with `gs fs`</h3>
                  <p>Best when you want to change remote files immediately.</p>
                </div>
                <pre className="code-block">
                  <code>{`gs fs mkdir /$USER/notes
gs fs write /$USER/notes/todo.md --text "ship the patch"
gs fs cat /$USER/notes/todo.md
gs fs snapshot -m "notes update"`}</code>
                </pre>
              </article>

              <article className="docs-code-card">
                <div className="docs-code-card-head">
                  <h3>Custom slice checkout</h3>
                  <p>Best when the task needs a local editor, tests, or a normal git-shaped tree.</p>
                </div>
                <pre className="code-block">
                  <code>{`gs slice create ui-refresh apps/web
mkdir ui-refresh && cd ui-refresh
gs slice checkout <slice-id-or-slug>
git status`}</code>
                </pre>
              </article>
            </div>
          </section>

          <section id="command-map" className="docs-section">
            <Badge variant="secondary" className="eyebrow">Command map</Badge>
            <h2>The three command paths most users actually need</h2>
            <div className="docs-command-map">
              {COMMAND_MAP.map((entry) => (
                <article key={entry.task} className="docs-command-card">
                  <h3>{entry.task}</h3>
                  <pre className="code-block">
                    <code>{entry.command}</code>
                  </pre>
                  <p>{entry.note}</p>
                </article>
              ))}
            </div>
          </section>

          <section id="cloud-fs" className="docs-section">
            <Badge variant="secondary" className="eyebrow">Cloud filesystem</Badge>
            <h2>Use `gs fs` when remote is the fastest path</h2>
            <p>
              `gs fs` operates on your home slice using absolute paths. Every mutation produces versioned history, and
              large transfers are deduplicated through manifest-first block exchange instead of raw full-file reupload.
            </p>
            <div className="docs-table-shell">
              <table className="docs-table">
                <thead>
                  <tr>
                    <th>Operation</th>
                    <th>Command</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>Read a remote file</td>
                    <td><code>gs fs cat /$USER/app/README.md</code></td>
                  </tr>
                  <tr>
                    <td>Write a remote file</td>
                    <td><code>gs fs write /$USER/app/README.md --text "hello"</code></td>
                  </tr>
                  <tr>
                    <td>Create a checkpoint</td>
                    <td><code>gs fs snapshot -m "checkpoint"</code></td>
                  </tr>
                  <tr>
                    <td>Inspect changes</td>
                    <td><code>gs fs diff &lt;snapshot-or-commit&gt;</code></td>
                  </tr>
                  <tr>
                    <td>Upload a directory tree</td>
                    <td><code>gs fs upload ./site /$USER/site</code></td>
                  </tr>
                  <tr>
                    <td>Sync a directory in one command</td>
                    <td><code>gs fs sync --direction push ./site /$USER/site</code></td>
                  </tr>
                  <tr>
                    <td>Batch several mutations</td>
                    <td><code>gs fs batch -f ops.jsonl</code></td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>

          <section id="repo-bindings" className="docs-section">
            <Badge variant="secondary" className="eyebrow">Repo bindings</Badge>
            <h2>Bind a GitHub repo to one directory in your home slice</h2>
            <p>
              Use <code>gs repo</code> when one absolute path in your home slice should track an upstream repository.
              Import a repo into a directory, pull future remote updates into that bound path, and optionally push your
              edits back upstream later.
            </p>
            <pre className="code-block">
              <code>{`gs repo import https://github.com/org/repo.git /$USER/vendor/repo --push-enabled
gs repo status /$USER/vendor/repo
gs repo pull /$USER/vendor/repo
gs repo push /$USER/vendor/repo --message "sync upstream fixes"
gs repo unlink /$USER/vendor/repo`}</code>
            </pre>
            <ul className="docs-bullet-list">
              <li><code>gs repo import</code> snapshots the remote repo into one bound directory and records the binding.</li>
              <li><code>gs repo pull</code> refreshes the bound directory from upstream and records a normal home-slice commit.</li>
              <li><code>gs repo push</code> exports the bound directory back to the remote repo when push is enabled.</li>
              <li>The Settings page lists your current bindings so you can see path, branch, push mode, and last sync state.</li>
            </ul>
          </section>

          <section id="custom-slices" className="docs-section">
            <Badge variant="secondary" className="eyebrow">Custom slices</Badge>
            <h2>Check out a focused slice instead of dragging a whole tree everywhere</h2>
            <p>
              A custom slice is the local-work path. Create one around the folder or surface you care about, then
              check it out. The client asks for manifests first and downloads only blocks missing from local cache, so
              repeat checkouts stay fast.
            </p>
            <div className="docs-step-list">
              <article className="docs-step">
                <span>01</span>
                <div>
                  <h3>Create a slice around a bounded task</h3>
                  <p>Use a focused name and folder scope so the slice maps cleanly to the work you plan to publish.</p>
                </div>
              </article>
              <article className="docs-step">
                <span>02</span>
                <div>
                  <h3>Check it out locally</h3>
                  <p>Git Slice reconstructs the worktree from manifests plus cached and downloaded blocks.</p>
                </div>
              </article>
              <article className="docs-step">
                <span>03</span>
                <div>
                  <h3>Sync and publish when ready</h3>
                  <p>Use <code>gs slice sync</code> to refresh the worktree, <code>gs slice publish</code> to create or update the tracked changeset, and <code>gs changeset show</code> to inspect it.</p>
                </div>
              </article>
            </div>
            <pre className="code-block">
              <code>{`gs slice list
gs slice create ui-refresh apps/web
mkdir ui-refresh && cd ui-refresh
gs slice checkout <slice-id-or-slug>
gs slice tree
gs slice sync
gs slice publish --message "refresh settings page" --files src/routes/settings.tsx
gs changeset show`}</code>
            </pre>
          </section>

          <section id="changesets" className="docs-section">
            <Badge variant="secondary" className="eyebrow">Changesets</Badge>
            <h2>Publish local work through explicit merge steps</h2>
            <p>
              Changesets are the publish unit for checked-out slices. Create one from the files you changed, review it,
              and merge it back into the published tree. That keeps local experimentation separate from the shared state
              until you intentionally publish.
            </p>
            <pre className="code-block">
              <code>{`$EDITOR src/routes/settings.tsx
gs slice publish --message "refresh settings page" --files src/routes/settings.tsx
gs changeset show
gs changeset list --status merged`}</code>
            </pre>
            <p className="docs-note">
              Remote `gs fs` mutations also end up on the same publish model. They create slice history immediately, and
              publication flows through the same merge logic instead of a separate ad hoc sync path.
            </p>
          </section>

          <section id="local-cache" className="docs-section">
            <Badge variant="secondary" className="eyebrow">Local cache</Badge>
            <h2>Track checked-out slices globally and clean local cache state</h2>
            <p>
              Git Slice now keeps a global local registry of checked-out slices and their paths under your
              <code>~/.gitslice</code> state. That makes it possible to answer two practical questions quickly: which
              slices are checked out on this machine, and how much cached object data is still taking space.
            </p>
            <div className="docs-code-grid">
              <article className="docs-code-card">
                <div className="docs-code-card-head">
                  <h3>Inspect global checkout state</h3>
                  <p>See every tracked checkout path and the slice it belongs to.</p>
                </div>
                <pre className="code-block">
                  <code>{`gs slice checkouts
gs slice checkouts --slice home.$USER`}</code>
                </pre>
              </article>

              <article className="docs-code-card">
                <div className="docs-code-card-head">
                  <h3>Inspect and clean cache state</h3>
                  <p>Measure local cache usage, prune stale checkout records, or clear cached objects.</p>
                </div>
                <pre className="code-block">
                  <code>{`gs cache stats --checkouts
gs cache prune
gs cache clear --objects`}</code>
                </pre>
              </article>
            </div>
            <ul className="docs-bullet-list">
              <li><code>gs slice checkouts</code> reports how many checkouts exist globally and where they live.</li>
              <li><code>gs cache stats</code> shows cached object count, cached bytes, tracked checkouts, and stale records.</li>
              <li><code>gs cache prune</code> removes registry entries for deleted or invalid local worktrees.</li>
              <li><code>gs cache clear --objects</code> wipes cached objects so you can reclaim disk when needed.</li>
              <li><code>gs doctor</code> checks auth, current slice binding, global state, cache stats, and checkout health in one command.</li>
            </ul>
          </section>

          <section id="web-app" className="docs-section">
            <Badge variant="secondary" className="eyebrow">Web app</Badge>
            <h2>Use the browser when you want visibility, not just commands</h2>
            <ul className="docs-bullet-list">
              <li>The repo browser shows slices, folders, file previews, and commit history.</li>
              <li>Diff pages let you inspect commit patches and changeset patches in the browser.</li>
              <li>The web app defaults signed-in users to their home slice and keeps custom slices available for inspection.</li>
              <li>The docs page you are reading is part of the same app, so docs and product stay in one surface.</li>
            </ul>
          </section>

          <section id="auth" className="docs-section">
            <Badge variant="secondary" className="eyebrow">Auth</Badge>
            <h2>Authenticate once, then use the same identity everywhere</h2>
            <ul className="docs-bullet-list">
              <li>Use <code>gs login</code> to start the OAuth device flow for the CLI.</li>
              <li>The web app uses cookie-backed session auth.</li>
              <li>Your account owns a home slice, which is why `gs fs` can work from absolute paths immediately.</li>
            </ul>
          </section>

          <section id="faq" className="docs-section">
            <Badge variant="secondary" className="eyebrow">FAQ</Badge>
            <h2>Common questions</h2>
            <div className="docs-faq-list">
              {FAQS.map((item) => (
                <article key={item.question} className="docs-faq-card">
                  <h3>{item.question}</h3>
                  <p>{item.answer}</p>
                </article>
              ))}
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}
