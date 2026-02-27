// ---------------------------------------------------------------------------
// Overview / Landing Page Component
// ---------------------------------------------------------------------------
import { Button } from './ui/button.jsx';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card.jsx';
import { Badge } from './ui/badge.jsx';

const BENEFITS = [
  {
    title: 'API-first for agents',
    detail: 'Every action starts from typed APIs and gRPC definitions so your AI workflow is scriptable from day one.',
  },
  {
    title: 'Simple by default',
    detail: 'Replace fragile branch choreography with slice IDs that stay readable across local, CI, and review.',
  },
  {
    title: 'Dev environment + codebase together',
    detail: 'Attach environment context directly to slice work so agents run against the same code and setup you do.',
  },
];

const WORKFLOW = [
  {
    title: 'Define scope',
    copy: 'Create a slice for one outcome with clear ownership and minimal blast radius.',
    command: 'gs fork checkout-api ./internal/httpapi --parent root_slice',
  },
  {
    title: 'Sync environment',
    copy: 'Bind the same environment assumptions used by your dev tools and agents.',
    command: 'gs env attach checkout-api --from .devcontainer',
  },
  {
    title: 'Ship through API',
    copy: 'Trigger agent runs, validate diffs, and merge cleanly without branch-heavy handoffs.',
    command: 'gs agent run checkout-api --provider codex --prompt "open PR"',
  },
];

export default function OverviewPage({ onBrowseRepo }) {
  return (
    <>
      <section className="hero hero--landing-redesign bg-gradient-to-br from-secondary/60 via-background to-card">
        <div className="hero-content py-8">
          <Badge variant="secondary" className="eyebrow border border-border/60">Built for modern agent workflows</Badge>
          <h1>Ship reliable software faster with API-first slices.</h1>
          <p className="lede">
            Keep code, environment context, and agent runs aligned from first commit to merge.
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
              <Badge variant="outline" className="w-fit">Agent-ready core</Badge>
              <CardTitle className="text-xl">One platform for code + environment + automation</CardTitle>
            </CardHeader>
            <CardContent>
            <pre className="code-block">
              <code>{`service AgentSessionService {
  rpc CreateAgentSession(CreateAgentSessionRequest)
      returns (CreateAgentSessionResponse) {
    option (google.api.http) = {
      post: "/v1/agent-sessions"
      body: "*"
    };
  }
}`}</code>
            </pre>
            </CardContent>
          </Card>
        </div>
      </section>

      <section className="section space-y-6">
        <div className="section-header space-y-2">
          <Badge variant="secondary" className="eyebrow">Why Gitslice</Badge>
          <h2>Built for clarity across human and agent workflows</h2>
          <p>Designed for human + agent collaboration without process overhead.</p>
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
          <Badge variant="secondary" className="eyebrow">Workflow</Badge>
          <h2>Simple flow, no branch maze</h2>
          <p>Run the same clean sequence in local development, CI, or managed agent sessions.</p>
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
