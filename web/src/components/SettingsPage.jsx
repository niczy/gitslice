export default function SettingsPage({ username, onOpenProfile }) {
  return (
    <section className="section" data-testid="settings-page">
      <div className="section-header">
        <p className="eyebrow">Account</p>
        <h2>Settings</h2>
        <p>Manage account-level preferences and organization details.</p>
      </div>

      {!username && <div className="panel-error">You need to log in before account settings are available.</div>}
      {username && (
        <>
          <div className="panel-empty" data-testid="settings-empty-state">
            Advanced settings are not configured yet for this account.
          </div>
          <div className="auth-actions" style={{ marginTop: '16px' }}>
            <button type="button" className="ghost" onClick={onOpenProfile}>Open Profile Details</button>
          </div>
        </>
      )}
    </section>
  );
}
