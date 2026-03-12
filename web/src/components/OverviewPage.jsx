// ---------------------------------------------------------------------------
// Overview / Landing Page Component
// ---------------------------------------------------------------------------
import { Button } from './ui/button.jsx';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card.jsx';
import { Badge } from './ui/badge.jsx';

const WORKFLOWS = [
  {
    title: 'Edit the cloud filesystem directly',
    copy: 'Start with absolute paths and a versioned home slice. Read, write, list, and snapshot without cloning first.',
    command: `gs login
gs fs mkdir /$USER/app
gs fs write /$USER/app/README.md --text "hello from gitslice"
gs fs cat /$USER/app/README.md
gs fs snapshot -m "first remote edit"`,
  },
  {
    title: 'Check out locally and merge with changesets',
    copy: 'When you want a full worktree, check out the slice, edit locally, and merge back through a changeset review flow.',
    command: `mkdir checkout-demo && cd checkout-demo
gs slice checkout <slice-id>
git status
$EDITOR README.md
gs changeset create --message "update readme" --files README.md
gs changeset merge <changeset-id>`,
  },
];

export default function OverviewPage({ onBrowseRepo }) {
  return (
    <>
      <section className="hero hero--landing-redesign bg-gradient-to-br from-secondary/60 via-background to-card">
        <div className="hero-content py-8">
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
        </div>

        <div className="hero-panel">
          <Card className="hero-card hero-card--api border-border/70 bg-card/95">
            <CardHeader>
              <Badge variant="outline" className="w-fit">Quickstart</Badge>
              <CardTitle className="text-xl">Write a remote file in under a minute</CardTitle>
            </CardHeader>
            <CardContent>
            <pre className="code-block">
              <code>{`gs login
gs fs mkdir /$USER/app
gs fs write /$USER/app/hello.txt --text "hello from gitslice"
gs fs cat /$USER/app/hello.txt
gs fs snapshot -m "first remote edit"`}</code>
            </pre>
            </CardContent>
          </Card>
        </div>
      </section>

      <section className="section space-y-6">
        <div className="section-header space-y-2">
          <Badge variant="secondary" className="eyebrow">Two ways to work</Badge>
          <h2>Stay remote by default. Drop local when you need it.</h2>
          <p>Start with direct filesystem commands. Switch to a checked-out slice only when a full local worktree is the faster tool.</p>
        </div>
        <div className="workflow-grid grid gap-4 md:grid-cols-2">
          {WORKFLOWS.map((item) => (
            <Card key={item.title} className="workflow-card border-border/70 bg-card/95">
              <CardHeader className="space-y-2 pb-3">
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
