// ---------------------------------------------------------------------------
// Overview / Landing Page Component
// ---------------------------------------------------------------------------

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
      <section className="hero hero--landing-redesign">
        <div className="hero-content">
          <p className="eyebrow">Built for modern agent workflows</p>
          <h1>Ship faster with API-first slices for every change.</h1>
          <p className="lede">
            Keep code, environment context, and agent actions aligned so teams move from idea to merged PR without branch maze overhead.
          </p>
          <div className="cta-row">
            <button type="button" className="primary" onClick={onBrowseRepo}>
              Explore repo browser
            </button>
            <a className="secondary-link" href="https://github.com/agenttools-dev/gitslice" target="_blank" rel="noreferrer">
              View on GitHub
            </a>
          </div>
        </div>

        <div className="hero-panel">
          <div className="hero-card hero-card--api">
            <p className="eyebrow">Agent-ready core</p>
            <h2>One platform for code + environment + automation</h2>
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
          </div>
        </div>
      </section>

      <section className="section">
        <div className="section-header">
          <h2>Why teams switch to Gitslice</h2>
          <p>Designed for human + agent collaboration without process overhead.</p>
        </div>
        <div className="benefit-grid">
          {BENEFITS.map((benefit) => (
            <article key={benefit.title} className="benefit-card">
              <h3>{benefit.title}</h3>
              <p>{benefit.detail}</p>
            </article>
          ))}
        </div>
      </section>

      <section className="section quickstart">
        <div className="section-header">
          <p className="eyebrow">Workflow</p>
          <h2>Simple flow, no branch maze</h2>
          <p>Run the same clean sequence in local development, CI, or managed agent sessions.</p>
        </div>
        <div className="workflow-grid">
          {WORKFLOW.map((item, index) => (
            <div key={item.title} className="workflow-card">
              <div className="step-number">{index + 1}</div>
              <h3>{item.title}</h3>
              <p>{item.copy}</p>
              <pre className="code-block">
                <code>{item.command}</code>
              </pre>
            </div>
          ))}
        </div>
      </section>
    </>
  );
}
