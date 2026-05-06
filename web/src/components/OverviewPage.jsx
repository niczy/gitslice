import { Button } from './ui/button.jsx';
import { Badge } from './ui/badge.jsx';

const HERO_PROOF = [
  {
    label: 'Custom slices',
    value: 'Create a focused work surface instead of dragging an entire checkout into every task.',
  },
  {
    label: 'Quick checkout',
    value: 'Plain checkout skips git by default and downloads only the blocks your machine is missing.',
  },
  {
    label: 'Cloud edit',
    value: 'Use `gs fs` for direct remote edits when a full local tree would be slower than the fix.',
  },
];

const CHECKOUT_STEPS = [
  {
    step: '01',
    title: 'Create the slice you actually want',
    copy: 'Branch from a folder, repo surface, or focused task boundary instead of cloning everything by default.',
  },
  {
    step: '02',
    title: 'Check it out fast',
    copy: 'Checkout stays cheap because the client reuses cached objects and only downloads missing blocks.',
  },
  {
    step: '03',
    title: 'Merge with changesets',
    copy: 'Do the local work in your editor, then publish back through the same explicit review and merge path.',
  },
];

const CHECKOUT_FACTS = [
  'Fast no-git checkout by default',
  'Custom slice per feature or folder',
  'No local git mode to manage',
];

const CLOUD_EDIT_FACTS = [
  'Same home slice and commit history',
  'Remote reads, writes, snapshots, and diffs',
  'Good for hotfixes, notes, and tiny edits',
];

export default function OverviewPage({ onBrowseRepo, onOpenDocs }) {
  return (
    <>
      <section className="hero hero--landing-redesign landing-hero bg-gradient-to-br from-secondary/60 via-background to-card">
        <div className="hero-content landing-hero-copy py-8">
          <Badge variant="secondary" className="eyebrow border border-border/60">
            Quick custom slice checkouts
          </Badge>
          <h1>Check out a custom slice in seconds.</h1>
          <p className="lede">
            Create a focused slice, pull only the missing blocks, and materialize the tree immediately.
            Plain checkout now covers local status, diff, restore, sync, and publish on its own. There is no separate local
            git mode to manage. When the task is smaller than a local checkout,
            edit the same versioned files directly with <code>gs fs</code>.
          </p>
          <div className="cta-row flex flex-wrap gap-3">
            <Button type="button" onClick={onBrowseRepo}>
              Open slices
            </Button>
            <Button type="button" variant="outline" onClick={onOpenDocs}>
              Read docs
            </Button>
          </div>
          <dl className="landing-proof-strip">
            {HERO_PROOF.map((point) => (
              <div key={point.label} className="landing-proof-item">
                <dt>{point.label}</dt>
                <dd>{point.value}</dd>
              </div>
            ))}
          </dl>
        </div>

        <div className="landing-hero-stack">
          <div className="landing-command-ribbon" aria-hidden="true">
            <span>create a slice</span>
            <span>checkout fast</span>
            <span>merge with changesets</span>
          </div>

          <div className="hero-card hero-card--api landing-terminal landing-terminal--checkout border-border/70 bg-card/95">
            <div className="landing-terminal-header">
              <Badge variant="outline" className="w-fit">Quick checkout</Badge>
              <p>Focused local work without the full-download penalty</p>
            </div>
            <pre className="code-block">
              <code>{`gs slice create ui-refresh apps/web
mkdir ui-refresh && cd ui-refresh
gs slice checkout <slice-id-or-slug>`}</code>
            </pre>
          </div>

          <div className="landing-inline-card">
            <div className="landing-inline-card-copy">
              <Badge variant="outline" className="w-fit">Cloud edit</Badge>
              <h3>Skip the checkout for tiny changes</h3>
              <p>Use the same versioned filesystem directly when the fix is faster than opening a worktree.</p>
            </div>
            <pre className="code-block">
              <code>{`gs fs write /$USER/app/NOTICE.txt --text "hotfix shipped remotely"
gs fs snapshot -m "patch notice"`}</code>
            </pre>
          </div>
        </div>
      </section>

      <section className="section landing-story">
        <div className="section-header space-y-2">
          <Badge variant="secondary" className="eyebrow">Custom slice loop</Badge>
          <h2>Make local work the main path when the task deserves a real checkout.</h2>
          <p>
            The product pitch is simple: carve out the exact slice you want, check it out fast, and merge back through
            changesets. Cloud edit is still there, but it is the lightweight side path instead of the whole story.
          </p>
        </div>

        <div className="landing-story-grid">
          <div className="landing-story-rail">
            {CHECKOUT_STEPS.map((item) => (
              <article key={item.title} className="landing-story-step">
                <div className="landing-story-step-number">{item.step}</div>
                <div className="landing-story-step-copy">
                  <h3>{item.title}</h3>
                  <p>{item.copy}</p>
                </div>
              </article>
            ))}
          </div>

          <div className="landing-story-code">
            <div className="landing-story-code-head">
              <Badge variant="outline" className="w-fit">Full path</Badge>
              <p>From custom slice creation to no-git publish</p>
            </div>
            <pre className="code-block">
              <code>{`gs slice create ui-refresh apps/web
mkdir ui-refresh && cd ui-refresh
gs slice checkout <slice-id-or-slug>
$EDITOR src/routes/settings.tsx
gs slice diff
gs slice publish --message "refresh settings page" --files src/routes/settings.tsx`}</code>
            </pre>
            <ul className="landing-inline-facts">
              {CHECKOUT_FACTS.map((fact) => (
                <li key={fact}>{fact}</li>
              ))}
            </ul>
          </div>
        </div>
      </section>

      <section className="section landing-cloud-section">
        <div className="landing-cloud-grid">
          <div className="landing-cloud-copy">
            <Badge variant="secondary" className="eyebrow">Cloud edit when local is overkill</Badge>
            <h2>Still versioned. Still slice-backed. Just lighter weight.</h2>
            <p>
              Not every change needs a worktree. For direct file edits, snapshots, and quick reads, <code>gs fs</code>
              keeps you in the same slice model without the checkout step.
            </p>
            <ul className="landing-inline-facts">
              {CLOUD_EDIT_FACTS.map((fact) => (
                <li key={fact}>{fact}</li>
              ))}
            </ul>
          </div>

          <div className="hero-card hero-card--api landing-terminal border-border/70 bg-card/95">
            <div className="landing-terminal-header">
              <Badge variant="outline" className="w-fit">Remote path</Badge>
              <p>Use the cloud filesystem directly</p>
            </div>
            <pre className="code-block">
              <code>{`gs fs mkdir /$USER/notes
gs fs write /$USER/notes/todo.md --text "ship the patch"
gs fs cat /$USER/notes/todo.md
gs fs snapshot -m "notes update"`}</code>
            </pre>
          </div>
        </div>
      </section>

      <section className="landing-cta-band">
        <div>
          <p className="landing-cta-kicker">Start with the right surface.</p>
          <h2>Create a custom slice. Check it out fast. Use cloud edit for the rest.</h2>
        </div>
        <Button type="button" onClick={onBrowseRepo}>
          Open slices
        </Button>
      </section>
    </>
  );
}
