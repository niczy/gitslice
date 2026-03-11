// ---------------------------------------------------------------------------
// Overview / Landing Page Component
// ---------------------------------------------------------------------------
import { Button } from './ui/button.jsx';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card.jsx';
import { Badge } from './ui/badge.jsx';

const BENEFITS = [
  {
    title: 'Remote filesystem, plain CLI',
    detail: 'Use absolute paths with `gs fs` so reading, writing, and diffing files feels direct instead of branch-heavy.',
  },
  {
    title: 'Simple by default',
    detail: 'Replace fragile branch choreography with slice IDs that stay readable across local, CI, and review.',
  },
  {
    title: 'Clear review surface',
    detail: 'Keep commit history, diffs, and slice ownership readable so changes stay easy to review and merge.',
  },
];

const WORKFLOW = [
  {
    title: 'Create a home path',
    copy: 'Log in once, then create a directory and write a file in your remote home slice.',
    command: `gs login
gs fs mkdir /$USER/demo
gs fs write /$USER/demo/README.md --from ./README.md`,
  },
  {
    title: 'Inspect and snapshot',
    copy: 'Read the remote file back, list the directory, and capture a checkpoint before you publish anything.',
    command: `gs fs cat /$USER/demo/README.md
gs fs ls /$USER/demo
gs fs snapshot -m "demo ready"`,
  },
  {
    title: 'Check out locally',
    copy: 'When you want a full working tree, check the slice out into an empty directory and use normal Git commands.',
    command: `mkdir checkout-demo && cd checkout-demo
gs slice checkout <slice-id>
git status`,
  },
];

export default function OverviewPage({ onBrowseRepo }) {
  return (
    <>
      <section className="hero hero--landing-redesign bg-gradient-to-br from-secondary/60 via-background to-card">
        <div className="hero-content py-8">
          <Badge variant="secondary" className="eyebrow border border-border/60">Built for modern delivery workflows</Badge>
          <h1>Ship reliable software faster with API-first slices.</h1>
          <p className="lede">
            Keep files, snapshots, and review flow aligned from first write to merge.
          </p>
          <div className="cta-row flex flex-wrap gap-3">
            <Button type="button" onClick={onBrowseRepo}>
              Explore repo browser
            </Button>
            <Button asChild variant="outline">
              <a className="ghost" href="https://github.com/agenttools-dev/gitslice" target="_blank" rel="noreferrer">
                Learn more on GitHub
              </a>
            </Button>
          </div>
        </div>

        <div className="hero-panel">
          <Card className="hero-card hero-card--api border-border/70 bg-card/95">
            <CardHeader>
              <Badge variant="outline" className="w-fit">FS CLI Quickstart</Badge>
              <CardTitle className="text-xl">Try the remote filesystem in under a minute</CardTitle>
            </CardHeader>
            <CardContent>
            <pre className="code-block">
              <code>{`gs login
gs fs mkdir /$USER/demo
gs fs write /$USER/demo/hello.txt --text "hello from gitslice"
gs fs cat /$USER/demo/hello.txt
gs fs snapshot -m "first remote edit"`}</code>
            </pre>
            </CardContent>
          </Card>
        </div>
      </section>

      <section className="section space-y-6">
        <div className="section-header space-y-2">
          <Badge variant="secondary" className="eyebrow">Why Gitslice</Badge>
          <h2>Built for clarity across code and review workflows</h2>
          <p>Designed for collaborative delivery without process overhead.</p>
        </div>
        <div className="benefit-grid grid gap-4 md:grid-cols-3">
          {BENEFITS.map((benefit) => (
            <Card key={benefit.title} className="benefit-card border-border/70">
              <CardHeader className="space-y-2 pb-3">
                <CardTitle className="text-lg">{benefit.title}</CardTitle>
              </CardHeader>
              <CardContent>
                <CardDescription className="text-sm text-foreground/85">{benefit.detail}</CardDescription>
              </CardContent>
            </Card>
          ))}
        </div>
      </section>

      <section className="section quickstart space-y-6">
        <div className="section-header">
          <Badge variant="secondary" className="eyebrow">Tutorial</Badge>
          <h2>Learn `gs fs` in three steps</h2>
          <p>Start with remote file operations, capture a snapshot, then move into a git-compatible local checkout when you need a full worktree.</p>
        </div>
        <div className="workflow-grid grid gap-4 md:grid-cols-3">
          {WORKFLOW.map((item, index) => (
            <Card key={item.title} className="workflow-card border-border/70 bg-card/95">
              <CardHeader className="space-y-2 pb-3">
                <div className="step-number">{index + 1}</div>
                <CardTitle className="text-lg">{item.title}</CardTitle>
                <CardDescription className="text-sm text-foreground/85">{item.copy}</CardDescription>
              </CardHeader>
              <CardContent>
              <pre className="code-block">
                <code>{item.command}</code>
              </pre>
              </CardContent>
            </Card>
          ))}
        </div>
      </section>
    </>
  );
}
