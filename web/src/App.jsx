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

function App() {
  return (
    <div className="page">
      <header className="hero">
        <div className="eyebrow">Introducing Git Slice</div>
        <h1>Slice-based workflows for shipping more confidently.</h1>
        <p className="lede">
          Git Slice lets teams carve out focused slices of work, run them end-to-end, and merge back with clarity. No more
          sprawling branches—just fast, predictable delivery.
        </p>
        <div className="cta-row">
          <a className="primary" href="#features">
            Explore the workflow
          </a>
          <a className="ghost" href="#overview">
            See how it works
          </a>
        </div>
      </header>

      <section id="overview" className="section card">
        <div className="section-header">
          <p className="eyebrow">Slice-first development</p>
          <h2>Run isolated slices from idea to production</h2>
          <p>
            Start by defining a slice around a task. Git Slice provisions the context, fetches dependencies, and wires up tooling
            so you can develop, test, and preview changes without disturbing the rest of the repo. When you are ready, merge the
            slice with full traceability.
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

      <footer className="footer">
        <p>Git Slice • Slice smart. Ship faster.</p>
      </footer>
    </div>
  );
}

export default App;
