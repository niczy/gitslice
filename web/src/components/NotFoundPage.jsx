export default function NotFoundPage({ unknownPath, onGoHome }) {
  return (
    <section className="section" data-testid="not-found-page">
      <div className="section-header">
        <p className="eyebrow">Navigation</p>
        <h2>Page not found</h2>
        <p>No page exists for <code>/{unknownPath || ''}</code>.</p>
      </div>
      <div className="panel-error">Choose a menu destination to continue.</div>
      <div className="auth-actions" style={{ marginTop: '16px' }}>
        <button type="button" className="primary" onClick={onGoHome}>Go to Get Started</button>
      </div>
    </section>
  );
}
