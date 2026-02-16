import { useState } from 'react';
import { currentUsername } from '../utils/api.js';

// ---------------------------------------------------------------------------
// Login Page Component
// ---------------------------------------------------------------------------

export default function LoginPage({ onLogin, onOAuthLogin, onLoggedIn, onCancel }) {
  const [value, setValue] = useState(() => currentUsername());
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  return (
    <section className="section auth-page" data-testid="login-page">
      <div className="section-header">
        <p className="eyebrow">Accounts</p>
        <h2>Sign in</h2>
        <p>Use your provider account to continue, or use a username for local/dev workflows.</p>
      </div>

      <div className="auth-layout">
        <div className="auth-card auth-card--oauth">
          <h3 className="auth-card-title">Continue with OAuth</h3>
          <p className="auth-card-subtitle">Best for normal usage across devices.</p>
          <div className="auth-provider-list">
            <button type="button" className="auth-provider auth-provider--google" onClick={() => onOAuthLogin?.('google')}>
              <span className="auth-provider-logo" aria-hidden="true">G</span>
              <span>Continue with Google</span>
            </button>
            <button type="button" className="auth-provider auth-provider--github" onClick={() => onOAuthLogin?.('github')}>
              <span className="auth-provider-logo" aria-hidden="true">GH</span>
              <span>Continue with GitHub</span>
            </button>
          </div>
        </div>

        <form
          className="auth-card auth-card--username"
          onSubmit={async (e) => {
            e.preventDefault();
            setError('');
            setLoading(true);
            try {
              await onLogin(value);
              onLoggedIn?.();
            } catch {
              setError('Invalid username. Use 3-32 chars: letters, numbers, "_" or "-", starting with a letter/number.');
            } finally {
              setLoading(false);
            }
          }}
        >
          <h3 className="auth-card-title">Username sign-in</h3>
          <p className="auth-card-subtitle">Fallback path for local testing and CLI-aligned flows.</p>

          <label className="field">
            <span className="field-label">Username</span>
            <input
              type="text"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder="e.g. nic"
              autoFocus
              spellCheck={false}
              autoComplete="off"
            />
          </label>

          {error && <div className="panel-error">{error}</div>}

          <div className="auth-actions">
            <button type="submit" className="primary" disabled={loading}>
              {loading ? 'Logging in…' : 'Login with username'}
            </button>
            <button type="button" className="ghost" onClick={onCancel}>
              Cancel
            </button>
          </div>
        </form>
      </div>
    </section>
  );
}
