// ---------------------------------------------------------------------------
// Overview / Landing Page Component
// ---------------------------------------------------------------------------

export default function OverviewPage({ onBrowseRepo }) {
  return (
    <>
      <section className="hero">
        <div className="hero-content">
          <p className="eyebrow">Git Slice</p>
          <h1>Scope changes by slice, not by long-lived branch.</h1>
          <p className="lede">
            Gitslice gives you a CLI + API service to define a focused slice, check it out by ID, and ship smaller, reviewable
            diffs with less repo noise.
          </p>
          <div className="cta-row">
            <button type="button" className="primary" onClick={onBrowseRepo}>
              Open repo browser
            </button>
            <a className="ghost" href="https://github.com/agenttools-dev/gitslice" target="_blank" rel="noreferrer">
              View on GitHub
            </a>
          </div>
        </div>
        <div className="hero-panel">
          <div className="hero-card">
            <p className="eyebrow">Core value</p>
            <h2>Smaller scope, reproducible checkout, safer merge</h2>
            <p>Use slice IDs to recreate work context in local dev, CI, and release validation.</p>
          </div>
        </div>
      </section>

      <section id="overview" className="section">
        <div className="section-header">
          <h2>Simple workflow</h2>
          <p>Define, checkout, and validate slices with deterministic commands.</p>
        </div>
        <div className="steps">
          <div className="step">
            <div className="step-number">1</div>
            <div>
              <h3>Create</h3>
              <p>Fork from root or parent slice with explicit scope.</p>
            </div>
          </div>
          <div className="step">
            <div className="step-number">2</div>
            <div>
              <h3>Checkout</h3>
              <p>Pull only that slice using its stable ID.</p>
            </div>
          </div>
          <div className="step">
            <div className="step-number">3</div>
            <div>
              <h3>Ship</h3>
              <p>Review clean diffs and merge with lower risk.</p>
            </div>
          </div>
        </div>
      </section>

      <section className="section quickstart">
        <div className="section-header">
          <p className="eyebrow">Quick start</p>
          <h2>Three commands</h2>
        </div>
        <div className="quickstart-grid">
          <div className="quickstart-step">
            <pre className="code-block">
              <code>gs fork auth-fix ./services/auth --parent root_slice</code>
            </pre>
          </div>
          <div className="quickstart-step">
            <pre className="code-block">
              <code>gs slice checkout auth-fix</code>
            </pre>
          </div>
          <div className="quickstart-step">
            <pre className="code-block">
              <code>gs slice checkout auth-fix --commit HEAD</code>
            </pre>
          </div>
        </div>
      </section>
    </>
  );
}
