export default function RouteAccessState({ state, onGoToLogin }) {
  if (state === 'unauthorized') {
    return (
      <section className="section" data-testid="route-access-denied">
        <div className="section-header">
          <p className="eyebrow">Restricted</p>
          <h2>Access denied</h2>
          <p>Your account does not have permission to view this route.</p>
        </div>
      </section>
    );
  }

  return (
    <section className="section" data-testid="route-auth-required">
      <div className="section-header">
        <p className="eyebrow">Authentication</p>
        <h2>Please sign in</h2>
        <p>You need to log in before viewing this route.</p>
      </div>
      <div className="auth-actions" style={{ marginTop: '16px' }}>
        <button type="button" className="primary" onClick={onGoToLogin}>
          Go to login
        </button>
      </div>
    </section>
  );
}
