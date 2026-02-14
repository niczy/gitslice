// ---------------------------------------------------------------------------
// Overview / Landing Page Component
// ---------------------------------------------------------------------------

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

export default function OverviewPage({ onBrowseRepo }) {
  return (
    <>
      <section className="hero">
        <div className="hero-content">
          <p className="eyebrow">Introducing Git Slice</p>
          <h1>Slice-based workflows for shipping more confidently.</h1>
          <p className="lede">
            Git Slice lets teams carve out focused slices of work, run them end-to-end, and merge back with clarity. Each slice is
            stored in the slice service, so teams can standardize how a given area of the repo is scoped and tested.
          </p>
          <div className="cta-row">
            <button type="button" className="primary" onClick={onBrowseRepo}>
              Open repo browser
            </button>
            <a className="ghost" href="mailto:team@gitslice.dev">
              Contact the team
            </a>
          </div>
        </div>
        <div className="hero-panel">
          <div className="hero-card">
            <p className="eyebrow">Slice-first development</p>
            <h2>Run isolated slices from idea to production</h2>
            <p>
              Define a slice around a task, pull the dependencies you need, and keep every change traceable. Git Slice keeps
              delivery focused so teams can move without long-lived branches.
            </p>
          </div>
        </div>
      </section>

      <section id="overview" className="section">
        <div className="section-header">
          <h2>How slices keep changes focused</h2>
          <p>
            A slice captures only the files and services you specify, plus any required dependencies. That means slimmer clones,
            deterministic test runs, and a clean diff that can be merged without dragging unrelated changes along for the ride.
          </p>
        </div>
        <div className="steps">
          <div className="step">
            <div className="step-number">1</div>
            <div>
              <h3>Carve out the slice</h3>
              <p>Define the slice in the service with the immutable set of files, directories, and services required for the task.</p>
            </div>
          </div>
          <div className="step">
            <div className="step-number">2</div>
            <div>
              <h3>Iterate quickly</h3>
              <p>Use the CLI to check out the slice by its slice ID and run targeted tests.</p>
            </div>
          </div>
          <div className="step">
            <div className="step-number">3</div>
            <div>
              <h3>Merge with confidence</h3>
              <p>Every slice ships with reproducible logs, checks, and diffs so merging back is predictable and low-risk.</p>
            </div>
          </div>
        </div>
      </section>

      <section className="section quickstart">
        <div className="section-header">
          <p className="eyebrow">Quick start</p>
          <h2>Go from repo to slice in minutes</h2>
          <p>Use the CLI to check out a slice using its slice ID.</p>
        </div>
        <div className="quickstart-grid">
          <div className="quickstart-step">
            <h3>1. Create a slice</h3>
            <pre className="code-block">
              <code>gs fork auth-refresh ./services/auth --parent root_slice</code>
            </pre>
            <p>Store the slice definition in the service and reference it by a stable slice ID.</p>
          </div>
          <div className="quickstart-step">
            <h3>2. Check out the slice</h3>
            <pre className="code-block">
              <code>gs slice checkout auth-refresh</code>
            </pre>
            <p>Use the slice ID to pull just the required scope into a local workspace.</p>
          </div>
          <div className="quickstart-step">
            <h3>3. Validate the slice</h3>
            <pre className="code-block">
              <code>gs slice checkout auth-refresh --commit HEAD</code>
            </pre>
            <p>Pin a commit when needed so the slice scope is reproducible for reviewers and CI.</p>
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
            <div key={feature.title} className="feature">
              <h3>{feature.title}</h3>
              <p>{feature.description}</p>
            </div>
          ))}
        </div>
      </section>

      <section className="section cta">
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
